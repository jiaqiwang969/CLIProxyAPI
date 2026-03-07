package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	internalcodex "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	auggieAuthBaseURL         = "https://auth.augmentcode.com"
	auggieLocalClientID       = "auggie-cli"
	auggieManualPasteClientID = "auggie-cli-json-paste"
	auggieLocalLoginMode      = "localhost"
	auggieManualLoginMode     = "manual_json_paste"
	auggieDefaultScope        = "email"
)

var (
	auggieManualPromptDelay = 15 * time.Second
	auggieCallbackTimeout   = 5 * time.Minute
	auggieNow               = func() time.Time { return time.Now().UTC() }
	auggieNewState          = misc.GenerateRandomState
	auggieNewPKCE           = func() (string, string, error) {
		codes, err := internalcodex.GeneratePKCECodes()
		if err != nil {
			return "", "", err
		}
		return codes.CodeVerifier, codes.CodeChallenge, nil
	}
	auggieNewHTTPClient = func(cfg *config.Config) *http.Client {
		if cfg == nil {
			cfg = &config.Config{}
		}
		return util.SetProxy(&cfg.SDKConfig, &http.Client{})
	}
	auggieBrowserAvailable = browser.IsAvailable
	auggieOpenBrowser      = browser.OpenURL
)

// AuggieAuthenticator is the auth implementation for the "auggie" provider.
type AuggieAuthenticator struct{}

func NewAuggieAuthenticator() Authenticator { return &AuggieAuthenticator{} }

func (AuggieAuthenticator) Provider() string { return "auggie" }

// RefreshLead returns nil to disable proactive refresh; Auggie v1 uses revalidation.
func (AuggieAuthenticator) RefreshLead() *time.Duration { return nil }

