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
