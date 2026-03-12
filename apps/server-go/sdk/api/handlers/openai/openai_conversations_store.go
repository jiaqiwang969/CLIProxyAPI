package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	storedOpenAIConversationTTL   = 30 * 24 * time.Hour
	maxOpenAIConversationReqItems = 20
)

type storedOpenAIConversation struct {
	ID        string
	CreatedAt int64
	Metadata  map[string]any
	Items     []map[string]any
	UpdatedAt time.Time
}

type storedOpenAIConversationStore struct {
	mu    sync.Mutex
	items map[string]storedOpenAIConversation
}

type openAIConversationExecutionContext struct {
	ID                string
	CurrentInputItems []byte
}

var (
	defaultStoredOpenAIConversationStore = &storedOpenAIConversationStore{
		items: make(map[string]storedOpenAIConversation),
	}
	openAIConversationIDCounter     uint64
	openAIConversationItemIDCounter uint64
)

func (s *storedOpenAIConversationStore) Create(metadata map[string]any, items []map[string]any) storedOpenAIConversation {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversation := storedOpenAIConversation{
		ID:        newOpenAIConversationID(),
		CreatedAt: now.Unix(),
		Metadata:  cloneMapStringAny(metadata),
		Items:     cloneMapStringAnySlice(items),
		UpdatedAt: now,
	}
	s.items[conversation.ID] = conversation
	return cloneStoredOpenAIConversation(conversation)
}

func (s *storedOpenAIConversationStore) Load(conversationID string) (storedOpenAIConversation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversation, ok := s.items[strings.TrimSpace(conversationID)]
	if !ok {
		return storedOpenAIConversation{}, false
	}
	return cloneStoredOpenAIConversation(conversation), true
}

func (s *storedOpenAIConversationStore) AddItems(conversationID string, items []map[string]any) (storedOpenAIConversation, []map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversationID = strings.TrimSpace(conversationID)
	conversation, ok := s.items[conversationID]
	if !ok {
		return storedOpenAIConversation{}, nil, false
	}

	normalized := cloneMapStringAnySlice(items)
	conversation.Items = append(conversation.Items, normalized...)
	conversation.UpdatedAt = now
	s.items[conversationID] = conversation
	return cloneStoredOpenAIConversation(conversation), cloneMapStringAnySlice(normalized), true
}

func (s *storedOpenAIConversationStore) UpdateMetadata(conversationID string, metadata map[string]any) (storedOpenAIConversation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversationID = strings.TrimSpace(conversationID)
	conversation, ok := s.items[conversationID]
	if !ok {
		return storedOpenAIConversation{}, false
	}

	conversation.Metadata = cloneMapStringAny(metadata)
	conversation.UpdatedAt = now
	s.items[conversationID] = conversation
	return cloneStoredOpenAIConversation(conversation), true
}

func (s *storedOpenAIConversationStore) Delete(conversationID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversationID = strings.TrimSpace(conversationID)
	if _, ok := s.items[conversationID]; !ok {
		return false
	}
	delete(s.items, conversationID)
	return true
}

func (s *storedOpenAIConversationStore) GetItem(conversationID string, itemID string) (map[string]any, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversationID = strings.TrimSpace(conversationID)
	conversation, ok := s.items[conversationID]
	if !ok {
		return nil, false, false
	}

	itemID = strings.TrimSpace(itemID)
	for _, item := range conversation.Items {
		if strings.TrimSpace(asString(item["id"])) == itemID {
			return cloneMapStringAny(item), true, true
		}
	}
	return nil, true, false
}

func (s *storedOpenAIConversationStore) DeleteItem(conversationID string, itemID string) (storedOpenAIConversation, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	conversationID = strings.TrimSpace(conversationID)
	conversation, ok := s.items[conversationID]
	if !ok {
		return storedOpenAIConversation{}, false, false
	}

	itemID = strings.TrimSpace(itemID)
	updatedItems := make([]map[string]any, 0, len(conversation.Items))
	deleted := false
	for _, item := range conversation.Items {
		if !deleted && strings.TrimSpace(asString(item["id"])) == itemID {
			deleted = true
			continue
		}
		updatedItems = append(updatedItems, item)
	}
	if !deleted {
		return storedOpenAIConversation{}, true, false
	}

	conversation.Items = cloneMapStringAnySlice(updatedItems)
	conversation.UpdatedAt = now
	s.items[conversationID] = conversation
	return cloneStoredOpenAIConversation(conversation), true, true
}

