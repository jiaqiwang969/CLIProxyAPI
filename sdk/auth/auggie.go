package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// AuggieAuthenticator is the auth implementation for the "auggie" provider.
//
// Task 1 keeps this intentionally minimal: it only wires refresh lead support
// and provides helpers for parsing the manual callback JSON payload.
type AuggieAuthenticator struct{}

func NewAuggieAuthenticator() Authenticator { return &AuggieAuthenticator{} }

func (AuggieAuthenticator) Provider() string { return "auggie" }

// RefreshLead returns nil to disable proactive refresh; Auggie v1 uses revalidation.
func (AuggieAuthenticator) RefreshLead() *time.Duration { return nil }

func (AuggieAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	return nil, fmt.Errorf("auggie: login not implemented")
}

type auggieCallbackPayload struct {
	Code      string `json:"code"`
	State     string `json:"state"`
	TenantURL string `json:"tenant_url"`
}

func parseAuggieManualPayload(raw string) (*auggieCallbackPayload, error) {
	var payload auggieCallbackPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	if err := validateAuggieTenantURL(payload.TenantURL); err != nil {
		return nil, err
	}
	return &payload, nil
}

func validateAuggieTenantURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("auggie: tenant URL is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		parsed, err = url.Parse("https://" + raw)
	}
	if err != nil {
		return fmt.Errorf("auggie: invalid tenant URL: %w", err)
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if !strings.HasSuffix(host, ".augmentcode.com") {
		return fmt.Errorf("auggie: tenant host must end with .augmentcode.com")
	}

	return nil
}
