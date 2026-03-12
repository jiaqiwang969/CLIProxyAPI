package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

const testAuggieWebSearchDescription = "Search the web for information."
const testAuggieWebSearchInputSchema = `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`

func parseExecutorResponsesSSEEvent(t *testing.T, chunk string) (string, gjson.Result) {
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

func assertOpenAIErrorJSON(t *testing.T, err error, param, code, wantMessageContains string) {
	t.Helper()

	errText := err.Error()
	if !gjson.Valid(errText) {
		t.Fatalf("error is not valid JSON: %q", errText)
	}
	if got := gjson.Get(errText, "error.type").String(); got != "invalid_request_error" {
		t.Fatalf("error.type = %q, want invalid_request_error; body=%s", got, errText)
	}
	if got := gjson.Get(errText, "error.param").String(); got != param {
		t.Fatalf("error.param = %q, want %q; body=%s", got, param, errText)
	}
	if got := gjson.Get(errText, "error.code").String(); got != code {
		t.Fatalf("error.code = %q, want %q; body=%s", got, code, errText)
	}
	if got := gjson.Get(errText, "error.message").String(); !strings.Contains(got, wantMessageContains) {
		t.Fatalf("error.message = %q, want mention of %q; body=%s", got, wantMessageContains, errText)
	}
}

func TestPublicAuggieResponsesID_StripsChatCompletionPrefixes(t *testing.T) {
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
			if got := publicAuggieResponsesID(tt.in); got != tt.want {
				t.Fatalf("publicAuggieResponsesID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func expectAuggieIDEStateNodeOnContinuation(t *testing.T, body []byte) {
	t.Helper()

	if got := gjson.GetBytes(body, "nodes.0.type").Int(); got != 4 {
		t.Fatalf("nodes.0.type = %d, want ide_state_node type 4; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "nodes.0.ide_state_node.workspace_folders_unchanged").Bool(); !got {
		t.Fatalf("workspace_folders_unchanged = %v, want true; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "nodes.0.ide_state_node.current_terminal.terminal_id").Int(); got != 0 {
		t.Fatalf("terminal_id = %d, want 0; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "nodes.0.ide_state_node.current_terminal.current_working_directory").String(); got == "" {
		t.Fatalf("current_working_directory = %q, want non-empty; body=%s", got, body)
	}
}

func TestAuggieExecuteStream_EmitsTranslatedOpenAISSE(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want Bearer token-1", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "mode").String(); got != "CHAT" {
			t.Fatalf("mode = %q, want CHAT", got)
		}
		if got := gjson.GetBytes(body, "message").String(); got != "You are terse.\n\nhelp me" {
			t.Fatalf("message = %q, want inlined system instructions + help me", got)
		}
		if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "hello" {
			t.Fatalf("chat_history[0].request_message = %q, want hello", got)
		}
		if got := gjson.GetBytes(body, "chat_history.0.response_text").String(); got != "hi" {
			t.Fatalf("chat_history[0].response_text = %q, want hi", got)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "list_files" {
			t.Fatalf("tool_definitions[0].name = %q, want list_files", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if !strings.Contains(chunks[0], `"id":"chatcmpl-`) {
		t.Fatalf("unexpected first chunk id: %s", chunks[0])
	}
	if !strings.Contains(chunks[0], `"chat.completion.chunk"`) {
		t.Fatalf("unexpected first chunk: %s", chunks[0])
	}
	if !strings.Contains(chunks[0], `"content":"hello"`) {
		t.Fatalf("unexpected first chunk content: %s", chunks[0])
	}
	if !strings.Contains(chunks[1], `"content":" world"`) {
		t.Fatalf("unexpected second chunk content: %s", chunks[1])
	}
	if !strings.Contains(chunks[1], `"finish_reason":"stop"`) {
		t.Fatalf("unexpected second chunk finish_reason: %s", chunks[1])
	}
}

func TestAuggieExecute_AggregatesTranslatedStreamIntoOpenAIResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want Bearer token-1", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := resp.Headers.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson", got)
	}
	if got := gjson.GetBytes(resp.Payload, "object").String(); got != "chat.completion" {
		t.Fatalf("object = %q, want chat.completion", got)
	}
	if got := gjson.GetBytes(resp.Payload, "id").String(); !strings.HasPrefix(got, "chatcmpl-") {
		t.Fatalf("id = %q, want chatcmpl-*", got)
	}
	if got := gjson.GetBytes(resp.Payload, "model").String(); got != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.role").String(); got != "assistant" {
		t.Fatalf("message.role = %q, want assistant", got)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "hello world" {
		t.Fatalf("message.content = %q, want hello world", got)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop", got)
	}
}

func TestAuggieExecute_AggregatesNodeContentWhenTopLevelTextMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"I'll create a Snake game in HTML with embedded CSS and JavaScript."}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"id":2,"type":2,"content":"\n<canvas id=\"game\"></canvas>\n","tool_use":null,"thinking":null,"token_usage":null}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"帮我写一个贪吃蛇的代码，html"}]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	content := gjson.GetBytes(resp.Payload, "choices.0.message.content").String()
	if !strings.Contains(content, "I'll create a Snake game in HTML") {
		t.Fatalf("message.content missing opening sentence; content=%q payload=%s", content, resp.Payload)
	}
	if !strings.Contains(content, `<canvas id="game"></canvas>`) {
		t.Fatalf("message.content missing node content fallback; content=%q payload=%s", content, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIPreservesMetadataOnFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "metadata").Exists() {
			t.Fatalf("translated Auggie request must not forward metadata upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"metadata":{"trace_id":"trace-auggie-chat-1"}
	}`)
	if err != nil {
		t.Fatalf("unexpected error for metadata request: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "metadata.trace_id").String(); got != "trace-auggie-chat-1" {
		t.Fatalf("response metadata.trace_id = %q, want trace-auggie-chat-1; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "hello" {
		t.Fatalf("message content = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIChatCompletionToolLoopCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		expectAuggieIDEStateNodeOnContinuation(t, body)
		if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != "call_chat_1" {
			t.Fatalf("tool_use_id = %q, want call_chat_1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); got != "{\"temperature\":23}" {
			t.Fatalf("tool_result content = %q, want {\"temperature\":23}; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"The temperature is 23C","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[
			{"role":"user","content":"Weather in Shanghai?"},
			{"role":"assistant","tool_calls":[{"id":"call_chat_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}}]},
			{"role":"tool","tool_call_id":"call_chat_1","content":"{\"temperature\":23}"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "The temperature is 23C" {
		t.Fatalf("message.content = %q, want The temperature is 23C; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIChatCompletionToolLoopRestoresConversationState(t *testing.T) {
	attempts := 0
	var firstConversationID string
	var firstTurnID string
	const internalToolUseID = "toolu_chat_state_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		switch attempts {
		case 1:
			firstConversationID = gjson.GetBytes(body, "conversation_id").String()
			if firstConversationID == "" {
				t.Fatalf("first request conversation_id = %q, want non-empty; body=%s", firstConversationID, body)
			}
			firstTurnID = gjson.GetBytes(body, "turn_id").String()
			if firstTurnID == "" {
				t.Fatalf("first request turn_id = %q, want non-empty; body=%s", firstTurnID, body)
			}
			_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"tool_use_id":"toolu_chat_state_1","tool_name":"get_weather","input_json":"{\"location\":\"Boston\"}","is_partial":false,"started_at_ms":1710000000123,"completed_at_ms":1710000000456}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		case 2:
			if got := gjson.GetBytes(body, "conversation_id").String(); got != firstConversationID {
				t.Fatalf("conversation_id = %q, want %q; body=%s", got, firstConversationID, body)
			}
			if got := gjson.GetBytes(body, "turn_id").String(); got != firstTurnID {
				t.Fatalf("turn_id = %q, want %q; body=%s", got, firstTurnID, body)
			}
			if got := gjson.GetBytes(body, "message").String(); got != "" {
				t.Fatalf("continuation message = %q, want empty string; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "chat_history.#").Int(); got != 1 {
				t.Fatalf("chat_history length = %d, want 1 replayed prior exchange; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "What is the weather in Boston? Please use the weather tool." {
				t.Fatalf("chat_history[0].request_message = %q, want prior user request; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.tool_use_id").String(); got != internalToolUseID {
				t.Fatalf("chat_history[0].response_nodes[0].tool_use.tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
			}
			if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.tool_name").String(); got != "get_weather" {
				t.Fatalf("chat_history[0].response_nodes[0].tool_use.tool_name = %q, want get_weather; body=%s", got, body)
			}
			expectAuggieIDEStateNodeOnContinuation(t, body)
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != internalToolUseID {
				t.Fatalf("tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); got != "{\"temperature\":7,\"condition\":\"Cloudy\"}" {
				t.Fatalf("tool_result content = %q, want weather payload; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.request_id").String(); got != firstTurnID {
				t.Fatalf("tool_result request_id = %q, want %q; body=%s", got, firstTurnID, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.start_time_ms").Int(); got != 1710000000123 {
				t.Fatalf("tool_result start_time_ms = %d, want %d; body=%s", got, int64(1710000000123), body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.duration_ms").Int(); got != 333 {
				t.Fatalf("tool_result duration_ms = %d, want %d; body=%s", got, int64(333), body)
			}
			_, _ = fmt.Fprintln(w, `{"text":"Boston is 7C and cloudy.","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	firstResp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[
			{"role":"user","content":"What is the weather in Boston? Please use the weather tool."}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}]
	}`)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if got := gjson.GetBytes(firstResp.Payload, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("first finish_reason = %q, want tool_calls; payload=%s", got, firstResp.Payload)
	}
	if got := gjson.GetBytes(firstResp.Payload, "choices.0.message.content"); got.Type != gjson.Null {
		t.Fatalf("first message.content = %s, want null when assistant returned only tool_calls; payload=%s", got.Raw, firstResp.Payload)
	}

	toolCalls := gjson.GetBytes(firstResp.Payload, "choices.0.message.tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() || len(toolCalls.Array()) != 1 {
		t.Fatalf("first response missing tool_calls: %s", firstResp.Payload)
	}
	toolCallID := toolCalls.Get("0.id").String()
	if !strings.HasPrefix(toolCallID, "call_") {
		t.Fatalf("tool_call_id = %q, want public call_* id; payload=%s", toolCallID, firstResp.Payload)
	}
	if toolCallID == internalToolUseID {
		t.Fatalf("tool_call_id = %q, should not expose upstream internal id; payload=%s", toolCallID, firstResp.Payload)
	}

	secondPayload := fmt.Sprintf(`{
		"messages":[
			{"role":"user","content":"What is the weather in Boston? Please use the weather tool."},
			{"role":"assistant","tool_calls":[{"id":"%s","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Boston\"}"}}]},
			{"role":"tool","tool_call_id":"%s","content":"{\"temperature\":7,\"condition\":\"Cloudy\"}"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}]
	}`, toolCallID, toolCallID)
	secondResp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, secondPayload)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if got := gjson.GetBytes(secondResp.Payload, "choices.0.message.content").String(); got != "Boston is 7C and cloudy." {
		t.Fatalf("message.content = %q, want Boston is 7C and cloudy.; payload=%s", got, secondResp.Payload)
	}
	if got := gjson.GetBytes(secondResp.Payload, "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop; payload=%s", got, secondResp.Payload)
	}
}

func TestAuggieResponses_WebSearchCompletesViaRemoteToolBridge(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0
	runRemoteToolCalls := 0
	const internalToolUseID = "toolu_web_1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)

			switch chatStreamCalls {
			case 1:
				if listRemoteToolsCalls != 1 {
					t.Fatalf("listRemoteToolsCalls before first /chat-stream = %d, want 1", listRemoteToolsCalls)
				}
				if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
					t.Fatalf("tool_definitions length = %d, want 1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
					t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
					t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
					t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-1","turn_id":"turn-web-1","text":"","nodes":[{"tool_use":{"tool_use_id":"toolu_web_1","tool_name":"web-search","input_json":"{\"query\":\"OpenAI latest news\",\"num_results\":1}","is_partial":false,"started_at_ms":1773101708519,"completed_at_ms":1773101709200}}]}`)
				flusher.Flush()
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-1","turn_id":"turn-web-1","text":"","stop_reason":"tool_use"}`)
				flusher.Flush()
			case 2:
				if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-web-1" {
					t.Fatalf("conversation_id = %q, want conv-web-1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-web-1" {
					t.Fatalf("turn_id = %q, want turn-web-1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "message").String(); got != "" {
					t.Fatalf("message = %q, want empty continuation message; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "chat_history.#").Int(); got != 1 {
					t.Fatalf("chat_history length = %d, want 1 replayed exchange; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "Find the latest OpenAI news" {
					t.Fatalf("chat_history[0].request_message = %q, want original prompt; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.tool_use_id").String(); got != internalToolUseID {
					t.Fatalf("chat_history[0].response_nodes[0].tool_use.tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
				}
				expectAuggieIDEStateNodeOnContinuation(t, body)
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != internalToolUseID {
					t.Fatalf("tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
				}
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); !strings.Contains(got, "OpenAI News") {
					t.Fatalf("tool_result content = %q, want OpenAI News; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.start_time_ms").Int(); got != 1773101708519 {
					t.Fatalf("tool_result start_time_ms = %d, want %d; body=%s", got, int64(1773101708519), body)
				}
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.duration_ms").Int(); got != 681 {
					t.Fatalf("tool_result duration_ms = %d, want %d; body=%s", got, int64(681), body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-1","turn_id":"turn-web-2","text":"Top headline: OpenAI News","stop_reason":"end_turn"}`)
				flusher.Flush()
			default:
				t.Fatalf("unexpected /chat-stream call %d", chatStreamCalls)
			}

		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			if got := gjson.GetBytes(body, "tool_id_list.tool_ids.#").Int(); got == 0 {
				t.Fatalf("tool_id_list.tool_ids missing; body=%s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)

		case "/agents/run-remote-tool":
			runRemoteToolCalls++
			if got := gjson.GetBytes(body, "tool_name").String(); got != "web-search" {
				t.Fatalf("tool_name = %q, want web-search; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "tool_id").Int(); got != 1 {
				t.Fatalf("tool_id = %d, want 1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "tool_input_json").String(); !strings.Contains(got, "OpenAI latest news") {
				t.Fatalf("tool_input_json = %q, want OpenAI latest news; body=%s", got, body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintln(w, `{"tool_output":"- [OpenAI News](https://openai.com/news/)","tool_result_message":"","is_error":false,"status":1}`)

		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Find the latest OpenAI news"}]}
		],
		"tools":[{"type":"web_search"}]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if chatStreamCalls != 2 {
		t.Fatalf("chatStreamCalls = %d, want 2", chatStreamCalls)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1", listRemoteToolsCalls)
	}
	if runRemoteToolCalls != 1 {
		t.Fatalf("runRemoteToolCalls = %d, want 1", runRemoteToolCalls)
	}
	webSearchCount := int64(0)
	gjson.GetBytes(resp.Payload, "output").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "web_search_call" {
			webSearchCount++
		}
		return true
	})
	if webSearchCount != 1 {
		t.Fatalf("web_search_call outputs = %d, want 1; payload=%s", webSearchCount, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="web_search_call").status`).String(); got != "completed" {
		t.Fatalf("web_search_call status = %q, want completed; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="web_search_call").id`).String(); !strings.HasPrefix(got, "ws_call_") {
		t.Fatalf("web_search_call id = %q, want public ws_call_* id; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="web_search_call").id`).String(); strings.Contains(got, "toolu_") {
		t.Fatalf("web_search_call id = %q, should not expose upstream internal id; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "Top headline: OpenAI News" {
		t.Fatalf("message output text = %q, want Top headline: OpenAI News; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="function_call")#`).Int(); got != 0 {
		t.Fatalf("function_call outputs = %d, want 0; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_WebSearchPreviewAliasCompletesViaRemoteToolBridgeWithAllowedToolsAuto(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0
	runRemoteToolCalls := 0
	const internalToolUseID = "toolu_web_preview_1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)

			switch chatStreamCalls {
			case 1:
				if listRemoteToolsCalls != 1 {
					t.Fatalf("listRemoteToolsCalls before first /chat-stream = %d, want 1", listRemoteToolsCalls)
				}
				if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
					t.Fatalf("tool_definitions length = %d, want 1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
					t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
					t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
					t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-preview-1","turn_id":"turn-web-preview-1","text":"","nodes":[{"tool_use":{"tool_use_id":"toolu_web_preview_1","tool_name":"web-search","input_json":"{\"query\":\"OpenAI latest news\",\"num_results\":1}","is_partial":false,"started_at_ms":1773101708519,"completed_at_ms":1773101709200}}]}`)
				flusher.Flush()
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-preview-1","turn_id":"turn-web-preview-1","text":"","stop_reason":"tool_use"}`)
				flusher.Flush()
			case 2:
				if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-web-preview-1" {
					t.Fatalf("conversation_id = %q, want conv-web-preview-1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-web-preview-1" {
					t.Fatalf("turn_id = %q, want turn-web-preview-1; body=%s", got, body)
				}
				expectAuggieIDEStateNodeOnContinuation(t, body)
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != internalToolUseID {
					t.Fatalf("tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-preview-1","turn_id":"turn-web-preview-2","text":"Top headline: OpenAI News","stop_reason":"end_turn"}`)
				flusher.Flush()
			default:
				t.Fatalf("unexpected /chat-stream call %d", chatStreamCalls)
			}

		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)

		case "/agents/run-remote-tool":
			runRemoteToolCalls++
			if got := gjson.GetBytes(body, "tool_name").String(); got != "web-search" {
				t.Fatalf("tool_name = %q, want web-search; body=%s", got, body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintln(w, `{"tool_output":"- [OpenAI News](https://openai.com/news/)","tool_result_message":"","is_error":false,"status":1}`)

		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Find the latest OpenAI news"}]}
		],
		"tools":[{"type":"web_search_preview"}],
		"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"web_search_preview"}]}}
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if chatStreamCalls != 2 {
		t.Fatalf("chatStreamCalls = %d, want 2", chatStreamCalls)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1", listRemoteToolsCalls)
	}
	if runRemoteToolCalls != 1 {
		t.Fatalf("runRemoteToolCalls = %d, want 1", runRemoteToolCalls)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="web_search_call").status`).String(); got != "completed" {
		t.Fatalf("web_search_call status = %q, want completed; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "Top headline: OpenAI News" {
		t.Fatalf("message output text = %q, want Top headline: OpenAI News; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_WebSearchHonorsMaxToolCalls(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0
	runRemoteToolCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)

			switch chatStreamCalls {
			case 1:
				if listRemoteToolsCalls != 1 {
					t.Fatalf("listRemoteToolsCalls before first /chat-stream = %d, want 1", listRemoteToolsCalls)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
					t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
					t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-cap-1","turn_id":"turn-web-cap-1","text":"","nodes":[{"tool_use":{"tool_use_id":"toolu_web_cap_1","tool_name":"web-search","input_json":"{\"query\":\"first query\",\"num_results\":1}","is_partial":false}}]}`)
				flusher.Flush()
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-cap-1","turn_id":"turn-web-cap-1","text":"","stop_reason":"tool_use"}`)
				flusher.Flush()
			case 2:
				if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-web-cap-1" {
					t.Fatalf("conversation_id = %q, want conv-web-cap-1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-web-cap-1" {
					t.Fatalf("turn_id = %q, want turn-web-cap-1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != "toolu_web_cap_1" {
					t.Fatalf("tool_use_id = %q, want toolu_web_cap_1; body=%s", got, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-cap-1","turn_id":"turn-web-cap-2","text":"","nodes":[{"tool_use":{"tool_use_id":"toolu_web_cap_2","tool_name":"web-search","input_json":"{\"query\":\"second query\",\"num_results\":1}","is_partial":false}}]}`)
				flusher.Flush()
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-cap-1","turn_id":"turn-web-cap-2","text":"","stop_reason":"tool_use"}`)
				flusher.Flush()
			default:
				t.Fatalf("unexpected /chat-stream call %d", chatStreamCalls)
			}

		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)

		case "/agents/run-remote-tool":
			runRemoteToolCalls++
			if runRemoteToolCalls != 1 {
				t.Fatalf("unexpected /agents/run-remote-tool call %d; body=%s", runRemoteToolCalls, body)
			}
			if got := gjson.GetBytes(body, "tool_input_json").String(); !strings.Contains(got, "first query") {
				t.Fatalf("tool_input_json = %q, want first query; body=%s", got, body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintln(w, `{"tool_output":"- [First Result](https://example.com/first) first result","tool_result_message":"","is_error":false,"status":1}`)

		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Search twice if needed"}]}
		],
		"tools":[{"type":"web_search"}],
		"max_tool_calls":1
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if chatStreamCalls != 2 {
		t.Fatalf("chatStreamCalls = %d, want 2", chatStreamCalls)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1", listRemoteToolsCalls)
	}
	if runRemoteToolCalls != 1 {
		t.Fatalf("runRemoteToolCalls = %d, want 1", runRemoteToolCalls)
	}
	if got := gjson.GetBytes(resp.Payload, "max_tool_calls").Int(); got != 1 {
		t.Fatalf("max_tool_calls = %d, want 1; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="function_call")#`).Int(); got != 0 {
		t.Fatalf("function_call outputs = %d, want 0; payload=%s", got, resp.Payload)
	}

	webSearchCount := int64(0)
	webSearchQuery := ""
	gjson.GetBytes(resp.Payload, "output").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "web_search_call" {
			return true
		}
		webSearchCount++
		if webSearchQuery == "" {
			webSearchQuery = item.Get("action.query").String()
		}
		return true
	})
	if webSearchCount != 1 {
		t.Fatalf("web_search_call outputs = %d, want 1; payload=%s", webSearchCount, resp.Payload)
	}
	if webSearchQuery != "first query" {
		t.Fatalf("web_search_call action.query = %q, want first query; payload=%s", webSearchQuery, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "status").String(); got != "completed" {
		t.Fatalf("status = %q, want completed; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_AggregatesTranslatedStreamIntoOpenAIResponsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want Bearer token-1", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "mode").String(); got != "CHAT" {
			t.Fatalf("mode = %q, want CHAT", got)
		}
		if got := gjson.GetBytes(body, "message").String(); got != "You are terse.\n\nhelp me" {
			t.Fatalf("message = %q, want inlined system instructions + help me", got)
		}
		if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "hello" {
			t.Fatalf("chat_history[0].request_message = %q, want hello", got)
		}
		if got := gjson.GetBytes(body, "chat_history.0.response_text").String(); got != "hi" {
			t.Fatalf("chat_history[0].response_text = %q, want hi", got)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "list_files" {
			t.Fatalf("tool_definitions[0].name = %q, want list_files", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "object").String(); got != "response" {
		t.Fatalf("object = %q, want response", got)
	}
	if got := gjson.GetBytes(resp.Payload, "status").String(); got != "completed" {
		t.Fatalf("status = %q, want completed", got)
	}
	if got := gjson.GetBytes(resp.Payload, "model").String(); got != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "hello world" {
		t.Fatalf("message output text = %q, want hello world", got)
	}
}

func TestAuggieExecute_AggregatesReasoningIntoOpenAIResponsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"id":1,"type":9,"thinking":{"content":"I should inspect the tool result first."}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"All set","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(resp.Payload, `output.#(type=="reasoning").summary.0.text`).String(); got != "I should inspect the tool result first." {
		t.Fatalf("reasoning summary text = %q, want thinking text; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "All set" {
		t.Fatalf("message output text = %q, want All set; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_AggregatesReasoningItemIDIntoOpenAIChatCompletionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"id":1,"type":9,"thinking":{"content":"I should inspect the tool result first.","openai_responses_api_item_id":"rs_native_resp_1"}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"All set","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(resp.Payload, "choices.0.message.reasoning_item_id").String(); got != "rs_native_resp_1" {
		t.Fatalf("reasoning_item_id = %q, want rs_native_resp_1; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_AggregatesEncryptedReasoningIntoOpenAIResponsesResponseWhenIncluded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"id":1,"type":9,"thinking":{"content":"I should inspect the tool result first.","encrypted_content":"enc:auggie:resp-1"}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"All set","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"include":["reasoning.encrypted_content"]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(resp.Payload, `output.#(type=="reasoning").summary.0.text`).String(); got != "I should inspect the tool result first." {
		t.Fatalf("reasoning summary text = %q, want thinking text; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="reasoning").encrypted_content`).String(); got != "enc:auggie:resp-1" {
		t.Fatalf("reasoning encrypted_content = %q, want enc:auggie:resp-1; payload=%s", got, resp.Payload)
	}
}

func TestValidateAuggieResponsesInputItemTypes_RejectsItemReferenceWithGuidance(t *testing.T) {
	err := validateAuggieResponsesInputItemTypes([]byte(`{"input":[{"type":"item_reference","id":"rs_native_1"}]}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "item_reference") {
		t.Fatalf("error = %q, want item_reference guidance", err.Error())
	}
	if !strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("error = %q, want previous_response_id guidance", err.Error())
	}
}

func TestValidateAuggieResponsesInputItemTypes_RejectsReasoningItemWithGuidance(t *testing.T) {
	err := validateAuggieResponsesInputItemTypes([]byte(`{"input":[{"type":"reasoning","id":"rs_native_1","encrypted_content":"enc:reasoning:1"}]}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "reasoning") {
		t.Fatalf("error = %q, want reasoning guidance", err.Error())
	}
	if !strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("error = %q, want previous_response_id guidance", err.Error())
	}
}

func TestValidateAuggieResponsesInputItemTypes_RejectsNonTextToolOutputArray(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		param   string
	}{
		{
			name:    "function_call_output input_image",
			payload: `{"input":[{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}]}`,
			param:   "input[0].output[0].type",
		},
		{
			name:    "custom_tool_call_output input_image",
			payload: `{"input":[{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}]}`,
			param:   "input[0].output[0].type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesInputItemTypes([]byte(tc.payload))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			assertOpenAIErrorJSON(t, err, tc.param, "invalid_value", "input_image")
		})
	}
}

func TestValidateAuggieResponsesIncludeSupport_RejectsItemReferenceContent(t *testing.T) {
	err := validateAuggieResponsesIncludeSupport([]byte(`{"include":["item_reference.content"]}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "item_reference.content") {
		t.Fatalf("error = %q, want item_reference.content", err.Error())
	}
}

func TestAuggieResponses_UsesStoredConversationStateForPreviousResponseID(t *testing.T) {
	attempts := 0
	var firstConversationID string
	var firstTurnID string
	const internalToolUseID = "toolu_prev_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		switch attempts {
		case 1:
			firstConversationID = gjson.GetBytes(body, "conversation_id").String()
			if firstConversationID == "" {
				t.Fatalf("first request conversation_id = %q, want non-empty; body=%s", firstConversationID, body)
			}
			firstTurnID = gjson.GetBytes(body, "turn_id").String()
			if firstTurnID == "" {
				t.Fatalf("first request turn_id = %q, want non-empty; body=%s", firstTurnID, body)
			}
			_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"tool_use_id":"toolu_prev_1","tool_name":"get_weather","input_json":"{\"location\":\"Shanghai\"}","is_partial":false,"started_at_ms":1710000001000,"completed_at_ms":1710000001750}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		case 2:
			if got := gjson.GetBytes(body, "conversation_id").String(); got != firstConversationID {
				t.Fatalf("conversation_id = %q, want %q; body=%s", got, firstConversationID, body)
			}
			if got := gjson.GetBytes(body, "turn_id").String(); got != firstTurnID {
				t.Fatalf("turn_id = %q, want %q; body=%s", got, firstTurnID, body)
			}
			if got := gjson.GetBytes(body, "chat_history.#").Int(); got != 1 {
				t.Fatalf("chat_history length = %d, want 1 restored prior exchange; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "Weather in Shanghai?" {
				t.Fatalf("chat_history[0].request_message = %q, want Weather in Shanghai?; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.tool_use_id").String(); got != internalToolUseID {
				t.Fatalf("chat_history[0].response_nodes[0].tool_use.tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
			}
			if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.tool_name").String(); got != "get_weather" {
				t.Fatalf("chat_history[0].response_nodes[0].tool_use.tool_name = %q, want get_weather; body=%s", got, body)
			}
			expectAuggieIDEStateNodeOnContinuation(t, body)
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != internalToolUseID {
				t.Fatalf("tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); got != "{\"temperature\":23}" {
				t.Fatalf("tool_result content = %q, want {\"temperature\":23}; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.request_id").String(); got != firstTurnID {
				t.Fatalf("tool_result request_id = %q, want %q; body=%s", got, firstTurnID, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.start_time_ms").Int(); got != 1710000001000 {
				t.Fatalf("tool_result start_time_ms = %d, want %d; body=%s", got, int64(1710000001000), body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.duration_ms").Int(); got != 750 {
				t.Fatalf("tool_result duration_ms = %d, want %d; body=%s", got, int64(750), body)
			}
			_, _ = fmt.Fprintln(w, `{"text":"The temperature is 23C","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	firstPayload := `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Weather in Shanghai?"}]}],
		"tools":[{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]
	}`
	firstResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, firstPayload)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	responseID := gjson.GetBytes(firstResp.Payload, "id").String()
	if !strings.HasPrefix(responseID, "resp_") {
		t.Fatalf("first response id = %q, want resp_*; payload=%s", responseID, firstResp.Payload)
	}
	publicCallID := gjson.GetBytes(firstResp.Payload, `output.#(type=="function_call").call_id`).String()
	if !strings.HasPrefix(publicCallID, "call_") {
		t.Fatalf("function_call call_id = %q, want public call_* id; payload=%s", publicCallID, firstResp.Payload)
	}
	if publicCallID == internalToolUseID {
		t.Fatalf("function_call call_id = %q, should not expose upstream internal id; payload=%s", publicCallID, firstResp.Payload)
	}

	secondPayload := fmt.Sprintf(`{
		"model":"gpt-5.4",
		"previous_response_id":"%s",
		"input":[{"type":"function_call_output","call_id":"%s","output":"{\"temperature\":23}"}],
		"tools":[{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]
	}`, responseID, publicCallID)
	secondResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, secondPayload)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if got := gjson.GetBytes(secondResp.Payload, `output.#(type=="message").content.0.text`).String(); got != "The temperature is 23C" {
		t.Fatalf("message output text = %q, want The temperature is 23C; payload=%s", got, secondResp.Payload)
	}
}

func TestAuggieResponses_StreamPublicResponseIDSupportsPreviousResponseContinuation(t *testing.T) {
	attempts := 0
	const internalToolUseID = "toolu_stream_pwd_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		switch attempts {
		case 1:
			_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-stream-pwd-1","turn_id":"turn-stream-pwd-1","nodes":[{"tool_use":{"tool_use_id":"toolu_stream_pwd_1","tool_name":"run_shell","input_json":"{\"command\":\"pwd\"}","is_partial":false,"started_at_ms":1710000002000,"completed_at_ms":1710000002600}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		case 2:
			if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-stream-pwd-1" {
				t.Fatalf("conversation_id = %q, want conv-stream-pwd-1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-stream-pwd-1" {
				t.Fatalf("turn_id = %q, want turn-stream-pwd-1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != internalToolUseID {
				t.Fatalf("tool_use_id = %q, want %s; body=%s", got, internalToolUseID, body)
			}
			if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); got != "/Users/jqwang/05-api-代理/CLIProxyAPI-wjq" {
				t.Fatalf("tool_result content = %q, want pwd output; body=%s", got, body)
			}
			_, _ = fmt.Fprintln(w, `{"text":"当前目录是 /Users/jqwang/05-api-代理/CLIProxyAPI-wjq","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	firstChunks, err := executeAuggieResponsesStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"请使用 pwd 来分析当前的文件夹位置。不要猜。先调用工具。"}]}],
		"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
	}`)
	if err != nil {
		t.Fatalf("stream Execute error: %v", err)
	}

	var responseID string
	var publicCallID string
	for _, chunk := range firstChunks {
		event, payload := parseExecutorResponsesSSEEvent(t, chunk)
		switch event {
		case "response.created":
			responseID = payload.Get("response.id").String()
		case "response.output_item.added":
			if payload.Get("item.type").String() == "function_call" {
				publicCallID = payload.Get("item.call_id").String()
			}
		}
	}

	if !strings.HasPrefix(responseID, "resp_") {
		t.Fatalf("stream response id = %q, want resp_*; chunks=%v", responseID, firstChunks)
	}
	if !strings.HasPrefix(publicCallID, "call_") {
		t.Fatalf("stream function call id = %q, want call_*; chunks=%v", publicCallID, firstChunks)
	}
	if publicCallID == internalToolUseID {
		t.Fatalf("stream function call id = %q, should not expose internal id; chunks=%v", publicCallID, firstChunks)
	}

	secondResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
		"model":"gpt-5.4",
		"previous_response_id":"%s",
		"input":[{"type":"function_call_output","call_id":"%s","output":"/Users/jqwang/05-api-代理/CLIProxyAPI-wjq"}],
		"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
	}`, responseID, publicCallID))
	if err != nil {
		t.Fatalf("continuation Execute error: %v", err)
	}
	if got := gjson.GetBytes(secondResp.Payload, `output.#(type=="message").content.0.text`).String(); got != "当前目录是 /Users/jqwang/05-api-代理/CLIProxyAPI-wjq" {
		t.Fatalf("message output text = %q, want pwd answer; payload=%s", got, secondResp.Payload)
	}
}

func TestAuggieResponses_PreviousResponseContinuationRequiresStoredPriorResponse(t *testing.T) {
	attempts := 0
	const internalToolUseID = "toolu_store_false_pwd_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		switch attempts {
		case 1:
			_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-store-false-pwd-1","turn_id":"turn-store-false-pwd-1","nodes":[{"tool_use":{"tool_use_id":"toolu_store_false_pwd_1","tool_name":"run_shell","input_json":"{\"command\":\"pwd\"}","is_partial":false,"started_at_ms":1710000002000,"completed_at_ms":1710000002600}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected upstream continuation for store=false request; attempt=%d body=%s", attempts, body)
		}
	}))
	defer server.Close()

	firstResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"store":false,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"请使用 pwd 来分析当前的文件夹位置。不要猜。先调用工具。"}]}],
		"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
	}`)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}

	responseID := gjson.GetBytes(firstResp.Payload, "id").String()
	if !strings.HasPrefix(responseID, "resp_") {
		t.Fatalf("response id = %q, want resp_*; payload=%s", responseID, firstResp.Payload)
	}
	publicCallID := gjson.GetBytes(firstResp.Payload, `output.#(type=="function_call").call_id`).String()
	if !strings.HasPrefix(publicCallID, "call_") {
		t.Fatalf("function_call call_id = %q, want public call_* id; payload=%s", publicCallID, firstResp.Payload)
	}
	if publicCallID == internalToolUseID {
		t.Fatalf("function_call call_id = %q, should not expose upstream internal id; payload=%s", publicCallID, firstResp.Payload)
	}

	_, err = executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
		"model":"gpt-5.4",
		"previous_response_id":"%s",
		"input":[{"type":"function_call_output","call_id":"%s","output":"/Users/jqwang/05-api-代理/CLIProxyAPI-wjq"}],
		"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
	}`, responseID, publicCallID))
	if err == nil {
		t.Fatal("expected error for previous_response_id continuation after store=false")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "previous_response_id", "invalid_value", "previous_response_id")
	if attempts != 1 {
		t.Fatalf("upstream attempts = %d, want 1 because continuation must fail before upstream", attempts)
	}
}

func TestAuggieResponses_PreviousResponseReplayStripsRequiredToolDirectiveFromStoredMessage(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		switch attempts {
		case 1:
			_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"id":"call_prev_pwd_1","name":"run_shell","input":{"command":"pwd"}}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		case 2:
			if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "请使用 pwd 来分析当前的文件夹位置。不要猜。先调用工具。" {
				t.Fatalf("chat_history[0].request_message = %q, want original user prompt only; body=%s", got, body)
			}
			if strings.Contains(gjson.GetBytes(body, "chat_history.0.request_message").String(), "OpenAI compatibility:") {
				t.Fatalf("chat_history request_message must not replay compatibility directive; body=%s", body)
			}
			_, _ = fmt.Fprintln(w, `{"text":"当前目录是 /Users/jqwang/05-api-代理/CLIProxyAPI-wjq","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	firstResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"请使用 pwd 来分析当前的文件夹位置。不要猜。先调用工具。"}]}],
		"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
	}`)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	responseID := gjson.GetBytes(firstResp.Payload, "id").String()
	if responseID == "" {
		t.Fatalf("first response missing id: %s", firstResp.Payload)
	}

	secondPayload := fmt.Sprintf(`{
		"model":"gpt-5.4",
		"previous_response_id":"%s",
		"input":[{"type":"function_call_output","call_id":"call_prev_pwd_1","output":"/Users/jqwang/05-api-代理/CLIProxyAPI-wjq"}],
		"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
	}`, responseID)
	secondResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, secondPayload)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if got := gjson.GetBytes(secondResp.Payload, `output.#(type=="message").content.0.text`).String(); got != "当前目录是 /Users/jqwang/05-api-代理/CLIProxyAPI-wjq" {
		t.Fatalf("message output text = %q, want final text; payload=%s", got, secondResp.Payload)
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorWhenPreviousResponseIDIsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected upstream request for unknown previous_response_id")
	}))
	defer server.Close()

	_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"previous_response_id":"resp_missing_prev",
		"input":[{"type":"function_call_output","call_id":"call_missing","output":"{\"temperature\":23}"}]
	}`)
	if err == nil {
		t.Fatal("expected error for unknown previous_response_id")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "previous_response_id", "invalid_value", "previous_response_id")
}

func TestAuggieResponses_ReturnsOpenAIErrorWhenPreviousResponseStateIsMissing(t *testing.T) {
	responseID := "resp_missing_state"

	defaultAuggieResponsesStateStore.mu.Lock()
	defaultAuggieResponsesStateStore.items[responseID] = auggieConversationState{UpdatedAt: time.Now().UTC()}
	defaultAuggieResponsesStateStore.mu.Unlock()
	t.Cleanup(func() {
		defaultAuggieResponsesStateStore.mu.Lock()
		delete(defaultAuggieResponsesStateStore.items, responseID)
		defaultAuggieResponsesStateStore.mu.Unlock()
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected upstream request for previous_response_id missing state")
	}))
	defer server.Close()

	_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
		"model":"gpt-5.4",
		"previous_response_id":"%s",
		"input":[{"type":"function_call_output","call_id":"call_missing_state","output":"{\"temperature\":23}"}]
	}`, responseID))
	if err == nil {
		t.Fatal("expected error for previous_response_id missing conversation state")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "previous_response_id", "invalid_value", "missing")
}

