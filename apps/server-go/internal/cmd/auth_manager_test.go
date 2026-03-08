package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestNewAuthManagerRegistersOnlyTargetProviders(t *testing.T) {
	manager := newAuthManager()

	targetProviders := []string{"antigravity", "auggie"}
	for _, provider := range targetProviders {
		_, _, err := manager.Login(context.Background(), provider, nil, nil)
		if err == nil || strings.Contains(err.Error(), "not registered") {
			t.Fatalf("provider %q should be registered, got err=%v", provider, err)
		}
	}

	removedProviders := []string{"claude", "codex", "gemini", "iflow", "kimi", "qwen"}
	for _, provider := range removedProviders {
		_, _, err := manager.Login(context.Background(), provider, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("provider %q should be removed, got err=%v", provider, err)
		}
	}
}
