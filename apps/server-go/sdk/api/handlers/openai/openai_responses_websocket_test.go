package openai

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestNormalizeResponsesWebsocketRequestCreate(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","stream":false,"input":[{"type":"message","id":"msg-1"}]}`)

	normalized, last, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	if gjson.GetBytes(normalized, "type").Exists() {
		t.Fatalf("normalized create request must not include type field")
	}
	if !gjson.GetBytes(normalized, "stream").Bool() {
		t.Fatalf("normalized create request must force stream=true")
	}
	if gjson.GetBytes(normalized, "model").String() != "test-model" {
		t.Fatalf("unexpected model: %s", gjson.GetBytes(normalized, "model").String())
	}
	if !bytes.Equal(last, normalized) {
		t.Fatalf("last request snapshot should match normalized request")
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsFunctionCallOutputMissingCallID(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","input":[{"type":"function_call_output","output":"{\"ok\":true}"}]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for function_call_output without call_id")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	if !strings.Contains(errMsg.Error.Error(), "call_id") {
		t.Fatalf("expected call_id validation error, got %v", errMsg.Error)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsFunctionCallOutputMissingOutput(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","input":[{"type":"function_call_output","call_id":"call-1"}]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for function_call_output without output")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	if !strings.Contains(errMsg.Error.Error(), "output") {
		t.Fatalf("expected output validation error, got %v", errMsg.Error)
	}
}

func TestNormalizeResponsesWebsocketRequestRejectsUnsupportedType(t *testing.T) {
	raw := []byte(`{"type":"response.delete","model":"test-model"}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for unsupported websocket request type")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), "type", "invalid_value", "response.create")
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsMissingModel(t *testing.T) {
	raw := []byte(`{"type":"response.create","input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for missing model")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), "model", "missing_required_parameter", "model")
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsConversationWithPreviousResponseID(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","conversation":"conv_123","previous_response_id":"resp_123","input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for conversation with previous_response_id")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	if !strings.Contains(errMsg.Error.Error(), "conversation") || !strings.Contains(errMsg.Error.Error(), "previous_response_id") {
		t.Fatalf("expected conversation/previous_response_id validation error, got %v", errMsg.Error)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateAllowsSupportedInclude(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","include":["reasoning.encrypted_content"],"input":[]}`)

	normalized, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg != nil {
		t.Fatalf("unexpected error for supported include: %v", errMsg.Error)
	}
	if got := gjson.GetBytes(normalized, "include.0").String(); got != "reasoning.encrypted_content" {
		t.Fatalf("normalized include[0] = %q, want reasoning.encrypted_content; normalized=%s", got, normalized)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsUnsupportedInclude(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","include":["unsupported.include"],"input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for unsupported include")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	if !strings.Contains(errMsg.Error.Error(), "unsupported.include") {
		t.Fatalf("expected unsupported include validation error, got %v", errMsg.Error)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsBackgroundTrue(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","background":true,"input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for background")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), "background", "invalid_value", "background=true")
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsBackgroundTrueWithReplayGuidance(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","background":true,"input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for background")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	message := errMsg.Error.Error()
	if !strings.Contains(message, "background=true") {
		t.Fatalf("expected background=true guidance, got %q", message)
	}
	if !strings.Contains(message, "GET /v1/responses/{response_id}?stream=true") {
		t.Fatalf("expected retrieval replay guidance, got %q", message)
	}
}

func TestNormalizeResponsesWebsocketRequestAppendRejectsConversationAfterInitialCreate(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)
	raw := []byte(`{"type":"response.append","conversation":"conv_123","input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, lastRequest, []byte("[]"))
	if errMsg == nil {
		t.Fatalf("expected error for append conversation reuse")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), "conversation", "invalid_value", "response.append")
}

func TestNormalizeResponsesWebsocketRequestCreateAllowsPromptTemplate(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","prompt":{"id":"pmpt_123"},"input":[]}`)

	normalized, lastRequest, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg != nil {
		t.Fatalf("unexpected error for prompt: %v", errMsg.Error)
	}
	if got := gjson.GetBytes(normalized, "prompt.id").String(); got != "pmpt_123" {
		t.Fatalf("prompt.id = %q, want pmpt_123; normalized=%s", got, normalized)
	}
	if !gjson.GetBytes(normalized, "stream").Bool() {
		t.Fatalf("expected websocket create to normalize to stream=true; normalized=%s", normalized)
	}
	if got := gjson.GetBytes(lastRequest, "prompt.id").String(); got != "pmpt_123" {
		t.Fatalf("lastRequest prompt.id = %q, want pmpt_123; lastRequest=%s", got, lastRequest)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateAllowsStreamOptions(t *testing.T) {
	raw := []byte(`{"type":"response.create","model":"test-model","stream_options":{"include_obfuscation":true},"input":[]}`)

	normalized, lastRequest, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg != nil {
		t.Fatalf("unexpected error for stream_options: %v", errMsg.Error)
	}
	if got := gjson.GetBytes(normalized, "stream_options.include_obfuscation").Bool(); !got {
		t.Fatalf("stream_options.include_obfuscation = %v, want true; normalized=%s", got, normalized)
	}
	if !gjson.GetBytes(normalized, "stream").Bool() {
		t.Fatalf("expected websocket create to normalize to stream=true; normalized=%s", normalized)
	}
	if got := gjson.GetBytes(lastRequest, "stream_options.include_obfuscation").Bool(); !got {
		t.Fatalf("lastRequest stream_options.include_obfuscation = %v, want true; lastRequest=%s", got, lastRequest)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateMaterializesConversation(t *testing.T) {
	resetStoredOpenAIConversationsForTest()
	t.Cleanup(resetStoredOpenAIConversationsForTest)

	conversation := defaultStoredOpenAIConversationStore.Create(nil, []map[string]any{
		{
			"id":   "msg_seed",
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "seed"},
			},
		},
	})

	raw := []byte(`{"type":"response.create","model":"test-model","conversation":"` + conversation.ID + `","input":[{"id":"msg_1","type":"message","role":"user","content":[{"type":"input_text","text":"follow up"}]}]}`)

	normalized, last, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	if gjson.GetBytes(normalized, "conversation").Exists() {
		t.Fatalf("normalized request must not include conversation field")
	}
	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 2 {
		t.Fatalf("materialized input len = %d, want 2", len(input))
	}
	if input[0].Get("id").String() != "msg_seed" || input[1].Get("id").String() != "msg_1" {
		t.Fatalf("unexpected materialized input order: %s", normalized)
	}
	if !bytes.Equal(last, normalized) {
		t.Fatalf("last request snapshot should match normalized request")
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsUnknownConversation(t *testing.T) {
	resetStoredOpenAIConversationsForTest()
	t.Cleanup(resetStoredOpenAIConversationsForTest)

	raw := []byte(`{"type":"response.create","model":"test-model","conversation":"conv_missing","input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for missing conversation")
	}
	if errMsg.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusNotFound)
	}
	if !strings.Contains(errMsg.Error.Error(), "conv_missing") {
		t.Fatalf("expected missing conversation id in error, got %v", errMsg.Error)
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsFunctionCallMissingRequiredFields(t *testing.T) {
	testCases := []struct {
		name          string
		inputItemJSON string
		wantField     string
	}{
		{
			name:          "missing call_id",
			inputItemJSON: `{"type":"function_call","name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "call_id",
		},
		{
			name:          "missing name",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "name",
		},
		{
			name:          "missing arguments",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","name":"get_weather"}`,
			wantField:     "arguments",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"type":"response.create","model":"test-model","input":[` + tc.inputItemJSON + `]}`)

			_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
			if errMsg == nil {
				t.Fatalf("expected error for function_call without %s", tc.wantField)
			}
			if errMsg.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
			}
			if !strings.Contains(errMsg.Error.Error(), tc.wantField) {
				t.Fatalf("expected %s validation error, got %v", tc.wantField, errMsg.Error)
			}
		})
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsFunctionCallInvalidFieldTypes(t *testing.T) {
	testCases := []struct {
		name          string
		inputItemJSON string
		wantField     string
	}{
		{
			name:          "call_id not string",
			inputItemJSON: `{"type":"function_call","call_id":123,"name":"get_weather","arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "call_id",
		},
		{
			name:          "name not string",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","name":123,"arguments":"{\"location\":\"Shanghai\"}"}`,
			wantField:     "name",
		},
		{
			name:          "arguments not string",
			inputItemJSON: `{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":{"location":"Shanghai"}}`,
			wantField:     "arguments",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"type":"response.create","model":"test-model","input":[` + tc.inputItemJSON + `]}`)

			_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
			if errMsg == nil {
				t.Fatalf("expected error for function_call with invalid %s", tc.wantField)
			}
			if errMsg.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
			}
			if !strings.Contains(errMsg.Error.Error(), tc.wantField) {
				t.Fatalf("expected %s validation error, got %v", tc.wantField, errMsg.Error)
			}
		})
	}
}

func TestNormalizeResponsesWebsocketRequestCreateRejectsFunctionCallOutputInvalidFieldTypes(t *testing.T) {
	testCases := []struct {
		name          string
		inputItemJSON string
		wantField     string
	}{
		{
			name:          "call_id not string",
			inputItemJSON: `{"type":"function_call_output","call_id":123,"output":"{\"ok\":true}"}`,
			wantField:     "call_id",
		},
		{
			name:          "output not string or array",
			inputItemJSON: `{"type":"function_call_output","call_id":"call-1","output":{"ok":true}}`,
			wantField:     "output",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"type":"response.create","model":"test-model","input":[` + tc.inputItemJSON + `]}`)

			_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
			if errMsg == nil {
				t.Fatalf("expected error for function_call_output with invalid %s", tc.wantField)
			}
			if errMsg.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
			}
			if !strings.Contains(errMsg.Error.Error(), tc.wantField) {
				t.Fatalf("expected %s validation error, got %v", tc.wantField, errMsg.Error)
			}
		})
	}
}

func TestNormalizeResponsesWebsocketRequestCreateWithHistory(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)
	lastResponseOutput := []byte(`[
		{"type":"function_call","id":"fc-1","call_id":"call-1"},
		{"type":"message","id":"assistant-1"}
	]`)
	raw := []byte(`{"type":"response.create","input":[{"type":"function_call_output","call_id":"call-1","id":"tool-out-1","output":"{\"ok\":true}"}]}`)

	normalized, next, errMsg := normalizeResponsesWebsocketRequest(raw, lastRequest, lastResponseOutput)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	if gjson.GetBytes(normalized, "type").Exists() {
		t.Fatalf("normalized subsequent create request must not include type field")
	}
	if gjson.GetBytes(normalized, "model").String() != "test-model" {
		t.Fatalf("unexpected model: %s", gjson.GetBytes(normalized, "model").String())
	}

	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 4 {
		t.Fatalf("merged input len = %d, want 4", len(input))
	}
	if input[0].Get("id").String() != "msg-1" ||
		input[1].Get("id").String() != "fc-1" ||
		input[2].Get("id").String() != "assistant-1" ||
		input[3].Get("id").String() != "tool-out-1" {
		t.Fatalf("unexpected merged input order")
	}
	if !bytes.Equal(next, normalized) {
		t.Fatalf("next request snapshot should match normalized request")
	}
}

func TestNormalizeResponsesWebsocketRequestWithPreviousResponseIDIncremental(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"instructions":"be helpful","input":[{"type":"message","id":"msg-1"}]}`)
	lastResponseOutput := []byte(`[
		{"type":"function_call","id":"fc-1","call_id":"call-1"},
		{"type":"message","id":"assistant-1"}
	]`)
	raw := []byte(`{"type":"response.create","previous_response_id":"resp-1","input":[{"type":"function_call_output","call_id":"call-1","id":"tool-out-1","output":"{\"ok\":true}"}]}`)

	normalized, next, errMsg := normalizeResponsesWebsocketRequestWithMode(raw, lastRequest, lastResponseOutput, true)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	if gjson.GetBytes(normalized, "type").Exists() {
		t.Fatalf("normalized request must not include type field")
	}
	if gjson.GetBytes(normalized, "previous_response_id").String() != "resp-1" {
		t.Fatalf("previous_response_id must be preserved in incremental mode")
	}
	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 1 {
		t.Fatalf("incremental input len = %d, want 1", len(input))
	}
	if input[0].Get("id").String() != "tool-out-1" {
		t.Fatalf("unexpected incremental input item id: %s", input[0].Get("id").String())
	}
	if gjson.GetBytes(normalized, "model").String() != "test-model" {
		t.Fatalf("unexpected model: %s", gjson.GetBytes(normalized, "model").String())
	}
	if gjson.GetBytes(normalized, "instructions").Exists() {
		t.Fatalf("incremental request must not inherit prior instructions automatically: %s", normalized)
	}
	if !bytes.Equal(next, normalized) {
		t.Fatalf("next request snapshot should match normalized request")
	}
}

func TestNormalizeResponsesWebsocketRequestWithPreviousResponseIDMergedWhenIncrementalDisabled(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)
	lastResponseOutput := []byte(`[
		{"type":"function_call","id":"fc-1","call_id":"call-1"},
		{"type":"message","id":"assistant-1"}
	]`)
	raw := []byte(`{"type":"response.create","previous_response_id":"resp-1","input":[{"type":"function_call_output","call_id":"call-1","id":"tool-out-1","output":"{\"ok\":true}"}]}`)

	normalized, next, errMsg := normalizeResponsesWebsocketRequestWithMode(raw, lastRequest, lastResponseOutput, false)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	if gjson.GetBytes(normalized, "previous_response_id").Exists() {
		t.Fatalf("previous_response_id must be removed when incremental mode is disabled")
	}
	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 4 {
		t.Fatalf("merged input len = %d, want 4", len(input))
	}
	if input[0].Get("id").String() != "msg-1" ||
		input[1].Get("id").String() != "fc-1" ||
		input[2].Get("id").String() != "assistant-1" ||
		input[3].Get("id").String() != "tool-out-1" {
		t.Fatalf("unexpected merged input order")
	}
	if !bytes.Equal(next, normalized) {
		t.Fatalf("next request snapshot should match normalized request")
	}
}

func TestNormalizeResponsesWebsocketRequestAppend(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)
	lastResponseOutput := []byte(`[
		{"type":"message","id":"assistant-1"},
		{"type":"function_call_output","id":"tool-out-1"}
	]`)
	raw := []byte(`{"type":"response.append","input":[{"type":"message","id":"msg-2"},{"type":"message","id":"msg-3"}]}`)

	normalized, next, errMsg := normalizeResponsesWebsocketRequest(raw, lastRequest, lastResponseOutput)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	input := gjson.GetBytes(normalized, "input").Array()
	if len(input) != 5 {
		t.Fatalf("merged input len = %d, want 5", len(input))
	}
	if input[0].Get("id").String() != "msg-1" ||
		input[1].Get("id").String() != "assistant-1" ||
		input[2].Get("id").String() != "tool-out-1" ||
		input[3].Get("id").String() != "msg-2" ||
		input[4].Get("id").String() != "msg-3" {
		t.Fatalf("unexpected merged input order")
	}
	if !bytes.Equal(next, normalized) {
		t.Fatalf("next request snapshot should match normalized append request")
	}
}

func TestNormalizeResponsesWebsocketRequestAppendWithoutCreate(t *testing.T) {
	raw := []byte(`{"type":"response.append","input":[]}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, nil, nil)
	if errMsg == nil {
		t.Fatalf("expected error for append without previous request")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), "type", "invalid_value", "response.create")
}

func TestNormalizeResponsesWebsocketRequestAppendAllowsSupportedInclude(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)

	raw := []byte(`{"type":"response.append","include":["reasoning.encrypted_content"],"input":[]}`)

	normalized, _, errMsg := normalizeResponsesWebsocketRequest(raw, lastRequest, []byte("[]"))
	if errMsg != nil {
		t.Fatalf("unexpected error for supported include on append: %v", errMsg.Error)
	}
	if got := gjson.GetBytes(normalized, "include.0").String(); got != "reasoning.encrypted_content" {
		t.Fatalf("normalized include[0] = %q, want reasoning.encrypted_content; normalized=%s", got, normalized)
	}
}

func TestNormalizeResponsesWebsocketRequestAppendRejectsNonArrayInput(t *testing.T) {
	lastRequest := []byte(`{"model":"test-model","stream":true,"input":[{"type":"message","id":"msg-1"}]}`)
	raw := []byte(`{"type":"response.append","input":"hello"}`)

	_, _, errMsg := normalizeResponsesWebsocketRequest(raw, lastRequest, []byte("[]"))
	if errMsg == nil {
		t.Fatalf("expected error for non-array append input")
	}
	if errMsg.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusBadRequest)
	}
	assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), "input", "invalid_type", "input")
}

