# Auggie Runtime Capture Evidence

Date: 2026-03-10

## Goal

Replace black-box assumptions about the terminal `auggie` client with runtime-verified upstream evidence, with special focus on function/tool-calling behavior and whether `web-search` is a real upstream capability or only local reconstruction.

## Capture Method

- Local executable:
  - `/opt/homebrew/bin/auggie`
  - symlink target: `/opt/homebrew/lib/node_modules/@augmentcode/auggie/augment.mjs`
  - package version: `0.18.1`
- Runtime:
  - `node v25.6.1`
  - `NODE_USE_ENV_PROXY=1` works on this Node version
- MITM proxy:
  - `mitmdump 12.2.1`
  - custom addon: `scripts/auggie_capture_addon.py`
- Capture output directory:
  - `/tmp/auggie-capture`

Tested launch shape:

```bash
NODE_USE_ENV_PROXY=1 \
HTTP_PROXY=http://127.0.0.1:8081 \
HTTPS_PROXY=http://127.0.0.1:8081 \
NODE_EXTRA_CA_CERTS=$HOME/.mitmproxy/mitmproxy-ca-cert.pem \
auggie ...
```

Verified CLI commands used during capture:

- `auggie account status`
- `auggie -p -q --max-turns 1 "Reply with the single word OK."`
- `auggie -p -q --max-turns 5 "Use the web-search tool to search for 'OpenAI latest news' and reply with the top headline only."`

## Verified Hosts And Endpoints

All product endpoints below were observed against `https://d8.api.augmentcode.com`. Analytics traffic separately hit `https://api.segment.io/v1/batch`.

Observed endpoint counts from the final capture set:

| Path | Count | Verified role |
| --- | ---: | --- |
| `/get-models` | 4 | bootstrap models, feature flags, and tenant/user metadata |
| `/get-credit-info` | 1 | account and credit usage query |
| `/agents/list-remote-tools` | 18 | remote tool registry lookup |
| `/chat-stream` | 4 | primary model streaming endpoint |
| `/find-missing` | 9 | blob/memory preflight for workspace context |
| `/batch-upload` | 9 | blob upload for workspace context |
| `/agents/run-remote-tool` | 2 | remote tool execution |
| `/v1/batch` | 4 | Segment analytics, not model execution |

## Current Proxy Coverage Versus Runtime Evidence

The answer to "is Auggie upstream only `/get-models` and `/chat-stream`?" is now two-layered:

- for the current Go proxy executor path, no longer just those two:
  - `apps/server-go/internal/runtime/executor/auggie_executor.go` currently hard-codes:
    - `/get-models`
    - `/chat-stream`
    - `/agents/list-remote-tools`
    - `/agents/run-remote-tool`
- for the real upstream product surface used by the terminal `auggie` client, no:
  - runtime capture verified additional product endpoints beyond the two executor endpoints

This distinction matters because the proxy currently uses a narrower upstream slice than the native CLI.

| Layer | Verified endpoints | Meaning |
| --- | --- | --- |
| current proxy executor | `/get-models`, `/chat-stream`, `/agents/list-remote-tools`, `/agents/run-remote-tool` | the tenant product endpoints currently wired into live Auggie execution in this repo |
| current proxy auth flow | `https://auth.augmentcode.com/authorize`, tenant `/token` | login and token exchange, not inference or tool execution |
| native terminal runtime | `/agents/list-remote-tools`, `/agents/run-remote-tool`, `/find-missing`, `/batch-upload`, `/get-credit-info` | capability discovery, remote tool execution, workspace sync, and account support |
| analytics side traffic | `/v1/batch` on `api.segment.io` | telemetry only, not part of the model/tool protocol |

The practical conclusion is:

- the current proxy already consumes the two remote-tool endpoints in addition to `/get-models` and `/chat-stream`
- but they are not the full upstream surface used by the real Auggie client
- the newly captured endpoints are support/control-plane interfaces around the main chat stream rather than alternate public inference APIs

## Alignment Update: Custom Tool Bridge On Top Of Captured Primitives

