package configaccess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestProviderAuthenticate_EmitsScopedMetadataForStructuredClientKey(t *testing.T) {
	t.Parallel()

	cfg := &sdkconfig.SDKConfig{
		APIKeys: []string{"legacy-key"},
		ClientAPIKeys: []sdkconfig.ClientAPIKey{
			{
				Key:  "scoped-key",
				Note: "primary auggie account",
				Scope: sdkconfig.ClientAPIKeyScope{
					Provider: "auggie",
					AuthID:   "auggie-main",
					Models:   []string{"gpt-5-4", "claude-sonnet-4-6"},
				},
			},
		},
	}

	Register(cfg)
	t.Cleanup(func() {
		Register(nil)
	})

	p := newProvider("config-inline", cfg.EffectiveClientAPIKeys())
	req := httptest.NewRequest(http.MethodGet, "/v1/models?auth_token=scoped-key", nil)

	result, authErr := p.Authenticate(context.Background(), req)
	if authErr != nil {
		t.Fatalf("Authenticate() authErr = %v", authErr)
	}
	if result == nil {
		t.Fatal("Authenticate() result is nil")
	}

	wantMetadata := map[string]string{
		"source":         "query-auth-token",
		"note":           "primary auggie account",
		"scope_provider": "auggie",
		"scope_auth_id":  "auggie-main",
		"scope_models":   "gpt-5-4,claude-sonnet-4-6",
	}
	if !reflect.DeepEqual(result.Metadata, wantMetadata) {
		t.Fatalf("Authenticate() metadata = %#v, want %#v", result.Metadata, wantMetadata)
	}
}
