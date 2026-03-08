package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type geminiErrorTestPayload struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func TestGeminiModels_ReturnsOnlyOfficialGeminiCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	clientIDs := []string{
		"gemini-handler-public-models-antigravity",
		"gemini-handler-public-models-auggie",
	}
	for _, clientID := range clientIDs {
		reg.UnregisterClient(clientID)
		t.Cleanup(func(id string) func() {
			return func() {
				reg.UnregisterClient(id)
			}
		}(clientID))
	}

	reg.RegisterClient(clientIDs[0], "antigravity", []*registry.ModelInfo{
		{
			ID:          "gemini-3.1-pro-preview",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro Preview",
		},
		{
			ID:          "gemini-3.1-pro-high",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro High",
		},
	})
	reg.RegisterClient(clientIDs[1], "auggie", []*registry.ModelInfo{
		{
			ID:          "gpt-5-4",
			Object:      "model",
			OwnedBy:     "auggie",
			Type:        "auggie",
			DisplayName: "GPT-5.4",
		},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.GET("/v1beta/models", h.GeminiModels)

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(payload.Models) < 2 {
		t.Fatalf("model count = %d, want at least 2; body=%s", len(payload.Models), resp.Body.String())
	}
	if !hasGeminiModelName(payload.Models, "models/gemini-3.1-pro-preview") {
		t.Fatalf("expected public model %q in response body=%s", "models/gemini-3.1-pro-preview", resp.Body.String())
	}
	if !hasGeminiModelName(payload.Models, "models/gemini-2.5-pro") {
		t.Fatalf("expected official but currently unavailable model %q in response body=%s", "models/gemini-2.5-pro", resp.Body.String())
	}
}

func TestGeminiGetHandler_ReturnsOfficialGeminiStaticFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.GET("/v1beta/models/*action", h.GeminiGetHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models/gemini-3.1-pro-preview", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		Name                       string   `json:"name"`
		Version                    string   `json:"version"`
		DisplayName                string   `json:"displayName"`
		Description                string   `json:"description"`
		InputTokenLimit            int      `json:"inputTokenLimit"`
		OutputTokenLimit           int      `json:"outputTokenLimit"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		Temperature                float64  `json:"temperature"`
		TopP                       float64  `json:"topP"`
		TopK                       int      `json:"topK"`
		MaxTemperature             float64  `json:"maxTemperature"`
		Thinking                   bool     `json:"thinking"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload.Name != "models/gemini-3.1-pro-preview" {
		t.Fatalf("name = %q, want %q", payload.Name, "models/gemini-3.1-pro-preview")
	}
	if payload.Version != "3.1-pro-preview-01-2026" {
		t.Fatalf("version = %q, want %q", payload.Version, "3.1-pro-preview-01-2026")
	}
	if payload.DisplayName != "Gemini 3.1 Pro Preview" {
		t.Fatalf("displayName = %q, want %q", payload.DisplayName, "Gemini 3.1 Pro Preview")
	}
	if payload.Description != "Gemini 3.1 Pro Preview" {
		t.Fatalf("description = %q, want %q", payload.Description, "Gemini 3.1 Pro Preview")
	}
	if payload.InputTokenLimit != 1048576 {
		t.Fatalf("inputTokenLimit = %d, want %d", payload.InputTokenLimit, 1048576)
	}
	if payload.OutputTokenLimit != 65536 {
		t.Fatalf("outputTokenLimit = %d, want %d", payload.OutputTokenLimit, 65536)
	}
	if len(payload.SupportedGenerationMethods) != 4 {
		t.Fatalf("supportedGenerationMethods len = %d, want %d", len(payload.SupportedGenerationMethods), 4)
	}
	if payload.Temperature != 1 {
		t.Fatalf("temperature = %v, want %v", payload.Temperature, 1)
	}
	if payload.TopP != 0.95 {
		t.Fatalf("topP = %v, want %v", payload.TopP, 0.95)
	}
	if payload.TopK != 64 {
		t.Fatalf("topK = %d, want %d", payload.TopK, 64)
	}
	if payload.MaxTemperature != 2 {
		t.Fatalf("maxTemperature = %v, want %v", payload.MaxTemperature, 2)
	}
	if !payload.Thinking {
		t.Fatalf("thinking = %v, want true", payload.Thinking)
	}
}

func TestGeminiGetHandler_RejectsPrivateModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	clientID := "gemini-handler-private-get-antigravity"
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})

	reg.RegisterClient(clientID, "antigravity", []*registry.ModelInfo{
		{
			ID:          "gemini-3.1-pro-high",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro High",
		},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.GET("/v1beta/models/*action", h.GeminiGetHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models/gemini-3.1-pro-high", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	var payload geminiErrorTestPayload
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload.Error.Code != http.StatusNotFound {
		t.Fatalf("error.code = %d, want %d", payload.Error.Code, http.StatusNotFound)
	}
	if payload.Error.Status != "NOT_FOUND" {
		t.Fatalf("error.status = %q, want %q", payload.Error.Status, "NOT_FOUND")
	}
	wantMessage := "Model is not found: models/gemini-3.1-pro-high for api version v1beta"
	if payload.Error.Message != wantMessage {
		t.Fatalf("error.message = %q, want %q", payload.Error.Message, wantMessage)
	}
}

func TestGeminiHandler_RejectsPrivateModelIDBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	clientID := "gemini-handler-private-post-antigravity"
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})

	reg.RegisterClient(clientID, "antigravity", []*registry.ModelInfo{
		{
			ID:          "gemini-3.1-pro-high",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro High",
		},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.POST("/v1beta/models/*action", h.GeminiHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3.1-pro-high:generateContent",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	var payload geminiErrorTestPayload
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload.Error.Code != http.StatusNotFound {
		t.Fatalf("error.code = %d, want %d", payload.Error.Code, http.StatusNotFound)
	}
	if payload.Error.Status != "NOT_FOUND" {
		t.Fatalf("error.status = %q, want %q", payload.Error.Status, "NOT_FOUND")
	}
	wantMessage := "models/gemini-3.1-pro-high is not found for API version v1beta, or is not supported for generateContent. Call ListModels to see the list of available models and their supported methods."
	if payload.Error.Message != wantMessage {
		t.Fatalf("error.message = %q, want %q", payload.Error.Message, wantMessage)
	}
}

func TestGeminiModels_FiltersToScopedAuthModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	const scopedAuthID = "gemini-models-scoped-antigravity"
	reg.UnregisterClient(scopedAuthID)
	t.Cleanup(func() {
		reg.UnregisterClient(scopedAuthID)
	})

	reg.RegisterClient(scopedAuthID, "antigravity", []*registry.ModelInfo{
		{
			ID:          "gemini-3.1-pro-preview",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro Preview",
		},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("accessScopeAuthID", scopedAuthID)
		c.Next()
	})
	router.GET("/v1beta/models", h.GeminiModels)

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !hasGeminiModelName(payload.Models, "models/gemini-3.1-pro-preview") {
		t.Fatalf("expected scoped model %q in response body=%s", "models/gemini-3.1-pro-preview", resp.Body.String())
	}
	if hasGeminiModelName(payload.Models, "models/gemini-2.5-pro") {
		t.Fatalf("did not expect out-of-scope public catalog model %q in response body=%s", "models/gemini-2.5-pro", resp.Body.String())
	}
}

func TestGeminiHandler_UsesGeminiErrorEnvelopeForExecutionErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	clientID := "gemini-handler-execution-error-antigravity"
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})

	reg.RegisterClient(clientID, "antigravity", []*registry.ModelInfo{
		{
			ID:          "gemini-3.1-pro-preview",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro Preview",
		},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.POST("/v1beta/models/*action", h.GeminiHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3.1-pro-preview:generateContent",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusInternalServerError, resp.Body.String())
	}

	var payload geminiErrorTestPayload
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload.Error.Code != http.StatusInternalServerError {
		t.Fatalf("error.code = %d, want %d", payload.Error.Code, http.StatusInternalServerError)
	}
	if payload.Error.Status != "INTERNAL" {
		t.Fatalf("error.status = %q, want %q", payload.Error.Status, "INTERNAL")
	}
	if payload.Error.Message != "auth_not_found: no auth available" {
		t.Fatalf("error.message = %q, want %q", payload.Error.Message, "auth_not_found: no auth available")
	}
}

func TestGeminiHandler_RejectsToolCallWithoutThoughtSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registerGeminiExecutionModel(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.POST("/v1beta/models/*action", h.GeminiHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3.1-pro-preview:generateContent",
		strings.NewReader(geminiToolCallContinuationRequest("", false)),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}

	wantMessage := "Function call is missing a thought_signature in functionCall parts. This is required for tools to work correctly, and missing thought_signature may lead to degraded model performance. Additional data, function call `default_api:shell_command` , position 2. Please refer to https://ai.google.dev/gemini-api/docs/thought-signatures for more details."
	assertGeminiErrorPayloadWithoutDetails(t, payload, http.StatusBadRequest, "INVALID_ARGUMENT", wantMessage)
}

func TestGeminiHandler_RejectsToolCallWithSkipThoughtSignatureSentinel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registerGeminiExecutionModel(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.POST("/v1beta/models/*action", h.GeminiHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3.1-pro-preview:generateContent",
		strings.NewReader(geminiToolCallContinuationRequest("skip_thought_signature_validator", true)),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}

	wantMessage := "Function call is missing a thought_signature in functionCall parts. This is required for tools to work correctly, and missing thought_signature may lead to degraded model performance. Additional data, function call `default_api:shell_command` , position 2. Please refer to https://ai.google.dev/gemini-api/docs/thought-signatures for more details."
	assertGeminiErrorPayloadWithoutDetails(t, payload, http.StatusBadRequest, "INVALID_ARGUMENT", wantMessage)
}

func TestGeminiHandler_AllowsToolCallWithThoughtSignatureToReachExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registerGeminiExecutionModel(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.POST("/v1beta/models/*action", h.GeminiHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3.1-pro-preview:generateContent",
		strings.NewReader(geminiToolCallContinuationRequest(strings.Repeat("a", 160), true)),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusInternalServerError, resp.Body.String())
	}

	var payload geminiErrorTestPayload
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload.Error.Message != "auth_not_found: no auth available" {
		t.Fatalf("error.message = %q, want %q", payload.Error.Message, "auth_not_found: no auth available")
	}
}

