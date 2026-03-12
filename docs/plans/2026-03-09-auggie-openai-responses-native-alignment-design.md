# Auggie OpenAI Responses Native Alignment Design

Date: 2026-03-09

## Goal

Align the proxy's Auggie-backed OpenAI compatibility layer with the official OpenAI `Responses` and function-calling contract, with special focus on:

- `function_call` to `function_call_output` loops
- `previous_response_id` incremental continuation
- reasoning output parity on `/v1/responses`
- preserving existing `/v1/chat/completions` compatibility

The target of truth is the official OpenAI API contract, not the current behavior of `code.ppchat.vip`.

## Source Evidence

Reverse-engineering of Auggie's native protocol from the VS Code extension sourcemap at:

- `/Users/jqwang/.vscode/extensions/augment.vscode-augment-nightly-0.809.0/out/extension.js.map`

confirms that Auggie natively exposes the semantic pieces required for OpenAI tool loops:

- `ChatRequest.nodes[]`
- `ChatRequestToolResult.tool_use_id`
- `ChatRequestToolResult.content`
- `ChatRequestToolResult.is_error`
- `ChatRequest.conversation_id`
- `ChatRequest.turn_id`
- `ChatRequest.parent_conversation_id`
- `ChatRequest.root_conversation_id`
- `ChatResponse.conversation_id`
- `ChatResponse.turn_id`
- `ChatResultThinking.openai_responses_api_item_id`
- `ChatResponse.nodes[]` entries for `tool_use`, `thinking`, and `token_usage`

This means the current mismatch is not caused by Auggie lacking tool-result or response-state support. The mismatch is caused by the proxy flattening those semantics before the request reaches Auggie.

## Runtime Capture Update

Runtime capture against the real terminal `auggie` client on 2026-03-10 upgraded several key assumptions from source-level inference to direct network evidence.

See:

- `docs/plans/2026-03-10-auggie-runtime-capture-evidence.md`

The most important runtime confirmations are:

- `chat-stream` requests really do carry `tool_definitions`
- `web-search` is a real upstream tool path, not only client-side reconstruction
- remote tool execution happens through `/agents/run-remote-tool`
- remote tool output is sent back to Auggie as `nodes[].tool_result_node`
- same-turn tool continuation reuses the same `conversation_id` and `turn_id`
- MCP-originated tools also produce upstream `tool_use` on `/chat-stream`
- plain function-style tool names such as `get_sum` also produce upstream `tool_use` on `/chat-stream`
- additional upstream support endpoints now verified in traffic:
  - `/agents/list-remote-tools`
  - `/find-missing`
  - `/batch-upload`
  - `/get-credit-info`

This narrows the design problem substantially:

- the upstream is still not a raw OpenAI HTTP surface
- but it is now proven to expose the semantic pieces required to bridge OpenAI tool loops without relying on response-only reconstruction

### What Moved From Inference To Proof

| Topic | Before runtime capture | Now verified | Design consequence |
| --- | --- | --- | --- |
| `chat-stream` carries tool metadata | inferred from sourcemap types and request builders | real terminal traffic shows `tool_definitions` on live `/chat-stream` requests | preserving and forwarding tool definitions is required, not speculative |
| `web-search` is an upstream capability | partially inferred; could still have been local client reconstruction | confirmed through `/agents/list-remote-tools`, `tool_use` nodes, and `/agents/run-remote-tool` | OpenAI tool loops can target a real upstream execution path instead of a fake or rejected bridge |
| non-built-in client tools can plan upstream | unknown; only built-in remote tools had live proof | MCP capture shows `/chat-stream` emits `tool_use` for `get-sum_everything` without `/agents/run-remote-tool` | Auggie can plan over client-injected tool definitions, so generic tool loops are not limited to built-ins |
| plain function-style tool names work upstream | unknown; MCP namespacing left room for doubt | direct `/chat-stream` request with `tool_definitions[].name = "get_sum"` produced `tool_use` and accepted a later `tool_result_node` | the OpenAI bridge does not need MCP naming tricks to make generic function calling viable on Auggie |
| tool results return through Auggie-native nodes | inferred from request/response type names | confirmed by follow-up `/chat-stream` request containing `nodes[].tool_result_node` | mapping OpenAI `function_call_output` into `tool_result_node` is grounded in live protocol evidence |
| same-turn continuation state exists upstream | inferred from `conversation_id` and `turn_id` fields in source | confirmed by same `conversation_id` and `turn_id` being reused across tool continuation | the proxy-side `previous_response_id` shim has a concrete upstream anchor instead of a guessed one |
| Auggie has more than two runtime product endpoints | unknown from the current executor alone | runtime capture observed `/agents/list-remote-tools`, `/agents/run-remote-tool`, `/find-missing`, `/batch-upload`, `/get-credit-info` | alignment work must distinguish core chat transport from surrounding control-plane/support endpoints |

