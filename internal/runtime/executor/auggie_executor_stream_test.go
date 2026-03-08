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

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"messages":[
				{"role":"system","content":"You are terse."},
				{"role":"user","content":"hello"},
				{"role":"assistant","content":"hi"},
				{"role":"user","content":"help me"}
			]
		}`),
		Format: sdktranslator.FormatOpenAI,
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

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"instructions":"You are terse.",
			"input":[
				{"role":"user","content":[{"type":"input_text","text":"hello"}]},
				{"role":"assistant","content":[{"type":"output_text","text":"hi"}]},
				{"role":"user","content":[{"type":"input_text","text":"help me"}]}
			],
			"tools":[{"type":"function","name":"list_files","description":"List files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]
		}`),
		Format: sdktranslator.FormatOpenAIResponse,
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

	exec := NewAuggieExecutor(&config.Config{})
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", newAuggieRewriteTransport(t, targetURL))

	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"instructions":"You are terse.",
			"input":[{"role":"user","content":[{"type":"input_text","text":"help me"}]}]
		}`),
		Format: sdktranslator.FormatOpenAIResponse,
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
