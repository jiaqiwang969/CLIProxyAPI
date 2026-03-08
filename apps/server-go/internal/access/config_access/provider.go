package configaccess

import (
	"context"
	"net/http"
	"slices"
	"strings"

	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

// Register ensures the config-access provider is available to the access manager.
func Register(cfg *sdkconfig.SDKConfig) {
	if cfg == nil {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigAPIKey)
		return
	}

	keys := cfg.EffectiveClientAPIKeys()
	if len(keys) == 0 {
		sdkaccess.UnregisterProvider(sdkaccess.AccessProviderTypeConfigAPIKey)
		return
	}

	sdkaccess.RegisterProvider(
		sdkaccess.AccessProviderTypeConfigAPIKey,
		newProvider(sdkaccess.DefaultAccessProviderName, keys),
	)
}

type provider struct {
	name string
	keys map[string]sdkconfig.ClientAPIKey
}

func newProvider(name string, keys []sdkconfig.ClientAPIKey) *provider {
	providerName := strings.TrimSpace(name)
	if providerName == "" {
		providerName = sdkaccess.DefaultAccessProviderName
	}
	keySet := make(map[string]sdkconfig.ClientAPIKey, len(keys))
	for _, entry := range keys {
		key := strings.TrimSpace(entry.Key)
		if key == "" {
			continue
		}
		entry.Key = key
		keySet[key] = entry
	}
	return &provider{name: providerName, keys: keySet}
}

func (p *provider) Identifier() string {
	if p == nil || p.name == "" {
		return sdkaccess.DefaultAccessProviderName
	}
	return p.name
}

func (p *provider) Authenticate(_ context.Context, r *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	if p == nil {
		return nil, sdkaccess.NewNotHandledError()
	}
	if len(p.keys) == 0 {
		return nil, sdkaccess.NewNotHandledError()
	}
	authHeader := r.Header.Get("Authorization")
	authHeaderGoogle := r.Header.Get("X-Goog-Api-Key")
	authHeaderAnthropic := r.Header.Get("X-Api-Key")
	queryKey := ""
	queryAuthToken := ""
	if r.URL != nil {
		queryKey = r.URL.Query().Get("key")
		queryAuthToken = r.URL.Query().Get("auth_token")
	}
	if authHeader == "" && authHeaderGoogle == "" && authHeaderAnthropic == "" && queryKey == "" && queryAuthToken == "" {
		return nil, sdkaccess.NewNoCredentialsError()
	}

	apiKey := extractBearerToken(authHeader)

	candidates := []struct {
		value  string
		source string
	}{
		{apiKey, "authorization"},
		{authHeaderGoogle, "x-goog-api-key"},
		{authHeaderAnthropic, "x-api-key"},
		{queryKey, "query-key"},
		{queryAuthToken, "query-auth-token"},
	}

	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		entry, ok := p.keys[candidate.value]
		if ok {
			metadata := map[string]string{
				"source": candidate.source,
			}
			if note := strings.TrimSpace(entry.Note); note != "" {
				metadata["note"] = note
			}
			if provider := strings.TrimSpace(entry.Scope.Provider); provider != "" {
				metadata["scope_provider"] = provider
			}
			if authID := strings.TrimSpace(entry.Scope.AuthID); authID != "" {
				metadata["scope_auth_id"] = authID
			}
			models := entry.Scope.Models
			if len(models) > 0 {
				models = slices.Clone(models)
				for i := range models {
					models[i] = strings.TrimSpace(models[i])
				}
				metadata["scope_models"] = strings.Join(models, ",")
			}
			return &sdkaccess.Result{
				Provider:  p.Identifier(),
				Principal: entry.Key,
				Metadata:  metadata,
			}, nil
		}
	}

	return nil, sdkaccess.NewInvalidCredentialError()
}

func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return header
	}
	if strings.ToLower(parts[0]) != "bearer" {
		return header
	}
	return strings.TrimSpace(parts[1])
}
