package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const auggieModelsPath = "/get-models"
const auggieChatStreamPath = "/chat-stream"
const auggieListRemoteToolsPath = "/agents/list-remote-tools"
const auggieRunRemoteToolPath = "/agents/run-remote-tool"
const auggieModelsUserAgent = "augment.cli/acp/cliproxyapi"
const AuggieShortNameAliasesMetadataKey = "model_short_name_aliases"
const auggieResponsesStateTTL = 30 * time.Minute
const auggieMaxInternalToolContinuations = 8

// AuggieExecutor handles Auggie-specific revalidation and upstream requests.
type AuggieExecutor struct {
	cfg *config.Config
}

type auggieConversationState struct {
	ConversationID       string
	TurnID               string
	ParentConversationID string
	RootConversationID   string
	Model                string
	UpdatedAt            time.Time
}

type auggieConversationStateStore struct {
	mu    sync.Mutex
	items map[string]auggieConversationState
}

type auggieGetModelsUpstreamModel struct {
	Name string `json:"name"`
}

type auggieGetModelsFeatureFlags struct {
	ModelInfoRegistry string `json:"model_info_registry"`
}

type auggieModelInfoRegistryEntry struct {
	ByokProvider string `json:"byokProvider"`
	Description  string `json:"description"`
	Disabled     bool   `json:"disabled"`
	DisplayName  string `json:"displayName"`
	IsDefault    bool   `json:"isDefault"`
	ShortName    string `json:"shortName"`
}

var defaultAuggieResponsesStateStore = &auggieConversationStateStore{
	items: make(map[string]auggieConversationState),
}

var defaultAuggieToolCallStateStore = &auggieConversationStateStore{
	items: make(map[string]auggieConversationState),
}

var defaultAuggieRemoteToolIDs = []int{0, 1, 8, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}

type auggieRemoteToolDefinition struct {
	Name string `json:"name"`
}

type auggieRemoteToolRegistryEntry struct {
	RemoteToolID   int                        `json:"remote_tool_id"`
	ToolDefinition auggieRemoteToolDefinition `json:"tool_definition"`
}

type auggieListRemoteToolsResponse struct {
	Tools []auggieRemoteToolRegistryEntry `json:"tools"`
}

type auggieRunRemoteToolResponse struct {
	ToolOutput        string `json:"tool_output"`
	ToolResultMessage string `json:"tool_result_message"`
	IsError           bool   `json:"is_error"`
	Status            int    `json:"status"`
}

type auggieToolResultContinuation struct {
	ToolUseID string
	Content   string
	IsError   bool
}

type auggieBuiltInToolCall struct {
	ID        string
	Name      string
	Arguments string
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

func (e *AuggieExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	from := opts.SourceFormat
	if from == "" {
		from = req.Format
	}
	if from == "" {
		from = sdktranslator.FormatOpenAI
	}
	if opts.Alt == "responses/compact" {
		return e.executeCompact(ctx, auth, req, opts)
	}

	switch from {
	case sdktranslator.FormatOpenAI:
		streamResult, err := e.ExecuteStream(ctx, auth, req, opts)
		if err != nil {
			return cliproxyexecutor.Response{}, err
		}
		if streamResult == nil {
			return cliproxyexecutor.Response{}, statusErr{code: http.StatusBadGateway, msg: "auggie stream result is nil"}
		}

		payload, err := collectAuggieOpenAINonStream(streamResult.Chunks, payloadRequestedModel(opts, req.Model))
		if err != nil {
			return cliproxyexecutor.Response{}, err
		}

		return cliproxyexecutor.Response{
			Payload: payload,
			Headers: streamResult.Headers,
		}, nil
	case sdktranslator.FormatClaude:
		return e.executeClaude(ctx, auth, req, opts)
	case sdktranslator.FormatOpenAIResponse:
		return e.executeOpenAIResponses(ctx, auth, req, opts)
	default:
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: fmt.Sprintf("auggie execute not implemented for %s", from)}
	}
}

func (e *AuggieExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	from := opts.SourceFormat
	if from == "" {
		from = req.Format
	}
	if from == "" {
		from = sdktranslator.FormatOpenAI
	}
	if opts.Alt == "responses/compact" {
		return nil, statusErr{code: http.StatusBadRequest, msg: "streaming not supported for /responses/compact"}
	}

	switch from {
	case sdktranslator.FormatOpenAI:
		if err := validateAuggieOpenAIRequestCapabilities(req.Payload); err != nil {
			return nil, err
		}
		resolvedReq := req
		resolvedReq.Model = resolveAuggieModelAlias(auth, req.Model)

		translated := sdktranslator.TranslateRequest(from, sdktranslator.FormatAuggie, resolvedReq.Model, req.Payload, true)
		translated, err := enrichAuggieOpenAIChatCompletionRequest(resolvedReq.Model, req.Payload, translated)
		if err != nil {
			return nil, err
		}
		return e.executeAuggieStream(ctx, auth, resolvedReq, opts, translated, from, true)
	case sdktranslator.FormatClaude:
		return e.executeClaudeStream(ctx, auth, req, opts)
	case sdktranslator.FormatOpenAIResponse:
		return e.executeOpenAIResponsesStream(ctx, auth, req, opts)
	default:
		return nil, statusErr{code: http.StatusNotImplemented, msg: fmt.Sprintf("auggie execute not implemented for %s", from)}
	}
}

func (e *AuggieExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	from := opts.SourceFormat
	if from == "" {
		from = req.Format
	}
	if from == "" {
		from = sdktranslator.FormatOpenAI
	}

	openAIReq := req
	switch from {
	case sdktranslator.FormatOpenAI:
	case sdktranslator.FormatClaude:
		openAIReq, _ = buildAuggieBridgeToOpenAIRequest(req, opts, sdktranslator.FormatClaude, false)
	case sdktranslator.FormatOpenAIResponse:
		openAIReq, _ = buildAuggieOpenAIRequest(req, opts, false)
	default:
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: fmt.Sprintf("auggie count_tokens not implemented for %s", from)}
	}

	openAIReq.Model = resolveAuggieModelAlias(auth, req.Model)
	baseModel := thinking.ParseSuffix(openAIReq.Model).ModelName

	enc, err := tokenizerForModel(baseModel)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("auggie executor: tokenizer init failed: %w", err)
	}

	count, err := countOpenAIChatTokens(enc, openAIReq.Payload)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("auggie executor: token counting failed: %w", err)
	}

	usageJSON := buildOpenAIUsageJSON(count)
	translated := sdktranslator.TranslateTokenCount(ctx, sdktranslator.FormatOpenAI, from, count, usageJSON)
	return cliproxyexecutor.Response{Payload: []byte(translated)}, nil
}

