package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	Message              string
	ChatHistory          json.RawMessage
	ResponseNodes        json.RawMessage
	UpdatedAt            time.Time
}

type auggieConversationStateStore struct {
	mu    sync.Mutex
	items map[string]auggieConversationState
}

type auggieToolCallIDMapping struct {
	PublicID   string
	InternalID string
	UpdatedAt  time.Time
}

type auggieToolCallIDMappingStore struct {
	mu         sync.Mutex
	byPublicID map[string]auggieToolCallIDMapping
	byInternal map[string]auggieToolCallIDMapping
}

type auggieToolResultReplayMetadata struct {
	StartTimeMS  int64
	DurationMS   int64
	HasStartTime bool
	HasDuration  bool
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

var defaultAuggieToolCallIDMappingStore = &auggieToolCallIDMappingStore{
	byPublicID: make(map[string]auggieToolCallIDMapping),
	byInternal: make(map[string]auggieToolCallIDMapping),
}

var defaultAuggieRemoteToolIDs = []int{0, 1, 8, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}

var (
	auggieWorkspaceRootOnce sync.Once
	auggieWorkspaceRoot     string
	auggiePublicToolCallSeq uint64
	auggiePublicChatSeq     uint64
	auggiePublicResponseSeq uint64
)

const auggieSuspendedAccountStatusMessage = "Auggie upstream account is suspended or requires an active subscription"

type auggieRemoteToolDefinition struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	InputSchemaJSON string `json:"input_schema_json,omitempty"`
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

type auggieResponsesBuiltInToolOutput struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Query  string `json:"query,omitempty"`
	Output string `json:"output,omitempty"`
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
		payload, err = appendAuggieOpenAIChatCompletionMetadata(payload, originalAuggieOpenAIRequest(req, opts))
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

func originalAuggieOpenAIRequest(req cliproxyexecutor.Request, opts cliproxyexecutor.Options) []byte {
	if len(opts.OriginalRequest) > 0 {
		return opts.OriginalRequest
	}
	return req.Payload
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

func (s *auggieToolCallIDMappingStore) Store(publicID, internalID string) {
	publicID = strings.TrimSpace(publicID)
	internalID = strings.TrimSpace(internalID)
	if publicID == "" || internalID == "" || publicID == internalID {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)
	entry := auggieToolCallIDMapping{
		PublicID:   publicID,
		InternalID: internalID,
		UpdatedAt:  now,
	}
	s.byPublicID[publicID] = entry
	s.byInternal[internalID] = entry
}

func (s *auggieToolCallIDMappingStore) LoadInternal(publicID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	entry, ok := s.byPublicID[strings.TrimSpace(publicID)]
	if !ok {
		return "", false
	}
	return entry.InternalID, true
}

func (s *auggieToolCallIDMappingStore) LoadPublic(internalID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	entry, ok := s.byInternal[strings.TrimSpace(internalID)]
	if !ok {
		return "", false
	}
	return entry.PublicID, true
}

func (s *auggieToolCallIDMappingStore) cleanupLocked(now time.Time) {
	cutoff := now.Add(-auggieResponsesStateTTL)
	for publicID, entry := range s.byPublicID {
		if entry.UpdatedAt.IsZero() || entry.UpdatedAt.Before(cutoff) {
			delete(s.byPublicID, publicID)
			delete(s.byInternal, entry.InternalID)
		}
	}
}

func newAuggiePublicToolCallID() string {
	return fmt.Sprintf("call_%x_%d", time.Now().UnixNano(), atomic.AddUint64(&auggiePublicToolCallSeq, 1))
}

func newAuggiePublicChatCompletionID() string {
	return fmt.Sprintf("chatcmpl-%x_%d", time.Now().UnixNano(), atomic.AddUint64(&auggiePublicChatSeq, 1))
}

func newAuggiePublicResponsesID() string {
	return fmt.Sprintf("resp_%x_%d", time.Now().UnixNano(), atomic.AddUint64(&auggiePublicResponseSeq, 1))
}

func publicAuggieResponsesID(openAIResponseID string) string {
	openAIResponseID = strings.TrimSpace(openAIResponseID)
	if openAIResponseID == "" {
		return newAuggiePublicResponsesID()
	}
	if strings.HasPrefix(openAIResponseID, "resp_") {
		return openAIResponseID
	}
	if strings.HasPrefix(openAIResponseID, "chatcmpl-") {
		return "resp_" + strings.TrimPrefix(openAIResponseID, "chatcmpl-")
	}
	if strings.HasPrefix(openAIResponseID, "chatcmpl_") {
		return "resp_" + strings.TrimPrefix(openAIResponseID, "chatcmpl_")
	}

	var b strings.Builder
	b.Grow(len(openAIResponseID))
	for _, r := range openAIResponseID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	sanitized := strings.Trim(b.String(), "_")
	if sanitized == "" {
		return newAuggiePublicResponsesID()
	}
	return "resp_" + sanitized
}

func publicAuggieToolCallID(toolCallID string) string {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID != "" && strings.HasPrefix(toolCallID, "call_") {
		return toolCallID
	}
	if toolCallID != "" {
		if publicID, ok := defaultAuggieToolCallIDMappingStore.LoadPublic(toolCallID); ok {
			return publicID
		}
	}

	publicID := newAuggiePublicToolCallID()
	if toolCallID != "" {
		defaultAuggieToolCallIDMappingStore.Store(publicID, toolCallID)
	}
	return publicID
}

func resolveAuggieInternalToolCallID(toolCallID string) string {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return ""
	}
	if internalID, ok := defaultAuggieToolCallIDMappingStore.LoadInternal(toolCallID); ok {
		return internalID
	}
	return toolCallID
}

func rewriteOpenAIToolCallIDs(payload []byte) ([]byte, error) {
	toolCalls := gjson.GetBytes(payload, "choices.0.delta.tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() {
		return payload, nil
	}

	var err error
	for index, toolCall := range toolCalls.Array() {
		publicID := publicAuggieToolCallID(toolCall.Get("id").String())
		if publicID == "" {
			continue
		}
		payload, err = sjson.SetBytes(payload, fmt.Sprintf("choices.0.delta.tool_calls.%d.id", index), publicID)
		if err != nil {
			return nil, err
		}
	}

	return payload, nil
}

func rewriteAuggieRequestToolCallIDs(translated []byte) ([]byte, error) {
	var err error

	chatHistory := gjson.GetBytes(translated, "chat_history")
	if chatHistory.Exists() && chatHistory.IsArray() {
		for historyIndex, entry := range chatHistory.Array() {
			responseNodes := entry.Get("response_nodes")
			if !responseNodes.Exists() || !responseNodes.IsArray() {
				continue
			}
			for nodeIndex, node := range responseNodes.Array() {
				toolCallID := strings.TrimSpace(node.Get("tool_use.tool_use_id").String())
				internalID := resolveAuggieInternalToolCallID(toolCallID)
				if internalID == "" || internalID == toolCallID {
					continue
				}
				translated, err = sjson.SetBytes(translated, fmt.Sprintf("chat_history.%d.response_nodes.%d.tool_use.tool_use_id", historyIndex, nodeIndex), internalID)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	nodes := gjson.GetBytes(translated, "nodes")
	if nodes.Exists() && nodes.IsArray() {
		for nodeIndex, node := range nodes.Array() {
			toolCallID := strings.TrimSpace(node.Get("tool_result_node.tool_use_id").String())
			internalID := resolveAuggieInternalToolCallID(toolCallID)
			if internalID == "" || internalID == toolCallID {
				continue
			}
			translated, err = sjson.SetBytes(translated, fmt.Sprintf("nodes.%d.tool_result_node.tool_use_id", nodeIndex), internalID)
			if err != nil {
				return nil, err
			}
		}
	}

	return translated, nil
}

func (e *AuggieExecutor) executeAuggieStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, translated []byte, from sdktranslator.Format, allowRefresh bool) (result *cliproxyexecutor.StreamResult, err error) {
	if auth == nil {
		return nil, statusErr{code: http.StatusInternalServerError, msg: "auggie executor: auth is nil"}
	}
	translated, err = enrichAuggieIDEStateNode(translated)
	if err != nil {
		return nil, err
	}
	translated, err = enrichAuggieToolResultNodes(translated)
	if err != nil {
		return nil, err
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
		if fallbackPayload, ok := buildAuggieSystemPromptCustomizationFallbackPayload(translated, body); ok {
			log.Warn("auggie executor: upstream rejected system prompt customization controls; retrying request without native system_prompt fields")
			return e.executeAuggieStream(ctx, auth, req, opts, fallbackPayload, from, allowRefresh)
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
		seedAuggieConversationStateFromRequest(&conversationState, translated)
		var toolCallIDs []string
		var internalToolCallIDs []string
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
			if err := detectAuggieSuspendedAccountStatusErr(payload); err != nil {
				recordAPIResponseError(ctx, e.cfg, err)
				reporter.publishFailure(ctx)
				replaceAuggieAuthState(auth, markAuggieAuthForbidden(auth, err.Error()))
				out <- cliproxyexecutor.StreamChunk{Err: err}
				return
			}
			updateAuggieConversationStateFromPayload(&conversationState, payload)
			if detail, ok := parseAuggieUsage(payload); ok {
				reporter.publish(ctx, detail)
			}
			if from == sdktranslator.FormatOpenAI {
				internalToolCallIDs = appendUniqueStrings(internalToolCallIDs, auggieToolCallIDsFromPayload(payload)...)
			}

			chunks := sdktranslator.TranslateStream(ctx, sdktranslator.FormatAuggie, from, responseModel, opts.OriginalRequest, translated, payload, &param)
			for i := range chunks {
				chunkPayload := []byte(chunks[i])
				if from == sdktranslator.FormatOpenAI {
					chunkPayload, err = rewriteOpenAIToolCallIDs(chunkPayload)
					if err != nil {
						recordAPIResponseError(ctx, e.cfg, err)
						reporter.publishFailure(ctx)
						out <- cliproxyexecutor.StreamChunk{Err: err}
						return
					}
				}
				if opts.SourceFormat == sdktranslator.FormatOpenAIResponse && translatedResponseID == "" {
					if got := strings.TrimSpace(gjson.GetBytes(chunkPayload, "id").String()); got != "" {
						translatedResponseID = got
					}
				}
				if from == sdktranslator.FormatOpenAI {
					toolCallIDs = appendUniqueStrings(toolCallIDs, openAIToolCallIDsFromChunk(chunkPayload)...)
				}
				out <- cliproxyexecutor.StreamChunk{Payload: bytes.Clone(chunkPayload)}
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
		if opts.SourceFormat == sdktranslator.FormatOpenAIResponse && shouldPersistAuggieResponsesState(opts.OriginalRequest) {
			if translatedResponseID == "" {
				translatedResponseID = newAuggiePublicChatCompletionID()
			}
			if translatedResponseID != "" {
				defaultAuggieResponsesStateStore.Store(translatedResponseID, conversationState)
				publicResponseID := publicAuggieResponsesID(translatedResponseID)
				if publicResponseID != "" && publicResponseID != translatedResponseID {
					defaultAuggieResponsesStateStore.Store(publicResponseID, conversationState)
				}
			}
		}
		if len(toolCallIDs) > 0 {
			for _, toolCallID := range toolCallIDs {
				defaultAuggieToolCallStateStore.Store(toolCallID, conversationState)
			}
		}
		if len(internalToolCallIDs) > 0 {
			for _, toolCallID := range internalToolCallIDs {
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

	for attempt := 0; attempt < 2; attempt++ {
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
			if attempt == 0 && isRetryableAuggieJSONReadErr(err) {
				log.Warnf("auggie executor: transient upstream JSON request error on %s, retrying once: %v", requestURL, err)
				continue
			}
			return nil, err
		}

		recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
		responseBody, readErr := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("auggie executor: close response body error: %v", errClose)
		}
		if len(responseBody) > 0 {
			appendAPIResponseChunk(ctx, e.cfg, responseBody)
		}
		if readErr != nil {
			recordAPIResponseError(ctx, e.cfg, readErr)
			if attempt == 0 && isRetryableAuggieJSONReadErr(readErr) {
				log.Warnf("auggie executor: transient upstream JSON read error on %s, retrying once: %v", requestURL, readErr)
				continue
			}
			return nil, readErr
		}

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

	return nil, statusErr{code: http.StatusBadGateway, msg: "auggie JSON request exhausted retries"}
}

func isRetryableAuggieJSONReadErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unexpected eof")
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

func markAuggieAuthForbidden(auth *cliproxyauth.Auth, message string) *cliproxyauth.Auth {
	if auth == nil {
		return nil
	}

	updated := auth.Clone()
	now := time.Now().UTC()
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(http.StatusForbidden)
	}

	updated.Unavailable = true
	updated.Status = cliproxyauth.StatusError
	updated.StatusMessage = "forbidden"
	updated.LastError = &cliproxyauth.Error{
		Code:       "forbidden",
		Message:    message,
		Retryable:  false,
		HTTPStatus: http.StatusForbidden,
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
		responseID = newAuggiePublicChatCompletionID()
	}

	assistantContent := any(content.String())
	if content.Len() == 0 && len(toolCalls) > 0 {
		assistantContent = nil
	}

	choice := map[string]any{
		"index": 0,
		"message": map[string]any{
			"role":    "assistant",
			"content": assistantContent,
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

func detectAuggieSuspendedAccountStatusErr(payload []byte) error {
	if !auggiePayloadContainsSuspendedAccountBanner(payload) {
		return nil
	}
	return statusErr{code: http.StatusForbidden, msg: auggieSuspendedAccountStatusMessage}
}

func auggiePayloadContainsSuspendedAccountBanner(payload []byte) bool {
	if text := strings.TrimSpace(gjson.GetBytes(payload, "text").String()); isAuggieSuspendedAccountBannerText(text) {
		return true
	}

	nodes := gjson.GetBytes(payload, "nodes")
	if !nodes.Exists() || !nodes.IsArray() {
		return false
	}

	for _, node := range nodes.Array() {
		if isAuggieSuspendedAccountBannerText(node.Get("content").String()) {
			return true
		}
	}
	return false
}

func isAuggieSuspendedAccountBannerText(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "your account") &&
		strings.Contains(normalized, "has been suspended") &&
		strings.Contains(normalized, "purchase a subscription")
}

func buildAuggieSystemPromptCustomizationFallbackPayload(translated, upstreamBody []byte) ([]byte, bool) {
	if !auggiePayloadContainsSystemPromptCustomizationDisabledMessage(upstreamBody) {
		return nil, false
	}

	// Keep the instructions by inlining them into the chat message/history when
	// Auggie account features reject native system prompt customization controls.
	var promptSegments []string
	for _, path := range []string{"system_prompt", "system_prompt_append", "system_prompt_override"} {
		value := strings.TrimSpace(gjson.GetBytes(translated, path).String())
		if value != "" {
			promptSegments = append(promptSegments, value)
		}
	}
	inlinePrompt := strings.TrimSpace(strings.Join(promptSegments, "\n\n"))

	sanitized := bytes.Clone(translated)
	removedAny := false
	for _, path := range []string{"system_prompt", "system_prompt_append", "system_prompt_override", "system_prompt_replacements"} {
		if value := gjson.GetBytes(sanitized, path); !value.Exists() || value.Type == gjson.Null {
			continue
		}
		next, err := sjson.DeleteBytes(sanitized, path)
		if err != nil {
			return nil, false
		}
		sanitized = next
		removedAny = true
	}
	if !removedAny {
		return nil, false
	}

	if inlinePrompt == "" {
		return sanitized, true
	}

	withInlinePrompt, err := injectAuggieSystemPromptIntoFallbackConversation(sanitized, inlinePrompt)
	if err != nil {
		return nil, false
	}
	return withInlinePrompt, true
}

func auggiePayloadContainsSystemPromptCustomizationDisabledMessage(payload []byte) bool {
	candidates := []string{
		gjson.GetBytes(payload, "error.message").String(),
		gjson.GetBytes(payload, "error").String(),
		gjson.GetBytes(payload, "message").String(),
		string(payload),
	}
	for _, candidate := range candidates {
		if isAuggieSystemPromptCustomizationDisabledMessage(candidate) {
			return true
		}
	}
	return false
}

func isAuggieSystemPromptCustomizationDisabledMessage(message string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(message), " "))
	if normalized == "" {
		return false
	}
	if !strings.Contains(normalized, "system prompt customization") {
		return false
	}
	return strings.Contains(normalized, "not enabled") || strings.Contains(normalized, "disabled")
}

func injectAuggieSystemPromptIntoFallbackConversation(translated []byte, prompt string) ([]byte, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return translated, nil
	}

	message := strings.TrimSpace(gjson.GetBytes(translated, "message").String())
	if message != "" {
		return sjson.SetBytes(translated, "message", prompt+"\n\n"+message)
	}

	chatHistory := gjson.GetBytes(translated, "chat_history")
	if chatHistory.Exists() && chatHistory.IsArray() {
		entries := chatHistory.Array()
		for i := len(entries) - 1; i >= 0; i-- {
			requestMessage := strings.TrimSpace(entries[i].Get("request_message").String())
			if requestMessage == "" {
				continue
			}
			return sjson.SetBytes(translated, fmt.Sprintf("chat_history.%d.request_message", i), prompt+"\n\n"+requestMessage)
		}
	}

	return sjson.SetBytes(translated, "message", prompt)
}

func appendAuggieOpenAIChatCompletionMetadata(payload, requestRawJSON []byte) ([]byte, error) {
	if len(payload) == 0 {
		return payload, nil
	}

	metadata := gjson.GetBytes(requestRawJSON, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return sjson.SetBytes(payload, "metadata", nil)
	}
	if !metadata.IsObject() {
		return payload, nil
	}
	return sjson.SetBytes(payload, "metadata", metadata.Value())
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
	builtInRegistry, translated, err := e.prepareAuggieResponsesBuiltInToolBridge(ctx, auth, originalPayload, translated)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	openAIPayload, headers, err := e.executeAuggieResponsesTurn(ctx, auth, resolvedReq, opts, translated)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	openAIPayload, headers, err = e.completeAuggieResponsesBuiltInToolLoop(ctx, auth, resolvedReq, opts, originalPayload, translated, builtInRegistry, openAIPayload, headers)
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
	storeAuggieResponsesStateForFinalResponseID(originalPayload, openAIPayload, []byte(translatedResponse))

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

func (e *AuggieExecutor) completeAuggieResponsesBuiltInToolLoop(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, requestRawJSON, baseTranslated []byte, registry []auggieRemoteToolRegistryEntry, openAIPayload []byte, headers http.Header) ([]byte, http.Header, error) {
	currentPayload := openAIPayload
	currentHeaders := headers
	builtInOutputs := make([]auggieResponsesBuiltInToolOutput, 0)
	maxToolCalls, limitBuiltInCalls := parseAuggieResponsesMaxBuiltInToolCalls(requestRawJSON)
	processedBuiltInCalls := 0

	for continuationCount := 0; continuationCount < auggieMaxInternalToolContinuations; continuationCount++ {
		toolCalls, shouldContinue, err := extractAuggieBuiltInToolCalls(currentPayload)
		if err != nil {
			return nil, nil, err
		}
		if !shouldContinue {
			currentPayload, err = finalizeAuggieResponsesBuiltInToolPayload(currentPayload, builtInOutputs, false)
			if err != nil {
				return nil, nil, err
			}
			return currentPayload, currentHeaders, nil
		}
		if limitBuiltInCalls {
			remaining := maxToolCalls - processedBuiltInCalls
			if remaining <= 0 {
				currentPayload, err = finalizeAuggieResponsesBuiltInToolPayload(currentPayload, builtInOutputs, true)
				if err != nil {
					return nil, nil, err
				}
				return currentPayload, currentHeaders, nil
			}
			if len(toolCalls) > remaining {
				toolCalls = append([]auggieBuiltInToolCall(nil), toolCalls[:remaining]...)
			}
		}

		responseID := strings.TrimSpace(gjson.GetBytes(currentPayload, "id").String())
		if responseID == "" {
			return nil, nil, statusErr{code: http.StatusBadGateway, msg: "missing response id for Auggie built-in tool continuation"}
		}
		state, ok := defaultAuggieResponsesStateStore.Load(responseID)
		if !ok {
			return nil, nil, statusErr{code: http.StatusBadGateway, msg: fmt.Sprintf("missing Auggie conversation state for built-in tool continuation: %s", responseID)}
		}

		toolResults, err := e.runAuggieBuiltInToolCalls(ctx, auth, registry, toolCalls)
		if err != nil {
			return nil, nil, err
		}
		builtInOutputs = append(builtInOutputs, buildAuggieResponsesBuiltInToolOutputs(toolCalls, toolResults)...)
		processedBuiltInCalls += len(toolCalls)

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

func parseAuggieResponsesMaxBuiltInToolCalls(requestRawJSON []byte) (int, bool) {
	maxToolCalls := gjson.GetBytes(requestRawJSON, "max_tool_calls")
	if !maxToolCalls.Exists() || maxToolCalls.Type == gjson.Null {
		return 0, false
	}
	return int(maxToolCalls.Int()), true
}

func finalizeAuggieResponsesBuiltInToolPayload(openAIPayload []byte, outputs []auggieResponsesBuiltInToolOutput, suppressToolCalls bool) ([]byte, error) {
	payload := openAIPayload
	var err error
	if suppressToolCalls {
		payload, err = suppressAuggieResponsesBuiltInToolCalls(payload)
		if err != nil {
			return nil, err
		}
	}
	if len(outputs) == 0 {
		return payload, nil
	}
	return attachAuggieResponsesBuiltInToolOutputs(payload, outputs)
}

func suppressAuggieResponsesBuiltInToolCalls(openAIPayload []byte) ([]byte, error) {
	toolCalls := gjson.GetBytes(openAIPayload, "choices.0.message.tool_calls")
	if !toolCalls.Exists() {
		return openAIPayload, nil
	}
	return sjson.DeleteBytes(openAIPayload, "choices.0.message.tool_calls")
}

func (e *AuggieExecutor) executeOpenAIResponsesStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	resolvedReq := req
	resolvedReq.Model = resolveAuggieModelAlias(auth, req.Model)
	translated, originalPayload, err := buildAuggieResponsesTranslatedRequest(resolvedReq.Model, req, opts, true)
	if err != nil {
		return nil, err
	}
	if auggieResponsesRequestUsesBuiltInToolBridge(originalPayload) {
		builtInRegistry, translated, err := e.prepareAuggieResponsesBuiltInToolBridge(ctx, auth, originalPayload, translated)
		if err != nil {
			return nil, err
		}
		openAIPayload, headers, err := e.executeAuggieResponsesTurn(ctx, auth, resolvedReq, opts, translated)
		if err != nil {
			return nil, err
		}
		openAIPayload, headers, err = e.completeAuggieResponsesBuiltInToolLoop(ctx, auth, resolvedReq, opts, originalPayload, translated, builtInRegistry, openAIPayload, headers)
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
			if isAuggieResponsesBuiltInWebSearchType(tool.Get("type").String()) {
				return true
			}
		}
	}

	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	return toolChoice.IsObject() && isAuggieResponsesBuiltInWebSearchType(toolChoice.Get("type").String())
}

func translatedAuggieRequestUsesBuiltInToolDefinitions(translated []byte) bool {
	toolDefinitions := gjson.GetBytes(translated, "tool_definitions")
	if !toolDefinitions.Exists() || !toolDefinitions.IsArray() {
		return false
	}

	for _, toolDefinition := range toolDefinitions.Array() {
		if _, ok := normalizeAuggieRemoteToolName(toolDefinition.Get("name").String()); ok {
			return true
		}
	}
	return false
}

func (e *AuggieExecutor) prepareAuggieResponsesBuiltInToolBridge(ctx context.Context, auth *cliproxyauth.Auth, originalPayload, translated []byte) ([]auggieRemoteToolRegistryEntry, []byte, error) {
	if !auggieResponsesRequestUsesBuiltInToolBridge(originalPayload) || !translatedAuggieRequestUsesBuiltInToolDefinitions(translated) {
		return nil, translated, nil
	}

	registry, err := e.listAuggieRemoteTools(ctx, auth)
	if err != nil {
		return nil, nil, err
	}
	translated, err = enrichAuggieBuiltInToolDefinitions(translated, registry)
	if err != nil {
		return nil, nil, err
	}
	return registry, translated, nil
}

func enrichAuggieBuiltInToolDefinitions(translated []byte, registry []auggieRemoteToolRegistryEntry) ([]byte, error) {
	toolDefinitions := gjson.GetBytes(translated, "tool_definitions")
	if !toolDefinitions.Exists() || !toolDefinitions.IsArray() {
		return translated, nil
	}

	remoteDefinitions := make(map[string]auggieRemoteToolDefinition, len(registry))
	for _, entry := range registry {
		normalizedName, ok := normalizeAuggieRemoteToolName(entry.ToolDefinition.Name)
		if !ok {
			continue
		}
		remoteDefinitions[normalizedName] = entry.ToolDefinition
	}

	missing := make([]string, 0)
	var err error
	for index, toolDefinition := range toolDefinitions.Array() {
		normalizedName, ok := normalizeAuggieRemoteToolName(toolDefinition.Get("name").String())
		if !ok {
			continue
		}
		remoteDefinition, ok := remoteDefinitions[normalizedName]
		if !ok {
			missing = appendUniqueStrings(missing, toolDefinition.Get("name").String())
			continue
		}

		definitionRaw, marshalErr := json.Marshal(remoteDefinition)
		if marshalErr != nil {
			return nil, marshalErr
		}
		translated, err = sjson.SetRawBytes(translated, fmt.Sprintf("tool_definitions.%d", index), definitionRaw)
		if err != nil {
			return nil, err
		}
	}

	if len(missing) > 0 {
		return nil, statusErr{code: http.StatusBadGateway, msg: fmt.Sprintf("Auggie remote tool registry missing definition for %s", strings.Join(missing, ", "))}
	}
	return translated, nil
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
		responseID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
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
	if builtInOutputs := root.Get("_cliproxy_builtin_tool_outputs"); builtInOutputs.Exists() && builtInOutputs.Type != gjson.Null {
		firstChunk["_cliproxy_builtin_tool_outputs"] = builtInOutputs.Value()
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
		translated, err := rewriteAuggieRequestToolCallIDs(translated)
		if err != nil {
			return nil, originalPayload, err
		}
		return translated, originalPayload, nil
	}

	state, ok := defaultAuggieResponsesStateStore.Load(previousResponseID)
	if !ok {
		return nil, originalPayload, newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("unknown previous_response_id: %s; Auggie /v1/responses continuation requires a stored prior response (omit store or set store=true on the previous turn)", previousResponseID),
			"previous_response_id",
			"invalid_value",
		)
	}
	if strings.TrimSpace(state.ConversationID) == "" || strings.TrimSpace(state.TurnID) == "" {
		return nil, originalPayload, newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("missing Auggie conversation state for previous_response_id: %s; Auggie /v1/responses continuation requires a stored prior response (omit store or set store=true on the previous turn)", previousResponseID),
			"previous_response_id",
			"invalid_value",
		)
	}

	var err error
	translated, err = applyAuggieConversationState(translated, state)
	if err != nil {
		return nil, originalPayload, err
	}
	translated, err = restoreAuggieConversationReplayContext(translated, state)
	if err != nil {
		return nil, originalPayload, err
	}
	translated, err = rewriteAuggieRequestToolCallIDs(translated)
	if err != nil {
		return nil, originalPayload, err
	}
	return translated, originalPayload, nil
}

func validateAuggieResponsesRequestCapabilities(rawJSON []byte) error {
	if err := validateAuggieStoreSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieParallelToolCalls(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieMaxToolCalls(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieMaxOutputTokens(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieTopLogprobs(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieTemperature(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieTopP(rawJSON); err != nil {
		return err
	}
	if err := validateAuggiePromptCacheKey(rawJSON); err != nil {
		return err
	}
	if err := validateAuggiePromptCacheRetention(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieSafetyIdentifier(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieUser(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieServiceTier(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesPromptTemplateSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesContextManagementSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesTextOptions(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesPromptCacheAndSafetyControlSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesServiceTierSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesTruncationSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesSamplingControlSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesReasoning(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesIncludeSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesTextFormatSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesToolTypes(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieResponsesFunctionToolStrictSupport(rawJSON); err != nil {
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
	if err := validateAuggieOpenAIRequestMetadata(rawJSON, "metadata"); err != nil {
		return err
	}
	if err := validateAuggieParallelToolCalls(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIVerbositySupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIWebSearchOptionsSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAISamplingControlSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAINStopAndSeedSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIChatStreamOptionsSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIServiceTierSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIPromptCacheAndUserSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIAudioOutputSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIIncludeSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIResponseFormatSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIReasoningEffort(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAILegacyFunctionSupport(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIToolTypes(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAIChatFunctionToolStrictSupport(rawJSON); err != nil {
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
		return newAuggieInvalidRequestStatusErr(
			"store must be a boolean for Auggie requests",
			"store",
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieOpenAIVerbositySupport(rawJSON []byte) error {
	verbosity := gjson.GetBytes(rawJSON, "verbosity")
	if !verbosity.Exists() || verbosity.Type == gjson.Null {
		return nil
	}
	if verbosity.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"verbosity must be a string for Auggie requests",
			"verbosity",
			"invalid_type",
		)
	}

	value := strings.ToLower(strings.TrimSpace(verbosity.String()))
	if _, ok := supportedAuggieOpenAIChatVerbosityValues[value]; !ok {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("verbosity=%q is not supported by Auggie; supported values are low, medium, or high", verbosity.String()),
			"verbosity",
			"invalid_value",
		)
	}

	return newAuggieInvalidRequestStatusErr(
		"verbosity is not supported by Auggie; the bridge cannot preserve official chat verbosity controls, so use a native OpenAI-compatible route",
		"verbosity",
		"invalid_value",
	)
}

func validateAuggieOpenAIWebSearchOptionsSupport(rawJSON []byte) error {
	options := gjson.GetBytes(rawJSON, "web_search_options")
	if !options.Exists() || options.Type == gjson.Null {
		return nil
	}
	if !options.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			"web_search_options must be an object for Auggie requests",
			"web_search_options",
			"invalid_type",
		)
	}

	searchContextSize := options.Get("search_context_size")
	if searchContextSize.Exists() && searchContextSize.Type != gjson.Null {
		if searchContextSize.Type != gjson.String {
			return newAuggieInvalidRequestStatusErr(
				"web_search_options.search_context_size must be a string for Auggie requests",
				"web_search_options.search_context_size",
				"invalid_type",
			)
		}
		value := strings.ToLower(strings.TrimSpace(searchContextSize.String()))
		if _, ok := supportedAuggieOpenAIChatWebSearchContextSizeValues[value]; !ok {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("web_search_options.search_context_size=%q is not supported by Auggie; supported values are low, medium, or high", searchContextSize.String()),
				"web_search_options.search_context_size",
				"invalid_value",
			)
		}
	}

	userLocation := options.Get("user_location")
	if userLocation.Exists() && userLocation.Type != gjson.Null {
		if !userLocation.IsObject() {
			return newAuggieInvalidRequestStatusErr(
				"web_search_options.user_location must be an object for Auggie requests",
				"web_search_options.user_location",
				"invalid_type",
			)
		}

		locationType := userLocation.Get("type")
		if locationType.Exists() && locationType.Type != gjson.Null {
			if locationType.Type != gjson.String {
				return newAuggieInvalidRequestStatusErr(
					"web_search_options.user_location.type must be a string for Auggie requests",
					"web_search_options.user_location.type",
					"invalid_type",
				)
			}
			if value := strings.ToLower(strings.TrimSpace(locationType.String())); value != "approximate" {
				return newAuggieInvalidRequestStatusErr(
					fmt.Sprintf("web_search_options.user_location.type=%q is not supported by Auggie; supported value is approximate", locationType.String()),
					"web_search_options.user_location.type",
					"invalid_value",
				)
			}
		}

		approximate := userLocation.Get("approximate")
		if approximate.Exists() && approximate.Type != gjson.Null {
			if !approximate.IsObject() {
				return newAuggieInvalidRequestStatusErr(
					"web_search_options.user_location.approximate must be an object for Auggie requests",
					"web_search_options.user_location.approximate",
					"invalid_type",
				)
			}
			for _, field := range []string{"city", "country", "region", "timezone"} {
				value := approximate.Get(field)
				if !value.Exists() || value.Type == gjson.Null {
					continue
				}
				if value.Type != gjson.String {
					return newAuggieInvalidRequestStatusErr(
						fmt.Sprintf("web_search_options.user_location.approximate.%s must be a string for Auggie requests", field),
						"web_search_options.user_location.approximate."+field,
						"invalid_type",
					)
				}
			}
		}
	}

	return newAuggieInvalidRequestStatusErr(
		"web_search_options is not supported by Auggie; the bridge cannot preserve official chat web-search activation and configuration semantics, so use a native OpenAI-compatible route",
		"web_search_options",
		"invalid_value",
	)
}

func validateAuggieParallelToolCalls(rawJSON []byte) error {
	parallelToolCalls := gjson.GetBytes(rawJSON, "parallel_tool_calls")
	if !parallelToolCalls.Exists() || parallelToolCalls.Type == gjson.Null {
		return nil
	}
	if parallelToolCalls.Type != gjson.True && parallelToolCalls.Type != gjson.False {
		return newAuggieInvalidRequestStatusErr(
			"parallel_tool_calls must be a boolean",
			"parallel_tool_calls",
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieOpenAIRequestMetadata(rawJSON []byte, field string) error {
	metadata := gjson.GetBytes(rawJSON, field)
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return nil
	}
	if !metadata.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be an object for Auggie requests", field),
			field,
			"invalid_type",
		)
	}

	items := metadata.Map()
	if len(items) > auggieOpenAIMetadataMaxPairs {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must contain at most %d key-value pairs for Auggie requests", field, auggieOpenAIMetadataMaxPairs),
			field,
			"invalid_value",
		)
	}
	for key, value := range items {
		itemField := field + "." + key
		if len(key) > auggieOpenAIMetadataKeyMaxLength {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s keys must be at most %d characters for Auggie requests", itemField, auggieOpenAIMetadataKeyMaxLength),
				itemField,
				"invalid_value",
			)
		}
		if value.Type != gjson.String {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s must be a string for Auggie requests", itemField),
				itemField,
				"invalid_type",
			)
		}
		if len(value.String()) > auggieOpenAIMetadataValueMaxLength {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s must be at most %d characters for Auggie requests", itemField, auggieOpenAIMetadataValueMaxLength),
				itemField,
				"invalid_value",
			)
		}
	}
	return nil
}

func validateAuggieOptionalIntegerField(rawJSON []byte, field string) error {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if value.Type != gjson.Number || float64(value.Int()) != value.Float() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be an integer", field),
			field,
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieOptionalBooleanField(rawJSON []byte, field string) error {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if value.Type != gjson.True && value.Type != gjson.False {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a boolean", field),
			field,
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieOptionalStopField(rawJSON []byte) error {
	value := gjson.GetBytes(rawJSON, "stop")
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}

	if value.Type == gjson.String {
		return nil
	}
	if value.Type != gjson.JSON || !value.IsArray() {
		return newAuggieInvalidRequestStatusErr(
			"stop must be a string or an array of strings",
			"stop",
			"invalid_type",
		)
	}

	items := value.Array()
	if len(items) > 4 {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("stop must contain at most 4 stop sequences, got %d", len(items)),
			"stop",
			"invalid_value",
		)
	}
	for index, item := range items {
		if item.Type != gjson.String {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("stop[%d] must be a string", index),
				fmt.Sprintf("stop[%d]", index),
				"invalid_type",
			)
		}
	}
	return nil
}

func validateAuggieOptionalNumberField(rawJSON []byte, field string) error {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if value.Type != gjson.Number {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a number", field),
			field,
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieOptionalIntegerRangeField(rawJSON []byte, field string, minValue int64, maxValue int64) error {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if err := validateAuggieOptionalIntegerField(rawJSON, field); err != nil {
		return err
	}

	intValue := value.Int()
	if intValue < minValue || intValue > maxValue {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be an integer between %d and %d", field, minValue, maxValue),
			field,
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieOptionalNumberRangeField(rawJSON []byte, field string, minValue float64, maxValue float64) error {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if err := validateAuggieOptionalNumberField(rawJSON, field); err != nil {
		return err
	}

	numberValue := value.Float()
	if numberValue < minValue || numberValue > maxValue {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a number between %g and %g", field, minValue, maxValue),
			field,
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieMaxToolCalls(rawJSON []byte) error {
	return validateAuggieOptionalIntegerField(rawJSON, "max_tool_calls")
}

func validateAuggieMaxOutputTokens(rawJSON []byte) error {
	if err := validateAuggieOptionalIntegerField(rawJSON, "max_output_tokens"); err != nil {
		return err
	}
	return validateAuggieOptionalIntegerField(rawJSON, "max_tokens")
}

func validateAuggieTopLogprobs(rawJSON []byte) error {
	return validateAuggieOptionalIntegerRangeField(rawJSON, "top_logprobs", 0, 20)
}

func validateAuggieTemperature(rawJSON []byte) error {
	return validateAuggieOptionalNumberRangeField(rawJSON, "temperature", 0, 2)
}

func validateAuggieTopP(rawJSON []byte) error {
	return validateAuggieOptionalNumberRangeField(rawJSON, "top_p", 0, 1)
}

func validateAuggiePromptCacheKey(rawJSON []byte) error {
	promptCacheKey := gjson.GetBytes(rawJSON, "prompt_cache_key")
	if !promptCacheKey.Exists() || promptCacheKey.Type == gjson.Null {
		return nil
	}
	if promptCacheKey.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"prompt_cache_key must be a string for Auggie requests",
			"prompt_cache_key",
			"invalid_type",
		)
	}
	return nil
}

func validateAuggiePromptCacheRetention(rawJSON []byte) error {
	promptCacheRetention := gjson.GetBytes(rawJSON, "prompt_cache_retention")
	if !promptCacheRetention.Exists() || promptCacheRetention.Type == gjson.Null {
		return nil
	}
	if promptCacheRetention.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"prompt_cache_retention must be a string for Auggie requests",
			"prompt_cache_retention",
			"invalid_type",
		)
	}

	value := strings.ToLower(strings.TrimSpace(promptCacheRetention.String()))
	switch value {
	case "in-memory", "24h":
		return nil
	default:
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("prompt_cache_retention=%q is not supported by Auggie; supported values are in-memory or 24h", promptCacheRetention.String()),
			"prompt_cache_retention",
			"invalid_value",
		)
	}
}

func validateAuggieSafetyIdentifier(rawJSON []byte) error {
	safetyIdentifier := gjson.GetBytes(rawJSON, "safety_identifier")
	if !safetyIdentifier.Exists() || safetyIdentifier.Type == gjson.Null {
		return nil
	}
	if safetyIdentifier.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"safety_identifier must be a string for Auggie requests",
			"safety_identifier",
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieUser(rawJSON []byte) error {
	user := gjson.GetBytes(rawJSON, "user")
	if !user.Exists() || user.Type == gjson.Null {
		return nil
	}
	if user.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"user must be a string for Auggie requests",
			"user",
			"invalid_type",
		)
	}
	return nil
}

func validateAuggieServiceTier(rawJSON []byte) error {
	serviceTier := gjson.GetBytes(rawJSON, "service_tier")
	if !serviceTier.Exists() || serviceTier.Type == gjson.Null {
		return nil
	}
	if serviceTier.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"service_tier must be a string for Auggie requests",
			"service_tier",
			"invalid_type",
		)
	}

	value := strings.ToLower(strings.TrimSpace(serviceTier.String()))
	switch value {
	case "auto", "default", "flex", "scale", "priority":
		return nil
	default:
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("service_tier=%q is not supported by Auggie; supported values are auto, default, flex, scale, or priority", serviceTier.String()),
			"service_tier",
			"invalid_value",
		)
	}
}

func shouldPersistAuggieResponsesState(rawJSON []byte) bool {
	store := gjson.GetBytes(rawJSON, "store")
	return !store.Exists() || store.Type != gjson.False
}

var supportedAuggieResponsesIncludeValues = map[string]struct{}{
	"reasoning.encrypted_content":    {},
	"web_search_call.action.sources": {},
	"web_search_call.results":        {},
}

var supportedAuggieResponsesVerbosityValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedAuggieResponsesReasoningEffortValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedAuggieOpenAIChatVerbosityValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedAuggieOpenAIChatWebSearchContextSizeValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

const (
	auggieOpenAIMetadataMaxPairs       = 16
	auggieOpenAIMetadataKeyMaxLength   = 64
	auggieOpenAIMetadataValueMaxLength = 512
)

func parseAuggieIncludeValues(rawJSON []byte) ([]string, error) {
	include := gjson.GetBytes(rawJSON, "include")
	if !include.Exists() {
		return nil, nil
	}
	if !include.IsArray() {
		return nil, newAuggieInvalidRequestStatusErr("include must be an array", "include", "invalid_type")
	}
	values := make([]string, 0, len(include.Array()))
	for index, item := range include.Array() {
		if item.Type != gjson.String {
			return nil, newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("include[%d] must be a string", index),
				fmt.Sprintf("include[%d]", index),
				"invalid_type",
			)
		}
		value := strings.TrimSpace(item.String())
		if value == "" {
			return nil, newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("include[%d] must be a non-empty string", index),
				fmt.Sprintf("include[%d]", index),
				"invalid_value",
			)
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
		if value == "message.output_text.logprobs" {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("include[%d]=%q is not supported on /v1/responses because Auggie cannot preserve output text logprobs", index, value),
				fmt.Sprintf("include[%d]", index),
				"invalid_value",
			)
		}
		if _, ok := supportedAuggieResponsesIncludeValues[value]; ok {
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("include[%d]=%q is not supported on /v1/responses because Auggie cannot preserve this expanded include shape; supported values are reasoning.encrypted_content, web_search_call.action.sources, and web_search_call.results", index, value),
			fmt.Sprintf("include[%d]", index),
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieOpenAISamplingControlSupport(rawJSON []byte) error {
	if err := validateAuggieMaxOutputTokens(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOptionalIntegerField(rawJSON, "max_completion_tokens"); err != nil {
		return err
	}
	if err := validateAuggieTopLogprobs(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOptionalBooleanField(rawJSON, "logprobs"); err != nil {
		return err
	}
	if err := validateAuggieTemperature(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOptionalNumberRangeField(rawJSON, "frequency_penalty", -2, 2); err != nil {
		return err
	}
	if err := validateAuggieOptionalNumberRangeField(rawJSON, "presence_penalty", -2, 2); err != nil {
		return err
	}
	if err := validateAuggieTopP(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOpenAILogitBias(rawJSON); err != nil {
		return err
	}

	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "max_output_tokens", param: "max_output_tokens", description: "token budget controls"},
		{path: "max_tokens", param: "max_tokens", description: "token budget controls"},
		{path: "max_completion_tokens", param: "max_completion_tokens", description: "token budget controls"},
		{path: "top_logprobs", param: "top_logprobs", description: "log probability controls"},
		{path: "logprobs", param: "logprobs", description: "log probability controls"},
		{path: "temperature", param: "temperature", description: "sampling controls"},
		{path: "frequency_penalty", param: "frequency_penalty", description: "penalty controls"},
		{path: "presence_penalty", param: "presence_penalty", description: "penalty controls"},
		{path: "top_p", param: "top_p", description: "sampling controls"},
		{path: "logit_bias", param: "logit_bias", description: "token logit bias controls"},
	}

	for _, control := range unsupportedControls {
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s is not supported by Auggie; the bridge cannot preserve %s, so use a native OpenAI-compatible route", control.param, control.description),
			control.param,
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieOpenAILogitBias(rawJSON []byte) error {
	logitBias := gjson.GetBytes(rawJSON, "logit_bias")
	if !logitBias.Exists() || logitBias.Type == gjson.Null {
		return nil
	}
	if !logitBias.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			"logit_bias must be an object for Auggie requests",
			"logit_bias",
			"invalid_type",
		)
	}

	for key, value := range logitBias.Map() {
		field := "logit_bias." + key
		if value.Type != gjson.Number {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s must be a number for Auggie requests", field),
				field,
				"invalid_type",
			)
		}
		numberValue := value.Float()
		if numberValue < -100 || numberValue > 100 {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s must be a number between -100 and 100 for Auggie requests", field),
				field,
				"invalid_value",
			)
		}
	}
	return nil
}

func validateAuggieOpenAINStopAndSeedSupport(rawJSON []byte) error {
	if err := validateAuggieOptionalIntegerField(rawJSON, "n"); err != nil {
		return err
	}
	if err := validateAuggieOptionalStopField(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieOptionalIntegerField(rawJSON, "seed"); err != nil {
		return err
	}

	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "n", param: "n", description: "choice count controls"},
		{path: "stop", param: "stop", description: "stop sequence controls"},
		{path: "seed", param: "seed", description: "determinism controls"},
	}

	for _, control := range unsupportedControls {
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s is not supported by Auggie; the bridge cannot preserve %s, so use a native OpenAI-compatible route", control.param, control.description),
			control.param,
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieOpenAIChatStreamOptionsSupport(rawJSON []byte) error {
	streamOptions := gjson.GetBytes(rawJSON, "stream_options")
	if !streamOptions.Exists() || streamOptions.Type == gjson.Null {
		return nil
	}
	if !streamOptions.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			"stream_options must be an object",
			"stream_options",
			"invalid_type",
		)
	}
	if !gjson.GetBytes(rawJSON, "stream").Bool() {
		return newAuggieInvalidRequestStatusErr(
			"stream_options requires stream=true on /v1/chat/completions",
			"stream_options",
			"invalid_value",
		)
	}

	for key, value := range streamOptions.Map() {
		switch key {
		case "include_obfuscation", "include_usage":
			if value.Type != gjson.True && value.Type != gjson.False {
				return newAuggieInvalidRequestStatusErr(
					fmt.Sprintf("stream_options.%s must be a boolean", key),
					"stream_options."+key,
					"invalid_type",
				)
			}
		default:
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("stream_options.%s is not supported for Auggie chat/completions; supported chat stream_options fields are include_obfuscation and include_usage", key),
				"stream_options."+key,
				"invalid_value",
			)
		}
	}

	return newAuggieInvalidRequestStatusErr(
		"stream_options is not supported by Auggie; the bridge cannot preserve chat streaming controls, so use a native OpenAI-compatible route",
		"stream_options",
		"invalid_value",
	)
}

func validateAuggieOpenAIServiceTierSupport(rawJSON []byte) error {
	if err := validateAuggieServiceTier(rawJSON); err != nil {
		return err
	}

	serviceTier := gjson.GetBytes(rawJSON, "service_tier")
	if !serviceTier.Exists() || serviceTier.Type == gjson.Null {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		"service_tier is not supported by Auggie; the bridge cannot preserve service tier controls, so use a native OpenAI-compatible route",
		"service_tier",
		"invalid_value",
	)
}

func validateAuggieOpenAIPromptCacheAndUserSupport(rawJSON []byte) error {
	if err := validateAuggiePromptCacheKey(rawJSON); err != nil {
		return err
	}
	if err := validateAuggiePromptCacheRetention(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieSafetyIdentifier(rawJSON); err != nil {
		return err
	}
	if err := validateAuggieUser(rawJSON); err != nil {
		return err
	}

	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "prompt_cache_key", param: "prompt_cache_key", description: "prompt cache controls"},
		{path: "prompt_cache_retention", param: "prompt_cache_retention", description: "prompt cache controls"},
		{path: "safety_identifier", param: "safety_identifier", description: "safety attribution controls"},
		{path: "user", param: "user", description: "end-user attribution controls"},
	}

	for _, control := range unsupportedControls {
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s is not supported by Auggie; the bridge cannot preserve %s, so use a native OpenAI-compatible route", control.param, control.description),
			control.param,
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieOpenAIAudioOutputSupport(rawJSON []byte) error {
	if err := validateAuggieOpenAIModalities(rawJSON); err != nil {
		return err
	}

	audio := gjson.GetBytes(rawJSON, "audio")
	if audio.Exists() && audio.Type != gjson.Null {
		if !audio.IsObject() {
			return newAuggieInvalidRequestStatusErr(
				"audio must be an object for Auggie requests",
				"audio",
				"invalid_type",
			)
		}
		return newAuggieInvalidRequestStatusErr(
			"audio is not supported by Auggie; the bridge cannot preserve audio output controls, so omit audio and use text output only or a native OpenAI-compatible route",
			"audio",
			"invalid_value",
		)
	}

	prediction := gjson.GetBytes(rawJSON, "prediction")
	if prediction.Exists() && prediction.Type != gjson.Null {
		if !prediction.IsObject() {
			return newAuggieInvalidRequestStatusErr(
				"prediction must be an object for Auggie requests",
				"prediction",
				"invalid_type",
			)
		}
		return newAuggieInvalidRequestStatusErr(
			"prediction is not supported by Auggie; the bridge cannot preserve predicted output controls, so use a native OpenAI-compatible route",
			"prediction",
			"invalid_value",
		)
	}

	return nil
}

func validateAuggieOpenAIModalities(rawJSON []byte) error {
	modalities := gjson.GetBytes(rawJSON, "modalities")
	if !modalities.Exists() || modalities.Type == gjson.Null {
		return nil
	}
	if !modalities.IsArray() {
		return newAuggieInvalidRequestStatusErr(
			"modalities must be an array for Auggie requests",
			"modalities",
			"invalid_type",
		)
	}

	items := modalities.Array()
	if len(items) == 0 {
		return newAuggieInvalidRequestStatusErr(
			"modalities must be a non-empty array for Auggie requests",
			"modalities",
			"invalid_value",
		)
	}

	hasAudio := false
	for index, item := range items {
		field := fmt.Sprintf("modalities[%d]", index)
		if item.Type != gjson.String {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s must be a string for Auggie requests", field),
				field,
				"invalid_type",
			)
		}
		switch value := strings.ToLower(strings.TrimSpace(item.String())); value {
		case "text":
		case "audio":
			hasAudio = true
		default:
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("%s=%q is not supported by Auggie; supported modalities are text or audio", field, item.String()),
				field,
				"invalid_value",
			)
		}
	}

	if hasAudio {
		return newAuggieInvalidRequestStatusErr(
			`modalities including "audio" are not supported by Auggie; the bridge only preserves text output, so use modalities=["text"] or omit the field entirely`,
			"modalities",
			"invalid_value",
		)
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
	return newAuggieInvalidRequestStatusErr(
		"include is not supported on /v1/chat/completions",
		"include",
		"unsupported_parameter",
	)
}

func validateAuggieOpenAIResponseFormatSupport(rawJSON []byte) error {
	return validateAuggieStructuredOutputFormat(gjson.GetBytes(rawJSON, "response_format"), "response_format")
}

func validateAuggieOpenAIReasoningEffort(rawJSON []byte) error {
	reasoningEffort := gjson.GetBytes(rawJSON, "reasoning_effort")
	if !reasoningEffort.Exists() || reasoningEffort.Type == gjson.Null {
		return nil
	}
	if reasoningEffort.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"reasoning_effort must be a string for Auggie requests",
			"reasoning_effort",
			"invalid_type",
		)
	}

	value := strings.ToLower(strings.TrimSpace(reasoningEffort.String()))
	if _, ok := supportedAuggieResponsesReasoningEffortValues[value]; ok {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("reasoning_effort=%q is not supported by Auggie; supported values are low, medium, or high because those are the native effort levels the bridge can preserve", reasoningEffort.String()),
		"reasoning_effort",
		"invalid_value",
	)
}

func validateAuggieResponsesTextFormatSupport(rawJSON []byte) error {
	return validateAuggieStructuredOutputFormat(gjson.GetBytes(rawJSON, "text.format"), "text.format")
}

func validateAuggieResponsesTextOptions(rawJSON []byte) error {
	text := gjson.GetBytes(rawJSON, "text")
	if !text.Exists() || text.Type == gjson.Null {
		return nil
	}
	if !text.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			"text must be an object for Auggie requests",
			"text",
			"invalid_type",
		)
	}

	verbosity := text.Get("verbosity")
	if !verbosity.Exists() || verbosity.Type == gjson.Null {
		return nil
	}
	if verbosity.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"text.verbosity must be a string for Auggie requests",
			"text.verbosity",
			"invalid_type",
		)
	}

	value := strings.ToLower(strings.TrimSpace(verbosity.String()))
	if _, ok := supportedAuggieResponsesVerbosityValues[value]; ok {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("text.verbosity=%q is not supported by Auggie; supported values are low, medium, or high", verbosity.String()),
		"text.verbosity",
		"invalid_value",
	)
}

func validateAuggieResponsesSamplingControlSupport(rawJSON []byte) error {
	builtInToolBridge := auggieResponsesRequestUsesBuiltInToolBridge(rawJSON)
	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "max_tool_calls", param: "max_tool_calls", description: "tool-call budget controls"},
		{path: "max_output_tokens", param: "max_output_tokens", description: "token budget controls"},
		{path: "max_tokens", param: "max_tokens", description: "token budget controls"},
		{path: "top_logprobs", param: "top_logprobs", description: "log probability controls"},
		{path: "temperature", param: "temperature", description: "sampling controls"},
		{path: "top_p", param: "top_p", description: "sampling controls"},
	}

	for _, control := range unsupportedControls {
		if control.param == "max_tool_calls" && builtInToolBridge {
			continue
		}
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s is not supported by Auggie; the bridge cannot preserve %s, so use a native OpenAI Responses route", control.param, control.description),
			control.param,
			"invalid_value",
		)
	}
	return nil
}

func validateAuggieResponsesServiceTierSupport(rawJSON []byte) error {
	serviceTier := gjson.GetBytes(rawJSON, "service_tier")
	if !serviceTier.Exists() || serviceTier.Type == gjson.Null {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		"service_tier is not supported by Auggie; the bridge cannot preserve service tier controls, so use a native OpenAI Responses route",
		"service_tier",
		"invalid_value",
	)
}

func validateAuggieResponsesTruncationSupport(rawJSON []byte) error {
	truncation := strings.ToLower(strings.TrimSpace(gjson.GetBytes(rawJSON, "truncation").String()))
	if truncation != "auto" {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		"truncation=\"auto\" is not supported by Auggie; the bridge cannot preserve automatic truncation controls, so use truncation=\"disabled\" or a native OpenAI Responses route",
		"truncation",
		"invalid_value",
	)
}

func validateAuggieResponsesPromptTemplateSupport(rawJSON []byte) error {
	prompt := gjson.GetBytes(rawJSON, "prompt")
	if !prompt.Exists() || prompt.Type == gjson.Null {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		"prompt is not supported by Auggie; the bridge cannot resolve or preserve prompt template references, so inline the prompt content in instructions/input or use a native OpenAI Responses route",
		"prompt",
		"invalid_value",
	)
}

func validateAuggieResponsesContextManagementSupport(rawJSON []byte) error {
	contextManagement := gjson.GetBytes(rawJSON, "context_management")
	if !contextManagement.Exists() || contextManagement.Type == gjson.Null {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		"context_management is not supported by Auggie on regular /v1/responses requests; the bridge cannot preserve native compaction controls there, so use /v1/responses/compact or a native OpenAI Responses route",
		"context_management",
		"invalid_value",
	)
}

func validateAuggieResponsesPromptCacheAndSafetyControlSupport(rawJSON []byte) error {
	// Accept and ignore prompt-cache / safety attribution controls for
	// compatibility with native OpenAI Responses payloads. The Auggie bridge
	// does not preserve these controls end-to-end.
	return nil
}

func validateAuggieResponsesReasoning(rawJSON []byte) error {
	reasoning := gjson.GetBytes(rawJSON, "reasoning")
	if !reasoning.Exists() || reasoning.Type == gjson.Null {
		return nil
	}
	if !reasoning.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			"reasoning must be an object for Auggie requests",
			"reasoning",
			"invalid_type",
		)
	}
	// Accept and ignore reasoning summary controls for compatibility with
	// native OpenAI Responses payloads. The Auggie bridge only preserves
	// reasoning effort.

	effort := reasoning.Get("effort")
	if !effort.Exists() || effort.Type == gjson.Null {
		return nil
	}
	if effort.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"reasoning.effort must be a string for Auggie requests",
			"reasoning.effort",
			"invalid_type",
		)
	}

	value := strings.ToLower(strings.TrimSpace(effort.String()))
	if _, ok := supportedAuggieResponsesReasoningEffortValues[value]; ok {
		return nil
	}
	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("reasoning.effort=%q is not supported by Auggie; supported values are low, medium, or high because those are the native effort levels the bridge can preserve", effort.String()),
		"reasoning.effort",
		"invalid_value",
	)
}