Runtime capture does not show a native OpenAI-style Auggie field named `custom_tool_call` or `tool_choice.custom`.
That is still expected, because Auggie is not an OpenAI-native upstream.

What the capture does prove is that the upstream already exposes the primitive pieces needed for a proxy-side bridge:

- outgoing tool declarations through `tool_definitions`
- model-planned tool invocations through `nodes[].tool_use`
- tool continuation through `nodes[].tool_result_node`
- turn continuity through `conversation_id` and `turn_id`

Using those primitives, the current branch now bridges official OpenAI Responses custom-tool semantics on the Auggie line as follows:

| Official OpenAI surface | Current Auggie bridge |
| --- | --- |
| `tools[].type = "custom"` | translated into an Auggie-compatible function-style tool definition with a single string `input` field |
| `tool_choice = { "type": "custom", "name": ... }` | explicitly rejected on Auggie `/v1/responses` because the bridge cannot prove native forced tool-use semantics |
| `output[].type = "custom_tool_call"` | synthesized from Auggie/OpenAI-chat `tool_use` with shimmed `{"input":"..."}` arguments |
| `input[].type = "custom_tool_call_output"` | translated into Auggie `nodes[].tool_result_node` via the tool message bridge |
| `response.custom_tool_call_input.delta` / `.done` | emitted from streamed function-argument deltas after unwrapping the shimmed `input` string |

This distinction is important:

- the captured Auggie runtime evidence proves the upstream building blocks
- the OpenAI-compatible custom-tool contract is still implemented in the proxy layer on top of those building blocks
- that is acceptable for alignment, as long as the proxy matches the official contract and fails explicitly where it cannot

### Official Contract Cross-Check Applied To The Bridge

Cross-checking the local official SDK bundle `openai-6.27.0.tgz` against the bridge exposed two concrete protocol details that are now reflected in the implementation:

- `ResponseCustomToolCallOutput.output` officially allows:
  - `string`
  - `Array<ResponseInputText | ResponseInputImage | ResponseInputFile>`
- `response.custom_tool_call_input.done` officially does not include a `name` field

The current Auggie bridge was updated accordingly:

- custom tool output arrays are accepted at the OpenAI surface and text-array payloads are bridged as chat content items instead of raw JSON strings
- non-text tool output array items are now explicitly rejected on the Auggie `/v1/responses` bridge with an OpenAI-style `400 invalid_request_error`
  - allowed bridged array item types remain only:
    - `input_text`
    - `output_text`
  - this preserves official observable semantics by refusing array items such as `input_image` or `input_file` that the current tool-result bridge cannot represent losslessly
- the streamed `response.custom_tool_call_input.done` event now matches the official field shape more closely

## Source Scan Update

Runtime capture is now backed by local source inspection of the installed Auggie artifacts:

- CLI bundle:
  - `/opt/homebrew/lib/node_modules/@augmentcode/auggie/augment.mjs`
- VS Code sourcemap:
  - `/Users/jqwang/.vscode/extensions/augment.vscode-augment-nightly-0.811.0/out/extension.js.map`

What the local source scan reinforces:

- concrete request/response protocol terms are present for:
  - `tool_definitions`
  - `nodes`
  - `conversation_id`
  - `turn_id`
  - `tool_result_node`
- the bundle also clearly contains the remote-tool bridge names used at runtime:
  - `web-search`
  - `web-fetch`
  - `/agents/list-remote-tools`
  - `/agents/run-remote-tool`

What the local source scan still does not prove:

- no concrete native request field was found that matches OpenAI public semantics for:
  - `tool_choice`
  - `allowed_tools`
  - `response_format`
  - Responses `text.format`
- `json_schema` hits in the local bundle/sourcemap appear to come from generic schema handling, telemetry, or tracing paths rather than a native Auggie response-format control surface

This matters because the proxy can still align to the official OpenAI contract by translating behavior above Auggie, but only where the observable contract is preserved. The docs must distinguish:

- proven native Auggie protocol fields
- proxy-side OpenAI compatibility behavior layered on top of that protocol
- proxy-side experiments that were intentionally rolled back to explicit rejection because they were not observably equivalent

