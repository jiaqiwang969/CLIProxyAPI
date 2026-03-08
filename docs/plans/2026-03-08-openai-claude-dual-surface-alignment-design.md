# OpenAI Claude Dual-Surface Alignment Design

Date: 2026-03-08

## Goal

Align the proxy's OpenAI-compatible and Anthropic-compatible surfaces with the observed external baseline at `https://code.ppchat.vip` while preserving internal provider separation between `antigravity` and `auggie`.

This design targets the currently exposed public endpoints:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `POST /v1/messages/count_tokens`

The required outcome is dual-surface Claude support:

- Claude models must continue to work on Anthropic-native `/v1/messages`
- Claude models must also work on OpenAI-style `/v1/chat/completions` and `/v1/responses`

GPT models remain OpenAI-surface models and must not be forced into Anthropic-native semantics.

## Problem Summary

The current `/v1` layer is only partially aligned with the target contract.

Observed gaps:

1. `GET /v1/models` is not a single stable catalog. It currently routes between OpenAI and Claude model listings based on `User-Agent`.
2. Claude support is split across handler families instead of being treated as one model family that can appear on multiple API surfaces.
3. Surface-specific error contracts are not enforced consistently at the handler boundary.
4. Surface-level model validation is too loose in some paths and too implicit in others.
5. The external baseline already behaves like a mixed compatibility gateway:
   - Claude models are accepted on `/v1/chat/completions`
   - Claude models are accepted on `/v1/messages`
   - GPT models are accepted on `/v1/chat/completions`
   - GPT models are accepted on `/v1/responses`

That means the proxy should not treat OpenAI and Claude compatibility as mutually exclusive catalogs. It should treat them as multiple official-looking surfaces over one internally routed model registry.

## Chosen Strategy

Use a strict surface-boundary design:

- internal provider identities stay separate
- public API surfaces own validation and error semantics
- model exposure is unified where the external contract is unified
- translators and executors are reused unless a handler-layer fix is insufficient

This matches the successful Gemini approach:

- fix the public contract at the handler boundary
- keep internal execution routing mostly unchanged
- add focused characterization and regression tests before deeper refactors

## Public Contract Rules

The public `/v1` layer must obey these rules:

1. `GET /v1/models` returns one unified catalog for the default `/v1` surface.
2. The unified catalog may contain both GPT and Claude model IDs.
3. Claude models are valid on:
   - `/v1/messages`
   - `/v1/chat/completions`
   - `/v1/responses`
4. GPT models are valid on:
   - `/v1/chat/completions`
   - `/v1/responses`
5. GPT models are rejected on `/v1/messages`.
6. OpenAI surfaces always return OpenAI-style errors and streaming envelopes.
7. Anthropic surfaces always return Anthropic-style errors and streaming envelopes.
8. Internal provider names such as `antigravity` and `auggie` remain routing concerns, not public API identity.

## External Baseline

The external reference service at `https://code.ppchat.vip` currently demonstrates these behaviors:

- `GET /v1/models` returns a single combined list containing GPT and Claude model IDs
- `POST /v1/chat/completions` accepts `gpt-5.4`
- `POST /v1/chat/completions` accepts `claude-opus-4-6`
- `POST /v1/responses` accepts `gpt-5.4`
- `POST /v1/messages` accepts `claude-opus-4-6`

This external baseline is a compatibility target, not a reason to flatten internal provider design.

## Architecture

### 1. Unified `/v1/models` Catalog

Replace the current `User-Agent` split for `/v1/models` with a single surface-specific model listing contract.

The unified list should:

- start from runtime-available models
- preserve canonical Auggie IDs only
- include Claude IDs that are routable on `/v1/chat/completions` and `/v1/responses`
- keep public metadata minimal and OpenAI-compatible

This change belongs at the `/v1/models` handler boundary, not in executors.

### 2. Surface Capability Validation

Introduce explicit validation helpers for `/v1` surfaces:

- OpenAI surface helper:
  accepts GPT and Claude compatible IDs
- Anthropic surface helper:
  accepts Claude compatible IDs only

These helpers should execute before request dispatch so invalid models fail with the correct contract-specific error body instead of leaking downstream routing failures.

### 3. Provider Separation Without Public Leakage

Internal provider identities remain distinct:

- `antigravity-*`
- `auggie-*`

The registry and auth-manager pipeline continue to decide which backing provider actually serves a request.

The public handler should work with public model IDs such as:

- `gpt-5.4`
- `claude-opus-4-6`