func (e *AuggieExecutor) executeCompact(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	from := opts.SourceFormat
	if from == "" {
		from = req.Format
	}
	if from == "" {
		from = sdktranslator.FormatOpenAIResponse
	}
	if from != sdktranslator.FormatOpenAIResponse {
		return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: fmt.Sprintf("auggie /responses/compact not implemented for %s", from)}
	}

	body := req.Payload
	if len(opts.OriginalRequest) > 0 {
		body = opts.OriginalRequest
	}
	body = sdktranslator.TranslateRequest(from, sdktranslator.FormatOpenAIResponse, req.Model, body, false)

	output := buildAuggieCompactOutput(body)
	resolvedModel := resolveAuggieModelAlias(auth, req.Model)
	inputTokens, err := countAuggieResponsesTokens(resolvedModel, body)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	outputEnvelope, err := json.Marshal(map[string]any{
		"model": req.Model,
		"input": output,
	})
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	outputTokens, err := countAuggieResponsesTokens(resolvedModel, outputEnvelope)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	createdAt := time.Now().Unix()
	payload, err := json.Marshal(map[string]any{
		"id":         fmt.Sprintf("auggie-compaction-%d", createdAt),
		"object":     "response.compaction",
		"created_at": createdAt,
		"output":     output,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + outputTokens,
		},
	})
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	return cliproxyexecutor.Response{Payload: payload}, nil
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
	httpReq.Header.Set("User-Agent", auggieModelsUserAgent)
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

	var response struct {
		DefaultModel string                         `json:"default_model"`
		Models       []auggieGetModelsUpstreamModel `json:"models"`
		FeatureFlags auggieGetModelsFeatureFlags    `json:"feature_flags"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, nil
	}

	now := time.Now().Unix()
	models, defaultModel, shortNameAliases, usedRegistry := buildAuggieModelsFromGetModelsResponse(now, response.DefaultModel, response.Models, response.FeatureFlags.ModelInfoRegistry)
	updated := auth.Clone()
	if updated.Metadata == nil {
		updated.Metadata = make(map[string]any)
	}
	markAuggieAuthActive(updated, time.Now().UTC())
	if usedRegistry {
		if defaultModel != "" {
			updated.Metadata["default_model"] = defaultModel
		} else {
			delete(updated.Metadata, "default_model")
		}
		if rawDefaultModel := strings.TrimSpace(response.DefaultModel); rawDefaultModel != "" && rawDefaultModel != defaultModel {
			updated.Metadata["default_model_raw"] = rawDefaultModel
		} else {
			delete(updated.Metadata, "default_model_raw")
		}
	} else if defaultModel := strings.TrimSpace(response.DefaultModel); defaultModel != "" {
		updated.Metadata["default_model"] = defaultModel
		delete(updated.Metadata, "default_model_raw")
	} else {
		delete(updated.Metadata, "default_model")
		delete(updated.Metadata, "default_model_raw")
	}
	if len(shortNameAliases) > 0 {
		updated.Metadata[AuggieShortNameAliasesMetadataKey] = auggieShortNameAliasesMetadata(shortNameAliases)
	} else {
		delete(updated.Metadata, AuggieShortNameAliasesMetadataKey)
	}
	if len(models) == 0 {
		return nil, updated
	}
	return models, updated
}

func buildAuggieModelsFromGetModelsResponse(now int64, rawDefaultModel string, upstreamModels []auggieGetModelsUpstreamModel, rawModelInfoRegistry string) ([]*registry.ModelInfo, string, map[string]string, bool) {
	if models, defaultModel, shortNameAliases, ok := buildAuggieModelsFromInfoRegistry(now, rawDefaultModel, rawModelInfoRegistry); ok {
		return models, defaultModel, shortNameAliases, true
	}
	return buildAuggieModelsFromNames(now, upstreamModels), strings.TrimSpace(rawDefaultModel), nil, false
}

func buildAuggieModelsFromInfoRegistry(now int64, rawDefaultModel, rawModelInfoRegistry string) ([]*registry.ModelInfo, string, map[string]string, bool) {
	rawModelInfoRegistry = strings.TrimSpace(rawModelInfoRegistry)
	if rawModelInfoRegistry == "" {
		return nil, "", nil, false
	}

	var entries map[string]auggieModelInfoRegistryEntry
	if err := json.Unmarshal([]byte(rawModelInfoRegistry), &entries); err != nil {
		log.Debugf("auggie get-models: failed to parse model_info_registry: %v", err)
		return nil, "", nil, false
	}

	ids := make([]string, 0, len(entries))
	for id, entry := range entries {
		id = strings.TrimSpace(id)
		if id == "" || entry.Disabled {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := auggieModelInfoSortKey(ids[i], entries[ids[i]])
		right := auggieModelInfoSortKey(ids[j], entries[ids[j]])
		if left == right {
			return ids[i] < ids[j]
		}
		return left < right
	})

	defaultModel := ""
	if id := strings.TrimSpace(rawDefaultModel); id != "" {
		if entry, ok := entries[id]; ok && !entry.Disabled {
			defaultModel = id
		}
	}
	if defaultModel == "" {
		for _, id := range ids {
			if entries[id].IsDefault {
				defaultModel = id
				break
			}
		}
	}

	models := make([]*registry.ModelInfo, 0, len(ids))
	shortNameAliases := make(map[string]string, len(ids))
	for _, id := range ids {
		entry := entries[id]
		displayName := strings.TrimSpace(entry.DisplayName)
		if displayName == "" {
			displayName = id
		}
		description := strings.TrimSpace(entry.Description)
		if description == "" {
			description = displayName
		}
		shortName := strings.TrimSpace(entry.ShortName)
		requestAlias := ""
		if shortName != "" && !strings.EqualFold(shortName, id) {
			addAuggieAlias(shortNameAliases, shortName, id)
			requestAlias = shortName
		}
		for _, alias := range auggieDisplayNameAliases(displayName) {
			addAuggieAlias(shortNameAliases, alias, id)
		}
		models = append(models, &registry.ModelInfo{
			ID:          id,
			Name:        requestAlias,
			DisplayName: displayName,
			Description: description,
			Version:     id,
			Object:      "model",
			Created:     now,
			OwnedBy:     "auggie",
			Type:        "auggie",
		})
	}
	if len(shortNameAliases) == 0 {
		shortNameAliases = nil
	}
	return models, defaultModel, shortNameAliases, true
}

func addAuggieAlias(aliases map[string]string, alias, canonicalID string) {
	if aliases == nil {
		return
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	canonicalID = strings.TrimSpace(canonicalID)
	if alias == "" || canonicalID == "" || strings.EqualFold(alias, canonicalID) {
		return
	}
	if _, exists := aliases[alias]; exists {
		return
	}
	aliases[alias] = canonicalID
}

func auggieDisplayNameAliases(displayName string) []string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return nil
	}

	raw := strings.ToLower(displayName)
	slug := auggieDisplayNameSlug(displayName)
	if slug == "" || slug == raw {
		return []string{raw}
	}
	return []string{raw, slug}
}

func auggieDisplayNameModelID(displayName string) string {
	slug := auggieDisplayNameSlug(displayName)
	if slug != "" {
		return slug
	}
	return strings.ToLower(strings.TrimSpace(displayName))
}

func auggieDisplayNameSlug(displayName string) string {
	displayName = strings.ToLower(strings.TrimSpace(displayName))
	if displayName == "" {
		return ""
	}

	var builder strings.Builder
	lastWasDash := false
	for _, r := range displayName {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.':
			builder.WriteRune(r)
			lastWasDash = false
		case r == '-' || unicode.IsSpace(r) || r == '_' || r == '/':
			if builder.Len() == 0 || lastWasDash {
				continue
			}
			builder.WriteByte('-')
			lastWasDash = true
		}
	}

	return strings.Trim(builder.String(), "-")
}

func buildAuggieModelsFromNames(now int64, upstreamModels []auggieGetModelsUpstreamModel) []*registry.ModelInfo {
	models := make([]*registry.ModelInfo, 0, len(upstreamModels))
	for _, model := range upstreamModels {
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
	return models
}

func auggieShortNameAliasesMetadata(aliases map[string]string) map[string]any {
	if len(aliases) == 0 {
		return nil
	}

	out := make(map[string]any, len(aliases))
	for shortName, canonicalID := range aliases {
		shortName = strings.ToLower(strings.TrimSpace(shortName))
		canonicalID = strings.TrimSpace(canonicalID)
		if shortName == "" || canonicalID == "" {
			continue
		}
		out[shortName] = canonicalID
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func auggieShortNameAliases(auth *cliproxyauth.Auth) map[string]string {
	if auth == nil || len(auth.Metadata) == 0 {
		return nil
	}

	raw, ok := auth.Metadata[AuggieShortNameAliasesMetadataKey]
	if !ok || raw == nil {
		return nil
	}

	switch typed := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(typed))
		for shortName, canonicalID := range typed {
			shortName = strings.ToLower(strings.TrimSpace(shortName))
			canonicalID = strings.TrimSpace(canonicalID)
			if shortName == "" || canonicalID == "" {
				continue
			}
			out[shortName] = canonicalID
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(typed))
		for rawShortName, rawCanonicalID := range typed {
			shortName := strings.ToLower(strings.TrimSpace(rawShortName))
			if shortName == "" {
				continue
			}
			canonicalID, _ := rawCanonicalID.(string)
			canonicalID = strings.TrimSpace(canonicalID)
			if canonicalID == "" {
				continue
			}
			out[shortName] = canonicalID
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func resolveAuggieModelAlias(auth *cliproxyauth.Auth, requestedModel string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ""
	}

	if aliases := auggieShortNameAliases(auth); len(aliases) > 0 {
		if canonicalID := strings.TrimSpace(aliases[strings.ToLower(requestedModel)]); canonicalID != "" {
			return canonicalID
		}
	}

	info := registry.LookupModelInfoByAlias(requestedModel, "auggie")
	if info == nil {
		return requestedModel
	}
	canonicalID := strings.TrimSpace(info.Version)
	if canonicalID == "" || strings.EqualFold(canonicalID, requestedModel) {
		return requestedModel
	}
	return canonicalID
}

func auggieModelInfoSortKey(id string, entry auggieModelInfoRegistryEntry) string {
	if displayName := strings.TrimSpace(entry.DisplayName); displayName != "" {
		return strings.ToLower(displayName)
	}
	return strings.ToLower(strings.TrimSpace(id))
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

func (s *auggieConversationStateStore) Store(key string, state auggieConversationState) {
	key = strings.TrimSpace(key)
	if key == "" || strings.TrimSpace(state.ConversationID) == "" || strings.TrimSpace(state.TurnID) == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)
	state.UpdatedAt = now
	s.items[key] = state
}

func (s *auggieConversationStateStore) Load(key string) (auggieConversationState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	state, ok := s.items[strings.TrimSpace(key)]
	return state, ok
}

func (s *auggieConversationStateStore) cleanupLocked(now time.Time) {
	cutoff := now.Add(-auggieResponsesStateTTL)
	for key, state := range s.items {
		if state.UpdatedAt.IsZero() || state.UpdatedAt.Before(cutoff) {
			delete(s.items, key)
		}
	}
}

func (e *AuggieExecutor) executeAuggieStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, translated []byte, from sdktranslator.Format, allowRefresh bool) (result *cliproxyexecutor.StreamResult, err error) {
	if auth == nil {
		return nil, statusErr{code: http.StatusInternalServerError, msg: "auggie executor: auth is nil"}
	}
	usageModel := thinking.ParseSuffix(resolveAuggieModelAlias(auth, req.Model)).ModelName
	if strings.TrimSpace(usageModel) == "" {
		usageModel = thinking.ParseSuffix(req.Model).ModelName
	}
	reporter := newUsageReporter(ctx, e.Identifier(), usageModel, auth)
	defer reporter.trackFailure(ctx, &err)

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
	responseModel := payloadRequestedModel(opts, req.Model)

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
		var translatedResponseID string
		var conversationState auggieConversationState
		var toolCallIDs []string
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
				reporter.publishFailure(ctx)
				out <- cliproxyexecutor.StreamChunk{Err: err}
				return
			}
			updateAuggieConversationStateFromPayload(&conversationState, payload)
			if detail, ok := parseAuggieUsage(payload); ok {
				reporter.publish(ctx, detail)
			}

			chunks := sdktranslator.TranslateStream(ctx, sdktranslator.FormatAuggie, from, responseModel, opts.OriginalRequest, translated, payload, &param)
			for i := range chunks {
				if opts.SourceFormat == sdktranslator.FormatOpenAIResponse && translatedResponseID == "" {
					if got := strings.TrimSpace(gjson.GetBytes([]byte(chunks[i]), "id").String()); got != "" {
						translatedResponseID = got
					}
				}
				if opts.SourceFormat == sdktranslator.FormatOpenAI {
					toolCallIDs = appendUniqueStrings(toolCallIDs, openAIToolCallIDsFromChunk([]byte(chunks[i]))...)
				}
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}

		tail := sdktranslator.TranslateStream(ctx, sdktranslator.FormatAuggie, from, responseModel, opts.OriginalRequest, translated, []byte("[DONE]"), &param)
		for i := range tail {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(tail[i])}
		}

		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			reporter.publishFailure(ctx)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
			return
		}
		conversationState.Model = strings.TrimSpace(gjson.GetBytes(translated, "model").String())
		if conversationState.Model == "" {
			conversationState.Model = responseModel
		}
		if opts.SourceFormat == sdktranslator.FormatOpenAIResponse && translatedResponseID != "" {
			defaultAuggieResponsesStateStore.Store(translatedResponseID, conversationState)
		}
		if opts.SourceFormat == sdktranslator.FormatOpenAI && len(toolCallIDs) > 0 {
			for _, toolCallID := range toolCallIDs {
				defaultAuggieToolCallStateStore.Store(toolCallID, conversationState)
			}
		}
		reporter.ensurePublished(ctx)
	}()

	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

func (e *AuggieExecutor) executeAuggieJSON(ctx context.Context, auth *cliproxyauth.Auth, path string, requestBody []byte, allowRefresh bool) ([]byte, error) {
	if auth == nil {
		return nil, statusErr{code: http.StatusInternalServerError, msg: "auggie executor: auth is nil"}
	}

	tenantURL, err := sdkauth.NormalizeAuggieTenantURL(auggieTenantURL(auth))
	if err != nil {
		if allowRefresh {
			refreshed, refreshErr := e.Refresh(ctx, auth)
			if refreshErr != nil {
				replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, refreshErr.Error()))
				return nil, refreshErr
			}
			replaceAuggieAuthState(auth, refreshed)
			return e.executeAuggieJSON(ctx, auth, path, requestBody, false)
		}
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, err.Error()))
		return nil, statusErr{code: http.StatusUnauthorized, msg: err.Error()}
	}

	token := auggieAccessToken(auth)
	if strings.TrimSpace(token) == "" {
		if allowRefresh {
			refreshed, refreshErr := e.Refresh(ctx, auth)
			if refreshErr != nil {
				replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, refreshErr.Error()))
				return nil, refreshErr
			}
			replaceAuggieAuthState(auth, refreshed)
			return e.executeAuggieJSON(ctx, auth, path, requestBody, false)
		}
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, "missing access token"))
		return nil, statusErr{code: http.StatusUnauthorized, msg: "missing access token"}
	}

	requestURL := strings.TrimSuffix(tenantURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
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
		Body:      requestBody,
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
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("auggie executor: close response body error: %v", errClose)
		}
	}()

	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	responseBody, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		recordAPIResponseError(ctx, e.cfg, readErr)
		return nil, readErr
	}
	appendAPIResponseChunk(ctx, e.cfg, responseBody)

	if httpResp.StatusCode == http.StatusUnauthorized {
		if allowRefresh {
			refreshed, refreshErr := e.Refresh(ctx, auth)
			if refreshErr != nil {
				replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, refreshErr.Error()))
				return nil, refreshErr
			}
			replaceAuggieAuthState(auth, refreshed)
			return e.executeAuggieJSON(ctx, auth, path, requestBody, false)
		}
		replaceAuggieAuthState(auth, markAuggieAuthUnauthorized(auth, "unauthorized"))
		return nil, statusErr{code: http.StatusUnauthorized, msg: string(responseBody)}
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return nil, statusErr{code: httpResp.StatusCode, msg: string(responseBody)}
	}

	markAuggieAuthActive(auth, time.Now().UTC())
	return responseBody, nil
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

func collectAuggieOpenAINonStream(chunks <-chan cliproxyexecutor.StreamChunk, fallbackModel string) ([]byte, error) {
	if chunks == nil {
		return nil, statusErr{code: http.StatusBadGateway, msg: "auggie stream returned no chunks"}
	}

	var (
		content                   strings.Builder
		reasoning                 strings.Builder
		reasoningEncryptedContent string
		reasoningItemID           string
		responseID                string
		responseModel             = strings.TrimSpace(fallbackModel)
		nativeFinishReason        any
		finishReason              any = "stop"
		created                   int64
		toolCalls                 []json.RawMessage
		usageRaw                  json.RawMessage
	)

	for chunk := range chunks {
		if chunk.Err != nil {
			return nil, chunk.Err
		}
		payload := bytes.TrimSpace(chunk.Payload)
		if len(payload) == 0 {
			continue
		}
		if !gjson.ValidBytes(payload) {
			return nil, statusErr{code: http.StatusBadGateway, msg: "auggie stream returned invalid JSON"}
		}
		if got := strings.TrimSpace(gjson.GetBytes(payload, "id").String()); got != "" && responseID == "" {
			responseID = got
		}
		if got := strings.TrimSpace(gjson.GetBytes(payload, "model").String()); got != "" {
			responseModel = got
		}
		if got := gjson.GetBytes(payload, "created"); got.Exists() && created == 0 {
			created = got.Int()
		}
		if text := gjson.GetBytes(payload, "choices.0.delta.content"); text.Exists() {
			content.WriteString(text.String())
		}
		if rc := gjson.GetBytes(payload, "choices.0.delta.reasoning_content"); rc.Exists() {
			reasoning.WriteString(rc.String())
		}
		if itemID := gjson.GetBytes(payload, "choices.0.delta.reasoning_item_id"); itemID.Exists() && strings.TrimSpace(itemID.String()) != "" {
			reasoningItemID = itemID.String()
		}
		if encrypted := gjson.GetBytes(payload, "choices.0.delta.reasoning_encrypted_content"); encrypted.Exists() && strings.TrimSpace(encrypted.String()) != "" {
			reasoningEncryptedContent = encrypted.String()
		}
		if tcs := gjson.GetBytes(payload, "choices.0.delta.tool_calls"); tcs.Exists() && tcs.IsArray() {
			tcs.ForEach(func(_, tc gjson.Result) bool {
				toolCalls = append(toolCalls, json.RawMessage(tc.Raw))
				return true
			})
		}
		if fr := gjson.GetBytes(payload, "choices.0.finish_reason"); fr.Exists() && strings.TrimSpace(fr.String()) != "" && fr.String() != "null" {
			finishReason = fr.Value()
		}
		if nfr := gjson.GetBytes(payload, "choices.0.native_finish_reason"); nfr.Exists() && strings.TrimSpace(nfr.String()) != "" && nfr.String() != "null" {
			nativeFinishReason = nfr.Value()
		}
		if u := gjson.GetBytes(payload, "usage"); u.Exists() && u.Type != gjson.Null {
			usageRaw = json.RawMessage(u.Raw)
		}
	}

	if created == 0 {
		created = time.Now().Unix()
	}
	if responseID == "" {
		responseID = fmt.Sprintf("auggie-%d", created)
	}

	choice := map[string]any{
		"index": 0,
		"message": map[string]any{
			"role":    "assistant",
			"content": content.String(),
		},
		"finish_reason": finishReason,
	}
	if len(toolCalls) > 0 {
		choice["message"].(map[string]any)["tool_calls"] = toolCalls
	}
	if reasoning.Len() > 0 {
		choice["message"].(map[string]any)["reasoning_content"] = reasoning.String()
	}
	if reasoningItemID != "" {
		choice["message"].(map[string]any)["reasoning_item_id"] = reasoningItemID
	}
	if reasoningEncryptedContent != "" {
		choice["message"].(map[string]any)["reasoning_encrypted_content"] = reasoningEncryptedContent
	}
	if nativeFinishReason != nil {
		choice["native_finish_reason"] = nativeFinishReason
	}

	response := map[string]any{
		"id":      responseID,
		"object":  "chat.completion",
		"created": created,
		"model":   responseModel,
		"choices": []map[string]any{choice},
	}
	if len(usageRaw) > 0 {
		response["usage"] = usageRaw
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (e *AuggieExecutor) executeClaude(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	openAIReq, originalPayload := buildAuggieBridgeToOpenAIRequest(req, opts, sdktranslator.FormatClaude, false)
	openAIOpts := opts
	openAIOpts.SourceFormat = sdktranslator.FormatOpenAI

	openAIResp, err := e.Execute(ctx, auth, openAIReq, openAIOpts)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	responseModel := payloadRequestedModel(opts, req.Model)
	var param any
	translated := sdktranslator.TranslateNonStream(
		ctx,
		sdktranslator.FormatOpenAI,
		sdktranslator.FormatClaude,
		responseModel,
		originalPayload,
		openAIReq.Payload,
		openAIResp.Payload,
		&param,
	)

	openAIResp.Payload = []byte(translated)
	return openAIResp, nil
}

func (e *AuggieExecutor) executeClaudeStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	openAIReq, originalPayload := buildAuggieBridgeToOpenAIRequest(req, opts, sdktranslator.FormatClaude, true)
	openAIOpts := opts
	openAIOpts.SourceFormat = sdktranslator.FormatOpenAI

	openAIResult, err := e.ExecuteStream(ctx, auth, openAIReq, openAIOpts)
	if err != nil {
		return nil, err
	}
	if openAIResult == nil {
		return nil, statusErr{code: http.StatusBadGateway, msg: "auggie stream result is nil"}
	}

	responseModel := payloadRequestedModel(opts, req.Model)
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)

		var param any
		for chunk := range openAIResult.Chunks {
			if chunk.Err != nil {
				out <- chunk
				return
			}

			lines := sdktranslator.TranslateStream(
				ctx,
				sdktranslator.FormatOpenAI,
				sdktranslator.FormatClaude,
				responseModel,
				originalPayload,
				openAIReq.Payload,
				wrapOpenAISSEPayload(chunk.Payload),
				&param,
			)
			for i := range lines {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(lines[i])}
			}
		}

		tail := sdktranslator.TranslateStream(
			ctx,
			sdktranslator.FormatOpenAI,
			sdktranslator.FormatClaude,
			responseModel,
			originalPayload,
			openAIReq.Payload,
			wrapOpenAISSEPayload([]byte("[DONE]")),
			&param,
		)
		for i := range tail {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(tail[i])}
		}
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: openAIResult.Headers,
		Chunks:  out,
	}, nil
}

func (e *AuggieExecutor) executeOpenAIResponses(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	resolvedReq := req
	resolvedReq.Model = resolveAuggieModelAlias(auth, req.Model)
	translated, originalPayload, err := buildAuggieResponsesTranslatedRequest(resolvedReq.Model, req, opts, false)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	openAIPayload, headers, err := e.executeAuggieResponsesTurn(ctx, auth, resolvedReq, opts, translated)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	openAIPayload, headers, err = e.completeAuggieResponsesBuiltInToolLoop(ctx, auth, resolvedReq, opts, translated, openAIPayload, headers)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	responseModel := payloadRequestedModel(opts, req.Model)
	var param any
	translatedResponse := sdktranslator.TranslateNonStream(
		ctx,
		sdktranslator.FormatOpenAI,
		sdktranslator.FormatOpenAIResponse,
		responseModel,
		originalPayload,
		originalPayload,
		openAIPayload,
		&param,
	)

	return cliproxyexecutor.Response{
		Payload: []byte(translatedResponse),
		Headers: headers,
	}, nil
}

func (e *AuggieExecutor) executeAuggieResponsesTurn(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, translated []byte) ([]byte, http.Header, error) {
	streamResult, err := e.executeAuggieStream(ctx, auth, req, opts, translated, sdktranslator.FormatOpenAI, true)
	if err != nil {
		return nil, nil, err
	}
	if streamResult == nil {
		return nil, nil, statusErr{code: http.StatusBadGateway, msg: "auggie stream result is nil"}
	}

	openAIPayload, err := collectAuggieOpenAINonStream(streamResult.Chunks, payloadRequestedModel(opts, req.Model))
	if err != nil {
		return nil, nil, err
	}
	return openAIPayload, streamResult.Headers, nil
}

func (e *AuggieExecutor) completeAuggieResponsesBuiltInToolLoop(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, baseTranslated, openAIPayload []byte, headers http.Header) ([]byte, http.Header, error) {
	currentPayload := openAIPayload
	currentHeaders := headers

	for continuationCount := 0; continuationCount < auggieMaxInternalToolContinuations; continuationCount++ {
		toolCalls, shouldContinue, err := extractAuggieBuiltInToolCalls(currentPayload)
		if err != nil {
			return nil, nil, err
		}
		if !shouldContinue {
			return currentPayload, currentHeaders, nil
		}

		responseID := strings.TrimSpace(gjson.GetBytes(currentPayload, "id").String())
		if responseID == "" {
			return nil, nil, statusErr{code: http.StatusBadGateway, msg: "missing response id for Auggie built-in tool continuation"}
		}
		state, ok := defaultAuggieResponsesStateStore.Load(responseID)
		if !ok {
			return nil, nil, statusErr{code: http.StatusBadGateway, msg: fmt.Sprintf("missing Auggie conversation state for built-in tool continuation: %s", responseID)}
		}

		toolResults, err := e.runAuggieBuiltInToolCalls(ctx, auth, toolCalls)
		if err != nil {
			return nil, nil, err
		}

		continuationRequest, err := buildAuggieToolContinuationRequest(baseTranslated, state, toolResults)
		if err != nil {
			return nil, nil, err
		}

		currentPayload, currentHeaders, err = e.executeAuggieResponsesTurn(ctx, auth, req, opts, continuationRequest)
		if err != nil {
			return nil, nil, err
		}
	}

	return nil, nil, statusErr{code: http.StatusBadGateway, msg: fmt.Sprintf("Auggie built-in tool continuation exceeded %d internal turns", auggieMaxInternalToolContinuations)}
}

func (e *AuggieExecutor) executeOpenAIResponsesStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	resolvedReq := req
	resolvedReq.Model = resolveAuggieModelAlias(auth, req.Model)
	translated, originalPayload, err := buildAuggieResponsesTranslatedRequest(resolvedReq.Model, req, opts, true)
	if err != nil {
		return nil, err
	}
	if auggieResponsesRequestUsesBuiltInToolBridge(originalPayload) {
		openAIPayload, headers, err := e.executeAuggieResponsesTurn(ctx, auth, resolvedReq, opts, translated)
		if err != nil {
			return nil, err
		}
		openAIPayload, headers, err = e.completeAuggieResponsesBuiltInToolLoop(ctx, auth, resolvedReq, opts, translated, openAIPayload, headers)
		if err != nil {
			return nil, err
		}
		return streamAuggieBufferedResponsesPayload(ctx, payloadRequestedModel(opts, req.Model), originalPayload, openAIPayload, headers)
	}

	openAIResult, err := e.executeAuggieStream(ctx, auth, resolvedReq, opts, translated, sdktranslator.FormatOpenAI, true)
	if err != nil {
		return nil, err
	}
	if openAIResult == nil {
		return nil, statusErr{code: http.StatusBadGateway, msg: "auggie stream result is nil"}
	}

	responseModel := payloadRequestedModel(opts, req.Model)
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)

		var param any
		completedSent := false
		for chunk := range openAIResult.Chunks {
			if chunk.Err != nil {
				out <- chunk
				return
			}

			lines := sdktranslator.TranslateStream(
				ctx,
				sdktranslator.FormatOpenAI,
				sdktranslator.FormatOpenAIResponse,
				responseModel,
				originalPayload,
				originalPayload,
				bytes.Clone(chunk.Payload),
				&param,
			)
			for i := range lines {
				if isOpenAIResponsesTerminalEventLine(lines[i]) {
					if completedSent {
						continue
					}
					completedSent = true
				}
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(lines[i])}
			}
		}
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: openAIResult.Headers,
		Chunks:  out,
	}, nil
}

func auggieResponsesRequestUsesBuiltInToolBridge(rawJSON []byte) bool {
	tools := gjson.GetBytes(rawJSON, "tools")
	if tools.Exists() && tools.IsArray() {
		for _, tool := range tools.Array() {
			if strings.TrimSpace(tool.Get("type").String()) == "web_search" {
				return true
			}
		}
	}

	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	return toolChoice.IsObject() && strings.TrimSpace(toolChoice.Get("type").String()) == "web_search"
}

func streamAuggieBufferedResponsesPayload(ctx context.Context, responseModel string, originalPayload, openAIPayload []byte, headers http.Header) (*cliproxyexecutor.StreamResult, error) {
	openAIChunks, err := synthesizeOpenAIResponseChunks(openAIPayload)
	if err != nil {
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)

		var param any
		completedSent := false
		for _, openAIChunk := range openAIChunks {
			lines := sdktranslator.TranslateStream(
				ctx,
				sdktranslator.FormatOpenAI,
				sdktranslator.FormatOpenAIResponse,
				responseModel,
				originalPayload,
				originalPayload,
				bytes.Clone(openAIChunk),
				&param,
			)
			for i := range lines {
				if isOpenAIResponsesTerminalEventLine(lines[i]) {
					if completedSent {
						continue
					}
					completedSent = true
				}
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(lines[i])}
			}
		}
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: headers,
		Chunks:  out,
	}, nil
}

func synthesizeOpenAIResponseChunks(openAIPayload []byte) ([][]byte, error) {
	root := gjson.ParseBytes(openAIPayload)

	responseID := strings.TrimSpace(root.Get("id").String())
	if responseID == "" {
		responseID = fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	}

	created := root.Get("created").Int()
	if created == 0 {
		created = time.Now().Unix()
	}

	model := strings.TrimSpace(root.Get("model").String())
	if model == "" {
		model = "gpt-5.4"
	}

	delta := map[string]any{
		"role": "assistant",
	}
	if content := root.Get("choices.0.message.content"); content.Exists() && content.String() != "" {
		delta["content"] = content.String()
	}
	if reasoning := root.Get("choices.0.message.reasoning_content"); reasoning.Exists() && reasoning.String() != "" {
		delta["reasoning_content"] = reasoning.String()
	}
	if reasoningItemID := strings.TrimSpace(root.Get("choices.0.message.reasoning_item_id").String()); reasoningItemID != "" {
		delta["reasoning_item_id"] = reasoningItemID
	}
	if reasoningEncrypted := strings.TrimSpace(root.Get("choices.0.message.reasoning_encrypted_content").String()); reasoningEncrypted != "" {
		delta["reasoning_encrypted_content"] = reasoningEncrypted
	}

	firstChunk := map[string]any{
		"id":      responseID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": delta,
			},
		},
	}

	finishReason := strings.TrimSpace(root.Get("choices.0.finish_reason").String())
	if finishReason == "" {
		finishReason = "stop"
	}

	secondChunk := map[string]any{
		"id":      responseID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			},
		},
	}
	if usage := root.Get("usage"); usage.Exists() && usage.Type != gjson.Null {
		secondChunk["usage"] = usage.Value()
	}

	firstRaw, err := json.Marshal(firstChunk)
	if err != nil {
		return nil, err
	}
	secondRaw, err := json.Marshal(secondChunk)
	if err != nil {
		return nil, err
	}

	return [][]byte{firstRaw, secondRaw}, nil
}

func isOpenAIResponsesTerminalEventLine(line string) bool {
	return strings.Contains(line, `"type":"response.completed"`) || strings.Contains(line, `"type":"response.incomplete"`)
}

func buildAuggieOpenAIRequest(req cliproxyexecutor.Request, opts cliproxyexecutor.Options, stream bool) (cliproxyexecutor.Request, []byte) {
	return buildAuggieBridgeToOpenAIRequest(req, opts, sdktranslator.FormatOpenAIResponse, stream)
}

func buildAuggieResponsesTranslatedRequest(model string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, stream bool) ([]byte, []byte, error) {
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}
	if err := validateAuggieResponsesRequestCapabilities(originalPayload); err != nil {
		return nil, originalPayload, err
	}

	openAIPayload := sdktranslator.TranslateRequest(sdktranslator.FormatOpenAIResponse, sdktranslator.FormatOpenAI, model, req.Payload, stream)
	translated := sdktranslator.TranslateRequest(sdktranslator.FormatOpenAI, sdktranslator.FormatAuggie, model, openAIPayload, stream)

	previousResponseID := strings.TrimSpace(gjson.GetBytes(originalPayload, "previous_response_id").String())
	if previousResponseID == "" {
		return translated, originalPayload, nil
	}

	state, ok := defaultAuggieResponsesStateStore.Load(previousResponseID)
	if !ok {
		return nil, originalPayload, statusErr{code: http.StatusBadRequest, msg: fmt.Sprintf("unknown previous_response_id: %s", previousResponseID)}
	}
	if strings.TrimSpace(state.ConversationID) == "" || strings.TrimSpace(state.TurnID) == "" {
		return nil, originalPayload, statusErr{code: http.StatusBadRequest, msg: fmt.Sprintf("missing Auggie conversation state for previous_response_id: %s", previousResponseID)}
	}

	var err error
	translated, err = applyAuggieConversationState(translated, state)
	if err != nil {
		return nil, originalPayload, err
	}
	return translated, originalPayload, nil
}

func validateAuggieResponsesRequestCapabilities(rawJSON []byte) error {
	if err := validateAuggieStoreSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesIncludeSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesToolTypes(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesToolChoice(rawJSON); err != nil {
		return err
	}
	return validateAuggieResponsesInputItemTypes(rawJSON)
}

func validateAuggieOpenAIRequestCapabilities(rawJSON []byte) error {
	if err := validateAuggieStoreSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIIncludeSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIToolTypes(rawJSON); err != nil {
		return err
	}
	return validateAuggieOpenAIToolChoice(rawJSON)
}

func validateAuggieStoreSupport(rawJSON []byte) error {
	store := gjson.GetBytes(rawJSON, "store")
	if !store.Exists() {
		return nil
	}
	if store.Type != gjson.True && store.Type != gjson.False {
		return statusErr{
			code: http.StatusBadRequest,
			msg:  "store must be a boolean",
		}
	}
	return nil
}

var supportedAuggieResponsesIncludeValues = map[string]struct{}{
	"code_interpreter_call.outputs":         {},
	"computer_call_output.output.image_url": {},
	"file_search_call.results":              {},
	"message.input_image.image_url":         {},
	"message.output_text.logprobs":          {},
	"reasoning.encrypted_content":           {},
	"web_search_call.action.sources":        {},
	"web_search_call.results":               {},
}

func parseAuggieIncludeValues(rawJSON []byte) ([]string, error) {
	include := gjson.GetBytes(rawJSON, "include")
	if !include.Exists() {
		return nil, nil
	}
	if !include.IsArray() {
		return nil, statusErr{
			code: http.StatusBadRequest,
			msg:  "include must be an array",
		}
	}
	values := make([]string, 0, len(include.Array()))
	for index, item := range include.Array() {
		if item.Type != gjson.String {
			return nil, statusErr{
				code: http.StatusBadRequest,
				msg:  fmt.Sprintf("include[%d] must be a string", index),
			}
		}
		value := strings.TrimSpace(item.String())
		if value == "" {
			return nil, statusErr{
				code: http.StatusBadRequest,
				msg:  fmt.Sprintf("include[%d] must be a non-empty string", index),
			}
		}
		values = append(values, value)
	}
	return values, nil
}

func validateAuggieResponsesIncludeSupport(rawJSON []byte) error {
	values, err := parseAuggieIncludeValues(rawJSON)
	if err != nil {
		return err
	}
	for index, value := range values {
		if _, ok := supportedAuggieResponsesIncludeValues[value]; ok {
			continue
		}
		return statusErr{
			code: http.StatusBadRequest,
			msg:  fmt.Sprintf("include[%d]=%q is not supported on /v1/responses", index, value),
		}
	}
	return nil
}

func validateAuggieOpenAIIncludeSupport(rawJSON []byte) error {
	values, err := parseAuggieIncludeValues(rawJSON)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	return statusErr{
		code: http.StatusBadRequest,
		msg:  "include is not supported on /v1/chat/completions",
	}
}

func validateAuggieResponsesToolTypes(rawJSON []byte) error {
	return validateAuggieToolTypes(rawJSON, true)
}

func validateAuggieOpenAIToolTypes(rawJSON []byte) error {
	return validateAuggieToolTypes(rawJSON, false)
}

func validateAuggieToolTypes(rawJSON []byte, allowResponsesBuiltIns bool) error {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return nil
	}

	for index, tool := range tools.Array() {
		toolType := strings.TrimSpace(tool.Get("type").String())
		if toolType == "" || toolType == "function" {
			continue
		}
		if allowResponsesBuiltIns && toolType == "web_search" {
			continue
		}
		return statusErr{
			code: http.StatusBadRequest,
			msg:  fmt.Sprintf("tools[%d].type=%q is not supported by Auggie; only function tools are currently supported", index, toolType),
		}
	}

	return nil
}

func validateAuggieResponsesToolChoice(rawJSON []byte) error {
	return validateAuggieToolChoice(rawJSON, true)
}

func validateAuggieOpenAIToolChoice(rawJSON []byte) error {
	return validateAuggieToolChoice(rawJSON, false)
}

func validateAuggieToolChoice(rawJSON []byte, allowResponsesBuiltIns bool) error {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return nil
	}

	if toolChoice.Type == gjson.String {
		value := strings.TrimSpace(toolChoice.String())
		if value == "" || value == "auto" || value == "none" {
			return nil
		}
		return statusErr{
			code: http.StatusBadRequest,
			msg:  fmt.Sprintf("tool_choice=%q is not supported by Auggie; supported values are auto, none, or omitted tool_choice", value),
		}
	}

	if toolChoice.IsObject() {
		toolChoiceType := strings.TrimSpace(toolChoice.Get("type").String())
		if allowResponsesBuiltIns && toolChoiceType == "web_search" {
			return nil
		}
		if toolChoiceType == "" {
			toolChoiceType = "object"
		}
		return statusErr{
			code: http.StatusBadRequest,
			msg:  fmt.Sprintf("tool_choice.type=%q is not supported by Auggie; supported values are auto, none, or omitted tool_choice", toolChoiceType),
		}
	}

	return statusErr{
		code: http.StatusBadRequest,
		msg:  "tool_choice must be a string or object for Auggie requests",
	}
}

func validateAuggieResponsesInputItemTypes(rawJSON []byte) error {
	input := gjson.GetBytes(rawJSON, "input")
	if !input.Exists() || !input.IsArray() {
		return nil
	}

	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "" && strings.TrimSpace(item.Get("role").String()) != "" {
			itemType = "message"
		}

		switch itemType {
		case "", "message", "function_call", "function_call_output":
			continue
		case "item_reference":
			return statusErr{
				code: http.StatusBadRequest,
				msg: fmt.Sprintf(
					"input[%d].type=%q is not supported by Auggie /v1/responses because Auggie cannot resolve prior response item references; use previous_response_id for native continuation or a native OpenAI Responses route for manual item replay",
					index,
					itemType,
				),
			}
		case "reasoning":
			return statusErr{
				code: http.StatusBadRequest,
				msg: fmt.Sprintf(
					"input[%d].type=%q is not supported by Auggie /v1/responses because Auggie cannot accept prior reasoning items as input; use previous_response_id for native continuation or a native OpenAI Responses route for manual item replay",
					index,
					itemType,
				),
			}
		default:
			return statusErr{
				code: http.StatusBadRequest,
				msg:  fmt.Sprintf("input[%d].type=%q is not supported by Auggie /v1/responses; supported item types are message, function_call, and function_call_output", index, itemType),
			}
		}
	}

	return nil
}

func extractAuggieBuiltInToolCalls(openAIPayload []byte) ([]auggieBuiltInToolCall, bool, error) {
	if strings.TrimSpace(gjson.GetBytes(openAIPayload, "choices.0.finish_reason").String()) != "tool_calls" {
		return nil, false, nil
	}

	toolCalls := gjson.GetBytes(openAIPayload, "choices.0.message.tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() || len(toolCalls.Array()) == 0 {
		return nil, false, statusErr{code: http.StatusBadGateway, msg: "Auggie returned finish_reason=tool_calls without tool calls"}
	}

	out := make([]auggieBuiltInToolCall, 0, len(toolCalls.Array()))
	unsupportedNames := make([]string, 0, len(toolCalls.Array()))
	for index, toolCall := range toolCalls.Array() {
		name := strings.TrimSpace(toolCall.Get("function.name").String())
		normalizedName, ok := normalizeAuggieRemoteToolName(name)
		if !ok {
			unsupportedNames = append(unsupportedNames, name)
			continue
		}

		toolCallID := strings.TrimSpace(toolCall.Get("id").String())
		if toolCallID == "" {
			return nil, false, statusErr{code: http.StatusBadGateway, msg: fmt.Sprintf("Auggie built-in tool call %d is missing id", index)}
		}

		arguments := strings.TrimSpace(toolCall.Get("function.arguments").String())
		if arguments == "" {
			arguments = "{}"
		}

		out = append(out, auggieBuiltInToolCall{
			ID:        toolCallID,
			Name:      normalizedName,
			Arguments: arguments,
		})
	}

	if len(out) == 0 {
		return nil, false, nil
	}
	if len(unsupportedNames) > 0 {
		return nil, false, statusErr{
			code: http.StatusBadGateway,
			msg:  fmt.Sprintf("Auggie returned mixed built-in and client-executed tool calls; unsupported names: %s", strings.Join(unsupportedNames, ", ")),
		}
	}

	return out, true, nil
}

func normalizeAuggieRemoteToolName(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web-search", "web_search":
		return "web-search", true
	default:
		return "", false
	}
}

func buildAuggieToolContinuationRequest(baseTranslated []byte, state auggieConversationState, toolResults []auggieToolResultContinuation) ([]byte, error) {
	translated := bytes.Clone(baseTranslated)
	translated, err := applyAuggieConversationState(translated, state)
	if err != nil {
		return nil, err
	}

	nodes := make([]map[string]any, 0, len(toolResults))
	for index, toolResult := range toolResults {
		nodes = append(nodes, map[string]any{
			"id":   index + 1,
			"type": 1,
			"tool_result_node": map[string]any{
				"tool_use_id": toolResult.ToolUseID,
				"content":     toolResult.Content,
				"is_error":    toolResult.IsError,
			},
		})
	}

	nodesRaw, err := json.Marshal(nodes)
	if err != nil {
		return nil, err
	}

	translated, err = sjson.SetRawBytes(translated, "nodes", nodesRaw)
	if err != nil {
		return nil, err
	}
	return translated, nil
}

func (e *AuggieExecutor) runAuggieBuiltInToolCalls(ctx context.Context, auth *cliproxyauth.Auth, toolCalls []auggieBuiltInToolCall) ([]auggieToolResultContinuation, error) {
	registry, err := e.listAuggieRemoteTools(ctx, auth)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]auggieRemoteToolRegistryEntry, len(registry))
	for _, entry := range registry {
		name, ok := normalizeAuggieRemoteToolName(entry.ToolDefinition.Name)
		if !ok {
			continue
		}
		byName[name] = entry
	}

	out := make([]auggieToolResultContinuation, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		entry, ok := byName[toolCall.Name]
		if !ok {
			return nil, statusErr{code: http.StatusBadGateway, msg: fmt.Sprintf("Auggie remote tool registry missing tool %q", toolCall.Name)}
		}

		result, err := e.runAuggieRemoteTool(ctx, auth, entry, toolCall)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}

	return out, nil
}

func (e *AuggieExecutor) listAuggieRemoteTools(ctx context.Context, auth *cliproxyauth.Auth) ([]auggieRemoteToolRegistryEntry, error) {
	requestBody, err := json.Marshal(map[string]any{
		"tool_id_list": map[string]any{
			"tool_ids": defaultAuggieRemoteToolIDs,
		},
	})
	if err != nil {
		return nil, err
	}

	responseBody, err := e.executeAuggieJSON(ctx, auth, auggieListRemoteToolsPath, requestBody, true)
	if err != nil {
		return nil, err
	}

	var response auggieListRemoteToolsResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}
	return response.Tools, nil
}

func (e *AuggieExecutor) runAuggieRemoteTool(ctx context.Context, auth *cliproxyauth.Auth, entry auggieRemoteToolRegistryEntry, toolCall auggieBuiltInToolCall) (auggieToolResultContinuation, error) {
	requestBody, err := json.Marshal(map[string]any{
		"tool_name":       entry.ToolDefinition.Name,
		"tool_input_json": toolCall.Arguments,
		"tool_id":         entry.RemoteToolID,
	})
	if err != nil {
		return auggieToolResultContinuation{}, err
	}

	responseBody, err := e.executeAuggieJSON(ctx, auth, auggieRunRemoteToolPath, requestBody, true)
	if err != nil {
		return auggieToolResultContinuation{}, err
	}

	var response auggieRunRemoteToolResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return auggieToolResultContinuation{}, err
	}

	content := strings.TrimSpace(response.ToolOutput)
	if content == "" {
		content = strings.TrimSpace(response.ToolResultMessage)
	}

	return auggieToolResultContinuation{
		ToolUseID: toolCall.ID,
		Content:   content,
		IsError:   response.IsError,
	}, nil
}

func enrichAuggieOpenAIChatCompletionRequest(model string, _ []byte, translated []byte) ([]byte, error) {
	state, ok := lookupAuggieToolCallConversationState(model, translated)
	if !ok {
		return translated, nil
	}
	return applyAuggieConversationState(translated, state)
}

func lookupAuggieToolCallConversationState(model string, translated []byte) (auggieConversationState, bool) {
	toolCallIDs := auggieToolResultNodeIDs(translated)
	if len(toolCallIDs) == 0 {
		return auggieConversationState{}, false
	}

	model = strings.TrimSpace(model)
	var selected auggieConversationState
	for _, toolCallID := range toolCallIDs {
		state, ok := defaultAuggieToolCallStateStore.Load(toolCallID)
		if !ok {
			continue
		}
		if model != "" && strings.TrimSpace(state.Model) != "" && !strings.EqualFold(state.Model, model) {
			log.Debugf("auggie executor: ignoring tool call continuation state for %s due to model mismatch state=%s request=%s", toolCallID, state.Model, model)
			continue
		}
		if strings.TrimSpace(selected.ConversationID) == "" {
			selected = state
			continue
		}
		if selected.ConversationID != state.ConversationID || selected.TurnID != state.TurnID {
			log.Debugf("auggie executor: mismatched conversation state across tool call ids, using first match for %s", toolCallID)
		}
	}
	if strings.TrimSpace(selected.ConversationID) == "" || strings.TrimSpace(selected.TurnID) == "" {
		return auggieConversationState{}, false
	}
	return selected, true
}

func auggieToolResultNodeIDs(translated []byte) []string {
	nodes := gjson.GetBytes(translated, "nodes")
	if !nodes.Exists() || !nodes.IsArray() {
		return nil
	}

	var ids []string
	nodes.ForEach(func(_, node gjson.Result) bool {
		toolCallID := strings.TrimSpace(node.Get("tool_result_node.tool_use_id").String())
		if toolCallID != "" {
			ids = appendUniqueStrings(ids, toolCallID)
		}
		return true
	})
	return ids
}

func applyAuggieConversationState(translated []byte, state auggieConversationState) ([]byte, error) {
	var err error
	translated, err = sjson.SetBytes(translated, "conversation_id", state.ConversationID)
	if err != nil {
		return nil, err
	}
	translated, err = sjson.SetBytes(translated, "turn_id", state.TurnID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(state.ParentConversationID) != "" {
		translated, err = sjson.SetBytes(translated, "parent_conversation_id", state.ParentConversationID)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(state.RootConversationID) != "" {
		translated, err = sjson.SetBytes(translated, "root_conversation_id", state.RootConversationID)
		if err != nil {
			return nil, err
		}
	}
	return translated, nil
}

func updateAuggieConversationStateFromPayload(state *auggieConversationState, payload []byte) {
	if state == nil {
		return
	}
	if got := strings.TrimSpace(gjson.GetBytes(payload, "conversation_id").String()); got != "" {
		state.ConversationID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(payload, "turn_id").String()); got != "" {
		state.TurnID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(payload, "parent_conversation_id").String()); got != "" {
		state.ParentConversationID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(payload, "root_conversation_id").String()); got != "" {
		state.RootConversationID = got
	}
}

func openAIToolCallIDsFromChunk(chunk []byte) []string {
	toolCalls := gjson.GetBytes(chunk, "choices.0.delta.tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() {
		return nil
	}

	var ids []string
	toolCalls.ForEach(func(_, toolCall gjson.Result) bool {
		toolCallID := strings.TrimSpace(toolCall.Get("id").String())
		if toolCallID != "" {
			ids = appendUniqueStrings(ids, toolCallID)
		}
		return true
	})
	return ids
}

func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		exists := false
		for _, existing := range dst {
			if existing == value {
				exists = true
				break
			}
		}
		if !exists {
			dst = append(dst, value)
		}
	}
	return dst
}

func buildAuggieBridgeToOpenAIRequest(req cliproxyexecutor.Request, opts cliproxyexecutor.Options, from sdktranslator.Format, stream bool) (cliproxyexecutor.Request, []byte) {
	originalPayload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayload = opts.OriginalRequest
	}

	openAIReq := req
	openAIReq.Format = sdktranslator.FormatOpenAI
	openAIReq.Payload = sdktranslator.TranslateRequest(from, sdktranslator.FormatOpenAI, req.Model, req.Payload, stream)
	return openAIReq, originalPayload
}

func wrapOpenAISSEPayload(payload []byte) []byte {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil
	}
	if bytes.HasPrefix(trimmed, dataTag) {
		return bytes.Clone(trimmed)
	}

	out := make([]byte, 0, len(trimmed)+len(dataTag)+1)
	out = append(out, dataTag...)
	out = append(out, ' ')
	out = append(out, trimmed...)
	return out
}

func buildAuggieCompactOutput(body []byte) []any {
	root := gjson.ParseBytes(body)
	output := make([]any, 0, 8)

	if instructions := strings.TrimSpace(root.Get("instructions").String()); instructions != "" {
		output = append(output, map[string]any{
			"type": "message",
			"role": "system",
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": instructions,
				},
			},
		})
	}

	input := root.Get("input")
	if input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if normalized := normalizeAuggieCompactOutputItem(item); normalized != nil {
				output = append(output, normalized)
			}
			return true
		})
		return output
	}

	if input.Type == gjson.String {
		output = append(output, map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": input.String(),
				},
			},
		})
	}

	return output
}

func normalizeAuggieCompactOutputItem(item gjson.Result) any {
	if !item.Exists() {
		return nil
	}

	value := item.Value()
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}

	if strings.TrimSpace(item.Get("type").String()) != "" || strings.TrimSpace(item.Get("role").String()) == "" {
		return object
	}

	normalized := make(map[string]any, len(object)+1)
	for key, rawValue := range object {
		normalized[key] = rawValue
	}
	normalized["type"] = "message"
	return normalized
}

func countAuggieResponsesTokens(model string, payload []byte) (int64, error) {
	openAIPayload := sdktranslator.TranslateRequest(sdktranslator.FormatOpenAIResponse, sdktranslator.FormatOpenAI, model, payload, false)
	return countAuggieOpenAITokens(model, openAIPayload)
}

func countAuggieOpenAITokens(model string, payload []byte) (int64, error) {
	baseModel := thinking.ParseSuffix(model).ModelName
	enc, err := tokenizerForModel(baseModel)
	if err != nil {
		return 0, fmt.Errorf("auggie executor: tokenizer init failed: %w", err)
	}

	count, err := countOpenAIChatTokens(enc, payload)
	if err != nil {
		return 0, fmt.Errorf("auggie executor: token counting failed: %w", err)
	}
	return count, nil
}