func (s *storedOpenAIConversationStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]storedOpenAIConversation)
}

func (s *storedOpenAIConversationStore) cleanupLocked(now time.Time) {
	cutoff := now.Add(-storedOpenAIConversationTTL)
	for key, item := range s.items {
		if item.UpdatedAt.IsZero() || item.UpdatedAt.Before(cutoff) {
			delete(s.items, key)
		}
	}
}

func cloneStoredOpenAIConversation(src storedOpenAIConversation) storedOpenAIConversation {
	return storedOpenAIConversation{
		ID:        src.ID,
		CreatedAt: src.CreatedAt,
		Metadata:  cloneMapStringAny(src.Metadata),
		Items:     cloneMapStringAnySlice(src.Items),
		UpdatedAt: src.UpdatedAt,
	}
}

func cloneMapStringAny(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	body, err := json.Marshal(src)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func cloneMapStringAnySlice(src []map[string]any) []map[string]any {
	if len(src) == 0 {
		return []map[string]any{}
	}
	body, err := json.Marshal(src)
	if err != nil {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil || out == nil {
		return []map[string]any{}
	}
	return out
}

func newOpenAIConversationID() string {
	return fmt.Sprintf("conv_%d", atomic.AddUint64(&openAIConversationIDCounter, 1))
}

func newOpenAIConversationItemID(itemType string) string {
	prefix := "item"
	switch strings.TrimSpace(itemType) {
	case "message":
		prefix = "msg"
	case "function_call":
		prefix = "fc"
	case "function_call_output":
		prefix = "fco"
	case "reasoning":
		prefix = "rs"
	}
	return fmt.Sprintf("%s_%d", prefix, atomic.AddUint64(&openAIConversationItemIDCounter, 1))
}

func marshalOpenAIConversation(conversation storedOpenAIConversation) []byte {
	body, err := json.Marshal(map[string]any{
		"id":         conversation.ID,
		"object":     "conversation",
		"created_at": conversation.CreatedAt,
		"metadata":   cloneMapStringAny(conversation.Metadata),
	})
	if err != nil {
		return []byte(`{"object":"conversation"}`)
	}
	return body
}

func marshalOpenAIConversationDeleted(conversationID string) []byte {
	body, err := json.Marshal(map[string]any{
		"id":      strings.TrimSpace(conversationID),
		"object":  "conversation.deleted",
		"deleted": true,
	})
	if err != nil {
		return []byte(`{"object":"conversation.deleted","deleted":true}`)
	}
	return body
}

func marshalOpenAIConversationItems(items []map[string]any) []byte {
	body, err := json.Marshal(cloneMapStringAnySlice(items))
	if err != nil {
		return []byte("[]")
	}
	return body
}

func marshalOpenAIConversationItem(item map[string]any) []byte {
	body, err := json.Marshal(normalizeOpenAIConversationItem(item))
	if err != nil {
		return []byte(`{}`)
	}
	return body
}

func parseOpenAIConversationCreateRequest(rawJSON []byte) (map[string]any, []map[string]any, *interfaces.ErrorMessage) {
	rawJSON = normalizeEmptyJSONObject(rawJSON)
	metadata, errMsg := parseOpenAIConversationMetadata(rawJSON, "/v1/conversations")
	if errMsg != nil {
		return nil, nil, errMsg
	}
	items, errMsg := parseOpenAIConversationItemsRequest(rawJSON, "/v1/conversations")
	if errMsg != nil {
		return nil, nil, errMsg
	}
	return metadata, items, nil
}

func parseOpenAIConversationAddItemsRequest(rawJSON []byte) ([]map[string]any, *interfaces.ErrorMessage) {
	rawJSON = normalizeEmptyJSONObject(rawJSON)
	items, errMsg := parseOpenAIConversationItemsRequest(rawJSON, "/v1/conversations/{conversation_id}/items")
	if errMsg != nil {
		return nil, errMsg
	}
	if len(items) == 0 {
		return nil, invalidOpenAIValue("items", "Invalid value for 'items': expected at least one item on /v1/conversations/{conversation_id}/items.")
	}
	return items, nil
}

func parseOpenAIConversationUpdateRequest(rawJSON []byte) (map[string]any, *interfaces.ErrorMessage) {
	rawJSON = normalizeEmptyJSONObject(rawJSON)
	metadata := gjson.GetBytes(rawJSON, "metadata")
	if !metadata.Exists() {
		return nil, missingOpenAIRequiredParameter("metadata")
	}
	parsed, errMsg := parseOpenAIConversationMetadata(rawJSON, "/v1/conversations/{conversation_id}")
	if errMsg != nil {
		return nil, errMsg
	}
	return parsed, nil
}

func parseOpenAIConversationReference(rawJSON []byte, endpoint string) (string, *interfaces.ErrorMessage) {
	conversation := gjson.GetBytes(rawJSON, "conversation")
	if !conversation.Exists() || conversation.Type == gjson.Null {
		return "", nil
	}

	switch conversation.Type {
	case gjson.String:
		id := strings.TrimSpace(conversation.String())
		if id == "" {
			return "", invalidOpenAIValue("conversation", "Invalid value for 'conversation': expected a non-empty string or object with non-empty id on /v1/%s.", endpoint)
		}
		return id, nil
	case gjson.JSON:
		if !conversation.IsObject() {
			return "", invalidOpenAIType("conversation", "a string or an object", conversation)
		}
		id := conversation.Get("id")
		if !id.Exists() || id.Type == gjson.Null {
			return "", invalidOpenAIValue("conversation.id", "Invalid value for 'conversation.id': expected a non-empty string on /v1/%s.", endpoint)
		}
		if id.Type != gjson.String {
			return "", invalidOpenAIType("conversation.id", "a string", id)
		}
		if strings.TrimSpace(id.String()) == "" {
			return "", invalidOpenAIValue("conversation.id", "Invalid value for 'conversation.id': expected a non-empty string on /v1/%s.", endpoint)
		}
		return strings.TrimSpace(id.String()), nil
	default:
		return "", invalidOpenAIType("conversation", "a string or an object", conversation)
	}
}

func parseOpenAIConversationMetadata(rawJSON []byte, endpoint string) (map[string]any, *interfaces.ErrorMessage) {
	metadata := gjson.GetBytes(rawJSON, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return map[string]any{}, nil
	}
	if !metadata.IsObject() {
		return nil, invalidOpenAIType("metadata", "an object", metadata)
	}
	value, ok := metadata.Value().(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return cloneMapStringAny(value), nil
}

func parseOpenAIConversationItemsRequest(rawJSON []byte, endpoint string) ([]map[string]any, *interfaces.ErrorMessage) {
	items := gjson.GetBytes(rawJSON, "items")
	if !items.Exists() || items.Type == gjson.Null {
		return []map[string]any{}, nil
	}
	if !items.IsArray() {
		return nil, invalidOpenAIType("items", "an array", items)
	}
	if len(items.Array()) > maxOpenAIConversationReqItems {
		return nil, invalidOpenAIValue("items", "Invalid value for 'items': expected at most %d items on %s.", maxOpenAIConversationReqItems, endpoint)
	}

	for index, item := range items.Array() {
		if !item.IsObject() {
			return nil, invalidOpenAIType(fmt.Sprintf("items[%d]", index), "an object", item)
		}
	}

	validationBody, err := sjson.SetRawBytes([]byte(`{}`), "input", []byte(items.Raw))
	if err == nil {
		if errMsg := validateOpenAIResponsesInput(validationBody); errMsg != nil {
			return nil, errMsg
		}
	}

	return normalizeOpenAIConversationItems([]byte(items.Raw)), nil
}

func normalizeEmptyJSONObject(rawJSON []byte) []byte {
	if len(bytes.TrimSpace(rawJSON)) == 0 {
		return []byte(`{}`)
	}
	return rawJSON
}

func normalizeOpenAIConversationItems(rawJSON []byte) []map[string]any {
	items := gjson.ParseBytes(cloneBytesOrDefault(rawJSON, []byte("[]"))).Array()
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, normalizeOpenAIConversationItem(item.Value()))
	}
	return out
}

func normalizeOpenAIConversationItem(value any) map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		item = map[string]any{}
	}

	normalized := cloneMapStringAny(item)
	itemType := strings.TrimSpace(asString(normalized["type"]))
	if itemType == "" && strings.TrimSpace(asString(normalized["role"])) != "" {
		itemType = "message"
		normalized["type"] = itemType
	}
	if strings.TrimSpace(asString(normalized["id"])) == "" {
		normalized["id"] = newOpenAIConversationItemID(itemType)
	}
	if itemType == "message" {
		if strings.TrimSpace(asString(normalized["status"])) == "" {
			normalized["status"] = "completed"
		}
	}
	return normalized
}