## Verified Request Shapes

### `/chat-stream`

Top-level request keys observed:

- `agent_memories`
- `agent_persona_id`
- `blobs`
- `chat_history`
- `context_code_exchange_request_id`
- `conversation_id`
- `disable_auto_external_sources`
- `enable_parallel_tool_use`
- `external_source_ids`
- `feature_detection_flags`
- `lang`
- `message`
- `mode`
- `model`
- `nodes`
- `path`
- `prefix`
- `root_conversation_id`
- `rules`
- `selected_code`
- `silent`
- `skills`
- `suffix`
- `third_party_override`
- `tool_definitions`
- `turn_id`
- `user_guided_blobs`
- `user_guidelines`
- `workspace_guidelines`

Confirmed properties:

- `tool_definitions` was present on every captured chat request.
- Captured requests contained 19 tool definitions.
- The first eight tool names were:
  - `launch-process`
  - `kill-process`
  - `read-process`
  - `write-process`
  - `list-processes`
  - `web-search`
  - `web-fetch`
  - `codebase-retrieval`

### `/agents/list-remote-tools`

Observed request shape:

```json
{"tool_id_list":{"tool_ids":[0,1,8,12,13,14,15,16,17,18,19,20,21]}}
```

Observed response shape:

- top-level `tools`
- each entry includes:
  - `remote_tool_id`
  - `availability_status`
  - `tool_safety`
  - `tool_definition`
  - `oauth_url`

Verified remote tool names returned by this endpoint:

- `web-search`
- `github-api`
- `linear`
- `jira`
- `confluence`
- `notion`
- `supabase`

### `/agents/run-remote-tool`

Observed request shape:

```json
{
  "tool_name": "web-search",
  "tool_input_json": "{\"query\":\"OpenAI latest news\",\"num_results\":1}",
  "tool_id": 1
}
```

Observed response shape:

```json
{
  "tool_output": "- [OpenAI News](https://openai.com/news/) ...",
  "tool_result_message": "",
  "is_error": false,
  "status": 1
}
```

### `/find-missing`

Observed request keys:

- `model`
- `mem_object_names`

Observed response keys:

- `nonindexed_blob_names`
- `unknown_memory_names`

### `/batch-upload`

Observed request keys:

- `blobs`

Observed response keys:

- `blob_names`

## Verified `web-search` Tool Loop

The `web-search` path is no longer a guess. It was verified end-to-end by runtime capture.

### Sequence

1. CLI bootstraps with `/get-models`.
2. CLI enumerates remote tool availability with `/agents/list-remote-tools`.
3. CLI performs workspace sync support calls:
   - `/find-missing`
   - `/batch-upload`
4. CLI calls `/chat-stream` with `tool_definitions` that include `web-search`.
5. `/chat-stream` response emits tool-use nodes for `web-search`.
6. CLI calls `/agents/run-remote-tool` with:
   - `tool_name = web-search`
   - `tool_id = 1`
   - concrete JSON input
7. CLI calls `/chat-stream` again.
8. The follow-up `/chat-stream` request includes:
   - the same `conversation_id`
   - the same `turn_id`
   - `chat_history` with the prior exchange
   - `nodes[].tool_result_node` containing the remote tool output
9. Final `/chat-stream` response emits the assistant's answer.

### Concrete Continuation Evidence

For the captured `web-search` run:

- first model turn request:
  - `conversation_id = 8524a7a2-b069-44bb-a10e-208ab3e53dc9`
  - `turn_id = 84d3eee1-692d-4613-ae68-331b79e910bc`
  - `chat_history_len = 0`
- tool-result continuation request:
  - `conversation_id = 8524a7a2-b069-44bb-a10e-208ab3e53dc9`
  - `turn_id = 84d3eee1-692d-4613-ae68-331b79e910bc`
  - `chat_history_len = 1`

Observed continuation node:

```json
{
  "id": 1,
  "type": 1,
  "tool_result_node": {
    "tool_use_id": "call_MfKGJ5OkaNvnqYdKlOycUQUj",
    "content": "- [OpenAI News](https://openai.com/news/) ...",
    "is_error": false,
    "request_id": "00582687-6070-4837-9b9c-1ec4ad961160",
    "duration_ms": 681,
    "start_time_ms": 1773101708519
  }
}
```

## Verified MCP Tool Loop

The first runtime capture only proved Auggie's built-in remote tool path, especially `web-search`.

That left an important open question:

- does Auggie upstream only support its own remote tools
- or can `/chat-stream` also plan over ordinary client-provided tools

This was tested with the real terminal `auggie` client plus a local MCP server:

- MCP config used a stdio server launched as:
  - `npx -y @modelcontextprotocol/server-everything`
- CLI invocation shape:

```bash
auggie -p --output-format json --max-turns 3 \
  --workspace-root /tmp/auggie-empty \
  --mcp-config /tmp/.../mcp.json \
  "Use the get-sum tool to add 2 and 3. Do not do the math yourself. Reply with only the final result."
```

### What Was Observed

- `/chat-stream` request `tool_definitions` length increased from 19 to 32
- the first eight tool names became:
  - `echo_everything`
  - `get-annotated-message_everything`
  - `get-env_everything`
  - `get-resource-links_everything`
  - `get-resource-reference_everything`
  - `get-structured-content_everything`
  - `get-sum_everything`
  - `get-tiny-image_everything`
- first model turn request:
  - `conversation_id = e659b681-f15c-4136-a4e8-dacc83753d84`
  - `turn_id = cf76a7b6-5f9e-4651-bed9-2cdcbd69fa32`
  - `chat_history_len = 0`
- first `/chat-stream` response emitted MCP tool-use nodes:

```json
{
  "id": 1,
  "type": 5,
  "tool_use": {
    "tool_use_id": "call_V2WTdiiKMqjmJ0glaNwxypTV",
    "tool_name": "get-sum_everything",
    "input_json": "{\"a\": 2, \"b\": 3}",
    "is_partial": false
  }
}
```

- there was no `/agents/run-remote-tool` call for this MCP tool
- the follow-up `/chat-stream` request reused the same `conversation_id` and `turn_id`
- the follow-up request carried a local tool result as `nodes[].tool_result_node`:

```json
{
  "id": 2,
  "type": 1,
  "tool_result_node": {
    "tool_use_id": "call_V2WTdiiKMqjmJ0glaNwxypTV",
    "content": "The sum of 2 and 3 is 5.",
    "is_error": false
  }
}
```

- the final `/chat-stream` response returned the final answer `5`

### What This Proves

- Auggie upstream is not limited to built-in remote tools like `web-search`
- `/chat-stream` can accept client-injected non-built-in tool definitions
- `/chat-stream` can emit `tool_use` for those tools
- generic tool execution can be performed client-side and resumed through `nodes[].tool_result_node`
- `/agents/run-remote-tool` is required for Auggie remote tools, not for all tool loops

## Verified Plain Tool Definitions Without MCP Namespacing

The MCP capture above still left one narrower concern for the OpenAI bridge:

- does Auggie only understand MCP-originated tool names like `get-sum_everything`
- or can it also handle plain function-style names such as `get_sum`

This was tested by sending a raw `/chat-stream` request directly to Auggie upstream using the same captured request shape, but replacing `tool_definitions` with a single plain tool:

```json
{
  "name": "get_sum",
  "description": "Returns the sum of two numbers",
  "input_schema_json": "{\"type\":\"object\",\"properties\":{\"a\":{\"type\":\"number\"},\"b\":{\"type\":\"number\"}},\"required\":[\"a\",\"b\"]}",
  "tool_safety": 0
}
```

The request used a plain text node:

```json
{
  "id": 1,
  "type": 0,
  "text_node": {
    "content": "Use the get_sum tool to add 2 and 3. Do not answer without using the tool."
  }
}
```

### Direct `/chat-stream` Result

The first upstream response emitted a plain tool-use for `get_sum`:

```json
{
  "id": 1,
  "type": 5,
  "tool_use": {
    "tool_use_id": "call_KCgWT5S47xeLuweFoFzFzicx",
    "tool_name": "get_sum",
    "input_json": "{\"a\": 2, \"b\": 3}",
    "is_partial": false
  }
}
```

The follow-up request then reused the same `conversation_id` and `turn_id`, injected:

```json
{
  "id": 2,
  "type": 1,
  "tool_result_node": {
    "tool_use_id": "call_KCgWT5S47xeLuweFoFzFzicx",
    "content": "The sum of 2 and 3 is 5.",
    "is_error": false
  }
}
```

and the second `/chat-stream` response returned the final answer:

```text
## Result

`2 + 3 = 5`
```

### What This Proves

- Auggie upstream accepts plain client-defined tool names, not only MCP-suffixed names
- Auggie upstream can emit `tool_use` for plain function-style tool definitions
- Auggie upstream can resume the same turn when the client returns a matching `tool_result_node`
- generic function-calling semantics are real on `/chat-stream` in auto mode

## Current Boundary After The New Evidence

What is now directly proven:

- built-in remote tools exist upstream and can be bridged through:
  - `/agents/list-remote-tools`
  - `/agents/run-remote-tool`
  - `/chat-stream` continuation with `tool_result_node`
- client-defined tool definitions also work upstream through `/chat-stream`
- plain function-style tool names also work upstream through `/chat-stream`
- tool-result continuation is not specific to `web-search`

What is still not proven:

- a native Auggie `/chat-stream` field equivalent to OpenAI:
  - `tool_choice: "required"`
  - `tool_choice: { "type": "function", "function": { "name": ... } }`
- native Auggie enforcement equivalent to OpenAI Responses function-tool strict schema mode:
  - omitted `tools[].strict`
  - `tools[].strict = true`
- a native Auggie structured-output field equivalent to OpenAI:
  - `response_format: { "type": "json_schema" | "json_object" }`
  - `text.format: { "type": "json_schema" | "json_object" }`

So the capability boundary is now narrower than before:

- generic function calling is proven upstream
- exact OpenAI forced-tool-choice semantics are still unresolved natively upstream
- structured outputs are still unresolved natively upstream

This is the strongest runtime evidence so far that Auggie supports a native tool-result continuation path.

## Proxy Alignment Update

The runtime/source evidence above now lines up with the current Go implementation more closely than the earlier design notes did.

What is already implemented in the proxy today:

- built-in `web_search` on the OpenAI Responses path is bridged through Auggie remote tools
- the executor now drives:
  - `/agents/list-remote-tools`
  - `/agents/run-remote-tool`
- `parallel_tool_calls` is now forwarded into the Auggie request shape using the runtime-observed native fields:
  - top-level `enable_parallel_tool_use`
  - `feature_detection_flags.support_parallel_tool_use`
  - this makes the proxy request shape closer to captured Auggie traffic, but it is still separate from proving full behavioral parity for parallel tool scheduling
- auto-compatible `tool_choice` narrowing is supported where it is observably safe:
  - `tool_choice: "auto"`
  - `tool_choice: "none"`
  - `tool_choice: { "type": "allowed_tools", ..., "mode": "auto" }`
  - implementation detail: the proxy filters `tool_definitions` down to the selected subset
- non-preserved forced tool-choice forms are now intentionally rejected on Auggie `/v1/responses`:
  - `tool_choice: "required"`
  - `tool_choice: { "type": "function", ... }`
  - `tool_choice: { "type": "custom", ... }`
  - `tool_choice: { "type": "allowed_tools", ..., "mode": "required" }`
  - direct built-in selections such as `tool_choice: { "type": "web_search" }`
- OpenAI Responses function-tool strict schema mode is now explicitly narrowed on the Auggie line:
  - `tools[].strict = false` is allowed
  - omitted `tools[].strict` is rejected because official Responses function tools default to strict mode
  - `tools[].strict = true` is rejected because runtime/source evidence still does not prove native Auggie strict schema enforcement
