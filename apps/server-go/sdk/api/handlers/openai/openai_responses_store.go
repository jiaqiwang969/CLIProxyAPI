package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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

const storedOpenAIResponseTTL = 30 * time.Minute

type storedOpenAIResponse struct {
	Response     []byte
	InputItems   []byte
	ReplayEvents [][]byte
	UpdatedAt    time.Time
}

type storedOpenAIResponseTask struct {
	cancel context.CancelCauseFunc
}

type storedOpenAIResponseStore struct {
	mu    sync.Mutex
	items map[string]storedOpenAIResponse
	tasks map[string]storedOpenAIResponseTask
}

var defaultStoredOpenAIResponseStore = &storedOpenAIResponseStore{
	items: make(map[string]storedOpenAIResponse),
	tasks: make(map[string]storedOpenAIResponseTask),
}

var openAIBackgroundResponseCounter uint64

var errOpenAIBackgroundResponseDeleted = errors.New("openai background response deleted")

func shouldStoreOpenAIResponse(rawJSON []byte) bool {
	store := gjson.GetBytes(rawJSON, "store")
	return !store.Exists() || store.Type != gjson.False
}

func (s *storedOpenAIResponseStore) Store(responseID string, responseBody, inputItems []byte) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" || len(bytes.TrimSpace(responseBody)) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(time.Now().UTC())
	s.storeLocked(responseID, responseBody, inputItems, false)
}

func (s *storedOpenAIResponseStore) StoreWithReplay(responseID string, responseBody, inputItems []byte, replayEvents ...[]byte) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" || len(bytes.TrimSpace(responseBody)) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(time.Now().UTC())
	s.storeLocked(responseID, responseBody, inputItems, false, replayEvents...)
}

func (s *storedOpenAIResponseStore) StoreIfPresent(responseID string, responseBody, inputItems []byte) bool {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" || len(bytes.TrimSpace(responseBody)) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(time.Now().UTC())

	return s.storeIfPresentLocked(responseID, responseBody, inputItems)
}

func (s *storedOpenAIResponseStore) StoreIfPresentWithReplay(responseID string, responseBody, inputItems []byte, replayEvents ...[]byte) bool {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" || len(bytes.TrimSpace(responseBody)) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked(time.Now().UTC())

	return s.storeIfPresentLocked(responseID, responseBody, inputItems, replayEvents...)
}

func (s *storedOpenAIResponseStore) Load(responseID string) (storedOpenAIResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	item, ok := s.items[strings.TrimSpace(responseID)]
	if !ok {
		return storedOpenAIResponse{}, false
	}
	item.Response = bytes.Clone(item.Response)
	item.InputItems = bytes.Clone(item.InputItems)
	item.ReplayEvents = cloneByteSlices(item.ReplayEvents)
	return item, true
}

