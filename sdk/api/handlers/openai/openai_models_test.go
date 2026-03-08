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

func TestOpenAIModelsIncludesAuggieDisplayAliases(t *testing.T) {
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

	for _, modelID := range []string{"gpt-5.4", "claude-opus-4.5"} {
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
}