### What Did Not Change

- the current proxy still targets the official OpenAI public contract rather than exposing Auggie's private endpoints 1:1
- the current proxy executor now hard-codes four tenant product endpoints:
  - `/get-models`
  - `/chat-stream`
  - `/agents/list-remote-tools`
  - `/agents/run-remote-tool`
- the public source of truth is still the official OpenAI API contract
- the newly observed Auggie endpoints are private upstream building blocks, not public APIs we should expose 1:1
- there is still no verified native Auggie field matching OpenAI's forced tool-choice semantics, and the earlier prompt-shim experiment is no longer treated as aligned:
  - `tool_choice: "required"`
  - `tool_choice: { "type": "function", "function": { "name": ... } }`
- there is still no verified native Auggie field matching OpenAI structured-output controls:
  - `response_format: { "type": "json_schema" | "json_object" }`
  - `text.format: { "type": "json_schema" | "json_object" }`

### Updated Capability Boundary

After the MCP and direct `/chat-stream` experiments, the upstream boundary is now clearer:

- proven upstream:
  - plain client-defined tool definitions
  - plain function-style tool names
  - upstream `tool_use` emission for those tools
  - same-turn continuation via `nodes[].tool_result_node`
- still unresolved:
  - native upstream parity for OpenAI forced tool-choice semantics
  - native upstream parity for OpenAI structured-output semantics

This changes the implementation priority:

- generic function calling is no longer blocked on proving Auggie capability
- proxy-side prompt emulation is no longer considered sufficient for official forced tool choice on `/v1/responses`
- the remaining unknown is native upstream field support, and the proxy must reject any forced-tool form it cannot preserve
- the main still-unimplemented OpenAI surface on the Auggie line is structured outputs

## Implementation Update

The sections below started as a forward-looking design on 2026-03-09. Several items are no longer merely planned; they are now implemented and covered by focused tests in the current branch.

What is now implemented:

- OpenAI chat/function tool loops preserve native Auggie `nodes[].tool_result_node` instead of flattening tool results away
- OpenAI `/v1/responses` continuation with `previous_response_id` now replays stored Auggie conversation state using:
  - `conversation_id`
  - `turn_id`
  - replay context from prior exchange state
- OpenAI `parallel_tool_calls` is now forwarded into the Auggie request shape using the native fields seen in runtime capture:
  - top-level `enable_parallel_tool_use`
  - `feature_detection_flags.support_parallel_tool_use`
- Auggie reasoning is now propagated into the OpenAI-compatible response pipeline
- built-in `web_search` on the Responses path is now bridged through the native Auggie remote-tool loop
- OpenAI Responses custom tools are now bridged on the Auggie line via a function-style shim:
  - `tools[].type = "custom"`
  - `input[].type = "custom_tool_call"`
  - `input[].type = "custom_tool_call_output"`
  - `response.custom_tool_call_input.delta`
  - `response.custom_tool_call_input.done`
- forced custom-tool selection via `tool_choice = { "type": "custom", "name": ... }` is intentionally rejected on Auggie `/v1/responses` because the bridge cannot preserve native forced tool-use semantics
- OpenAI-compatible custom tool outputs now accept the official `string | array` shape and bridge text-array payloads through the Auggie tool-result path instead of collapsing them into a raw JSON string
- bridged tool-result arrays on the Auggie Responses line are now explicitly narrowed to text items only:
  - `input[].type = "function_call_output"` with `output[]`
  - `input[].type = "custom_tool_call_output"` with `output[]`
  - allowed array item types:
    - `input_text`
    - `output_text`
- non-text tool-result array items are now intentionally rejected on the Auggie Responses line with an OpenAI-style `400 invalid_request_error`, including:
  - `input_image`
  - `input_file`
  - this is intentional because the current bridged tool-output path can only preserve text items without collapsing official structured content into a raw JSON string