func (s *storedOpenAIResponseStore) AppendReplayEvent(responseID string, eventPayload []byte) bool {
	responseID = strings.TrimSpace(responseID)
	eventPayload = bytes.TrimSpace(eventPayload)
	if responseID == "" || len(eventPayload) == 0 || !json.Valid(eventPayload) {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	existing, ok := s.items[responseID]
	if !ok {
		return false
	}
	existing.ReplayEvents = append(cloneByteSlices(existing.ReplayEvents), bytes.Clone(eventPayload))
	existing.UpdatedAt = now
	s.items[responseID] = existing
	return true
}

func (s *storedOpenAIResponseStore) storeIfPresentLocked(responseID string, responseBody, inputItems []byte, replayEvents ...[]byte) bool {
	existing, ok := s.items[responseID]
	if !ok {
		return false
	}
	if len(bytes.TrimSpace(inputItems)) == 0 {
		inputItems = existing.InputItems
	}
	s.storeLocked(responseID, responseBody, inputItems, true, replayEvents...)
	return true
}

func (s *storedOpenAIResponseStore) storeLocked(responseID string, responseBody, inputItems []byte, mustExist bool, replayEvents ...[]byte) {
	existing, ok := s.items[responseID]
	if mustExist && !ok {
		return
	}
	if len(bytes.TrimSpace(inputItems)) == 0 {
		if ok {
			inputItems = existing.InputItems
		} else {
			inputItems = []byte("[]")
		}
	}

	combinedReplayEvents := cloneByteSlices(existing.ReplayEvents)
	combinedReplayEvents = append(combinedReplayEvents, compactReplayEvents(replayEvents)...)
	s.items[responseID] = storedOpenAIResponse{
		Response:     bytes.Clone(responseBody),
		InputItems:   cloneBytesOrDefault(inputItems, []byte("[]")),
		ReplayEvents: combinedReplayEvents,
		UpdatedAt:    time.Now().UTC(),
	}
}

func (s *storedOpenAIResponseStore) RegisterBackgroundTask(responseID string, cancel context.CancelCauseFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	responseID = strings.TrimSpace(responseID)
	if responseID == "" || cancel == nil {
		return false
	}
	if _, ok := s.items[responseID]; !ok {
		return false
	}
	s.tasks[responseID] = storedOpenAIResponseTask{cancel: cancel}
	return true
}

func (s *storedOpenAIResponseStore) CancelBackgroundTask(responseID string, cause error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	responseID = strings.TrimSpace(responseID)
	task, ok := s.tasks[responseID]
	if !ok || task.cancel == nil {
		return false
	}
	task.cancel(cause)
	return true
}

func (s *storedOpenAIResponseStore) FinishBackgroundTask(responseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return
	}
	delete(s.tasks, responseID)
}

func (s *storedOpenAIResponseStore) Delete(responseID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return false
	}
	if _, ok := s.items[responseID]; !ok {
		return false
	}
	if task, ok := s.tasks[responseID]; ok {
		if task.cancel != nil {
			task.cancel(errOpenAIBackgroundResponseDeleted)
		}
		delete(s.tasks, responseID)
	}
	delete(s.items, responseID)
	return true
}

func (s *storedOpenAIResponseStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, task := range s.tasks {
		if task.cancel != nil {
			task.cancel(errOpenAIBackgroundResponseDeleted)
		}
		delete(s.tasks, key)
	}
	s.items = make(map[string]storedOpenAIResponse)
	s.tasks = make(map[string]storedOpenAIResponseTask)
}

func (s *storedOpenAIResponseStore) cleanupLocked(now time.Time) {
	cutoff := now.Add(-storedOpenAIResponseTTL)
	for key, item := range s.items {
		if item.UpdatedAt.IsZero() || item.UpdatedAt.Before(cutoff) {
			if task, ok := s.tasks[key]; ok {
				if task.cancel != nil {
					task.cancel(errOpenAIBackgroundResponseDeleted)
				}
				delete(s.tasks, key)
			}
			delete(s.items, key)
		}
	}
}

func cloneBytesOrDefault(src, fallback []byte) []byte {
	src = bytes.TrimSpace(src)
	if len(src) == 0 {
		return bytes.Clone(fallback)
	}
	return bytes.Clone(src)
}

func cloneByteSlices(src [][]byte) [][]byte {
	if len(src) == 0 {
		return nil
	}
	cloned := make([][]byte, 0, len(src))
	for _, item := range src {
		cloned = append(cloned, bytes.Clone(item))
	}
	return cloned
}

func newStoredOpenAIResponseID() string {
	return fmt.Sprintf("resp_%x_%d", time.Now().UnixNano(), atomic.AddUint64(&openAIBackgroundResponseCounter, 1))
}

