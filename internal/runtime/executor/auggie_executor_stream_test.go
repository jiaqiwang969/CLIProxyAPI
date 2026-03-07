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

func executeAuggieStreamForTest(t *testing.T, ctx context.Context, auth *cliproxyauth.Auth, targetURL string) ([]string, error) {
	t.Helper()

	return executeAuggieStreamForModelTest(t, ctx, auth, targetURL, "gpt-5.4")
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
