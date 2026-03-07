package auth

import (
	"strings"
	"testing"
)

func TestAuggieManualPayloadRejectsInvalidTenantHost(t *testing.T) {
	t.Parallel()

	_, err := parseAuggieManualPayload(`{"code":"abc","state":"s","tenant_url":"https://evil.example.com"}`)
	if err == nil || !strings.Contains(err.Error(), ".augmentcode.com") {
		t.Fatalf("expected augment tenant validation error, got %v", err)
	}
}

func TestAuggieRefreshLeadIsNil(t *testing.T) {
	t.Parallel()

	if lead := NewAuggieAuthenticator().RefreshLead(); lead != nil {
		t.Fatalf("expected nil refresh lead, got %v", lead)
	}
}