func (AuggieAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	state, err := auggieNewState()
	if err != nil {
		return nil, fmt.Errorf("auggie: failed to generate state: %w", err)
	}

	codeVerifier, codeChallenge, err := auggieNewPKCE()
	if err != nil {
		return nil, fmt.Errorf("auggie: failed to generate pkce: %w", err)
	}

	srv, port, cbChan, err := startAuggieCallbackServer(opts.CallbackPort)
	if err != nil {
		return nil, fmt.Errorf("auggie: failed to start callback server: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	authURL, err := buildAuggieAuthorizationURL(state, codeChallenge, redirectURI)
	if err != nil {
		return nil, err
	}
	manualAuthURL, err := buildAuggieAuthorizationURL(state, codeChallenge, "")
	if err != nil {
		return nil, err
	}

	if !opts.NoBrowser {
		fmt.Println("Opening browser for Auggie authentication")
		if !auggieBrowserAvailable() {
			log.Warn("No browser available; please open the URL manually")
			util.PrintSSHTunnelInstructions(port)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		} else if err = auggieOpenBrowser(authURL); err != nil {
			log.Warnf("Failed to open browser automatically: %v", err)
			util.PrintSSHTunnelInstructions(port)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		}
	} else {
		util.PrintSSHTunnelInstructions(port)
		fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
	}

	fmt.Println("Waiting for Auggie authentication callback...")

	payload, clientID, loginMode, exchangeRedirectURI, err := waitForAuggieCallback(cbChan, opts, redirectURI, manualAuthURL)
	if err != nil {
		return nil, err
	}

	token, err := exchangeAuggieCallback(ctx, cfg, state, codeVerifier, exchangeRedirectURI, clientID, payload)
	if err != nil {
		return nil, err
	}

	return newAuggieAuthRecord(payload.TenantURL, clientID, loginMode, token), nil
}

type auggieCallbackPayload struct {
	Code             string `json:"code"`
	State            string `json:"state"`
	TenantURL        string `json:"tenant_url"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type auggieTokenResponse struct {
	AccessToken string
	Scopes      []string
}

func parseAuggieManualPayload(raw string) (*auggieCallbackPayload, error) {
	var payload auggieCallbackPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("auggie: invalid manual payload: %w", err)
	}

	payload.Code = strings.TrimSpace(payload.Code)
	payload.State = strings.TrimSpace(payload.State)
	payload.TenantURL = strings.TrimSpace(payload.TenantURL)
	payload.Error = strings.TrimSpace(payload.Error)
	payload.ErrorDescription = strings.TrimSpace(payload.ErrorDescription)

	if payload.State == "" {
		return nil, fmt.Errorf("auggie: missing state")
	}
	if payload.Error != "" {
		return &payload, nil
	}
	if payload.Code == "" {
		return nil, fmt.Errorf("auggie: missing authorization code")
	}

	normalizedTenantURL, err := normalizeAuggieTenantURL(payload.TenantURL)
	if err != nil {
		return nil, err
	}
	payload.TenantURL = normalizedTenantURL
	return &payload, nil
}

func validateAuggieTenantURL(raw string) error {
	_, err := normalizeAuggieTenantURL(raw)
	return err
}

func normalizeAuggieTenantURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("auggie: tenant URL is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		parsed, err = url.Parse("https://" + raw)
	}
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("auggie: invalid tenant URL: %w", err)
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme == "" {
		scheme = "https"
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("auggie: invalid tenant URL scheme: %s", scheme)
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if !strings.HasSuffix(host, ".augmentcode.com") {
		return "", fmt.Errorf("auggie: tenant host must end with .augmentcode.com")
	}

	canonicalHost := host
	if port := strings.TrimSpace(parsed.Port()); port != "" {
		canonicalHost = net.JoinHostPort(host, port)
	}

	return (&url.URL{
		Scheme: scheme,
		Host:   canonicalHost,
		Path:   "/",
	}).String(), nil
}

func buildAuggieAuthorizationURL(state, codeChallenge, redirectURI string) (string, error) {
	base, err := url.Parse(auggieAuthBaseURL)
	if err != nil {
		return "", fmt.Errorf("auggie: invalid auth base URL: %w", err)
	}

	params := url.Values{
		"response_type":  {"code"},
		"code_challenge": {strings.TrimSpace(codeChallenge)},
		"client_id":      {auggieManualPasteClientID},
		"state":          {strings.TrimSpace(state)},
		"prompt":         {"login"},
	}
	if trimmedRedirectURI := strings.TrimSpace(redirectURI); trimmedRedirectURI != "" {
		params.Set("client_id", auggieLocalClientID)
		params.Set("redirect_uri", trimmedRedirectURI)
	}

	return base.ResolveReference(&url.URL{
		Path:     "/authorize",
		RawQuery: params.Encode(),
	}).String(), nil
}

func waitForAuggieCallback(cbChan <-chan auggieCallbackPayload, opts *LoginOptions, redirectURI, manualAuthURL string) (*auggieCallbackPayload, string, string, string, error) {
	timeoutTimer := time.NewTimer(auggieCallbackTimeout)
	defer timeoutTimer.Stop()

	var manualPromptTimer *time.Timer
	var manualPromptC <-chan time.Time
	if opts != nil && opts.Prompt != nil {
		manualPromptTimer = time.NewTimer(auggieManualPromptDelay)
		manualPromptC = manualPromptTimer.C
		defer manualPromptTimer.Stop()
	}

	for {
		select {
		case payload := <-cbChan:
			return &payload, auggieLocalClientID, auggieLocalLoginMode, redirectURI, nil
		case <-manualPromptC:
			manualPromptC = nil
			if manualPromptTimer != nil {
				manualPromptTimer.Stop()
			}
			select {
			case payload := <-cbChan:
				return &payload, auggieLocalClientID, auggieLocalLoginMode, redirectURI, nil
			default:
			}

			prompt := "Paste the Auggie authentication JSON (or press Enter to keep waiting): "
			if strings.TrimSpace(manualAuthURL) != "" {
				prompt = fmt.Sprintf("If localhost callback is unavailable, open this URL in a browser and complete login:\n%s\nPaste the Auggie authentication JSON (or press Enter to keep waiting): ", manualAuthURL)
			}

			input, err := opts.Prompt(prompt)
			if err != nil {
				return nil, "", "", "", err
			}
			if strings.TrimSpace(input) == "" {
				continue
			}

			payload, err := parseAuggieManualPayload(input)
			if err != nil {
				return nil, "", "", "", err
			}
			return payload, auggieManualPasteClientID, auggieManualLoginMode, "", nil
		case <-timeoutTimer.C:
			return nil, "", "", "", fmt.Errorf("auggie: authentication timed out")
		}
	}
}

func exchangeAuggieCallback(ctx context.Context, cfg *config.Config, expectedState, codeVerifier, redirectURI, clientID string, payload *auggieCallbackPayload) (*auggieTokenResponse, error) {
	if payload == nil {
		return nil, fmt.Errorf("auggie: missing callback payload")
	}

	expectedState = strings.TrimSpace(expectedState)
	if expectedState == "" {
		return nil, fmt.Errorf("auggie: missing state")
	}
	payload.State = strings.TrimSpace(payload.State)
	payload.Code = strings.TrimSpace(payload.Code)
	payload.Error = strings.TrimSpace(payload.Error)
	payload.ErrorDescription = strings.TrimSpace(payload.ErrorDescription)
	if payload.State == "" {
		return nil, fmt.Errorf("auggie: missing state")
	}
	if payload.State != expectedState {
		return nil, fmt.Errorf("auggie: invalid state")
	}
	if payload.Error != "" {
		message := payload.Error
		if payload.ErrorDescription != "" {
			message = fmt.Sprintf("%s: %s", payload.Error, payload.ErrorDescription)
		}
		return nil, fmt.Errorf("auggie: authentication failed: %s", message)
	}
	if payload.Code == "" {
		return nil, fmt.Errorf("auggie: missing authorization code")
	}

	normalizedTenantURL, err := normalizeAuggieTenantURL(payload.TenantURL)
	if err != nil {
		return nil, err
	}
	payload.TenantURL = normalizedTenantURL

	tokenURL, err := url.Parse(normalizedTenantURL)
	if err != nil {
		return nil, fmt.Errorf("auggie: invalid tenant URL: %w", err)
	}
	tokenURL = tokenURL.ResolveReference(&url.URL{Path: "token"})

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {strings.TrimSpace(clientID)},
		"code_verifier": {strings.TrimSpace(codeVerifier)},
		"code":          {payload.Code},
	}
	if trimmedRedirectURI := strings.TrimSpace(redirectURI); trimmedRedirectURI != "" {
		form.Set("redirect_uri", trimmedRedirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auggie: failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := auggieNewHTTPClient(cfg)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auggie: token exchange failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auggie: failed to read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auggie: token exchange failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp struct {
		AccessToken string   `json:"access_token"`
		Scope       string   `json:"scope"`
		Scopes      []string `json:"scopes"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("auggie: failed to parse token response: %w", err)
	}

	accessToken := strings.TrimSpace(tokenResp.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("auggie: token exchange returned empty access token")
	}

	return &auggieTokenResponse{
		AccessToken: accessToken,
		Scopes:      normalizeAuggieScopes(tokenResp.Scopes, tokenResp.Scope),
	}, nil
}

func normalizeAuggieScopes(scopes []string, scope string) []string {
	ordered := make([]string, 0, len(scopes)+1)
	seen := make(map[string]struct{}, len(scopes)+1)

	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		ordered = append(ordered, raw)
	}

	for _, entry := range scopes {
		add(entry)
	}
	for _, entry := range strings.FieldsFunc(scope, func(r rune) bool {
		return r == ' ' || r == ','
	}) {
		add(entry)
	}
	if len(ordered) == 0 {
		add(auggieDefaultScope)
	}
	return ordered
}

