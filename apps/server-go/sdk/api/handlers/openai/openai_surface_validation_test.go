package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

type surfaceCaptureExecutor struct {
	provider           string
	executeCalls       int
	streamExecuteCalls int
	countTokensCalls   int
	lastModel          string
	lastCountModel     string
	lastStream         bool
	lastPayload        []byte
	lastCountPayload   []byte
	payload            []byte
	streamPayload      []byte
	countPayload       []byte
	countErr           error
	executeStarted     chan struct{}
	executeRelease     chan struct{}
	executeFinished    chan struct{}
	waitForCancel      bool
}

func (e *surfaceCaptureExecutor) Identifier() string {
	if strings.TrimSpace(e.provider) != "" {
		return e.provider
	}
	return "surface-test-provider"
}

func (e *surfaceCaptureExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.executeCalls++
	e.lastModel = req.Model
	e.lastStream = opts.Stream
	e.lastPayload = append(e.lastPayload[:0], req.Payload...)
	signalSurfaceExecutorChan(e.executeStarted)
	defer signalSurfaceExecutorChan(e.executeFinished)
	if e.waitForCancel {
		<-ctx.Done()
		return coreexecutor.Response{}, ctx.Err()
	}
	if e.executeRelease != nil {
		<-e.executeRelease
	}
	if len(e.payload) == 0 {
		e.payload = []byte(`{"ok":true}`)
	}
	return coreexecutor.Response{Payload: e.payload}, nil
}

func (e *surfaceCaptureExecutor) ExecuteStream(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.streamExecuteCalls++
	e.lastModel = req.Model
	e.lastStream = opts.Stream
	e.lastPayload = append(e.lastPayload[:0], req.Payload...)

	payload := e.streamPayload
	if len(payload) == 0 {
		payload = []byte(`{"type":"response.completed","response":{"id":"resp_test_stream","object":"response","output":[]}}`)
	}

	ch := make(chan coreexecutor.StreamChunk, 1)
	ch <- coreexecutor.StreamChunk{Payload: payload}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *surfaceCaptureExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *surfaceCaptureExecutor) CountTokens(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	e.countTokensCalls++
	e.lastCountModel = req.Model
	e.lastCountPayload = append(e.lastCountPayload[:0], req.Payload...)
	if e.countErr != nil {
		return coreexecutor.Response{}, e.countErr
	}
	if len(e.countPayload) == 0 {
		return coreexecutor.Response{}, errors.New("not implemented")
	}
	return coreexecutor.Response{Payload: e.countPayload}, nil
}

func (e *surfaceCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func signalSurfaceExecutorChan(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func assertSurfaceOpenAIErrorBody(t *testing.T, body string, param, code, wantMessageContains string) {
	t.Helper()

	if !gjson.Valid(body) {
		t.Fatalf("response body is not valid JSON: %q", body)
	}
	if got := gjson.Get(body, "error.type").String(); got != "invalid_request_error" {
		t.Fatalf("error.type = %q, want invalid_request_error; body=%s", got, body)
	}
	if got := gjson.Get(body, "error.param").String(); got != param {
		t.Fatalf("error.param = %q, want %q; body=%s", got, param, body)
	}
	if got := gjson.Get(body, "error.code").String(); got != code {
		t.Fatalf("error.code = %q, want %q; body=%s", got, code, body)
	}
	if got := gjson.Get(body, "error.message").String(); !strings.Contains(got, wantMessageContains) {
		t.Fatalf("error.message = %q, want mention of %q; body=%s", got, wantMessageContains, body)
	}
}

func TestChatCompletions_AllowsClaudeModelIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if executor.lastModel != "claude-opus-4-6" {
		t.Fatalf("model = %q, want %q", executor.lastModel, "claude-opus-4-6")
	}
}

func TestChatCompletions_RejectsMissingModelBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "missing_required_parameter", "model")
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
}

func TestCompletions_AllowsClaudeModelIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/completions", h.Completions)

	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"claude-opus-4-6","prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if executor.lastModel != "claude-opus-4-6" {
		t.Fatalf("model = %q, want %q", executor.lastModel, "claude-opus-4-6")
	}
}

func TestCompletions_RejectsMissingModelBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/completions", h.Completions)

	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "missing_required_parameter", "model")
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
}

func TestResponses_AllowsClaudeModelIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if executor.lastModel != "claude-opus-4-6" {
		t.Fatalf("model = %q, want %q", executor.lastModel, "claude-opus-4-6")
	}
}

func TestResponses_RejectsMissingModelBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "missing_required_parameter", "model")
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
}

func TestResponses_RejectsFunctionCallOutputMissingCallIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":[{"type":"function_call_output","output":"{\"ok\":true}"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].call_id", "missing_required_parameter", "input[0].call_id")
}

func TestResponses_AllowsBridgedCustomToolInputBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_RejectsCustomToolCallMissingInputBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].input", "missing_required_parameter", "input[0].input")
}

func TestResponses_AllowsCustomToolCallOutputArrayBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_text","text":"pwd"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_RejectsNonTextToolOutputArrayBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name          string
		inputItemJSON string
		wantParam     string
	}{
		{
			name:          "function_call_output input_image",
			inputItemJSON: `{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}`,
			wantParam:     "input[0].output[0].type",
		},
		{
			name:          "custom_tool_call_output input_image",
			inputItemJSON: `{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}`,
			wantParam:     "input[0].output[0].type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"gpt-5-4","input":[`+tc.inputItemJSON+`]}`),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
			if !strings.Contains(resp.Body.String(), "input_image") {
				t.Fatalf("expected input_image guidance, got %s", resp.Body.String())
			}
		})
	}
}

func TestResponses_RejectsCustomToolGrammarBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run pwd"}]}],"tools":[{"type":"custom","name":"bash","format":{"type":"grammar","syntax":"regex","definition":".*"}}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "tools[0].format.type", "invalid_value", "tools[0].format.type")
	if !strings.Contains(resp.Body.String(), "grammar") {
		t.Fatalf("expected grammar guidance, got %s", resp.Body.String())
	}
}

func TestResponses_RejectsDeferredCustomToolLoadingBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run pwd"}]}],"tools":[{"type":"custom","name":"bash","defer_loading":true}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "tools[0].defer_loading", "invalid_value", "tools[0].defer_loading")
	if !strings.Contains(resp.Body.String(), "defer_loading") {
		t.Fatalf("expected defer_loading guidance, got %s", resp.Body.String())
	}
}

func TestResponses_RejectsItemReferenceInputBeforeExecutionForAuggieWithGuidance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"item_reference","id":"rs_native_1"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if !strings.Contains(resp.Body.String(), `"type":"invalid_request_error"`) {
		t.Fatalf("expected OpenAI invalid_request_error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `item_reference`) {
		t.Fatalf("expected item_reference validation error, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `previous_response_id`) {
		t.Fatalf("expected previous_response_id guidance, got %s", resp.Body.String())
	}
}

func TestResponses_RejectsReasoningInputBeforeExecutionForAuggieWithGuidance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"reasoning","id":"rs_native_1","summary":[{"type":"summary_text","text":"prior reasoning"}],"encrypted_content":"enc:reasoning:1"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if !strings.Contains(resp.Body.String(), `"type":"invalid_request_error"`) {
		t.Fatalf("expected OpenAI invalid_request_error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `reasoning`) {
		t.Fatalf("expected reasoning validation error, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `previous_response_id`) {
		t.Fatalf("expected previous_response_id guidance, got %s", resp.Body.String())
	}
}

func TestResponses_AllowsUnsupportedResponsesInputItemToReachExecutionForCodexNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-2",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "codex",
		Version: "gpt-5-2",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-2","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_AllowsScopedNativeInputItemsForCodexDespiteMixedProviderModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarnessWithProvider(t, "codex")
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "codex",
		Version: "gpt-5-4",
	})
	registerSurfaceModel(t, "surface-mixed-auggie-"+t.Name(), "auggie", &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("accessScopeProvider", "codex")
		c.Set("accessScopeAuthID", auth.ID)
		c.Next()
	})
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_AllowsNativeInputItemsForOpenAICompatibleNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarnessWithProvider(t, "openai-compatibility")
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "openai",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"item_reference","id":"rs_native_1"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_RejectsUnsupportedMessageContentTypeBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].content[0].type", "invalid_value", "input_image")
}

func TestResponses_AllowsInputFileMessageContentToReachExecutionForClaudeResponsesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-sonnet-4-5",
		Object:  "model",
		OwnedBy: "anthropic",
		Type:    "claude",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-sonnet-4-5","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-1","filename":"notes.txt"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_AllowsInputFileMessageContentForOpenAICompatibleNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarnessWithProvider(t, "openai-compatibility")
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-1",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "openai",
		Version: "gpt-5-1",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-1","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-1","filename":"notes.txt"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_AllowsStoreTrueAndExecutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","store":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
}

func TestResponses_RejectsNonBooleanStoreBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","store":1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "store", "invalid_type", "store")
}

func TestResponses_AllowsSupportedIncludeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","include":["reasoning.encrypted_content","message.output_text.logprobs"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "include.0").String(); got != "reasoning.encrypted_content" {
		t.Fatalf("forwarded include[0] = %q, want reasoning.encrypted_content; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "include.1").String(); got != "message.output_text.logprobs" {
		t.Fatalf("forwarded include[1] = %q, want message.output_text.logprobs; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_AllowsAdditionalDocumentedIncludeValuesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","include":["web_search_call.results","web_search_call.action.sources","code_interpreter_call.outputs"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "include.0").String(); got != "web_search_call.results" {
		t.Fatalf("forwarded include[0] = %q, want web_search_call.results; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "include.1").String(); got != "web_search_call.action.sources" {
		t.Fatalf("forwarded include[1] = %q, want web_search_call.action.sources; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "include.2").String(); got != "code_interpreter_call.outputs" {
		t.Fatalf("forwarded include[2] = %q, want code_interpreter_call.outputs; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_RejectsUnsupportedIncludeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","include":["unsupported.include"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "include[0]", "invalid_value", "unsupported.include")
}

func TestResponses_RejectsItemReferenceIncludeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","include":["item_reference.content"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if !strings.Contains(resp.Body.String(), "item_reference.content") {
		t.Fatalf("expected unsupported include validation error, got %s", resp.Body.String())
	}
}

func TestResponses_RejectsBackgroundTrueWhenStoreFalseBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","background":true,"store":false}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "background", "invalid_value", "store")
}

func TestResponses_RejectsBackgroundTrueForStreamingBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","background":true,"stream":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "background", "invalid_value", "stream")
}

func TestResponses_RejectsNonBooleanBackgroundBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","background":"true"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "background", "invalid_type", "background")
}

