# Auggie Native Provider Design

## Summary

This document defines a native `auggie` provider for CLIProxyAPI that behaves like
existing first-party OAuth providers such as `antigravity`, while speaking Auggie's
private upstream protocol directly.

The v1 scope is intentionally limited to:

- native `auggie login`
- auth/session revalidation
- `/v1/models`
- streaming chat execution

The design does not treat Auggie as a generic OpenAI-compatible upstream. It adds a
provider-specific authenticator, executor, and model registration path.

## Goals

- Add a first-class `auggie` provider that feels native inside CLIProxyAPI.
- Reproduce Auggie's real login flow instead of importing raw session files as the
  primary user workflow.
- Expose Auggie's real model list through `/v1/models`.
- Route OpenAI-compatible chat requests to Auggie's private `chat-stream` endpoint.
- Reuse CLIProxyAPI's existing auth selection, retry, model registry, and failure
  state machinery.

## Non-Goals

- Full Auggie IDE or agent-runtime parity.
- Workspace indexing, blob upload, remote agents, canvas, or rules/skills parity.
- Silent OAuth refresh-token rotation unless later reverse-engineering proves it.
- Full tool-call parity in v1. The first shipping target is stable text streaming.

## Reverse-Engineering Inputs

The design is based on the installed Auggie CLI bundle:

- bundle path: `/opt/homebrew/lib/node_modules/@augmentcode/auggie/augment.mjs`
- saved session path: `~/.augment/session.json`
- saved session shape: `accessToken`, `tenantURL`, `scopes`

Relevant private behavior extracted from the bundle:

- login uses OAuth authorize URL generation with PKCE
- localhost client ID: `auggie-cli`
- manual JSON-paste client ID: `auggie-cli-json-paste`
- callback expects `code`, `state`, `tenant_url`
- token exchange uses `grant_type=authorization_code`
- model discovery calls `get-models`
- chat execution calls `chat-stream`
- request auth uses `Authorization: Bearer <access token>`
- upstream base URL is tenant-scoped via `tenantURL`

Observed request and response shapes used by this design:

- `get-models` returns `default_model` and `models[]`
- each model contains at least `name`
- models may also include `internal_name`, `completion_timeout_ms`,
  `suggested_prefix_char_count`, and `suggested_suffix_char_count`
- `chat-stream` emits line-delimited JSON chunks parsed by Auggie as `BackChatResult`
- observed `BackChatResult` fields include `text`, `unknown_blob_names`,
  `checkpoint_not_found`, `workspace_file_chunks`, `nodes`, and optional `stop_reason`

## Architecture

Add a native provider path parallel to `antigravity`:

- `sdk/auth/auggie.go`
  - provider authenticator
  - native login flow
  - auth record creation
- `internal/runtime/executor/auggie_executor.go`
  - upstream HTTP execution against Auggie private endpoints
  - model discovery
  - stream execution
  - revalidation behavior
- `internal/cmd/auggie_login.go`
  - CLI entrypoint wiring
- `sdk/cliproxy/service.go`
  - register executor
  - register models for `auggie` auths
  - tenant-level backfill support
- `sdk/translator`
  - add `FormatAuggie`
  - add request/response translators required by v1

This is intentionally not implemented via `openai_compat_executor.go`. Auggie's
actual upstream API is private and structurally different from OpenAI.

## Auth Record

The persisted auth record uses provider `auggie` and stores runtime metadata in the
normal CLIProxyAPI auth file.

Required metadata fields:

- `type=auggie`
- `access_token`
- `tenant_url`
- `scopes`
- `client_id`
- `login_mode`
- `last_refresh`

Recommended additional metadata:

- `default_model`

The display label should default to the tenant host or `auggie`. V1 should not rely
on a separate user-info call because a stable public user identity endpoint has not
been confirmed from the bundle.

Transient OAuth values such as `state`, `code_verifier`, and callback session state
must remain in-memory only for the active login flow.

## Login Flow

CLIProxyAPI should expose a native `auggie-login` command and reproduce Auggie's real
OAuth behavior.

### Localhost mode

For local environments:

- bind `127.0.0.1` on a random local port
- use redirect URI `http://127.0.0.1:<port>/callback`
- open the authorize URL in a browser when available
- receive `code`, `state`, and `tenant_url`
- validate `state`
- validate `tenant_url` host suffix ends with `.augmentcode.com`

### Manual JSON-paste mode

For remote environments:

- use the manual login client ID
- instruct the user to complete auth in a browser
- accept pasted JSON payload containing `code`, `state`, and `tenant_url`
- perform the same `state` and tenant validation as localhost mode

### Token exchange

Exchange the code using the private token request:

- `grant_type=authorization_code`
- `client_id`
- `code_verifier`
- `redirect_uri`
- `code`

Persist only the access token session material that Auggie itself is known to persist.

## Refresh and Revalidation

Current reverse-engineering evidence does not show CLI-side persistence of a
`refresh_token`. Therefore v1 must not pretend it supports silent OAuth refresh
rotation.

V1 refresh semantics:

- `RefreshLead()` should return `nil`
- executor `Refresh(ctx, auth)` means revalidate/resync, not refresh-token exchange
- first try current auth metadata
- optionally resync from `~/.augment/session.json` as an internal fallback source
- if revalidation still fails with `401`, mark the auth invalid and require re-login

For `get-models` and `chat-stream`, one `401` before useful output may trigger a
single revalidate-and-retry attempt.

