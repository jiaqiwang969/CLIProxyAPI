package claude

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestClaudeMessages_RejectsGPTModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const clientID = "claude-surface-gpt"
	reg := registry.GetGlobalRegistry()
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})
	reg.RegisterClient(clientID, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5.4", Object: "model", OwnedBy: "auggie", Type: "auggie", Version: "gpt-5-4"},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewClaudeCodeAPIHandler(base)
	router := gin.New()
	router.POST("/v1/messages", h.ClaudeMessages)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.4","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"type":"error"`) {
		t.Fatalf("expected Anthropic-style error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"invalid_request_error"`) {
		t.Fatalf("expected invalid_request_error body, got %s", resp.Body.String())
	}
}

func TestClaudeMessages_RejectsUnknownModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewClaudeCodeAPIHandler(base)
	router := gin.New()
	router.POST("/v1/messages", h.ClaudeMessages)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"totally-unknown-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"type":"error"`) {
		t.Fatalf("expected Anthropic-style error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"invalid_request_error"`) {
		t.Fatalf("expected invalid_request_error body, got %s", resp.Body.String())
	}
}

func TestClaudeCountTokens_RejectsGPTModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const clientID = "claude-count-tokens-gpt"
	reg := registry.GetGlobalRegistry()
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})
	reg.RegisterClient(clientID, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5.4", Object: "model", OwnedBy: "auggie", Type: "auggie", Version: "gpt-5-4"},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewClaudeCodeAPIHandler(base)
	router := gin.New()
	router.POST("/v1/messages/count_tokens", h.ClaudeCountTokens)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"type":"error"`) {
		t.Fatalf("expected Anthropic-style error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"invalid_request_error"`) {
		t.Fatalf("expected invalid_request_error body, got %s", resp.Body.String())
	}
}

func TestClaudeCountTokens_RejectsUnknownModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewClaudeCodeAPIHandler(base)
	router := gin.New()
	router.POST("/v1/messages/count_tokens", h.ClaudeCountTokens)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"totally-unknown-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"type":"error"`) {
		t.Fatalf("expected Anthropic-style error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"invalid_request_error"`) {
		t.Fatalf("expected invalid_request_error body, got %s", resp.Body.String())
	}
}
