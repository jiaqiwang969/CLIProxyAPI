package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestAuggieManualPayloadRejectsInvalidTenantHost(t *testing.T) {
	_, err := parseAuggieManualPayload(`{"code":"abc","state":"s","tenant_url":"https://evil.example.com"}`)
	if err == nil || !strings.Contains(err.Error(), ".augmentcode.com") {
		t.Fatalf("expected augment tenant validation error, got %v", err)
	}
}

func TestAuggieRefreshLeadIsNil(t *testing.T) {
	if lead := NewAuggieAuthenticator().RefreshLead(); lead != nil {
		t.Fatalf("expected nil refresh lead, got %v", lead)
	}
}

func TestAuggieLoginManualJSONPasteStoresExpectedMetadata(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Fatalf("token path = %q, want /token", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("token method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type = %q", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse query: %v", err)
		}

		if got := values.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := values.Get("client_id"); got != auggieManualPasteClientID {
			t.Fatalf("client_id = %q, want %q", got, auggieManualPasteClientID)
		}
		if got := values.Get("redirect_uri"); got != "" {
			t.Fatalf("redirect_uri = %q, want empty", got)
		}
		if got := values.Get("code_verifier"); got != "verifier-1" {
			t.Fatalf("code_verifier = %q, want verifier-1", got)
		}
		if got := values.Get("code"); got != "code-1" {
			t.Fatalf("code = %q, want code-1", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token-1"}`))
	}))
	defer tokenServer.Close()

	restore := stubAuggieLoginTestHooks(t, fixedAuggieTestTime(), newAuggieRewriteClient(t, tokenServer.URL))
	defer restore()

	auth, err := NewAuggieAuthenticator().Login(context.Background(), &config.Config{}, &LoginOptions{
		NoBrowser: true,
		Prompt: func(prompt string) (string, error) {
			return `{"code":"code-1","state":"state-1","tenant_url":"https://tenant.augmentcode.com"}`, nil
		},
	})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if got := auth.Provider; got != "auggie" {
		t.Fatalf("provider = %q, want auggie", got)
	}
	if got := auth.Label; got != "tenant.augmentcode.com" {
		t.Fatalf("label = %q, want tenant.augmentcode.com", got)
	}
	if got := auth.FileName; got != "auggie-tenant-augmentcode-com.json" {
		t.Fatalf("file name = %q, want auggie-tenant-augmentcode-com.json", got)
	}

	if got := auth.Metadata["type"]; got != "auggie" {
		t.Fatalf("type = %v, want auggie", got)
	}
	if got := auth.Metadata["label"]; got != "tenant.augmentcode.com" {
		t.Fatalf("label = %v, want tenant.augmentcode.com", got)
	}
	if got := auth.Metadata["access_token"]; got != "token-1" {
		t.Fatalf("access_token = %v, want token-1", got)
	}
	if got := auth.Metadata["tenant_url"]; got != "https://tenant.augmentcode.com/" {
		t.Fatalf("tenant_url = %v, want https://tenant.augmentcode.com/", got)
	}
	if got := auth.Metadata["client_id"]; got != auggieManualPasteClientID {
		t.Fatalf("client_id = %v, want %q", got, auggieManualPasteClientID)
	}
	if got := auth.Metadata["login_mode"]; got != "manual_json_paste" {
		t.Fatalf("login_mode = %v, want manual_json_paste", got)
	}
	if got := auth.Metadata["last_refresh"]; got != fixedAuggieTestTime().Format(time.RFC3339) {
		t.Fatalf("last_refresh = %v", got)
	}
	if got := auth.Metadata["default_model"]; got != nil {
		t.Fatalf("default_model should be absent, got %v", got)
	}

	gotScopes, ok := auth.Metadata["scopes"].([]string)
	if !ok {
		t.Fatalf("scopes type = %T, want []string", auth.Metadata["scopes"])
	}
	if !reflect.DeepEqual(gotScopes, []string{"email"}) {
		t.Fatalf("scopes = %#v, want []string{\"email\"}", gotScopes)
	}
	if len(auth.Metadata) != 8 {
		t.Fatalf("metadata len = %d, want 8", len(auth.Metadata))
	}
}

