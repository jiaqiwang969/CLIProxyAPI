package cliproxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestEnsureExecutorsForAuthWithMode_RegistersAuggieExecutor(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{
		cfg:         &config.Config{},
		coreManager: manager,
	}

	service.ensureExecutorsForAuthWithMode(&coreauth.Auth{
		ID:       "auggie-executor-auth",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
	}, false)

	resolved, ok := manager.Executor("auggie")
	if !ok {
		t.Fatal("expected auggie executor to be registered")
	}
	if _, ok := resolved.(*runtimeexecutor.AuggieExecutor); !ok {
		t.Fatalf("executor type = %T, want *executor.AuggieExecutor", resolved)
	}
}

func TestRegisterModelsForAuth_AuggieBackfillsOnlySameTenant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get-models" {
			t.Fatalf("path = %q, want /get-models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer source-token" {
			t.Fatalf("authorization = %q, want Bearer source-token", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if strings.TrimSpace(string(body)) != "{}" {
			t.Fatalf("body = %q, want {}", string(body))
		}
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[{"name":"gpt-5.4"},{"name":"claude-opus-4.6"}]}`))
	}))
	defer server.Close()
	rewriteAuggieDefaultTransport(t, server.URL)

	source := &coreauth.Auth{
		ID:       "auggie-source",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"access_token": "source-token",
			"tenant_url":   "https://a.augmentcode.com/",
			"client_id":    "auggie-cli",
			"login_mode":   "localhost",
		},
	}
	sameTenant := &coreauth.Auth{
		ID:       "auggie-same",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"tenant_url":   "https://a.augmentcode.com/",
			"access_token": "same-token",
		},
	}
	otherTenant := &coreauth.Auth{
		ID:       "auggie-other",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "b.augmentcode.com",
			"tenant_url":   "https://b.augmentcode.com/",
			"access_token": "other-token",
		},
	}

	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{source, sameTenant, otherTenant} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}

	service := &Service{
		cfg:         &config.Config{},
		coreManager: manager,
	}

	reg := registry.GetGlobalRegistry()
	for _, id := range []string{source.ID, sameTenant.ID, otherTenant.ID} {
		reg.UnregisterClient(id)
	}
	t.Cleanup(func() {
		for _, id := range []string{source.ID, sameTenant.ID, otherTenant.ID} {
			reg.UnregisterClient(id)
		}
	})

	service.registerModelsForAuth(source)

	if got := reg.GetModelsForClient(source.ID); len(got) != 2 {
		t.Fatalf("source models = %d, want 2", len(got))
	}
	if got := reg.GetModelsForClient(sameTenant.ID); len(got) != 2 {
		t.Fatalf("same-tenant models = %d, want 2", len(got))
	}
	if got := reg.GetModelsForClient(otherTenant.ID); len(got) != 0 {
		t.Fatalf("other-tenant models = %d, want 0", len(got))
	}
}

func TestRegisterModelsForAuth_AuggieBackfillsModelInfoRegistryModels(t *testing.T) {
	body := mustMarshalAuggieBackfillJSON(t, map[string]any{
		"default_model": "9c199f09053b637dd66d9fe1454467b6de40ce10344042674b7f34c9cb69f440",
		"models": []map[string]any{
			{"name": "4e68d9be07a644ce975509fa4c7afae84b51ca39986e9c40db4ad6a2cf756948"},
			{"name": "c96e6a74aee83e1fa8947916ce7aa0c72387f4c170242d2ec30194fd6a63a001"},
		},
		"feature_flags": map[string]any{
			"model_info_registry": mustMarshalAuggieBackfillJSON(t, map[string]any{
				"claude-opus-4-5": map[string]any{
					"byokProvider": "anthropic",
					"description":  "Best for complex tasks",
					"disabled":     false,
					"displayName":  "Claude Opus 4.5",
					"shortName":    "opus4.5",
				},
				"disabled-model": map[string]any{
					"description": "Disabled",
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
			}),
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	rewriteAuggieDefaultTransport(t, server.URL)

	source := &coreauth.Auth{
		ID:       "auggie-source-model-info",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"access_token": "source-token",
			"tenant_url":   "https://a.augmentcode.com/",
		},
	}
	sameTenant := &coreauth.Auth{
		ID:       "auggie-same-model-info",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"tenant_url":   "https://a.augmentcode.com/",
			"access_token": "same-token",
		},
	}

	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{source, sameTenant} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}

	service := &Service{
		cfg:         &config.Config{},
		coreManager: manager,
	}

	reg := registry.GetGlobalRegistry()
	for _, id := range []string{source.ID, sameTenant.ID} {
		reg.UnregisterClient(id)
	}
	t.Cleanup(func() {
		for _, id := range []string{source.ID, sameTenant.ID} {
			reg.UnregisterClient(id)
		}
	})

	service.registerModelsForAuth(source)

	got := reg.GetModelsForClient(sameTenant.ID)
	if len(got) != 4 {
		t.Fatalf("same-tenant models = %d, want 4", len(got))
	}
	gotIDs := make([]string, 0, len(got))
	for _, model := range got {
		if model == nil {
			t.Fatal("expected non-nil model info")
		}
		gotIDs = append(gotIDs, model.ID)
	}
	sort.Strings(gotIDs)
	if want := []string{"claude-opus-4-5", "gpt-5-1", "gpt5.1", "opus4.5"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("same-tenant model ids = %#v, want %#v", gotIDs, want)
	}
}

func TestRegisterModelsForAuth_AuggieExcludesShortNameWhenCanonicalModelIsExcluded(t *testing.T) {
	body := mustMarshalAuggieBackfillJSON(t, map[string]any{
		"default_model": "gpt-5-1",
		"models": []map[string]any{
			{"name": "opaque"},
		},
		"feature_flags": map[string]any{
			"model_info_registry": mustMarshalAuggieBackfillJSON(t, map[string]any{
				"claude-opus-4-5": map[string]any{
					"description": "Best for complex tasks",
					"displayName": "Claude Opus 4.5",
					"shortName":   "opus4.5",
				},
				"gpt-5-1": map[string]any{
					"description": "Strong reasoning and planning",
					"displayName": "GPT-5.1",
					"shortName":   "gpt5.1",
				},
			}),
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	rewriteAuggieDefaultTransport(t, server.URL)

	source := &coreauth.Auth{
		ID:       "auggie-source-excluded-aliases",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"access_token": "source-token",
			"tenant_url":   "https://a.augmentcode.com/",
		},
	}
	target := &coreauth.Auth{
		ID:       "auggie-target-excluded-aliases",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"excluded_models": "gpt-5-1",
		},
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"tenant_url":   "https://a.augmentcode.com/",
			"access_token": "target-token",
		},
	}

	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{source, target} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}

	service := &Service{
		cfg:         &config.Config{},
		coreManager: manager,
	}

	reg := registry.GetGlobalRegistry()
	for _, id := range []string{source.ID, target.ID} {
		reg.UnregisterClient(id)
	}
	t.Cleanup(func() {
		for _, id := range []string{source.ID, target.ID} {
			reg.UnregisterClient(id)
		}
	})

	service.registerModelsForAuth(source)

	got := reg.GetModelsForClient(target.ID)
	gotIDs := make([]string, 0, len(got))
	for _, model := range got {
		if model == nil {
			t.Fatal("expected non-nil model info")
		}
		gotIDs = append(gotIDs, model.ID)
	}
	sort.Strings(gotIDs)
	if want := []string{"claude-opus-4-5", "opus4.5"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("target model ids = %#v, want %#v", gotIDs, want)
	}
}

func TestRegisterModelsForAuth_AuggieBackfillRespectsAliasPrefixAndExcludedModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_model":"gpt-5.4","models":[{"name":"gpt-5.4"},{"name":"claude-opus-4.6"}]}`))
	}))
	defer server.Close()
	rewriteAuggieDefaultTransport(t, server.URL)

	source := &coreauth.Auth{
		ID:       "auggie-source-alias",
		Provider: "auggie",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"access_token": "source-token",
			"tenant_url":   "https://a.augmentcode.com/",
		},
	}
	target := &coreauth.Auth{
		ID:       "auggie-target-alias",
		Provider: "auggie",
		Prefix:   "team",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"excluded_models": "claude-opus-4.6",
		},
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "a.augmentcode.com",
			"tenant_url":   "https://a.augmentcode.com/",
			"access_token": "target-token",
		},
	}

	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{source, target} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}

	service := &Service{
		cfg: &config.Config{
			SDKConfig: config.SDKConfig{
				ForceModelPrefix: true,
			},
			OAuthModelAlias: map[string][]config.OAuthModelAlias{
				"auggie": {{Name: "gpt-5.4", Alias: "gpt-5"}},
			},
		},
		coreManager: manager,
	}

	reg := registry.GetGlobalRegistry()
	for _, id := range []string{source.ID, target.ID} {
		reg.UnregisterClient(id)
	}
	t.Cleanup(func() {
		for _, id := range []string{source.ID, target.ID} {
			reg.UnregisterClient(id)
		}
	})

	service.registerModelsForAuth(source)

	got := reg.GetModelsForClient(target.ID)
	if len(got) != 1 {
		t.Fatalf("target models = %d, want 1", len(got))
	}
	if got[0] == nil || got[0].ID != "team/gpt-5" {
		t.Fatalf("target model = %+v, want ID team/gpt-5", got[0])
	}
}

func rewriteAuggieDefaultTransport(t *testing.T, targetURL string) {
	t.Helper()

	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	previous := http.DefaultTransport
	base := previous
	http.DefaultTransport = serviceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		return base.RoundTrip(clone)
	})
	t.Cleanup(func() {
		http.DefaultTransport = previous
	})
}

type serviceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f serviceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mustMarshalAuggieBackfillJSON(t *testing.T, value any) string {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(body)
}