func TestWebsocketJSONPayloadsFromChunk(t *testing.T) {
	chunk := []byte("event: response.created\n\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\"}}\n\ndata: [DONE]\n")

	payloads := websocketJSONPayloadsFromChunk(chunk)
	if len(payloads) != 1 {
		t.Fatalf("payloads len = %d, want 1", len(payloads))
	}
	if gjson.GetBytes(payloads[0], "type").String() != "response.created" {
		t.Fatalf("unexpected payload type: %s", gjson.GetBytes(payloads[0], "type").String())
	}
}

func TestWebsocketJSONPayloadsFromPlainJSONChunk(t *testing.T) {
	chunk := []byte(`{"type":"response.completed","response":{"id":"resp-1"}}`)

	payloads := websocketJSONPayloadsFromChunk(chunk)
	if len(payloads) != 1 {
		t.Fatalf("payloads len = %d, want 1", len(payloads))
	}
	if gjson.GetBytes(payloads[0], "type").String() != "response.completed" {
		t.Fatalf("unexpected payload type: %s", gjson.GetBytes(payloads[0], "type").String())
	}
}

func TestResponseCompletedOutputFromPayload(t *testing.T) {
	payload := []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[{"type":"message","id":"out-1"}]}}`)

	output := responseCompletedOutputFromPayload(payload)
	items := gjson.ParseBytes(output).Array()
	if len(items) != 1 {
		t.Fatalf("output len = %d, want 1", len(items))
	}
	if items[0].Get("id").String() != "out-1" {
		t.Fatalf("unexpected output id: %s", items[0].Get("id").String())
	}
}

func TestNormalizeResponsesWebsocketTerminalPayloadCompleted(t *testing.T) {
	payload := []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[{"type":"message","id":"out-1"}]}}`)

	normalized, terminal := normalizeResponsesWebsocketTerminalPayload(payload)
	if !terminal {
		t.Fatalf("expected response.completed to be treated as terminal")
	}
	if got := gjson.GetBytes(normalized, "type").String(); got != "response.done" {
		t.Fatalf("normalized type = %q, want response.done", got)
	}
	if got := gjson.GetBytes(responseCompletedOutputFromPayload(normalized), "0.id").String(); got != "out-1" {
		t.Fatalf("normalized output id = %q, want out-1", got)
	}
}

