package test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func newClientAPIKeysTestHandler(t *testing.T) (*management.Handler, *config.Config, *coreauth.Manager, string, string) {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("port: 8080\nauth-dir: "+authDir+"\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	authManager := coreauth.NewManager(nil, nil, nil)
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: []string{"legacy-key"},
		},
		AuthDir: authDir,
	}
	h := management.NewHandler(cfg, configPath, authManager)
	return h, cfg, authManager, configPath, authDir
}

func setupClientAPIKeysRouter(h *management.Handler) *gin.Engine {
	r := gin.New()
	mgmt := r.Group("/v0/management")
	{
		mgmt.GET("/client-api-keys", h.GetClientAPIKeys)
		mgmt.PUT("/client-api-keys", h.PutClientAPIKeys)
		mgmt.GET("/auth-files", h.ListAuthFiles)
	}
	return r
}

func TestPutClientAPIKeys_PersistsStructuredScopedKeys(t *testing.T) {
	h, _, _, configPath, _ := newClientAPIKeysTestHandler(t)
	r := setupClientAPIKeysRouter(h)

	body := `{
	  "client-api-keys": [
	    {
	      "key": " scoped-key ",
	      "note": " primary auggie ",
	      "scope": {
	        "provider": " auggie ",
	        "auth_id": " auggie-main ",
	        "models": [" gpt-5-4 ", "gpt-5-4", ""]
	      }
	    }
	  ]
	}`
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-keys", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.ClientAPIKeys) != 1 {
		t.Fatalf("expected 1 client-api-keys entry, got %d", len(loaded.ClientAPIKeys))
	}
	entry := loaded.ClientAPIKeys[0]
	if entry.Key != "scoped-key" {
		t.Fatalf("key = %q, want %q", entry.Key, "scoped-key")
	}
	if entry.Note != "primary auggie" {
		t.Fatalf("note = %q, want %q", entry.Note, "primary auggie")
	}
	if entry.Scope.Provider != "auggie" {
		t.Fatalf("scope.provider = %q, want %q", entry.Scope.Provider, "auggie")
	}
	if entry.Scope.AuthID != "auggie-main" {
		t.Fatalf("scope.auth_id = %q, want %q", entry.Scope.AuthID, "auggie-main")
	}
	if len(entry.Scope.Models) != 1 || entry.Scope.Models[0] != "gpt-5-4" {
		t.Fatalf("scope.models = %#v, want [gpt-5-4]", entry.Scope.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/v0/management/client-api-keys", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	var resp struct {
		ClientAPIKeys []config.ClientAPIKey `json:"client-api-keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.ClientAPIKeys) != 1 || resp.ClientAPIKeys[0].Scope.AuthID != "auggie-main" {
		t.Fatalf("unexpected GET payload: %#v", resp.ClientAPIKeys)
	}
}

func TestPutClientAPIKeys_MigratesSubmittedLegacyKeyIntoStructuredConfig(t *testing.T) {
	h, _, _, configPath, _ := newClientAPIKeysTestHandler(t)
	r := setupClientAPIKeysRouter(h)

	body := `{
	  "client-api-keys": [
	    {
	      "key": "legacy-key",
	      "note": "migrated"
	    }
	  ]
	}`
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-keys", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.APIKeys) != 0 {
		t.Fatalf("expected migrated legacy key to be removed from top-level api-keys, got %#v", loaded.APIKeys)
	}
	if len(loaded.ClientAPIKeys) != 1 {
		t.Fatalf("expected migrated legacy key to be persisted structurally, got %#v", loaded.ClientAPIKeys)
	}
	if loaded.ClientAPIKeys[0].Key != "legacy-key" || loaded.ClientAPIKeys[0].Note != "migrated" {
		t.Fatalf("unexpected migrated structured key: %#v", loaded.ClientAPIKeys[0])
	}
}

func TestPutClientAPIKeys_DeleteRemovesLegacyKeyFromManagedView(t *testing.T) {
	h, _, _, configPath, _ := newClientAPIKeysTestHandler(t)
	r := setupClientAPIKeysRouter(h)

	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-keys", bytes.NewBufferString(`{"client-api-keys":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(loaded.APIKeys) != 0 {
		t.Fatalf("expected delete to clear legacy api-keys, got %#v", loaded.APIKeys)
	}
	if len(loaded.ClientAPIKeys) != 0 {
		t.Fatalf("expected delete to leave no structured keys, got %#v", loaded.ClientAPIKeys)
	}
}

func TestGetClientAPIKeys_KeepsDisabledStructuredEntriesVisible(t *testing.T) {
	h, cfg, _, _, _ := newClientAPIKeysTestHandler(t)
	r := setupClientAPIKeysRouter(h)

	disabled := false
	cfg.ClientAPIKeys = []config.ClientAPIKey{
		{
			Key:     "disabled-structured",
			Enabled: &disabled,
			Note:    "off",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/v0/management/client-api-keys", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp struct {
		ClientAPIKeys []config.ClientAPIKey `json:"client-api-keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.ClientAPIKeys) != 2 {
		t.Fatalf("expected legacy + disabled structured keys, got %#v", resp.ClientAPIKeys)
	}
	if resp.ClientAPIKeys[1].Key != "disabled-structured" || resp.ClientAPIKeys[1].Enabled == nil || *resp.ClientAPIKeys[1].Enabled {
		t.Fatalf("expected disabled structured key to remain visible, got %#v", resp.ClientAPIKeys)
	}
}

func TestListAuthFiles_IncludeModelsReturnsPerAuthModelInventory(t *testing.T) {
	h, _, authManager, _, authDir := newClientAPIKeysTestHandler(t)
	r := setupClientAPIKeysRouter(h)

	auth := &coreauth.Auth{
		ID:       "auggie-main",
		Provider: "auggie",
		FileName: "auggie-main.json",
		Label:    "auggie-main",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filepath.Join(authDir, "auggie-main.json"),
		},
	}
	if _, err := authManager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	reg := registry.GetGlobalRegistry()
	reg.UnregisterClient("auggie-main")
	t.Cleanup(func() {
		reg.UnregisterClient("auggie-main")
	})
	reg.RegisterClient("auggie-main", "auggie", []*registry.ModelInfo{
		{ID: "gpt-5-4", DisplayName: "GPT-5.4"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	})

	req := httptest.NewRequest(http.MethodGet, "/v0/management/auth-files?include_models=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp struct {
		Files []struct {
			ID     string `json:"id"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 auth file entry, got %d", len(resp.Files))
	}
	if resp.Files[0].ID != "auggie-main" {
		t.Fatalf("id = %q, want %q", resp.Files[0].ID, "auggie-main")
	}
	if len(resp.Files[0].Models) != 2 {
		t.Fatalf("expected 2 models, got %#v", resp.Files[0].Models)
	}
}
