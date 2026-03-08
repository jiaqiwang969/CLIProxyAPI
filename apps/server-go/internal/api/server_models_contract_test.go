package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
)

func TestUnifiedModelsHandler_DoesNotSwitchOnUserAgent(t *testing.T) {
	server := newTestServer(t)

	reg := registry.GetGlobalRegistry()
	const gptClient = "server-models-auggie-gpt"
	const claudeClient = "server-models-antigravity-claude"
	reg.UnregisterClient(gptClient)
	reg.UnregisterClient(claudeClient)
	t.Cleanup(func() {
		reg.UnregisterClient(gptClient)
		reg.UnregisterClient(claudeClient)
	})

	reg.RegisterClient(gptClient, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5.4", Object: "model", OwnedBy: "auggie", Type: "auggie", Version: "gpt-5-4"},
	})
	reg.RegisterClient(claudeClient, "antigravity", []*registry.ModelInfo{
		{ID: "claude-opus-4-6", Object: "model", OwnedBy: "antigravity", Type: "antigravity"},
	})

	defaultResp := fetchOpenAIModelsResponse(t, server, "")
	claudeCLIResp := fetchOpenAIModelsResponse(t, server, "claude-cli/1.0")

	if defaultResp.Object != "list" {
		t.Fatalf("default object = %q, want %q", defaultResp.Object, "list")
	}
	if claudeCLIResp.Object != "list" {
		t.Fatalf("claude-cli object = %q, want %q; raw=%s", claudeCLIResp.Object, "list", claudeCLIResp.Raw)
	}

	defaultIDs := defaultResp.IDSet()
	claudeCLIIDs := claudeCLIResp.IDSet()
	if len(defaultIDs) == 0 {
		t.Fatalf("default IDs empty")
	}
	if len(defaultIDs) != len(claudeCLIIDs) {
		t.Fatalf("model count mismatch: default=%v claude-cli=%v", defaultIDs, claudeCLIIDs)
	}
	for id := range defaultIDs {
		if _, ok := claudeCLIIDs[id]; !ok {
			t.Fatalf("claude-cli response missing model %q; default=%v claude-cli=%v", id, defaultIDs, claudeCLIIDs)
		}
	}
}

type openAIModelsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID string `json:"id"`
	} `json:"data"`
	Raw string `json:"-"`
}

func (r openAIModelsResponse) IDSet() map[string]struct{} {
	out := make(map[string]struct{}, len(r.Data))
	for _, model := range r.Data {
		out[model.ID] = struct{}{}
	}
	return out
}

func fetchOpenAIModelsResponse(t *testing.T, server *Server, userAgent string) openAIModelsResponse {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	rec := httptest.NewRecorder()
	server.engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload openAIModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, rec.Body.String())
	}
	payload.Raw = rec.Body.String()
	return payload
}
