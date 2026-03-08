package config

import (
	"reflect"
	"testing"
)

func TestSDKConfigEffectiveClientAPIKeys_MergesLegacyAndStructuredWithScopedOverride(t *testing.T) {
	t.Parallel()

	cfg := &SDKConfig{
		APIKeys: []string{
			" legacy-shared ",
			"",
			"legacy-only",
		},
		ClientAPIKeys: []ClientAPIKey{
			{
				Key:  " legacy-shared ",
				Note: "scoped version",
				Scope: ClientAPIKeyScope{
					Provider: "auggie",
					AuthID:   "auggie-main",
				},
			},
			{
				Key:     "disabled-key",
				Enabled: boolPtr(false),
			},
			{
				Key: "scoped-only",
				Scope: ClientAPIKeyScope{
					Provider: "antigravity",
					AuthID:   "antigravity-work",
					Models:   []string{" gemini-3.1-pro-preview ", "gemini-3.1-pro-preview", ""},
				},
			},
		},
	}

	got := cfg.EffectiveClientAPIKeys()
	want := []ClientAPIKey{
		{
			Key: "legacy-shared",
			Scope: ClientAPIKeyScope{
				Provider: "auggie",
				AuthID:   "auggie-main",
			},
			Note: "scoped version",
		},
		{
			Key: "legacy-only",
		},
		{
			Key: "scoped-only",
			Scope: ClientAPIKeyScope{
				Provider: "antigravity",
				AuthID:   "antigravity-work",
				Models:   []string{"gemini-3.1-pro-preview"},
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveClientAPIKeys() = %#v, want %#v", got, want)
	}
}

func boolPtr(v bool) *bool { return &v }