func validateAuggieStructuredOutputFormat(format gjson.Result, field string) error {
	if !format.Exists() || format.Type == gjson.Null {
		return nil
	}
	if !format.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be an object for Auggie requests", field),
			field,
			"invalid_type",
		)
	}

	formatType := strings.ToLower(strings.TrimSpace(format.Get("type").String()))
	switch formatType {
	case "text":
		return nil
	case "json_schema", "json_object":
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type=%q is not supported by Auggie; structured output response formats are not currently supported", field, formatType),
			field+".type",
			"invalid_value",
		)
	case "":
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type must be a non-empty string for Auggie requests", field),
			field+".type",
			"invalid_value",
		)
	default:
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type=%q is not supported by Auggie; supported response formats are text or omitted %s", field, formatType, field),
			field+".type",
			"invalid_value",
		)
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
			if err := validateAuggieToolDeferredLoading(tool.Get("defer_loading"), index); err != nil {
				return err
			}
			continue
		}
		if allowResponsesBuiltIns && toolType == "custom" {
			if err := validateAuggieToolDeferredLoading(tool.Get("defer_loading"), index); err != nil {
				return err
			}
			if err := validateAuggieCustomToolFormat(tool.Get("format"), index); err != nil {
				return err
			}
			continue
		}
		if allowResponsesBuiltIns && isAuggieResponsesBuiltInWebSearchType(toolType) {
			if err := validateAuggieBuiltInWebSearchToolConfig(tool, index); err != nil {
				return err
			}
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("tools[%d].type=%q is not supported by Auggie; supported tool types are %s", index, toolType, auggieSupportedToolTypesSummary(allowResponsesBuiltIns)),
			fmt.Sprintf("tools[%d].type", index),
			"invalid_value",
		)
	}

	return nil
}

