package executor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestFetchAuggieModelsMapsUpstreamNamesAndDefaultModel(t *testing.T) {
	models, updatedAuth := fetchAuggieModelsForTest(t, http.StatusOK, `{
		"default_model":"gpt-5.4",
		"models":[{"name":"gpt-5.4"},{"name":"claude-opus-4.6"}]
	}`)

	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	if got := updatedAuth.Metadata["default_model"]; got != "gpt-5.4" {
		t.Fatalf("default_model = %v, want gpt-5.4", got)
	}
	if got := models[0].ID; got != "gpt-5.4" {
		t.Fatalf("first model id = %q, want gpt-5.4", got)
	}
	if got := models[1].ID; got != "claude-opus-4.6" {
		t.Fatalf("second model id = %q, want claude-opus-4.6", got)
	}
	if got := models[0].Object; got != "model" {
		t.Fatalf("object = %q, want model", got)
	}
	if got := models[0].OwnedBy; got != "auggie" {
		t.Fatalf("owned_by = %q, want auggie", got)
	}
	if got := models[0].Type; got != "auggie" {
		t.Fatalf("type = %q, want auggie", got)
	}
	if got := models[0].DisplayName; got != "gpt-5.4" {
		t.Fatalf("display_name = %q, want gpt-5.4", got)
	}
}

func TestFetchAuggieModelsUsesModelInfoRegistryWhenBackendModelNamesAreOpaque(t *testing.T) {
	modelInfoRegistry := map[string]any{
		"claude-haiku-4-5": map[string]any{
			"byokProvider": "anthropic",
			"description":  "Fast and efficient responses",
			"disabled":     false,
			"displayName":  "Haiku 4.5",
			"shortName":    "haiku4.5",
		},
		"claude-opus-4-5": map[string]any{
			"byokProvider": "anthropic",
			"description":  "Best for complex tasks",
			"disabled":     false,
			"displayName":  "Claude Opus 4.5",
			"isDefault":    true,
			"shortName":    "opus4.5",
		},
		"disabled-model": map[string]any{
			"description": "Not available",
			"disabled":    true,
			"displayName": "Disabled Model",
		},
		"gpt-5-1": map[string]any{
			"byokProvider": "openai",
			"description":  "Strong reasoning and planning",
			"disabled":     false,
			"displayName":  "GPT-5.1",
			"shortName":    "gpt5.1",
		},
	}
	body := mustMarshalAuggieJSON(t, map[string]any{
		"default_model": "9c199f09053b637dd66d9fe1454467b6de40ce10344042674b7f34c9cb69f440",
		"models": []map[string]any{
			{"name": "4e68d9be07a644ce975509fa4c7afae84b51ca39986e9c40db4ad6a2cf756948"},
			{"name": "c96e6a74aee83e1fa8947916ce7aa0c72387f4c170242d2ec30194fd6a63a001"},
		},
		"feature_flags": map[string]any{
			"agent_chat_model":    "claude-sonnet-4-0-200k-v9-c4-p2-agent",
			"model_info_registry": mustMarshalAuggieJSON(t, modelInfoRegistry),
		},
	})

	models, updatedAuth := fetchAuggieModelsForTest(t, http.StatusOK, body)

	if len(models) != 3 {
		t.Fatalf("models = %d, want 3", len(models))
	}
	gotIDs := make([]string, 0, len(models))
	byID := make(map[string]*registry.ModelInfo, len(models))
	for _, model := range models {
		gotIDs = append(gotIDs, model.ID)
		byID[model.ID] = model
	}
	sort.Strings(gotIDs)
	if want := []string{"claude-haiku-4-5", "claude-opus-4-5", "gpt-5-1"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("model ids = %#v, want %#v", gotIDs, want)
	}
	if _, ok := byID["4e68d9be07a644ce975509fa4c7afae84b51ca39986e9c40db4ad6a2cf756948"]; ok {
		t.Fatal("expected opaque backend model id to be ignored")
	}
	if _, ok := byID["disabled-model"]; ok {
		t.Fatal("expected disabled model to be excluded")
	}
	if got := byID["claude-opus-4-5"].DisplayName; got != "Claude Opus 4.5" {
		t.Fatalf("display_name = %q, want Claude Opus 4.5", got)
	}
	if got := byID["claude-opus-4-5"].Description; got != "Best for complex tasks" {
		t.Fatalf("description = %q, want Best for complex tasks", got)
	}
	if got := byID["gpt-5-1"].Description; got != "Strong reasoning and planning" {
		t.Fatalf("gpt-5-1 description = %q, want Strong reasoning and planning", got)
	}
	if got := updatedAuth.Metadata["default_model"]; got != "claude-opus-4-5" {
		t.Fatalf("default_model = %v, want claude-opus-4-5", got)
	}
	if got := updatedAuth.Metadata["default_model_raw"]; got != "9c199f09053b637dd66d9fe1454467b6de40ce10344042674b7f34c9cb69f440" {
		t.Fatalf("default_model_raw = %v, want upstream hash", got)
	}
}

