# OpenAI Responses Background Replay Design

Date: 2026-03-09

## Goal

Align the proxy's stored `Responses` retrieval surface with the official OpenAI background workflow by adding replay for:

- `GET /v1/responses/{response_id}?stream=true`
- background `queued` / `in_progress` / terminal lifecycle visibility for stored responses

This design explicitly keeps websocket `response.create` with `background=true` out of scope for this pass.

## Current State

The proxy now supports non-streaming HTTP `background=true` on `POST /v1/responses`:

- create returns a stored `response` object with `status=queued`
- polling `GET /v1/responses/{id}` returns the latest stored object
- `POST /v1/responses/{id}/cancel` cancels active background work and returns `status=cancelled`

What is still missing is official-style replay of the response lifecycle. Today:

- `GET /v1/responses/{id}?stream=true` is rejected
- websocket `response.create` with `background=true` is rejected

That means the HTTP lifecycle is only partially aligned with the official OpenAI contract.

## Source Evidence

The local OpenAI SDK tarball in `openai-6.27.0.tgz` shows:

- `responses.retrieve(responseID, { stream: true })` is a supported client path
- the `Responses` type surface includes lifecycle events such as:
  - `response.queued`
  - `response.in_progress`
  - terminal events like `response.completed` and `response.incomplete`

This means replay is part of the official client contract, not an optional convenience.

## Alternatives Considered

### 1. Add HTTP replay only, keep websocket background rejected

Recommended.

Implement stored event replay for `GET /v1/responses/{id}?stream=true` while keeping websocket background explicitly unsupported.

Pros:

- smallest behavior slice that closes the current official SDK gap
- reuses the existing stored response model
- avoids mixing websocket session management with background persistence

Cons:

- websocket still remains intentionally behind the official surface

### 2. Add HTTP replay and websocket background together

Rejected for this pass.

Pros:

- broader parity in one shot

Cons:

- requires durable lifecycle events plus websocket request/session semantics at the same time
- increases implementation and verification surface significantly
- makes it harder to isolate regressions from the newly added HTTP background store

### 3. Fake replay from the final stored object only

Rejected.

This would emit a synthetic stream derived only from the final object. It would be fast to implement, but it loses the real lifecycle transitions that the proxy already knows when background execution is active. It would likely need to be rewritten once websocket or richer replay is added.

## Chosen Design

Implement event-backed HTTP replay for stored responses.

### Replay Model

Extend the stored response layer so each stored response can keep a compact in-memory event log for replay. The event log should store already-materialized OpenAI `Responses` SSE payloads, not abstract internal structs.

Required event coverage for this pass:

- `response.created`
- `response.queued`
- `response.in_progress`
- terminal lifecycle event:
  - `response.completed`
  - `response.incomplete`
  - `response.failed`
  - `response.cancelled`
- `response.done`

### Event Source Strategy

There are two event sources:

1. Background HTTP responses created by the proxy
   The proxy already owns the lifecycle transitions for these responses, so it should append event payloads at each transition.

2. Existing stored responses without event history
   For backward compatibility, `GET ...?stream=true` should synthesize a minimal replay from the stored final response when no event log exists.

This keeps old stored entries readable while allowing new entries to replay real lifecycle transitions.

### Retrieval Behavior

`GET /v1/responses/{id}?stream=true` should:

- return `200 OK`
- set SSE headers
- emit the stored replay events in order
- terminate with `data: [DONE]`

`GET /v1/responses/{id}` without `stream=true` remains unchanged and returns the latest stored response body.

### Validation Behavior

Change stored retrieval validation so:

- `stream=true` is allowed on `GET /v1/responses/{id}`
- `starting_after` can remain rejected for now
- `include_obfuscation` can remain rejected for now

This keeps the replay slice minimal and explicit.

### Websocket Behavior

Websocket `response.create` with `background=true` remains explicitly rejected in this pass.

Reason:

- websocket background needs a separate design for event fan-out and session recovery
- the current proxy does not persist live websocket subscribers against stored response IDs

### Error Handling

- Missing stored response ID continues to return the existing OpenAI-style `invalid_request_error`
- malformed stored event payloads should fall back to synthesized replay rather than crashing the request
- if a stored response has no event log and cannot be synthesized, return the existing stored response body on non-stream retrieval and keep stream retrieval as an internal server error

## Data Flow

### New background response

1. `POST /v1/responses` with `background=true`
2. proxy stores initial response body and appends:
   - `response.created`
   - `response.queued`
3. background executor starts and appends `response.in_progress`
4. terminal state is stored and corresponding terminal event is appended
5. `response.done` is appended
6. `GET /v1/responses/{id}?stream=true` replays those events

### Old stored response without event history

1. `GET /v1/responses/{id}?stream=true`
2. proxy detects missing event log
3. proxy synthesizes:
   - `response.created`
   - `response.in_progress`
   - terminal event inferred from stored status
   - `response.done`
4. synthesized events are streamed back to the client

## Files Expected To Change

- `apps/server-go/sdk/api/handlers/openai/openai_responses_store.go`
- `apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go`
- `apps/server-go/sdk/api/handlers/openai/openai_responses_retrieval_test.go`
- `apps/server-go/sdk/api/handlers/openai/openai_responses_background_test.go`
- `apps/server-go/sdk/api/handlers/openai/openai_responses_websocket_test.go`

## Testing Strategy

Use TDD with focused handler tests.

Add failing tests for:

- `GET /v1/responses/{id}?stream=true` replaying stored background lifecycle events
- `GET /v1/responses/{id}?stream=true` replaying a synthesized lifecycle for legacy stored responses
- cancelled background response replay
- websocket `background=true` still returning an explicit validation error

Verification should include:

- `go test ./sdk/api/handlers/openai -count=1`
- `go test ./sdk/api/handlers/... -count=1`
- `go test ./internal/api/... -count=1`

## Non-Goals

This pass does not:

- add websocket `background=true`
- add `starting_after` event pagination for stored replay
- add durable cross-process replay persistence
- replay token-by-token content deltas from past executions
