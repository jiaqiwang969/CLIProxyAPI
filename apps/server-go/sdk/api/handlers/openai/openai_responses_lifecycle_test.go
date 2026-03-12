package openai

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestResponses_DeleteStoredResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	defaultStoredOpenAIResponseStore.Store(
		"resp_delete_test",
		[]byte(`{"id":"resp_delete_test","object":"response","status":"completed","background":false,"output":[]}`),
		[]byte(`[]`),
	)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.DELETE("/v1/responses/:response_id", mustResponsesRouteHandler(t, h, "DeleteResponse"))

	req := httptest.NewRequest(http.MethodDelete, "/v1/responses/resp_delete_test", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "id").String(); got != "resp_delete_test" {
		t.Fatalf("id = %q, want resp_delete_test; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "object").String(); got != "response" {
		t.Fatalf("object = %q, want response; body=%s", got, resp.Body.String())
	}
	if !gjson.GetBytes(resp.Body.Bytes(), "deleted").Bool() {
		t.Fatalf("deleted = false, want true; body=%s", resp.Body.String())
	}
	if _, ok := defaultStoredOpenAIResponseStore.Load("resp_delete_test"); ok {
		t.Fatalf("stored response still present after delete")
	}
}

func TestResponses_CancelRejectsNonBackgroundStoredResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	defaultStoredOpenAIResponseStore.Store(
		"resp_cancel_test",
		[]byte(`{"id":"resp_cancel_test","object":"response","status":"completed","background":false,"output":[]}`),
		[]byte(`[]`),
	)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses/:response_id/cancel", mustResponsesRouteHandler(t, h, "CancelResponse"))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/resp_cancel_test/cancel", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "background", "invalid_value", "background=true")
}

func TestResponses_InputTokensReturnsOfficialObject(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.countPayload = []byte(`{"input_tokens":17}`)

	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses/input_tokens", mustResponsesRouteHandler(t, h, "InputTokens"))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/input_tokens", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.countTokensCalls != 1 {
		t.Fatalf("count tokens calls = %d, want 1", executor.countTokensCalls)
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "object").String(); got != "response.input_tokens" {
		t.Fatalf("object = %q, want response.input_tokens; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "input_tokens").Int(); got != 17 {
		t.Fatalf("input_tokens = %d, want 17; body=%s", got, resp.Body.String())
	}
}

func TestResponses_InputTokensNormalizesNestedUsageShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.countPayload = []byte(`{"response":{"usage":{"input_tokens":23,"output_tokens":0,"total_tokens":23}}}`)

	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses/input_tokens", mustResponsesRouteHandler(t, h, "InputTokens"))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/input_tokens", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "object").String(); got != "response.input_tokens" {
		t.Fatalf("object = %q, want response.input_tokens; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "input_tokens").Int(); got != 23 {
		t.Fatalf("input_tokens = %d, want 23; body=%s", got, resp.Body.String())
	}
}

func TestResponses_InputTokensNormalizesOpenAIUsageShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.countPayload = []byte(`{"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}}`)

	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses/input_tokens", mustResponsesRouteHandler(t, h, "InputTokens"))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/input_tokens", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "object").String(); got != "response.input_tokens" {
		t.Fatalf("object = %q, want response.input_tokens; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "input_tokens").Int(); got != 3 {
		t.Fatalf("input_tokens = %d, want 3; body=%s", got, resp.Body.String())
	}
}

func mustResponsesRouteHandler(t *testing.T, h *OpenAIResponsesAPIHandler, name string) gin.HandlerFunc {
	t.Helper()

	method := reflect.ValueOf(h).MethodByName(name)
	if !method.IsValid() {
		t.Fatalf("OpenAIResponsesAPIHandler is missing method %s", name)
	}
	handler, ok := method.Interface().(func(*gin.Context))
	if !ok {
		t.Fatalf("OpenAIResponsesAPIHandler.%s has unexpected signature", name)
	}
	return handler
}
