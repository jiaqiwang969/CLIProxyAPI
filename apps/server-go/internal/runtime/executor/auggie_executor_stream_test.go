package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

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
		if got := gjson.GetBytes(body, "message").String(); got != "help me" {
			t.Fatalf("message = %q, want help me", got)
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

func TestAuggieExecute_OpenAIChatCompletionToolLoopCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_chat_1" {
			t.Fatalf("tool_use_id = %q, want call_chat_1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "nodes.0.tool_result_node.content").String(); got != "{\"temperature\":23}" {
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
			if gjson.GetBytes(body, "conversation_id").Exists() {
				t.Fatalf("first request must not include conversation_id; body=%s", body)
			}
			if gjson.GetBytes(body, "turn_id").Exists() {
				t.Fatalf("first request must not include turn_id; body=%s", body)
			}
			_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-chat-1","turn_id":"turn-chat-1","nodes":[{"tool_use":{"id":"call_chat_state_1","name":"get_weather","input":{"location":"Boston"}}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		case 2:
			if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-chat-1" {
				t.Fatalf("conversation_id = %q, want conv-chat-1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-chat-1" {
				t.Fatalf("turn_id = %q, want turn-chat-1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_chat_state_1" {
				t.Fatalf("tool_use_id = %q, want call_chat_state_1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.0.tool_result_node.content").String(); got != "{\"temperature\":7,\"condition\":\"Cloudy\"}" {
				t.Fatalf("tool_result content = %q, want weather payload; body=%s", got, body)
			}
			_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-chat-1","turn_id":"turn-chat-2","text":"Boston is 7C and cloudy.","stop_reason":"end_turn"}`)
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

	toolCalls := gjson.GetBytes(firstResp.Payload, "choices.0.message.tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() || len(toolCalls.Array()) != 1 {
		t.Fatalf("first response missing tool_calls: %s", firstResp.Payload)
	}
	toolCallID := toolCalls.Get("0.id").String()
	if toolCallID != "call_chat_state_1" {
		t.Fatalf("tool_call_id = %q, want call_chat_state_1; payload=%s", toolCallID, firstResp.Payload)
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
				if got := gjson.GetBytes(body, "tool_definitions.#").Int(); got != 1 {
					t.Fatalf("tool_definitions length = %d, want 1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
					t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
				}
				_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-web-1","turn_id":"turn-web-1","text":"","nodes":[{"tool_use":{"tool_use_id":"call_web_1","tool_name":"web-search","input_json":"{\"query\":\"OpenAI latest news\",\"num_results\":1}","is_partial":false}}]}`)
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
				if got := gjson.GetBytes(body, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_web_1" {
					t.Fatalf("tool_use_id = %q, want call_web_1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "nodes.0.tool_result_node.content").String(); !strings.Contains(got, "OpenAI News") {
					t.Fatalf("tool_result content = %q, want OpenAI News; body=%s", got, body)
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
			_, _ = fmt.Fprintln(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search"}}]}`)

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
		"tools":[{"type":"web_search","search_context_size":"high"}]
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
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "Top headline: OpenAI News" {
		t.Fatalf("message output text = %q, want Top headline: OpenAI News; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="function_call")#`).Int(); got != 0 {
		t.Fatalf("function_call outputs = %d, want 0; payload=%s", got, resp.Payload)
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
		if got := gjson.GetBytes(body, "message").String(); got != "help me" {
			t.Fatalf("message = %q, want help me", got)
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
			if gjson.GetBytes(body, "conversation_id").Exists() {
				t.Fatalf("first request must not include conversation_id; body=%s", body)
			}
			_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-prev-1","turn_id":"turn-prev-1","nodes":[{"tool_use":{"id":"call_prev_1","name":"get_weather","input":{"location":"Shanghai"}}}],"stop_reason":"tool_use"}`)
			flusher.Flush()
		case 2:
			if got := gjson.GetBytes(body, "conversation_id").String(); got != "conv-prev-1" {
				t.Fatalf("conversation_id = %q, want conv-prev-1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "turn_id").String(); got != "turn-prev-1" {
				t.Fatalf("turn_id = %q, want turn-prev-1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_prev_1" {
				t.Fatalf("tool_use_id = %q, want call_prev_1; body=%s", got, body)
			}
			if got := gjson.GetBytes(body, "nodes.0.tool_result_node.content").String(); got != "{\"temperature\":23}" {
				t.Fatalf("tool_result content = %q, want {\"temperature\":23}; body=%s", got, body)
			}
			_, _ = fmt.Fprintln(w, `{"conversation_id":"conv-prev-1","turn_id":"turn-prev-2","text":"The temperature is 23C","stop_reason":"end_turn"}`)
			flusher.Flush()
		default:
			t.Fatalf("unexpected attempt %d", attempts)
		}
	}))
	defer server.Close()

	firstPayload := `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Weather in Shanghai?"}]}],
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]
	}`
	firstResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, firstPayload)
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
		"input":[{"type":"function_call_output","call_id":"call_prev_1","output":"{\"temperature\":23}"}],
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]
	}`, responseID)
	secondResp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, secondPayload)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if got := gjson.GetBytes(secondResp.Payload, `output.#(type=="message").content.0.text`).String(); got != "The temperature is 23C" {
		t.Fatalf("message output text = %q, want The temperature is 23C; payload=%s", got, secondResp.Payload)
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
	if !strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("error = %q, want mention of previous_response_id", err.Error())
	}
}

func TestAuggieResponses_FullInlineToolLoopCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_inline_1" {
			t.Fatalf("tool_use_id = %q, want call_inline_1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "nodes.0.tool_result_node.content").String(); got != "{\"temperature\":23}" {
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
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]
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

func TestAuggieResponses_ReturnsOpenAIErrorForUnsupportedToolType(t *testing.T) {
	for _, toolType := range []string{"custom"} {
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
			name:          "custom_tool_call",
			inputItemJSON: `{"type":"custom_tool_call","call_id":"custom-call-1","name":"bash","input":"ls"}`,
		},
		{
			name:          "custom_tool_call_output",
			inputItemJSON: `{"type":"custom_tool_call_output","call_id":"custom-call-1","output":"file1\nfile2"}`,
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
			if !strings.Contains(err.Error(), `input[0].type`) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("error = %q, want mention of input[0].type and %q", err.Error(), tc.name)
			}
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
		"include":["reasoning.encrypted_content","message.output_text.logprobs"]
	}`)
	if err != nil {
		t.Fatalf("unexpected error for supported include: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "hello" {
		t.Fatalf("response output text = %q, want hello; payload=%s", got, resp.Payload)
	}
}

func TestAuggieResponses_AllowsAdditionalDocumentedIncludeValues(t *testing.T) {
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
		"include":["web_search_call.results","web_search_call.action.sources","code_interpreter_call.outputs"]
	}`)
	if err != nil {
		t.Fatalf("unexpected error for additional documented include values: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "output.0.content.0.text").String(); got != "hello" {
		t.Fatalf("response output text = %q, want hello; payload=%s", got, resp.Payload)
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
	if !strings.Contains(err.Error(), "unsupported.include") {
		t.Fatalf("error = %q, want mention of unsupported.include", err.Error())
	}
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
			if !strings.Contains(err.Error(), "tools[0].type") || !strings.Contains(err.Error(), toolType) {
				t.Fatalf("error = %q, want mention of tools[0].type and %q", err.Error(), toolType)
			}
		})
	}
}

func TestAuggieExecute_OpenAIReturnsOpenAIErrorForUnsupportedToolChoice(t *testing.T) {
	testCases := []struct {
		name           string
		toolChoiceJSON string
	}{
		{name: "required", toolChoiceJSON: `"required"`},
		{name: "function_object", toolChoiceJSON: `{"type":"function","function":{"name":"get_weather"}}`},
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
			if !strings.Contains(err.Error(), "tool_choice") {
				t.Fatalf("error = %q, want mention of tool_choice", err.Error())
			}
		})
	}
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
	if !strings.Contains(err.Error(), "include") {
		t.Fatalf("error = %q, want mention of include", err.Error())
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
		{name: "required", toolChoiceJSON: `"required"`},
		{name: "function_object", toolChoiceJSON: `{"type":"function","function":{"name":"get_weather"}}`},
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
				"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}],
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
			if !strings.Contains(err.Error(), "tool_choice") {
				t.Fatalf("error = %q, want mention of tool_choice", err.Error())
			}
		})
	}
}

