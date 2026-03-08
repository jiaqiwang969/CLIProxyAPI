package translator

import (
	"testing"

	translatorregistry "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/translator"
)

func TestTargetProviderTranslatorsRemainRegistered(t *testing.T) {
	testCases := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "openai_to_auggie", from: "openai", to: "auggie", want: true},
		{name: "openai_to_antigravity", from: "openai", to: "antigravity", want: true},
		{name: "openai_response_to_antigravity", from: "openai-response", to: "antigravity", want: true},
		{name: "gemini_to_antigravity", from: "gemini", to: "antigravity", want: true},
		{name: "claude_to_antigravity", from: "claude", to: "antigravity", want: true},
		{name: "claude_to_openai_bridge", from: "claude", to: "openai", want: true},
		{name: "openai_response_to_openai_bridge", from: "openai-response", to: "openai", want: true},
		{name: "openai_to_gemini_removed", from: "openai", to: "gemini", want: false},
		{name: "openai_to_gemini_cli_removed", from: "openai", to: "gemini-cli", want: false},
		{name: "openai_to_codex_removed", from: "openai", to: "codex", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := translatorregistry.NeedConvert(tc.from, tc.to); got != tc.want {
				t.Fatalf("NeedConvert(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}
