package registry

import "testing"

func TestGetPublicGeminiModels_ReturnsOfficialCatalogWithoutAvailabilityFiltering(t *testing.T) {
	_ = GetGlobalRegistry()
	r := newTestModelRegistry()
	oldGlobal := globalRegistry
	globalRegistry = r
	t.Cleanup(func() {
		globalRegistry = oldGlobal
	})

	r.RegisterClient("antigravity-1", "antigravity", []*ModelInfo{
		{
			ID:          "gemini-3.1-pro-preview",
			Name:        "models/gemini-3.1-pro-preview",
			DisplayName: "Gemini 3.1 Pro Preview",
			Description: "Gemini 3.1 Pro Preview",
			Type:        "antigravity",
			OwnedBy:     "antigravity",
		},
	})
	r.RegisterClient("codex-1", "codex", []*ModelInfo{
		{
			ID:          "gpt-5-4",
			Name:        "models/gpt-5-4",
			DisplayName: "GPT-5.4",
			Description: "GPT-5.4",
			Type:        "codex",
			OwnedBy:     "codex",
		},
	})

	models := GetPublicGeminiModels()
	if len(models) == 0 {
		t.Fatalf("expected non-empty public Gemini catalog")
	}
	if !hasPublicGeminiModel(models, "gemini-3.1-pro-preview") {
		t.Fatalf("expected model %q in public Gemini catalog", "gemini-3.1-pro-preview")
	}
	if !hasPublicGeminiModel(models, "gemini-2.5-pro") {
		t.Fatalf("expected model %q in public Gemini catalog", "gemini-2.5-pro")
	}
}

func TestGetPublicGeminiModels_ExcludesPrivateProviderOnlyModels(t *testing.T) {
	_ = GetGlobalRegistry()
	r := newTestModelRegistry()
	oldGlobal := globalRegistry
	globalRegistry = r
	t.Cleanup(func() {
		globalRegistry = oldGlobal
	})

	r.RegisterClient("antigravity-1", "antigravity", []*ModelInfo{
		{
			ID:          "gemini-3-flash-preview",
			Name:        "models/gemini-3-flash-preview",
			DisplayName: "Gemini 3 Flash Preview",
			Description: "Gemini 3 Flash Preview",
			Type:        "antigravity",
			OwnedBy:     "antigravity",
		},
	})

	models := GetPublicGeminiModels()
	if !hasPublicGeminiModel(models, "gemini-3-flash-preview") {
		t.Fatalf("expected public model %q in public Gemini catalog", "gemini-3-flash-preview")
	}
	if hasPublicGeminiModel(models, "gemini-3.1-pro-high") {
		t.Fatalf("did not expect private provider model %q in public Gemini catalog", "gemini-3.1-pro-high")
	}
}

func hasPublicGeminiModel(models []*ModelInfo, id string) bool {
	for _, model := range models {
		if model != nil && model.ID == id {
			return true
		}
	}
	return false
}