func normalizeOpenAIConversationOutputItems(responseBody []byte) []map[string]any {
	output := gjson.GetBytes(responseBody, "output")
	if !output.Exists() || !output.IsArray() {
		return []map[string]any{}
	}
	return normalizeOpenAIConversationItems([]byte(output.Raw))
}

func asString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func buildOpenAIConversationItemsList(items []map[string]any, c *gin.Context) ([]byte, *interfaces.ErrorMessage) {
	return buildOpenAIResponseInputItemsList(marshalOpenAIConversationItems(items), c)
}

func validateStoredOpenAIConversationItemsQuery(c *gin.Context) *interfaces.ErrorMessage {
	return validateStoredOpenAIResponseInputItemsQuery(c)
}

func validateStoredOpenAIConversationItemIncludeQuery(c *gin.Context, endpoint string) *interfaces.ErrorMessage {
	if queryHasNonEmptyArrayValue(c, "include", "include[]") {
		return invalidOpenAIRequestWithDetailf("include", "unsupported_parameter", "include is not supported on %s because include expansions are not implemented", endpoint)
	}
	return nil
}

func prepareOpenAIConversationRequest(rawJSON []byte, endpoint string) ([]byte, *openAIConversationExecutionContext, *interfaces.ErrorMessage) {
	conversationID, errMsg := parseOpenAIConversationReference(rawJSON, endpoint)
	if errMsg != nil {
		return nil, nil, errMsg
	}
	if conversationID == "" {
		return rawJSON, nil, nil
	}

	stored, ok := defaultStoredOpenAIConversationStore.Load(conversationID)
	if !ok {
		return nil, nil, missingStoredOpenAIConversation(conversationID)
	}

	currentInputItems := normalizeOpenAIResponseInputItems(rawJSON)
	mergedItems := append(
		normalizeOpenAIConversationItems(marshalOpenAIConversationItems(stored.Items)),
		normalizeOpenAIConversationItems(currentInputItems)...,
	)
	mergedRaw := marshalOpenAIConversationItems(mergedItems)

	updated, err := sjson.SetRawBytes(rawJSON, "input", mergedRaw)
	if err != nil {
		return nil, nil, invalidOpenAIValue("conversation", "Invalid value for 'conversation': failed to materialize conversation input for %s.", conversationID)
	}
	updated, _ = sjson.DeleteBytes(updated, "conversation")

	return updated, &openAIConversationExecutionContext{
		ID:                conversationID,
		CurrentInputItems: currentInputItems,
	}, nil
}