func TestAuggieResponses_FullInlineToolLoopCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		expectAuggieIDEStateNodeOnContinuation(t, body)
		if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != "call_inline_1" {
			t.Fatalf("tool_use_id = %q, want call_inline_1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); got != "{\"temperature\":23}" {
			t.Fatalf("tool_result content = %q, want {\"temperature\":23}; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"The temperature is 23C","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Weather in Shanghai?"}]},
			{"type":"function_call","call_id":"call_inline_1","name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"},
			{"type":"function_call_output","call_id":"call_inline_1","output":"{\"temperature\":23}"}
		],
		"tools":[{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "The temperature is 23C" {
		t.Fatalf("message output text = %q, want The temperature is 23C; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="function_call")#`).Int(); got != 0 {
		t.Fatalf("function_call outputs = %d, want 0; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_CustomToolLoopUsesFunctionShim(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		expectAuggieIDEStateNodeOnContinuation(t, body)
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
			t.Fatalf("tool_definitions length = %d, want 1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "bash" {
			t.Fatalf("tool_definitions.0.name = %q, want bash; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); !strings.Contains(got, `"input"`) || !strings.Contains(got, `"string"`) {
			t.Fatalf("tool_definitions.0.input_schema_json = %q, want string input shim; body=%s", got, body)
		}
		schema := gjson.Parse(gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String())
		if got := schema.Get("properties.input.pattern").String(); got != `^(pwd|ls)(\s+[-\w./]+)?$` {
			t.Fatalf("tool_definitions.0.input_schema_json pattern = %q, want %q; body=%s", got, `^(pwd|ls)(\s+[-\w./]+)?$`, body)
		}
		if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.tool_name").String(); got != "bash" {
			t.Fatalf("chat_history.0.response_nodes.0.tool_use.tool_name = %q, want bash; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "chat_history.0.response_nodes.0.tool_use.input_json").String(); got != `{"input":"pwd"}` {
			t.Fatalf("chat_history.0.response_nodes.0.tool_use.input_json = %q, want %q; body=%s", got, `{"input":"pwd"}`, body)
		}
		if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != "custom-call-1" {
			t.Fatalf("nodes.1.tool_result_node.tool_use_id = %q, want custom-call-1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); got != "/Users/jqwang/05-api-代理/CLIProxyAPI-wjq" {
			t.Fatalf("nodes.1.tool_result_node.content = %q, want current directory; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"当前目录是 /Users/jqwang/05-api-代理/CLIProxyAPI-wjq","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"请用 bash 工具运行 pwd。"}]},
			{"type":"custom_tool_call","call_id":"custom-call-1","name":"bash","input":"pwd"},
			{"type":"custom_tool_call_output","call_id":"custom-call-1","output":"/Users/jqwang/05-api-代理/CLIProxyAPI-wjq"}
		],
		"tools":[{"type":"custom","name":"bash","description":"Run shell commands","format":{"type":"grammar","syntax":"regex","definition":"^(pwd|ls)(\\s+[-\\w./]+)?$"}}]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "当前目录是 /Users/jqwang/05-api-代理/CLIProxyAPI-wjq" {
		t.Fatalf("message output text = %q, want final text; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_CustomToolUseFromAuggieBecomesCustomToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "bash" {
			t.Fatalf("tool_definitions.0.name = %q, want bash; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"tool_use_id":"call_bash_1","tool_name":"bash","input_json":"{\"input\":\"pwd\"}"}}],"stop_reason":"tool_use"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"请先用 bash 工具运行 pwd。"}]}],
		"tools":[{"type":"custom","name":"bash","description":"Run shell commands"}]
	}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `output.0.type`).String(); got != "custom_tool_call" {
		t.Fatalf("output.0.type = %q, want custom_tool_call; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.0.call_id`).String(); got != "call_bash_1" {
		t.Fatalf("output.0.call_id = %q, want call_bash_1; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.0.name`).String(); got != "bash" {
		t.Fatalf("output.0.name = %q, want bash; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.0.input`).String(); got != "pwd" {
		t.Fatalf("output.0.input = %q, want pwd; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedToolType(t *testing.T) {
	for _, toolType := range []string{"code_interpreter"} {
		t.Run(toolType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for unsupported tool type %q", toolType)
			}))
			defer server.Close()

			_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
				"model":"gpt-5.4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
				"tools":[{"type":"%s","name":"tool_1","description":"test tool"}]
			}`, toolType))
			if err == nil {
				t.Fatalf("expected error for unsupported tool type %q", toolType)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			if !strings.Contains(err.Error(), "tools[0].type") || !strings.Contains(err.Error(), toolType) {
				t.Fatalf("error = %q, want mention of tools[0].type and %q", err.Error(), toolType)
			}
		})
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedInputItemType(t *testing.T) {
	testCases := []struct {
		name          string
		inputItemJSON string
	}{
		{
			name:          "computer_call_output",
			inputItemJSON: `{"type":"computer_call_output","call_id":"computer-call-1","output":"screenshot.png"}`,
		},
		{
			name:          "file_search_call",
			inputItemJSON: `{"type":"file_search_call","call_id":"file-search-1","queries":["pwd"]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for unsupported input item type %q", tc.name)
			}))
			defer server.Close()

			payload := fmt.Sprintf(`{
				"model":"gpt-5.4",
				"input":[%s]
			}`, tc.inputItemJSON)
			_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, payload)
			if err == nil {
				t.Fatalf("expected error for unsupported input item type %q", tc.name)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, "input[0].type", "invalid_value", tc.name)
		})
	}
}