func TestResponses_RejectsMalformedSharedRequestFeaturesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name         string
		requestBody  string
		wantParam    string
		wantCode     string
		wantContains string
	}{
		{
			name:         "non-string truncation",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","truncation":123}`,
			wantParam:    "truncation",
			wantCode:     "invalid_type",
			wantContains: "truncation",
		},
		{
			name:         "unsupported truncation value",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","truncation":"keep_last"}`,
			wantParam:    "truncation",
			wantCode:     "invalid_value",
			wantContains: "truncation",
		},
		{
			name:         "prompt must be object",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","prompt":"pmpt_test"}`,
			wantParam:    "prompt",
			wantCode:     "invalid_type",
			wantContains: "prompt",
		},
		{
			name:         "prompt id required",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","prompt":{}}`,
			wantParam:    "prompt.id",
			wantCode:     "missing_required_parameter",
			wantContains: "prompt.id",
		},
		{
			name:         "context management must be array",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","context_management":{}}`,
			wantParam:    "context_management",
			wantCode:     "invalid_type",
			wantContains: "context_management",
		},
		{
			name:         "context management compact threshold required",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","context_management":[{"type":"compaction"}]}`,
			wantParam:    "context_management[0].compact_threshold",
			wantCode:     "missing_required_parameter",
			wantContains: "context_management[0].compact_threshold",
		},
		{
			name:         "stream options must be object",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","stream_options":1}`,
			wantParam:    "stream_options",
			wantCode:     "invalid_type",
			wantContains: "stream_options",
		},
		{
			name:         "stream options include obfuscation type",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","stream":true,"stream_options":{"include_obfuscation":"yes"}}`,
			wantParam:    "stream_options.include_obfuscation",
			wantCode:     "invalid_type",
			wantContains: "stream_options.include_obfuscation",
		},
		{
			name:         "stream options requires stream",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","stream_options":{"include_obfuscation":true}}`,
			wantParam:    "stream_options",
			wantCode:     "invalid_value",
			wantContains: "stream_options",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "claude-opus-4-6",
				Object:  "model",
				OwnedBy: "antigravity",
				Type:    "antigravity",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantContains)
		})
	}
}

func TestResponses_AllowsTruncationAutoToReachExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","truncation":"auto"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "truncation").String(); got != "auto" {
		t.Fatalf("truncation = %q, want auto; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_RejectsNonBooleanParallelToolCallsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","parallel_tool_calls":"false"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "parallel_tool_calls", "invalid_type", "parallel_tool_calls")
}

func TestResponses_RejectsNonIntegerMaxToolCallsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","max_tool_calls":1.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "max_tool_calls", "invalid_type", "max_tool_calls")
}

func TestResponses_RejectsNonIntegerMaxOutputTokensBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","max_output_tokens":64.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "max_output_tokens", "invalid_type", "max_output_tokens")
}

func TestResponses_RejectsNonIntegerTopLogprobsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","top_logprobs":1.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "top_logprobs", "invalid_type", "top_logprobs")
}

func TestResponses_RejectsOutOfRangeTopLogprobsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","top_logprobs":21}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "top_logprobs", "invalid_value", "top_logprobs")
}

func TestResponses_RejectsNonNumericTemperatureBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","temperature":"0.7"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "temperature", "invalid_type", "temperature")
}

func TestResponses_RejectsOutOfRangeTemperatureBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","temperature":2.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "temperature", "invalid_value", "temperature")
}

func TestResponses_RejectsNonNumericTopPBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","top_p":"0.9"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "top_p", "invalid_type", "top_p")
}

func TestResponses_RejectsOutOfRangeTopPBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","top_p":1.1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "top_p", "invalid_value", "top_p")
}

func TestResponses_RejectsNonStringServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","service_tier":1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "service_tier", "invalid_type", "service_tier")
}

func TestResponses_RejectsUnsupportedServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","service_tier":"turbo"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "service_tier", "invalid_value", "service_tier")
}

func TestResponses_RejectsInvalidPromptCacheAndSafetyControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name:        "prompt_cache_key",
			requestBody: `{"model":"gpt-5-4","input":"hello","prompt_cache_key":1}`,
			wantParam:   "prompt_cache_key",
			wantCode:    "invalid_type",
		},
		{
			name:        "prompt_cache_retention",
			requestBody: `{"model":"gpt-5-4","input":"hello","prompt_cache_retention":"7d"}`,
			wantParam:   "prompt_cache_retention",
			wantCode:    "invalid_value",
		},
		{
			name:        "safety_identifier",
			requestBody: `{"model":"gpt-5-4","input":"hello","safety_identifier":1}`,
			wantParam:   "safety_identifier",
			wantCode:    "invalid_type",
		},
		{
			name:        "user",
			requestBody: `{"model":"gpt-5-4","input":"hello","user":1}`,
			wantParam:   "user",
			wantCode:    "invalid_type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestResponses_RejectsUnsupportedTextVerbosityBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","text":{"verbosity":"loud"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "text.verbosity", "invalid_value", "text.verbosity")
}

func TestResponses_RejectsUnsupportedReasoningEffortBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","reasoning":{"effort":"banana"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "reasoning.effort", "invalid_value", "reasoning.effort")
}

func TestResponses_RejectsNonPreservedReasoningEffortBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, effort := range []string{"none", "minimal", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(fmt.Sprintf(`{"model":"gpt-5-4","input":"hello","reasoning":{"effort":%q}}`, effort)),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "reasoning.effort", "invalid_value", "reasoning.effort")
		})
	}
}

func TestResponses_RejectsReasoningSummaryBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","reasoning":{"summary":"detailed"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "reasoning.summary", "invalid_value", "reasoning.summary")
}

func TestResponses_RejectsNonPreservedAuggieSamplingControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "max_tool_calls",
			requestBody: `{"model":"gpt-5-4","input":"hello","max_tool_calls":1}`,
			wantParam:   "max_tool_calls",
		},
		{
			name:        "max_output_tokens",
			requestBody: `{"model":"gpt-5-4","input":"hello","max_output_tokens":64}`,
			wantParam:   "max_output_tokens",
		},
		{
			name:        "top_logprobs",
			requestBody: `{"model":"gpt-5-4","input":"hello","top_logprobs":5}`,
			wantParam:   "top_logprobs",
		},
		{
			name:        "temperature",
			requestBody: `{"model":"gpt-5-4","input":"hello","temperature":0.7}`,
			wantParam:   "temperature",
		},
		{
			name:        "top_p",
			requestBody: `{"model":"gpt-5-4","input":"hello","top_p":0.9}`,
			wantParam:   "top_p",
		},
		{
			name:        "text.verbosity",
			requestBody: `{"model":"gpt-5-4","input":"hello","text":{"verbosity":"high"}}`,
			wantParam:   "text.verbosity",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestResponses_RejectsNonPreservedAuggieServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","service_tier":"priority"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "service_tier", "invalid_value", "service_tier")
}