- auto-compatible `tool_choice` forms remain supported on the Auggie Responses line:
  - `tool_choice: "auto"`
  - `tool_choice: "none"`
  - `tool_choice: { "type": "allowed_tools", ..., "mode": "auto" }`
- non-preserved forced `tool_choice` forms are now intentionally rejected on the Auggie Responses line with an OpenAI-style `400 invalid_request_error`:
  - `tool_choice: "required"`
  - `tool_choice: { "type": "function", ... }`
  - `tool_choice: { "type": "custom", ... }`
  - `tool_choice: { "type": "allowed_tools", ..., "mode": "required" }`
  - direct built-in tool selections such as `tool_choice: { "type": "web_search" }`
- the Auggie `/v1/responses` HTTP and websocket entry paths now have direct proxy evidence for that rejection behavior before execution begins
- non-preserved forced `tool_choice` forms are now also intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`:
  - `tool_choice: "required"`
  - `tool_choice: { "type": "function", ... }`
  - `tool_choice: { "type": "allowed_tools", ..., "mode": "required" }`
- the Auggie `/v1/chat/completions` bridge no longer injects prompt-shimmed "must call a tool" directives into the user message for those forced forms
- auto-compatible `tool_choice` forms remain supported on the Auggie `/v1/chat/completions` line:
  - `tool_choice: "auto"`
  - `tool_choice: "none"`
  - `tool_choice: { "type": "allowed_tools", ..., "mode": "auto" }`
- OpenAI Chat Completions tool definitions are now explicitly narrowed on the Auggie `/v1/chat/completions` line to the tool type the bridge can actually preserve:
  - `tools[].type = "function"` remains allowed
  - `tools[].function.strict = false` remains allowed
  - omitted `tools[].function.strict` remains allowed
- non-preserved OpenAI Chat Completions tool definition types are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `tools[].type = "custom"`
  - built-in web-search tool types on `tools[]`, such as `web_search`
  - this is intentional because the current Auggie chat-completions bridge only preserves function-tool definitions
- non-preserved OpenAI Chat Completions function-tool strict mode is now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`:
  - `tools[].function.strict = true`
  - this is intentional because the current Auggie chat-completions bridge cannot preserve official function-tool strict schema semantics
- deprecated Chat Completions function-calling aliases are now partially bridged on the Auggie `/v1/chat/completions` line where the observable semantics can be preserved:
  - `functions[]` now maps into Auggie `tool_definitions[]`
  - `function_call: "auto"` is accepted as the legacy alias of auto tool selection
  - `function_call: "none"` is accepted as the legacy alias of suppressing tool availability
- non-preserved deprecated Chat Completions function-calling forms are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `function_call: { "name": ... }`
  - mixed `tools` + `functions`
  - mixed `tool_choice` + `function_call`
  - this is intentional because the current Auggie bridge cannot preserve native forced function-use semantics or resolve ambiguous mixed modern/legacy tool configuration without silent degradation