func validateAuggieBuiltInWebSearchToolConfig(tool gjson.Result, index int) error {
	// Accept and ignore built-in web search configuration fields for compatibility with
	// native OpenAI Responses payloads. The Auggie bridge still only preserves
	// tool availability (type), not detailed web-search semantics.
	return nil
}

func validateAuggieOpenAIChatFunctionToolStrictSupport(rawJSON []byte) error {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return nil
	}

	for index, tool := range tools.Array() {
		toolType := strings.TrimSpace(tool.Get("type").String())
		if toolType != "" && toolType != "function" {
			continue
		}
		if err := validateAuggieOpenAIChatFunctionToolStrictMode(tool.Get("function").Get("strict"), index); err != nil {
			return err
		}
	}

	return nil
}

func validateAuggieOpenAIChatFunctionToolStrictMode(strict gjson.Result, index int) error {
	field := fmt.Sprintf("tools[%d].function.strict", index)
	if !strict.Exists() || strict.Type == gjson.Null {
		return nil
	}
	if strict.Type != gjson.True && strict.Type != gjson.False {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a boolean for Auggie requests", field),
			field,
			"invalid_type",
		)
	}
	if !strict.Bool() {
		return nil
	}

	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("%s=%t is not supported by Auggie; the chat-completions bridge cannot preserve official function-tool strict schema semantics, so set %s=%t or omit the field", field, true, field, false),
		field,
		"invalid_value",
	)
}