func marshalQueuedOpenAIResponse(rawRequestJSON []byte, responseID string, createdAt int64) []byte {
	payload := []byte(`{"id":"","object":"response","created_at":0,"status":"queued","background":true,"completed_at":null,"error":null,"incomplete_details":null,"instructions":null,"max_output_tokens":null,"metadata":{},"output":[],"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[],"top_p":1.0,"truncation":"disabled","usage":null,"user":null}`)
	payload, _ = sjson.SetBytes(payload, "id", strings.TrimSpace(responseID))
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	payload, _ = sjson.SetBytes(payload, "created_at", createdAt)

	if v := gjson.GetBytes(rawRequestJSON, "model"); v.Exists() {
		payload, _ = sjson.SetBytes(payload, "model", v.Value())
	}
	if v := gjson.GetBytes(rawRequestJSON, "instructions"); v.Exists() {
		payload, _ = sjson.SetBytes(payload, "instructions", v.Value())
	}
	if v := gjson.GetBytes(rawRequestJSON, "max_output_tokens"); v.Exists() {
		payload, _ = sjson.SetBytes(payload, "max_output_tokens", v.Value())
	} else if v = gjson.GetBytes(rawRequestJSON, "max_tokens"); v.Exists() {
		payload, _ = sjson.SetBytes(payload, "max_output_tokens", v.Value())
	}
	if v := gjson.GetBytes(rawRequestJSON, "max_tool_calls"); v.Exists() {
		payload, _ = sjson.SetBytes(payload, "max_tool_calls", v.Value())
	}
	for _, field := range []string{
		"background",
		"metadata",
		"parallel_tool_calls",
		"previous_response_id",
		"prompt",
		"prompt_cache_key",
		"prompt_cache_retention",
		"reasoning",
		"safety_identifier",
		"service_tier",
		"store",
		"temperature",
		"text",
		"tool_choice",
		"tools",
		"top_logprobs",
		"top_p",
		"truncation",
		"user",
	} {
		if v := gjson.GetBytes(rawRequestJSON, field); v.Exists() {
			payload, _ = sjson.SetBytes(payload, field, v.Value())
		}
	}

	return payload
}

func normalizeOpenAIBackgroundResponseCancelled(responseBody []byte) []byte {
	responseBody = cloneBytesOrDefault(responseBody, []byte(`{}`))
	if !gjson.ValidBytes(responseBody) {
		responseBody = marshalQueuedOpenAIResponse(nil, "", time.Now().Unix())
	}
	responseBody, _ = sjson.SetBytes(responseBody, "status", "cancelled")
	responseBody, _ = sjson.SetBytes(responseBody, "error", nil)
	responseBody, _ = sjson.SetBytes(responseBody, "completed_at", nil)
	if !gjson.GetBytes(responseBody, "background").Exists() {
		responseBody, _ = sjson.SetBytes(responseBody, "background", true)
	}
	if !gjson.GetBytes(responseBody, "output").Exists() {
		responseBody, _ = sjson.SetBytes(responseBody, "output", []any{})
	}
	if !gjson.GetBytes(responseBody, "usage").Exists() {
		responseBody, _ = sjson.SetBytes(responseBody, "usage", nil)
	}
	return responseBody
}

func normalizeOpenAIBackgroundResponseInProgress(responseBody []byte) []byte {
	responseBody = cloneBytesOrDefault(responseBody, []byte(`{}`))
	if !gjson.ValidBytes(responseBody) {
		responseBody = marshalQueuedOpenAIResponse(nil, "", time.Now().Unix())
	}
	responseBody, _ = sjson.SetBytes(responseBody, "status", "in_progress")
	responseBody, _ = sjson.SetBytes(responseBody, "background", true)
	if !gjson.GetBytes(responseBody, "output").Exists() {
		responseBody, _ = sjson.SetBytes(responseBody, "output", []any{})
	}
	if !gjson.GetBytes(responseBody, "usage").Exists() {
		responseBody, _ = sjson.SetBytes(responseBody, "usage", nil)
	}
	return responseBody
}