func TestResponses_RejectsNonPreservedAuggieTruncationAutoBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","truncation":"auto"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "truncation", "invalid_value", "truncation")
}

func TestResponses_RejectsNonPreservedAuggiePromptTemplateBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","prompt":{"id":"pmpt_test","version":"1","variables":{"name":"world"}}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "prompt", "invalid_value", "prompt")
}

func TestResponses_RejectsNonPreservedAuggieContextManagementBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","context_management":[{"type":"compaction","compact_threshold":1000}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "context_management", "invalid_value", "context_management")
}

func TestResponses_RejectsNonPreservedAuggiePromptCacheAndSafetyControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "prompt_cache_key",
			requestBody: `{"model":"gpt-5-4","input":"hello","prompt_cache_key":"cache-key-1"}`,
			wantParam:   "prompt_cache_key",
		},
		{
			name:        "prompt_cache_retention",
			requestBody: `{"model":"gpt-5-4","input":"hello","prompt_cache_retention":"24h"}`,
			wantParam:   "prompt_cache_retention",
		},
		{
			name:        "safety_identifier",
			requestBody: `{"model":"gpt-5-4","input":"hello","safety_identifier":"safe-user-1"}`,
			wantParam:   "safety_identifier",
		},
		{
			name:        "user",
			requestBody: `{"model":"gpt-5-4","input":"hello","user":"user_123"}`,
			wantParam:   "user",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestResponses_RejectsNonPreservedAuggieStructuredOutputBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
	}{
		{
			name:        "json_object",
			requestBody: `{"model":"gpt-5-4","input":"hello","text":{"format":{"type":"json_object"}}}`,
		},
		{
			name: "json_schema",
			requestBody: `{
				"model":"gpt-5-4",
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
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "text.format.type", "invalid_value", "text.format.type")
		})
	}
}

func TestResponses_RejectsNonPreservedAuggieOutputTextLogprobsIncludeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5-4","input":"hello","include":["message.output_text.logprobs"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "include[0]", "invalid_value", "message.output_text.logprobs")
}

func TestResponses_RejectsNonPreservedAuggieExpandedIncludeValuesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []string{
		"code_interpreter_call.outputs",
		"computer_call_output.output.image_url",
		"file_search_call.results",
		"message.input_image.image_url",
	}

	for _, includeValue := range testCases {
		t.Run(includeValue, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(fmt.Sprintf(`{"model":"gpt-5-4","input":"hello","include":["%s"]}`, includeValue)),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "include[0]", "invalid_value", "include[0]")
		})
	}
}

func TestResponses_RejectsNonPreservedAuggieForcedToolChoiceFormsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name: "required",
			requestBody: `{
				"model":"gpt-5-4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use pwd to inspect the current directory."}]}],
				"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
				"tool_choice":"required"
			}`,
			wantParam: "tool_choice",
		},
		{
			name: "function selection",
			requestBody: `{
				"model":"gpt-5-4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use pwd to inspect the current directory."}]}],
				"tools":[
					{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},
					{"type":"function","name":"list_files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}
				],
				"tool_choice":{"type":"function","function":{"name":"run_shell"}}
			}`,
			wantParam: "tool_choice.type",
		},
		{
			name: "custom selection",
			requestBody: `{
				"model":"gpt-5-4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use bash to print pwd."}]}],
				"tools":[{"type":"custom","name":"bash"}],
				"tool_choice":{"type":"custom","name":"bash"}
			}`,
			wantParam: "tool_choice.type",
		},
		{
			name: "allowed tools required",
			requestBody: `{
				"model":"gpt-5-4",
				"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
				"tools":[
					{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}}}},
					{"type":"function","name":"list_files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}}}}
				],
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"get_weather"}}]}}
			}`,
			wantParam: "tool_choice.allowed_tools.mode",
		},
		{
			name: "web search selection",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Find the latest OpenAI news",
				"tools":[{"type":"web_search"}],
				"tool_choice":{"type":"web_search"}
			}`,
			wantParam: "tool_choice.type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestResponses_RejectsNonPreservedAuggieBuiltInWebSearchToolConfigBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name: "web_search_search_context_size",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Find the latest OpenAI news",
				"tools":[{"type":"web_search","search_context_size":"high"}]
			}`,
			wantParam: "tools[0].search_context_size",
		},
		{
			name: "web_search_filters",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Find the latest OpenAI news",
				"tools":[{"type":"web_search","filters":{"allowed_domains":["openai.com"]}}]
			}`,
			wantParam: "tools[0].filters",
		},
		{
			name: "web_search_preview_search_content_types",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Find the latest OpenAI images",
				"tools":[{"type":"web_search_preview","search_content_types":["image"]}]
			}`,
			wantParam: "tools[0].search_content_types",
		},
		{
			name: "web_search_user_location",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Find local weather news",
				"tools":[{"type":"web_search","user_location":{"type":"approximate","country":"US","timezone":"America/Los_Angeles"}}]
			}`,
			wantParam: "tools[0].user_location",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestResponses_RejectsDefaultStrictFunctionToolsBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
	}{
		{
			name: "strict omitted defaults true",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Use pwd to inspect the current directory.",
				"tools":[{"type":"function","name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
			}`,
		},
		{
			name: "strict true",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"Use pwd to inspect the current directory.",
				"tools":[{"type":"function","name":"run_shell","strict":true,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "tools[0].strict", "invalid_value", "tools[0].strict")
		})
	}
}

func TestResponses_AllowsMaxToolCallsThroughToExecutor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","max_tool_calls":1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "max_tool_calls").Int(); got != 1 {
		t.Fatalf("executor max_tool_calls = %d, want 1; payload=%s", got, executor.lastPayload)
	}
	if !gjson.Valid(resp.Body.String()) {
		t.Fatalf("expected JSON success response, got %s", resp.Body.String())
	}
}

func TestResponses_AllowsPromptTemplateToReachExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","prompt":{"id":"pmpt_test","version":"1","variables":{"name":"world"}}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "prompt.id").String(); got != "pmpt_test" {
		t.Fatalf("prompt.id = %q, want pmpt_test; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "prompt.version").String(); got != "1" {
		t.Fatalf("prompt.version = %q, want 1; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "prompt.variables.name").String(); got != "world" {
		t.Fatalf("prompt.variables.name = %q, want world; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_AllowsContextManagementCompactionToReachExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","context_management":[{"type":"compaction","compact_threshold":1000}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "context_management.0.type").String(); got != "compaction" {
		t.Fatalf("context_management[0].type = %q, want compaction; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "context_management.0.compact_threshold").Int(); got != 1000 {
		t.Fatalf("context_management[0].compact_threshold = %d, want 1000; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_AllowsStreamOptionsIncludeObfuscationToReachStreamingExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","stream":true,"stream_options":{"include_obfuscation":true}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "stream_options.include_obfuscation").Bool(); !got {
		t.Fatalf("stream_options.include_obfuscation = %v, want true; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_AllowsPromptCacheRetentionToReachExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","prompt_cache_retention":"24h"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("prompt_cache_retention = %q, want 24h; payload=%s", got, executor.lastPayload)
	}
}

func TestResponses_RejectsFunctionCallOutputMissingOutputBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":[{"type":"function_call_output","call_id":"call-1"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].output", "missing_required_parameter", "input[0].output")
}

func TestResponses_RejectsFunctionCallMissingRequiredFieldsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name          string
		inputItemJSON string
		wantField     string
	}{
		{
			name:          "missing call_id",
			inputItemJSON: `{"type":"function_call","name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "call_id",
		},
		{
			name:          "missing name",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "name",
		},
		{
			name:          "missing arguments",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","name":"get_weather"}`,
			wantField:     "arguments",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "claude-opus-4-6",
				Object:  "model",
				OwnedBy: "antigravity",
				Type:    "antigravity",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"claude-opus-4-6","input":[`+tc.inputItemJSON+`]}`),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(
				t,
				resp.Body.String(),
				"input[0]."+tc.wantField,
				"missing_required_parameter",
				"input[0]."+tc.wantField,
			)
		})
	}
}

func TestResponses_RejectsFunctionCallWithInvalidFieldTypesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name          string
		inputItemJSON string
		wantField     string
	}{
		{
			name:          "call_id not string",
			inputItemJSON: `{"type":"function_call","call_id":123,"name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "call_id",
		},
		{
			name:          "name not string",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","name":123,"arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "name",
		},
		{
			name:          "arguments not string",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":{"location":"Shanghai"}}`,
			wantField:     "arguments",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "claude-opus-4-6",
				Object:  "model",
				OwnedBy: "antigravity",
				Type:    "antigravity",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"claude-opus-4-6","input":[`+tc.inputItemJSON+`]}`),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(
				t,
				resp.Body.String(),
				"input[0]."+tc.wantField,
				"invalid_type",
				"input[0]."+tc.wantField,
			)
		})
	}
}

func TestResponses_RejectsFunctionCallOutputWithInvalidFieldTypesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name          string
		inputItemJSON string
		wantField     string
	}{
		{
			name:          "call_id not string",
			inputItemJSON: `{"type":"function_call_output","call_id":123,"output":"{\"ok\":true}"}`,
			wantField:     "call_id",
		},
		{
			name:          "output not string or array",
			inputItemJSON: `{"type":"function_call_output","call_id":"call-1","output":{"ok":true}}`,
			wantField:     "output",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "claude-opus-4-6",
				Object:  "model",
				OwnedBy: "antigravity",
				Type:    "antigravity",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"claude-opus-4-6","input":[`+tc.inputItemJSON+`]}`),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(
				t,
				resp.Body.String(),
				"input[0]."+tc.wantField,
				"invalid_type",
				"input[0]."+tc.wantField,
			)
		})
	}
}

