package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestOpenAIModelsCollapsesAuggieAliasesToCanonicalIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clientID := "auggie-openai-models-display-aliases"
	reg := registry.GetGlobalRegistry()
	reg.UnregisterClient(clientID)
	t.Cleanup(func() {
		reg.UnregisterClient(clientID)
	})

	reg.RegisterClient(clientID, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5-4", Object: "model", OwnedBy: "auggie", Type: "auggie", DisplayName: "GPT-5.4", Version: "gpt-5-4"},
		{ID: "gpt5.4", Object: "model", OwnedBy: "auggie", Type: "auggie", DisplayName: "gpt5.4", Version: "gpt-5-4"},
		{ID: "gpt-5.4", Object: "model", OwnedBy: "auggie", Type: "auggie", DisplayName: "GPT-5.4", Version: "gpt-5-4"},
		{ID: "claude-opus-4-5", Object: "model", OwnedBy: "auggie", Type: "auggie", DisplayName: "Claude Opus 4.5", Version: "claude-opus-4-5"},
		{ID: "claude-opus-4.5", Object: "model", OwnedBy: "auggie", Type: "auggie", DisplayName: "Claude Opus 4.5", Version: "claude-opus-4-5"},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.GET("/v1/models", h.OpenAIModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload.Object != "list" {
		t.Fatalf("object = %q, want %q", payload.Object, "list")
	}

	modelByID := make(map[string]struct {
		Object  string
		OwnedBy string
	}, len(payload.Data))
	for _, model := range payload.Data {
		modelByID[model.ID] = struct {
			Object  string
			OwnedBy string
		}{
			Object:  model.Object,
			OwnedBy: model.OwnedBy,
		}
	}

	for _, modelID := range []string{"gpt-5-4", "claude-opus-4-5"} {
		model, ok := modelByID[modelID]
		if !ok {
			t.Fatalf("expected %q in /v1/models payload, ids=%v", modelID, modelByID)
		}
		if model.Object != "model" {
			t.Fatalf("%s object = %q, want %q", modelID, model.Object, "model")
		}
		if model.OwnedBy != "auggie" {
			t.Fatalf("%s owned_by = %q, want %q", modelID, model.OwnedBy, "auggie")
		}
	}

	for _, aliasID := range []string{"gpt5.4", "gpt-5.4", "claude-opus-4.5"} {
		if _, exists := modelByID[aliasID]; exists {
			t.Fatalf("did not expect Auggie alias %q in /v1/models payload, ids=%v", aliasID, modelByID)
		}
	}
}

func TestOpenAIModels_ReturnsUnifiedGPTAndClaudeCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	const gptClient = "openai-models-gpt"
	const claudeClient = "openai-models-claude"
	const geminiClient = "openai-models-gemini"
	reg.UnregisterClient(gptClient)
	reg.UnregisterClient(claudeClient)
	reg.UnregisterClient(geminiClient)
	t.Cleanup(func() {
		reg.UnregisterClient(gptClient)
		reg.UnregisterClient(claudeClient)
		reg.UnregisterClient(geminiClient)
	})

	reg.RegisterClient(gptClient, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5.4", Object: "model", OwnedBy: "auggie", Type: "auggie", Version: "gpt-5-4"},
	})
	reg.RegisterClient(claudeClient, "antigravity", []*registry.ModelInfo{
		{ID: "claude-opus-4-6", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})
	reg.RegisterClient(geminiClient, "antigravity", []*registry.ModelInfo{
		{ID: "gemini-3.1-pro-preview", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.GET("/v1/models", h.OpenAIModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	modelByID := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		modelByID[model.ID] = struct{}{}
	}

	for _, modelID := range []string{"gpt-5-4", "claude-opus-4-6"} {
		if _, ok := modelByID[modelID]; !ok {
			t.Fatalf("expected %q in OpenAI catalog, ids=%v", modelID, modelByID)
		}
	}
	if _, exists := modelByID["gemini-3.1-pro-preview"]; exists {
		t.Fatalf("did not expect Gemini model in OpenAI catalog, ids=%v", modelByID)
	}
}

func TestOpenAIModels_DeduplicatesCanonicalIDsAndFiltersInternalVariants(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	const claudeCanonicalClient = "openai-models-canonical-claude"
	const claudeDatedClient = "openai-models-dated-claude"
	const hybridClient = "openai-models-hybrid"
	const thinkingClient = "openai-models-thinking"
	const gptOSSClient = "openai-models-gpt-oss"
	for _, clientID := range []string{claudeCanonicalClient, claudeDatedClient, hybridClient, thinkingClient, gptOSSClient} {
		reg.UnregisterClient(clientID)
	}
	t.Cleanup(func() {
		for _, clientID := range []string{claudeCanonicalClient, claudeDatedClient, hybridClient, thinkingClient, gptOSSClient} {
			reg.UnregisterClient(clientID)
		}
	})

	reg.RegisterClient(claudeCanonicalClient, "antigravity", []*registry.ModelInfo{
		{ID: "claude-sonnet-4-5", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})
	reg.RegisterClient(claudeDatedClient, "antigravity", []*registry.ModelInfo{
		{ID: "claude-sonnet-4-5-20250929", Object: "model", OwnedBy: "anthropic", Type: "claude"},
	})
	reg.RegisterClient(hybridClient, "antigravity", []*registry.ModelInfo{
		{ID: "gemini-claude-sonnet-4-5", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})
	reg.RegisterClient(thinkingClient, "antigravity", []*registry.ModelInfo{
		{ID: "claude-sonnet-4-5-thinking", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})
	reg.RegisterClient(gptOSSClient, "antigravity", []*registry.ModelInfo{
		{ID: "gpt-oss-120b-medium", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.GET("/v1/models", h.OpenAIModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	countByID := make(map[string]int, len(payload.Data))
	for _, model := range payload.Data {
		countByID[model.ID]++
	}

	if countByID["claude-sonnet-4-5"] != 1 {
		t.Fatalf("expected exactly one canonical claude-sonnet-4-5 entry, got ids=%v", countByID)
	}
	for _, internalID := range []string{"gemini-claude-sonnet-4-5", "claude-sonnet-4-5-thinking", "gpt-oss-120b-medium"} {
		if countByID[internalID] != 0 {
			t.Fatalf("did not expect internal variant %q in OpenAI catalog, ids=%v", internalID, countByID)
		}
	}
}

func TestOpenAIModels_FiltersToScopedAuthModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	const scopedAuthID = "openai-models-scoped-auggie"
	const otherAuthID = "openai-models-scoped-antigravity"
	reg.UnregisterClient(scopedAuthID)
	reg.UnregisterClient(otherAuthID)
	t.Cleanup(func() {
		reg.UnregisterClient(scopedAuthID)
		reg.UnregisterClient(otherAuthID)
	})

	reg.RegisterClient(scopedAuthID, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5-4", Object: "model", OwnedBy: "auggie", Type: "auggie", Version: "gpt-5-4"},
	})
	reg.RegisterClient(otherAuthID, "antigravity", []*registry.ModelInfo{
		{ID: "claude-opus-4-6", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("accessScopeAuthID", scopedAuthID)
		c.Next()
	})
	router.GET("/v1/models", h.OpenAIModels)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	modelByID := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		modelByID[model.ID] = struct{}{}
	}

	if _, ok := modelByID["gpt-5-4"]; !ok {
		t.Fatalf("expected scoped model %q in OpenAI catalog, ids=%v", "gpt-5-4", modelByID)
	}
	if _, exists := modelByID["claude-opus-4-6"]; exists {
		t.Fatalf("did not expect out-of-scope model %q in OpenAI catalog, ids=%v", "claude-opus-4-6", modelByID)
	}
}