func TestFetchAuggieModelsClearsFailureStateAfterDirectSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[{"name":"gpt-5.4"}]}`))
	}))
	defer server.Close()

	entry := &auth.Auth{
		Provider:       "auggie",
		Label:          "tenant.augmentcode.com",
		FileName:       "auggie-tenant-augmentcode-com.json",
		Status:         auth.StatusError,
		StatusMessage:  "unauthorized",
		Unavailable:    true,
		LastError:      &auth.Error{Code: "unauthorized", Message: "unauthorized", HTTPStatus: http.StatusUnauthorized},
		NextRetryAfter: time.Now().Add(time.Hour),
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "token-1",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, entry, &config.Config{})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	if got := entry.Status; got != auth.StatusActive {
		t.Fatalf("status = %q, want %q", got, auth.StatusActive)
	}
	if entry.Unavailable {
		t.Fatal("expected auth to become available")
	}
	if entry.StatusMessage != "" {
		t.Fatalf("status_message = %q, want empty", entry.StatusMessage)
	}
	if entry.LastError != nil {
		t.Fatalf("last_error = %#v, want nil", entry.LastError)
	}
	if !entry.NextRetryAfter.IsZero() {
		t.Fatalf("next_retry_after = %v, want zero", entry.NextRetryAfter)
	}
}

func TestFetchAuggieModelsClearsFailureStateAfterEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[]}`))
	}))
	defer server.Close()

	entry := &auth.Auth{
		Provider:       "auggie",
		Label:          "tenant.augmentcode.com",
		FileName:       "auggie-tenant-augmentcode-com.json",
		Status:         auth.StatusError,
		StatusMessage:  "unauthorized",
		Unavailable:    true,
		LastError:      &auth.Error{Code: "unauthorized", Message: "unauthorized", HTTPStatus: http.StatusUnauthorized},
		NextRetryAfter: time.Now().Add(time.Hour),
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "token-1",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, entry, &config.Config{})
	if len(models) != 0 {
		t.Fatalf("models = %d, want 0", len(models))
	}
	if got := entry.Status; got != auth.StatusActive {
		t.Fatalf("status = %q, want %q", got, auth.StatusActive)
	}
	if entry.Unavailable {
		t.Fatal("expected auth to become available")
	}
	if entry.StatusMessage != "" {
		t.Fatalf("status_message = %q, want empty", entry.StatusMessage)
	}
	if entry.LastError != nil {
		t.Fatalf("last_error = %#v, want nil", entry.LastError)
	}
	if !entry.NextRetryAfter.IsZero() {
		t.Fatalf("next_retry_after = %v, want zero", entry.NextRetryAfter)
	}
	if got := entry.Metadata["default_model"]; got != "gpt-5.4" {
		t.Fatalf("default_model = %v, want gpt-5.4", got)
	}
}

func TestFetchAuggieModelsRetriesAfterRefreshOnUnauthorized(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/get-models" {
			t.Fatalf("path = %q, want /get-models", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); attempts == 1 && got != "Bearer stale-token" {
			t.Fatalf("first authorization = %q, want Bearer stale-token", got)
		}
		if got := r.Header.Get("Authorization"); attempts == 2 && got != "Bearer session-token" {
			t.Fatalf("second authorization = %q, want Bearer session-token", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if strings.TrimSpace(string(body)) != "{}" {
			t.Fatalf("body = %q, want {}", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[{"name":"gpt-5.4"}]}`))
	}))
	defer server.Close()

	entry := &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "stale-token",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, entry, &config.Config{})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := entry.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
}

func TestFetchAuggieModelsRefreshesWhenAccessTokenMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Fatalf("authorization = %q, want Bearer session-token", got)
		}
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[{"name":"gpt-5.4"}]}`))
	}))
	defer server.Close()

	entry := &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":       "auggie",
			"label":      "tenant.augmentcode.com",
			"tenant_url": "https://tenant.augmentcode.com/",
			"client_id":  "auggie-cli",
			"login_mode": "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, entry, &config.Config{})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if got := entry.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
}

