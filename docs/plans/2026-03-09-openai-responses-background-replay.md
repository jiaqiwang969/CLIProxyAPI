# OpenAI Responses Background Replay Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add official-style stored replay for `GET /v1/responses/{response_id}?stream=true` while keeping websocket `background=true` explicitly unsupported.

**Architecture:** Extend the stored OpenAI response layer with an in-memory replay event log. Background HTTP responses append lifecycle events as they transition through `queued`, `in_progress`, and terminal states. Retrieval with `stream=true` replays those stored events, and older stored responses without event history fall back to a synthesized minimal lifecycle stream.

**Tech Stack:** Go, Gin, existing OpenAI handlers, in-memory response store, focused Go unit tests

---

### Task 1: Characterize Stored Replay Retrieval

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_retrieval_test.go`

**Step 1: Write the failing test**

Add tests that prove:

- `GET /v1/responses/{id}?stream=true` returns SSE for a stored background response
- the replay includes `response.created`, `response.queued`, `response.in_progress`, a terminal event, and `[DONE]`
- a legacy stored response with no event history still returns a synthesized replay

Suggested test names:

```go
func TestResponses_GetResponseStreamReplaysStoredBackgroundLifecycle(t *testing.T) {}

func TestResponses_GetResponseStreamSynthesizesLifecycleForLegacyStoredResponse(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestResponses_GetResponseStream(ReplaysStoredBackgroundLifecycle|SynthesizesLifecycleForLegacyStoredResponse)' -count=1
```

Expected: FAIL because `stream=true` is currently rejected on `GET /v1/responses/{response_id}`.

**Step 3: Write minimal implementation**

Do not add more than required for replay:

- allow `stream=true` on stored retrieval
- return SSE headers
- replay stored or synthesized events

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestResponses_GetResponseStream(ReplaysStoredBackgroundLifecycle|SynthesizesLifecycleForLegacyStoredResponse)' -count=1
```

Expected: PASS.

### Task 2: Add Replay Event Storage to the Response Store

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_store.go`

**Step 1: Write the failing test**

Add store-focused tests or extend retrieval tests so they prove:

- background response creation stores lifecycle replay events
- cancel appends a `response.cancelled` terminal event and `response.done`
- legacy `Store(...)` callers continue to work without replay data

Suggested test names:

```go
func TestStoredOpenAIResponseStore_AppendsLifecycleReplayEvents(t *testing.T) {}

func TestStoredOpenAIResponseStore_CancelledResponsesReplayTerminalEvents(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestStoredOpenAIResponseStore_(AppendsLifecycleReplayEvents|CancelledResponsesReplayTerminalEvents)' -count=1
```

Expected: FAIL because the current store only keeps the latest response body and input items.

**Step 3: Write minimal implementation**

Extend the store to:

- keep a compact event list per response ID
- append lifecycle payloads in order
- load replay events without mutating them
- preserve existing response body and input item behavior

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestStoredOpenAIResponseStore_(AppendsLifecycleReplayEvents|CancelledResponsesReplayTerminalEvents)' -count=1
```

Expected: PASS.

### Task 3: Wire Background HTTP Lifecycle Events

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_background_test.go`

**Step 1: Write the failing test**

Extend background handler tests so they prove:

- create appends `created` and `queued`
- execution start appends `in_progress`
- completion appends the correct terminal event and `done`
- cancel appends `cancelled` and `done`

Suggested test names:

```go
func TestResponses_BackgroundCreateStoresReplayEvents(t *testing.T) {}

func TestResponses_BackgroundCancelStoresCancelledReplay(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestResponses_Background(CreateStoresReplayEvents|CancelStoresCancelledReplay)' -count=1
```

Expected: FAIL because the background lifecycle currently stores only the latest response object.

**Step 3: Write minimal implementation**

Update background handler execution to append replay events at each lifecycle transition and to preserve event ordering.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestResponses_Background(CreateStoresReplayEvents|CancelStoresCancelledReplay)' -count=1
```

Expected: PASS.

### Task 4: Keep Websocket Background Explicitly Rejected

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_websocket_test.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_websocket.go`

**Step 1: Write the failing test**

Tighten websocket validation tests so they prove the rejection message remains explicit even after HTTP replay support is added.

Suggested test name:

```go
func TestNormalizeResponsesWebsocketRequestCreateRejectsBackgroundTrueWithReplayGuidance(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestNormalizeResponsesWebsocketRequestCreateRejectsBackgroundTrueWithReplayGuidance' -count=1
```

Expected: FAIL if the message is too vague or regresses.

**Step 3: Write minimal implementation**

Keep websocket background validation explicit and stable.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'TestNormalizeResponsesWebsocketRequestCreateRejectsBackgroundTrueWithReplayGuidance' -count=1
```

Expected: PASS.

### Task 5: Full Verification

**Files:**
- No new code files required

**Step 1: Run focused package tests**

Run:

```bash
go test ./sdk/api/handlers/openai -count=1
go test ./sdk/api/handlers/... -count=1
go test ./internal/api/... -count=1
```

Expected: PASS.

**Step 2: Spot-check local behavior**

Run:

```bash
curl -N -H "Authorization: Bearer <local-key>" "http://127.0.0.1:8317/v1/responses/<response_id>?stream=true"
```

Expected:

- SSE replay events in lifecycle order
- terminal event followed by `data: [DONE]`

**Step 3: Record remaining intentional gaps**

Document in the final summary that this pass still does not add websocket `background=true` or event pagination with `starting_after`.