func TestNormalizeResponsesWebsocketTerminalPayloadIncomplete(t *testing.T) {
	payload := []byte(`{"type":"response.incomplete","response":{"id":"resp-1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","id":"out-1"}]}}`)

	normalized, terminal := normalizeResponsesWebsocketTerminalPayload(payload)
	if !terminal {
		t.Fatalf("expected response.incomplete to be treated as terminal")
	}
	if got := gjson.GetBytes(normalized, "type").String(); got != "response.done" {
		t.Fatalf("normalized type = %q, want response.done", got)
	}
	if got := gjson.GetBytes(normalized, "response.status").String(); got != "incomplete" {
		t.Fatalf("normalized response.status = %q, want incomplete", got)
	}
	if got := gjson.GetBytes(responseCompletedOutputFromPayload(normalized), "0.id").String(); got != "out-1" {
		t.Fatalf("normalized output id = %q, want out-1", got)
	}
}

func TestProcessResponsesWebsocketStreamChunkAttachesConversationAndStoresTerminalResponse(t *testing.T) {
	resetStoredOpenAIResponsesForTest()
	resetStoredOpenAIConversationsForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)
	t.Cleanup(resetStoredOpenAIConversationsForTest)

	conversation := defaultStoredOpenAIConversationStore.Create(nil, nil)

	rawRequestJSON := []byte(`{
		"model":"test-model",
		"store":true,
		"input":[
			{
				"id":"msg_in",
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"follow up"}]
			}
		]
	}`)
	chunk := []byte("event: response.completed\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ws_conv\",\"object\":\"response\",\"output\":[{\"id\":\"msg_out\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello back\"}]}]}}\n\n")

	processed := processResponsesWebsocketStreamChunk(
		rawRequestJSON,
		&openAIConversationExecutionContext{
			ID: conversation.ID,
			CurrentInputItems: []byte(`[{
				"id":"msg_in",
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"follow up"}]
			}]`),
		},
		chunk,
	)

	if !strings.Contains(string(processed), `"conversation":{"id":"`+conversation.ID+`"}`) {
		t.Fatalf("expected conversation linkage in processed chunk, got %s", processed)
	}

	storedResponse, ok := defaultStoredOpenAIResponseStore.Load("resp_ws_conv")
	if !ok {
		t.Fatalf("expected stored websocket response")
	}
	if got := gjson.GetBytes(storedResponse.Response, "conversation.id").String(); got != conversation.ID {
		t.Fatalf("stored response conversation.id = %q, want %q; response=%s", got, conversation.ID, storedResponse.Response)
	}
	if got := gjson.GetBytes(storedResponse.InputItems, "0.id").String(); got != "msg_in" {
		t.Fatalf("stored input item id = %q, want msg_in; input=%s", got, storedResponse.InputItems)
	}

	storedConversation, ok := defaultStoredOpenAIConversationStore.Load(conversation.ID)
	if !ok {
		t.Fatalf("expected stored conversation")
	}
	if len(storedConversation.Items) != 2 {
		t.Fatalf("conversation items len = %d, want 2", len(storedConversation.Items))
	}
	if got := asString(storedConversation.Items[0]["id"]); got != "msg_in" {
		t.Fatalf("conversation input item id = %q, want msg_in", got)
	}
	if got := asString(storedConversation.Items[1]["id"]); got != "msg_out" {
		t.Fatalf("conversation output item id = %q, want msg_out", got)
	}
}