- non-preserved structured output formats are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`:
  - `response_format: { "type": "json_object" }`
  - `response_format: { "type": "json_schema", ... }`
- the same rejection now also happens before execution for Responses-format payloads accidentally sent to `/v1/chat/completions` and bridged into `response_format`
- OpenAI Chat Completions `reasoning_effort` is now explicitly narrowed on the Auggie `/v1/chat/completions` line to the native values the bridge can actually preserve:
  - `low`
  - `medium`
  - `high`
- non-preserved official chat reasoning effort values are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `reasoning_effort = "none"`
  - `reasoning_effort = "minimal"`
  - `reasoning_effort = "xhigh"`
  - this is intentional because the current Auggie chat-completions request translator only forwards native `low | medium | high` effort values and would otherwise silently drop the other official settings
- non-preserved OpenAI Chat Completions sampling and budget controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `max_tokens`
  - `max_completion_tokens`
  - `top_logprobs`
  - `logprobs`
  - `temperature`
  - `frequency_penalty`
  - `presence_penalty`
  - `top_p`
  - this is intentional because the current Auggie chat-completions request translator does not preserve those controls
  - official type and range validation is still applied before rejection for the fields whose contract constrains shape:
    - `max_completion_tokens` must be an integer
    - `frequency_penalty` must be a number in `[-2, 2]`
    - `presence_penalty` must be a number in `[-2, 2]`
    - `logprobs` must be a boolean
- non-preserved OpenAI Chat Completions choice-count, stop-sequence, and determinism controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `n`
  - `stop`
  - `seed`
  - this is intentional because the current Auggie chat-completions request translator does not preserve those controls
  - official type and shape validation is still applied before rejection for the fields whose contract constrains shape:
    - `n` must be an integer
    - `stop` must be a string or an array of strings
    - `stop` arrays may contain at most 4 sequences
    - `seed` must be an integer
- non-preserved OpenAI Chat Completions streaming controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `stream_options`
  - this is intentional because the current Auggie chat-completions bridge does not preserve chat streaming option semantics
  - official type and shape validation is still applied before rejection for the currently documented chat stream options:
    - `stream_options` must be an object
    - `stream_options.include_obfuscation` must be a boolean when present
    - `stream_options.include_usage` must be a boolean when present
    - `stream_options` requires `stream=true`
- non-preserved OpenAI Chat Completions request-scoped attribution and cache controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `service_tier`
  - `prompt_cache_key`
  - `prompt_cache_retention`
  - `safety_identifier`
  - `user`
  - this is intentional because the current Auggie chat-completions request translator does not preserve those controls
- request `metadata` is now explicitly preserved on the Auggie `/v1/chat/completions` non-stream path:
  - it is validated using the official shape constraints the bridge can enforce locally
  - it is not forwarded to Auggie upstream
  - it is echoed back on the final `chat.completion` object
  - `store=true` remains separately unsupported on `/v1/chat/completions`, so this local echo does not imply stored object retrieval support
- non-preserved OpenAI Chat Completions verbosity controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`:
  - `verbosity`
  - this is intentional because the current Auggie chat-completions bridge has no native field that preserves official top-level chat verbosity semantics
  - official type and enum validation is still applied before rejection:
    - `verbosity` must be a string
    - supported official values are `low`, `medium`, and `high`
