package cliproxy

import (
	"context"
	"strings"
	"testing"
)

func TestNewDefaultAuthManagerRegistersOnlyTargetProviders(t *testing.T) {
	manager := newDefaultAuthManager()

	targetProviders := []string{"antigravity", "auggie"}
	for _, provider := range targetProviders {
		_, _, err := manager.Login(context.Background(), provider, nil, nil)
		if err == nil || strings.Contains(err.Error(), "not registered") {
			t.Fatalf("provider %q should be registered, got err=%v", provider, err)
		}
	}

	removedProviders := []string{"claude", "codex", "gemini", "qwen"}
	for _, provider := range removedProviders {
		_, _, err := manager.Login(context.Background(), provider, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("provider %q should be removed, got err=%v", provider, err)
		}
	}
}