- OpenAI Responses `reasoning.effort` is now explicitly narrowed on the Auggie line:
  - `low`, `medium`, and `high` are allowed
  - official `none`, `minimal`, and `xhigh` are rejected early with `400 invalid_request_error`
  - source-level reason: the current Auggie request builder only forwards native `low | medium | high` effort values, so accepting the other official values would silently drop observable semantics
- built-in web-search tool definitions are now explicitly narrowed on the Auggie line to type-only availability:
  - `{ "type": "web_search" }`
  - `{ "type": "web_search_preview" }`
  - alias variants with only `type`
- non-preserved built-in web-search configuration is now rejected early with an OpenAI-style `400 invalid_request_error`, including:
  - `tools[].search_context_size`
  - `tools[].filters`
  - `tools[].search_content_types`
  - `tools[].user_location`
  - this is intentional because current translator/runtime evidence only proves that Auggie receives a built-in `web-search` tool definition, not that it preserves OpenAI's request-time search configuration fields
- the surface layer now has direct proxy evidence for that rejection behavior:
  - HTTP `/v1/responses` rejects those non-preserved forms before execution
  - websocket `response.create` on `/v1/responses` does the same
- request `metadata` is now proven to be proxy-preserved on the Auggie Responses line even though it is not an upstream Auggie field:
  - the proxy does not forward `metadata` to `/chat-stream`
  - the final OpenAI response object still echoes `metadata`
  - streaming lifecycle events still echo `metadata`
- `include` support on the Auggie Responses line is now narrowed to the expansions that have observable bridge output:
  - `reasoning.encrypted_content`
  - `web_search_call.action.sources`
  - `web_search_call.results`
- non-preserved `include` expansions are now rejected early with an OpenAI-style `400 invalid_request_error`, including:
  - `message.output_text.logprobs`
  - `code_interpreter_call.outputs`
  - `computer_call_output.output.image_url`
  - `file_search_call.results`
  - `message.input_image.image_url`
- this is intentional because the current Auggie bridge only has direct translator support for reasoning encrypted content and web-search expansion output; the other official `include` values would otherwise be accepted without matching observable output

What remains intentionally distinguished from native upstream proof:

- the runtime capture has not yet shown a first-class Auggie request field for forced tool choice
- the runtime capture has not yet shown native Auggie semantics equivalent to OpenAI forced tool-use guarantees
- the runtime capture has not yet shown native Auggie semantics equivalent to OpenAI Responses function-tool strict schema guarantees
- the runtime capture has not yet shown native Auggie request fields equivalent to OpenAI built-in web-search configuration controls such as:
  - `search_context_size`
  - `filters.allowed_domains`
  - `search_content_types`
  - `user_location`
- the runtime capture has not yet shown a first-class Auggie request field for structured outputs
- current Auggie routes in the proxy still reject OpenAI structured-output requests rather than pretending support exists
- current proxy-side `metadata` preservation is a public-contract behavior implemented above Auggie, not proof of a native Auggie metadata field

## Endpoint-To-Bridge Meaning

The newly captured runtime surface is enough to separate core transport from support/control paths.

| Upstream endpoint | Runtime role | Bridge meaning on the OpenAI side |
| --- | --- | --- |
| `/get-models` | model/bootstrap metadata | source material for `/v1/models`, model gating, and feature hints |
| `/chat-stream` | primary conversational stream | core transport for `/v1/chat/completions` and `/v1/responses` |
| `/agents/list-remote-tools` | remote tool capability discovery | private capability registry behind OpenAI tool availability, not a public OpenAI endpoint to expose 1:1 |
| `/agents/run-remote-tool` | remote tool execution | upstream execution leg behind function/tool calls; this is the key private primitive that can back OpenAI tool loops |
| `/find-missing` | workspace blob preflight | support path for code/context hydration, not part of OpenAI's public inference contract |
| `/batch-upload` | workspace blob upload | support path for code/context hydration, not part of OpenAI's public inference contract |
| `/get-credit-info` | account/credit query | management telemetry, not part of OpenAI responses semantics |

This is the important bridge boundary:

- we should align to the official OpenAI public contract
- we should not mirror Auggie's private support endpoints as public OpenAI endpoints
- we should, however, use the new runtime evidence to improve how the proxy drives the upstream private protocol behind that official contract

## Observed Node Types

The exact enum mapping is still incomplete, but these node forms were directly observed in requests or responses:

| Node type | Observed structure | Confidence |
| --- | --- | --- |
| `0` | `text_node` on request, assistant text on response | high |
| `1` | `tool_result_node` on follow-up request | high |
| `4` | `ide_state_node` on request | high |
| `5` | populated `tool_use` response node with JSON input | high |
| `7` | earlier `tool_use` response node with empty input payload | medium |
| `8` | `thinking` response node | high |
| `10` | `token_usage` response node | high |
| `2` | stream marker/phase boundary, exact semantic not yet mapped | low |
| `3` | stream marker/phase boundary, exact semantic not yet mapped | low |

Important detail:

- the `tool_use` response did not appear only as final text
- it appeared as explicit structured nodes before the remote tool execution request

## Confirmed Versus Inferred

### Confirmed

- `web-search` is a real upstream tool path, not only local post-processing.
- `chat-stream` carries explicit `tool_definitions`.
- Auggie upstream exposes a remote tool registry via `/agents/list-remote-tools`.
- Tool execution happens through `/agents/run-remote-tool`.
- Tool output is fed back into a later `/chat-stream` request as `nodes[].tool_result_node`.
- Continuation reuses the same `conversation_id` and `turn_id` within a single CLI run.

### Inferred

- `/find-missing` and `/batch-upload` are workspace blob sync primitives rather than conversational APIs.
- node type `7` is likely a provisional or partial `tool_use` emission before the full populated tool node.
- a robust OpenAI bridge will probably need to preserve the workspace sync path when code context matters.

## OpenAI Bridge Implications

### What Is Now Safe To Say

- The Auggie line is not limited to response-only reconstruction anymore.
- At least for remote tools such as `web-search`, we now have the real upstream control flow.
- Auggie is still not exposing a raw native OpenAI HTTP surface here.
- The bridge target is therefore:
  - private Auggie protocol on the upstream side
  - official OpenAI contract on the public side

### Practical Mapping Direction

Observed Auggie primitives already line up with OpenAI tool semantics:

- Auggie `tool_use` node
  - maps naturally to OpenAI `tool_calls` or `function_call`
- Auggie `tool_result_node`
  - maps naturally to OpenAI tool result messages or `function_call_output`
- Auggie `thinking`
  - maps naturally to OpenAI reasoning items
- Auggie `conversation_id` plus `turn_id`
  - are the best current candidates for bridging OpenAI continuation state

### What This Changes In Design

- `web-search` can be bridged through the real upstream execution path instead of being rejected or synthesized.
- `list-remote-tools` should be treated as capability discovery, not a side detail.
- `run-remote-tool` is the critical function-call execution endpoint discovered so far.
- The remaining design challenge is no longer "does Auggie have a real tool protocol?"
- The remaining design challenge is "how do we faithfully map Auggie's tool protocol into OpenAI's public contract while being explicit about which parts are native, which parts are safely preserved in-proxy, and which parts must be rejected?"

## Remaining Verification Gaps

- whether local tools such as `launch-process` and `apply_patch` always stay local or can also route through upstream control
- whether multiple tool calls in a single turn are emitted as multiple `tool_use` nodes with stable ordering
- whether `enable_parallel_tool_use=true` leads to materially different stream behavior
- whether `web-fetch` follows the same `/agents/run-remote-tool` path as `web-search`
- whether a native continuation path exists beyond the current same-turn replay shape
- whether Auggie has a native request field for forced tool choice instead of prompt-level emulation
- whether Auggie has a native structured-output control surface comparable to OpenAI `json_schema` or `json_object`
- whether a clean OpenAI `previous_response_id` mapping should use:
  - only `conversation_id` and `turn_id`
  - only replayed `chat_history`
  - or both, as the current CLI appears to do