func TestAppendWebsocketEvent(t *testing.T) {
	var builder strings.Builder

	appendWebsocketEvent(&builder, "request", []byte("  {\"type\":\"response.create\"}\n"))
	appendWebsocketEvent(&builder, "response", []byte("{\"type\":\"response.created\"}"))

	got := builder.String()
	if !strings.Contains(got, "websocket.request\n{\"type\":\"response.create\"}\n") {
		t.Fatalf("request event not found in body: %s", got)
	}
	if !strings.Contains(got, "websocket.response\n{\"type\":\"response.created\"}\n") {
		t.Fatalf("response event not found in body: %s", got)
	}
}

func TestSetWebsocketRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	setWebsocketRequestBody(c, " \n ")
	if _, exists := c.Get(wsRequestBodyKey); exists {
		t.Fatalf("request body key should not be set for empty body")
	}

	setWebsocketRequestBody(c, "event body")
	value, exists := c.Get(wsRequestBodyKey)
	if !exists {
		t.Fatalf("request body key not set")
	}
	bodyBytes, ok := value.([]byte)
	if !ok {
		t.Fatalf("request body key type mismatch")
	}
	if string(bodyBytes) != "event body" {
		t.Fatalf("request body = %q, want %q", string(bodyBytes), "event body")
	}
}