func newAuggieAuthRecord(tenantURL, clientID, loginMode string, token *auggieTokenResponse) *coreauth.Auth {
	host := auggieTenantHost(tenantURL)
	label := host
	if label == "" {
		label = "auggie"
	}
	fileName := auggieCredentialFileName(tenantURL)
	metadata := map[string]any{
		"type":         "auggie",
		"label":        label,
		"access_token": token.AccessToken,
		"tenant_url":   tenantURL,
		"scopes":       append([]string(nil), token.Scopes...),
		"client_id":    clientID,
		"login_mode":   loginMode,
		"last_refresh": auggieNow().Format(time.RFC3339),
	}

	return &coreauth.Auth{
		ID:       fileName,
		Provider: "auggie",
		FileName: fileName,
		Label:    label,
		Metadata: metadata,
	}
}

func auggieCredentialFileName(tenantURL string) string {
	host := auggieTenantHost(tenantURL)
	if host == "" {
		return fmt.Sprintf("auggie-%d.json", auggieNow().UnixMilli())
	}
	slug := strings.NewReplacer(".", "-", ":", "-").Replace(host)
	return fmt.Sprintf("auggie-%s.json", slug)
}

func auggieTenantHost(tenantURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(tenantURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func startAuggieCallbackServer(port int) (*http.Server, int, <-chan auggieCallbackPayload, error) {
	addr := "127.0.0.1:0"
	if port > 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", port)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, 0, nil, err
	}

	resultCh := make(chan auggieCallbackPayload, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		payload := auggieCallbackPayload{
			Code:             strings.TrimSpace(q.Get("code")),
			State:            strings.TrimSpace(q.Get("state")),
			TenantURL:        strings.TrimSpace(q.Get("tenant_url")),
			Error:            strings.TrimSpace(q.Get("error")),
			ErrorDescription: strings.TrimSpace(q.Get("error_description")),
		}

		select {
		case resultCh <- payload:
		default:
		}

		if payload.Error != "" || payload.Code == "" {
			_, _ = w.Write([]byte("<h1>Login failed</h1><p>Please check the CLI output.</p>"))
			return
		}
		_, _ = w.Write([]byte("<h1>Login successful</h1><p>You can close this window.</p>"))
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if errServe := srv.Serve(listener); errServe != nil && !strings.Contains(errServe.Error(), "Server closed") {
			log.Warnf("auggie callback server error: %v", errServe)
		}
	}()

	return srv, listener.Addr().(*net.TCPAddr).Port, resultCh, nil
}