func TestAuggieResponses_AllowsStoreTrue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "store").Exists() {
			t.Fatalf("translated Auggie request must not forward store field upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"stored hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"store":true
	}`)
	if err != nil {
		t.Fatalf("unexpected error for store=true: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "store"); !got.Exists() || !got.Bool() {
		t.Fatalf("response store = %s, want true; payload=%s", got.Raw, resp.Payload)
	}
}

func TestAuggieResponses_PreservesMetadataOnFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "metadata").Exists() {
			t.Fatalf("translated Auggie request must not forward metadata upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"metadata":{"trace_id":"trace-auggie-1"}
	}`)
	if err != nil {
		t.Fatalf("unexpected error for metadata request: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "metadata.trace_id").String(); got != "trace-auggie-1" {
		t.Fatalf("response metadata.trace_id = %q, want trace-auggie-1; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "hello" {
		t.Fatalf("response output text = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_AllowsSupportedInclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "include").Exists() {
			t.Fatalf("translated Auggie request must not forward include upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"include":["reasoning.encrypted_content"]
	}`)
	if err != nil {
		t.Fatalf("unexpected error for supported include: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "hello" {
		t.Fatalf("response output text = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorForNonPreservedOutputTextLogprobsInclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when include value is not preserved by Auggie")
	}))
	defer server.Close()

	_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"include":["message.output_text.logprobs"]
	}`)
	if err == nil {
		t.Fatal("expected error for non-preserved output text logprobs include")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "include[0]", "invalid_value", "message.output_text.logprobs")
}

func TestAuggieResponses_ReturnsOpenAIErrorForNonPreservedExpandedIncludeValues(t *testing.T) {
	testCases := []string{
		"code_interpreter_call.outputs",
		"computer_call_output.output.image_url",
		"file_search_call.results",
		"message.input_image.image_url",
	}

	for _, includeValue := range testCases {
		t.Run(includeValue, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request when include value %q is not preserved by Auggie", includeValue)
			}))
			defer server.Close()

			_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
				"model":"gpt-5.4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
				"include":["%s"]
			}`, includeValue))
			if err == nil {
				t.Fatalf("expected error for non-preserved include value %q", includeValue)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, "include[0]", "invalid_value", "include[0]")
		})
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedIncludeValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when include value is unsupported")
	}))
	defer server.Close()

	_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"include":["unsupported.include"]
	}`)
	if err == nil {
		t.Fatal("expected error for unsupported include value")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "include[0]", "invalid_value", "unsupported.include")
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForUnsupportedToolType(t *testing.T) {
	for _, toolType := range []string{"custom", "web_search"} {
		t.Run(toolType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for unsupported tool type %q", toolType)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
				"messages":[{"role":"user","content":"hello"}],
				"tools":[{"type":"%s","name":"tool_1","description":"test tool"}]
			}`, toolType))
			if err == nil {
				t.Fatalf("expected error for unsupported tool type %q", toolType)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, "tools[0].type", "invalid_value", toolType)
		})
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForUnsupportedToolChoice(t *testing.T) {
	testCases := []struct {
		name           string
		toolChoiceJSON string
	}{
		{name: "bogus_string", toolChoiceJSON: `"bogus"`},
		{name: "unknown_object", toolChoiceJSON: `{"type":"custom","name":"tool_1"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for unsupported tool_choice %q", tc.name)
			}))
			defer server.Close()

			payload := fmt.Sprintf(`{
				"messages":[{"role":"user","content":"hello"}],
				"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}}],
				"tool_choice":%s
			}`, tc.toolChoiceJSON)
			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, payload)
			if err == nil {
				t.Fatalf("expected error for unsupported tool_choice %q", tc.name)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			if tc.name == "bogus_string" {
				assertOpenAIErrorJSON(t, err, "tool_choice", "invalid_value", "tool_choice")
				return
			}
			assertOpenAIErrorJSON(t, err, "tool_choice.type", "invalid_value", "tool_choice.type")
		})
	}
}

func TestAuggieExecute_OpenAIRejectsRequiredToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request for required tool_choice")
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}}],
		"tool_choice":"required"
	}`)
	if err == nil {
		t.Fatal("expected error for required tool_choice")
	}
	assertOpenAIErrorJSON(t, err, "tool_choice", "invalid_value", "tool_choice")
}

func TestAuggieExecute_OpenAIRejectsSpecificFunctionToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request for function tool_choice")
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"tools":[
			{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}},
			{"type":"function","function":{"name":"list_files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}
		],
		"tool_choice":{"type":"function","function":{"name":"get_weather"}}
	}`)
	if err == nil {
		t.Fatal("expected error for function tool_choice")
	}
	assertOpenAIErrorJSON(t, err, "tool_choice.type", "invalid_value", "tool_choice.type")
}

