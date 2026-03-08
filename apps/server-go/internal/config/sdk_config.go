// Package config provides configuration management for the CLI Proxy API server.
// It handles loading and parsing YAML configuration files, and provides structured
// access to application settings including server port, authentication directory,
// debug settings, proxy configuration, and API keys.
package config

// SDKConfig represents the application's configuration, loaded from a YAML file.
type SDKConfig struct {
	// ProxyURL is the URL of an optional proxy server to use for outbound requests.
	ProxyURL string `yaml:"proxy-url" json:"proxy-url"`

	// ForceModelPrefix requires explicit model prefixes (e.g., "teamA/gemini-3-pro-preview")
	// to target prefixed credentials. When false, unprefixed model requests may use prefixed
	// credentials as well.
	ForceModelPrefix bool `yaml:"force-model-prefix" json:"force-model-prefix"`

	// RequestLog enables or disables detailed request logging functionality.
	RequestLog bool `yaml:"request-log" json:"request-log"`

	// APIKeys is a list of keys for authenticating clients to this proxy server.
	APIKeys []string `yaml:"api-keys" json:"api-keys"`

	// ClientAPIKeys defines structured client keys that can optionally pin access to a provider/auth pair.
	ClientAPIKeys []ClientAPIKey `yaml:"client-api-keys,omitempty" json:"client-api-keys,omitempty"`

	// PassthroughHeaders controls whether upstream response headers are forwarded to downstream clients.
	// Default is false (disabled).
	PassthroughHeaders bool `yaml:"passthrough-headers" json:"passthrough-headers"`

	// Streaming configures server-side streaming behavior (keep-alives and safe bootstrap retries).
	Streaming StreamingConfig `yaml:"streaming" json:"streaming"`

	// NonStreamKeepAliveInterval controls how often blank lines are emitted for non-streaming responses.
	// <= 0 disables keep-alives. Value is in seconds.
	NonStreamKeepAliveInterval int `yaml:"nonstream-keepalive-interval,omitempty" json:"nonstream-keepalive-interval,omitempty"`
}

// ClientAPIKey describes a proxy client key managed by the application.
type ClientAPIKey struct {
	Key     string            `yaml:"key" json:"key"`
	Enabled *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Note    string            `yaml:"note,omitempty" json:"note,omitempty"`
	Scope   ClientAPIKeyScope `yaml:"scope,omitempty" json:"scope,omitempty"`
}

// ClientAPIKeyScope restricts a client key to a provider/auth pair and optional model allowlist.
type ClientAPIKeyScope struct {
	Provider string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	AuthID   string   `yaml:"auth_id,omitempty" json:"auth_id,omitempty"`
	Models   []string `yaml:"models,omitempty" json:"models,omitempty"`
}

// EffectiveClientAPIKeys returns the deduplicated key set used for request authentication.
// Structured client-api-keys override legacy top-level api-keys when the same key appears in both.
func (cfg *SDKConfig) EffectiveClientAPIKeys() []ClientAPIKey {
	if cfg == nil {
		return nil
	}

	legacy := normalizeLegacyAPIKeys(cfg.APIKeys)
	structured := normalizeClientAPIKeys(cfg.ClientAPIKeys, false)
	if len(legacy) == 0 && len(structured) == 0 {
		return nil
	}

	result := make([]ClientAPIKey, 0, len(legacy)+len(structured))
	indexByKey := make(map[string]int, len(legacy)+len(structured))
	for _, key := range legacy {
		entry := ClientAPIKey{Key: key}
		indexByKey[key] = len(result)
		result = append(result, entry)
	}
	for _, entry := range structured {
		if idx, exists := indexByKey[entry.Key]; exists {
			result[idx] = entry
			continue
		}
		indexByKey[entry.Key] = len(result)
		result = append(result, entry)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// SanitizeClientAPIKeys normalizes legacy and structured client key configuration in-place.
func (cfg *SDKConfig) SanitizeClientAPIKeys() {
	if cfg == nil {
		return
	}
	cfg.APIKeys = normalizeLegacyAPIKeys(cfg.APIKeys)
	cfg.ClientAPIKeys = normalizeClientAPIKeys(cfg.ClientAPIKeys, true)
}

func (entry ClientAPIKey) isEnabled() bool {
	return entry.Enabled == nil || *entry.Enabled
}

func normalizeLegacyAPIKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	result := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, raw := range keys {
		key := raw
		if trimmed := trimASCIIWhitespace(raw); trimmed != "" {
			key = trimmed
		} else {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeClientAPIKeys(entries []ClientAPIKey, includeDisabled bool) []ClientAPIKey {
	if len(entries) == 0 {
		return nil
	}
	result := make([]ClientAPIKey, 0, len(entries))
	indexByKey := make(map[string]int, len(entries))
	for _, raw := range entries {
		entry := raw
		entry.Key = trimASCIIWhitespace(entry.Key)
		entry.Note = trimASCIIWhitespace(entry.Note)
		entry.Scope.Provider = trimASCIIWhitespace(entry.Scope.Provider)
		entry.Scope.AuthID = trimASCIIWhitespace(entry.Scope.AuthID)
		entry.Scope.Models = normalizeLegacyAPIKeys(entry.Scope.Models)
		if entry.Key == "" {
			continue
		}
		if !includeDisabled && !entry.isEnabled() {
			continue
		}
		if idx, exists := indexByKey[entry.Key]; exists {
			result[idx] = entry
			continue
		}
		indexByKey[entry.Key] = len(result)
		result = append(result, entry)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func trimASCIIWhitespace(value string) string {
	start := 0
	end := len(value)
	for start < end {
		switch value[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			goto trimEnd
		}
	}
trimEnd:
	for start < end {
		switch value[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
		default:
			return value[start:end]
		}
	}
	return ""
}

// StreamingConfig holds server streaming behavior configuration.
type StreamingConfig struct {
	// KeepAliveSeconds controls how often the server emits SSE heartbeats (": keep-alive\n\n").
	// <= 0 disables keep-alives. Default is 0.
	KeepAliveSeconds int `yaml:"keepalive-seconds,omitempty" json:"keepalive-seconds,omitempty"`

	// BootstrapRetries controls how many times the server may retry a streaming request before any bytes are sent,
	// to allow auth rotation / transient recovery.
	// <= 0 disables bootstrap retries. Default is 0.
	BootstrapRetries int `yaml:"bootstrap-retries,omitempty" json:"bootstrap-retries,omitempty"`
}