func TestResponsesWebsocket_RejectsUnavailableModelBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"missing-model","input":[]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "model" {
		t.Fatalf("error.param = %q, want model; payload=%s", got, payload)
	}
	if got := gjson.GetBytes(payload, "error.code").String(); got != "invalid_value" {
		t.Fatalf("error.code = %q, want invalid_value; payload=%s", got, payload)
	}
	if !strings.Contains(gjson.GetBytes(payload, "error.message").String(), "missing-model") {
		t.Fatalf("expected missing model name in validation error, got %s", payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsBridgedCustomToolInputBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonTextToolOutputArrayBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "function_call_output input_image",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":[{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}]}`,
			wantParam:   "input[0].output[0].type",
		},
		{
			name:        "custom_tool_call_output input_image",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":[{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"https://example.com/pwd.png"}]}]}`,
			wantParam:   "input[0].output[0].type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != tc.wantParam {
				t.Fatalf("error.param = %q, want %q; payload=%s", got, tc.wantParam, payload)
			}
			if !strings.Contains(gjson.GetBytes(payload, "error.message").String(), "input_image") {
				t.Fatalf("expected input_image guidance, got %s", payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_AllowsUnsupportedResponsesInputItemToReachExecutionForCodexNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-2",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "codex",
		Version: "gpt-5-2",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-2","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsScopedNativeInputItemsForCodexDespiteMixedProviderModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarnessWithProvider(t, "codex")
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "codex",
		Version: "gpt-5-4",
	})
	registerSurfaceModel(t, "surface-ws-mixed-auggie-"+t.Name(), "auggie", &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("accessScopeProvider", "codex")
		c.Set("accessScopeAuthID", auth.ID)
		c.Next()
	})
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":[{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"ls"}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsNativeInputItemsForOpenAICompatibleNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarnessWithProvider(t, "openai-compatibility")
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "openai",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":[{"type":"item_reference","id":"rs_native_1"}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsUnsupportedMessageContentTypeBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if !strings.Contains(gjson.GetBytes(payload, "error.message").String(), "input_image") {
		t.Fatalf("expected unsupported message content validation error, got %s", payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonBooleanParallelToolCallsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","parallel_tool_calls":"false"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "parallel_tool_calls" {
		t.Fatalf("error.param = %q, want parallel_tool_calls; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonIntegerMaxToolCallsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","max_tool_calls":1.5}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "max_tool_calls" {
		t.Fatalf("error.param = %q, want max_tool_calls; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonIntegerMaxOutputTokensBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","max_output_tokens":64.5}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "max_output_tokens" {
		t.Fatalf("error.param = %q, want max_output_tokens; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonIntegerTopLogprobsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","top_logprobs":1.5}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "top_logprobs" {
		t.Fatalf("error.param = %q, want top_logprobs; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsOutOfRangeTopLogprobsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","top_logprobs":21}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "top_logprobs" {
		t.Fatalf("error.param = %q, want top_logprobs; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonNumericTemperatureBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","temperature":"0.7"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "temperature" {
		t.Fatalf("error.param = %q, want temperature; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsOutOfRangeTemperatureBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","temperature":2.5}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "temperature" {
		t.Fatalf("error.param = %q, want temperature; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonNumericTopPBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","top_p":"0.9"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "top_p" {
		t.Fatalf("error.param = %q, want top_p; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsOutOfRangeTopPBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","top_p":1.1}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "top_p" {
		t.Fatalf("error.param = %q, want top_p; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonStringServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","service_tier":1}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "service_tier" {
		t.Fatalf("error.param = %q, want service_tier; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsUnsupportedServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","service_tier":"turbo"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "service_tier" {
		t.Fatalf("error.param = %q, want service_tier; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsInvalidPromptCacheAndSafetyControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
		wantCode    string
	}{
		{
			name:        "prompt_cache_key",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","prompt_cache_key":1}`,
			wantParam:   "prompt_cache_key",
			wantCode:    "invalid_type",
		},
		{
			name:        "prompt_cache_retention",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","prompt_cache_retention":"7d"}`,
			wantParam:   "prompt_cache_retention",
			wantCode:    "invalid_value",
		},
		{
			name:        "safety_identifier",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","safety_identifier":1}`,
			wantParam:   "safety_identifier",
			wantCode:    "invalid_type",
		},
		{
			name:        "user",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","user":1}`,
			wantParam:   "user",
			wantCode:    "invalid_type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != tc.wantParam {
				t.Fatalf("error.param = %q, want %q; payload=%s", got, tc.wantParam, payload)
			}
			if got := gjson.GetBytes(payload, "error.code").String(); got != tc.wantCode {
				t.Fatalf("error.code = %q, want %q; payload=%s", got, tc.wantCode, payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsUnsupportedTextVerbosityBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","text":{"verbosity":"loud"}}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "text.verbosity" {
		t.Fatalf("error.param = %q, want text.verbosity; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsSupportedTextVerbosityBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","text":{"verbosity":"high"}}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsUnsupportedReasoningEffortBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","reasoning":{"effort":"banana"}}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "reasoning.effort" {
		t.Fatalf("error.param = %q, want reasoning.effort; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonPreservedReasoningEffortBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, effort := range []string{"none", "minimal", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","model":"gpt-5-4","input":"hello","reasoning":{"effort":%q}}`, effort))); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != "reasoning.effort" {
				t.Fatalf("error.param = %q, want reasoning.effort; payload=%s", got, payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_AllowsReasoningSummaryControlsBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
	}{
		{
			name:        "reasoning_summary",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","reasoning":{"summary":"detailed"}}`,
		},
		{
			name:        "reasoning_generate_summary",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","reasoning":{"generate_summary":true}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
				t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
			}
			if executor.streamExecuteCalls != 1 {
				t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieSamplingControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "max_tool_calls",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","max_tool_calls":1}`,
			wantParam:   "max_tool_calls",
		},
		{
			name:        "max_output_tokens",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","max_output_tokens":64}`,
			wantParam:   "max_output_tokens",
		},
		{
			name:        "top_logprobs",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","top_logprobs":5}`,
			wantParam:   "top_logprobs",
		},
		{
			name:        "temperature",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","temperature":0.7}`,
			wantParam:   "temperature",
		},
		{
			name:        "top_p",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","top_p":0.9}`,
			wantParam:   "top_p",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != tc.wantParam {
				t.Fatalf("error.param = %q, want %s; payload=%s", got, tc.wantParam, payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieServiceTierBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","service_tier":"priority"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "service_tier" {
		t.Fatalf("error.param = %q, want service_tier; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieTruncationAutoBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","truncation":"auto"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "truncation" {
		t.Fatalf("error.param = %q, want truncation; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggiePromptTemplateBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","prompt":{"id":"pmpt_test","version":"1","variables":{"name":"world"}}}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "prompt" {
		t.Fatalf("error.param = %q, want prompt; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieContextManagementBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","context_management":[{"type":"compaction","compact_threshold":1000}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "context_management" {
		t.Fatalf("error.param = %q, want context_management; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsAuggiePromptCacheAndSafetyControlsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "prompt_cache_key",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","prompt_cache_key":"cache-key-1"}`,
			wantParam:   "prompt_cache_key",
		},
		{
			name:        "prompt_cache_retention",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","prompt_cache_retention":"24h"}`,
			wantParam:   "prompt_cache_retention",
		},
		{
			name:        "safety_identifier",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","safety_identifier":"safe-user-1"}`,
			wantParam:   "safety_identifier",
		},
		{
			name:        "user",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","user":"user_123"}`,
			wantParam:   "user",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
				t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
			}
			if executor.streamExecuteCalls != 1 {
				t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieStructuredOutputBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
	}{
		{
			name:        "json_object",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"hello","text":{"format":{"type":"json_object"}}}`,
		},
		{
			name: "json_schema",
			requestBody: `{
				"type":"response.create",
				"model":"gpt-5-4",
				"input":"hello",
				"text":{
					"format":{
						"type":"json_schema",
						"name":"pwd_result",
						"schema":{
							"type":"object",
							"properties":{"cwd":{"type":"string"}}
						}
					}
				}
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != "text.format.type" {
				t.Fatalf("error.param = %q, want text.format.type; payload=%s", got, payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieOutputTextLogprobsIncludeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":"hello","include":["message.output_text.logprobs"]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "include[0]" {
		t.Fatalf("error.param = %q, want include[0]; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieExpandedIncludeValuesBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []string{
		"code_interpreter_call.outputs",
		"computer_call_output.output.image_url",
		"file_search_call.results",
		"message.input_image.image_url",
	}

	for _, includeValue := range testCases {
		t.Run(includeValue, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			payload := fmt.Sprintf(`{"type":"response.create","model":"gpt-5-4","input":"hello","include":["%s"]}`, includeValue)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payloadBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payloadBytes, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payloadBytes)
			}
			if got := int(gjson.GetBytes(payloadBytes, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payloadBytes)
			}
			if got := gjson.GetBytes(payloadBytes, "error.param").String(); got != "include[0]" {
				t.Fatalf("error.param = %q, want include[0]; payload=%s", got, payloadBytes)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsNonPreservedAuggieForcedToolChoiceFormsBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "required",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use pwd to inspect the current directory."}]}],"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],"tool_choice":"required"}`,
			wantParam:   "tool_choice",
		},
		{
			name:        "function selection",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use pwd to inspect the current directory."}]}],"tools":[{"type":"function","name":"run_shell","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},{"type":"function","name":"list_files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}],"tool_choice":{"type":"function","function":{"name":"run_shell"}}}`,
			wantParam:   "tool_choice.type",
		},
		{
			name:        "custom selection",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Use bash to print pwd."}]}],"tools":[{"type":"custom","name":"bash"}],"tool_choice":{"type":"custom","name":"bash"}}`,
			wantParam:   "tool_choice.type",
		},
		{
			name:        "allowed tools required",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"get_weather","strict":false,"parameters":{"type":"object","properties":{"location":{"type":"string"}}}},{"type":"function","name":"list_files","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string"}}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"get_weather"}}]}}}`,
			wantParam:   "tool_choice.allowed_tools.mode",
		},
		{
			name:        "web search selection",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Find the latest OpenAI news","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`,
			wantParam:   "tool_choice.type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != tc.wantParam {
				t.Fatalf("error.param = %q, want %q; payload=%s", got, tc.wantParam, payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_AllowsAuggieBuiltInWebSearchToolConfigBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
		wantParam   string
	}{
		{
			name:        "web_search_search_context_size",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Find the latest OpenAI news","tools":[{"type":"web_search","search_context_size":"high"}]}`,
			wantParam:   "tools[0].search_context_size",
		},
		{
			name:        "web_search_filters",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Find the latest OpenAI news","tools":[{"type":"web_search","filters":{"allowed_domains":["openai.com"]}}]}`,
			wantParam:   "tools[0].filters",
		},
		{
			name:        "web_search_preview_search_content_types",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Find the latest OpenAI images","tools":[{"type":"web_search_preview","search_content_types":["image"]}]}`,
			wantParam:   "tools[0].search_content_types",
		},
		{
			name:        "web_search_user_location",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Find local weather news","tools":[{"type":"web_search","user_location":{"type":"approximate","country":"US","timezone":"America/Los_Angeles"}}]}`,
			wantParam:   "tools[0].user_location",
		},
		{
			name:        "web_search_external_web_access",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Find the latest OpenAI news","tools":[{"type":"web_search","external_web_access":true}]}`,
			wantParam:   "tools[0].external_web_access",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
				t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
			}
			if executor.streamExecuteCalls != 1 {
				t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_RejectsDefaultStrictFunctionToolsBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name        string
		requestBody string
	}{
		{
			name:        "strict omitted defaults true",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Use pwd to inspect the current directory.","tools":[{"type":"function","name":"run_shell","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`,
		},
		{
			name:        "strict true",
			requestBody: `{"type":"response.create","model":"gpt-5-4","input":"Use pwd to inspect the current directory.","tools":[{"type":"function","name":"run_shell","strict":true,"parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, manager, auth := newOpenAISurfaceTestHarness(t)
			registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
				ID:      "gpt-5-4",
				Object:  "model",
				OwnedBy: "auggie",
				Type:    "auggie",
				Version: "gpt-5-4",
			})

			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
			h := NewOpenAIResponsesAPIHandler(base)
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)

			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial websocket: %v", err)
			}
			defer conn.Close()

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.requestBody)); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if got := gjson.GetBytes(payload, "type").String(); got != "error" {
				t.Fatalf("event type = %q, want error; payload=%s", got, payload)
			}
			if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
			}
			if got := gjson.GetBytes(payload, "error.param").String(); got != "tools[0].strict" {
				t.Fatalf("error.param = %q, want tools[0].strict; payload=%s", got, payload)
			}
			if executor.streamExecuteCalls != 0 {
				t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
			}
		})
	}
}

func TestResponsesWebsocket_AllowsCustomToolGrammarBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run pwd"}]}],"tools":[{"type":"custom","name":"bash","format":{"type":"grammar","syntax":"regex","definition":".*"}}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_RejectsDeferredCustomToolLoadingBeforeExecutionForAuggie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-4",
		Object:  "model",
		OwnedBy: "auggie",
		Type:    "auggie",
		Version: "gpt-5-4",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run pwd"}]}],"tools":[{"type":"custom","name":"bash","defer_loading":true}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "error" {
		t.Fatalf("event type = %q, want error; payload=%s", got, payload)
	}
	if got := int(gjson.GetBytes(payload, "status").Int()); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; payload=%s", got, http.StatusBadRequest, payload)
	}
	if got := gjson.GetBytes(payload, "error.param").String(); got != "tools[0].defer_loading" {
		t.Fatalf("error.param = %q, want tools[0].defer_loading; payload=%s", got, payload)
	}
	if !strings.Contains(gjson.GetBytes(payload, "error.message").String(), "defer_loading") {
		t.Fatalf("expected defer_loading guidance, got %s", payload)
	}
	if executor.streamExecuteCalls != 0 {
		t.Fatalf("stream execute calls = %d, want 0", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsInputFileMessageContentToReachExecutionForClaudeResponsesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-sonnet-4-5",
		Object:  "model",
		OwnedBy: "anthropic",
		Type:    "claude",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"claude-sonnet-4-5","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-1","filename":"notes.txt"}]}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_AllowsInputFileMessageContentForOpenAICompatibleNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarnessWithProvider(t, "openai-compatibility")
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "gpt-5-1",
		Object:  "model",
		OwnedBy: "openai",
		Type:    "openai",
		Version: "gpt-5-1",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-5-1","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file-1","filename":"notes.txt"}]}]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
}

func TestResponsesWebsocket_StreamsDoneForRegisteredModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"claude-opus-4-6","input":[]}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if got := gjson.GetBytes(payload, "type").String(); got != "response.done" {
		t.Fatalf("event type = %q, want response.done; payload=%s", got, payload)
	}
	if got := gjson.GetBytes(payload, "response.id").String(); got == "" {
		t.Fatalf("expected response id in payload, got %s", payload)
	}
	if executor.streamExecuteCalls != 1 {
		t.Fatalf("stream execute calls = %d, want 1", executor.streamExecuteCalls)
	}
	if executor.lastModel != "claude-opus-4-6" {
		t.Fatalf("last model = %q, want %q", executor.lastModel, "claude-opus-4-6")
	}
}
