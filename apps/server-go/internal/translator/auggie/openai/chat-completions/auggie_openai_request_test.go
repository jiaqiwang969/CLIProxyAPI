package chat_completions

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIRequestToAuggie_MinimalChatStreamPayload(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"system","content":"You are terse."},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"help me"}
		],
		"tools":[{"type":"function","function":{"name":"list_files","description":"List files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]
	}`), true)

	if got := gjson.GetBytes(out, "mode").String(); got != "CHAT" {
		t.Fatalf("mode = %q, want CHAT", got)
	}
	if got := gjson.GetBytes(out, "model").String(); got != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got)
	}
	if got := gjson.GetBytes(out, "message").String(); got != "help me" {
		t.Fatalf("message = %q, want help me", got)
	}
	if got := gjson.GetBytes(out, "chat_history.#").Int(); got != 1 {
		t.Fatalf("chat_history length = %d, want 1", got)
	}
	if got := gjson.GetBytes(out, "chat_history.0.request_message").String(); got != "hello" {
		t.Fatalf("chat_history[0].request_message = %q, want hello", got)
	}
	if got := gjson.GetBytes(out, "chat_history.0.response_text").String(); got != "hi" {
		t.Fatalf("chat_history[0].response_text = %q, want hi", got)
	}
	if got := gjson.GetBytes(out, "tool_definitions.#").Int(); got != 1 {
		t.Fatalf("tool_definitions length = %d, want 1", got)
	}
	if got := gjson.GetBytes(out, "tool_definitions.0.name").String(); got != "list_files" {
		t.Fatalf("tool_definitions[0].name = %q, want list_files", got)
	}
	if got := gjson.GetBytes(out, "tool_definitions.0.description").String(); got != "List files" {
		t.Fatalf("tool_definitions[0].description = %q, want List files", got)
	}
	if got := gjson.GetBytes(out, "tool_definitions.0.input_schema_json").String(); got == "" {
		t.Fatal("expected tool_definitions[0].input_schema_json")
	}
}

func TestConvertOpenAIRequestToAuggie_PreservesToolResultNodes(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"user","content":"Weather in Shanghai?"},
			{"role":"assistant","tool_calls":[
				{"id":"call_weather_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_weather_1","content":"{\"temperature\":23,\"condition\":\"sunny\"}"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}]
	}`), true)

	if got := gjson.GetBytes(out, "nodes.#").Int(); got != 1 {
		t.Fatalf("nodes length = %d, want 1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "nodes.0.type").Int(); got != 1 {
		t.Fatalf("nodes.0.type = %d, want 1 for tool_result; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_weather_1" {
		t.Fatalf("tool_use_id = %q, want call_weather_1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "nodes.0.tool_result_node.content").String(); got != "{\"temperature\":23,\"condition\":\"sunny\"}" {
		t.Fatalf("tool_result content = %q; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "nodes.0.tool_result_node.is_error").Bool(); got {
		t.Fatalf("tool_result is_error = true, want false; payload=%s", out)
	}
}

func TestConvertOpenAIRequestToAuggie_KeepsPlainTurnsOnLegacyChatHistoryPath(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"help me"}
		]
	}`), true)

	if got := gjson.GetBytes(out, "message").String(); got != "help me" {
		t.Fatalf("message = %q, want help me", got)
	}
	if got := gjson.GetBytes(out, "chat_history.#").Int(); got != 1 {
		t.Fatalf("chat_history length = %d, want 1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "nodes").Exists(); got {
		t.Fatalf("expected no native nodes for plain turns; payload=%s", out)
	}
}

func TestConvertOpenAIRequestToAuggie_MapsSystemAndDeveloperMessagesToNativeSystemPromptFields(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"system","content":"You are terse."},
			{"role":"developer","content":[{"type":"text","text":"Only answer with JSON."}]},
			{"role":"user","content":"say hi"}
		]
	}`), true)

	if got := gjson.GetBytes(out, "system_prompt").String(); got != "You are terse." {
		t.Fatalf("system_prompt = %q, want %q; payload=%s", got, "You are terse.", out)
	}
	if got := gjson.GetBytes(out, "system_prompt_append").String(); got != "Only answer with JSON." {
		t.Fatalf("system_prompt_append = %q, want %q; payload=%s", got, "Only answer with JSON.", out)
	}
	if got := gjson.GetBytes(out, "message").String(); got != "say hi" {
		t.Fatalf("message = %q, want %q; payload=%s", got, "say hi", out)
	}
	if got := gjson.GetBytes(out, "chat_history.#").Int(); got != 0 {
		t.Fatalf("chat_history length = %d, want 0; payload=%s", got, out)
	}
}

func TestConvertOpenAIRequestToAuggie_MapsParallelToolCallsToFeatureDetectionFlags(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"user","content":"hello"}
		],
		"parallel_tool_calls":false
	}`), true)

	flag := gjson.GetBytes(out, "feature_detection_flags.support_parallel_tool_use")
	if !flag.Exists() {
		t.Fatalf("expected support_parallel_tool_use flag; payload=%s", out)
	}
	if got := flag.Bool(); got {
		t.Fatalf("support_parallel_tool_use = %t, want false; payload=%s", got, out)
	}
}

func TestConvertOpenAIRequestToAuggie_OmitsToolDefinitionsWhenToolChoiceIsNone(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"user","content":"hello"}
		],
		"tools":[
			{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}}
		],
		"tool_choice":"none"
	}`), true)

	if got := gjson.GetBytes(out, "tool_definitions.#").Int(); got != 0 {
		t.Fatalf("tool_definitions length = %d, want 0 when tool_choice=none; payload=%s", got, out)
	}
}

func TestConvertOpenAIRequestToAuggie_MapsBuiltInWebSearchTool(t *testing.T) {
	out := ConvertOpenAIRequestToAuggie("gpt-5.4", []byte(`{
		"messages":[
			{"role":"user","content":"Find the latest OpenAI news"}
		],
		"tools":[
			{"type":"web_search","search_context_size":"high"}
		]
	}`), true)

	if got := gjson.GetBytes(out, "tool_definitions.#").Int(); got != 1 {
		t.Fatalf("tool_definitions length = %d, want 1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_definitions.0.name").String(); got != "web-search" {
		t.Fatalf("tool_definitions[0].name = %q, want web-search; payload=%s", got, out)
	}
}