func TestChatCompletions_RejectsUnknownModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"totally-unknown-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "invalid_value", "totally-unknown-model")
}

func TestCompletions_RejectsUnknownModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/completions", h.Completions)

	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"totally-unknown-model","prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "invalid_value", "totally-unknown-model")
}

func TestChatCompletions_RejectsInternalVariantModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gemini-claude-sonnet-4-5",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gemini-claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "invalid_value", "gemini-claude-sonnet-4-5")
}

func TestCompletions_RejectsInternalVariantModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gemini-claude-sonnet-4-5",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/completions", h.Completions)

	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"gemini-claude-sonnet-4-5","prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "invalid_value", "gemini-claude-sonnet-4-5")
}

func TestResponses_RejectsUnknownModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"totally-unknown-model","input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "model", "invalid_value", "totally-unknown-model")
}

func TestChatCompletions_RejectsStoreTrueBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"store":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "store", "invalid_value", "store")
}

func TestChatCompletions_RejectsNonBooleanStoreBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"store":1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "store", "invalid_type", "store")
}

func TestResponses_RejectsConversationWithPreviousResponseIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","conversation":"conv_123","previous_response_id":"resp_123"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "conversation", "invalid_value", "previous_response_id")
}

func TestChatCompletions_RejectsIncludeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"include":["reasoning.encrypted_content"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "include", "unsupported_parameter", "include")
}

