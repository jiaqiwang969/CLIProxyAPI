package auth

import (
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestRefreshRegistryKeepsOnlyTargetProviders(t *testing.T) {
	testCases := []struct {
		provider string
		wantLead *time.Duration
	}{
		{provider: "antigravity", wantLead: durationPtr(5 * time.Minute)},
		{provider: "auggie", wantLead: nil},
		{provider: "claude", wantLead: nil},
		{provider: "codex", wantLead: nil},
		{provider: "gemini", wantLead: nil},
		{provider: "gemini-cli", wantLead: nil},
		{provider: "iflow", wantLead: nil},
		{provider: "kimi", wantLead: nil},
		{provider: "qwen", wantLead: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.provider, func(t *testing.T) {
			got := cliproxyauth.ProviderRefreshLead(tc.provider, nil)
			if !equalDurationPtr(got, tc.wantLead) {
				t.Fatalf("ProviderRefreshLead(%q) = %v, want %v", tc.provider, formatDurationPtr(got), formatDurationPtr(tc.wantLead))
			}
		})
	}
}

func durationPtr(v time.Duration) *time.Duration {
	return &v
}

func equalDurationPtr(a, b *time.Duration) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func formatDurationPtr(v *time.Duration) any {
	if v == nil {
		return nil
	}
	return *v
}