func TestAuggieExecute_OpenAIRejectsAllowedToolsRequiredToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request for allowed_tools required tool_choice")
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"tools":[
			{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}},
			{"type":"function","function":{"name":"list_files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}
		],
		"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","function":{"name":"get_weather"}}]}
	}`)
	if err == nil {
		t.Fatal("expected error for allowed_tools required tool_choice")
	}
	assertOpenAIErrorJSON(t, err, "tool_choice.mode", "invalid_value", "tool_choice.mode")
}

func TestAuggieExecute_OpenAIAllowsStoreTrue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "store").Exists() {
			t.Fatalf("translated Auggie request must not forward store field upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"store":true
	}`)
	if err != nil {
		t.Fatalf("unexpected error for store=true: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "hello" {
		t.Fatalf("message.content = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForUnsupportedInclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when include is unsupported")
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"include":["reasoning.encrypted_content"]
	}`)
	if err == nil {
		t.Fatal("expected error for unsupported include")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "include", "unsupported_parameter", "include")
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForUnsupportedStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when structured output is unsupported")
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"pwd_result",
				"schema":{
					"type":"object",
					"properties":{"cwd":{"type":"string"}}
				}
			}
		}
	}`)
	if err == nil {
		t.Fatal("expected error for unsupported structured output")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "response_format.type", "invalid_value", "response_format.type")
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedReasoningEffort(t *testing.T) {
	testCases := []string{"none", "minimal", "xhigh"}

	for _, effort := range testCases {
		t.Run(effort, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected upstream request when reasoning_effort is not preserved")
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, fmt.Sprintf(`{
				"messages":[{"role":"user","content":"hello"}],
				"reasoning_effort":%q
			}`, effort))
			if err == nil {
				t.Fatal("expected error for non-preserved reasoning_effort")
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, "reasoning_effort", "invalid_value", "reasoning_effort")
		})
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedRequestControls(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		param   string
	}{
		{
			name:    "max_completion_tokens",
			payload: `{"messages":[{"role":"user","content":"hello"}],"max_completion_tokens":64}`,
			param:   "max_completion_tokens",
		},
		{
			name:    "temperature",
			payload: `{"messages":[{"role":"user","content":"hello"}],"temperature":0.7}`,
			param:   "temperature",
		},
		{
			name:    "frequency_penalty",
			payload: `{"messages":[{"role":"user","content":"hello"}],"frequency_penalty":1.2}`,
			param:   "frequency_penalty",
		},
		{
			name:    "presence_penalty",
			payload: `{"messages":[{"role":"user","content":"hello"}],"presence_penalty":1.1}`,
			param:   "presence_penalty",
		},
		{
			name:    "logprobs",
			payload: `{"messages":[{"role":"user","content":"hello"}],"logprobs":true}`,
			param:   "logprobs",
		},
		{
			name:    "logit_bias",
			payload: `{"messages":[{"role":"user","content":"hello"}],"logit_bias":{"42":10}}`,
			param:   "logit_bias",
		},
		{
			name:    "service_tier",
			payload: `{"messages":[{"role":"user","content":"hello"}],"service_tier":"priority"}`,
			param:   "service_tier",
		},
		{
			name:    "prompt_cache_key",
			payload: `{"messages":[{"role":"user","content":"hello"}],"prompt_cache_key":"cache-key-1"}`,
			param:   "prompt_cache_key",
		},
		{
			name:    "user",
			payload: `{"messages":[{"role":"user","content":"hello"}],"user":"user_123"}`,
			param:   "user",
		},
		{
			name:    "safety_identifier",
			payload: `{"messages":[{"role":"user","content":"hello"}],"safety_identifier":"safe-user-1"}`,
			param:   "safety_identifier",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request when %s is not preserved", tc.param)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, tc.payload)
			if err == nil {
				t.Fatalf("expected error for non-preserved %s", tc.param)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, tc.param, "invalid_value", tc.param)
		})
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForInvalidMetadata(t *testing.T) {
	longKey := strings.Repeat("k", 65)
	longValue := strings.Repeat("v", 513)

	testCases := []struct {
		name    string
		payload string
		param   string
		code    string
	}{
		{
			name:    "metadata type",
			payload: `{"messages":[{"role":"user","content":"hello"}],"metadata":"bad"}`,
			param:   "metadata",
			code:    "invalid_type",
		},
		{
			name:    "metadata value type",
			payload: `{"messages":[{"role":"user","content":"hello"}],"metadata":{"trace_id":1}}`,
			param:   "metadata.trace_id",
			code:    "invalid_type",
		},
		{
			name:    "metadata key too long",
			payload: fmt.Sprintf(`{"messages":[{"role":"user","content":"hello"}],"metadata":{"%s":"ok"}}`, longKey),
			param:   "metadata." + longKey,
			code:    "invalid_value",
		},
		{
			name:    "metadata value too long",
			payload: fmt.Sprintf(`{"messages":[{"role":"user","content":"hello"}],"metadata":{"trace_id":"%s"}}`, longValue),
			param:   "metadata.trace_id",
			code:    "invalid_value",
		},
		{
			name: "metadata too many pairs",
			payload: `{"messages":[{"role":"user","content":"hello"}],"metadata":{
				"k01":"v","k02":"v","k03":"v","k04":"v","k05":"v","k06":"v","k07":"v","k08":"v",
				"k09":"v","k10":"v","k11":"v","k12":"v","k13":"v","k14":"v","k15":"v","k16":"v","k17":"v"
			}}`,
			param: "metadata",
			code:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for invalid metadata case %s", tc.name)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, tc.payload)
			if err == nil {
				t.Fatalf("expected error for invalid metadata case %s", tc.name)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, tc.param, tc.code, tc.param)
		})
	}
}

func TestAuggieExecute_OpenAIAllowsTextOnlyModalities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "modalities").Exists() {
			t.Fatalf("translated Auggie request must not forward modalities upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"modalities":["text"]
	}`)
	if err != nil {
		t.Fatalf("unexpected error for modalities=[text]: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `choices.0.message.content`).String(); got != "hello" {
		t.Fatalf("message content = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedAudioOutputControls(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		param   string
	}{
		{
			name:    "modalities audio",
			payload: `{"messages":[{"role":"user","content":"hello"}],"modalities":["audio"]}`,
			param:   "modalities",
		},
		{
			name:    "audio config",
			payload: `{"messages":[{"role":"user","content":"hello"}],"audio":{"format":"mp3","voice":"alloy"}}`,
			param:   "audio",
		},
		{
			name:    "prediction",
			payload: `{"messages":[{"role":"user","content":"hello"}],"prediction":{"type":"content","content":"hello"}}`,
			param:   "prediction",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request when %s is not preserved", tc.param)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, tc.payload)
			if err == nil {
				t.Fatalf("expected error for non-preserved %s", tc.param)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, tc.param, "invalid_value", tc.param)
		})
	}
}

func TestAuggieExecute_OpenAIToolChoiceNoneSuppressesUpstreamTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 0 {
			t.Fatalf("tool_definitions length = %d, want 0 when tool_choice=none; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	payload := `{
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}}],
		"tool_choice":"none"
	}`
	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, payload)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "hello" {
		t.Fatalf("choices.0.message.content = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedToolChoice(t *testing.T) {
	testCases := []struct {
		name           string
		toolChoiceJSON string
	}{
		{name: "bogus_string", toolChoiceJSON: `"bogus"`},
		{name: "unknown_object", toolChoiceJSON: `{"type":"mystery","name":"tool_1"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for unsupported responses tool_choice %q", tc.name)
			}))
			defer server.Close()

			payload := fmt.Sprintf(`{
				"model":"gpt-5.4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
				"tools":[{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}}}}],
				"tool_choice":%s
			}`, tc.toolChoiceJSON)
			_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, payload)
			if err == nil {
				t.Fatalf("expected error for unsupported responses tool_choice %q", tc.name)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			if tc.name == "bogus_string" {
				assertOpenAIErrorJSON(t, err, "tool_choice", "invalid_value", "tool_choice")
				return
			}
			assertOpenAIErrorJSON(t, err, "tool_choice.type", "invalid_value", "tool_choice.type")
		})
	}
}

func TestAuggieResponses_OfficialWebSearchAliasesAreAcceptedAsToolsAndRejectedAsForcedSelections(t *testing.T) {
	aliases := []string{
		"web_search_preview",
		"web_search_preview_2025_03_11",
		"web_search_2025_08_26",
	}

	for _, alias := range aliases {
		t.Run(alias, func(t *testing.T) {
			toolsPayload := []byte(fmt.Sprintf(`{
				"tools":[{"type":"%s"}]
			}`, alias))
			if err := validateAuggieResponsesToolTypes(toolsPayload); err != nil {
				t.Fatalf("validateAuggieResponsesToolTypes returned error for %s: %v", alias, err)
			}
			if !auggieResponsesRequestUsesBuiltInToolBridge(toolsPayload) {
				t.Fatalf("auggieResponsesRequestUsesBuiltInToolBridge = false for tools alias %s", alias)
			}

			toolChoicePayload := []byte(fmt.Sprintf(`{
				"tool_choice":{"type":"%s"}
			}`, alias))
			if err := validateAuggieResponsesToolChoice(toolChoicePayload); err == nil {
				t.Fatalf("expected validateAuggieResponsesToolChoice to reject forced selection alias %s", alias)
			}
		})
	}
}