func TestGeminiHandler_StreamRejectsToolCallWithoutThoughtSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registerGeminiExecutionModel(t)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.POST("/v1beta/models/*action", h.GeminiHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3.1-pro-preview:streamGenerateContent?alt=sse",
		strings.NewReader(geminiToolCallContinuationRequest("", false)),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}

	wantMessage := "Function call is missing a thought_signature in functionCall parts. This is required for tools to work correctly, and missing thought_signature may lead to degraded model performance. Additional data, function call `default_api:shell_command` , position 2. Please refer to https://ai.google.dev/gemini-api/docs/thought-signatures for more details."
	assertGeminiErrorPayloadWithoutDetails(t, payload, http.StatusBadRequest, "INVALID_ARGUMENT", wantMessage)
}

func TestWriteGeminiErrorMessage_NormalizesGoogleStyleInvalidJSONPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	h := NewGeminiAPIHandler(base)
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		h.writeGeminiErrorMessage(c, &interfaces.ErrorMessage{
			StatusCode: http.StatusBadRequest,
			Error: errors.New(`{
  "error": {
    "code": 400,
    "message": "Invalid JSON payload received. Unknown name \"badField\" at 'request': Cannot find field.",
    "status": "INVALID_ARGUMENT",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.BadRequest",
        "fieldViolations": [
          {
            "field": "request",
            "description": "Invalid JSON payload received. Unknown name \"badField\" at 'request': Cannot find field."
          }
        ]
      }
    ]
  }
}`),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	errPayload, _ := payload["error"].(map[string]any)
	if got, _ := errPayload["message"].(string); got != "Invalid JSON payload received. Unknown name \"badField\": Cannot find field." {
		t.Fatalf("error.message = %q", got)
	}

	details, _ := errPayload["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	detail, _ := details[0].(map[string]any)
	fieldViolations, _ := detail["fieldViolations"].([]any)
	if len(fieldViolations) != 1 {
		t.Fatalf("fieldViolations len = %d, want 1", len(fieldViolations))
	}
	violation, _ := fieldViolations[0].(map[string]any)
	if _, exists := violation["field"]; exists {
		t.Fatalf("expected normalized violation to omit field=request, got %#v", violation)
	}
}

func hasGeminiModelName(models []struct {
	Name string `json:"name"`
}, want string) bool {
	for _, model := range models {
		if model.Name == want {
			return true
		}
	}
	return false
}

func geminiToolCallContinuationRequest(thoughtSignature string, includeThoughtSignature bool) string {
	part := `{"functionCall":{"name":"shell_command","args":{"command":"ls"}}}`
	if includeThoughtSignature {
		part = fmt.Sprintf(`{"thoughtSignature":%q,"functionCall":{"name":"shell_command","args":{"command":"ls"}}}`, thoughtSignature)
	}

	return fmt.Sprintf(`{
  "contents": [
    {
      "role": "user",
      "parts": [
        {
          "text": "Run ls command"
        }
      ]
    },
    {
      "role": "model",
      "parts": [
        %s
      ]
    },
    {
      "role": "user",
      "parts": [
        {
          "functionResponse": {
            "name": "shell_command",
            "response": {
              "output": "file1"
            }
          }
        }
      ]
    }
  ],
  "tools": [
    {
      "functionDeclarations": [
        {
          "name": "shell_command",
          "description": "Run shell command",
          "parameters": {
            "type": "object",
            "properties": {
              "command": {
                "type": "string"
              }
            },
            "required": [
              "command"
            ]
          }
        }
      ]
    }
  ],
  "generationConfig": {
    "maxOutputTokens": 512
  }
}`, part)
}

func assertGeminiErrorPayload(t *testing.T, payload map[string]any, wantCode int, wantStatus, wantMessage string) {
	t.Helper()

	errPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error payload: %#v", payload)
	}
	if got, _ := errPayload["code"].(float64); int(got) != wantCode {
		t.Fatalf("error.code = %v, want %d", errPayload["code"], wantCode)
	}
	if got, _ := errPayload["status"].(string); got != wantStatus {
		t.Fatalf("error.status = %q, want %q", got, wantStatus)
	}
	if got, _ := errPayload["message"].(string); got != wantMessage {
		t.Fatalf("error.message = %q, want %q", got, wantMessage)
	}

	details, _ := errPayload["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	detail, _ := details[0].(map[string]any)
	fieldViolations, _ := detail["fieldViolations"].([]any)
	if len(fieldViolations) != 1 {
		t.Fatalf("fieldViolations len = %d, want 1", len(fieldViolations))
	}
	violation, _ := fieldViolations[0].(map[string]any)
	if got, _ := violation["description"].(string); got != wantMessage {
		t.Fatalf("fieldViolations[0].description = %q, want %q", got, wantMessage)
	}
}

func assertGeminiErrorPayloadWithoutDetails(t *testing.T, payload map[string]any, wantCode int, wantStatus, wantMessage string) {
	t.Helper()

	errPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error payload: %#v", payload)
	}
	if got, _ := errPayload["code"].(float64); int(got) != wantCode {
		t.Fatalf("error.code = %v, want %d", errPayload["code"], wantCode)
	}
	if got, _ := errPayload["status"].(string); got != wantStatus {
		t.Fatalf("error.status = %q, want %q", got, wantStatus)
	}
	if got, _ := errPayload["message"].(string); got != wantMessage {
		t.Fatalf("error.message = %q, want %q", got, wantMessage)
	}
	if _, exists := errPayload["details"]; exists {
		t.Fatalf("expected no error.details, got %#v", errPayload["details"])
	}
}

func registerGeminiExecutionModel(t *testing.T) {
	t.Helper()

	reg := registry.GetGlobalRegistry()
	clientID := "gemini-handler-execution-antigravity"
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})

	reg.RegisterClient(clientID, "antigravity", []*registry.ModelInfo{
		{
			ID:          "gemini-3.1-pro-preview",
			Object:      "model",
			OwnedBy:     "antigravity",
			Type:        "antigravity",
			DisplayName: "Gemini 3.1 Pro Preview",
		},
	})
}
