# Auggie OpenAI Responses Native Alignment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Align the Auggie-backed `/v1/chat/completions` and `/v1/responses` surfaces with the official OpenAI tool-calling and `Responses` contract, including `function_call_output`, `previous_response_id`, and reasoning output.

**Architecture:** Replace the lossy Auggie `chat_history` bridge for tool loops with native Auggie request semantics. Keep public OpenAI endpoints unchanged, upgrade the OpenAI-to-Auggie request translator to emit native tool-result nodes, add a small in-memory `response.id -> Auggie conversation state` store for `previous_response_id`, and extend Auggie response translation so reasoning survives into final OpenAI `Responses` output.

**Tech Stack:** Go, existing translator registry, Auggie executor, Go unit tests, focused local curl verification

---

### Task 1: Characterize and Fix OpenAI-to-Auggie Tool Result Request Translation

**Files:**
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request_test.go`
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request.go`

**Step 1: Write the failing test**

Add tests that prove `ConvertOpenAIRequestToAuggie` preserves native tool-loop semantics for OpenAI chat-completions input:

```go
func TestConvertOpenAIRequestToAuggie_PreservesToolResultNodes(t *testing.T) {}

func TestConvertOpenAIRequestToAuggie_KeepsPlainTurnsOnLegacyChatHistoryPath(t *testing.T) {}
```

The first test should include:

- assistant `tool_calls`
- tool message with matching `tool_call_id`
- expected Auggie `nodes.0.type == 1`
- expected `nodes.0.tool_result_node.tool_use_id`
- expected `nodes.0.tool_result_node.content`

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions -run 'TestConvertOpenAIRequestToAuggie_(PreservesToolResultNodes|KeepsPlainTurnsOnLegacyChatHistoryPath)' -count=1
```

Expected: FAIL because the current translator only emits `message`, `chat_history`, and `tool_definitions`.

**Step 3: Write minimal implementation**

Update `auggie_openai_request.go` so:

- plain text turns keep using the existing `message` plus `chat_history` representation
- tool result turns additionally emit native Auggie `nodes`
- assistant `tool_calls` are tracked by ID so later tool messages can map to `tool_result_node.tool_use_id`

Do not implement `previous_response_id` here.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions -run 'TestConvertOpenAIRequestToAuggie_(PreservesToolResultNodes|KeepsPlainTurnsOnLegacyChatHistoryPath)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request.go apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request_test.go
git commit -m "feat: preserve Auggie tool result nodes for OpenAI tool loops"
```

### Task 2: Surface Auggie Thinking in OpenAI Chat-Completions and Responses

**Files:**
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_response_test.go`
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_response.go`
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor.go`
- Modify: `apps/server-go/internal/translator/openai/openai/responses/openai_openai-responses_response.go`

**Step 1: Write the failing test**

Add tests that prove:

- Auggie `nodes[].thinking` becomes OpenAI `choices.0.delta.reasoning_content`
- aggregated non-stream OpenAI chat completion preserves reasoning content
- final OpenAI `/v1/responses` JSON body contains a `reasoning` output item

Suggested test names:

```go
func TestConvertAuggieResponseToOpenAI_EmitsReasoningContentFromThinkingNodes(t *testing.T) {}

