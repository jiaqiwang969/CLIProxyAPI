package openai

import (
	"context"
	"errors"
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
)

type surfaceCaptureExecutor struct {
	executeCalls int
	lastModel    string
	lastStream   bool
	payload      []byte
}

func (e *surfaceCaptureExecutor) Identifier() string { return "surface-test-provider" }

func (e *surfaceCaptureExecutor) Execute(_ context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.executeCalls++
	e.lastModel = req.Model
	e.lastStream = opts.Stream
	if len(e.payload) == 0 {
		e.payload = []byte(`{"ok":true}`)
	}
	return coreexecutor.Response{Payload: e.payload}, nil
}

func (e *surfaceCaptureExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, errors.New("not implemented")
}

func (e *surfaceCaptureExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *surfaceCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not implemented")
}

func (e *surfaceCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
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
	if !strings.Contains(resp.Body.String(), `"type":"invalid_request_error"`) {
		t.Fatalf("expected OpenAI invalid_request_error body, got %s", resp.Body.String())
	}
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
	if !strings.Contains(resp.Body.String(), `"type":"invalid_request_error"`) {
		t.Fatalf("expected OpenAI invalid_request_error body, got %s", resp.Body.String())
	}
}

func newOpenAISurfaceTestHarness(t *testing.T) (*surfaceCaptureExecutor, *coreauth.Manager, *coreauth.Auth) {
	t.Helper()

	executor := &surfaceCaptureExecutor{}
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