func TestAuggieLoginManualFallbackPromptUsesManualAuthorizeURL(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token-1"}`))
	}))
	defer tokenServer.Close()

	restore := stubAuggieLoginTestHooks(t, fixedAuggieTestTime(), newAuggieRewriteClient(t, tokenServer.URL))
	defer restore()

	var promptText string
	_, err := NewAuggieAuthenticator().Login(context.Background(), &config.Config{}, &LoginOptions{
		NoBrowser: true,
		Prompt: func(prompt string) (string, error) {
			promptText = prompt
			return `{"code":"code-1","state":"state-1","tenant_url":"https://tenant.augmentcode.com"}`, nil
		},
	})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	if !strings.Contains(promptText, "https://auth.augmentcode.com/authorize?") {
		t.Fatalf("prompt missing manual authorize url: %q", promptText)
	}
	if !strings.Contains(promptText, "client_id="+auggieManualPasteClientID) {
		t.Fatalf("prompt missing manual client id: %q", promptText)
	}
	if strings.Contains(promptText, "redirect_uri=") {
		t.Fatalf("manual prompt should not include redirect_uri: %q", promptText)
	}
}

func TestAuggieLoginRejectsStateMismatch(t *testing.T) {
	restore := stubAuggieLoginTestHooks(t, fixedAuggieTestTime(), http.DefaultClient)
	defer restore()

	_, err := exchangeAuggieCallbackForTest(t, "expected", auggieCallbackPayload{
		Code:      "code-1",
		State:     "wrong",
		TenantURL: "https://tenant.augmentcode.com",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("expected invalid state error, got %v", err)
	}
}

func TestAuggieLoginRejectsEmptyManualCode(t *testing.T) {
	_, err := parseAuggieManualPayload(`{"code":" ","state":"state-1","tenant_url":"https://tenant.augmentcode.com"}`)
	if err == nil || !strings.Contains(err.Error(), "authorization code") {
		t.Fatalf("expected authorization code error, got %v", err)
	}
}

func TestAuggieLoginRejectsEmptyManualState(t *testing.T) {
	_, err := parseAuggieManualPayload(`{"code":"code-1","state":" ","tenant_url":"https://tenant.augmentcode.com"}`)
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("expected state error, got %v", err)
	}
}

func TestAuggieAuthRecordRoundTripPreservesLabel(t *testing.T) {
	store := NewFileTokenStore()
	store.SetBaseDir(t.TempDir())

	auth := newAuggieAuthRecord("https://tenant.augmentcode.com/", auggieManualPasteClientID, auggieManualLoginMode, &auggieTokenResponse{
		AccessToken: "token-1",
		Scopes:      []string{"email"},
	})

	if _, err := store.Save(context.Background(), auth); err != nil {
		t.Fatalf("save auth: %v", err)
	}

	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list auths: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got := entries[0].Label; got != "tenant.augmentcode.com" {
		t.Fatalf("label after reload = %q, want tenant.augmentcode.com", got)
	}
}

func TestAuggieApplySessionPreservesCustomLabel(t *testing.T) {
	updated, err := ApplyAuggieSession(&coreauth.Auth{
		Provider: "auggie",
		Label:    "Team Alpha",
		FileName: "auggie-tenant-augmentcode-com.json",
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "Team Alpha",
			"access_token": "stale-token",
			"tenant_url":   "https://tenant.augmentcode.com/",
		},
	}, &AuggieSession{
		AccessToken: "session-token",
		TenantURL:   "https://tenant.augmentcode.com",
		Scopes:      []string{"email"},
	})
	if err != nil {
		t.Fatalf("apply session failed: %v", err)
	}
	if got := updated.Label; got != "Team Alpha" {
		t.Fatalf("label = %q, want Team Alpha", got)
	}
	if got := updated.Metadata["label"]; got != "Team Alpha" {
		t.Fatalf("metadata label = %v, want Team Alpha", got)
	}
}

