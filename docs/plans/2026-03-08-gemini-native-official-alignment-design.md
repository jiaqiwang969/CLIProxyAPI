# Gemini Native Official Alignment Design

Date: 2026-03-08

## Goal

Restore the proxy's Gemini native `/v1beta` surface so that it behaves like Google's official Gemini API at the HTTP contract level while still routing internally to the currently available providers.

This design targets the Gemini native endpoints already exposed by the proxy:

- `GET /v1beta/models`
- `GET /v1beta/models/{model}`
- `POST /v1beta/models/{model}:generateContent`
- `POST /v1beta/models/{model}:streamGenerateContent`
- `POST /v1beta/models/{model}:countTokens`

## Problem Summary

The current `/v1beta` layer regressed in two ways:

1. `GET /v1beta/models` is backed by the unified runtime registry and therefore leaks non-Gemini models such as GPT and Claude entries.
2. Gemini execution still uses private upstream model names such as `gemini-3.1-pro-high`, so responses can expose private model identity instead of the public official Gemini model name.

This breaks the user's requirement that the Gemini surface should align with the official Google API rather than act as a generic mixed-provider catalog.

## Chosen Strategy

Use a strict public/private split:

- public Gemini contract uses official Gemini model IDs only
- internal execution can still use private provider-specific model IDs
- alias resolution remains an execution concern, not a public API concern

This keeps the existing executor and auth-manager pipeline intact while restoring the public contract at the Gemini handler boundary.

## Public Contract Rules

The public Gemini API must obey these rules:

1. `/v1beta/models` returns only official Gemini-native models.
2. `/v1beta/models/{model}` accepts only official Gemini model IDs.
3. Gemini actions accept only official Gemini model IDs.
4. responses must not leak internal private model names
5. official Gemini fields such as `usageMetadata`, `thoughtSignature`, and `thinkingConfig` remain intact

For this pass, "official model IDs" means the static Gemini catalog defined in the server's Gemini model definitions. Runtime support is still filtered by actual provider availability.

## Architecture

The implementation introduces a dedicated Gemini public-catalog layer between the handler and the runtime registry.

### 1. Official Gemini Catalog View

Add a helper that derives the public Gemini catalog from:

- the static Gemini model definitions in `internal/registry`
- the current runtime availability from the global registry

The catalog view will:

- start from static official Gemini definitions
- keep only models that are currently routable through the active providers
- return Gemini-native metadata only

This prevents the handler from reusing the generic `GetAvailableModels("gemini")` path that currently converts every registered model into Gemini-shaped output.

### 2. Strict Gemini Model Validation

Before dispatching a Gemini request, the handler will validate that the requested model is:

- an official Gemini public model ID
- currently available in the public catalog

If either check fails, the handler returns a Gemini-style not-found error instead of falling through to provider resolution.

### 3. Public-to-Private Execution Mapping

Execution keeps using the existing auth-manager path:

- handler passes the public official model ID
- auth manager resolves aliases per provider
- provider executor receives the private upstream model ID when needed

This preserves the current working antigravity alias routing such as:

- public `gemini-3.1-pro-preview`
- internal `gemini-3.1-pro-high`

### 4. Response Model Rewriting

After execution returns, Gemini-native responses will be rewritten so the public response model identity matches the requested official model ID.

This applies to:

- non-streaming `generateContent`
- streaming `streamGenerateContent`

The rewrite is limited to Gemini-native response fields such as:

- `modelVersion`

No other Gemini-native payload structure should be altered unless required to remove private model leakage.

## Data Flow

For a native Gemini request:

1. client sends official model ID
2. Gemini handler validates against the public official catalog
3. handler executes with the public model ID
4. auth manager maps the public model ID to the private upstream model ID
5. executor calls the real provider
6. handler rewrites response model identity back to the public official model ID
7. client receives an official-looking Gemini-native response

## Error Handling

The handler must stop returning generic mixed-provider failures for invalid Gemini model names.

Required behavior:

- private model IDs such as `gemini-3.1-pro-high` are rejected at the public Gemini boundary
- unsupported official models are rejected at the public Gemini boundary
- only validated official models reach provider execution

This keeps error semantics close to the official Gemini API and avoids exposing internal alias rules.

## Testing Strategy

Testing focuses on contract restoration, not executor internals.

### Catalog Tests

Add unit tests for the public Gemini catalog helper:

- mixed registry entries do not appear in Gemini native model listings
- official Gemini aliases backed by active providers do appear
- unsupported official models do not appear

### Handler Tests

Add handler-level tests for:

- `GET /v1beta/models` returns only official Gemini entries
- `GET /v1beta/models/{model}` rejects private model IDs
- action endpoints reject private model IDs before execution
- response rewriting replaces private `modelVersion` with the requested public official model ID

### Regression Tests

Keep existing translator tests green and add focused tests around any new response rewrite helper.

## Files Expected To Change

- `apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go`
- `apps/server-go/internal/registry/...` for the public official Gemini catalog helper
- new Gemini handler tests under `apps/server-go/sdk/api/handlers/gemini/`
- possibly a small helper file for response rewriting if needed

## Non-Goals

This pass does not change:

- the generic `/v1` OpenAI-compatible surface
- provider executor internals unless a test proves they are required for native Gemini parity
- menubar or management UI behavior

## Success Criteria

The change is successful when all of the following are true:

1. local `/v1beta/models` no longer returns GPT, Claude, or other non-Gemini models
2. local Gemini native endpoints accept official Gemini model IDs and reject private ones
3. local Gemini native responses no longer expose private upstream model names
4. the existing alias-based routing to antigravity continues to work behind the scenes
