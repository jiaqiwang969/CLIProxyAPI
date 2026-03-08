package test

import (
	"fmt"
	"testing"
	"time"

	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/thinking/provider/antigravity"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

type antigravityThinkingCase struct {
	name            string
	from            string
	model           string
	inputJSON       string
	expectField     string
	expectValue     string
	includeThoughts string
}

func TestThinkingAntigravitySuffixMatrix(t *testing.T) {
	registerThinkingModels(t)

	runAntigravityThinkingCases(t, []antigravityThinkingCase{
		{
			name:      "gemini_passthrough_without_suffix",
			from:      "gemini",
			model:     "antigravity-budget-model",
			inputJSON: `{"model":"antigravity-budget-model","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
		},
		{
			name:            "gemini_medium_suffix_maps_to_budget",
			from:            "gemini",
			model:           "antigravity-budget-model(medium)",
			inputJSON:       `{"model":"antigravity-budget-model(medium)","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "8192",
			includeThoughts: "true",
		},
		{
			name:            "gemini_none_suffix_disables_thinking",
			from:            "gemini",
			model:           "antigravity-budget-model(none)",
			inputJSON:       `{"model":"antigravity-budget-model(none)","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "0",
			includeThoughts: "false",
		},
		{
			name:            "claude_large_budget_suffix_is_clamped",
			from:            "claude",
			model:           "antigravity-budget-model(64000)",
			inputJSON:       `{"model":"antigravity-budget-model(64000)","messages":[{"role":"user","content":"hi"}]}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "20000",
			includeThoughts: "true",
		},
		{
			name:            "gemini_cli_budget_suffix_passthrough",
			from:            "gemini-cli",
			model:           "antigravity-budget-model(8192)",
			inputJSON:       `{"model":"antigravity-budget-model(8192)","request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "8192",
			includeThoughts: "true",
		},
	})
}

func TestThinkingAntigravityBodyMatrix(t *testing.T) {
	registerThinkingModels(t)

	runAntigravityThinkingCases(t, []antigravityThinkingCase{
		{
			name:            "gemini_body_level_is_converted_to_budget",
			from:            "gemini",
			model:           "antigravity-budget-model",
			inputJSON:       `{"model":"antigravity-budget-model","contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"medium"}}}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "8192",
			includeThoughts: "true",
		},
		{
			name:            "gemini_body_none_becomes_zero_budget",
			from:            "gemini",
			model:           "antigravity-budget-model",
			inputJSON:       `{"model":"antigravity-budget-model","contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"none"}}}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "0",
			includeThoughts: "false",
		},
		{
			name:            "claude_body_budget_is_clamped",
			from:            "claude",
			model:           "antigravity-budget-model",
			inputJSON:       `{"model":"antigravity-budget-model","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":64000}}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "20000",
			includeThoughts: "true",
		},
		{
			name:            "claude_body_adaptive_uses_max_budget",
			from:            "claude",
			model:           "antigravity-budget-model",
			inputJSON:       `{"model":"antigravity-budget-model","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"}}`,
			expectField:     "request.generationConfig.thinkingConfig.thinkingBudget",
			expectValue:     "20000",
			includeThoughts: "true",
		},
	})
}

func registerThinkingModels(t *testing.T) {
	t.Helper()

	reg := registry.GetGlobalRegistry()
	uid := fmt.Sprintf("thinking-antigravity-%d", time.Now().UnixNano())
	reg.RegisterClient(uid, "test", []*registry.ModelInfo{
		{
			ID:          "antigravity-budget-model",
			Object:      "model",
			Created:     1700000000,
			OwnedBy:     "test",
			Type:        "gemini-cli",
			DisplayName: "Antigravity Budget Model",
			Thinking:    &registry.ThinkingSupport{Min: 128, Max: 20000, ZeroAllowed: true, DynamicAllowed: true},
		},
	})
	t.Cleanup(func() {
		reg.UnregisterClient(uid)
	})
}

func runAntigravityThinkingCases(t *testing.T, cases []antigravityThinkingCase) {
	t.Helper()

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baseModel := thinking.ParseSuffix(tc.model).ModelName
			body := sdktranslator.TranslateRequest(
				sdktranslator.FromString(tc.from),
				sdktranslator.FormatAntigravity,
				baseModel,
				[]byte(tc.inputJSON),
				true,
			)

			body, err := thinking.ApplyThinking(body, tc.model, tc.from, "antigravity", "antigravity")
			if err != nil {
				t.Fatalf("ApplyThinking error: %v, body=%s", err, string(body))
			}

			if tc.expectField == "" {
				if gjson.GetBytes(body, "request.generationConfig.thinkingConfig").Exists() {
					t.Fatalf("expected no thinkingConfig, got %s", string(body))
				}
				return
			}

			got := gjson.GetBytes(body, tc.expectField)
			if !got.Exists() {
				t.Fatalf("expected field %s, body=%s", tc.expectField, string(body))
			}

			actualValue := got.String()
			if got.Type == gjson.Number {
				actualValue = fmt.Sprintf("%d", got.Int())
			}
			if actualValue != tc.expectValue {
				t.Fatalf("field %s = %q, want %q, body=%s", tc.expectField, actualValue, tc.expectValue, string(body))
			}

			if tc.includeThoughts == "" {
				return
			}

			includeThoughts := gjson.GetBytes(body, "request.generationConfig.thinkingConfig.includeThoughts")
			if !includeThoughts.Exists() {
				t.Fatalf("expected includeThoughts, body=%s", string(body))
			}
			if actual := fmt.Sprintf("%v", includeThoughts.Bool()); actual != tc.includeThoughts {
				t.Fatalf("includeThoughts = %s, want %s, body=%s", actual, tc.includeThoughts, string(body))
			}
		})
	}
}
