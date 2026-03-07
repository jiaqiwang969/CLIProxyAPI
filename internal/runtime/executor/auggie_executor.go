package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const auggieModelsPath = "/get-models"
const auggieChatStreamPath = "/chat-stream"

// AuggieExecutor handles Auggie-specific revalidation and upstream requests.
type AuggieExecutor struct {
	cfg *config.Config
}

func NewAuggieExecutor(cfg *config.Config) *AuggieExecutor { return &AuggieExecutor{cfg: cfg} }

func (e *AuggieExecutor) Identifier() string { return "auggie" }

func (e *AuggieExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	token := auggieAccessToken(auth)
	if strings.TrimSpace(token) == "" {
		return statusErr{code: http.StatusUnauthorized, msg: "missing access token"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (e *AuggieExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("auggie executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

func (e *AuggieExecutor) Execute(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: "auggie execute not implemented"}
}

func (e *AuggieExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	from := opts.SourceFormat
	if from == "" {
		from = req.Format
	}
	if from == "" {
		from = sdktranslator.FormatOpenAI
	}

	translated := sdktranslator.TranslateRequest(from, sdktranslator.FormatAuggie, req.Model, req.Payload, true)
	return e.executeAuggieStream(ctx, auth, req, opts, translated, from, true)
}

func (e *AuggieExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: "auggie count_tokens not implemented"}
}

func (e *AuggieExecutor) Refresh(_ context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, statusErr{code: http.StatusInternalServerError, msg: "auggie executor: auth is nil"}
	}

	session, err := sdkauth.LoadAuggieSessionFile()
	if err != nil {
		return nil, statusErr{code: http.StatusUnauthorized, msg: err.Error()}
	}

	updated, err := sdkauth.ApplyAuggieSession(auth, session)
	if err != nil {
		return nil, statusErr{code: http.StatusUnauthorized, msg: err.Error()}
	}
	return updated, nil
}

func FetchAuggieModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	exec := NewAuggieExecutor(cfg)
	models, updatedAuth := exec.fetchModels(ctx, auth, true)
	if updatedAuth != nil && auth != nil && updatedAuth != auth {
		replaceAuggieAuthState(auth, updatedAuth)
	}
	return models
}

func (e *AuggieExecutor) fetchModels(ctx context.Context, auth *cliproxyauth.Auth, allowRefresh bool) ([]*registry.ModelInfo, *cliproxyauth.Auth) {
	if auth == nil {
		return nil, nil
	}

	tenantURL, err := sdkauth.NormalizeAuggieTenantURL(auggieTenantURL(auth))
	token := auggieAccessToken(auth)
	if err != nil || strings.TrimSpace(token) == "" {
		if allowRefresh {
			return e.revalidateAuggieModelsAuth(ctx, auth)
		}
		message := "missing access token"
		if err != nil {
			message = err.Error()
		}
		return nil, markAuggieAuthUnauthorized(auth, message)
	}

	requestURL := strings.TrimSuffix(tenantURL, "/") + auggieModelsPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return nil, nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	httpResp, err := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0).Do(httpReq)
	if err != nil {
		return nil, nil
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, nil
	}
	if httpResp.StatusCode == http.StatusUnauthorized && allowRefresh {
		return e.revalidateAuggieModelsAuth(ctx, auth)
	}
	if httpResp.StatusCode == http.StatusUnauthorized {
		return nil, markAuggieAuthUnauthorized(auth, "unauthorized")
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil
	}

	type auggieModel struct {
		Name string `json:"name"`
	}
	var response struct {
		DefaultModel string        `json:"default_model"`
		Models       []auggieModel `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, nil
	}

	now := time.Now().Unix()
	models := make([]*registry.ModelInfo, 0, len(response.Models))
	for _, model := range response.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			continue
		}
		models = append(models, &registry.ModelInfo{
			ID:          name,
			Name:        name,
			DisplayName: name,
			Description: name,
			Version:     name,
			Object:      "model",
			Created:     now,
			OwnedBy:     "auggie",
			Type:        "auggie",
		})
	}
	updated := auth.Clone()
	if updated.Metadata == nil {
		updated.Metadata = make(map[string]any)
	}
	markAuggieAuthActive(updated, time.Now().UTC())
	if defaultModel := strings.TrimSpace(response.DefaultModel); defaultModel != "" {
		updated.Metadata["default_model"] = defaultModel
	}
	if len(models) == 0 {
		return nil, updated
	}
	return models, updated
}

func (e *AuggieExecutor) revalidateAuggieModelsAuth(ctx context.Context, auth *cliproxyauth.Auth) ([]*registry.ModelInfo, *cliproxyauth.Auth) {
	refreshed, err := e.Refresh(ctx, auth)
	if err != nil {
		return nil, markAuggieAuthUnauthorized(auth, err.Error())
	}

	models, updated := e.fetchModels(ctx, refreshed, false)
	if updated == nil {
		updated = refreshed
	}
	return models, updated
}

func auggieAccessToken(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if token, ok := auth.Metadata["access_token"].(string); ok {
		return strings.TrimSpace(token)
	}
	return ""
}

func auggieTenantURL(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if tenantURL, ok := auth.Metadata["tenant_url"].(string); ok {
		return strings.TrimSpace(tenantURL)
	}
	return ""
}

func (e *AuggieExecutor) executeAuggieStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, translated []byte, from sdktranslator.Format, allowRefresh bool) (*cliproxyexecutor.StreamResult, error) {
	if auth == nil {
		return nil, statusErr{code: http.StatusInternalServerError, msg: "auggie executor: auth is nil"}
	}

	tenantURL, err := sdkauth.NormalizeAuggieTenantURL(auggieTenantURL(auth))
	if err != nil {
		if allowRefresh {
			return e.refreshAndRetryAuggieStream(ctx, auth, req, opts, translated, from)
		}
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, err.Error()))
		return nil, statusErr{code: http.StatusUnauthorized, msg: err.Error()}
	}

	token := auggieAccessToken(auth)
	if strings.TrimSpace(token) == "" {
		if allowRefresh {
			return e.refreshAndRetryAuggieStream(ctx, auth, req, opts, translated, from)
		}
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, "missing access token"))
		return nil, statusErr{code: http.StatusUnauthorized, msg: "missing access token"}
	}

	requestURL := strings.TrimSuffix(tenantURL, "/") + auggieChatStreamPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(translated))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/x-ndjson, application/json")
	httpReq.Header.Set("User-Agent", "cli-proxy-auggie")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       requestURL,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpResp, err := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0).Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("auggie executor: close response body error: %v", errClose)
		}
		if allowRefresh {
			return e.refreshAndRetryAuggieStream(ctx, auth, req, opts, translated, from)
		}
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, "unauthorized"))
		return nil, statusErr{code: http.StatusUnauthorized, msg: string(body)}
	}

	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("auggie executor: close response body error: %v", errClose)
		}
		return nil, statusErr{code: httpResp.StatusCode, msg: string(body)}
	}

	markAuggieAuthActive(auth, time.Now().UTC())

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("auggie executor: close response body error: %v", errClose)
			}
		}()

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, streamScannerBuffer)
		var param any
		for scanner.Scan() {
			line := bytes.Clone(scanner.Bytes())
			appendAPIResponseChunk(ctx, e.cfg, line)

			payload := bytes.TrimSpace(line)
			if len(payload) == 0 {
				continue
			}
			if !gjson.ValidBytes(payload) {
				err := statusErr{code: http.StatusBadGateway, msg: "auggie stream returned invalid JSON"}
				recordAPIResponseError(ctx, e.cfg, err)
				out <- cliproxyexecutor.StreamChunk{Err: err}
				return
			}

			chunks := sdktranslator.TranslateStream(ctx, sdktranslator.FormatAuggie, from, req.Model, opts.OriginalRequest, translated, payload, &param)
			for i := range chunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}

		tail := sdktranslator.TranslateStream(ctx, sdktranslator.FormatAuggie, from, req.Model, opts.OriginalRequest, translated, []byte("[DONE]"), &param)
		for i := range tail {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(tail[i])}
		}

		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
		}
	}()

	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

func (e *AuggieExecutor) refreshAndRetryAuggieStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, translated []byte, from sdktranslator.Format) (*cliproxyexecutor.StreamResult, error) {
	refreshed, err := e.Refresh(ctx, auth)
	if err != nil {
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, err.Error()))
		return nil, err
	}

	replaceAuggieAuthState(auth, refreshed)
	return e.executeAuggieStream(ctx, auth, req, opts, translated, from, false)
}

func replaceAuggieAuthState(dst, src *cliproxyauth.Auth) {
	if dst == nil || src == nil {
		return
	}
	clone := src.Clone()
	*dst = *clone
}

func markAuggieAuthUnauthorized(auth *cliproxyauth.Auth, message string) *cliproxyauth.Auth {
	if auth == nil {
		return nil
	}

	updated := auth.Clone()
	now := time.Now().UTC()
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unauthorized"
	}

	updated.Unavailable = true
	updated.Status = cliproxyauth.StatusError
	updated.StatusMessage = "unauthorized"
	updated.LastError = &cliproxyauth.Error{
		Code:       "unauthorized",
		Message:    message,
		Retryable:  false,
		HTTPStatus: http.StatusUnauthorized,
	}
	updated.UpdatedAt = now
	updated.NextRetryAfter = now.Add(30 * time.Minute)
	return updated
}

func markAuggieAuthActive(auth *cliproxyauth.Auth, now time.Time) {
	if auth == nil {
		return
	}
	auth.Unavailable = false
	auth.Status = cliproxyauth.StatusActive
	auth.StatusMessage = ""
	auth.LastError = nil
	auth.NextRetryAfter = time.Time{}
	auth.UpdatedAt = now
}