## Model Discovery

`registerModelsForAuth()` should add an `auggie` branch in `sdk/cliproxy/service.go`
that calls `FetchAuggieModels(ctx, auth, cfg)`.

`FetchAuggieModels` should:

- send `POST <tenant_url>/get-models`
- use `Authorization: Bearer <access token>`
- use `{}` as the v1 request body
- on missing/expired token, run the revalidation path

Model registration rules:

- save `default_model` into auth metadata
- expose `models[].name` as the public model ID
- preserve Auggie's real model names exactly
- map each item into CLIProxyAPI `ModelInfo`
- populate at least:
  - `ID`
  - `Object="model"`
  - `Created`
  - `OwnedBy="auggie"`
  - `Type="auggie"`
  - `DisplayName`

V1 does not need to expose executor hints like `internal_name` or completion timeout
through `/v1/models`.

### Tenant-level cache and backfill

Model cache and backfill should be tenant-scoped, not provider-global.

- cache key: normalized tenant host from `tenant_url`
- store the latest non-empty model list per tenant
- if one auth in a tenant fetches models successfully, backfill same-tenant `auggie`
  auths that currently have no registered models
- never backfill across tenants

This differs from `antigravity`, where the current backfill is provider-wide.

### Config compatibility

Support existing registry shaping mechanisms:

- add `auggie` to `OAuthModelAliasChannel(...)`
- allow `oauth-model-alias` to rename visible Auggie models
- allow `oauthExcludedModels` and per-auth `excluded_models`

## Chat Execution

Add a native `AuggieExecutor.ExecuteStream(...)`.

### Request path

The executor should:

- accept OpenAI-compatible downstream requests
- translate them into a minimal Auggie `chat-stream` request
- send `POST <tenant_url>/chat-stream`
- authenticate with bearer token

V1 request translation should be intentionally small:

- map requested model directly
- map the last user message into `message`
- map prior messages into simplified `chat_history`
- map OpenAI `tools` into `tool_definitions`
- set `mode="CHAT"`

V1 should not attempt full Auggie IDE/runtime parity:

- no blob upload or workspace sync
- no prefix/suffix editing context
- no remote agent orchestration
- no canvas, memory, rules, or skills parity

### Response path

Auggie streaming is line-delimited JSON, not OpenAI SSE. The executor should:

- read one JSON line at a time
- parse each line into the observed Auggie stream result shape
- convert output into OpenAI-style SSE chunks for downstream clients

V1 response policy is text-first:

- emit text deltas from incremental `text`
- terminate using `stop_reason` when present
- emit `[DONE]` at the end

The `nodes` field should be treated as a reserved future expansion point. V1 should
not promise full tool-call translation until the node schema is fully extracted and
stable.

### Retry boundaries

The executor must preserve CLIProxyAPI's existing stream safety behavior:

- retry is allowed before first payload byte
- once a payload byte has been emitted, do not switch auths or retry another upstream
- a `401` before first byte may trigger one revalidate-and-retry path
- mid-stream failures terminate the stream with an upstream error

## Error Handling

The executor should reuse existing `statusErr` semantics so the core auth conductor
can manage auth and model state.

Required mapping:

- `400`: invalid request, no provider fallback
- `401`: unauthorized, revalidate once then fail
- `402` or `403`: payment/tenant blocked
- `429`: quota/rate limit, preserve retry-after if available
- `408`, `5xx`, and transport failures: transient upstream errors
- malformed JSON stream line: `502 bad gateway`

Sensitive data must never be logged or surfaced in final error messages:

- access tokens
- raw pasted login JSON
- full tenant URL query strings

## Testing Strategy

Tests should be local and deterministic, using `httptest` where possible.

### Auth tests

- localhost callback success
- localhost callback invalid state
- localhost callback invalid tenant host
- manual JSON-paste success
- manual JSON-paste malformed payload
- refresh/revalidate behavior without refresh token

### Model tests

- `get-models` mapping into `ModelInfo`
- `default_model` persistence
- tenant-level model cache behavior
- same-tenant backfill
- cross-tenant no-backfill
- `excluded_models`
- `oauth-model-alias`

### Streaming tests

- line-delimited Auggie stream converts to OpenAI SSE
- text delta emission
- finish reason handling
- pre-first-byte `401` retry
- post-first-byte failure does not retry
- malformed upstream line returns `502`

### Service and wiring tests

- `auggie` executor registration
- auth add/update triggers model registration
- auth removal clears registry state
- `/v1/models` exposes registered Auggie models

## Implementation Notes

Recommended file additions and updates:

- add `sdk/auth/auggie.go`
- add `internal/cmd/auggie_login.go`
- add `internal/runtime/executor/auggie_executor.go`
- extend `sdk/auth/refresh_registry.go`
- extend `sdk/cliproxy/service.go`
- extend `sdk/translator/formats.go`
- add translator implementation for `openai <-> auggie`
- wire new CLI flag in `cmd/server/main.go`

## Open Questions Deferred From V1

- whether Auggie exposes a stable user-info endpoint suitable for auth labeling
- whether `nodes` can be safely translated into OpenAI tool calls
- whether there is a hidden refresh-token or session-rotation path worth supporting
- whether conversation/workspace identifiers should be persisted for sticky sessions

## Recommended Next Step

Create a detailed implementation plan and execute the provider in phases:

1. auth and login
2. model discovery
3. stream execution
4. error handling and tests
