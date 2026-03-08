package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigOptional_AddsDefaultAntigravityAliasesInMemoryWhenKeyMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.yaml")
	const rawConfig = "host: 127.0.0.1\nport: 8317\n"
	if err := os.WriteFile(configFile, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configFile, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	aliases := cfg.OAuthModelAlias["antigravity"]
	if len(aliases) == 0 {
		t.Fatalf("expected default antigravity aliases to be loaded in memory")
	}

	found := false
	for _, entry := range aliases {
		if entry.Name == "gemini-3.1-pro-high" && entry.Alias == "gemini-3.1-pro-preview" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected gemini-3.1-pro-preview alias in defaults, got %#v", aliases)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "oauth-model-alias") {
		t.Fatalf("expected config file to remain unchanged, got %q", string(data))
	}
}

func TestLoadConfigOptional_RespectsExplicitEmptyOAuthModelAlias(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.yaml")
	const rawConfig = "host: 127.0.0.1\nport: 8317\noauth-model-alias: {}\n"
	if err := os.WriteFile(configFile, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configFile, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if len(cfg.OAuthModelAlias) != 0 {
		t.Fatalf("expected explicit empty oauth-model-alias to stay empty, got %#v", cfg.OAuthModelAlias)
	}
}