func prepareOpenAIResponsesConversationRequest(rawJSON []byte) ([]byte, *openAIConversationExecutionContext, *interfaces.ErrorMessage) {
	return prepareOpenAIConversationRequest(rawJSON, "responses")
}

func attachOpenAIConversationToResponseBody(conversation *openAIConversationExecutionContext, responseBody []byte) []byte {
	if conversation == nil || strings.TrimSpace(conversation.ID) == "" || !gjson.ValidBytes(responseBody) {
		return cloneBytesOrDefault(responseBody, []byte(`{}`))
	}

	updated, err := sjson.SetBytes(responseBody, "conversation.id", conversation.ID)
	if err != nil {
		return cloneBytesOrDefault(responseBody, []byte(`{}`))
	}
	return updated
}

func attachOpenAIConversationToTerminalPayload(conversationID string, payload []byte) []byte {
	if strings.TrimSpace(conversationID) == "" || !gjson.ValidBytes(payload) {
		return cloneBytesOrDefault(payload, []byte(`{}`))
	}

	switch strings.TrimSpace(gjson.GetBytes(payload, "type").String()) {
	case wsEventTypeCompleted, wsEventTypeIncomplete, wsEventTypeDone:
	default:
		return cloneBytesOrDefault(payload, []byte(`{}`))
	}

	response := gjson.GetBytes(payload, "response")
	if !response.Exists() || !response.IsObject() {
		return cloneBytesOrDefault(payload, []byte(`{}`))
	}

	updatedResponse, err := sjson.SetBytes([]byte(response.Raw), "conversation.id", conversationID)
	if err != nil {
		return cloneBytesOrDefault(payload, []byte(`{}`))
	}
	updatedPayload, err := sjson.SetRawBytes(payload, "response", updatedResponse)
	if err != nil {
		return cloneBytesOrDefault(payload, []byte(`{}`))
	}
	return updatedPayload
}