func TestAuggieResponses_AllowsBuiltInWebSearchAllowedToolsAutoSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/agents/list-remote-tools":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)
		case "/chat-stream":
			if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
				t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
				t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
			}
			if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
				t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
			}

			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprintln(w, `{"text":"headline ready","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"web_search"}],
		"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"web_search"}]}}
	}`)
	if err != nil {
		t.Fatalf("unexpected error for built-in web_search allowed_tools auto selection: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "headline ready" {
		t.Fatalf("message output text = %q, want headline ready; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_AllowsAllowedToolsAutoFunctionSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
			t.Fatalf("tool_definitions length = %d, want 1 selected tool; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "run_shell" {
			t.Fatalf("tool_definitions.0.name = %q, want run_shell; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "message").String(); strings.Contains(got, "OpenAI compatibility:") {
			t.Fatalf("message = %q, want no forced-tool directive for allowed_tools auto; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"id":"call_pwd_resp_1","name":"run_shell","input":{"command":"pwd"}}}],"stop_reason":"tool_use"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use pwd to inspect the current directory."}]}],
		"tools":[
			{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},
			{"type":"function","name":"list_files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}
		],
		"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"run_shell"}]}
	}`)
	if err != nil {
		t.Fatalf("unexpected error for allowed_tools auto function selection: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `tool_choice.type`).String(); got != "allowed_tools" {
		t.Fatalf("tool_choice.type = %q, want allowed_tools; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `tool_choice.mode`).String(); got != "auto" {
		t.Fatalf("tool_choice.mode = %q, want auto; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="function_call").name`).String(); got != "run_shell" {
		t.Fatalf("function_call name = %q, want run_shell; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_AllowsAllowedToolsAutoToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
			t.Fatalf("tool_definitions length = %d, want 1 selected tool; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "get_weather" {
			t.Fatalf("tool_definitions.0.name = %q, want get_weather; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "message").String(); strings.Contains(got, "OpenAI compatibility:") {
			t.Fatalf("message = %q, want no forced-tool directive for allowed_tools auto; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"id":"call_allowed_resp_1","name":"get_weather","input":{"location":"Boston"}}}],"stop_reason":"tool_use"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[
			{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}}}},
			{"type":"function","name":"list_files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}}}}
		],
		"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"function","name":"get_weather"}]}}
	}`)
	if err != nil {
		t.Fatalf("unexpected error for allowed_tools auto tool_choice: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="function_call").name`).String(); got != "get_weather" {
		t.Fatalf("function_call name = %q, want get_weather; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIMapsLegacyFunctionsToAuggieToolDefinitions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
			t.Fatalf("tool_definitions length = %d, want 1 legacy function tool; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "run_shell" {
			t.Fatalf("tool_definitions.0.name = %q, want run_shell; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"nodes":[{"tool_use":{"id":"call_pwd_legacy_1","name":"run_shell","input":{"command":"pwd"}}}],"stop_reason":"tool_use"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
		"functions":[
			{"name":"run_shell","description":"Run a shell command","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}
		]
	}`)
	if err != nil {
		t.Fatalf("unexpected error for legacy functions tool declaration: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `choices.0.message.tool_calls.0.function.name`).String(); got != "run_shell" {
		t.Fatalf("tool_calls[0].function.name = %q, want run_shell; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAILegacyFunctionCallNoneSuppressesUpstreamTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 0 {
			t.Fatalf("tool_definitions length = %d, want 0 when function_call=none; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"pwd is /tmp","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
		"functions":[
			{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}
		],
		"function_call":"none"
	}`)
	if err != nil {
		t.Fatalf("unexpected error for legacy function_call=none: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `choices.0.finish_reason`).String(); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForLegacyFunctionFieldConflicts(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		param   string
		code    string
	}{
		{
			name: "tools and functions",
			payload: `{
				"messages":[{"role":"user","content":"hello"}],
				"tools":[{"type":"function","function":{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}],
				"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]
			}`,
			param: "functions",
			code:  "invalid_value",
		},
		{
			name: "tool_choice and function_call",
			payload: `{
				"messages":[{"role":"user","content":"hello"}],
				"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
				"tool_choice":"auto",
				"function_call":"auto"
			}`,
			param: "function_call",
			code:  "invalid_value",
		},
		{
			name: "forced function call object",
			payload: `{
				"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
				"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
				"function_call":{"name":"run_shell"}
			}`,
			param: "function_call",
			code:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request for %s", tc.name)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, tc.payload)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, tc.param, tc.code, tc.param)
		})
	}
}

func TestAuggieResponses_ToolChoiceNoneSuppressesUpstreamTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 0 {
			t.Fatalf("tool_definitions length = %d, want 0 when tool_choice=none; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	payload := `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}}}}],
		"tool_choice":"none"
	}`
	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, payload)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "status").String(); got != "completed" {
		t.Fatalf("status = %q, want completed; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when responses structured output is unsupported")
	}))
	defer server.Close()

	_, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":"hello",
		"text":{
			"format":{
				"type":"json_schema",
				"name":"pwd_result",
				"schema":{
					"type":"object",
					"properties":{"cwd":{"type":"string"}}
				}
			}
		}
	}`)
	if err == nil {
		t.Fatal("expected error for unsupported responses structured output")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "text.format.type", "invalid_value", "text.format.type")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonBooleanParallelToolCalls(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"parallel_tool_calls":"false"
	}`))
	if err == nil {
		t.Fatal("expected error for non-boolean parallel_tool_calls")
	}
	assertOpenAIErrorJSON(t, err, "parallel_tool_calls", "invalid_type", "parallel_tool_calls")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonBooleanStore(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{"store":1}`))
	if err == nil {
		t.Fatal("expected error for invalid store type")
	}
	assertOpenAIErrorJSON(t, err, "store", "invalid_type", "store")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonBooleanParallelToolCalls(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"parallel_tool_calls":"false"
	}`))
	if err == nil {
		t.Fatal("expected error for non-boolean parallel_tool_calls")
	}
	assertOpenAIErrorJSON(t, err, "parallel_tool_calls", "invalid_type", "parallel_tool_calls")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonBooleanStore(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{"store":1}`))
	if err == nil {
		t.Fatal("expected error for invalid store type")
	}
	assertOpenAIErrorJSON(t, err, "store", "invalid_type", "store")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedSamplingControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "max_tokens",
			payload:   `{"max_tokens":64}`,
			wantParam: "max_tokens",
		},
		{
			name:      "max_completion_tokens",
			payload:   `{"max_completion_tokens":64}`,
			wantParam: "max_completion_tokens",
		},
		{
			name:      "top_logprobs",
			payload:   `{"top_logprobs":5}`,
			wantParam: "top_logprobs",
		},
		{
			name:      "temperature",
			payload:   `{"temperature":0.7}`,
			wantParam: "temperature",
		},
		{
			name:      "frequency_penalty",
			payload:   `{"frequency_penalty":1.2}`,
			wantParam: "frequency_penalty",
		},
		{
			name:      "presence_penalty",
			payload:   `{"presence_penalty":1.1}`,
			wantParam: "presence_penalty",
		},
		{
			name:      "logprobs",
			payload:   `{"logprobs":true}`,
			wantParam: "logprobs",
		},
		{
			name:      "top_p",
			payload:   `{"top_p":0.9}`,
			wantParam: "top_p",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatal("expected error for non-preserved Auggie chat/completions control")
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonIntegerMaxCompletionTokens(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"max_completion_tokens":64.5
	}`))
	if err == nil {
		t.Fatal("expected error for non-integer max_completion_tokens")
	}
	assertOpenAIErrorJSON(t, err, "max_completion_tokens", "invalid_type", "max_completion_tokens")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonNumericFrequencyPenalty(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"frequency_penalty":"1.2"
	}`))
	if err == nil {
		t.Fatal("expected error for non-numeric frequency_penalty")
	}
	assertOpenAIErrorJSON(t, err, "frequency_penalty", "invalid_type", "frequency_penalty")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsOutOfRangePresencePenalty(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"presence_penalty":2.5
	}`))
	if err == nil {
		t.Fatal("expected error for out-of-range presence_penalty")
	}
	assertOpenAIErrorJSON(t, err, "presence_penalty", "invalid_value", "presence_penalty")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonBooleanLogprobs(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"logprobs":"true"
	}`))
	if err == nil {
		t.Fatal("expected error for non-boolean logprobs")
	}
	assertOpenAIErrorJSON(t, err, "logprobs", "invalid_type", "logprobs")
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedVerbosityAndWebSearchOptions(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		param   string
	}{
		{
			name:    "verbosity",
			payload: `{"messages":[{"role":"user","content":"hello"}],"verbosity":"high"}`,
			param:   "verbosity",
		},
		{
			name:    "web_search_options",
			payload: `{"messages":[{"role":"user","content":"hello"}],"web_search_options":{}}`,
			param:   "web_search_options",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request when %s is not preserved", tc.param)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, tc.payload)
			if err == nil {
				t.Fatalf("expected error for non-preserved %s", tc.param)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, tc.param, "invalid_value", tc.param)
		})
	}
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedVerbosityAndWebSearchOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "verbosity",
			payload:   `{"verbosity":"high"}`,
			wantParam: "verbosity",
		},
		{
			name:      "web_search_options",
			payload:   `{"web_search_options":{}}`,
			wantParam: "web_search_options",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatal("expected error for non-preserved Auggie chat/completions control")
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsInvalidVerbosity(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"verbosity":1
	}`))
	if err == nil {
		t.Fatal("expected error for invalid verbosity type")
	}
	assertOpenAIErrorJSON(t, err, "verbosity", "invalid_type", "verbosity")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsInvalidWebSearchOptions(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"web_search_options":1
	}`))
	if err == nil {
		t.Fatal("expected error for invalid web_search_options type")
	}
	assertOpenAIErrorJSON(t, err, "web_search_options", "invalid_type", "web_search_options")
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedFunctionToolStrict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when function strict semantics are not preserved")
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"run_shell","strict":true,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]
	}`)
	if err == nil {
		t.Fatal("expected error for non-preserved tools[0].function.strict")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "tools[0].function.strict", "invalid_value", "tools[0].function.strict")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedFunctionToolStrict(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"tools":[{"type":"function","function":{"name":"run_shell","strict":true,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]
	}`))
	if err == nil {
		t.Fatal("expected error for non-preserved function strict semantics")
	}
	assertOpenAIErrorJSON(t, err, "tools[0].function.strict", "invalid_value", "tools[0].function.strict")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsInvalidFunctionToolStrict(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"tools":[{"type":"function","function":{"name":"run_shell","strict":"true","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]
	}`))
	if err == nil {
		t.Fatal("expected error for invalid function strict type")
	}
	assertOpenAIErrorJSON(t, err, "tools[0].function.strict", "invalid_type", "tools[0].function.strict")
}

func TestValidateAuggieOpenAIRequestCapabilities_AllowsExplicitNonStrictFunctionTools(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"tools":[{"type":"function","function":{"name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]}`)
	if err := validateAuggieOpenAIRequestCapabilities(payload); err != nil {
		t.Fatalf("validateAuggieOpenAIRequestCapabilities returned error for tools[0].function.strict=false: %v", err)
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedNStopAndSeed(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
		param   string
	}{
		{
			name:    "n",
			payload: `{"messages":[{"role":"user","content":"hello"}],"n":2}`,
			param:   "n",
		},
		{
			name:    "stop",
			payload: `{"messages":[{"role":"user","content":"hello"}],"stop":"END"}`,
			param:   "stop",
		},
		{
			name:    "seed",
			payload: `{"messages":[{"role":"user","content":"hello"}],"seed":7}`,
			param:   "seed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected upstream request when %s is not preserved", tc.param)
			}))
			defer server.Close()

			_, err := executeAuggieNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, tc.payload)
			if err == nil {
				t.Fatalf("expected error for non-preserved %s", tc.param)
			}
			statusCoder, ok := err.(interface{ StatusCode() int })
			if !ok {
				t.Fatalf("error does not expose status code: %T %v", err, err)
			}
			if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
			}
			assertOpenAIErrorJSON(t, err, tc.param, "invalid_value", tc.param)
		})
	}
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedNStopAndSeed(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "n",
			payload:   `{"n":2}`,
			wantParam: "n",
		},
		{
			name:      "stop",
			payload:   `{"stop":"END"}`,
			wantParam: "stop",
		},
		{
			name:      "seed",
			payload:   `{"seed":7}`,
			wantParam: "seed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatal("expected error for non-preserved Auggie chat/completions control")
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonIntegerN(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"n":1.5
	}`))
	if err == nil {
		t.Fatal("expected error for non-integer n")
	}
	assertOpenAIErrorJSON(t, err, "n", "invalid_type", "n")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsInvalidStopType(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"stop":{"bad":true}
	}`))
	if err == nil {
		t.Fatal("expected error for invalid stop type")
	}
	assertOpenAIErrorJSON(t, err, "stop", "invalid_type", "stop")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsTooManyStopSequences(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"stop":["a","b","c","d","e"]
	}`))
	if err == nil {
		t.Fatal("expected error for too many stop sequences")
	}
	assertOpenAIErrorJSON(t, err, "stop", "invalid_value", "stop")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonIntegerSeed(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"seed":7.5
	}`))
	if err == nil {
		t.Fatal("expected error for non-integer seed")
	}
	assertOpenAIErrorJSON(t, err, "seed", "invalid_type", "seed")
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForNonPreservedStreamOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected upstream request when stream_options is not preserved")
	}))
	defer server.Close()

	_, err := executeAuggieStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"messages":[{"role":"user","content":"hello"}],
		"stream":true,
		"stream_options":{"include_usage":true}
	}`)
	if err == nil {
		t.Fatal("expected error for non-preserved stream_options")
	}
	statusCoder, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error does not expose status code: %T %v", err, err)
	}
	if got := statusCoder.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; err=%v", got, http.StatusBadRequest, err)
	}
	assertOpenAIErrorJSON(t, err, "stream_options", "invalid_value", "stream_options")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedStreamOptions(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"stream":true,
		"stream_options":{"include_usage":true}
	}`))
	if err == nil {
		t.Fatal("expected error for non-preserved stream_options")
	}
	assertOpenAIErrorJSON(t, err, "stream_options", "invalid_value", "stream_options")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonBooleanStreamOptionsIncludeUsage(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{
		"stream":true,
		"stream_options":{"include_usage":"true"}
	}`))
	if err == nil {
		t.Fatal("expected error for non-boolean stream_options.include_usage")
	}
	assertOpenAIErrorJSON(t, err, "stream_options.include_usage", "invalid_type", "stream_options.include_usage")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedServiceTier(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIRequestCapabilities([]byte(`{"service_tier":"priority"}`))
	if err == nil {
		t.Fatal("expected error for non-preserved service_tier")
	}
	assertOpenAIErrorJSON(t, err, "service_tier", "invalid_value", "service_tier")
}

func TestValidateAuggieOpenAIRequestCapabilities_RejectsNonPreservedPromptCacheAndUserControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "prompt_cache_key",
			payload:   `{"prompt_cache_key":"cache-key-1"}`,
			wantParam: "prompt_cache_key",
		},
		{
			name:      "prompt_cache_retention",
			payload:   `{"prompt_cache_retention":"24h"}`,
			wantParam: "prompt_cache_retention",
		},
		{
			name:      "user",
			payload:   `{"user":"user_123"}`,
			wantParam: "user",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for non-preserved %s", tc.wantParam)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonIntegerMaxToolCalls(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"max_tool_calls":1.5
	}`))
	if err == nil {
		t.Fatal("expected error for non-integer max_tool_calls")
	}
	assertOpenAIErrorJSON(t, err, "max_tool_calls", "invalid_type", "max_tool_calls")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonIntegerMaxOutputTokens(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"max_output_tokens":64.5
	}`))
	if err == nil {
		t.Fatal("expected error for non-integer max_output_tokens")
	}
	assertOpenAIErrorJSON(t, err, "max_output_tokens", "invalid_type", "max_output_tokens")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonIntegerTopLogprobs(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"top_logprobs":1.5
	}`))
	if err == nil {
		t.Fatal("expected error for non-integer top_logprobs")
	}
	assertOpenAIErrorJSON(t, err, "top_logprobs", "invalid_type", "top_logprobs")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsOutOfRangeTopLogprobs(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"top_logprobs":21
	}`))
	if err == nil {
		t.Fatal("expected error for out-of-range top_logprobs")
	}
	assertOpenAIErrorJSON(t, err, "top_logprobs", "invalid_value", "top_logprobs")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonNumericTemperature(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"temperature":"0.7"
	}`))
	if err == nil {
		t.Fatal("expected error for non-numeric temperature")
	}
	assertOpenAIErrorJSON(t, err, "temperature", "invalid_type", "temperature")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsOutOfRangeTemperature(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"temperature":2.5
	}`))
	if err == nil {
		t.Fatal("expected error for out-of-range temperature")
	}
	assertOpenAIErrorJSON(t, err, "temperature", "invalid_value", "temperature")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonNumericTopP(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"top_p":"0.9"
	}`))
	if err == nil {
		t.Fatal("expected error for non-numeric top_p")
	}
	assertOpenAIErrorJSON(t, err, "top_p", "invalid_type", "top_p")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsOutOfRangeTopP(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"top_p":1.1
	}`))
	if err == nil {
		t.Fatal("expected error for out-of-range top_p")
	}
	assertOpenAIErrorJSON(t, err, "top_p", "invalid_value", "top_p")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonStringServiceTier(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"service_tier":1
	}`))
	if err == nil {
		t.Fatal("expected error for non-string service_tier")
	}
	assertOpenAIErrorJSON(t, err, "service_tier", "invalid_type", "service_tier")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsUnsupportedServiceTier(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"service_tier":"turbo"
	}`))
	if err == nil {
		t.Fatal("expected error for unsupported service_tier")
	}
	assertOpenAIErrorJSON(t, err, "service_tier", "invalid_value", "service_tier")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsInvalidPromptCacheAndSafetyControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
		wantCode  string
	}{
		{
			name:      "prompt_cache_key",
			payload:   `{"prompt_cache_key":1}`,
			wantParam: "prompt_cache_key",
			wantCode:  "invalid_type",
		},
		{
			name:      "prompt_cache_retention",
			payload:   `{"prompt_cache_retention":"7d"}`,
			wantParam: "prompt_cache_retention",
			wantCode:  "invalid_value",
		},
		{
			name:      "safety_identifier",
			payload:   `{"safety_identifier":1}`,
			wantParam: "safety_identifier",
			wantCode:  "invalid_type",
		},
		{
			name:      "user",
			payload:   `{"user":1}`,
			wantParam: "user",
			wantCode:  "invalid_type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for invalid %s", tc.wantParam)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsUnsupportedTextVerbosity(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"text":{"verbosity":"loud"}
	}`))
	if err == nil {
		t.Fatal("expected error for unsupported text.verbosity")
	}
	assertOpenAIErrorJSON(t, err, "text.verbosity", "invalid_value", "text.verbosity")
}

func TestValidateAuggieResponsesRequestCapabilities_AllowsSupportedTextVerbosity(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"text":{"verbosity":"high"}
	}`))
	if err != nil {
		t.Fatalf("unexpected error for supported text.verbosity: %v", err)
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsUnsupportedReasoningEffort(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"reasoning":{"effort":"banana"}
	}`))
	if err == nil {
		t.Fatal("expected error for unsupported reasoning.effort")
	}
	assertOpenAIErrorJSON(t, err, "reasoning.effort", "invalid_value", "reasoning.effort")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedReasoningEffort(t *testing.T) {
	t.Parallel()

	for _, effort := range []string{"none", "minimal", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(fmt.Sprintf(`{
		"reasoning":{"effort":%q}
	}`, effort)))
			if err == nil {
				t.Fatalf("expected error for non-preserved reasoning.effort=%q", effort)
			}
			assertOpenAIErrorJSON(t, err, "reasoning.effort", "invalid_value", "reasoning.effort")
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_AllowsReasoningSummaryControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		payload string
	}{
		{
			name: "reasoning_summary",
			payload: `{
		"reasoning":{"summary":"detailed"}
	}`,
		},
		{
			name: "reasoning_generate_summary",
			payload: `{
		"reasoning":{"generate_summary":true}
	}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(tc.payload))
			if err != nil {
				t.Fatalf("unexpected error for reasoning summary control %q: %v", tc.name, err)
			}
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedSamplingControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "max_tool_calls",
			payload:   `{"max_tool_calls":1}`,
			wantParam: "max_tool_calls",
		},
		{
			name:      "max_output_tokens",
			payload:   `{"max_output_tokens":64}`,
			wantParam: "max_output_tokens",
		},
		{
			name:      "top_logprobs",
			payload:   `{"top_logprobs":5}`,
			wantParam: "top_logprobs",
		},
		{
			name:      "temperature",
			payload:   `{"temperature":0.7}`,
			wantParam: "temperature",
		},
		{
			name:      "top_p",
			payload:   `{"top_p":0.9}`,
			wantParam: "top_p",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatal("expected error for non-preserved Auggie sampling control")
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedServiceTier(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"service_tier":"priority"
	}`))
	if err == nil {
		t.Fatal("expected error for non-preserved service_tier")
	}
	assertOpenAIErrorJSON(t, err, "service_tier", "invalid_value", "service_tier")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedTruncationAuto(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"truncation":"auto"
	}`))
	if err == nil {
		t.Fatal("expected error for non-preserved truncation")
	}
	assertOpenAIErrorJSON(t, err, "truncation", "invalid_value", "truncation")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedPromptTemplate(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"prompt":{"id":"pmpt_test","version":"1","variables":{"name":"world"}}
	}`))
	if err == nil {
		t.Fatal("expected error for non-preserved prompt template")
	}
	assertOpenAIErrorJSON(t, err, "prompt", "invalid_value", "prompt")
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedContextManagement(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesRequestCapabilities([]byte(`{
		"context_management":[{"type":"compaction","compact_threshold":1000}]
	}`))
	if err == nil {
		t.Fatal("expected error for non-preserved context_management")
	}
	assertOpenAIErrorJSON(t, err, "context_management", "invalid_value", "context_management")
}

func TestValidateAuggieResponsesRequestCapabilities_AllowsPromptCacheAndSafetyControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "prompt_cache_key",
			payload:   `{"prompt_cache_key":"cache-key-1"}`,
			wantParam: "prompt_cache_key",
		},
		{
			name:      "prompt_cache_retention",
			payload:   `{"prompt_cache_retention":"24h"}`,
			wantParam: "prompt_cache_retention",
		},
		{
			name:      "safety_identifier",
			payload:   `{"safety_identifier":"safe-user-1"}`,
			wantParam: "safety_identifier",
		},
		{
			name:      "user",
			payload:   `{"user":"user_123"}`,
			wantParam: "user",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(tc.payload))
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.wantParam, err)
			}
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedStructuredOutput(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "json_object",
			payload:   `{"text":{"format":{"type":"json_object"}}}`,
			wantParam: "text.format.type",
		},
		{
			name: "json_schema",
			payload: `{
				"text":{
					"format":{
						"type":"json_schema",
						"name":"pwd_result",
						"schema":{
							"type":"object",
							"properties":{"cwd":{"type":"string"}}
						}
					}
				}
			}`,
			wantParam: "text.format.type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for non-preserved %s", tc.wantParam)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieResponsesToolTypes_ErrorMentionsWebSearchSupport(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesToolTypes([]byte(`{
		"tools":[{"type":"code_interpreter"}]
	}`))
	if err == nil {
		t.Fatal("expected error for unsupported responses tool type")
	}
	assertOpenAIErrorJSON(t, err, "tools[0].type", "invalid_value", "code_interpreter")
	if !strings.Contains(err.Error(), "function, custom, and web_search") {
		t.Fatalf("error = %q, want mention of custom and web_search support", err.Error())
	}
}