func TestChatCompletions_RejectsConversationBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"conversation":"conv_123"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "conversation", "invalid_value", "conversation")
}

func TestResponses_RejectsUnsupportedAuggieToolChoiceValuesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name: "bogus string",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":"bogus"
			}`,
			wantParam: "tool_choice",
			wantCode:  "invalid_value",
		},
		{
			name: "unknown object type",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":{"type":"mystery"}
			}`,
			wantParam: "tool_choice.type",
			wantCode:  "invalid_value",
		},
		{
			name: "invalid type",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":1
			}`,
			wantParam: "tool_choice",
			wantCode:  "invalid_type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestResponses_RejectsInvalidAuggieAllowedToolsShapeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name: "invalid nested mode",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"bogus","tools":[{"type":"function","name":"run_shell"}]}}
			}`,
			wantParam: "tool_choice.allowed_tools.mode",
			wantCode:  "invalid_value",
		},
		{
			name: "missing flat tools",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":{"type":"allowed_tools","mode":"auto"}
			}`,
			wantParam: "tool_choice.tools",
			wantCode:  "invalid_value",
		},
		{
			name: "invalid nested tools type",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":1}}
			}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_type",
		},
		{
			name: "unsupported nested tool selection",
			requestBody: `{
				"model":"gpt-5-4",
				"input":"hello",
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"mystery"}]}}
			}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.POST("/v1/responses", h.Responses)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsNonPreservedAuggieForcedToolChoiceFormsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name: "required",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
				"tools":[{"type":"function","function":{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}],
				"tool_choice":"required"
			}`,
			wantParam: "tool_choice",
		},
		{
			name: "function selection",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
				"tools":[
					{"type":"function","function":{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}},
					{"type":"function","function":{"name":"list_files","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}
				],
				"tool_choice":{"type":"function","function":{"name":"run_shell"}}
			}`,
			wantParam: "tool_choice.type",
		},
		{
			name: "allowed tools required",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tools":[
					{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}},
					{"type":"function","function":{"name":"list_files","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}
				],
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"get_weather"}}]}}
			}`,
			wantParam: "tool_choice.allowed_tools.mode",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_AllowsAuggieLegacyFunctionAliasesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{
			"model":"gpt-5-4",
			"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
			"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
			"function_call":"auto"
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "functions.0.name").String(); got != "run_shell" {
		t.Fatalf("forwarded functions[0].name = %q, want run_shell; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "function_call").String(); got != "auto" {
		t.Fatalf("forwarded function_call = %q, want auto; payload=%s", got, executor.lastPayload)
	}
}

func TestChatCompletions_RejectsAuggieLegacyFunctionFieldConflictsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name: "tools and functions",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tools":[{"type":"function","function":{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}],
				"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]
			}`,
			wantParam: "functions",
		},
		{
			name: "tool_choice and function_call",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
				"tool_choice":"auto",
				"function_call":"auto"
			}`,
			wantParam: "function_call",
		},
		{
			name: "forced function_call object",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"Use pwd to inspect the current directory."}],
				"functions":[{"name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
				"function_call":{"name":"run_shell"}
			}`,
			wantParam: "function_call",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsAuggieStructuredOutputBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name: "json_object",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"Return the current working directory as JSON."}],
				"response_format":{"type":"json_object"}
			}`,
			wantParam: "response_format.type",
		},
		{
			name: "json_schema",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"Return the current working directory as JSON."}],
				"response_format":{
					"type":"json_schema",
					"json_schema":{
						"name":"pwd_result",
						"schema":{
							"type":"object",
							"properties":{"cwd":{"type":"string"}},
							"required":["cwd"]
						}
					}
				}
			}`,
			wantParam: "response_format.type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsResponsesFormatAuggieStructuredOutputBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{
			"model":"gpt-5-4",
			"input":"Return the current working directory as JSON.",
			"text":{
				"format":{
					"type":"json_schema",
					"name":"pwd_result",
					"schema":{
						"type":"object",
						"properties":{"cwd":{"type":"string"}},
						"required":["cwd"]
					}
				}
			}
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "response_format.type", "invalid_value", "response_format.type")
}