- non-preserved OpenAI Chat Completions web search controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`:
  - `web_search_options`
  - this is intentional because the current Auggie chat-completions bridge cannot preserve official top-level chat web-search activation and configuration semantics
  - official shape validation is still applied before rejection for the currently documented fields:
    - `web_search_options` must be an object
    - `web_search_options.search_context_size` must be a string and one of `low`, `medium`, or `high`
    - `web_search_options.user_location` must be an object when present
    - `web_search_options.user_location.type` must be a string when present and, when set, must equal `approximate`
    - `web_search_options.user_location.approximate` must be an object when present
    - `web_search_options.user_location.approximate.city|country|region|timezone` must be strings when present
- non-preserved OpenAI Chat Completions token-bias and predicted-output controls are now intentionally rejected on the Auggie `/v1/chat/completions` line with an OpenAI-style `400 invalid_request_error`, including:
  - `logit_bias`
  - `prediction`
  - this is intentional because the current Auggie chat-completions request translator does not preserve those controls
  - official shape validation is still applied before rejection for the fields whose contract constrains shape:
    - `logit_bias` must be an object
    - each `logit_bias` entry must be a number in `[-100, 100]`
    - `prediction` must be an object
- OpenAI Chat Completions output-modality handling is now explicitly narrowed on the Auggie `/v1/chat/completions` line:
  - `modalities: ["text"]` remains allowed because it is equivalent to the default text-only behavior the bridge can preserve
  - any request that includes audio output semantics is intentionally rejected with an OpenAI-style `400 invalid_request_error`, including:
    - `modalities` containing `"audio"`
    - `audio`
  - this is intentional because the current Auggie chat-completions bridge only preserves text output and does not preserve native audio output controls
- OpenAI Responses function-tool strict schema mode is now explicitly narrowed on the Auggie line:
  - `tools[].strict = false` is allowed
  - omitted `tools[].strict` is rejected because official Responses function tools default to strict mode
  - `tools[].strict = true` is rejected because the Auggie bridge cannot prove native strict function schema enforcement
- request `metadata` is now proven end-to-end on the Auggie Responses path:
  - it is not forwarded to Auggie upstream
  - it is echoed back on the final OpenAI response object
  - it is echoed in streaming lifecycle events such as `response.created` and `response.in_progress`
- `include` support on Auggie `/v1/responses` is now explicitly narrowed to the values with observable bridge output:
  - `reasoning.encrypted_content`
  - `web_search_call.action.sources`
  - `web_search_call.results`
- OpenAI Responses `reasoning.effort` is now explicitly narrowed on the Auggie line to the native values the bridge can actually preserve:
  - `low`
  - `medium`
  - `high`
- non-preserved official reasoning effort values are now intentionally rejected on the Auggie Responses line with an OpenAI-style `400 invalid_request_error`, including:
  - `reasoning.effort = "none"`
  - `reasoning.effort = "minimal"`
  - `reasoning.effort = "xhigh"`
  - this is intentional because the current Auggie request builder only forwards native `low | medium | high` effort values and would otherwise silently drop the other official settings
- built-in web-search tool definitions are now explicitly narrowed on the Auggie line:
  - `{ "type": "web_search" }`
  - `{ "type": "web_search_preview" }`
  - alias variants with only `type`
- non-preserved built-in web-search configuration is now intentionally rejected on the Auggie Responses line with an OpenAI-style `400 invalid_request_error`, including:
  - `tools[].search_context_size`
  - `tools[].filters`
  - `tools[].search_content_types`
  - `tools[].user_location`
  - this is intentional because the current Auggie bridge collapses built-in web-search tool definitions down to upstream tool availability only and does not preserve the official request-time search configuration shape
- non-preserved `include` expansions are now intentionally rejected on Auggie routes with an OpenAI-style `400 invalid_request_error`, including:
  - `message.output_text.logprobs`
  - `code_interpreter_call.outputs`
  - `computer_call_output.output.image_url`
  - `file_search_call.results`
  - `message.input_image.image_url`

What `tool_choice` support means today on the Auggie line:

- the proxy still supports tool availability narrowing where it is observably safe:
  - restrict the outgoing `tool_definitions` set to the selected `allowed_tools` subset
- the proxy no longer exposes prompt-shimmed forced tool-use as aligned behavior on `/v1/responses`
- any request that depends on native forced tool-use semantics is rejected instead of being silently downgraded to a best-effort prompt instruction

What is still not implemented on the Auggie line:

- native upstream proof for a first-class Auggie `tool_choice` request field
- native upstream parity for OpenAI forced tool-use semantics
- native upstream parity for OpenAI Responses function-tool strict schema semantics:
  - official omitted `tools[].strict` defaults to strict mode
  - current Auggie bridge only accepts `tools[].strict = false`
- native upstream parity for the full OpenAI Responses reasoning-effort range:
  - official values include `none`, `minimal`, `low`, `medium`, `high`, and `xhigh`
  - current Auggie bridge only preserves `low`, `medium`, and `high` and now explicitly rejects the other official values on both `/v1/responses` and `/v1/chat/completions`
- native upstream proof for a first-class Auggie structured-output request field
- OpenAI output-text logprob inclusion on Auggie routes:
  - `include = ["message.output_text.logprobs"]`
  - current behavior is explicit rejection, not silent degradation
- OpenAI include expansions on Auggie routes that do not have observable bridge output:
  - `code_interpreter_call.outputs`
  - `computer_call_output.output.image_url`
  - `file_search_call.results`
  - `message.input_image.image_url`
  - current behavior is explicit rejection, not silent degradation
- native upstream parity for OpenAI built-in web-search request configuration:
  - official Responses web-search tools expose `search_context_size`, `filters`, `search_content_types`, and `user_location`
  - current Auggie bridge only preserves built-in tool availability, not those request-time configuration semantics
- OpenAI structured outputs on Auggie routes:
  - chat `response_format.type=json_schema|json_object`
  - responses `text.format.type=json_schema|json_object`
- OpenAI custom-tool input grammars are not yet represented on the Auggie shim:
  - official `tools[].format.type = "grammar"` exists in the OpenAI contract
  - current Auggie bridge only models unconstrained text input via a function-style `{ "input": string }` shim

### Official Contract Update (2026-03-11)

Cross-checking the local official SDK bundle at `openai-6.27.0.tgz` against the current Auggie Responses bridge clarified three concrete protocol points:

- `ToolChoiceCustom` is part of the official Responses contract and uses:
  - `{ "type": "custom", "name": ... }`
- `ResponseCustomToolCallOutput.output` is officially:
  - `string | Array<ResponseInputText | ResponseInputImage | ResponseInputFile>`
- `response.custom_tool_call_input.done` does not carry a `name` field in the official SDK type

The current branch now matches those three contract points on the Auggie Responses path.

## Historical Problem Summary

This section describes the original 2026-03-09 problem statement that motivated the work. It is not the current live behavior of the branch anymore.

The current `/v1/responses` path for Auggie is:

`OpenAI Responses -> OpenAI Chat Completions -> minimal Auggie chat_history`

That path drops the important parts of the official contract:

1. `function_call_output` becomes a generic tool message, then disappears when translated into Auggie `chat_history`.
2. `previous_response_id` is not translated into Auggie conversation state at all.
3. Auggie `thinking` is not surfaced as OpenAI reasoning output.
4. Non-stream aggregation keeps tool calls and usage, but not reasoning.

The result is partial first-turn compatibility and broken second-turn tool execution semantics.

Historical note:

- items 1 through 4 above have since been addressed in the current implementation, except for structured-output support and native upstream field proof

## Chosen Strategy

Use a native Auggie request bridge for tool loops while keeping the public API surface unchanged.

### Request Path

- Keep external `/v1/responses` unchanged.
- Keep external `/v1/chat/completions` unchanged.
- Upgrade the OpenAI-to-Auggie request translator so OpenAI chat-completions messages can preserve:
  - assistant `tool_calls`
  - tool messages with `tool_call_id`
  - native Auggie `nodes[].tool_result_node`
- Add an Auggie-specific `/v1/responses` request builder inside the executor for incremental `previous_response_id` requests.

### Response Path

- Keep using Auggie stream output as the transport source.
- Extend the Auggie-to-OpenAI chat-completions response translator to surface:
  - `tool_use` as `tool_calls`
  - `thinking` as `reasoning_content`
  - `token_usage` as OpenAI-compatible usage
- Extend non-stream aggregation so reasoning survives into the synthesized final OpenAI chat completion payload.
- Continue reusing the existing OpenAI chat-completions to OpenAI `Responses` translator after the Auggie-to-OpenAI step.

### State Path

- Introduce a minimal proxy-side in-memory mapping:
  - key: OpenAI `response.id`
  - value: Auggie `conversation_id`, `turn_id`, requested model, and timestamps
- Use this only for official `previous_response_id` continuation.
- Do not require general-purpose chat history persistence for normal stateless `input` requests.

This keeps the proxy officially compatible without inventing a full conversation database.

## Alternatives Considered

### 1. Keep Patching the Existing `chat_history` Bridge

Rejected.

The bridge is structurally lossy. It cannot faithfully represent:

- multiple tool calls
- tool call IDs
- `function_call_output`
- `previous_response_id`
- reasoning items

It would keep accumulating special cases while still failing the official contract.

### 2. Fully Switch to Auggie Conversation APIs First

Rejected for this pass.

Auggie exposes conversation and exchange endpoints, but adopting them as the first move would widen the implementation surface too much. The current priority is official API parity on the existing `/v1/responses` and `/v1/chat/completions` paths.

Those APIs remain a second-stage option if later work shows they are necessary for durability or multi-process state sharing.

## Architecture

### 1. Richer OpenAI Chat-Completions to Auggie Translation

Upgrade `ConvertOpenAIRequestToAuggie` so it can emit both:

- legacy `message` and `chat_history` for plain text turns
- native `nodes` when the request contains tool loop semantics

The translator should map:

- assistant `tool_calls[].id` to Auggie `tool_result_node.tool_use_id`
- tool message `content` to Auggie `tool_result_node.content`
- tool message error flag when present to `tool_result_node.is_error`

This fixes:

- `/v1/chat/completions` second-turn tool execution
- `/v1/responses` full-inline tool execution after the Responses-to-Chat translation

### 2. Native Incremental `/v1/responses` Request Builder

For requests that use `previous_response_id`, bypass the lossy OpenAI chat-completions bridge and build the Auggie payload directly from the original Responses request.

That builder should:

- load the stored Auggie conversation state for the referenced response ID
- emit `conversation_id` and `turn_id`
- translate `function_call_output` into `nodes[].tool_result_node`
- keep `tool_definitions`
- continue to apply the same model aliasing and auth flow already used by Auggie execution

If the state lookup fails, the proxy should return an OpenAI-compatible invalid request error instead of silently degrading to stateless behavior.

### 3. Ephemeral Response-State Store

Add a small in-memory store owned by the Auggie executor path.

Required stored fields:

- OpenAI response ID
- Auggie conversation ID
- Auggie turn ID
- model name
- created/updated timestamps

The store should:

- support concurrent access
- expire entries after a bounded TTL
- be used only when `previous_response_id` is present

This is not full conversational memory. It is a protocol-compatibility shim required by the official API contract.

### 4. Reasoning Translation

Auggie `nodes[].thinking` should be exposed to OpenAI chat-completions as `reasoning_content`, then converted by the existing OpenAI-to-Responses translator into official `reasoning` items and SSE events.

For non-stream responses, the aggregated final OpenAI chat completion payload must also include reasoning so the final `/v1/responses` JSON body contains reasoning output.

### 5. Error Handling

The alignment layer should fail explicitly when it cannot satisfy official semantics.

Examples:

- unknown `previous_response_id`
  - return an OpenAI invalid request error
- `function_call_output` without `call_id`
  - return an OpenAI invalid request error
- malformed Auggie tool loop response
  - return an OpenAI upstream or bad gateway style error according to the existing handler contract

Do not silently drop semantic fields just to keep the request moving.

## Data Flow

### Full Inline `Responses` Tool Loop

1. client sends `/v1/responses` with `message + function_call + function_call_output`
2. proxy converts Responses input into OpenAI chat-completions semantics
3. upgraded OpenAI-to-Auggie translator emits native Auggie tool-result nodes
4. Auggie continues the turn and streams back text, tool use, thinking, and usage
5. proxy translates Auggie to OpenAI chat-completions
6. proxy translates OpenAI chat-completions to OpenAI `Responses`

### Incremental `previous_response_id` Continuation

1. first turn returns official OpenAI `response.id`
2. proxy stores `response.id -> Auggie conversation_id + turn_id`
3. client sends `/v1/responses` with `previous_response_id` and `function_call_output`
4. proxy looks up Auggie state and emits direct Auggie continuation request with `tool_result_node`
5. Auggie resumes the native conversation turn
6. proxy returns official OpenAI `Responses` output

## Testing Strategy

Use TDD and keep tests tightly scoped.

### Request Translation Tests

Add failing tests for:

- OpenAI chat-completions second turn with assistant `tool_calls` plus tool message
- Responses full-inline tool chain producing native Auggie `nodes[].tool_result_node`
- Responses incremental request using `previous_response_id` producing `conversation_id`, `turn_id`, and tool result nodes

### Response Translation Tests

Add failing tests for:

- Auggie `thinking` becoming OpenAI `reasoning_content`
- non-stream aggregated chat completion preserving reasoning and tool calls
- final `/v1/responses` JSON body containing reasoning items

### Executor Tests

Add failing tests for:

- response-state store persistence across first turn and `previous_response_id` continuation
- missing `previous_response_id` mapping returning an OpenAI-style error

### Live Verification

After tests pass, compare:

- local `http://localhost:8317`
- external `https://code.ppchat.vip`
- official OpenAI contract from docs

Treat the official docs as the source of truth and record any intentionally preserved differences from `code.ppchat.vip`.

## Files Expected To Change

- `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request.go`
- `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_request_test.go`
- `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_response.go`
- `apps/server-go/internal/translator/auggie/openai/chat-completions/auggie_openai_response_test.go`
- `apps/server-go/internal/runtime/executor/auggie_executor.go`
- `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`
- `apps/server-go/internal/translator/openai/openai/responses/openai_openai-responses_response.go`
- new focused tests for Auggie `/v1/responses` continuation behavior if needed

## Non-Goals

This pass does not:

- replace Auggie with an official OpenAI backend
- build persistent cross-process conversation storage
- redesign unrelated model catalog or auth flows
- adopt every Auggie workspace or exchange endpoint immediately

## Success Criteria

The change is successful when all of the following are true:

1. `/v1/chat/completions` can continue a tool loop using assistant `tool_calls` plus tool messages.
2. `/v1/responses` full-inline tool loops produce the same semantic result expected by official OpenAI clients.
3. `/v1/responses` with `previous_response_id` and `function_call_output` works for Auggie-backed models.
4. Auggie `thinking` is surfaced as OpenAI reasoning output.
5. local behavior is driven by official OpenAI semantics instead of the old lossy bridge.