func normalizeOpenAIBackgroundTerminalResponse(rawRequestJSON, previousResponseBody, terminalResponseBody []byte, responseID string) []byte {
	createdAt := gjson.GetBytes(previousResponseBody, "created_at").Int()
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	body := cloneBytesOrDefault(terminalResponseBody, marshalQueuedOpenAIResponse(rawRequestJSON, responseID, createdAt))
	if !gjson.ValidBytes(body) {
		body = marshalQueuedOpenAIResponse(rawRequestJSON, responseID, createdAt)
	}
	body, _ = sjson.SetBytes(body, "id", strings.TrimSpace(responseID))
	body, _ = sjson.SetBytes(body, "created_at", createdAt)
	body, _ = sjson.SetBytes(body, "background", true)
	if !gjson.GetBytes(body, "usage").Exists() {
		body, _ = sjson.SetBytes(body, "usage", nil)
	}
	if !gjson.GetBytes(body, "output").Exists() {
		body, _ = sjson.SetBytes(body, "output", []any{})
	}
	if status := strings.TrimSpace(gjson.GetBytes(body, "status").String()); status == "" {
		body, _ = sjson.SetBytes(body, "status", "completed")
	}
	if strings.EqualFold(gjson.GetBytes(body, "status").String(), "completed") && gjson.GetBytes(body, "completed_at").Int() == 0 {
		body, _ = sjson.SetBytes(body, "completed_at", time.Now().Unix())
	}
	return body
}

func marshalFailedOpenAIBackgroundResponse(rawRequestJSON, previousResponseBody []byte, responseID string, err error) []byte {
	body := normalizeOpenAIBackgroundTerminalResponse(rawRequestJSON, previousResponseBody, previousResponseBody, responseID)
	body, _ = sjson.SetBytes(body, "status", "failed")
	body, _ = sjson.SetBytes(body, "completed_at", nil)
	body, _ = sjson.SetBytes(body, "output", []any{})
	body, _ = sjson.SetBytes(body, "usage", nil)
	body, _ = sjson.SetBytes(body, "error", map[string]any{
		"code":    openAIBackgroundResponseErrorCode(err),
		"message": backgroundExecutionErrorText(err),
	})
	return body
}

func backgroundExecutionErrorText(err error) string {
	if err == nil {
		return "background response execution failed"
	}
	return strings.TrimSpace(err.Error())
}

func openAIBackgroundResponseErrorCode(err error) string {
	if err == nil {
		return "server_error"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "server_error"
	}
	type statusCoder interface {
		StatusCode() int
	}
	if coded, ok := err.(statusCoder); ok {
		switch coded.StatusCode() {
		case http.StatusTooManyRequests:
			return "rate_limit_exceeded"
		default:
			return "server_error"
		}
	}
	return "server_error"
}

func marshalOpenAIResponseDeleted(responseID string) []byte {
	body, err := json.Marshal(map[string]any{
		"id":      strings.TrimSpace(responseID),
		"object":  "response",
		"deleted": true,
	})
	if err != nil {
		return []byte(`{"object":"response","deleted":true}`)
	}
	return body
}

func marshalStoredOpenAIResponseReplayEvent(eventType string, responseBody []byte) []byte {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil
	}

	responseBody = cloneBytesOrDefault(responseBody, []byte(`{}`))
	if !json.Valid(responseBody) {
		responseBody = []byte(`{}`)
	}

	payload, err := json.Marshal(struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}{
		Type:     eventType,
		Response: json.RawMessage(responseBody),
	})
	if err != nil {
		return nil
	}
	return payload
}

func openAIResponseTerminalReplayEventType(responseBody []byte) string {
	switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, "status").String())) {
	case "queued":
		return "response.queued"
	case "in_progress":
		return "response.in_progress"
	case "incomplete":
		return "response.incomplete"
	case "failed":
		return "response.failed"
	case "cancelled":
		return "response.cancelled"
	case "completed", "":
		return "response.completed"
	default:
		return "response.completed"
	}
}