func TestChatCompletions_RejectsAuggieNonPreservedReasoningEffortBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []string{"none", "minimal", "xhigh"}

	for _, effort := range testCases {
		t.Run(effort, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(fmt.Sprintf(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"reasoning_effort":%q}`, effort)),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "reasoning_effort", "invalid_value", "reasoning_effort")
		})
	}
}

func TestChatCompletions_RejectsNonPreservedAuggieSamplingControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "max_tokens",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`,
			wantParam:   "max_tokens",
		},
		{
			name:        "max_completion_tokens",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":64}`,
			wantParam:   "max_completion_tokens",
		},
		{
			name:        "top_logprobs",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"top_logprobs":5}`,
			wantParam:   "top_logprobs",
		},
		{
			name:        "temperature",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"temperature":0.7}`,
			wantParam:   "temperature",
		},
		{
			name:        "top_p",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"top_p":0.9}`,
			wantParam:   "top_p",
		},
		{
			name:        "frequency_penalty",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"frequency_penalty":1.2}`,
			wantParam:   "frequency_penalty",
		},
		{
			name:        "presence_penalty",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"presence_penalty":1.1}`,
			wantParam:   "presence_penalty",
		},
		{
			name:        "logprobs",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"logprobs":true}`,
			wantParam:   "logprobs",
		},
		{
			name:        "logit_bias",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"logit_bias":{"42":10}}`,
			wantParam:   "logit_bias",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_AllowsAuggieTextOnlyModalitiesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"modalities":["text"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "modalities.0").String(); got != "text" {
		t.Fatalf("forwarded modalities[0] = %q, want text; payload=%s", got, executor.lastPayload)
	}
}

func TestChatCompletions_AllowsAuggieMetadataBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"metadata":{"trace_id":"trace-auggie-chat-1"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "metadata.trace_id").String(); got != "trace-auggie-chat-1" {
		t.Fatalf("forwarded metadata.trace_id = %q, want trace-auggie-chat-1; payload=%s", got, executor.lastPayload)
	}
}

func TestChatCompletions_RejectsNonPreservedAuggieVerbosityBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"verbosity":"high"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "verbosity", "invalid_value", "verbosity")
}

func TestChatCompletions_RejectsInvalidVerbosityBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"verbosity":1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "verbosity", "invalid_type", "verbosity")
}

func TestChatCompletions_RejectsNonPreservedAuggieWebSearchOptionsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"web_search_options":{}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "web_search_options", "invalid_value", "web_search_options")
}

func TestChatCompletions_RejectsInvalidWebSearchOptionsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"web_search_options":1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "web_search_options", "invalid_type", "web_search_options")
}

func TestChatCompletions_RejectsUnsupportedAuggieToolTypesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name: "custom",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tools":[{"type":"custom","custom":{"name":"bash"}}]
			}`,
			wantParam: "tools[0].type",
		},
		{
			name: "web_search",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tools":[{"type":"web_search"}]
			}`,
			wantParam: "tools[0].type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsUnsupportedAuggieToolChoiceValuesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name: "bogus string",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":"bogus"
			}`,
			wantParam: "tool_choice",
			wantCode:  "invalid_value",
		},
		{
			name: "custom selection",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":{"type":"custom","custom":{"name":"bash"}}
			}`,
			wantParam: "tool_choice.type",
			wantCode:  "invalid_value",
		},
		{
			name: "invalid type",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":1
			}`,
			wantParam: "tool_choice",
			wantCode:  "invalid_type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsInvalidAuggieAllowedToolsShapeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name: "invalid nested mode",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"bogus","tools":[{"type":"function","function":{"name":"run_shell"}}]}}
			}`,
			wantParam: "tool_choice.allowed_tools.mode",
			wantCode:  "invalid_value",
		},
		{
			name: "missing flat tools",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":{"type":"allowed_tools","mode":"auto"}
			}`,
			wantParam: "tool_choice.tools",
			wantCode:  "invalid_value",
		},
		{
			name: "invalid nested tools type",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":1}}
			}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_type",
		},
		{
			name: "unsupported nested tool selection",
			requestBody: `{
				"model":"gpt-5-4",
				"messages":[{"role":"user","content":"hello"}],
				"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"custom","name":"bash"}]}}
			}`,
			wantParam: "tool_choice.allowed_tools.tools",
			wantCode:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsNonPreservedAuggieFunctionToolStrictBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{
			"model":"gpt-5-4",
			"messages":[{"role":"user","content":"hello"}],
			"tools":[{"type":"function","function":{"name":"run_shell","strict":true,"parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "tools[0].function.strict", "invalid_value", "tools[0].function.strict")
}

func TestChatCompletions_RejectsInvalidFunctionToolStrictBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{
			"model":"gpt-5-4",
			"messages":[{"role":"user","content":"hello"}],
			"tools":[{"type":"function","function":{"name":"run_shell","strict":"true","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "tools[0].function.strict", "invalid_type", "tools[0].function.strict")
}

func TestChatCompletions_RejectsNonIntegerMaxCompletionTokensBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":64.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "max_completion_tokens", "invalid_type", "max_completion_tokens")
}

func TestChatCompletions_RejectsNonNumericFrequencyPenaltyBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"frequency_penalty":"1.2"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "frequency_penalty", "invalid_type", "frequency_penalty")
}

func TestChatCompletions_RejectsOutOfRangePresencePenaltyBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"presence_penalty":2.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "presence_penalty", "invalid_value", "presence_penalty")
}

func TestChatCompletions_RejectsNonBooleanLogprobsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"logprobs":"true"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "logprobs", "invalid_type", "logprobs")
}

