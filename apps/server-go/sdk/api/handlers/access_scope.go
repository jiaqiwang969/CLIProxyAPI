package handlers

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
)

const (
	AccessScopeProviderContextKey = "accessScopeProvider"
	AccessScopeAuthIDContextKey   = "accessScopeAuthID"
	AccessScopeModelsContextKey   = "accessScopeModels"
	AccessKeyNoteContextKey       = "accessKeyNote"
)

type AccessScope struct {
	Provider string
	AuthID   string
	Note     string
	Models   []string
}

func AccessScopeFromContext(ctx context.Context) AccessScope {
	if ctx == nil {
		return AccessScope{}
	}
	if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil {
		return AccessScopeFromGin(ginCtx)
	}
	return AccessScope{}
}

func AccessScopeFromGin(c *gin.Context) AccessScope {
	if c == nil {
		return AccessScope{}
	}

	scope := AccessScope{
		Provider: strings.TrimSpace(getStringContextValue(c, AccessScopeProviderContextKey)),
		AuthID:   strings.TrimSpace(getStringContextValue(c, AccessScopeAuthIDContextKey)),
		Note:     strings.TrimSpace(getStringContextValue(c, AccessKeyNoteContextKey)),
	}
	scope.Models = normalizeScopeModelList(getStringContextValue(c, AccessScopeModelsContextKey))
	return scope
}

func ModelVisibleForRequest(c *gin.Context, modelID string) bool {
	return modelVisibleForScope(AccessScopeFromGin(c), modelID)
}

func modelVisibleForScope(scope AccessScope, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}

	if len(scope.Models) > 0 && !modelAllowedByExplicitScope(modelID, scope.Models) {
		return false
	}

	reg := registry.GetGlobalRegistry()
	if scope.AuthID != "" && !reg.ClientSupportsModel(scope.AuthID, modelID) {
		return false
	}

	if scope.Provider != "" {
		providers := reg.GetModelProviders(modelID)
		providers = append(providers, reg.GetModelProvidersByAlias(modelID)...)
		for _, provider := range providers {
			if strings.EqualFold(strings.TrimSpace(provider), scope.Provider) {
				return true
			}
		}
		return scope.AuthID != ""
	}

	return true
}

func modelAllowedByExplicitScope(modelID string, allowed []string) bool {
	modelID = canonicalScopeModelID(modelID)
	for _, candidate := range allowed {
		candidate = canonicalScopeModelID(candidate)
		if candidate == "" {
			continue
		}
		if strings.EqualFold(candidate, modelID) {
			return true
		}
	}
	return false
}

func canonicalScopeModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	if info := registry.LookupModelInfoByAlias(modelID); info != nil {
		if canonical := strings.TrimSpace(info.ID); canonical != "" {
			return canonical
		}
	}
	return modelID
}

func normalizeScopeModelList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		model := strings.TrimSpace(part)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func getStringContextValue(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(key)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}