func storedOpenAIResponseReplayEvents(stored storedOpenAIResponse) [][]byte {
	if events := validatedStoredOpenAIResponseReplayEvents(stored.ReplayEvents); len(events) > 0 {
		return events
	}
	return synthesizeStoredOpenAIResponseReplayEvents(stored.Response)
}

func validatedStoredOpenAIResponseReplayEvents(events [][]byte) [][]byte {
	if len(events) == 0 {
		return nil
	}

	validated := make([][]byte, 0, len(events))
	for _, event := range events {
		event = bytes.TrimSpace(event)
		if len(event) == 0 || !json.Valid(event) || strings.TrimSpace(gjson.GetBytes(event, "type").String()) == "" {
			return nil
		}
		validated = append(validated, bytes.Clone(event))
	}
	return validated
}

func synthesizeStoredOpenAIResponseReplayEvents(responseBody []byte) [][]byte {
	responseBody = cloneBytesOrDefault(responseBody, []byte(`{}`))
	if !json.Valid(responseBody) {
		return nil
	}

	events := make([][]byte, 0, 4)
	terminalType := openAIResponseTerminalReplayEventType(responseBody)

	switch terminalType {
	case "response.queued":
		queued := normalizeStoredOpenAIReplayStateResponse(responseBody, "queued")
		events = append(events,
			marshalStoredOpenAIResponseReplayEvent("response.created", queued),
			marshalStoredOpenAIResponseReplayEvent("response.queued", queued),
			marshalStoredOpenAIResponseReplayEvent("response.done", queued),
		)
		return compactReplayEvents(events)
	case "response.in_progress":
		inProgress := normalizeStoredOpenAIReplayStateResponse(responseBody, "in_progress")
		events = append(events,
			marshalStoredOpenAIResponseReplayEvent("response.created", inProgress),
			marshalStoredOpenAIResponseReplayEvent("response.in_progress", inProgress),
			marshalStoredOpenAIResponseReplayEvent("response.done", inProgress),
		)
		return compactReplayEvents(events)
	default:
		inProgress := normalizeStoredOpenAIReplayStateResponse(responseBody, "in_progress")
		events = append(events,
			marshalStoredOpenAIResponseReplayEvent("response.created", inProgress),
			marshalStoredOpenAIResponseReplayEvent("response.in_progress", inProgress),
			marshalStoredOpenAIResponseReplayEvent(terminalType, responseBody),
			marshalStoredOpenAIResponseReplayEvent("response.done", responseBody),
		)
		return compactReplayEvents(events)
	}
}

func normalizeStoredOpenAIReplayStateResponse(responseBody []byte, status string) []byte {
	body := cloneBytesOrDefault(responseBody, []byte(`{}`))
	if !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}
	body, _ = sjson.SetBytes(body, "status", strings.TrimSpace(status))
	body, _ = sjson.SetBytes(body, "completed_at", nil)
	if strings.EqualFold(strings.TrimSpace(status), "queued") || strings.EqualFold(strings.TrimSpace(status), "in_progress") {
		body, _ = sjson.SetBytes(body, "output", []any{})
		body, _ = sjson.SetBytes(body, "error", nil)
		body, _ = sjson.SetBytes(body, "incomplete_details", nil)
	}
	return body
}

func compactReplayEvents(events [][]byte) [][]byte {
	compacted := make([][]byte, 0, len(events))
	for _, event := range events {
		event = bytes.TrimSpace(event)
		if len(event) == 0 {
			continue
		}
		compacted = append(compacted, bytes.Clone(event))
	}
	return compacted
}