func TestChatCompletions_RejectsNonPreservedAuggieNStopAndSeedBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "n",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"n":2}`,
			wantParam:   "n",
		},
		{
			name:        "stop",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"stop":"END"}`,
			wantParam:   "stop",
		},
		{
			name:        "seed",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"seed":7}`,
			wantParam:   "seed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsNonIntegerNBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"n":1.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "n", "invalid_type", "n")
}

func TestChatCompletions_RejectsInvalidStopTypeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"stop":{"bad":true}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "stop", "invalid_type", "stop")
}

func TestChatCompletions_RejectsTooManyStopSequencesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"stop":["a","b","c","d","e"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "stop", "invalid_value", "stop")
}

func TestChatCompletions_RejectsNonIntegerSeedBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"seed":7.5}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "seed", "invalid_type", "seed")
}

func TestChatCompletions_RejectsNonPreservedAuggieStreamOptionsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":true}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "stream_options", "invalid_value", "stream_options")
}

func TestChatCompletions_RejectsNonBooleanStreamOptionsIncludeUsageBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":"true"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "stream_options.include_usage", "invalid_type", "stream_options.include_usage")
}

func TestChatCompletions_RejectsNonPreservedAuggieServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"service_tier":"priority"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "service_tier", "invalid_value", "service_tier")
}

func TestChatCompletions_RejectsNonPreservedAuggiePromptCacheAndUserControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "prompt_cache_key",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"prompt_cache_key":"cache-key-1"}`,
			wantParam:   "prompt_cache_key",
		},
		{
			name:        "prompt_cache_retention",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"prompt_cache_retention":"24h"}`,
			wantParam:   "prompt_cache_retention",
		},
		{
			name:        "user",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"user":"user_123"}`,
			wantParam:   "user",
		},
		{
			name:        "safety_identifier",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"safety_identifier":"safe-user-1"}`,
			wantParam:   "safety_identifier",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsNonPreservedAuggieAudioOutputControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "modalities audio",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"modalities":["audio"]}`,
			wantParam:   "modalities",
		},
		{
			name:        "audio config",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"audio":{"format":"mp3","voice":"alloy"}}`,
			wantParam:   "audio",
		},
		{
			name:        "prediction",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"prediction":{"type":"content","content":"hello"}}`,
			wantParam:   "prediction",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, "invalid_value", tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsInvalidMetadataBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	longKey := strings.Repeat("k", 65)
	longValue := strings.Repeat("v", 513)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name:        "metadata type",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"metadata":"bad"}`,
			wantParam:   "metadata",
			wantCode:    "invalid_type",
		},
		{
			name:        "metadata value type",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"metadata":{"trace_id":1}}`,
			wantParam:   "metadata.trace_id",
			wantCode:    "invalid_type",
		},
		{
			name:        "metadata key too long",
			requestBody: fmt.Sprintf(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"metadata":{"%s":"ok"}}`, longKey),
			wantParam:   "metadata." + longKey,
			wantCode:    "invalid_value",
		},
		{
			name:        "metadata value too long",
			requestBody: fmt.Sprintf(`{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"metadata":{"trace_id":"%s"}}`, longValue),
			wantParam:   "metadata.trace_id",
			wantCode:    "invalid_value",
		},
		{
			name: "metadata too many pairs",
			requestBody: `{"model":"gpt-5-4","messages":[{"role":"user","content":"hello"}],"metadata":{
				"k01":"v","k02":"v","k03":"v","k04":"v","k05":"v","k06":"v","k07":"v","k08":"v",
				"k09":"v","k10":"v","k11":"v","k12":"v","k13":"v","k14":"v","k15":"v","k16":"v","k17":"v"
			}}`,
			wantParam: "metadata",
			wantCode:  "invalid_value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantParam)
		})
	}
}

func TestChatCompletions_RejectsResponsesFormatUnsupportedInputItemBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].type", "invalid_value", "custom_tool_call")
}

func TestChatCompletions_RejectsResponsesFormatUnsupportedMessageContentTypeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-1"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].content[0].type", "invalid_value", "input_file")
}

func TestChatCompletions_RejectsResponsesFormatNonTextToolOutputArrayBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","input":[{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "input[0].output[0].type", "invalid_value", "input_image")
}

func TestChatCompletions_RejectsMalformedResponsesFormatBridgePayloadBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name         string
		requestBody  string
		wantParam    string
		wantCode     string
		wantContains string
	}{
		{
			name:         "input must be string or array",
			requestBody:  `{"model":"claude-opus-4-6","input":{"type":"message","role":"user","content":"hello"}}`,
			wantParam:    "input",
			wantCode:     "invalid_type",
			wantContains: "input",
		},
		{
			name:         "text format must be object",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","text":{"format":true}}`,
			wantParam:    "text.format",
			wantCode:     "invalid_type",
			wantContains: "text.format",
		},
		{
			name:         "text format type required",
			requestBody:  `{"model":"claude-opus-4-6","input":"hello","text":{"format":{}}}`,
			wantParam:    "text.format.type",
			wantCode:     "missing_required_parameter",
			wantContains: "text.format.type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "claude-opus-4-6",
				Object:  "model",
				OwnedBy: "antigravity",
				Type:    "antigravity",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIAPIHandler(base)
			router := gin.New()
			router.POST("/v1/chat/completions", h.ChatCompletions)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if executor.executeCalls != 0 {
				t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantContains)
		})
	}
}

func TestChatCompletions_ResponsesFormatPreservesSharedCachingAndStreamingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","stream":true,"prompt_cache_retention":"24h","stream_options":{"include_obfuscation":true}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("prompt_cache_retention = %q, want 24h; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "stream_options.include_obfuscation"); got.Type != gjson.True {
		t.Fatalf("stream_options.include_obfuscation = %s, want true; payload=%s", got.Raw, executor.lastPayload)
	}
}

func TestChatCompletions_ResponsesFormatBridgesTextFormatToResponseFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/chat/completions", h.ChatCompletions)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{
			"model":"claude-opus-4-6",
			"input":"hello",
			"text":{
				"format":{
					"type":"json_schema",
					"name":"pwd_result",
					"strict":true,
					"schema":{
						"type":"object",
						"properties":{"cwd":{"type":"string"}},
						"required":["cwd"],
						"additionalProperties":false
					}
				}
			}
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "response_format.type").String(); got != "json_schema" {
		t.Fatalf("response_format.type = %q, want json_schema; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "response_format.json_schema.name").String(); got != "pwd_result" {
		t.Fatalf("response_format.json_schema.name = %q, want pwd_result; payload=%s", got, executor.lastPayload)
	}
}

func newOpenAISurfaceTestHarness(t *testing.T) (*surfaceCaptureExecutor, *coreauth.Manager, *coreauth.Auth) {
	return newOpenAISurfaceTestHarnessWithProvider(t, "")
}

func newOpenAISurfaceTestHarnessWithProvider(t *testing.T, provider string) (*surfaceCaptureExecutor, *coreauth.Manager, *coreauth.Auth) {
	t.Helper()

	executor := &surfaceCaptureExecutor{provider: provider}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth := &coreauth.Auth{ID: "surface-auth-" + t.Name(), Provider: executor.Identifier(), Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register auth: %v", err)
	}
	return executor, manager, auth
}

func registerSurfaceModel(t *testing.T, clientID, provider string, model *registry.ModelInfo) {
	t.Helper()

	reg := registry.GetGlobalRegistry()
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})
	reg.RegisterClient(clientID, provider, []*registry.ModelInfo{model})
}