func validateAuggieResponsesFunctionToolStrictSupport(rawJSON []byte) error {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return nil
	}

	for index, tool := range tools.Array() {
		toolType := strings.TrimSpace(tool.Get("type").String())
		if toolType != "" && toolType != "function" {
			continue
		}
		if err := validateAuggieResponsesFunctionToolStrictMode(tool.Get("strict"), index); err != nil {
			return err
		}
	}

	return nil
}

func validateAuggieResponsesFunctionToolStrictMode(strict gjson.Result, index int) error {
	field := fmt.Sprintf("tools[%d].strict", index)
	if !strict.Exists() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s defaults to true for OpenAI Responses function tools and is not supported by Auggie; the bridge cannot preserve native strict function schema semantics, so set %s=%t or use a native OpenAI Responses route", field, field, false),
			field,
			"invalid_value",
		)
	}
	if strict.Type != gjson.True && strict.Type != gjson.False {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a boolean for Auggie requests", field),
			field,
			"invalid_type",
		)
	}
	if !strict.Bool() {
		return nil
	}

	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("%s=%t is not supported by Auggie; the bridge cannot preserve native strict function schema semantics, so set %s=%t or use a native OpenAI Responses route", field, true, field, false),
		field,
		"invalid_value",
	)
}

func validateAuggieToolDeferredLoading(deferLoading gjson.Result, index int) error {
	field := fmt.Sprintf("tools[%d].defer_loading", index)
	if !deferLoading.Exists() || deferLoading.Type == gjson.Null {
		return nil
	}
	if deferLoading.Type != gjson.True && deferLoading.Type != gjson.False {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a boolean for Auggie requests", field),
			field,
			"invalid_type",
		)
	}
	if !deferLoading.Bool() {
		return nil
	}

	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("%s=true is not supported by Auggie; deferred tool discovery is not implemented for function or custom tools", field),
		field,
		"invalid_value",
	)
}