func normalizeOpenAIResponseInputTokensBody(responseBody []byte) []byte {
	responseBody = bytes.TrimSpace(responseBody)
	if len(responseBody) == 0 || !gjson.ValidBytes(responseBody) {
		return cloneBytesOrDefault(responseBody, []byte(`{}`))
	}
	if strings.TrimSpace(gjson.GetBytes(responseBody, "object").String()) == "response.input_tokens" {
		return cloneBytesOrDefault(responseBody, []byte(`{}`))
	}

	var inputTokens gjson.Result
	for _, path := range []string{
		"input_tokens",
		"usage.input_tokens",
		"response.usage.input_tokens",
		"usage.prompt_tokens",
		"response.usage.prompt_tokens",
	} {
		inputTokens = gjson.GetBytes(responseBody, path)
		if inputTokens.Exists() {
			break
		}
	}
	if !inputTokens.Exists() {
		return cloneBytesOrDefault(responseBody, []byte(`{}`))
	}

	body, err := json.Marshal(map[string]any{
		"object":       "response.input_tokens",
		"input_tokens": inputTokens.Int(),
	})
	if err != nil {
		return cloneBytesOrDefault(responseBody, []byte(`{}`))
	}
	return body
}

func maybeStoreOpenAIResponse(rawRequestJSON, responseBody []byte) {
	if !shouldStoreOpenAIResponse(rawRequestJSON) {
		return
	}
	responseID := strings.TrimSpace(gjson.GetBytes(responseBody, "id").String())
	if responseID == "" {
		return
	}
	defaultStoredOpenAIResponseStore.Store(responseID, responseBody, normalizeOpenAIResponseInputItems(rawRequestJSON))
}

func maybeStoreOpenAIResponseFromStreamChunk(rawRequestJSON, chunk []byte) {
	if !shouldStoreOpenAIResponse(rawRequestJSON) {
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

		responseID := strings.TrimSpace(response.Get("id").String())
		if responseID == "" {
			continue
		}
		defaultStoredOpenAIResponseStore.Store(responseID, []byte(response.Raw), normalizeOpenAIResponseInputItems(rawRequestJSON))
	}
}

func normalizeOpenAIResponseInputItems(rawRequestJSON []byte) []byte {
	input := gjson.GetBytes(rawRequestJSON, "input")
	switch {
	case input.Exists() && input.IsArray():
		items := make([]any, 0, len(input.Array()))
		for _, item := range input.Array() {
			value := item.Value()
			if obj, ok := value.(map[string]any); ok {
				if strings.TrimSpace(item.Get("type").String()) == "" && strings.TrimSpace(item.Get("role").String()) != "" {
					normalized := make(map[string]any, len(obj)+1)
					for key, rawValue := range obj {
						normalized[key] = rawValue
					}
					normalized["type"] = "message"
					value = normalized
				}
			}
			items = append(items, value)
		}
		normalized, err := json.Marshal(items)
		if err == nil {
			return normalized
		}
	case input.Exists() && input.IsObject():
		item := input.Value()
		if obj, ok := item.(map[string]any); ok {
			if strings.TrimSpace(input.Get("type").String()) == "" && strings.TrimSpace(input.Get("role").String()) != "" {
				normalized := make(map[string]any, len(obj)+1)
				for key, rawValue := range obj {
					normalized[key] = rawValue
				}
				normalized["type"] = "message"
				item = normalized
			}
		}
		normalized, err := json.Marshal([]any{item})
		if err == nil {
			return normalized
		}
	case input.Type == gjson.String:
		normalized, err := json.Marshal([]map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": input.String(),
					},
				},
			},
		})
		if err == nil {
			return normalized
		}
	}

	return []byte("[]")
}