func attachOpenAIConversationToStreamChunk(conversation *openAIConversationExecutionContext, chunk []byte) []byte {
	if conversation == nil || strings.TrimSpace(conversation.ID) == "" {
		return cloneBytesOrDefault(chunk, []byte{})
	}

	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 {
		return cloneBytesOrDefault(chunk, []byte{})
	}
	if json.Valid(trimmed) {
		return attachOpenAIConversationToTerminalPayload(conversation.ID, trimmed)
	}

	lines := bytes.Split(chunk, []byte("\n"))
	updated := false
	for i := range lines {
		line := bytes.TrimSpace(lines[i])
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte(wsDoneMarker)) || !json.Valid(payload) {
			continue
		}
		patched := attachOpenAIConversationToTerminalPayload(conversation.ID, payload)
		if !bytes.Equal(patched, payload) {
			lines[i] = append([]byte("data: "), patched...)
			updated = true
		}
	}
	if !updated {
		return cloneBytesOrDefault(chunk, []byte{})
	}
	return bytes.Join(lines, []byte("\n"))
}

func maybeStoreOpenAIConversationResponse(conversation *openAIConversationExecutionContext, responseBody []byte) {
	if conversation == nil || strings.TrimSpace(conversation.ID) == "" {
		return
	}

	items := append(
		normalizeOpenAIConversationItems(conversation.CurrentInputItems),
		normalizeOpenAIConversationOutputItems(responseBody)...,
	)
	if len(items) == 0 {
		return
	}
	_, _, _ = defaultStoredOpenAIConversationStore.AddItems(conversation.ID, items)
}

func maybeStoreOpenAIConversationResponseFromStreamChunk(conversation *openAIConversationExecutionContext, chunk []byte) {
	if conversation == nil || strings.TrimSpace(conversation.ID) == "" {
		return
	}

	for _, payload := range websocketJSONPayloadsFromChunk(chunk) {
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if eventType != wsEventTypeCompleted && eventType != wsEventTypeIncomplete {
			continue
		}

		response := gjson.GetBytes(payload, "response")
		if !response.Exists() || !response.IsObject() {
			continue
		}
		maybeStoreOpenAIConversationResponse(conversation, []byte(response.Raw))
	}
}

func writeStoredOpenAIConversationNotFound(c *gin.Context, conversationID string) {
	c.JSON(http.StatusNotFound, handlers.ErrorResponse{
		Error: handlers.ErrorDetail{
			Message: fmt.Sprintf("No conversation found with id '%s'", conversationID),
			Type:    "invalid_request_error",
		},
	})
}

func writeStoredOpenAIConversationItemNotFound(c *gin.Context, conversationID string, itemID string) {
	c.JSON(http.StatusNotFound, handlers.ErrorResponse{
		Error: handlers.ErrorDetail{
			Message: fmt.Sprintf("No conversation item found with id '%s' in conversation '%s'", itemID, conversationID),
			Type:    "invalid_request_error",
		},
	})
}

func missingStoredOpenAIConversation(conversationID string) *interfaces.ErrorMessage {
	return &interfaces.ErrorMessage{
		StatusCode: http.StatusNotFound,
		Error:      fmt.Errorf("No conversation found with id '%s'", conversationID),
	}
}

func resetStoredOpenAIConversationsForTest() {
	defaultStoredOpenAIConversationStore.Reset()
	atomic.StoreUint64(&openAIConversationIDCounter, 0)
	atomic.StoreUint64(&openAIConversationItemIDCounter, 0)
}