func validateAuggieCustomToolFormat(format gjson.Result, index int) error {
	field := fmt.Sprintf("tools[%d].format", index)
	if !format.Exists() || format.Type == gjson.Null {
		return nil
	}
	if !format.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be an object for Auggie requests", field),
			field,
			"invalid_type",
		)
	}

	formatType := format.Get("type")
	if !formatType.Exists() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type is required for Auggie custom tool requests when %s is provided", field, field),
			field+".type",
			"missing_required_parameter",
		)
	}
	if formatType.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type must be a string for Auggie requests", field),
			field+".type",
			"invalid_type",
		)
	}

	switch value := strings.ToLower(strings.TrimSpace(formatType.String())); value {
	case "text", "grammar":
		return nil
	case "":
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type must be a non-empty string for Auggie requests", field),
			field+".type",
			"invalid_value",
		)
	default:
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s.type=%q is not supported by Auggie; supported custom tool formats are text, grammar, or omitted %s", field, value, field),
			field+".type",
			"invalid_value",
		)
	}
}

func validateAuggieResponsesToolChoice(rawJSON []byte) error {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return nil
	}

	if toolChoice.Type == gjson.String {
		value := strings.ToLower(strings.TrimSpace(toolChoice.String()))
		switch value {
		case "", "auto", "none":
			return nil
		case "required":
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("tool_choice=%q is not supported by Auggie; the bridge cannot preserve native forced tool-use semantics, so use tool_choice=%q, tool_choice=%q, allowed_tools selection in %q mode, or a native OpenAI Responses route", value, "auto", "none", "auto"),
				"tool_choice",
				"invalid_value",
			)
		default:
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("tool_choice=%q is not supported by Auggie; supported values are %s", value, auggieSupportedResponsesToolChoiceSummary()),
				"tool_choice",
				"invalid_value",
			)
		}
	}

	if toolChoice.IsObject() {
		toolChoiceType := strings.ToLower(strings.TrimSpace(toolChoice.Get("type").String()))
		switch {
		case toolChoiceType == "allowed_tools":
			container := toolChoice.Get("allowed_tools")
			param := "tool_choice.mode"
			if container.Exists() && container.IsObject() {
				param = "tool_choice.allowed_tools.mode"
			} else {
				container = toolChoice
			}
			mode := strings.ToLower(strings.TrimSpace(container.Get("mode").String()))
			if mode == "required" {
				return newAuggieInvalidRequestStatusErr(
					fmt.Sprintf("%s=%q is not supported by Auggie; the bridge cannot preserve native forced tool-use semantics, so use %s=%q or a native OpenAI Responses route", param, mode, param, "auto"),
					param,
					"invalid_value",
				)
			}
			if err := validateAuggieAllowedToolsToolChoice(toolChoice, true); err != nil {
				return err
			}
			return nil
		case toolChoiceType == "function", toolChoiceType == "custom", isAuggieResponsesBuiltInWebSearchType(toolChoiceType):
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("tool_choice.type=%q is not supported by Auggie; the bridge cannot preserve native forced tool-use semantics, so use tool_choice=%q, tool_choice=%q, allowed_tools selection in %q mode, or a native OpenAI Responses route", toolChoiceType, "auto", "none", "auto"),
				"tool_choice.type",
				"invalid_value",
			)
		default:
			if toolChoiceType == "" {
				toolChoiceType = "object"
			}
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("tool_choice.type=%q is not supported by Auggie; supported values are %s", toolChoiceType, auggieSupportedResponsesToolChoiceSummary()),
				"tool_choice.type",
				"invalid_value",
			)
		}
	}

	return newAuggieInvalidRequestStatusErr(
		"tool_choice must be a string or object for Auggie requests",
		"tool_choice",
		"invalid_type",
	)
}

