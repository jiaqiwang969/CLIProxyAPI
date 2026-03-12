# Auggie Remote Tool Bridge Alignment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Execute official OpenAI `responses` built-in `web_search` requests against Auggie's private remote-tool runtime so non-stream `/v1/responses` returns the final assistant answer instead of rejecting or exposing a manual tool loop.

**Architecture:** Keep OpenAI as the public contract and Auggie as the upstream capability source. Extend the Auggie executor to detect supported built-in tools, discover the matching Auggie remote tool via `/agents/list-remote-tools`, execute it via `/agents/run-remote-tool`, and continue the same Auggie turn with `nodes[].tool_result_node` until the final assistant message is produced.

**Tech Stack:** Go, `httptest`, `gjson`, `sjson`, existing executor/translator pipeline in `apps/server-go`

---

### Task 1: Lock the target behavior with failing tests

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request_test.go`

**Step 1: Write the failing executor test**

Add a non-stream `/v1/responses` test that simulates this full Auggie loop:

```go
func TestAuggieResponses_WebSearchCompletesViaRemoteToolBridge(t *testing.T) {
    // 1. first /chat-stream returns tool_use name=web-search
    // 2. executor calls /agents/list-remote-tools
    // 3. executor calls /agents/run-remote-tool
    // 4. second /chat-stream contains nodes[].tool_result_node
    // 5. final /v1/responses payload contains assistant text and no surfaced function_call item
}
```

Also replace the current "reject web_search" expectations so:

- `custom` still fails
- `web_search` is now accepted on `/v1/responses`
- `/v1/chat/completions` may stay rejected for this slice if the executor path is still unsupported there

**Step 2: Run the failing executor test**

Run:

```bash
go test ./internal/runtime/executor -run 'TestAuggieResponses_WebSearchCompletesViaRemoteToolBridge|TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedToolType' -count=1
```

Expected:

- new `web_search` bridge test fails because executor never calls `/agents/list-remote-tools` or `/agents/run-remote-tool`
- rejection test fails if it still expects `web_search` to be unsupported

**Step 3: Write the failing translator test**

Add or extend a translator test proving OpenAI built-in `web_search` becomes Auggie `tool_definitions[].name = "web-search"` and keeps built-in configuration needed for the executor.

```go
func TestConvertOpenAIRequestToAuggie_MapsBuiltInWebSearchTool(t *testing.T) {
    raw := []byte(`{"tools":[{"type":"web_search","search_context_size":"high"}]}`)
    out := ConvertOpenAIRequestToAuggie("gpt-5", raw, false)
    // assert tool_definitions[0].name == "web-search"
}
```

**Step 4: Run the failing translator test**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions -run TestConvertOpenAIRequestToAuggie_MapsBuiltInWebSearchTool -count=1
```

Expected:

- FAIL because built-in tools are currently ignored

**Step 5: Commit checkpoint**

Do not commit yet unless the user asks, but keep this as the checkpoint boundary.

### Task 2: Implement minimal Auggie remote-tool support for built-in `web_search`

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor.go`
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request.go`

**Step 1: Allow supported built-in tool types**

Update request validation so Auggie `responses` accepts `tools[].type == "web_search"` while still rejecting unsupported custom non-function types.

Minimal target:

```go
switch toolType {
case "", "function", "web_search":
    continue
default:
    return statusErr{...}
}
```

If needed, scope this allowance so it is only used by the `responses` path, not by the chat-completions path.

**Step 2: Translate OpenAI built-in tool definitions into Auggie tool definitions**

Teach `buildAuggieToolDefinitions(...)` to emit Auggie-native tool names for supported OpenAI built-ins:

- `web_search` -> `web-search`

Keep the implementation minimal. The tool definition only needs the fields required by Auggie's `/chat-stream` request.

**Step 3: Add Auggie remote-tool transport helpers**

Inside `auggie_executor.go`, add small request/response structs and helper methods for:

- `POST /agents/list-remote-tools`
- `POST /agents/run-remote-tool`

Helpers should:

- reuse Auggie auth and tenant URL normalization
- send JSON
- return explicit errors for non-2xx responses
- parse `remote_tool_id`, `tool_definition.name`, `tool_output`, `is_error`

**Step 4: Implement the internal tool loop for non-stream `responses`**

In `executeOpenAIResponses(...)`, replace the single-pass flow with:

1. build initial translated Auggie request
2. execute `/chat-stream` and collect non-stream OpenAI payload
3. if finish reason is not `tool_calls`, translate to final `/v1/responses` and return
4. if finish reason is `tool_calls` and the tool is supported built-in `web_search`:
   - resolve remote tool id by Auggie tool name `web-search`
   - execute `/agents/run-remote-tool`
   - append `nodes[].tool_result_node` to the translated Auggie request
   - preserve `conversation_id` and `turn_id`
   - rerun `/chat-stream`
5. stop when final assistant text is returned or when an unsupported tool call is encountered

Keep the first implementation single-tool and non-stream only. If multiple tool calls appear, fail explicitly unless all are supported by the same minimal loop.

**Step 5: Preserve official surface semantics**

The returned `/v1/responses` body should contain:

- final assistant message output
- request fields copied through by the existing OpenAI responses translator
- no requirement for the client to send `function_call_output` for built-in `web_search`

For this slice, it is acceptable if the output does not yet emit full official `web_search_call` items, as long as the tool executes internally and the final response is correct.

### Task 3: Verify and tighten the behavior

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go` if assertion cleanup is needed
- Modify: `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request_test.go` if assertion cleanup is needed

**Step 1: Run targeted package tests**

Run:

```bash
go test ./internal/translator/auggie/openai/chat-completions ./internal/runtime/executor -count=1
```

Expected:

- PASS for the updated translator and executor packages

**Step 2: Run the higher-level built-in tool translation tests**

Run:

```bash
go test ./test -run 'TestBuiltIn|TestOpenAIWebsearch' -count=1
```

Expected:

- PASS

**Step 3: Review remaining known gaps**

Document or note any still-open items discovered during implementation:

- stream path still lacks the same internal built-in tool loop
- `/v1/chat/completions` Auggie path may still reject or surface built-in tool loops
- final `/v1/responses` output may still represent Auggie-origin tool calls as `function_call` instead of official `web_search_call`

**Step 4: Commit checkpoint**

If the user asks for a commit later:

```bash
git add apps/server-go/internal/runtime/executor/auggie_executor.go \
        apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go \
        apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request.go \
        apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request_test.go \
        docs/plans/2026-03-10-auggie-remote-tool-bridge-alignment.md
git commit -m "feat: bridge auggie web search tools for responses"
```

Plan complete and saved to `docs/plans/2026-03-10-auggie-remote-tool-bridge-alignment.md`. Execution will continue in this session using TDD.