func TestValidateAuggieResponsesToolTypes_AllowsCustomGrammarFormat(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesToolTypes([]byte(`{
		"tools":[{
			"type":"custom",
			"name":"bash",
			"format":{
				"type":"grammar",
				"syntax":"regex",
				"definition":".*"
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("unexpected error for supported custom grammar format: %v", err)
	}
}

func TestValidateAuggieResponsesToolTypes_RejectsDeferredFunctionToolLoading(t *testing.T) {
	t.Parallel()

	err := validateAuggieResponsesToolTypes([]byte(`{
		"tools":[{
			"type":"function",
			"name":"run_shell",
			"parameters":{"type":"object"},
			"defer_loading":true
		}]
	}`))
	if err == nil {
		t.Fatal("expected error for unsupported deferred function tool loading")
	}
	assertOpenAIErrorJSON(t, err, "tools[0].defer_loading", "invalid_value", "tools[0].defer_loading")
	if !strings.Contains(err.Error(), "defer_loading") {
		t.Fatalf("error = %q, want defer_loading guidance", err.Error())
	}
}

func TestValidateAuggieResponsesRequestCapabilities_AllowsBuiltInWebSearchToolConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name:      "web_search_search_context_size",
			payload:   `{"tools":[{"type":"web_search","search_context_size":"high"}]}`,
			wantParam: "tools[0].search_context_size",
		},
		{
			name:      "web_search_filters",
			payload:   `{"tools":[{"type":"web_search","filters":{"allowed_domains":["openai.com"]}}]}`,
			wantParam: "tools[0].filters",
		},
		{
			name:      "web_search_preview_search_content_types",
			payload:   `{"tools":[{"type":"web_search_preview","search_content_types":["image"]}]}`,
			wantParam: "tools[0].search_content_types",
		},
		{
			name:      "web_search_user_location",
			payload:   `{"tools":[{"type":"web_search","user_location":{"type":"approximate","country":"US"}}]}`,
			wantParam: "tools[0].user_location",
		},
		{
			name:      "web_search_external_web_access",
			payload:   `{"tools":[{"type":"web_search","external_web_access":true}]}`,
			wantParam: "tools[0].external_web_access",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesRequestCapabilities([]byte(tc.payload))
			if err != nil {
				t.Fatalf("unexpected error for built-in web search config %q: %v", tc.name, err)
			}
		})
	}
}

func TestValidateAuggieResponsesToolChoice_ErrorMentionsAllowedModesOnly(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		payload string
	}{
		{name: "bogus_string", payload: `{"tool_choice":"bogus"}`},
		{name: "unknown_object", payload: `{"tool_choice":{"type":"mystery"}}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesToolChoice([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for unsupported responses tool_choice %q", tc.name)
			}
			want := `supported values are auto, none, allowed_tools selection in auto mode, or omitted tool_choice`
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, want mention of %q", err.Error(), want)
			}
		})
	}
}

func TestValidateAuggieResponsesToolChoice_RejectsNonPreservedForcedSelectionModes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{name: "required", payload: `{"tool_choice":"required"}`, wantParam: "tool_choice"},
		{name: "function_selection", payload: `{"tool_choice":{"type":"function","function":{"name":"run_shell"}}}`, wantParam: "tool_choice.type"},
		{name: "custom_selection", payload: `{"tool_choice":{"type":"custom","name":"bash"}}`, wantParam: "tool_choice.type"},
		{name: "allowed_tools_required", payload: `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"run_shell"}}]}}}`, wantParam: "tool_choice.allowed_tools.mode"},
		{name: "web_search_selection", payload: `{"tool_choice":{"type":"web_search"}}`, wantParam: "tool_choice.type"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesToolChoice([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for non-preserved tool_choice %q", tc.name)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieResponsesToolChoice_AllowsAutoCompatibleModes(t *testing.T) {
	t.Parallel()

	testCases := []string{
		`{"tool_choice":"auto"}`,
		`{"tool_choice":"none"}`,
		`{"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","function":{"name":"run_shell"}}]}}`,
		`{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"web_search"}]}}}`,
	}

	for _, payload := range testCases {
		if err := validateAuggieResponsesToolChoice([]byte(payload)); err != nil {
			t.Fatalf("validateAuggieResponsesToolChoice returned error for compatible mode %s: %v", payload, err)
		}
	}
}

func TestValidateAuggieResponsesToolChoice_RejectsInvalidAllowedToolsShape(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
		wantCode  string
	}{
		{
			name:      "invalid_nested_mode",
			payload:   `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"bogus","tools":[{"type":"function","name":"run_shell"}]}}}`,
			wantParam: "tool_choice.allowed_tools.mode",
			wantCode:  "invalid_value",
		},
		{
			name:      "missing_flat_tools",
			payload:   `{"tool_choice":{"type":"allowed_tools","mode":"auto"}}`,
			wantParam: "tool_choice.tools",
			wantCode:  "invalid_value",
		},
		{
			name:      "invalid_nested_tools_type",
			payload:   `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":1}}}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_type",
		},
		{
			name:      "unsupported_nested_tool_selection",
			payload:   `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"mystery"}]}}}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieResponsesToolChoice([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for invalid allowed_tools shape %q", tc.name)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestValidateAuggieResponsesRequestCapabilities_RejectsNonPreservedStrictFunctionTools(t *testing.T) {
	t.Parallel()

	testCases := []string{
		`{"tools":[{"type":"function","name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]}`,
		`{"tools":[{"type":"function","name":"run_shell","strict":true,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]}`,
	}

	for _, payload := range testCases {
		err := validateAuggieResponsesRequestCapabilities([]byte(payload))
		if err == nil {
			t.Fatalf("expected error for non-preserved strict function tool payload %s", payload)
		}
		assertOpenAIErrorJSON(t, err, "tools[0].strict", "invalid_value", "tools[0].strict")
	}
}

func TestValidateAuggieResponsesRequestCapabilities_AllowsExplicitNonStrictFunctionTools(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]}`)
	if err := validateAuggieResponsesRequestCapabilities(payload); err != nil {
		t.Fatalf("validateAuggieResponsesRequestCapabilities returned error for strict=false function tool: %v", err)
	}
}

func TestValidateAuggieOpenAIToolChoice_ErrorDoesNotMentionWebSearchSelection(t *testing.T) {
	t.Parallel()

	err := validateAuggieOpenAIToolChoice([]byte(`{"tool_choice":{"type":"custom"}}`))
	if err == nil {
		t.Fatal("expected error for unsupported OpenAI tool_choice")
	}
	if strings.Contains(err.Error(), "web_search selection") {
		t.Fatalf("error = %q, should not mention web_search selection for chat-completions path", err.Error())
	}
}

func TestValidateAuggieOpenAIReasoningEffort_RejectsNonPreservedValues(t *testing.T) {
	t.Parallel()

	for _, effort := range []string{"none", "minimal", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
			err := validateAuggieOpenAIReasoningEffort([]byte(fmt.Sprintf(`{"reasoning_effort":%q}`, effort)))
			if err == nil {
				t.Fatalf("expected error for reasoning_effort=%q", effort)
			}
			assertOpenAIErrorJSON(t, err, "reasoning_effort", "invalid_value", "reasoning_effort")
		})
	}
}

func TestValidateAuggieOpenAIReasoningEffort_AllowsNativeValues(t *testing.T) {
	t.Parallel()

	for _, effort := range []string{"low", "medium", "high"} {
		if err := validateAuggieOpenAIReasoningEffort([]byte(fmt.Sprintf(`{"reasoning_effort":%q}`, effort))); err != nil {
			t.Fatalf("validateAuggieOpenAIReasoningEffort returned error for reasoning_effort=%q: %v", effort, err)
		}
	}
}

func TestValidateAuggieOpenAIToolChoice_ErrorMentionsAllowedModesOnly(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		payload string
	}{
		{name: "bogus_string", payload: `{"tool_choice":"bogus"}`},
		{name: "unknown_object", payload: `{"tool_choice":{"type":"mystery"}}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIToolChoice([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for unsupported tool_choice %q", tc.name)
			}
			want := `supported values are auto, none, allowed_tools selection in auto mode, or omitted tool_choice`
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, want mention of %q", err.Error(), want)
			}
		})
	}
}