func TestAuggieResponses_AllowsBuiltInWebSearchToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
			t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"headline ready","stop_reason":"end_turn"}`)
		flusher.Flush()
	}))
	defer server.Close()

	resp, err := executeAuggieResponsesNonStreamWithPayloadForTest(t, context.Background(), newAuggieStreamTestAuth("token-1"), server.URL, `{
		"model":"gpt-5.4",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"web_search","search_context_size":"high"}],
		"tool_choice":{"type":"web_search"}
	}`)
	if err != nil {
		t.Fatalf("unexpected error for built-in web_search tool_choice: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, `output.#(type=="message").content.0.text`).String(); got != "headline ready" {
		t.Fatalf("message output text = %q, want headline ready; payload=%s", got, resp.Payload)
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
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}],
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

func TestAuggieExecuteStream_EmitsTranslatedOpenAIResponsesSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat-stream" {
			t.Fatalf("path = %q, want /chat-stream", r.URL.Path)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := gjson.GetBytes(body, "message").String(); got != "help me" {
			t.Fatalf("message = %q, want help me", got)
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
				if got := gjson.GetBytes(body, "tool_definitions.0.name").String(); got != "web-search" {
					t.Fatalf("tool_definitions.0.name = %q, want web-search; body=%s", got, body)
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
				if got := gjson.GetBytes(body, "nodes.0.tool_result_node.tool_use_id").String(); got != "call_web_stream_1" {
					t.Fatalf("tool_use_id = %q, want call_web_stream_1; body=%s", got, body)
				}
				if got := gjson.GetBytes(body, "nodes.0.tool_result_node.content").String(); !strings.Contains(got, "OpenAI News") {
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
			_, _ = fmt.Fprintln(w, `{"tools":[{"remote_tool_id":1,"availability_status":1,"tool_safety":1,"tool_definition":{"name":"web-search"}}]}`)

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
		"tools":[{"type":"web_search","search_context_size":"high"}]
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
		if got := gjson.GetBytes(body, "message").String(); got != "help me" {
			t.Fatalf("message = %q, want help me", got)
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
		if got := gjson.GetBytes(body, "message").String(); got != "help me" {
			t.Fatalf("message = %q, want help me", got)
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
		"tools":[{"type":"function","name":"list_files","description":"List files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]
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
	if !strings.Contains(chunks[0], `"tooluse_abc"`) {
		t.Fatalf("expected tooluse_abc id in first chunk: %s", chunks[0])
	}

	// Second chunk should have finish_reason=tool_calls
	if !strings.Contains(chunks[1], `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected finish_reason tool_calls in second chunk: %s", chunks[1])
	}
}

func TestAuggieExecute_AggregatesToolCallsInNonStreamResponse(t *testing.T) {
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
	tc := gjson.GetBytes(resp.Payload, "choices.0.message.tool_calls")
	if !tc.Exists() || !tc.IsArray() {
		t.Fatalf("expected tool_calls array in message; payload=%s", resp.Payload)
	}
	if tc.Get("#").Int() != 1 {
		t.Fatalf("expected 1 tool_call, got %d; payload=%s", tc.Get("#").Int(), resp.Payload)
	}
	if got := tc.Get("0.id").String(); got != "tooluse_xyz" {
		t.Fatalf("tool_call id = %q, want tooluse_xyz", got)
	}
	if got := tc.Get("0.function.name").String(); got != "read_file" {
		t.Fatalf("function.name = %q, want read_file", got)
	}
	if got := tc.Get("0.function.arguments").String(); !strings.Contains(got, "/tmp/test.txt") {
		t.Fatalf("function.arguments = %q, want to contain /tmp/test.txt", got)
	}
}
