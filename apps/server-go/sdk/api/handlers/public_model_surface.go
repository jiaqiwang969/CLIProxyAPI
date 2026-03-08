package handlers

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
)

type PublicModelSurfaceCapabilities struct {
	InputID              string
	CanonicalID          string
	Available            bool
	SupportsOpenAI       bool
	SupportsClaudeNative bool
}

func ResolvePublicModelSurface(modelID string) PublicModelSurfaceCapabilities {
	modelID = strings.TrimSpace(modelID)
	caps := PublicModelSurfaceCapabilities{InputID: modelID}
	if modelID == "" {
		return caps
	}

	info := registry.LookupModelInfoByAlias(modelID)
	if info == nil {
		return caps
	}

	caps.CanonicalID = canonicalPublicModelID(info, modelID)
	familyKey := strings.ToLower(strings.TrimSpace(caps.CanonicalID))
	if familyKey == "" {
		familyKey = strings.ToLower(strings.TrimSpace(info.ID))
	}

	switch {
	case isClaudePublicModelFamily(familyKey, info):
		caps.SupportsOpenAI = true
		caps.SupportsClaudeNative = true
	case isOpenAIPublicModelFamily(familyKey, info):
		caps.SupportsOpenAI = true
	}

	reg := registry.GetGlobalRegistry()
	if len(reg.GetModelProviders(modelID)) > 0 || len(reg.GetModelProvidersByAlias(modelID)) > 0 {
		caps.Available = true
	}

	return caps
}

func canonicalPublicModelID(info *registry.ModelInfo, fallback string) string {
	candidates := make([]string, 0, 3)
	if info != nil {
		if strings.EqualFold(strings.TrimSpace(info.OwnedBy), "auggie") || strings.EqualFold(strings.TrimSpace(info.Type), "auggie") {
			if version := strings.TrimSpace(info.Version); version != "" {
				candidates = append(candidates, version)
			}
		}
		if id := strings.TrimSpace(info.ID); id != "" {
			candidates = append(candidates, id)
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		candidates = append(candidates, fallback)
	}

	for _, candidate := range candidates {
		if canonical, ok := normalizeKnownPublicModelID(candidate); ok {
			return canonical
		}
	}

	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func isClaudePublicModelFamily(familyKey string, info *registry.ModelInfo) bool {
	if familyKey == "" {
		return false
	}
	if strings.HasPrefix(familyKey, "gemini-") || strings.Contains(familyKey, "-thinking") {
		return false
	}
	if strings.HasPrefix(familyKey, "claude-") {
		return true
	}

	ownedBy := strings.ToLower(strings.TrimSpace(info.OwnedBy))
	kind := strings.ToLower(strings.TrimSpace(info.Type))
	return ownedBy == "anthropic" || kind == "claude"
}

func isOpenAIPublicModelFamily(familyKey string, info *registry.ModelInfo) bool {
	if familyKey == "" {
		return false
	}
	if strings.HasPrefix(familyKey, "gpt-5") {
		return true
	}

	ownedBy := strings.ToLower(strings.TrimSpace(info.OwnedBy))
	kind := strings.ToLower(strings.TrimSpace(info.Type))
	return ownedBy == "openai" || kind == "openai"
}

func normalizeKnownPublicModelID(modelID string) (string, bool) {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return "", false
	}

	switch {
	case modelID == "gpt-5", modelID == "gpt-5-1", modelID == "gpt-5-2", modelID == "gpt-5-4":
		return modelID, true
	case modelID == "claude-haiku-4-5" || strings.HasPrefix(modelID, "claude-haiku-4-5-"):
		return "claude-haiku-4-5", true
	case modelID == "claude-opus-4-5" || strings.HasPrefix(modelID, "claude-opus-4-5-"):
		return "claude-opus-4-5", true
	case modelID == "claude-opus-4-6" || strings.HasPrefix(modelID, "claude-opus-4-6-"):
		return "claude-opus-4-6", true
	case modelID == "claude-sonnet-4":
		return "claude-sonnet-4", true
	case modelID == "claude-sonnet-4-20250514":
		return "claude-sonnet-4", true
	case modelID == "claude-sonnet-4-5" || strings.HasPrefix(modelID, "claude-sonnet-4-5-"):
		return "claude-sonnet-4-5", true
	case modelID == "claude-sonnet-4-6" || strings.HasPrefix(modelID, "claude-sonnet-4-6-"):
		return "claude-sonnet-4-6", true
	case modelID == "claude-3-5-haiku-20241022":
		return modelID, true
	default:
		return "", false
	}
}