func TestAuggieApplySessionClearsFailureState(t *testing.T) {
	restore := stubAuggieLoginTestHooks(t, fixedAuggieTestTime(), http.DefaultClient)
	defer restore()

	updated, err := ApplyAuggieSession(&coreauth.Auth{
		Provider:       "auggie",
		Label:          "tenant.augmentcode.com",
		FileName:       "auggie-tenant-augmentcode-com.json",
		Status:         coreauth.StatusError,
		StatusMessage:  "unauthorized",
		Unavailable:    true,
		LastError:      &coreauth.Error{Code: "unauthorized", Message: "unauthorized", HTTPStatus: http.StatusUnauthorized},
		NextRetryAfter: fixedAuggieTestTime().Add(time.Hour),
		Metadata: map[string]any{
			"type":         "auggie",
			"label":        "tenant.augmentcode.com",
			"access_token": "stale-token",
			"tenant_url":   "https://tenant.augmentcode.com/",
		},
	}, &AuggieSession{
		AccessToken: "session-token",
		TenantURL:   "https://tenant.augmentcode.com",
		Scopes:      []string{"email"},
	})
	if err != nil {
		t.Fatalf("apply session failed: %v", err)
	}
	if got := updated.Status; got != coreauth.StatusActive {
		t.Fatalf("status = %q, want %q", got, coreauth.StatusActive)
	}
	if updated.Unavailable {
		t.Fatal("expected auth to become available again")
	}
	if updated.StatusMessage != "" {
		t.Fatalf("status_message = %q, want empty", updated.StatusMessage)
	}
	if updated.LastError != nil {
		t.Fatalf("last_error = %#v, want nil", updated.LastError)
	}
	if !updated.NextRetryAfter.IsZero() {
		t.Fatalf("next_retry_after = %v, want zero", updated.NextRetryAfter)
	}
	if got := updated.Metadata["access_token"]; got != "session-token" {
		t.Fatalf("access_token = %v, want session-token", got)
	}
}

func exchangeAuggieCallbackForTest(t *testing.T, expectedState string, payload auggieCallbackPayload) (*auggieTokenResponse, error) {
	t.Helper()

	return exchangeAuggieCallback(context.Background(), &config.Config{}, expectedState, "verifier-1", "", auggieManualPasteClientID, &payload)
}

func fixedAuggieTestTime() time.Time {
	return time.Date(2026, time.March, 7, 12, 0, 0, 0, time.UTC)
}

func stubAuggieLoginTestHooks(t *testing.T, now time.Time, client *http.Client) func() {
	t.Helper()

	prevManualPromptDelay := auggieManualPromptDelay
	prevNow := auggieNow
	prevNewState := auggieNewState
	prevNewPKCE := auggieNewPKCE
	prevNewHTTPClient := auggieNewHTTPClient

	auggieManualPromptDelay = 0
	auggieNow = func() time.Time {
		return now
	}
	auggieNewState = func() (string, error) {
		return "state-1", nil
	}
	auggieNewPKCE = func() (string, string, error) {
		return "verifier-1", "challenge-1", nil
	}
	auggieNewHTTPClient = func(cfg *config.Config) *http.Client {
		return client
	}

	return func() {
		auggieManualPromptDelay = prevManualPromptDelay
		auggieNow = prevNow
		auggieNewState = prevNewState
		auggieNewPKCE = prevNewPKCE
		auggieNewHTTPClient = prevNewHTTPClient
	}
}

func newAuggieRewriteClient(t *testing.T, targetURL string) *http.Client {
	t.Helper()

	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	base := http.DefaultTransport
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			return base.RoundTrip(clone)
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