func TestValidateAuggieOpenAIToolChoice_RejectsNonPreservedForcedSelectionModes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{name: "required", payload: `{"tool_choice":"required"}`, wantParam: "tool_choice"},
		{name: "function_selection", payload: `{"tool_choice":{"type":"function","function":{"name":"run_shell"}}}`, wantParam: "tool_choice.type"},
		{name: "allowed_tools_required_nested", payload: `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"run_shell"}}]}}}`, wantParam: "tool_choice.allowed_tools.mode"},
		{name: "allowed_tools_required_flat", payload: `{"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","function":{"name":"run_shell"}}]}}`, wantParam: "tool_choice.mode"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIToolChoice([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for non-preserved tool_choice %q", tc.name)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestValidateAuggieOpenAIToolChoice_AllowsAutoCompatibleModes(t *testing.T) {
	t.Parallel()

	testCases := []string{
		`{"tool_choice":"auto"}`,
		`{"tool_choice":"none"}`,
		`{"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","function":{"name":"run_shell"}}]}}`,
		`{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"function","function":{"name":"run_shell"}}]}}}`,
	}

	for _, payload := range testCases {
		if err := validateAuggieOpenAIToolChoice([]byte(payload)); err != nil {
			t.Fatalf("validateAuggieOpenAIToolChoice returned error for compatible mode %s: %v", payload, err)
		}
	}
}

func TestValidateAuggieOpenAIToolChoice_RejectsInvalidAllowedToolsShape(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		payload   string
		wantParam string
		wantCode  string
	}{
		{
			name:      "invalid_nested_mode",
			payload:   `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"bogus","tools":[{"type":"function","function":{"name":"run_shell"}}]}}}`,
			wantParam: "tool_choice.allowed_tools.mode",
			wantCode:  "invalid_value",
		},
		{
			name:      "missing_flat_tools",
			payload:   `{"tool_choice":{"type":"allowed_tools","mode":"auto"}}`,
			wantParam: "tool_choice.tools",
			wantCode:  "invalid_value",
		},
		{
			name:      "invalid_nested_tools_type",
			payload:   `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":1}}}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_type",
		},
		{
			name:      "unsupported_nested_tool_selection",
			payload:   `{"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"custom","name":"bash"}]}}}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuggieOpenAIToolChoice([]byte(tc.payload))
			if err == nil {
				t.Fatalf("expected error for invalid allowed_tools shape %q", tc.name)
			}
			assertOpenAIErrorJSON(t, err, tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestAuggieExecuteStream_ResolvesShortNameAliasToCanonicalModelID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "model").String(); got != "gpt-5-4" {
			t.Fatalf("model = %q, want gpt-5-4", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	auth := newAuggieStreamTestAuth("token-1")
	auth.Metadata["model_short_name_aliases"] = map[string]any{
		"gpt5.4": "gpt-5-4",
	}
	chunks, err := executeAuggieStreamForModelTest(t, context.Background(), auth, server.URL, "gpt5.4")
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
}

func TestAuggieExecuteStream_RetriesUnauthorizedBeforeFirstByte(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		switch attempts {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer stale-token" {
				t.Fatalf("first authorization = %q, want Bearer stale-token", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
				t.Fatalf("second authorization = %q, want Bearer session-token", got)
			}
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprintln(w, `{"text":"hello","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	auth := newAuggieStreamTestAuth("stale-token")
	chunks, err := executeAuggieStreamForTest(t, context.Background(), auth, server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if got := auth.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
}

func TestBuildAuggieSystemPromptCustomizationFallbackPayload_StripsNativeFieldsAndInlinesPrompt(t *testing.T) {
	translated := []byte(`{
		"model":"gpt-5-4",
		"mode":"CHAT",
		"message":"hello",
		"system_prompt":"You are terse.",
		"system_prompt_append":"Only answer with JSON.",
		"chat_history":[]
	}`)
	upstreamError := []byte(`{"error":"System prompt customization (override, append, replacements) is not enabled."}`)

	fallback, ok := buildAuggieSystemPromptCustomizationFallbackPayload(translated, upstreamError)
	if !ok {
		t.Fatalf("expected fallback payload to be generated; translated=%s", translated)
	}
	if gjson.GetBytes(fallback, "system_prompt").Exists() {
		t.Fatalf("fallback payload should omit system_prompt; payload=%s", fallback)
	}
	if gjson.GetBytes(fallback, "system_prompt_append").Exists() {
		t.Fatalf("fallback payload should omit system_prompt_append; payload=%s", fallback)
	}
	if got := gjson.GetBytes(fallback, "message").String(); got != "You are terse.\n\nOnly answer with JSON.\n\nhello" {
		t.Fatalf("fallback message = %q, want inlined prompt+append+message; payload=%s", got, fallback)
	}
}

func TestAuggieExecuteStream_DoesNotRetryAfterFirstByte(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":`)
		flusher.Flush()
	}))
	defer server.Close()

	_, err := executeAuggieStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err == nil {
		t.Fatal("expected stream error")
	}
	status, ok := err.(interface{ StatusCode() int })
	if !ok || status.StatusCode() != http.StatusBadGateway {
		t.Fatalf("expected 502 status error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestAuggieExecute_AuggieSuspensionBannerReturnsForbiddenOnNonStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"\n\n---\n\n*Your account someone@example.com has been suspended. To continue, [purchase a subscription](https://app.augmentcode.com/account).*","nodes":[{"id":0,"type":0,"content":""},{"id":1,"type":2,"content":"\n\n---\n\n*Your account someone@example.com has been suspended. To continue, [purchase a subscription](https://app.augmentcode.com/account).*"}],"stop_reason":1}`)
		flusher.Flush()
	}))
	defer server.Close()

	_, err := executeAuggieNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err == nil {
		t.Fatal("expected non-stream error")
	}

	status, ok := err.(interface{ StatusCode() int })
	if !ok || status.StatusCode() != http.StatusForbidden {
		t.Fatalf("expected 403 status error, got %v", err)
	}
	if strings.Contains(err.Error(), "someone@example.com") {
		t.Fatalf("error leaked upstream account email: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "subscription") {
		t.Fatalf("error = %q, want sanitized subscription guidance", err.Error())
	}
}

func TestAuggieExecuteStream_AuggieSuspensionBannerReturnsForbiddenBeforeEmittingChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"\n\n---\n\n*Your account someone@example.com has been suspended. To continue, [purchase a subscription](https://app.augmentcode.com/account).*","nodes":[{"id":0,"type":0,"content":""},{"id":1,"type":2,"content":"\n\n---\n\n*Your account someone@example.com has been suspended. To continue, [purchase a subscription](https://app.augmentcode.com/account).*"}],"stop_reason":1}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err == nil {
		t.Fatal("expected stream error")
	}
	if len(chunks) != 0 {
		t.Fatalf("chunks = %d, want 0 before terminal error; chunks=%v", len(chunks), chunks)
	}

	status, ok := err.(interface{ StatusCode() int })
	if !ok || status.StatusCode() != http.StatusForbidden {
		t.Fatalf("expected 403 status error, got %v", err)
	}
	if strings.Contains(err.Error(), "someone@example.com") {
		t.Fatalf("error leaked upstream account email: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "subscription") {
		t.Fatalf("error = %q, want sanitized subscription guidance", err.Error())
	}
}

func TestAuggieExecuteStream_EmitsTranslatedOpenAIResponsesSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "message").String(); got != "You are terse.\n\nhelp me" {
			t.Fatalf("message = %q, want inlined instructions + help me", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	joined := strings.Join(chunks, "\n")
	if !strings.Contains(joined, "event: response.created") {
		t.Fatalf("missing response.created event: %s", joined)
	}
	if !strings.Contains(joined, `"type":"response.output_text.delta"`) {
		t.Fatalf("missing output_text.delta event: %s", joined)
	}
	if !strings.Contains(joined, `"delta":"hello"`) {
		t.Fatalf("missing hello delta: %s", joined)
	}
	if !strings.Contains(joined, `"type":"response.completed"`) {
		t.Fatalf("missing response.completed event: %s", joined)
	}
}

func TestAuggieExecuteStream_OpenAIResponsesLifecyclePreservesMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if gjson.GetBytes(body, "metadata").Exists() {
			t.Fatalf("translated Auggie request must not forward metadata upstream; body=%s", body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"help me"}]}],
		"metadata":{"trace_id":"trace-auggie-stream-1"}
	}`)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var (
		created    gjson.Result
		inProgress gjson.Result
	)
	for _, chunk := range chunks {
		event, payload := parseExecutorResponsesSSEEvent(t, chunk)
		switch event {
		case "response.created":
			created = payload
		case "response.in_progress":
			inProgress = payload
		}
	}

	if !created.Exists() {
		t.Fatalf("missing response.created event: %v", chunks)
	}
	if got := created.Get("response.metadata.trace_id").String(); got != "trace-auggie-stream-1" {
		t.Fatalf("response.created metadata.trace_id = %q, want trace-auggie-stream-1; event=%s", got, created.Raw)
	}
	if !inProgress.Exists() {
		t.Fatalf("missing response.in_progress event: %v", chunks)
	}
	if got := inProgress.Get("response.metadata.trace_id").String(); got != "trace-auggie-stream-1" {
		t.Fatalf("response.in_progress metadata.trace_id = %q, want trace-auggie-stream-1; event=%s", got, inProgress.Raw)
	}
}

func TestAuggieExecuteStream_EmitsTranslatedOpenAIResponsesIncompleteSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"max_output_tokens"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	joined := strings.Join(chunks, "\n")
	if !strings.Contains(joined, `"type":"response.incomplete"`) {
		t.Fatalf("missing response.incomplete event: %s", joined)
	}
	if !strings.Contains(joined, `"reason":"max_output_tokens"`) {
		t.Fatalf("missing incomplete_details.reason=max_output_tokens: %s", joined)
	}
	if strings.Contains(joined, `"type":"response.completed"`) {
		t.Fatalf("unexpected response.completed event: %s", joined)
	}
}

func TestAuggieExecuteStream_OpenAIResponsesSuppressesDuplicateCompletedEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	joined := strings.Join(chunks, "\n")
	if got := strings.Count(joined, `"type":"response.completed"`); got != 1 {
		t.Fatalf("response.completed count = %d, want 1: %s", got, joined)
	}
}

func TestAuggieExecuteStream_OpenAIResponsesSuppressesDuplicateIncompleteEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"max_output_tokens"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"stop_reason":"max_output_tokens"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	joined := strings.Join(chunks, "\n")
	if got := strings.Count(joined, `"type":"response.incomplete"`); got != 1 {
		t.Fatalf("response.incomplete count = %d, want 1: %s", got, joined)
	}
}

func TestAuggieExecuteStream_OpenAIResponsesWebSearchCompletesViaRemoteToolBridge(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0
	runRemoteToolCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)

			switch chatStreamCalls {
			case 1:
				if listRemoteToolsCalls != 1 {
					t.Fatalf("listRemoteToolsCalls before first /chat-stream = %d, want 1", listRemoteToolsCalls)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
					t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
					t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
					t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-stream-1","turn_id":"turn-web-stream-1","text":"","nodes":[{"tool_use":{"tool_use_id":"call_web_stream_1","tool_name":"web-search","input_json":"{\"query\":\"OpenAI latest news\",\"num_results\":1}","is_partial":false}}]}`)
				flusher.Flush()
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-stream-1","turn_id":"turn-web-stream-1","text":"","stop_reason":"tool_use"}`)
				flusher.Flush()
			case 2:
				if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-web-stream-1" {
					t.Fatalf("conversation_id = %q, want conv-web-stream-1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-web-stream-1" {
					t.Fatalf("turn_id = %q, want turn-web-stream-1; body=%s", got, body)
				}
				expectAuggieIDEStateNodeOnContinuation(t, body)
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != "call_web_stream_1" {
					t.Fatalf("tool_use_id = %q, want call_web_stream_1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.content").String(); !strings.Contains(got, "OpenAI News") {
					t.Fatalf("tool_result content = %q, want OpenAI News; body=%s", got, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-stream-1","turn_id":"turn-web-stream-2","text":"Top headline: OpenAI News","stop_reason":"end_turn"}`)
				flusher.Flush()
			default:
				t.Fatalf("unexpected /chat-stream call %d", chatStreamCalls)
			}

		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			if got := gjson.GetBytes(body, "tool_id_list.tool_ids.#").Int(); got == 0 {
				t.Fatalf("tool_id_list.tool_ids missing; body=%s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)

		case "/agents/run-remote-tool":
			runRemoteToolCalls++
			if got := gjson.GetBytes(body, "tool_name").String(); got != "web-search" {
				t.Fatalf("tool_name = %q, want web-search; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "tool_id").Int(); got != 1 {
				t.Fatalf("tool_id = %d, want 1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "tool_input_json").String(); !strings.Contains(got, "OpenAI latest news") {
				t.Fatalf("tool_input_json = %q, want OpenAI latest news; body=%s", got, body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintln(w, `{"tool_output":"- [OpenAI News](https://openai.com/news/)","tool_result_message":"","is_error":false,"status":1}`)

		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Find the latest OpenAI news"}]}
		],
		"tools":[{"type":"web_search"}]
	}`)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	if chatStreamCalls != 2 {
		t.Fatalf("chatStreamCalls = %d, want 2", chatStreamCalls)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1", listRemoteToolsCalls)
	}
	if runRemoteToolCalls != 1 {
		t.Fatalf("runRemoteToolCalls = %d, want 1", runRemoteToolCalls)
	}

	joined := strings.Join(chunks, "\n")
	if !strings.Contains(joined, `"delta":"Top headline: OpenAI News"`) {
		t.Fatalf("missing final output delta: %s", joined)
	}
	if !strings.Contains(joined, "event: response.web_search_call.searching") {
		t.Fatalf("missing response.web_search_call.searching event: %s", joined)
	}
	if !strings.Contains(joined, "event: response.web_search_call.completed") {
		t.Fatalf("missing response.web_search_call.completed event: %s", joined)
	}
	if !strings.Contains(joined, `"type":"web_search_call"`) {
		t.Fatalf("missing web_search_call output item: %s", joined)
	}
	if !strings.Contains(joined, `"type":"response.completed"`) {
		t.Fatalf("missing response.completed event: %s", joined)
	}
	if strings.Contains(joined, `"type":"response.function_call_arguments.delta"`) {
		t.Fatalf("unexpected function_call_arguments delta event: %s", joined)
	}
	if strings.Contains(joined, `"type":"response.output_item.added"`) && strings.Contains(joined, `"type":"function_call"`) {
		t.Fatalf("unexpected function_call output item in responses stream: %s", joined)
	}
}

type auggieUnexpectedEOFReadCloser struct {
	payload []byte
	emitted bool
}

func (r *auggieUnexpectedEOFReadCloser) Read(p []byte) (int, error) {
	if r.emitted {
		return 0, io.ErrUnexpectedEOF
	}
	r.emitted = true
	n := copy(p, r.payload)
	return n, io.ErrUnexpectedEOF
}

func (r *auggieUnexpectedEOFReadCloser) Close() error {
	return nil
}

func TestAuggieResponses_RetriesListRemoteToolsOnBodyReadUnexpectedEOF(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
				t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
			}
			if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
				t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
			}

			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprintln(w, `{"text":"hello from retry","stop_reason":"end_turn"}`)
			flusher.Flush()
		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)
		case "/agents/run-remote-tool":
			t.Fatalf("unexpected /agents/run-remote-tool request; body=%s", body)
		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	baseTransport := newAuggieRewriteTransport(t, server.URL)
	var listRequestAttemptCount atomic.Int32
	var injectedEOFCount atomic.Int32
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/agents/list-remote-tools" {
			listRequestAttemptCount.Add(1)
			if injectedEOFCount.CompareAndSwap(0, 1) {
				faultPayload := []byte(`{"tools":[{"remote_tool_id":1}]}`)
				return &http.Response{
					StatusCode:    http.StatusOK,
					Status:        "200 OK",
					Header:        http.Header{"Content-Type": []string{"application/json"}},
					ContentLength: int64(len(faultPayload) + 64),
					Body:          &auggieUnexpectedEOFReadCloser{payload: faultPayload},
					Request:       req,
				}, nil
			}
		}
		return baseTransport.RoundTrip(req)
	}))

	exec := NewAuggieExecutor(&config.Config{})
	payload := `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"web_search"}]
	}`
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(payload),
		Format:  sdktranslator.FormatOpenAIResponse,
	}
	opts := cliproxyexecutor.Options{
		Stream:          false,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
	}

	resp, err := exec.Execute(ctx, newAuggieStreamTestAuth("token-1"), req, opts)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := injectedEOFCount.Load(); got != 1 {
		t.Fatalf("injected EOF count = %d, want 1", got)
	}
	if got := listRequestAttemptCount.Load(); got != 2 {
		t.Fatalf("list request attempts = %d, want 2 (one failed + one retry)", got)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1 successful retry call", listRemoteToolsCalls)
	}
	if chatStreamCalls != 1 {
		t.Fatalf("chatStreamCalls = %d, want 1", chatStreamCalls)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "hello from retry" {
		t.Fatalf("message output text = %q, want hello from retry; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_RetriesListRemoteToolsOnTransportUnexpectedEOF(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
				t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
			}

			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprintln(w, `{"text":"hello after transport retry","stop_reason":"end_turn"}`)
			flusher.Flush()
		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)
		case "/agents/run-remote-tool":
			t.Fatalf("unexpected /agents/run-remote-tool request; body=%s", body)
		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	baseTransport := newAuggieRewriteTransport(t, server.URL)
	var listRequestAttemptCount atomic.Int32
	var injectedTransportEOFCount atomic.Int32
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/agents/list-remote-tools" {
			listRequestAttemptCount.Add(1)
			if injectedTransportEOFCount.CompareAndSwap(0, 1) {
				return nil, io.ErrUnexpectedEOF
			}
		}
		return baseTransport.RoundTrip(req)
	}))

	exec := NewAuggieExecutor(&config.Config{})
	payload := `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"web_search"}]
	}`
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(payload),
		Format:  sdktranslator.FormatOpenAIResponse,
	}
	opts := cliproxyexecutor.Options{
		Stream:          false,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
	}

	resp, err := exec.Execute(ctx, newAuggieStreamTestAuth("token-1"), req, opts)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := injectedTransportEOFCount.Load(); got != 1 {
		t.Fatalf("injected transport EOF count = %d, want 1", got)
	}
	if got := listRequestAttemptCount.Load(); got != 2 {
		t.Fatalf("list request attempts = %d, want 2 (one failed + one retry)", got)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1 successful retry call", listRemoteToolsCalls)
	}
	if chatStreamCalls != 1 {
		t.Fatalf("chatStreamCalls = %d, want 1", chatStreamCalls)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "hello after transport retry" {
		t.Fatalf("message output text = %q, want hello after transport retry; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecuteStream_OpenAIResponsesWebSearchIncludesRequestedSourcesAndResults(t *testing.T) {
	chatStreamCalls := 0
	listRemoteToolsCalls := 0
	runRemoteToolCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		switch r.URL.Path {
		case "/chat-stream":
			chatStreamCalls++
			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)

			switch chatStreamCalls {
			case 1:
				if listRemoteToolsCalls != 1 {
					t.Fatalf("listRemoteToolsCalls before first /chat-stream = %d, want 1", listRemoteToolsCalls)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
					t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.description").String(); got != testAuggieWebSearchDescription {
					t.Fatalf("tool_definitions.0.description = %q, want %q; body=%s", got, testAuggieWebSearchDescription, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.input_schema_json").String(); got != testAuggieWebSearchInputSchema {
					t.Fatalf("tool_definitions.0.input_schema_json = %q, want %q; body=%s", got, testAuggieWebSearchInputSchema, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-stream-include-1","turn_id":"turn-web-stream-include-1","text":"","nodes":[{"tool_use":{"tool_use_id":"call_web_stream_include_1","tool_name":"web-search","input_json":"{\"query\":\"OpenAI latest news\",\"num_results\":2}","is_partial":false}}]}`)
				flusher.Flush()
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-stream-include-1","turn_id":"turn-web-stream-include-1","text":"","stop_reason":"tool_use"}`)
				flusher.Flush()
			case 2:
				expectAuggieIDEStateNodeOnContinuation(t, body)
				if got := gjson.GetBytes(body, "nodes.1.tool_result_node.tool_use_id").String(); got != "call_web_stream_include_1" {
					t.Fatalf("tool_use_id = %q, want call_web_stream_include_1; body=%s", got, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-stream-include-1","turn_id":"turn-web-stream-include-2","text":"Top headline: OpenAI News","stop_reason":"end_turn"}`)
				flusher.Flush()
			default:
				t.Fatalf("unexpected /chat-stream call %d", chatStreamCalls)
			}

		case "/agents/list-remote-tools":
			listRemoteToolsCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search","description":%q,"input_schema_json":%q}}]}`+"\n", testAuggieWebSearchDescription, testAuggieWebSearchInputSchema)

		case "/agents/run-remote-tool":
			runRemoteToolCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintln(w, `{"tool_output":"- [OpenAI News](https://openai.com/news/) Latest updates from OpenAI\n- [OpenAI Blog](https://openai.com/blog/) Product announcements","tool_result_message":"","is_error":false,"status":1}`)

		default:
			t.Fatalf("unexpected path %q body=%s", r.URL.Path, body)
		}
	}))
	defer server.Close()

	chunks, err := executeAuggieResponsesStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Find the latest OpenAI news"}]}
		],
		"tools":[{"type":"web_search"}],
		"include":["web_search_call.results","web_search_call.action.sources"]
	}`)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	if chatStreamCalls != 2 {
		t.Fatalf("chatStreamCalls = %d, want 2", chatStreamCalls)
	}
	if listRemoteToolsCalls != 1 {
		t.Fatalf("listRemoteToolsCalls = %d, want 1", listRemoteToolsCalls)
	}
	if runRemoteToolCalls != 1 {
		t.Fatalf("runRemoteToolCalls = %d, want 1", runRemoteToolCalls)
	}

	var (
		itemDone  gjson.Result
		completed gjson.Result
	)
	for _, chunk := range chunks {
		event, data := parseExecutorResponsesSSEEvent(t, chunk)
		switch event {
		case "response.output_item.done":
			if data.Get("item.type").String() == "web_search_call" {
				itemDone = data
			}
		case "response.completed":
			completed = data
		}
	}

	if !itemDone.Exists() {
		t.Fatalf("missing web_search_call output_item.done event: %v", chunks)
	}
	if got := itemDone.Get("item.action.sources.0.url").String(); got != "https://openai.com/news/" {
		t.Fatalf("output_item.done action.sources[0].url = %q, want https://openai.com/news/; chunk=%s", got, itemDone.Raw)
	}
	if got := itemDone.Get("item.results.0.title").String(); got != "OpenAI News" {
		t.Fatalf("output_item.done results[0].title = %q, want OpenAI News; chunk=%s", got, itemDone.Raw)
	}
	if got := itemDone.Get("item.results.0.text").String(); got != "Latest updates from OpenAI" {
		t.Fatalf("output_item.done results[0].text = %q, want Latest updates from OpenAI; chunk=%s", got, itemDone.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event: %v", chunks)
	}
	if got := completed.Get(`response.output.#(type=="web_search_call").action.query`).String(); got != "OpenAI latest news" {
		t.Fatalf("response.output web_search_call action.query = %q, want OpenAI latest news; chunk=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(type=="web_search_call").action.sources.1.url`).String(); got != "https://openai.com/blog/" {
		t.Fatalf("response.output web_search_call action.sources[1].url = %q, want https://openai.com/blog/; chunk=%s", got, completed.Raw)
	}
	if got := completed.Get(`response.output.#(type=="web_search_call").results.1.title`).String(); got != "OpenAI Blog" {
		t.Fatalf("response.output web_search_call results[1].title = %q, want OpenAI Blog; chunk=%s", got, completed.Raw)
	}
}

