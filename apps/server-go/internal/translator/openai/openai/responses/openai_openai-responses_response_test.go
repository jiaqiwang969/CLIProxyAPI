package responses

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func parseOpenAIResponsesSSEEvent(t *testing.T, chunk string) (string, gjson.Result) {
	t.Helper()

	lines := strings.Split(chunk, "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected SSE chunk: %q", chunk)
	}

	event := strings.TrimSpace(strings.TrimPrefix(lines[0], "event:"))
	dataLine := strings.TrimSpace(strings.TrimPrefix(lines[1], "data:"))
	if !gjson.Valid(dataLine) {
		t.Fatalf("invalid SSE data JSON: %q", dataLine)
	}
	return event, gjson.Parse(dataLine)
}

func assertOfficialDefaultResponseScaffold(t *testing.T, payload gjson.Result, prefix string, wantModel string) {
	t.Helper()

	path := func(field string) string {
		if prefix == "" {
			return field
		}
		return prefix + "." + field
	}

	if got := payload.Get(path("instructions")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("instructions"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("max_output_tokens")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("max_output_tokens"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("model")).String(); got != wantModel {
		t.Fatalf("%s = %q, want %q; payload=%s", path("model"), got, wantModel, payload.Raw)
	}
	if !payload.Get(path("parallel_tool_calls")).Bool() {
		t.Fatalf("%s missing/false; payload=%s", path("parallel_tool_calls"), payload.Raw)
	}
	if got := payload.Get(path("previous_response_id")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("previous_response_id"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("reasoning.effort")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("reasoning.effort"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("reasoning.summary")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("reasoning.summary"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("store")); !got.Exists() || !got.Bool() {
		t.Fatalf("%s = %s, want true; payload=%s", path("store"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("temperature")).Float(); got != 1 {
		t.Fatalf("%s = %v, want 1; payload=%s", path("temperature"), got, payload.Raw)
	}
	if got := payload.Get(path("text.format.type")).String(); got != "text" {
		t.Fatalf("%s = %q, want text; payload=%s", path("text.format.type"), got, payload.Raw)
	}
	if got := payload.Get(path("tool_choice")).String(); got != "auto" {
		t.Fatalf("%s = %q, want auto; payload=%s", path("tool_choice"), got, payload.Raw)
	}
	if got := payload.Get(path("tools.#")).Int(); got != 0 {
		t.Fatalf("%s = %d, want 0; payload=%s", path("tools.#"), got, payload.Raw)
	}
	if got := payload.Get(path("top_p")).Float(); got != 1 {
		t.Fatalf("%s = %v, want 1; payload=%s", path("top_p"), got, payload.Raw)
	}
	if got := payload.Get(path("truncation")).String(); got != "disabled" {
		t.Fatalf("%s = %q, want disabled; payload=%s", path("truncation"), got, payload.Raw)
	}
	if got := payload.Get(path("user")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("user"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("metadata")); got.Type != gjson.JSON || got.Raw != "{}" {
		t.Fatalf("%s = %s, want {}; payload=%s", path("metadata"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("usage")); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("usage"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("output_text")); got.Exists() {
		t.Fatalf("%s = %s, want field omitted; payload=%s", path("output_text"), got.Raw, payload.Raw)
	}
}

func TestPublicOpenAIResponsesID_StripsChatCompletionPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "hyphen", in: "chatcmpl-abc123", want: "resp_abc123"},
		{name: "underscore", in: "chatcmpl_legacy123", want: "resp_legacy123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := publicOpenAIResponsesID(tt.in); got != tt.want {
				t.Fatalf("publicOpenAIResponsesID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_LengthFinishReasonBecomesIncomplete(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","max_output_tokens":16}`),
		[]byte(`{
			"id":"chatcmpl_len_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"length"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, "status").String(); got != "incomplete" {
		t.Fatalf("status = %q, want incomplete; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "incomplete_details.reason").String(); got != "max_output_tokens" {
		t.Fatalf("incomplete_details.reason = %q, want max_output_tokens; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="message").content.0.text`).String(); got != "partial answer" {
		t.Fatalf("message output text = %q, want partial answer; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLengthFinishReasonEmitsIncompleteEvent(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","max_output_tokens":16}`),
		[]byte(`{"id":"chatcmpl_len_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"partial answer"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","max_output_tokens":16}`),
		[]byte(`{"id":"chatcmpl_len_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`),
		&param,
	)...)

	var (
		sawIncomplete bool
		sawCompleted  bool
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.incomplete":
			sawIncomplete = true
			if got := data.Get("response.status").String(); got != "incomplete" {
				t.Fatalf("response.status = %q, want incomplete; chunk=%s", got, chunk)
			}
			if got := data.Get("response.incomplete_details.reason").String(); got != "max_output_tokens" {
				t.Fatalf("incomplete_details.reason = %q, want max_output_tokens; chunk=%s", got, chunk)
			}
			if got := data.Get(`response.output.#(type=="message").content.0.text`).String(); got != "partial answer" {
				t.Fatalf("response.output message text = %q, want partial answer; chunk=%s", got, chunk)
			}
		case "response.completed":
			sawCompleted = true
		}
	}

	if !sawIncomplete {
		t.Fatalf("missing response.incomplete event: %v", chunks)
	}
	if sawCompleted {
		t.Fatalf("unexpected response.completed event when finish_reason=length: %v", chunks)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_ContentFilterFinishReasonBecomesIncomplete(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{
			"id":"chatcmpl_cf_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"filtered answer"},"finish_reason":"content_filter"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, "status").String(); got != "incomplete" {
		t.Fatalf("status = %q, want incomplete; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "incomplete_details.reason").String(); got != "content_filter" {
		t.Fatalf("incomplete_details.reason = %q, want content_filter; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="message").content.0.text`).String(); got != "filtered answer" {
		t.Fatalf("message output text = %q, want filtered answer; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamContentFilterFinishReasonEmitsIncompleteEvent(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_cf_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"filtered answer"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_cf_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`),
		&param,
	)...)

	var (
		sawIncomplete bool
		sawCompleted  bool
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.incomplete":
			sawIncomplete = true
			if got := data.Get("response.status").String(); got != "incomplete" {
				t.Fatalf("response.status = %q, want incomplete; chunk=%s", got, chunk)
			}
			if got := data.Get("response.incomplete_details.reason").String(); got != "content_filter" {
				t.Fatalf("incomplete_details.reason = %q, want content_filter; chunk=%s", got, chunk)
			}
			if got := data.Get(`response.output.#(type=="message").content.0.text`).String(); got != "filtered answer" {
				t.Fatalf("response.output message text = %q, want filtered answer; chunk=%s", got, chunk)
			}
		case "response.completed":
			sawCompleted = true
		}
	}

	if !sawIncomplete {
		t.Fatalf("missing response.incomplete event: %v", chunks)
	}
	if sawCompleted {
		t.Fatalf("unexpected response.completed event when finish_reason=content_filter: %v", chunks)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamMultipleToolCallsPreservesAllFunctionCalls(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","parallel_tool_calls":true}`),
		[]byte(`{"id":"chatcmpl_tools_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_alpha","type":"function","function":{"name":"alpha","arguments":"{\"city\":\"Boston\"}"}},{"index":1,"id":"call_beta","type":"function","function":{"name":"beta","arguments":"{\"city\":\"Tokyo\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","parallel_tool_calls":true}`),
		[]byte(`{"id":"chatcmpl_tools_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		functionItemsAdded []gjson.Result
		functionArgsDelta  []gjson.Result
		completed          gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			if data.Get("item.type").String() == "function_call" {
				functionItemsAdded = append(functionItemsAdded, data)
			}
		case "response.function_call_arguments.delta":
			functionArgsDelta = append(functionArgsDelta, data)
		case "response.completed":
			completed = data
		}
	}

	if len(functionItemsAdded) != 2 {
		t.Fatalf("function output_item.added count = %d, want 2; chunks=%v", len(functionItemsAdded), chunks)
	}
	if got := functionItemsAdded[0].Get("item.call_id").String(); got != "call_alpha" {
		t.Fatalf("first function call_id = %q, want call_alpha", got)
	}
	if got := functionItemsAdded[1].Get("item.call_id").String(); got != "call_beta" {
		t.Fatalf("second function call_id = %q, want call_beta", got)
	}

	if len(functionArgsDelta) != 2 {
		t.Fatalf("function_call_arguments.delta count = %d, want 2; chunks=%v", len(functionArgsDelta), chunks)
	}
	if got := functionArgsDelta[0].Get("item_id").String(); got != "fc_call_alpha" {
		t.Fatalf("first function delta item_id = %q, want fc_call_alpha", got)
	}
	if got := functionArgsDelta[1].Get("item_id").String(); got != "fc_call_beta" {
		t.Fatalf("second function delta item_id = %q, want fc_call_beta", got)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	functionCallCount := 0
	for _, item := range completed.Get("response.output").Array() {
		if item.Get("type").String() == "function_call" {
			functionCallCount++
		}
	}
	if functionCallCount != 2 {
		t.Fatalf("completed function_call count = %d, want 2; completed=%s", functionCallCount, completed.Raw)
	}
	if got := completed.Get(`response.output.#(call_id=="call_alpha").name`).String(); got != "alpha" {
		t.Fatalf("completed alpha name = %q, want alpha; completed=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(call_id=="call_beta").name`).String(); got != "beta" {
		t.Fatalf("completed beta name = %q, want beta; completed=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(call_id=="call_alpha").arguments`).String(); !strings.Contains(got, "Boston") {
		t.Fatalf("completed alpha arguments = %q, want Boston; completed=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(call_id=="call_beta").arguments`).String(); !strings.Contains(got, "Tokyo") {
		t.Fatalf("completed beta arguments = %q, want Tokyo; completed=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_WebSearchCallIncludesRequestedSourcesAndResults(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","include":["web_search_call.results","web_search_call.action.sources"]}`),
		[]byte(`{
			"id":"chatcmpl_web_nonstream_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"_cliproxy_builtin_tool_outputs":[
				{
					"id":"ws_call_1",
					"type":"web_search_call",
					"status":"completed",
					"query":"OpenAI latest news",
					"output":"- [OpenAI News](https://openai.com/news/) Latest updates from OpenAI\\n- [OpenAI Blog](https://openai.com/blog/) Product announcements"
				}
			],
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"Top headline: OpenAI News"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="web_search_call").action.type`).String(); got != "search" {
		t.Fatalf("web_search_call action.type = %q, want search; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").action.query`).String(); got != "OpenAI latest news" {
		t.Fatalf("web_search_call action.query = %q, want OpenAI latest news; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").action.queries.0`).String(); got != "OpenAI latest news" {
		t.Fatalf("web_search_call action.queries[0] = %q, want OpenAI latest news; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").action.sources.0.type`).String(); got != "url" {
		t.Fatalf("web_search_call action.sources[0].type = %q, want url; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").action.sources.0.url`).String(); got != "https://openai.com/news/" {
		t.Fatalf("web_search_call action.sources[0].url = %q, want https://openai.com/news/; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").results.#`).Int(); got != 2 {
		t.Fatalf("web_search_call results count = %d, want 2; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").results.0.title`).String(); got != "OpenAI News" {
		t.Fatalf("web_search_call results[0].title = %q, want OpenAI News; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").results.0.url`).String(); got != "https://openai.com/news/" {
		t.Fatalf("web_search_call results[0].url = %q, want https://openai.com/news/; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="web_search_call").results.0.text`).String(); got != "Latest updates from OpenAI" {
		t.Fatalf("web_search_call results[0].text = %q, want Latest updates from OpenAI; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamWebSearchCallDoneIncludesRequestedSourcesAndResults(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","include":["web_search_call.results","web_search_call.action.sources"]}`),
		[]byte(`{
			"id":"chatcmpl_web_stream_1",
			"object":"chat.completion.chunk",
			"created":1741478400,
			"model":"gpt-5",
			"_cliproxy_builtin_tool_outputs":[
				{
					"id":"ws_call_1",
					"type":"web_search_call",
					"status":"completed",
					"query":"OpenAI latest news",
					"output":"- [OpenAI News](https://openai.com/news/) Latest updates from OpenAI"
				}
			],
			"choices":[{"index":0,"delta":{"role":"assistant","content":"Top headline: OpenAI News"},"finish_reason":null}]
		}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","include":["web_search_call.results","web_search_call.action.sources"]}`),
		[]byte(`{"id":"chatcmpl_web_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	var (
		added     gjson.Result
		done      gjson.Result
		completed gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			if data.Get("item.type").String() == "web_search_call" {
				added = data
			}
		case "response.output_item.done":
			if data.Get("item.type").String() == "web_search_call" {
				done = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !added.Exists() {
		t.Fatalf("missing web_search_call output_item.added event: %v", chunks)
	}
	if got := added.Get("item.action.type").String(); got != "search" {
		t.Fatalf("output_item.added item.action.type = %q, want search; chunk=%s", got, added.Raw)
	}
	if got := added.Get("item.action.query").String(); got != "OpenAI latest news" {
		t.Fatalf("output_item.added item.action.query = %q, want OpenAI latest news; chunk=%s", got, added.Raw)
	}

	if !done.Exists() {
		t.Fatalf("missing web_search_call output_item.done event: %v", chunks)
	}
	if got := done.Get("item.action.sources.0.url").String(); got != "https://openai.com/news/" {
		t.Fatalf("output_item.done item.action.sources[0].url = %q, want https://openai.com/news/; chunk=%s", got, done.Raw)
	}
	if got := done.Get("item.results.0.title").String(); got != "OpenAI News" {
		t.Fatalf("output_item.done item.results[0].title = %q, want OpenAI News; chunk=%s", got, done.Raw)
	}
	if got := done.Get("item.results.0.text").String(); got != "Latest updates from OpenAI" {
		t.Fatalf("output_item.done item.results[0].text = %q, want Latest updates from OpenAI; chunk=%s", got, done.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get(`response.output.#(type=="web_search_call").action.sources.0.url`).String(); got != "https://openai.com/news/" {
		t.Fatalf("response.output web_search_call action.sources[0].url = %q, want https://openai.com/news/; chunk=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(type=="web_search_call").results.0.title`).String(); got != "OpenAI News" {
		t.Fatalf("response.output web_search_call results[0].title = %q, want OpenAI News; chunk=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamReasoningAndFunctionCallUseDistinctOutputIndices(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"reasoning_content":"Need a tool before answering."},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Boston\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		reasoningAdded gjson.Result
		reasoningDone  gjson.Result
		functionAdded  gjson.Result
		completed      gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			switch data.Get("item.type").String() {
			case "reasoning":
				reasoningAdded = data
			case "function_call":
				functionAdded = data
			}
		case "response.output_item.done":
			if data.Get("item.type").String() == "reasoning" {
				reasoningDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !reasoningAdded.Exists() {
		t.Fatalf("missing reasoning output_item.added: %v", chunks)
	}
	if !functionAdded.Exists() {
		t.Fatalf("missing function_call output_item.added: %v", chunks)
	}
	if got := reasoningAdded.Get("output_index").Int(); got != 0 {
		t.Fatalf("reasoning output_index = %d, want 0; chunk=%s", got, reasoningAdded.Raw)
	}
	if got := functionAdded.Get("output_index").Int(); got != 1 {
		t.Fatalf("function_call output_index = %d, want 1; chunk=%s", got, functionAdded.Raw)
	}
	if !reasoningDone.Exists() {
		t.Fatalf("missing reasoning output_item.done: %v", chunks)
	}
	if got := reasoningDone.Get("item.status").String(); got != "completed" {
		t.Fatalf("reasoning output_item.done status = %q, want completed; chunk=%s", got, reasoningDone.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get("response.output.0.type").String(); got != "reasoning" {
		t.Fatalf("response.output[0].type = %q, want reasoning; completed=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.0.status").String(); got != "completed" {
		t.Fatalf("response.output[0].status = %q, want completed; completed=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.1.type").String(); got != "function_call" {
		t.Fatalf("response.output[1].type = %q, want function_call; completed=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.1.call_id").String(); got != "call_weather" {
		t.Fatalf("response.output[1].call_id = %q, want call_weather; completed=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_ToolCallWithoutReasoningContentDoesNotSynthesizeReasoningItem(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","tools":[{"type":"function","name":"pwd","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`),
		[]byte(`{
			"id":"chatcmpl_tool_reasoning_placeholder_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_pwd","type":"function","function":{"name":"pwd","arguments":"{\"command\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="reasoning")`); got.Exists() {
		t.Fatalf("unexpected synthesized reasoning output: %s; resp=%s", got.Raw, resp)
	}
	if got := gjson.Get(resp, "output.0.type").String(); got != "function_call" {
		t.Fatalf("output[0].type = %q, want function_call; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "output.0.call_id").String(); got != "call_pwd" {
		t.Fatalf("output[0].call_id = %q, want call_pwd; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamToolCallWithoutReasoningContentDoesNotSynthesizeReasoningItem(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","tools":[{"type":"function","name":"pwd","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`),
		[]byte(`{"id":"chatcmpl_tool_reasoning_placeholder_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_pwd","type":"function","function":{"name":"pwd","arguments":"{\"command\":\"pwd\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","tools":[{"type":"function","name":"pwd","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`),
		[]byte(`{"id":"chatcmpl_tool_reasoning_placeholder_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		reasoningAdded gjson.Result
		reasoningDone  gjson.Result
		functionAdded  gjson.Result
		completed      gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			switch data.Get("item.type").String() {
			case "reasoning":
				reasoningAdded = data
			case "function_call":
				functionAdded = data
			}
		case "response.output_item.done":
			if data.Get("item.type").String() == "reasoning" {
				reasoningDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if reasoningAdded.Exists() {
		t.Fatalf("unexpected reasoning output_item.added: %s; chunks=%v", reasoningAdded.Raw, chunks)
	}
	if reasoningDone.Exists() {
		t.Fatalf("unexpected reasoning output_item.done: %s; chunks=%v", reasoningDone.Raw, chunks)
	}
	if !functionAdded.Exists() {
		t.Fatalf("missing function_call output_item.added: %v", chunks)
	}
	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get("response.output.0.type").String(); got != "function_call" {
		t.Fatalf("response.output[0].type = %q, want function_call; completed=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(type=="reasoning")`); got.Exists() {
		t.Fatalf("unexpected synthesized reasoning in completed response: %s; completed=%s", got.Raw, completed.Raw)
	}
	if got := completed.Get("response.output.0.call_id").String(); got != "call_pwd" {
		t.Fatalf("response.output[0].call_id = %q, want call_pwd; completed=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_CustomToolCallUsesOfficialCustomItemShape(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","tools":[{"type":"custom","name":"bash","description":"Run shell commands"}]}`),
		[]byte(`{
			"id":"chatcmpl_custom_tool_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_bash","type":"function","function":{"name":"bash","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, "output.0.type").String(); got != "custom_tool_call" {
		t.Fatalf("output[0].type = %q, want custom_tool_call; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "output.0.call_id").String(); got != "call_bash" {
		t.Fatalf("output[0].call_id = %q, want call_bash; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "output.0.name").String(); got != "bash" {
		t.Fatalf("output[0].name = %q, want bash; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "output.0.input").String(); got != "pwd" {
		t.Fatalf("output[0].input = %q, want pwd; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "output.0.arguments"); got.Exists() {
		t.Fatalf("output[0].arguments unexpectedly present = %s; resp=%s", got.Raw, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamCustomToolCallUsesOfficialCustomEvents(t *testing.T) {
	var param any

	request := []byte(`{"model":"gpt-5","tools":[{"type":"custom","name":"bash","description":"Run shell commands"}]}`)

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_custom_tool_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_bash","type":"function","function":{"name":"bash","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_custom_tool_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		customAdded gjson.Result
		customDelta gjson.Result
		customDone  gjson.Result
		itemDone    gjson.Result
		completed   gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			if data.Get("item.type").String() == "custom_tool_call" {
				customAdded = data
			}
		case "response.custom_tool_call_input.delta":
			customDelta = data
		case "response.custom_tool_call_input.done":
			customDone = data
		case "response.output_item.done":
			if data.Get("item.type").String() == "custom_tool_call" {
				itemDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !customAdded.Exists() {
		t.Fatalf("missing custom_tool_call output_item.added: %v", chunks)
	}
	if got := customAdded.Get("item.call_id").String(); got != "call_bash" {
		t.Fatalf("output_item.added item.call_id = %q, want call_bash; chunk=%s", got, customAdded.Raw)
	}
	if !customDelta.Exists() {
		t.Fatalf("missing response.custom_tool_call_input.delta event: %v", chunks)
	}
	if got := customDelta.Get("delta").String(); got != "pwd" {
		t.Fatalf("custom_tool_call_input.delta = %q, want pwd; chunk=%s", got, customDelta.Raw)
	}
	if !customDone.Exists() {
		t.Fatalf("missing response.custom_tool_call_input.done event: %v", chunks)
	}
	if got := customDone.Get("input").String(); got != "pwd" {
		t.Fatalf("custom_tool_call_input.done input = %q, want pwd; chunk=%s", got, customDone.Raw)
	}
	if customDone.Get("name").Exists() {
		t.Fatalf("response.custom_tool_call_input.done unexpectedly included name: %s", customDone.Raw)
	}
	if !itemDone.Exists() {
		t.Fatalf("missing custom_tool_call output_item.done: %v", chunks)
	}
	if got := itemDone.Get("item.input").String(); got != "pwd" {
		t.Fatalf("output_item.done item.input = %q, want pwd; chunk=%s", got, itemDone.Raw)
	}
	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get("response.output.0.type").String(); got != "custom_tool_call" {
		t.Fatalf("response.output[0].type = %q, want custom_tool_call; completed=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.0.input").String(); got != "pwd" {
		t.Fatalf("response.output[0].input = %q, want pwd; completed=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_RequestReasoningWithoutReasoningContentDoesNotSynthesizeReasoningItem(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"},"tools":[{"type":"function","name":"pwd","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`),
		[]byte(`{
			"id":"chatcmpl_tool_reasoning_request_only_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_pwd","type":"function","function":{"name":"pwd","arguments":"{\"command\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="reasoning")`); got.Exists() {
		t.Fatalf("unexpected synthesized reasoning output from request-only reasoning config: %s; resp=%s", got.Raw, resp)
	}
	if got := gjson.Get(resp, "output.0.type").String(); got != "function_call" {
		t.Fatalf("output[0].type = %q, want function_call; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, "reasoning.effort").String(); got != "medium" {
		t.Fatalf("reasoning.effort = %q, want medium in response scaffold; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamRequestReasoningWithoutReasoningContentDoesNotSynthesizeReasoningItem(t *testing.T) {
	var param any

	request := []byte(`{"model":"gpt-5","reasoning":{"effort":"medium"},"tools":[{"type":"function","name":"pwd","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`)

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_tool_reasoning_request_only_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_pwd","type":"function","function":{"name":"pwd","arguments":"{\"command\":\"pwd\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_tool_reasoning_request_only_stream_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		reasoningAdded gjson.Result
		reasoningDone  gjson.Result
		functionAdded  gjson.Result
		completed      gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			switch data.Get("item.type").String() {
			case "reasoning":
				reasoningAdded = data
			case "function_call":
				functionAdded = data
			}
		case "response.output_item.done":
			if data.Get("item.type").String() == "reasoning" {
				reasoningDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if reasoningAdded.Exists() {
		t.Fatalf("unexpected reasoning output_item.added from request-only reasoning config: %s; chunks=%v", reasoningAdded.Raw, chunks)
	}
	if reasoningDone.Exists() {
		t.Fatalf("unexpected reasoning output_item.done from request-only reasoning config: %s; chunks=%v", reasoningDone.Raw, chunks)
	}
	if !functionAdded.Exists() {
		t.Fatalf("missing function_call output_item.added: %v", chunks)
	}
	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get("response.output.0.type").String(); got != "function_call" {
		t.Fatalf("response.output[0].type = %q, want function_call; completed=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(type=="reasoning")`); got.Exists() {
		t.Fatalf("unexpected synthesized reasoning in completed response from request-only reasoning config: %s; completed=%s", got.Raw, completed.Raw)
	}
	if got := completed.Get("response.reasoning.effort").String(); got != "medium" {
		t.Fatalf("response.reasoning.effort = %q, want medium in scaffold; completed=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_ReasoningItemsIncludeCompletedStatus(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{
			"id":"chatcmpl_reasoning_status_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"Final answer","reasoning_content":"Need a tool before answering."},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="reasoning").status`).String(); got != "completed" {
		t.Fatalf("reasoning status = %q, want completed; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamReasoningContentEmitsOfficialReasoningTextEvents(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_text_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"reasoning_content":"Need a tool before answering."},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_text_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	var (
		contentPartAdded gjson.Result
		reasoningDelta   gjson.Result
		reasoningDone    gjson.Result
		contentPartDone  gjson.Result
		completed        gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.content_part.added":
			if data.Get("part.type").String() == "reasoning_text" {
				contentPartAdded = data
			}
		case "response.reasoning_text.delta":
			reasoningDelta = data
		case "response.reasoning_text.done":
			reasoningDone = data
		case "response.content_part.done":
			if data.Get("part.type").String() == "reasoning_text" {
				contentPartDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !contentPartAdded.Exists() {
		t.Fatalf("missing response.content_part.added reasoning_text event: %v", chunks)
	}
	if got := contentPartAdded.Get("part.type").String(); got != "reasoning_text" {
		t.Fatalf("content_part.added part.type = %q, want reasoning_text; chunk=%s", got, contentPartAdded.Raw)
	}

	if !reasoningDelta.Exists() {
		t.Fatalf("missing response.reasoning_text.delta event: %v", chunks)
	}
	if got := reasoningDelta.Get("delta").String(); got != "Need a tool before answering." {
		t.Fatalf("reasoning_text.delta = %q, want full reasoning text; chunk=%s", got, reasoningDelta.Raw)
	}

	if !reasoningDone.Exists() {
		t.Fatalf("missing response.reasoning_text.done event: %v", chunks)
	}
	if got := reasoningDone.Get("text").String(); got != "Need a tool before answering." {
		t.Fatalf("reasoning_text.done text = %q, want full reasoning text; chunk=%s", got, reasoningDone.Raw)
	}

	if !contentPartDone.Exists() {
		t.Fatalf("missing response.content_part.done reasoning_text event: %v", chunks)
	}
	if got := contentPartDone.Get("part.text").String(); got != "Need a tool before answering." {
		t.Fatalf("content_part.done part.text = %q, want full reasoning text; chunk=%s", got, contentPartDone.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if completed.Get("response.output.0.encrypted_content").Exists() {
		t.Fatalf("response.output[0].encrypted_content unexpectedly present without include param; completed=%s", completed.Raw)
	}
	if got := completed.Get("response.output.0.content.0.type").String(); got != "reasoning_text" {
		t.Fatalf("response.output[0].content[0].type = %q, want reasoning_text; completed=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.0.content.0.text").String(); got != "Need a tool before answering." {
		t.Fatalf("response.output[0].content[0].text = %q, want full reasoning text; completed=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_ReasoningItemsIncludeContent(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{
			"id":"chatcmpl_reasoning_content_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"Final answer","reasoning_content":"Need a tool before answering."},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="reasoning").content.0.type`).String(); got != "reasoning_text" {
		t.Fatalf("reasoning content type = %q, want reasoning_text; resp=%s", got, resp)
	}
	if got := gjson.Get(resp, `output.#(type=="reasoning").content.0.text`).String(); got != "Need a tool before answering." {
		t.Fatalf("reasoning content text = %q, want full reasoning text; resp=%s", got, resp)
	}
	if gjson.Get(resp, `output.#(type=="reasoning").encrypted_content`).Exists() {
		t.Fatalf("reasoning encrypted_content unexpectedly present without include param; resp=%s", resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_ReasoningItemsPreserveNativeID(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{
			"id":"chatcmpl_reasoning_content_native_id_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"Final answer","reasoning_content":"Need a tool before answering.","reasoning_item_id":"rs_native_nonstream_1"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="reasoning").id`).String(); got != "rs_native_nonstream_1" {
		t.Fatalf("reasoning id = %q, want rs_native_nonstream_1; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_ReasoningItemsIncludeEncryptedContentWhenRequested(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"},"include":["reasoning.encrypted_content"]}`),
		[]byte(`{
			"id":"chatcmpl_reasoning_content_2",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"Final answer","reasoning_content":"Need a tool before answering.","reasoning_encrypted_content":"enc:reasoning:nonstream"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, `output.#(type=="reasoning").encrypted_content`).String(); got != "enc:reasoning:nonstream" {
		t.Fatalf("reasoning encrypted_content = %q, want enc:reasoning:nonstream; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamReasoningDoneOmitsEncryptedContentByDefault(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_encrypted_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"reasoning_content":"Need a tool before answering."},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_encrypted_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event != "response.output_item.done" || data.Get("item.type").String() != "reasoning" {
			continue
		}
		if data.Get("item.encrypted_content").Exists() {
			t.Fatalf("reasoning output_item.done unexpectedly included encrypted_content without include param; chunk=%s", data.Raw)
		}
		return
	}

	t.Fatalf("missing reasoning response.output_item.done event: %v", chunks)
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamReasoningDonePreservesNativeID(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_native_id_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"reasoning_content":"Need a tool before answering.","reasoning_item_id":"rs_native_stream_1"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"}}`),
		[]byte(`{"id":"chatcmpl_reasoning_native_id_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	var (
		itemDone  gjson.Result
		completed gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.done":
			if data.Get("item.type").String() == "reasoning" {
				itemDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !itemDone.Exists() {
		t.Fatalf("missing reasoning response.output_item.done event: %v", chunks)
	}
	if got := itemDone.Get("item.id").String(); got != "rs_native_stream_1" {
		t.Fatalf("reasoning output_item.done id = %q, want rs_native_stream_1; chunk=%s", got, itemDone.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get(`response.output.#(type=="reasoning").id`).String(); got != "rs_native_stream_1" {
		t.Fatalf("response.completed reasoning id = %q, want rs_native_stream_1; chunk=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamReasoningDoneIncludesEncryptedContentWhenRequested(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"},"include":["reasoning.encrypted_content"]}`),
		[]byte(`{"id":"chatcmpl_reasoning_encrypted_2","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"reasoning_content":"Need a tool before answering.","reasoning_encrypted_content":"enc:reasoning:stream"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","reasoning":{"effort":"medium"},"include":["reasoning.encrypted_content"]}`),
		[]byte(`{"id":"chatcmpl_reasoning_encrypted_2","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	var (
		itemDone  gjson.Result
		completed gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.done":
			if data.Get("item.type").String() == "reasoning" {
				itemDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !itemDone.Exists() {
		t.Fatalf("missing reasoning response.output_item.done event: %v", chunks)
	}
	if got := itemDone.Get("item.encrypted_content").String(); got != "enc:reasoning:stream" {
		t.Fatalf("reasoning output_item.done encrypted_content = %q, want enc:reasoning:stream; chunk=%s", got, itemDone.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get(`response.output.#(type=="reasoning").encrypted_content`).String(); got != "enc:reasoning:stream" {
		t.Fatalf("response.completed reasoning encrypted_content = %q, want enc:reasoning:stream; chunk=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_FunctionCallArgumentsDoneIncludesFunctionName(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_fc_name_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather_name","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Boston\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_fc_name_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		addedEvent gjson.Result
		deltaEvent gjson.Result
		doneEvent  gjson.Result
		itemDone   gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.added":
			if data.Get("item.type").String() == "function_call" {
				addedEvent = data
			}
		case "response.function_call_arguments.delta":
			deltaEvent = data
		case "response.function_call_arguments.done":
			doneEvent = data
		case "response.output_item.done":
			if data.Get("item.type").String() == "function_call" {
				itemDone = data
			}
		}
	}

	if !addedEvent.Exists() {
		t.Fatalf("missing response.output_item.added event for function call: %v", chunks)
	}
	if addedEvent.Get("response_id").Exists() {
		t.Fatalf("added event unexpectedly included response_id: %s", addedEvent.Raw)
	}

	if !deltaEvent.Exists() {
		t.Fatalf("missing response.function_call_arguments.delta event: %v", chunks)
	}
	if deltaEvent.Get("response_id").Exists() {
		t.Fatalf("delta event unexpectedly included response_id: %s", deltaEvent.Raw)
	}

	if !doneEvent.Exists() {
		t.Fatalf("missing response.function_call_arguments.done event: %v", chunks)
	}
	if doneEvent.Get("response_id").Exists() {
		t.Fatalf("done event unexpectedly included response_id: %s", doneEvent.Raw)
	}
	if got := doneEvent.Get("name").String(); got != "get_weather" {
		t.Fatalf("done event name = %q, want get_weather; event=%s", got, doneEvent.Raw)
	}

	if !itemDone.Exists() {
		t.Fatalf("missing response.output_item.done event for function call: %v", chunks)
	}
	if itemDone.Get("response_id").Exists() {
		t.Fatalf("item.done unexpectedly included response_id: %s", itemDone.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamOutputTextDeltaIncludesObfuscationByDefault(t *testing.T) {
	var param any

	chunks := ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_obf_text_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)

	var deltaEvent gjson.Result
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event == "response.output_text.delta" {
			deltaEvent = data
			break
		}
	}

	if !deltaEvent.Exists() {
		t.Fatalf("missing response.output_text.delta event: %v", chunks)
	}
	if got := deltaEvent.Get("obfuscation").String(); got == "" {
		t.Fatalf("response.output_text.delta obfuscation = %q, want non-empty; event=%s", got, deltaEvent.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamFunctionCallArgumentsDeltaIncludesObfuscationByDefault(t *testing.T) {
	var param any

	chunks := ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_obf_fc_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_obf_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Boston\"}"}}]},"finish_reason":null}]}`),
		&param,
	)

	var deltaEvent gjson.Result
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event == "response.function_call_arguments.delta" {
			deltaEvent = data
			break
		}
	}

	if !deltaEvent.Exists() {
		t.Fatalf("missing response.function_call_arguments.delta event: %v", chunks)
	}
	if got := deltaEvent.Get("obfuscation").String(); got == "" {
		t.Fatalf("response.function_call_arguments.delta obfuscation = %q, want non-empty; event=%s", got, deltaEvent.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamFunctionCallArgumentsDeltaOmitsObfuscationWhenDisabled(t *testing.T) {
	var param any

	chunks := ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","stream_options":{"include_obfuscation":false}}`),
		[]byte(`{"id":"chatcmpl_obf_fc_2","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_obf_weather_2","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Boston\"}"}}]},"finish_reason":null}]}`),
		&param,
	)

	var deltaEvent gjson.Result
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event == "response.function_call_arguments.delta" {
			deltaEvent = data
			break
		}
	}

	if !deltaEvent.Exists() {
		t.Fatalf("missing response.function_call_arguments.delta event: %v", chunks)
	}
	if got := deltaEvent.Get("obfuscation"); got.Exists() {
		t.Fatalf("response.function_call_arguments.delta obfuscation = %s, want field omitted; event=%s", got.Raw, deltaEvent.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLifecycleOmitsSDKOnlyOutputTextFromResponseObjects(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_output_text_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello world"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_output_text_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	var (
		created    gjson.Result
		inProgress gjson.Result
		completed  gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.created":
			created = data
		case "response.in_progress":
			inProgress = data
		case "response.completed":
			completed = data
		}
	}

	if !created.Exists() {
		t.Fatalf("missing response.created event: %v", chunks)
	}
	if got := created.Get("response.output_text"); got.Exists() {
		t.Fatalf("response.created output_text = %s, want field omitted; event=%s", got.Raw, created.Raw)
	}

	if !inProgress.Exists() {
		t.Fatalf("missing response.in_progress event: %v", chunks)
	}
	if got := inProgress.Get("response.output_text"); got.Exists() {
		t.Fatalf("response.in_progress output_text = %s, want field omitted; event=%s", got.Raw, inProgress.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get("response.output_text"); got.Exists() {
		t.Fatalf("response.completed output_text = %s, want field omitted; event=%s", got.Raw, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_OmitsSDKOnlyOutputText(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{
			"id":"chatcmpl_output_text_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, "output_text"); got.Exists() {
		t.Fatalf("output_text = %s, want field omitted; resp=%s", got.Raw, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLifecycleEchoesRequestFields(t *testing.T) {
	var param any

	request := []byte(`{
		"model":"gpt-5",
		"instructions":"Be terse",
		"parallel_tool_calls":true,
		"tool_choice":"auto",
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],
		"temperature":0.2,
		"top_p":0.9,
		"metadata":{"trace_id":"trace-1"}
	}`)

	chunks := ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)

	var (
		created    gjson.Result
		inProgress gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.created":
			created = data
		case "response.in_progress":
			inProgress = data
		}
	}

	if !created.Exists() {
		t.Fatalf("missing response.created event: %v", chunks)
	}
	if got := created.Get("response.model").String(); got != "gpt-5" {
		t.Fatalf("response.created model = %q, want gpt-5; event=%s", got, created.Raw)
	}
	if got := created.Get("response.instructions").String(); got != "Be terse" {
		t.Fatalf("response.created instructions = %q, want Be terse; event=%s", got, created.Raw)
	}
	if !created.Get("response.parallel_tool_calls").Bool() {
		t.Fatalf("response.created parallel_tool_calls missing/false; event=%s", created.Raw)
	}
	if got := created.Get("response.tool_choice").String(); got != "auto" {
		t.Fatalf("response.created tool_choice = %q, want auto; event=%s", got, created.Raw)
	}
	if got := created.Get("response.tools.0.name").String(); got != "get_weather" {
		t.Fatalf("response.created tools[0].name = %q, want get_weather; event=%s", got, created.Raw)
	}
	if got := created.Get("response.temperature").Float(); got != 0.2 {
		t.Fatalf("response.created temperature = %v, want 0.2; event=%s", got, created.Raw)
	}
	if got := created.Get("response.top_p").Float(); got != 0.9 {
		t.Fatalf("response.created top_p = %v, want 0.9; event=%s", got, created.Raw)
	}
	if got := created.Get("response.metadata.trace_id").String(); got != "trace-1" {
		t.Fatalf("response.created metadata.trace_id = %q, want trace-1; event=%s", got, created.Raw)
	}

	if !inProgress.Exists() {
		t.Fatalf("missing response.in_progress event: %v", chunks)
	}
	if got := inProgress.Get("response.model").String(); got != "gpt-5" {
		t.Fatalf("response.in_progress model = %q, want gpt-5; event=%s", got, inProgress.Raw)
	}
	if got := inProgress.Get("response.instructions").String(); got != "Be terse" {
		t.Fatalf("response.in_progress instructions = %q, want Be terse; event=%s", got, inProgress.Raw)
	}
	if !inProgress.Get("response.parallel_tool_calls").Bool() {
		t.Fatalf("response.in_progress parallel_tool_calls missing/false; event=%s", inProgress.Raw)
	}
	if got := inProgress.Get("response.tool_choice").String(); got != "auto" {
		t.Fatalf("response.in_progress tool_choice = %q, want auto; event=%s", got, inProgress.Raw)
	}
	if got := inProgress.Get("response.tools.0.name").String(); got != "get_weather" {
		t.Fatalf("response.in_progress tools[0].name = %q, want get_weather; event=%s", got, inProgress.Raw)
	}
	if got := inProgress.Get("response.temperature").Float(); got != 0.2 {
		t.Fatalf("response.in_progress temperature = %v, want 0.2; event=%s", got, inProgress.Raw)
	}
	if got := inProgress.Get("response.top_p").Float(); got != 0.9 {
		t.Fatalf("response.in_progress top_p = %v, want 0.9; event=%s", got, inProgress.Raw)
	}
	if got := inProgress.Get("response.metadata.trace_id").String(); got != "trace-1" {
		t.Fatalf("response.in_progress metadata.trace_id = %q, want trace-1; event=%s", got, inProgress.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamSequenceStartsAtZero(t *testing.T) {
	var param any

	chunks := ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_seq_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)

	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want at least 2; chunks=%v", len(chunks), chunks)
	}

	createdEvent, created := parseOpenAIResponsesSSEEvent(t, chunks[0])
	if createdEvent != "response.created" {
		t.Fatalf("first event = %q, want response.created; chunk=%s", createdEvent, chunks[0])
	}
	if got := created.Get("sequence_number").Int(); got != 0 {
		t.Fatalf("response.created sequence_number = %d, want 0; chunk=%s", got, created.Raw)
	}

	inProgressEvent, inProgress := parseOpenAIResponsesSSEEvent(t, chunks[1])
	if inProgressEvent != "response.in_progress" {
		t.Fatalf("second event = %q, want response.in_progress; chunk=%s", inProgressEvent, chunks[1])
	}
	if got := inProgress.Get("sequence_number").Int(); got != 1 {
		t.Fatalf("response.in_progress sequence_number = %d, want 1; chunk=%s", got, inProgress.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLifecycleNormalizesResponsesFunctionToolsInScaffold(t *testing.T) {
	var param any

	request := []byte(`{
		"model":"gpt-5",
		"tool_choice":"required",
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"},"unit":{"type":"string"}},"required":["city"]}}]
	}`)

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_scaffold_tools_strict_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Boston\"}"}}]},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_scaffold_tools_strict_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`),
		&param,
	)...)

	var (
		created    gjson.Result
		inProgress gjson.Result
		completed  gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.created":
			created = data
		case "response.in_progress":
			inProgress = data
		case "response.completed":
			completed = data
		}
	}

	for _, payload := range []struct {
		name string
		data gjson.Result
	}{
		{name: "created", data: created},
		{name: "in_progress", data: inProgress},
		{name: "completed", data: completed},
	} {
		if !payload.data.Exists() {
			t.Fatalf("missing response.%s event: %v", payload.name, chunks)
		}
		if got := payload.data.Get("response.tools.0.strict"); got.Type != gjson.True {
			t.Fatalf("response.%s tools[0].strict = %s, want true; event=%s", payload.name, got.Raw, payload.data.Raw)
		}
		if got := payload.data.Get("response.tools.0.parameters.additionalProperties"); got.Type != gjson.False {
			t.Fatalf("response.%s tools[0].parameters.additionalProperties = %s, want false; event=%s", payload.name, got.Raw, payload.data.Raw)
		}
		if got := payload.data.Get("response.tools.0.parameters.required.#").Int(); got != 2 {
			t.Fatalf("response.%s tools[0].parameters.required length = %d, want 2; event=%s", payload.name, got, payload.data.Raw)
		}
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_NormalizesResponsesFunctionToolsInScaffold(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{
			"model":"gpt-5",
			"tool_choice":"required",
			"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"},"unit":{"type":"string"}},"required":["city"]}}]
		}`),
		[]byte(`{
			"id":"chatcmpl_scaffold_tools_strict_nonstream_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Boston\"}"}}]},"finish_reason":"tool_calls"}
			]
		}`),
		nil,
	)

	parsed := gjson.Parse(resp)
	if got := parsed.Get("tools.0.strict"); got.Type != gjson.True {
		t.Fatalf("tools[0].strict = %s, want true; resp=%s", got.Raw, resp)
	}
	if got := parsed.Get("tools.0.parameters.additionalProperties"); got.Type != gjson.False {
		t.Fatalf("tools[0].parameters.additionalProperties = %s, want false; resp=%s", got.Raw, resp)
	}
	if got := parsed.Get("tools.0.parameters.required.#").Int(); got != 2 {
		t.Fatalf("tools[0].parameters.required length = %d, want 2; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLifecycleUsesOfficialDefaultResponseScaffold(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_default_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_default_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	var (
		created    gjson.Result
		inProgress gjson.Result
		completed  gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch event {
		case "response.created":
			created = data
		case "response.in_progress":
			inProgress = data
		case "response.completed":
			completed = data
		}
	}

	if !created.Exists() {
		t.Fatalf("missing response.created event: %v", chunks)
	}
	assertOfficialDefaultResponseScaffold(t, created, "response", "gpt-5")
	if got := created.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("response.created completed_at = %s, want null; event=%s", got.Raw, created.Raw)
	}
	if got := created.Get("response.output.#").Int(); got != 0 {
		t.Fatalf("response.created output count = %d, want 0; event=%s", got, created.Raw)
	}

	if !inProgress.Exists() {
		t.Fatalf("missing response.in_progress event: %v", chunks)
	}
	assertOfficialDefaultResponseScaffold(t, inProgress, "response", "gpt-5")
	if got := inProgress.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("response.in_progress completed_at = %s, want null; event=%s", got.Raw, inProgress.Raw)
	}
	if got := inProgress.Get("response.output.#").Int(); got != 0 {
		t.Fatalf("response.in_progress output count = %d, want 0; event=%s", got, inProgress.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	assertOfficialDefaultResponseScaffold(t, completed, "response", "gpt-5")
	if got := completed.Get("response.output.#").Int(); got != 1 {
		t.Fatalf("response.completed output count = %d, want 1; event=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.0.type").String(); got != "message" {
		t.Fatalf("response.completed output[0].type = %q, want message; event=%s", got, completed.Raw)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_UsesOfficialDefaultResponseScaffold(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{
			"id":"chatcmpl_default_scaffold_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	parsed := gjson.Parse(resp)
	assertOfficialDefaultResponseScaffold(t, parsed, "", "gpt-5")
	if got := parsed.Get("completed_at").Int(); got <= 0 {
		t.Fatalf("completed_at = %d, want > 0; resp=%s", got, resp)
	}
	if got := parsed.Get("output.#").Int(); got != 1 {
		t.Fatalf("output count = %d, want 1; resp=%s", got, resp)
	}
	if got := parsed.Get("output.0.type").String(); got != "message" {
		t.Fatalf("output[0].type = %q, want message; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLifecycleMergesPartialNestedRequestConfigIntoDefaultResponseScaffold(t *testing.T) {
	var param any

	request := []byte(`{
		"model":"gpt-5",
		"reasoning":{"effort":"medium"},
		"text":{"verbosity":"low"}
	}`)

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_nested_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_nested_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event != "response.created" && event != "response.in_progress" && event != "response.completed" {
			continue
		}
		if got := data.Get("response.reasoning.effort").String(); got != "medium" {
			t.Fatalf("%s reasoning.effort = %q, want medium; event=%s", event, got, data.Raw)
		}
		if got := data.Get("response.reasoning.summary"); !got.Exists() || got.Type != gjson.Null {
			t.Fatalf("%s reasoning.summary = %s, want null; event=%s", event, got.Raw, data.Raw)
		}
		if got := data.Get("response.text.verbosity").String(); got != "low" {
			t.Fatalf("%s text.verbosity = %q, want low; event=%s", event, got, data.Raw)
		}
		if got := data.Get("response.text.format.type").String(); got != "text" {
			t.Fatalf("%s text.format.type = %q, want text; event=%s", event, got, data.Raw)
		}
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_MergesPartialNestedRequestConfigIntoDefaultResponseScaffold(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{
			"model":"gpt-5",
			"reasoning":{"effort":"medium"},
			"text":{"verbosity":"low"}
		}`),
		[]byte(`{
			"id":"chatcmpl_nested_scaffold_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	parsed := gjson.Parse(resp)
	if got := parsed.Get("reasoning.effort").String(); got != "medium" {
		t.Fatalf("reasoning.effort = %q, want medium; resp=%s", got, resp)
	}
	if got := parsed.Get("reasoning.summary"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("reasoning.summary = %s, want null; resp=%s", got.Raw, resp)
	}
	if got := parsed.Get("text.verbosity").String(); got != "low" {
		t.Fatalf("text.verbosity = %q, want low; resp=%s", got, resp)
	}
	if got := parsed.Get("text.format.type").String(); got != "text" {
		t.Fatalf("text.format.type = %q, want text; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamLifecycleEchoesPromptFieldsInResponseScaffold(t *testing.T) {
	var param any

	request := []byte(`{
		"model":"gpt-5",
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_retention":"24h"
	}`)

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_prompt_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		request,
		[]byte(`{"id":"chatcmpl_prompt_scaffold_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event != "response.created" && event != "response.in_progress" && event != "response.completed" {
			continue
		}
		if got := data.Get("response.prompt.id").String(); got != "pmpt_test" {
			t.Fatalf("%s prompt.id = %q, want pmpt_test; event=%s", event, got, data.Raw)
		}
		if got := data.Get("response.prompt.version").String(); got != "3" {
			t.Fatalf("%s prompt.version = %q, want 3; event=%s", event, got, data.Raw)
		}
		if got := data.Get("response.prompt.variables.city").String(); got != "Boston" {
			t.Fatalf("%s prompt.variables.city = %q, want Boston; event=%s", event, got, data.Raw)
		}
		if got := data.Get("response.prompt_cache_retention").String(); got != "24h" {
			t.Fatalf("%s prompt_cache_retention = %q, want 24h; event=%s", event, got, data.Raw)
		}
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_EchoesPromptFieldsInResponseScaffold(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{
			"model":"gpt-5",
			"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
			"prompt_cache_retention":"24h"
		}`),
		[]byte(`{
			"id":"chatcmpl_prompt_scaffold_nonstream_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	parsed := gjson.Parse(resp)
	if got := parsed.Get("prompt.id").String(); got != "pmpt_test" {
		t.Fatalf("prompt.id = %q, want pmpt_test; resp=%s", got, resp)
	}
	if got := parsed.Get("prompt.version").String(); got != "3" {
		t.Fatalf("prompt.version = %q, want 3; resp=%s", got, resp)
	}
	if got := parsed.Get("prompt.variables.city").String(); got != "Boston" {
		t.Fatalf("prompt.variables.city = %q, want Boston; resp=%s", got, resp)
	}
	if got := parsed.Get("prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("prompt_cache_retention = %q, want 24h; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamCompletedUsageIncludesOfficialDetailShapeWhenSourceDetailsAreMissing(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_usage_shape_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_usage_shape_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`),
		&param,
	)...)

	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event != "response.completed" {
			continue
		}
		if got := data.Get("response.usage.input_tokens").Int(); got != 3 {
			t.Fatalf("usage.input_tokens = %d, want 3; event=%s", got, data.Raw)
		}
		if got := data.Get("response.usage.input_tokens_details.cached_tokens"); !got.Exists() || got.Int() != 0 {
			t.Fatalf("usage.input_tokens_details.cached_tokens = %s, want existing 0; event=%s", got.Raw, data.Raw)
		}
		if got := data.Get("response.usage.output_tokens").Int(); got != 5 {
			t.Fatalf("usage.output_tokens = %d, want 5; event=%s", got, data.Raw)
		}
		if got := data.Get("response.usage.output_tokens_details.reasoning_tokens"); !got.Exists() || got.Int() != 0 {
			t.Fatalf("usage.output_tokens_details.reasoning_tokens = %s, want existing 0; event=%s", got.Raw, data.Raw)
		}
		if got := data.Get("response.usage.total_tokens").Int(); got != 8 {
			t.Fatalf("usage.total_tokens = %d, want 8; event=%s", got, data.Raw)
		}
		return
	}

	t.Fatalf("missing response.completed event: %v", chunks)
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_UsageIncludesOfficialDetailShapeWhenSourceDetailsAreMissing(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{
			"id":"chatcmpl_usage_shape_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}
			],
			"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}
		}`),
		nil,
	)

	parsed := gjson.Parse(resp)
	if got := parsed.Get("usage.input_tokens").Int(); got != 3 {
		t.Fatalf("usage.input_tokens = %d, want 3; resp=%s", got, resp)
	}
	if got := parsed.Get("usage.input_tokens_details.cached_tokens"); !got.Exists() || got.Int() != 0 {
		t.Fatalf("usage.input_tokens_details.cached_tokens = %s, want existing 0; resp=%s", got.Raw, resp)
	}
	if got := parsed.Get("usage.output_tokens").Int(); got != 5 {
		t.Fatalf("usage.output_tokens = %d, want 5; resp=%s", got, resp)
	}
	if got := parsed.Get("usage.output_tokens_details.reasoning_tokens"); !got.Exists() || got.Int() != 0 {
		t.Fatalf("usage.output_tokens_details.reasoning_tokens = %s, want existing 0; resp=%s", got.Raw, resp)
	}
	if got := parsed.Get("usage.total_tokens").Int(); got != 8 {
		t.Fatalf("usage.total_tokens = %d, want 8; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_CompletedResponsesIncludeCompletedAt(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_completed_at_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{"id":"chatcmpl_completed_at_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		&param,
	)...)

	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event != "response.completed" {
			continue
		}
		if got := data.Get("response.completed_at").Int(); got <= 0 {
			t.Fatalf("response.completed_at = %d, want > 0; event=%s", got, data.Raw)
		}
		return
	}

	t.Fatalf("missing response.completed event: %v", chunks)
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_CompletedResponsesIncludeCompletedAt(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5"}`),
		[]byte(`{
			"id":"chatcmpl_completed_at_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, "completed_at").Int(); got <= 0 {
		t.Fatalf("completed_at = %d, want > 0; resp=%s", got, resp)
	}
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_StreamIncompleteResponsesIncludeNullCompletedAt(t *testing.T) {
	var param any

	var chunks []string
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","max_output_tokens":16}`),
		[]byte(`{"id":"chatcmpl_incomplete_completed_at_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`),
		&param,
	)...)
	chunks = append(chunks, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","max_output_tokens":16}`),
		[]byte(`{"id":"chatcmpl_incomplete_completed_at_1","object":"chat.completion.chunk","created":1741478400,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`),
		&param,
	)...)

	for _, chunk := range chunks {
		event, data := parseOpenAIResponsesSSEEvent(t, chunk)
		if event != "response.incomplete" {
			continue
		}
		if got := data.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
			t.Fatalf("response.incomplete completed_at = %s, want null; event=%s", got.Raw, data.Raw)
		}
		if got := data.Get("response.usage"); !got.Exists() || got.Type != gjson.Null {
			t.Fatalf("response.incomplete usage = %s, want null; event=%s", got.Raw, data.Raw)
		}
		return
	}

	t.Fatalf("missing response.incomplete event: %v", chunks)
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream_IncompleteResponsesIncludeNullCompletedAt(t *testing.T) {
	resp := ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(
		context.Background(),
		"gpt-5",
		nil,
		[]byte(`{"model":"gpt-5","max_output_tokens":16}`),
		[]byte(`{
			"id":"chatcmpl_incomplete_completed_at_1",
			"object":"chat.completion",
			"created":1741478400,
			"model":"gpt-5",
			"choices":[
				{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}
			]
		}`),
		nil,
	)

	if got := gjson.Get(resp, "completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("completed_at = %s, want null; resp=%s", got.Raw, resp)
	}
	if got := gjson.Get(resp, "usage"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("usage = %s, want null; resp=%s", got.Raw, resp)
	}
}