func TestAuggieExecute_AggregatesReasoningIntoOpenAIResponsesResponse(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions ./internal/runtime/executor -run 'Test(ConvertAuggieResponseToOpenAI_EmitsReasoningContentFromThinkingNodes|AuggieExecute_AggregatesReasoningIntoOpenAIResponsesResponse)' -count=1
```

Expected: FAIL because `thinking` is currently ignored by the Auggie response translator and non-stream aggregation.

**Step 3: Write minimal implementation**

Update the translators so:

- streaming Auggie `thinking` becomes `reasoning_content`
- non-stream collection stores reasoning text in the final OpenAI chat completion message
- the OpenAI-to-Responses non-stream translator turns that reasoning text into a `reasoning` item

Keep tool call and usage handling intact.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions ./internal/runtime/executor -run 'Test(ConvertAuggieResponseToOpenAI_EmitsReasoningContentFromThinkingNodes|AuggieExecute_AggregatesReasoningIntoOpenAIResponsesResponse)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_response.go apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_response_test.go apps/server-go/internal/runtime/executor/auggie_executor.go apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go apps/server-go/internal/translator/openai/openai/responses/openai_openai-responses_response.go
git commit -m "feat: surface Auggie reasoning on OpenAI responses"
```

### Task 3: Add Auggie Response-State Mapping for `previous_response_id`

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor.go`

**Step 1: Write the failing test**

Add executor tests that prove:

- first-turn `/v1/responses` stores `response.id -> Auggie conversation_id + turn_id`
- second-turn `/v1/responses` with `previous_response_id` sends Auggie `conversation_id`, `turn_id`, and `nodes[].tool_result_node`
- missing mapping returns an OpenAI-style request error

Suggested test names:

```go
func TestAuggieResponses_UsesStoredConversationStateForPreviousResponseID(t *testing.T) {}

func TestAuggieResponses_ReturnsOpenAIErrorWhenPreviousResponseIDIsUnknown(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/runtime/executor -run 'TestAuggieResponses_(UsesStoredConversationStateForPreviousResponseID|ReturnsOpenAIErrorWhenPreviousResponseIDIsUnknown)' -count=1
```

Expected: FAIL because the current executor never maps `previous_response_id` to Auggie conversation state.

**Step 3: Write minimal implementation**

Update `auggie_executor.go` to:

- store Auggie `conversation_id` and `turn_id` when the first-turn `/v1/responses` call completes
- build a direct native Auggie request when `previous_response_id` is present
- emit an OpenAI-style invalid request error when the mapping is missing
- keep the existing response translation path after Auggie returns OpenAI chat-completions chunks

Use a small in-memory TTL store. Do not add durable storage.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/runtime/executor -run 'TestAuggieResponses_(UsesStoredConversationStateForPreviousResponseID|ReturnsOpenAIErrorWhenPreviousResponseIDIsUnknown)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/internal/runtime/executor/auggie_executor.go apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go
git commit -m "feat: support previous_response_id for Auggie responses"
```

### Task 4: Verify Full Inline and Incremental Responses Tool Loops End-to-End

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_websocket_test.go`

**Step 1: Write the failing test**

Add end-to-end style tests that prove:

- inline `message + function_call + function_call_output` no longer reissues the same tool call
- incremental `previous_response_id + function_call_output` reaches Auggie as a native tool result continuation

Suggested test names:

```go
func TestAuggieResponses_FullInlineToolLoopCompletes(t *testing.T) {}

func TestAuggieResponses_IncrementalToolLoopCompletes(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/runtime/executor ./sdk/api/handlers/openai -run 'TestAuggieResponses_(FullInlineToolLoopCompletes|IncrementalToolLoopCompletes)' -count=1
```

Expected: FAIL on the current bridge.

**Step 3: Write minimal implementation**

Only if prior tasks did not already make these tests pass:

- tighten request building for inline tool loops
- tighten response-state lookup and error propagation

Do not add new abstractions unless the tests force them.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/runtime/executor ./sdk/api/handlers/openai -run 'TestAuggieResponses_(FullInlineToolLoopCompletes|IncrementalToolLoopCompletes)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go apps/server-go/sdk/api/handlers/openai/openai_responses_websocket_test.go
git commit -m "test: verify Auggie responses tool-loop alignment"
```

### Task 5: Run Focused Verification and Live Diff Checks

**Files:**
- Modify: `docs/plans/2026-03-09-auggie-openai-responses-native-alignment-design.md`

**Step 1: Run focused Go tests**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions ./internal/runtime/executor ./internal/translator/openai/openai/responses ./sdk/api/handlers/openai -count=1
```

Expected: PASS.

**Step 2: Run local live checks**

Verify:

```bash
curl -sS http://localhost:8317/v1/chat/completions -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","messages":[{"role":"user","content":"天气如何"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]}'
curl -sS http://localhost:8317/v1/chat/completions -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","messages":[{"role":"user","content":"天气如何"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"上海\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"{\"temperature\":23}"}]}'
curl -sS http://localhost:8317/v1/responses -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"天气如何"}]}],"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]}'
```

Capture the first `response.id`, then verify:

```bash
curl -sS http://localhost:8317/v1/responses -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","previous_response_id":"<response-id>","input":[{"type":"function_call_output","call_id":"<call-id>","output":"{\"temperature\":23}"}]}'
```

Expected:

- second-turn chat-completions no longer forgets the tool result
- inline `/v1/responses` tool loop completes instead of repeating the tool call
- incremental `/v1/responses` continuation succeeds with `previous_response_id`

**Step 3: Compare against official contract and external baseline**

Compare local behavior against:

- official OpenAI docs for `Responses` and function calling
- `https://code.ppchat.vip`

Use the official docs as the final arbiter when there is a disagreement.

**Step 4: Record verification notes**

Update the design doc with any observed residual gaps if needed.

**Step 5: Commit**

```bash
git add docs/plans/2026-03-09-auggie-openai-responses-native-alignment-design.md
git commit -m "docs: record Auggie OpenAI responses alignment verification"
```