func validateAuggieOpenAIToolChoice(rawJSON []byte) error {
	return validateAuggieToolChoice(rawJSON, false)
}

func validateAuggieOpenAILegacyFunctionSupport(rawJSON []byte) error {
	tools := gjson.GetBytes(rawJSON, "tools")
	functions := gjson.GetBytes(rawJSON, "functions")
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	functionCall := gjson.GetBytes(rawJSON, "function_call")

	if tools.Exists() && tools.Type != gjson.Null && functions.Exists() && functions.Type != gjson.Null {
		return newAuggieInvalidRequestStatusErr(
			"functions cannot be used together with tools for Auggie chat/completions; use either deprecated functions/function_call or modern tools/tool_choice semantics, not both",
			"functions",
			"invalid_value",
		)
	}
	if toolChoice.Exists() && toolChoice.Type != gjson.Null && functionCall.Exists() && functionCall.Type != gjson.Null {
		return newAuggieInvalidRequestStatusErr(
			"function_call cannot be used together with tool_choice for Auggie chat/completions; use either deprecated functions/function_call or modern tools/tool_choice semantics, not both",
			"function_call",
			"invalid_value",
		)
	}
	if err := validateAuggieLegacyFunctionsField(functions); err != nil {
		return err
	}
	return validateAuggieLegacyFunctionCallField(functionCall)
}

func validateAuggieLegacyFunctionsField(functions gjson.Result) error {
	if !functions.Exists() || functions.Type == gjson.Null {
		return nil
	}
	if !functions.IsArray() {
		return newAuggieInvalidRequestStatusErr(
			"functions must be an array",
			"functions",
			"invalid_type",
		)
	}

	for index, function := range functions.Array() {
		if !function.IsObject() {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("functions[%d] must be an object", index),
				fmt.Sprintf("functions[%d]", index),
				"invalid_type",
			)
		}

		name := function.Get("name")
		if !name.Exists() {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("functions[%d].name is required", index),
				fmt.Sprintf("functions[%d].name", index),
				"missing_required_parameter",
			)
		}
		if name.Type != gjson.String {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("functions[%d].name must be a string", index),
				fmt.Sprintf("functions[%d].name", index),
				"invalid_type",
			)
		}
		if strings.TrimSpace(name.String()) == "" {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("functions[%d].name must be a non-empty string", index),
				fmt.Sprintf("functions[%d].name", index),
				"invalid_value",
			)
		}

		if description := function.Get("description"); description.Exists() && description.Type != gjson.Null && description.Type != gjson.String {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("functions[%d].description must be a string", index),
				fmt.Sprintf("functions[%d].description", index),
				"invalid_type",
			)
		}
		if parameters := function.Get("parameters"); parameters.Exists() && parameters.Type != gjson.Null && !parameters.IsObject() {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("functions[%d].parameters must be an object", index),
				fmt.Sprintf("functions[%d].parameters", index),
				"invalid_type",
			)
		}
	}

	return nil
}

func validateAuggieLegacyFunctionCallField(functionCall gjson.Result) error {
	if !functionCall.Exists() || functionCall.Type == gjson.Null {
		return nil
	}
	if functionCall.Type == gjson.String {
		switch value := strings.ToLower(strings.TrimSpace(functionCall.String())); value {
		case "", "auto", "none":
			return nil
		default:
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("function_call=%q is not supported by Auggie; the bridge only preserves deprecated function_call=%q or function_call=%q, so use one of those values or a native OpenAI-compatible route", functionCall.String(), "auto", "none"),
				"function_call",
				"invalid_value",
			)
		}
	}
	if !functionCall.IsObject() {
		return newAuggieInvalidRequestStatusErr(
			"function_call must be a string or object",
			"function_call",
			"invalid_type",
		)
	}

	name := functionCall.Get("name")
	if !name.Exists() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("function_call objects are not supported by Auggie; the bridge cannot preserve native forced function-use semantics, so use function_call=%q, function_call=%q, or a native OpenAI-compatible route", "auto", "none"),
			"function_call",
			"invalid_value",
		)
	}
	if name.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			"function_call.name must be a string",
			"function_call.name",
			"invalid_type",
		)
	}
	if strings.TrimSpace(name.String()) == "" {
		return newAuggieInvalidRequestStatusErr(
			"function_call.name must be a non-empty string",
			"function_call.name",
			"invalid_value",
		)
	}
	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("function_call.name=%q is not supported by Auggie; the bridge cannot preserve native forced function-use semantics, so use function_call=%q, function_call=%q, or a native OpenAI-compatible route", name.String(), "auto", "none"),
		"function_call",
		"invalid_value",
	)
}

func validateAuggieToolChoice(rawJSON []byte, allowResponsesBuiltIns bool) error {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return nil
	}

	if toolChoice.Type == gjson.String {
		value := strings.ToLower(strings.TrimSpace(toolChoice.String()))
		switch value {
		case "", "auto", "none":
			return nil
		case "required":
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("tool_choice=%q is not supported by Auggie; the bridge cannot preserve native forced tool-use semantics, so use tool_choice=%q, tool_choice=%q, allowed_tools selection in %q mode, or a native OpenAI-compatible route", value, "auto", "none", "auto"),
				"tool_choice",
				"invalid_value",
			)
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("tool_choice=%q is not supported by Auggie; supported values are %s", value, auggieSupportedToolChoiceSummary(allowResponsesBuiltIns)),
			"tool_choice",
			"invalid_value",
		)
	}

	if toolChoice.IsObject() {
		toolChoiceType := strings.ToLower(strings.TrimSpace(toolChoice.Get("type").String()))
		if allowResponsesBuiltIns && isAuggieResponsesBuiltInWebSearchType(toolChoiceType) {
			return nil
		}
		if allowResponsesBuiltIns && toolChoiceType == "custom" && extractAuggieToolChoiceFunctionName(toolChoice) != "" {
			return nil
		}
		if toolChoiceType == "allowed_tools" {
			container := toolChoice.Get("allowed_tools")
			param := "tool_choice.mode"
			if container.Exists() && container.IsObject() {
				param = "tool_choice.allowed_tools.mode"
			} else {
				container = toolChoice
			}
			mode := strings.ToLower(strings.TrimSpace(container.Get("mode").String()))
			if mode == "required" {
				return newAuggieInvalidRequestStatusErr(
					fmt.Sprintf("%s=%q is not supported by Auggie; the bridge cannot preserve native forced tool-use semantics, so use %s=%q or a native OpenAI-compatible route", param, mode, param, "auto"),
					param,
					"invalid_value",
				)
			}
			if err := validateAuggieAllowedToolsToolChoice(toolChoice, allowResponsesBuiltIns); err != nil {
				return err
			}
			return nil
		}
		if toolChoiceType == "function" && extractAuggieToolChoiceFunctionName(toolChoice) != "" {
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("tool_choice.type=%q is not supported by Auggie; the bridge cannot preserve native forced tool-use semantics, so use tool_choice=%q, tool_choice=%q, allowed_tools selection in %q mode, or a native OpenAI-compatible route", toolChoiceType, "auto", "none", "auto"),
				"tool_choice.type",
				"invalid_value",
			)
		}
		if toolChoiceType == "" {
			toolChoiceType = "object"
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("tool_choice.type=%q is not supported by Auggie; supported values are %s", toolChoiceType, auggieSupportedToolChoiceSummary(allowResponsesBuiltIns)),
			"tool_choice.type",
			"invalid_value",
		)
	}

	return newAuggieInvalidRequestStatusErr(
		"tool_choice must be a string or object for Auggie requests",
		"tool_choice",
		"invalid_type",
	)
}