func TestAuggieExecute_AggregatesTranslatedStreamIntoClaudeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want Bearer token-1", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "model").String(); got != "claude-sonnet-4-6" {
			t.Fatalf("model = %q, want claude-sonnet-4-6", got)
		}
		if got := gjson.GetBytes(body, "mode").String(); got != "CHAT" {
			t.Fatalf("mode = %q, want CHAT", got)
		}
		if got := gjson.GetBytes(body, "message").String(); got != "You are terse.\n\nhelp me" {
			t.Fatalf("message = %q, want inlined system instructions + help me", got)
		}
		if got := gjson.GetBytes(body, "chat_history.0.request_message").String(); got != "hello" {
			t.Fatalf("chat_history[0].request_message = %q, want hello", got)
		}
		if got := gjson.GetBytes(body, "chat_history.0.response_text").String(); got != "hi" {
			t.Fatalf("chat_history[0].response_text = %q, want hi", got)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "list_files" {
			t.Fatalf("tool_definitions[0].name = %q, want list_files", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieClaudeNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "type").String(); got != "message" {
		t.Fatalf("type = %q, want message", got)
	}
	if got := gjson.GetBytes(resp.Payload, "role").String(); got != "assistant" {
		t.Fatalf("role = %q, want assistant", got)
	}
	if got := gjson.GetBytes(resp.Payload, "model").String(); got != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", got)
	}
	if got := gjson.GetBytes(resp.Payload, "content.0.type").String(); got != "text" {
		t.Fatalf("content[0].type = %q, want text", got)
	}
	if got := gjson.GetBytes(resp.Payload, "content.0.text").String(); got != "hello world" {
		t.Fatalf("content[0].text = %q, want hello world", got)
	}
	if got := gjson.GetBytes(resp.Payload, "stop_reason").String(); got != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn", got)
	}
}

func TestAuggieExecuteStream_EmitsTranslatedClaudeSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want Bearer token-1", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "model").String(); got != "claude-sonnet-4-6" {
			t.Fatalf("model = %q, want claude-sonnet-4-6", got)
		}
		if got := gjson.GetBytes(body, "message").String(); got != "You are terse.\n\nhelp me" {
			t.Fatalf("message = %q, want inlined system instructions + help me", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieClaudeStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	joined := strings.Join(chunks, "\n")
	if !strings.Contains(joined, "event: message_start") {
		t.Fatalf("missing message_start event: %s", joined)
	}
	if !strings.Contains(joined, "event: content_block_start") {
		t.Fatalf("missing content_block_start event: %s", joined)
	}
	if !strings.Contains(joined, "event: content_block_delta") {
		t.Fatalf("missing content_block_delta event: %s", joined)
	}
	if !strings.Contains(joined, `"type":"text_delta"`) {
		t.Fatalf("missing text_delta chunk: %s", joined)
	}
	if !strings.Contains(joined, `"text":"hello"`) {
		t.Fatalf("missing hello text delta: %s", joined)
	}
	if !strings.Contains(joined, `"stop_reason":"end_turn"`) {
		t.Fatalf("missing end_turn stop reason: %s", joined)
	}
	if !strings.Contains(joined, "event: message_stop") {
		t.Fatalf("missing message_stop event: %s", joined)
	}
}

func TestAuggieCountTokens_ReturnsTranslatedOpenAIUsage(t *testing.T) {
	exec := NewAuggieExecutor(&config.Config{})
	openAICompat := NewOpenAICompatExecutor("openai", &config.Config{})

	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"messages":[
				{"role":"system","content":"You are terse."},
				{"role":"user","content":"hello"},
				{"role":"assistant","content":"hi"},
				{"role":"user","content":"help me"}
			],
			"tools":[{"type":"function","function":{"name":"list_files","description":"List files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]
		}`),
		Format: sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAI,
	}

	resp, err := exec.CountTokens(context.Background(), newAuggieStreamTestAuth("token-1"), req, opts)
	if err != nil {
		t.Fatalf("Auggie CountTokens error: %v", err)
	}

	expected, err := openAICompat.CountTokens(context.Background(), nil, req, opts)
	if err != nil {
		t.Fatalf("OpenAI compat CountTokens error: %v", err)
	}

	if got, want := gjson.GetBytes(resp.Payload, "usage.prompt_tokens").Int(), gjson.GetBytes(expected.Payload, "usage.prompt_tokens").Int(); got != want {
		t.Fatalf("usage.prompt_tokens = %d, want %d; payload=%s", got, want, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "usage.total_tokens").Int(); got <= 0 {
		t.Fatalf("usage.total_tokens = %d, want > 0; payload=%s", got, resp.Payload)
	}
}

func TestAuggieCountTokens_ReturnsTranslatedClaudeUsage(t *testing.T) {
	exec := NewAuggieExecutor(&config.Config{})
	openAICompat := NewOpenAICompatExecutor("openai", &config.Config{})

	req := cliproxyexecutor.Request{
		Model: "claude-sonnet-4-6",
		Payload: []byte(`{
			"system":"You are terse.",
			"messages":[
				{"role":"user","content":[{"type":"text","text":"hello"}]},
				{"role":"assistant","content":[{"type":"text","text":"hi"}]},
				{"role":"user","content":[{"type":"text","text":"help me"}]}
			],
			"tools":[{"name":"list_files","description":"List files","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
		}`),
		Format: sdktranslator.FormatClaude,
	}
	opts := cliproxyexecutor.Options{
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatClaude,
	}

	resp, err := exec.CountTokens(context.Background(), newAuggieStreamTestAuth("token-1"), req, opts)
	if err != nil {
		t.Fatalf("Auggie CountTokens error: %v", err)
	}

	openAIReq := req
	openAIReq.Format = sdktranslator.FormatOpenAI
	openAIReq.Payload = sdktranslator.TranslateRequest(sdktranslator.FormatClaude, sdktranslator.FormatOpenAI, req.Model, req.Payload, false)
	openAIOpts := opts
	openAIOpts.SourceFormat = sdktranslator.FormatOpenAI

	expected, err := openAICompat.CountTokens(context.Background(), nil, openAIReq, openAIOpts)
	if err != nil {
		t.Fatalf("OpenAI compat CountTokens error: %v", err)
	}

	if got, want := gjson.GetBytes(resp.Payload, "input_tokens").Int(), gjson.GetBytes(expected.Payload, "usage.prompt_tokens").Int(); got != want {
		t.Fatalf("input_tokens = %d, want %d; payload=%s", got, want, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "input_tokens").Int(); got <= 0 {
		t.Fatalf("input_tokens = %d, want > 0; payload=%s", got, resp.Payload)
	}
}

func TestAuggieExecute_CompactReturnsRehydratableResponsesOutput(t *testing.T) {
	exec := NewAuggieExecutor(&config.Config{})

	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"instructions":"You are terse.",
			"input":[
				{"role":"user","content":[{"type":"input_text","text":"hello"}]},
				{"role":"assistant","content":[{"type":"output_text","text":"hi"}]},
				{"role":"user","content":[{"type":"input_text","text":"help me"}]}
			]
		}`),
		Format: sdktranslator.FormatOpenAIResponse,
	}
	opts := cliproxyexecutor.Options{
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		Alt:             "responses/compact",
	}

	resp, err := exec.Execute(context.Background(), newAuggieStreamTestAuth("token-1"), req, opts)
	if err != nil {
		t.Fatalf("Auggie compact Execute error: %v", err)
	}

	if got := gjson.GetBytes(resp.Payload, "object").String(); got != "response.compaction" {
		t.Fatalf("object = %q, want response.compaction; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.role").String(); got != "system" {
		t.Fatalf("output[0].role = %q, want system; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "You are terse." {
		t.Fatalf("output[0].content[0].text = %q, want %q; payload=%s", got, "You are terse.", resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "usage.input_tokens").Int(); got <= 0 {
		t.Fatalf("usage.input_tokens = %d, want > 0; payload=%s", got, resp.Payload)
	}

	output := gjson.GetBytes(resp.Payload, "output")
	nextPayload := mustMarshalAuggieJSON(t, map[string]any{
		"model": "gpt-5.4",
		"input": output.Value(),
	})
	translated := sdktranslator.TranslateRequest(sdktranslator.FormatOpenAIResponse, sdktranslator.FormatOpenAI, "gpt-5.4", []byte(nextPayload), false)

	if got := gjson.GetBytes(translated, "messages.0.role").String(); got != "system" {
		t.Fatalf("translated messages[0].role = %q, want system; translated=%s", got, translated)
	}
	if got := gjson.GetBytes(translated, "messages.0.content.0.text").String(); got != "You are terse." {
		t.Fatalf("translated messages[0].content[0].text = %q, want %q; translated=%s", got, "You are terse.", translated)
	}
	if got := gjson.GetBytes(translated, "messages.1.content.0.text").String(); got != "hello" {
		t.Fatalf("translated messages[1].content[0].text = %q, want hello; translated=%s", got, translated)
	}
	if got := gjson.GetBytes(translated, "messages.2.content.0.text").String(); got != "hi" {
		t.Fatalf("translated messages[2].content[0].text = %q, want hi; translated=%s", got, translated)
	}
	if got := gjson.GetBytes(translated, "messages.3.content.0.text").String(); got != "help me" {
		t.Fatalf("translated messages[3].content[0].text = %q, want help me; translated=%s", got, translated)
	}
}

func executeAuggieStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) ([]string, error) {
	t.Helper()

	return executeAuggieStreamForModelTest(t, ctx, auth, targetURL, "gpt-5.4")
}

func executeAuggieStreamWithPayloadForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL, payload string) ([]string, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(payload),
		Format:  sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{
		Stream:          true,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAI,
	}

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			return chunks, chunk.Err
		}
		chunks = append(chunks, string(chunk.Payload))
	}
	return chunks, nil
}

func executeAuggieNonStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) (cliproxyexecutor.Response, error) {
	t.Helper()

	return executeAuggieNonStreamWithPayloadForTest(t, ctx, auth, targetURL, `{
		"messages":[
			{"role":"system","content":"You are terse."},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"help me"}
		]
	}`)
}

func executeAuggieNonStreamWithPayloadForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL, payload string) (cliproxyexecutor.Response, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(payload),
		Format:  sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{
		Stream:          false,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAI,
	}

	return exec.Execute(ctx, auth, req, opts)
}

func executeAuggieResponsesNonStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) (cliproxyexecutor.Response, error) {
	t.Helper()

	return executeAuggieResponsesNonStreamWithPayloadForTest(t, ctx, auth, targetURL, `{
		"instructions":"You are terse.",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"hi"}]},
			{"role":"user","content":[{"type":"input_text","text":"help me"}]}
		],
		"tools":[{"type":"function","name":"list_files","description":"List files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
}

func executeAuggieResponsesNonStreamWithPayloadForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL, payload string) (cliproxyexecutor.Response, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(payload),
		Format:  sdktranslator.FormatOpenAIResponse,
	}
	opts := cliproxyexecutor.Options{
		Stream:          false,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
	}

	return exec.Execute(ctx, auth, req, opts)
}

func executeAuggieResponsesStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) ([]string, error) {
	t.Helper()

	return executeAuggieResponsesStreamWithPayloadForTest(t, ctx, auth, targetURL, `{
		"instructions":"You are terse.",
		"input":[{"role":"user","content":[{"type":"input_text","text":"help me"}]}]
	}`)
}

func executeAuggieResponsesStreamWithPayloadForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL, payload string) ([]string, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(payload),
		Format:  sdktranslator.FormatOpenAIResponse,
	}
	opts := cliproxyexecutor.Options{
		Stream:          true,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
	}

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			return chunks, chunk.Err
		}
		chunks = append(chunks, string(chunk.Payload))
	}
	return chunks, nil
}

func executeAuggieClaudeNonStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) (cliproxyexecutor.Response, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model: "claude-sonnet-4-6",
		Payload: []byte(`{
			"system":"You are terse.",
			"messages":[
				{"role":"user","content":[{"type":"text","text":"hello"}]},
				{"role":"assistant","content":[{"type":"text","text":"hi"}]},
				{"role":"user","content":[{"type":"text","text":"help me"}]}
			],
			"tools":[{"name":"list_files","description":"List files","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
		}`),
		Format: sdktranslator.FormatClaude,
	}
	opts := cliproxyexecutor.Options{
		Stream:          false,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatClaude,
	}

	return exec.Execute(ctx, auth, req, opts)
}

func executeAuggieClaudeStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) ([]string, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model: "claude-sonnet-4-6",
		Payload: []byte(`{
			"system":"You are terse.",
			"messages":[
				{"role":"user","content":[{"type":"text","text":"hello"}]},
				{"role":"assistant","content":[{"type":"text","text":"hi"}]},
				{"role":"user","content":[{"type":"text","text":"help me"}]}
			],
			"tools":[{"name":"list_files","description":"List files","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
			"stream":true
		}`),
		Format: sdktranslator.FormatClaude,
	}
	opts := cliproxyexecutor.Options{
		Stream:          true,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatClaude,
	}

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			return chunks, chunk.Err
		}
		chunks = append(chunks, string(chunk.Payload))
	}
	return chunks, nil
}

func executeAuggieStreamForModelTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL, model string) ([]string, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model: model,
		Payload: []byte(`{
			"messages":[
				{"role":"system","content":"You are terse."},
				{"role":"user","content":"hello"},
				{"role":"assistant","content":"hi"},
				{"role":"user","content":"help me"}
			],
			"tools":[{"type":"function","function":{"name":"list_files","description":"List files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}]
		}`),
		Format: sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{
		Stream:          true,
		OriginalRequest: req.Payload,
		SourceFormat:    sdktranslator.FormatOpenAI,
	}

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			return chunks, chunk.Err
		}
		chunks = append(chunks, string(chunk.Payload))
	}
	return chunks, nil
}

func newAuggieStreamTestAuth(token string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": token,
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}
}

func TestAuggieExecuteStream_EmitsToolCallsInOpenAIChunks(t *testing.T) {
	const internalToolUseID = "tooluse_abc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"tool_use":{"id":"tooluse_abc","name":"get_weather","input":{"location":"SF"}}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"","stop_reason":"tool_use"}`)
		flusher.Flush()
	}))
	defer server.Close()

	chunks, err := executeAuggieStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}

	// First chunk should contain tool_calls
	if !strings.Contains(chunks[0], `"tool_calls"`) {
		t.Fatalf("expected tool_calls in first chunk: %s", chunks[0])
	}
	if !strings.Contains(chunks[0], `"get_weather"`) {
		t.Fatalf("expected get_weather in first chunk: %s", chunks[0])
	}
	if got := gjson.Get(chunks[0], "choices.0.delta.tool_calls.0.id").String(); !strings.HasPrefix(got, "call_") {
		t.Fatalf("tool_call id = %q, want public call_* id; chunk=%s", got, chunks[0])
	}
	if got := gjson.Get(chunks[0], "choices.0.delta.tool_calls.0.id").String(); got == internalToolUseID {
		t.Fatalf("tool_call id = %q, should not expose upstream internal id; chunk=%s", got, chunks[0])
	}

	// Second chunk should have finish_reason=tool_calls
	if !strings.Contains(chunks[1], `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected finish_reason tool_calls in second chunk: %s", chunks[1])
	}
}

func TestAuggieExecute_AggregatesToolCallsInNonStreamResponse(t *testing.T) {
	const internalToolUseID = "tooluse_xyz"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"tool_use":{"id":"tooluse_xyz","name":"read_file","input":{"path":"/tmp/test.txt"}}}]}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"","stop_reason":"tool_use"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieNonStreamForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(resp.Payload, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content"); got.Type != gjson.Null {
		t.Fatalf("message.content = %s, want null when response only contains tool_calls; payload=%s", got.Raw, resp.Payload)
	}
	tc := gjson.GetBytes(resp.Payload, "choices.0.message.tool_calls")
	if !tc.Exists() || !tc.IsArray() {
		t.Fatalf("expected tool_calls array in message; payload=%s", resp.Payload)
	}
	if tc.Get("#").Int() != 1 {
		t.Fatalf("expected 1 tool_call, got %d; payload=%s", tc.Get("#").Int(), resp.Payload)
	}
	if got := tc.Get("0.id").String(); !strings.HasPrefix(got, "call_") {
		t.Fatalf("tool_call id = %q, want public call_* id", got)
	}
	if got := tc.Get("0.id").String(); got == internalToolUseID {
		t.Fatalf("tool_call id = %q, should not expose upstream internal id", got)
	}
	if got := tc.Get("0.function.name").String(); got != "read_file" {
		t.Fatalf("function.name = %q, want read_file", got)
	}
	if got := tc.Get("0.function.arguments").String(); !strings.Contains(got, "/tmp/test.txt") {
		t.Fatalf("function.arguments = %q, want to contain /tmp/test.txt", got)
	}
}