func TestFetchAuggieModelsRefreshesWhenTenantURLInvalid(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Fatalf("authorization = %q, want Bearer session-token", got)
		}
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[{"name":"gpt-5.4"}]}`))
	}))
	defer server.Close()

	auth := &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "stale-token",
			"tenant_url":   "https://evil.example.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, auth, &config.Config{})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if got := auth.Metadata["tenant_url"]; got != "https://tenant.augmentcode.com/" {
		t.Fatalf("tenant_url = %v, want https://tenant.augmentcode.com/", got)
	}
}

func TestFetchAuggieModelsKeepsRefreshedTokenWhenRetryStillFails(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"still-broken"}`))
	}))
	defer server.Close()

	entry := &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "stale-token",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, entry, &config.Config{})
	if len(models) != 0 {
		t.Fatalf("models = %d, want 0", len(models))
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := entry.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
}

func TestFetchAuggieModelsMarksAuthUnauthorizedWhenRetryStillReturnsUnauthorized(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	entry := &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "stale-token",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, entry, &config.Config{})
	if len(models) != 0 {
		t.Fatalf("models = %d, want 0", len(models))
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := entry.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
	if got := entry.Status; got != auth.StatusError {
		t.Fatalf("status = %q, want %q", got, auth.StatusError)
	}
	if !entry.Unavailable {
		t.Fatal("expected auth to be unavailable")
	}
	if got := entry.StatusMessage; got != "unauthorized" {
		t.Fatalf("status_message = %q, want unauthorized", got)
	}
	if entry.LastError == nil || entry.LastError.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("last_error = %#v, want http_status 401", entry.LastError)
	}
}

func TestAuggieRefreshReloadsSessionFileOnUnauthorized(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeAuggieSessionFile(t, homeDir, `{"accessToken":"session-token","tenantURL":"https://tenant.augmentcode.com","scopes":["email"]}`)

	auth, err := refreshAuggieFromSessionForTest(t)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if got := auth.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
	if got := auth.Metadata["tenant_url"]; got != "https://tenant.augmentcode.com/" {
		t.Fatalf("tenant_url = %v, want https://tenant.augmentcode.com/", got)
	}
	if got := auth.Label; got != "tenant.augmentcode.com" {
		t.Fatalf("label = %q, want tenant.augmentcode.com", got)
	}
}

func fetchAuggieModelsForTest(t *testing.T, statusCode int, body string) ([]*registry.ModelInfo, *auth.Auth) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get-models" {
			t.Fatalf("path = %q, want /get-models", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("authorization = %q, want Bearer token-1", got)
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if strings.TrimSpace(string(payload)) != "{}" {
			t.Fatalf("payload = %q, want {}", string(payload))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	auth := &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "token-1",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli-json-paste",
			"login_mode":   "manual_json_paste",
		},
	}

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", newAuggieRewriteTransport(t, server.URL))
	models := FetchAuggieModels(ctx, auth, &config.Config{})
	return models, auth
}

func refreshAuggieFromSessionForTest(t *testing.T) (*auth.Auth, error) {
	t.Helper()

	exec := NewAuggieExecutor(&config.Config{})
	return exec.Refresh(context.Background(), &auth.Auth{
		Provider: "auggie",
		Label:    "tenant.augmentcode.com",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "stale-token",
			"tenant_url":   "https://tenant.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	})
}

func writeAuggieSessionFile(t *testing.T, homeDir, body string) {
	t.Helper()

	sessionDir := filepath.Join(homeDir, ".augment")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}
}

func newAuggieRewriteTransport(t *testing.T, targetURL string) http.RoundTripper {
	t.Helper()

	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	base := http.DefaultTransport
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		return base.RoundTrip(clone)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mustMarshalAuggieJSON(t *testing.T, value any) string {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(body)
}