func newAuggieInvalidRequestStatusErr(message, param, code string) statusErr {
	payload := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
		},
	}
	if param != "" {
		payload["error"].(map[string]any)["param"] = param
	}
	if code != "" {
		payload["error"].(map[string]any)["code"] = code
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return statusErr{
			code: http.StatusBadRequest,
			msg:  message,
		}
	}
	return statusErr{
		code: http.StatusBadRequest,
		msg:  string(body),
	}
}

func auggieSupportedToolTypesSummary(allowResponsesBuiltIns bool) string {
	if allowResponsesBuiltIns {
		return "function, custom, and web_search"
	}
	return "function"
}

func auggieSupportedToolChoiceSummary(allowResponsesBuiltIns bool) string {
	return "auto, none, allowed_tools selection in auto mode, or omitted tool_choice"
}

func auggieSupportedResponsesToolChoiceSummary() string {
	return "auto, none, allowed_tools selection in auto mode, or omitted tool_choice"
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
		case "", "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output":
			if err := validateAuggieResponsesToolOutputArrayItems(item, index, itemType); err != nil {
				return err
			}
			continue
		case "item_reference":
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf(
					"input[%d].type=%q is not supported by Auggie /v1/responses because Auggie cannot resolve prior response item references; use previous_response_id for native continuation or a native OpenAI Responses route for manual item replay",
					index,
					itemType,
				),
				fmt.Sprintf("input[%d].type", index),
				"invalid_value",
			)
		case "reasoning":
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf(
					"input[%d].type=%q is not supported by Auggie /v1/responses because Auggie cannot accept prior reasoning items as input; use previous_response_id for native continuation or a native OpenAI Responses route for manual item replay",
					index,
					itemType,
				),
				fmt.Sprintf("input[%d].type", index),
				"invalid_value",
			)
		default:
			return newAuggieInvalidRequestStatusErr(
				fmt.Sprintf("input[%d].type=%q is not supported by Auggie /v1/responses; supported item types are message, function_call, function_call_output, custom_tool_call, and custom_tool_call_output", index, itemType),
				fmt.Sprintf("input[%d].type", index),
				"invalid_value",
			)
		}
	}

	return nil
}

func validateAuggieResponsesToolOutputArrayItems(item gjson.Result, index int, itemType string) error {
	if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
		return nil
	}

	output := item.Get("output")
	if !output.Exists() || !output.IsArray() {
		return nil
	}

	for outputIndex, outputItem := range output.Array() {
		outputType := strings.TrimSpace(outputItem.Get("type").String())
		if outputType == "" && outputItem.Get("text").Exists() {
			outputType = "input_text"
		}
		if outputType == "input_text" || outputType == "output_text" {
			continue
		}
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf(
				"input[%d].output[%d].type=%q is not supported by Auggie /v1/responses because bridged tool outputs can only preserve text items; supported tool output item types are input_text and output_text",
				index,
				outputIndex,
				outputType,
			),
			fmt.Sprintf("input[%d].output[%d].type", index, outputIndex),
			"invalid_value",
		)
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

func buildAuggieResponsesBuiltInToolOutputs(toolCalls []auggieBuiltInToolCall, toolResults []auggieToolResultContinuation) []auggieResponsesBuiltInToolOutput {
	resultsByID := make(map[string]auggieToolResultContinuation, len(toolResults))
	for _, result := range toolResults {
		resultsByID[strings.TrimSpace(result.ToolUseID)] = result
	}

	outputs := make([]auggieResponsesBuiltInToolOutput, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		switch toolCall.Name {
		case "web-search":
			status := "completed"
			if result, ok := resultsByID[strings.TrimSpace(toolCall.ID)]; ok && result.IsError {
				status = "failed"
			}
			output := auggieResponsesBuiltInToolOutput{
				ID:     "ws_" + strings.TrimSpace(toolCall.ID),
				Type:   "web_search_call",
				Status: status,
				Query:  strings.TrimSpace(gjson.Get(toolCall.Arguments, "query").String()),
			}
			if result, ok := resultsByID[strings.TrimSpace(toolCall.ID)]; ok {
				output.Output = strings.TrimSpace(result.Content)
			}
			outputs = append(outputs, output)
		}
	}

	return outputs
}

func attachAuggieResponsesBuiltInToolOutputs(openAIPayload []byte, outputs []auggieResponsesBuiltInToolOutput) ([]byte, error) {
	if len(outputs) == 0 {
		return openAIPayload, nil
	}

	outputsRaw, err := json.Marshal(outputs)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(openAIPayload, "_cliproxy_builtin_tool_outputs", outputsRaw)
}

func buildAuggieToolContinuationRequest(baseTranslated []byte, state auggieConversationState, toolResults []auggieToolResultContinuation) ([]byte, error) {
	translated := bytes.Clone(baseTranslated)
	translated, err := applyAuggieConversationState(translated, state)
	if err != nil {
		return nil, err
	}
	translated, err = sjson.SetBytes(translated, "message", "")
	if err != nil {
		return nil, err
	}
	translated, err = sjson.SetRawBytes(translated, "chat_history", []byte("[]"))
	if err != nil {
		return nil, err
	}
	translated, err = restoreAuggieConversationReplayContext(translated, state)
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
	return rewriteAuggieRequestToolCallIDs(translated)
}

func (e *AuggieExecutor) runAuggieBuiltInToolCalls(ctx context.Context, auth *cliproxyauth.Auth, registry []auggieRemoteToolRegistryEntry, toolCalls []auggieBuiltInToolCall) ([]auggieToolResultContinuation, error) {
	if len(registry) == 0 {
		var err error
		registry, err = e.listAuggieRemoteTools(ctx, auth)
		if err != nil {
			return nil, err
		}
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
		return rewriteAuggieRequestToolCallIDs(translated)
	}
	translated, err := applyAuggieConversationState(translated, state)
	if err != nil {
		return nil, err
	}
	return rewriteAuggieRequestToolCallIDs(translated)
}