Later, if dedicated provider-specific base URLs are introduced, those entry points can constrain routing to one provider without rewriting the core compatibility logic.

### 4. Surface-Specific Error Writers

Error normalization must be handler-owned.

Required split:

- `/v1/chat/completions` and `/v1/responses`
  return OpenAI-style JSON errors and OpenAI-style stream errors
- `/v1/messages` and `/v1/messages/count_tokens`
  return Anthropic-style JSON errors and Anthropic-style stream errors

This prevents cross-surface leakage such as OpenAI endpoints emitting Anthropic error bodies for Claude-backed failures.

### 5. Claude Multi-Surface Support

Claude models already have an Anthropic-native handler path and are already accepted by the external baseline on OpenAI-compatible endpoints.

The proxy should therefore formalize Claude as a dual-surface model family:

- same public model IDs
- different surface validation and error behavior
- same internal routing selection logic where possible

This is a compatibility concern, not a new provider family.

## Data Flow

### OpenAI Surface Request

1. client sends `/v1/chat/completions` or `/v1/responses`
2. handler validates model against OpenAI-surface capability rules
3. handler preserves OpenAI request/response contract
4. auth-manager resolves routing to `antigravity` or `auggie`
5. translator and executor perform provider-specific conversion
6. handler returns OpenAI-style success or error payload

### Anthropic Surface Request

1. client sends `/v1/messages`
2. handler validates model against Anthropic-surface capability rules
3. non-Claude IDs are rejected immediately with Anthropic-style error
4. auth-manager resolves routing to a Claude-capable backend
5. translator and executor perform provider-specific conversion
6. handler returns Anthropic-style success or error payload

## Error Handling

Handler-layer validation must become deterministic.

Examples:

- `gpt-5.4` on `/v1/messages`
  return Anthropic-style invalid request error
- unknown model on `/v1/chat/completions`
  return OpenAI-style invalid request or not-found style error according to current endpoint behavior
- Claude upstream failure while using `/v1/chat/completions`
  still return OpenAI-style error body

This is the main guardrail that keeps the compatibility layer coherent even when providers differ underneath.

## Testing Strategy

Testing should follow the same pattern that worked for Gemini.

### Characterization Tests

Capture current external baseline behavior for:

- `/v1/models`
- `/v1/chat/completions`
- `/v1/responses`
- `/v1/messages`

Use those results as the contract reference for local handler tests where practical.

### Handler Tests

Add focused tests for:

- unified `/v1/models` behavior
- OpenAI surface acceptance of Claude model IDs
- Anthropic surface rejection of GPT model IDs
- surface-specific error envelopes

### Live Regression

Run real curl checks for:

- `gpt-5.4` on `/v1/chat/completions`
- `claude-opus-4-6` on `/v1/chat/completions`
- `gpt-5.4` on `/v1/responses`
- `claude-opus-4-6` on `/v1/responses`
- `claude-opus-4-6` on `/v1/messages`
- `gpt-5.4` on `/v1/messages` rejection path

Both `antigravity` and `auggie` should remain routable behind the same public compatibility layer.

## Files Expected To Change

- `apps/server-go/internal/api/server.go`
- `apps/server-go/sdk/api/handlers/openai/openai_handlers.go`
- `apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go`
- `apps/server-go/sdk/api/handlers/claude/code_handlers.go`
- `apps/server-go/internal/registry/model_registry.go`
- `apps/server-go/sdk/api/handlers/openai/openai_models_test.go`
- new or expanded tests under:
  - `apps/server-go/sdk/api/handlers/openai/`
  - `apps/server-go/sdk/api/handlers/claude/`
  - possibly `apps/server-go/internal/api/`

## Non-Goals

This pass does not:

- collapse `antigravity` and `auggie` into one provider
- expose provider-specific public model prefixes on the default `/v1` compatibility surface
- make GPT models emulate Anthropic-native semantics on `/v1/messages`
- redesign executors before handler-level evidence proves that is necessary

## Success Criteria

The change is successful when all of the following are true:

1. `/v1/models` returns one stable combined compatibility catalog
2. Claude models work on `/v1/chat/completions`
3. Claude models work on `/v1/responses`
4. Claude models continue to work on `/v1/messages`
5. GPT models remain valid on OpenAI surfaces
6. GPT models are rejected on Anthropic-native `/v1/messages`
7. each surface emits its own official-style error contract
8. internal provider separation remains intact for future dedicated base URL routing