func buildOpenAIResponseInputItemsList(inputItems []byte, c *gin.Context) ([]byte, *interfaces.ErrorMessage) {
	order := strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "desc")))
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return nil, invalidOpenAIValue("order", "Invalid value for 'order': expected one of asc or desc on GET /v1/responses/{response_id}/input_items.")
	}

	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return nil, invalidOpenAIValue("limit", "Invalid value for 'limit': expected an integer between 1 and 100 on GET /v1/responses/{response_id}/input_items.")
		}
		limit = parsed
	}

	items := gjson.ParseBytes(cloneBytesOrDefault(inputItems, []byte("[]"))).Array()
	ordered := make([]gjson.Result, 0, len(items))
	if order == "asc" {
		ordered = append(ordered, items...)
	} else {
		for i := len(items) - 1; i >= 0; i-- {
			ordered = append(ordered, items[i])
		}
	}

	after := strings.TrimSpace(c.Query("after"))
	if after != "" {
		filtered := ordered[:0]
		seen := false
		for _, item := range ordered {
			if seen {
				filtered = append(filtered, item)
				continue
			}
			if strings.TrimSpace(item.Get("id").String()) == after {
				seen = true
			}
		}
		if seen {
			ordered = filtered
		} else {
			ordered = ordered[:0]
		}
	}

	hasMore := len(ordered) > limit
	if hasMore {
		ordered = ordered[:limit]
	}

	dataItems := make([]any, 0, len(ordered))
	for _, item := range ordered {
		dataItems = append(dataItems, item.Value())
	}

	payload := map[string]any{
		"object":   "list",
		"data":     dataItems,
		"has_more": hasMore,
	}

	if len(ordered) > 0 {
		if firstID := strings.TrimSpace(ordered[0].Get("id").String()); firstID != "" {
			payload["first_id"] = firstID
		}
		if lastID := strings.TrimSpace(ordered[len(ordered)-1].Get("id").String()); lastID != "" {
			payload["last_id"] = lastID
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"object":"list","data":[],"has_more":false}`), nil
	}
	return body, nil
}

func validateStoredOpenAIResponseGetQuery(c *gin.Context) *interfaces.ErrorMessage {
	if raw := strings.TrimSpace(c.Query("stream")); raw != "" && !strings.EqualFold(raw, "false") && !strings.EqualFold(raw, "true") {
		return invalidOpenAIValue("stream", "Invalid value for 'stream': expected one of true or false on GET /v1/responses/{response_id}.")
	}
	if raw := strings.TrimSpace(c.Query("starting_after")); raw != "" {
		return invalidOpenAIValue("starting_after", "Invalid value for 'starting_after': starting_after is not supported on GET /v1/responses/{response_id} because stored replay pagination is not implemented.")
	}
	if raw := strings.TrimSpace(c.Query("include_obfuscation")); raw != "" && !strings.EqualFold(raw, "false") {
		return invalidOpenAIValue("include_obfuscation", "Invalid value for 'include_obfuscation': include_obfuscation is not supported on GET /v1/responses/{response_id}.")
	}
	if values := queryArrayValues(c, "include", "include[]"); len(values) > 0 {
		return validateSupportedOpenAIResponsesIncludeValues(values, "GET /v1/responses/{response_id}")
	}
	return nil
}

func validateStoredOpenAIResponseInputItemsQuery(c *gin.Context) *interfaces.ErrorMessage {
	if queryHasNonEmptyArrayValue(c, "include", "include[]") {
		return invalidOpenAIRequestWithDetailf("include", "unsupported_parameter", "include is not supported on GET /v1/responses/{response_id}/input_items because include expansions are not implemented")
	}
	return nil
}

func queryHasNonEmptyArrayValue(c *gin.Context, keys ...string) bool {
	return len(queryArrayValues(c, keys...)) > 0
}

func queryArrayValues(c *gin.Context, keys ...string) []string {
	values := make([]string, 0)
	for _, key := range keys {
		for _, value := range c.QueryArray(key) {
			value = strings.TrimSpace(value)
			if value != "" {
				values = append(values, value)
			}
		}
		if value := strings.TrimSpace(c.Query(key)); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func writeStoredOpenAIResponseNotFound(c *gin.Context, responseID string) {
	c.JSON(http.StatusNotFound, handlers.ErrorResponse{
		Error: handlers.ErrorDetail{
			Message: fmt.Sprintf("No response found with id '%s'", responseID),
			Type:    "invalid_request_error",
		},
	})
}

func resetStoredOpenAIResponsesForTest() {
	defaultStoredOpenAIResponseStore.Reset()
}