func lookupAuggieToolCallConversationState(model string, translated []byte) (auggieConversationState, bool) {
	toolCallIDs := auggieToolResultNodeIDs(translated)
	if len(toolCallIDs) == 0 {
		return auggieConversationState{}, false
	}

	model = strings.TrimSpace(model)
	var selected auggieConversationState
	for _, toolCallID := range toolCallIDs {
		state, ok := lookupAuggieStoredToolCallConversationState(model, toolCallID)
		if !ok {
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

func lookupAuggieStoredToolCallConversationState(model, toolCallID string) (auggieConversationState, bool) {
	model = strings.TrimSpace(model)
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return auggieConversationState{}, false
	}

	state, ok := defaultAuggieToolCallStateStore.Load(toolCallID)
	if !ok {
		return auggieConversationState{}, false
	}
	if model != "" && strings.TrimSpace(state.Model) != "" && !strings.EqualFold(state.Model, model) {
		log.Debugf("auggie executor: ignoring tool call continuation state for %s due to model mismatch state=%s request=%s", toolCallID, state.Model, model)
		return auggieConversationState{}, false
	}
	return state, true
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
	appendAuggieConversationResponseNodes(state, payload)
}

func appendAuggieConversationResponseNodes(state *auggieConversationState, payload []byte) {
	if state == nil {
		return
	}

	nodes := gjson.GetBytes(payload, "nodes")
	if !nodes.Exists() || !nodes.IsArray() {
		return
	}

	responseNodes := make([]json.RawMessage, 0, len(nodes.Array()))
	seen := make(map[string]struct{})
	if raw := bytes.TrimSpace(state.ResponseNodes); len(raw) > 0 {
		_ = json.Unmarshal(raw, &responseNodes)
		for _, existing := range responseNodes {
			toolUseID := strings.TrimSpace(gjson.GetBytes(existing, "tool_use.tool_use_id").String())
			if toolUseID == "" {
				toolUseID = strings.TrimSpace(gjson.GetBytes(existing, "tool_use.id").String())
			}
			if toolUseID != "" {
				seen[toolUseID] = struct{}{}
			}
		}
	}

	changed := false
	for _, node := range nodes.Array() {
		toolUse := node.Get("tool_use")
		if !toolUse.Exists() || toolUse.Type == gjson.Null {
			continue
		}

		toolUseID := strings.TrimSpace(toolUse.Get("tool_use_id").String())
		if toolUseID == "" {
			toolUseID = strings.TrimSpace(toolUse.Get("id").String())
		}
		if toolUseID != "" {
			if _, ok := seen[toolUseID]; ok {
				continue
			}
			seen[toolUseID] = struct{}{}
		}

		responseNodes = append(responseNodes, json.RawMessage(node.Raw))
		changed = true
	}

	if !changed {
		return
	}

	raw, err := json.Marshal(responseNodes)
	if err != nil {
		return
	}
	state.ResponseNodes = raw
}

func seedAuggieConversationStateFromRequest(state *auggieConversationState, translated []byte) {
	if state == nil {
		return
	}
	if got := strings.TrimSpace(gjson.GetBytes(translated, "conversation_id").String()); got != "" {
		state.ConversationID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(translated, "turn_id").String()); got != "" {
		state.TurnID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(translated, "parent_conversation_id").String()); got != "" {
		state.ParentConversationID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(translated, "root_conversation_id").String()); got != "" {
		state.RootConversationID = got
	}
	if got := strings.TrimSpace(gjson.GetBytes(translated, "model").String()); got != "" {
		state.Model = got
	}
	if message := gjson.GetBytes(translated, "message"); message.Exists() {
		state.Message = sanitizeAuggieConversationReplayMessage(message.String())
	}
	if chatHistory := gjson.GetBytes(translated, "chat_history"); chatHistory.Exists() && chatHistory.IsArray() {
		state.ChatHistory = json.RawMessage(bytes.Clone([]byte(chatHistory.Raw)))
	}
}

func sanitizeAuggieConversationReplayMessage(message string) string {
	requiredSuffix := "\n\nOpenAI compatibility: you must call at least one available tool before answering. Do not answer directly without a tool call."
	if strings.HasSuffix(message, requiredSuffix) {
		return strings.TrimSuffix(message, requiredSuffix)
	}

	specificPrefix := "\n\nOpenAI compatibility: you must call the tool \""
	specificSuffix := "\" before answering. Do not call any other tool and do not answer directly."
	if strings.HasSuffix(message, specificSuffix) {
		if idx := strings.LastIndex(message, specificPrefix); idx >= 0 {
			return message[:idx]
		}
	}

	return message
}

func restoreAuggieConversationReplayContext(translated []byte, state auggieConversationState) ([]byte, error) {
	history := make([]any, 0, 4)

	if raw := bytes.TrimSpace(state.ChatHistory); len(raw) > 0 {
		var priorHistory []any
		if err := json.Unmarshal(raw, &priorHistory); err != nil {
			return nil, err
		}
		history = append(history, priorHistory...)
	}

	if strings.TrimSpace(state.Message) != "" {
		exchange := map[string]any{
			"request_message": state.Message,
		}
		if raw := bytes.TrimSpace(state.ResponseNodes); len(raw) > 0 {
			var responseNodes []any
			if err := json.Unmarshal(raw, &responseNodes); err != nil {
				return nil, err
			}
			if len(responseNodes) > 0 {
				exchange["response_nodes"] = responseNodes
			}
		}
		history = append(history, exchange)
	}

	currentHistory := gjson.GetBytes(translated, "chat_history")
	if currentHistory.Exists() && currentHistory.IsArray() {
		var translatedHistory []any
		if err := json.Unmarshal([]byte(currentHistory.Raw), &translatedHistory); err != nil {
			return nil, err
		}
		history = append(history, translatedHistory...)
	}

	historyRaw, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(translated, "chat_history", historyRaw)
}

func enrichAuggieToolResultNodes(translated []byte) ([]byte, error) {
	nodes := gjson.GetBytes(translated, "nodes")
	if !nodes.Exists() || !nodes.IsArray() {
		return translated, nil
	}

	turnID := strings.TrimSpace(gjson.GetBytes(translated, "turn_id").String())
	replayMetadataByToolUseID := collectAuggieToolResultReplayMetadata(translated)
	defaultStartTimeMS := time.Now().UnixMilli()

	var err error
	for index, node := range nodes.Array() {
		toolResult := node.Get("tool_result_node")
		if !toolResult.Exists() || toolResult.Type == gjson.Null {
			continue
		}
		toolUseID := strings.TrimSpace(toolResult.Get("tool_use_id").String())
		replayMetadata := replayMetadataByToolUseID[toolUseID]

		if turnID != "" && strings.TrimSpace(toolResult.Get("request_id").String()) == "" {
			translated, err = sjson.SetBytes(translated, fmt.Sprintf("nodes.%d.tool_result_node.request_id", index), turnID)
			if err != nil {
				return nil, err
			}
		}
		if !toolResult.Get("start_time_ms").Exists() {
			startTimeMS := defaultStartTimeMS
			if replayMetadata.HasStartTime {
				startTimeMS = replayMetadata.StartTimeMS
			}
			translated, err = sjson.SetBytes(translated, fmt.Sprintf("nodes.%d.tool_result_node.start_time_ms", index), startTimeMS)
			if err != nil {
				return nil, err
			}
		}
		if !toolResult.Get("duration_ms").Exists() {
			durationMS := int64(0)
			if replayMetadata.HasDuration {
				durationMS = replayMetadata.DurationMS
			}
			translated, err = sjson.SetBytes(translated, fmt.Sprintf("nodes.%d.tool_result_node.duration_ms", index), durationMS)
			if err != nil {
				return nil, err
			}
		}
	}

	return translated, nil
}

func collectAuggieToolResultReplayMetadata(translated []byte) map[string]auggieToolResultReplayMetadata {
	metadataByToolUseID := make(map[string]auggieToolResultReplayMetadata)

	chatHistory := gjson.GetBytes(translated, "chat_history")
	if chatHistory.Exists() && chatHistory.IsArray() {
		for _, entry := range chatHistory.Array() {
			responseNodes := entry.Get("response_nodes")
			if !responseNodes.Exists() || !responseNodes.IsArray() {
				continue
			}
			appendAuggieToolResultReplayMetadata(metadataByToolUseID, []byte(responseNodes.Raw))
		}
	}

	model := strings.TrimSpace(gjson.GetBytes(translated, "model").String())
	for _, toolCallID := range auggieToolResultNodeIDs(translated) {
		replayMetadata := metadataByToolUseID[toolCallID]
		if replayMetadata.HasStartTime && replayMetadata.HasDuration {
			continue
		}
		state, ok := lookupAuggieStoredToolCallConversationState(model, toolCallID)
		if !ok {
			continue
		}
		appendAuggieToolResultReplayMetadata(metadataByToolUseID, state.ResponseNodes)
	}

	return metadataByToolUseID
}

func appendAuggieToolResultReplayMetadata(metadataByToolUseID map[string]auggieToolResultReplayMetadata, rawResponseNodes []byte) {
	responseNodes := gjson.ParseBytes(rawResponseNodes)
	if !responseNodes.Exists() || !responseNodes.IsArray() {
		return
	}

	responseNodes.ForEach(func(_, node gjson.Result) bool {
		toolUse := node.Get("tool_use")
		if !toolUse.Exists() || toolUse.Type == gjson.Null {
			return true
		}

		toolUseID := strings.TrimSpace(toolUse.Get("tool_use_id").String())
		if toolUseID == "" {
			toolUseID = strings.TrimSpace(toolUse.Get("id").String())
		}
		if toolUseID == "" {
			return true
		}

		replayMetadata := metadataByToolUseID[toolUseID]
		startedAtMS := toolUse.Get("started_at_ms").Int()
		completedAtMS := toolUse.Get("completed_at_ms").Int()
		if !replayMetadata.HasStartTime && startedAtMS > 0 {
			replayMetadata.StartTimeMS = startedAtMS
			replayMetadata.HasStartTime = true
		}
		if !replayMetadata.HasDuration && startedAtMS > 0 && completedAtMS >= startedAtMS {
			replayMetadata.DurationMS = completedAtMS - startedAtMS
			replayMetadata.HasDuration = true
		}
		metadataByToolUseID[toolUseID] = replayMetadata
		return true
	})
}

func enrichAuggieIDEStateNode(translated []byte) ([]byte, error) {
	nodes := gjson.GetBytes(translated, "nodes")
	if nodes.Exists() && nodes.IsArray() {
		for _, node := range nodes.Array() {
			if node.Get("type").Int() == 4 || node.Get("ide_state_node").Exists() {
				return translated, nil
			}
		}
	}

	hasToolResult := false
	if nodes.Exists() && nodes.IsArray() {
		for _, node := range nodes.Array() {
			if toolResult := node.Get("tool_result_node"); toolResult.Exists() && toolResult.Type != gjson.Null {
				hasToolResult = true
				break
			}
		}
	}
	if !hasToolResult {
		return translated, nil
	}

	workspaceRoot := currentAuggieWorkspaceRoot()
	if strings.TrimSpace(workspaceRoot) == "" {
		return translated, nil
	}

	ideStateRaw, err := json.Marshal(map[string]any{
		"id":   1,
		"type": 4,
		"ide_state_node": map[string]any{
			"workspace_folders": []map[string]any{
				{
					"repository_root": workspaceRoot,
					"folder_root":     workspaceRoot,
				},
			},
			"workspace_folders_unchanged": hasToolResult,
			"current_terminal": map[string]any{
				"terminal_id":               0,
				"current_working_directory": workspaceRoot,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	mergedNodes := make([]json.RawMessage, 0, len(nodes.Array())+1)
	mergedNodes = append(mergedNodes, json.RawMessage(ideStateRaw))
	if nodes.Exists() && nodes.IsArray() {
		for _, node := range nodes.Array() {
			mergedNodes = append(mergedNodes, json.RawMessage(node.Raw))
		}
	}

	mergedNodesRaw, err := json.Marshal(mergedNodes)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(translated, "nodes", mergedNodesRaw)
}

func currentAuggieWorkspaceRoot() string {
	auggieWorkspaceRootOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		auggieWorkspaceRoot = detectAuggieWorkspaceRoot(cwd)
	})
	return strings.TrimSpace(auggieWorkspaceRoot)
}

func detectAuggieWorkspaceRoot(start string) string {
	start = strings.TrimSpace(start)
	if start == "" {
		return ""
	}

	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start
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

func auggieToolCallIDsFromPayload(payload []byte) []string {
	nodes := gjson.GetBytes(payload, "nodes")
	if !nodes.Exists() || !nodes.IsArray() {
		return nil
	}

	var ids []string
	nodes.ForEach(func(_, node gjson.Result) bool {
		toolUse := node.Get("tool_use")
		if !toolUse.Exists() || toolUse.Type == gjson.Null {
			return true
		}
		toolCallID := strings.TrimSpace(toolUse.Get("tool_use_id").String())
		if toolCallID == "" {
			toolCallID = strings.TrimSpace(toolUse.Get("id").String())
		}
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

func storeAuggieResponsesStateForFinalResponseID(rawRequestJSON, openAIPayload, translatedResponse []byte) {
	if !shouldPersistAuggieResponsesState(rawRequestJSON) {
		return
	}

	finalResponseID := strings.TrimSpace(gjson.GetBytes(translatedResponse, "id").String())
	if finalResponseID == "" {
		return
	}

	state, ok := lookupAuggieResponsesConversationState(openAIPayload, translatedResponse)
	if ok {
		defaultAuggieResponsesStateStore.Store(finalResponseID, state)
	}
}

func lookupAuggieResponsesConversationState(openAIPayload, translatedResponse []byte) (auggieConversationState, bool) {
	openAIResponseID := strings.TrimSpace(gjson.GetBytes(openAIPayload, "id").String())
	if openAIResponseID != "" {
		if state, ok := defaultAuggieResponsesStateStore.Load(openAIResponseID); ok {
			return state, true
		}
	}

	callIDs := appendUniqueStrings(
		nil,
		strings.TrimSpace(gjson.GetBytes(openAIPayload, "choices.0.message.tool_calls.0.id").String()),
	)
	gjson.GetBytes(translatedResponse, "output").ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) == "function_call" {
			callIDs = appendUniqueStrings(callIDs, item.Get("call_id").String())
		}
		return true
	})

	for _, callID := range callIDs {
		if state, ok := defaultAuggieToolCallStateStore.Load(callID); ok {
			return state, true
		}
	}
	return auggieConversationState{}, false
}

func extractAuggieToolChoiceFunctionName(toolChoice gjson.Result) string {
	name := strings.TrimSpace(toolChoice.Get("function.name").String())
	if name != "" {
		return name
	}
	name = strings.TrimSpace(toolChoice.Get("custom.name").String())
	if name != "" {
		return name
	}
	return strings.TrimSpace(toolChoice.Get("name").String())
}

func validateAuggieAllowedToolsToolChoice(toolChoice gjson.Result, allowResponsesBuiltIns bool) error {
	container, paramPrefix, err := auggieAllowedToolsToolChoiceContainer(toolChoice)
	if err != nil {
		return err
	}

	mode := container.Get("mode")
	modeParam := paramPrefix + ".mode"
	if !mode.Exists() || mode.Type == gjson.Null {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s is required for Auggie allowed_tools requests", modeParam),
			modeParam,
			"invalid_value",
		)
	}
	if mode.Type != gjson.String {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a string for Auggie requests", modeParam),
			modeParam,
			"invalid_type",
		)
	}
	modeValue := strings.ToLower(strings.TrimSpace(mode.String()))
	if modeValue != "auto" && modeValue != "required" {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s=%q is not supported by Auggie; supported values are auto and required", modeParam, modeValue),
			modeParam,
			"invalid_value",
		)
	}

	tools := container.Get("tools")
	toolsParam := paramPrefix + ".tools"
	if !tools.Exists() || tools.Type == gjson.Null {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a non-empty array for Auggie requests", toolsParam),
			toolsParam,
			"invalid_value",
		)
	}
	if !tools.IsArray() {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be an array for Auggie requests", toolsParam),
			toolsParam,
			"invalid_type",
		)
	}
	if len(tools.Array()) == 0 {
		return newAuggieInvalidRequestStatusErr(
			fmt.Sprintf("%s must be a non-empty array for Auggie requests", toolsParam),
			toolsParam,
			"invalid_value",
		)
	}

	for _, tool := range tools.Array() {
		toolType := strings.TrimSpace(tool.Get("type").String())
		switch toolType {
		case "function":
			if name := strings.TrimSpace(extractAuggieToolChoiceFunctionName(tool)); name != "" {
				return nil
			}
		case "custom":
			if allowResponsesBuiltIns {
				if name := strings.TrimSpace(extractAuggieToolChoiceFunctionName(tool)); name != "" {
					return nil
				}
			}
		default:
			if allowResponsesBuiltIns && isAuggieResponsesBuiltInWebSearchType(toolType) {
				return nil
			}
		}
	}

	return newAuggieInvalidRequestStatusErr(
		fmt.Sprintf("%s must include at least one supported %s tool selection for Auggie requests", toolsParam, auggieAllowedToolsSelectionLabel(allowResponsesBuiltIns)),
		toolsParam,
		"invalid_value",
	)
}

func auggieAllowedToolsToolChoiceContainer(toolChoice gjson.Result) (gjson.Result, string, error) {
	container := toolChoice.Get("allowed_tools")
	if container.Exists() && container.Type != gjson.Null {
		if !container.IsObject() {
			return gjson.Result{}, "", newAuggieInvalidRequestStatusErr(
				"tool_choice.allowed_tools must be an object for Auggie requests",
				"tool_choice.allowed_tools",
				"invalid_type",
			)
		}
		return container, "tool_choice.allowed_tools", nil
	}
	return toolChoice, "tool_choice", nil
}

func auggieAllowedToolsSelectionLabel(allowResponsesBuiltIns bool) string {
	if allowResponsesBuiltIns {
		return "tool"
	}
	return "function"
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

func isAuggieResponsesBuiltInWebSearchType(toolType string) bool {
	switch strings.ToLower(strings.TrimSpace(toolType)) {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26":
		return true
	default:
		return false
	}
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
