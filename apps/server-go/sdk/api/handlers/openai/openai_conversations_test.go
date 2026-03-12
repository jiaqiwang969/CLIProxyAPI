package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestConversations_CreateRetrieveAndListItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.GET("/v1/conversations/:conversation_id", h.GetConversation)
	router.GET("/v1/conversations/:conversation_id/items", h.GetConversationItems)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{
		"metadata":{"topic":"weather"},
		"items":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "object").String(); got != "conversation" {
		t.Fatalf("conversation object = %q, want conversation; body=%s", got, createResp.Body.String())
	}
	conversationID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()
	if !strings.HasPrefix(conversationID, "conv_") {
		t.Fatalf("conversation id = %q, want conv_ prefix; body=%s", conversationID, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "metadata.topic").String(); got != "weather" {
		t.Fatalf("conversation metadata.topic = %q, want weather; body=%s", got, createResp.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID, nil)
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "id").String(); got != conversationID {
		t.Fatalf("retrieved id = %q, want %q; body=%s", got, conversationID, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "metadata.topic").String(); got != "weather" {
		t.Fatalf("retrieved metadata.topic = %q, want weather; body=%s", got, getResp.Body.String())
	}

	itemsReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID+"/items?order=asc", nil)
	itemsResp := httptest.NewRecorder()
	router.ServeHTTP(itemsResp, itemsReq)

	if itemsResp.Code != http.StatusOK {
		t.Fatalf("items status = %d, want %d; body=%s", itemsResp.Code, http.StatusOK, itemsResp.Body.String())
	}
	if got := gjson.GetBytes(itemsResp.Body.Bytes(), "object").String(); got != "list" {
		t.Fatalf("items object = %q, want list; body=%s", got, itemsResp.Body.String())
	}
	if got := gjson.GetBytes(itemsResp.Body.Bytes(), "data.#").Int(); got != 1 {
		t.Fatalf("items data length = %d, want 1; body=%s", got, itemsResp.Body.String())
	}
	if got := gjson.GetBytes(itemsResp.Body.Bytes(), "data.0.type").String(); got != "message" {
		t.Fatalf("items data[0].type = %q, want message; body=%s", got, itemsResp.Body.String())
	}
	if got := gjson.GetBytes(itemsResp.Body.Bytes(), "data.0.role").String(); got != "user" {
		t.Fatalf("items data[0].role = %q, want user; body=%s", got, itemsResp.Body.String())
	}
	if got := gjson.GetBytes(itemsResp.Body.Bytes(), "data.0.content.0.text").String(); got != "hello" {
		t.Fatalf("items data[0].content[0].text = %q, want hello; body=%s", got, itemsResp.Body.String())
	}
}

func TestConversations_AddItemsReturnsAddedItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.POST("/v1/conversations/:conversation_id/items", h.AddConversationItems)
	router.GET("/v1/conversations/:conversation_id/items", h.GetConversationItems)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}
	conversationID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()

	addReq := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conversationID+"/items", strings.NewReader(`{
		"items":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
		]
	}`))
	addReq.Header.Set("Content-Type", "application/json")
	addResp := httptest.NewRecorder()
	router.ServeHTTP(addResp, addReq)

	if addResp.Code != http.StatusOK {
		t.Fatalf("add status = %d, want %d; body=%s", addResp.Code, http.StatusOK, addResp.Body.String())
	}
	if got := gjson.GetBytes(addResp.Body.Bytes(), "object").String(); got != "list" {
		t.Fatalf("add object = %q, want list; body=%s", got, addResp.Body.String())
	}
	if got := gjson.GetBytes(addResp.Body.Bytes(), "data.#").Int(); got != 1 {
		t.Fatalf("added items length = %d, want 1; body=%s", got, addResp.Body.String())
	}
	if got := gjson.GetBytes(addResp.Body.Bytes(), "data.0.role").String(); got != "assistant" {
		t.Fatalf("added item role = %q, want assistant; body=%s", got, addResp.Body.String())
	}
	if got := gjson.GetBytes(addResp.Body.Bytes(), "data.0.content.0.text").String(); got != "done" {
		t.Fatalf("added item content = %q, want done; body=%s", got, addResp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID+"/items?order=asc", nil)
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)

	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	if got := gjson.GetBytes(listResp.Body.Bytes(), "data.#").Int(); got != 1 {
		t.Fatalf("conversation items length = %d, want 1; body=%s", got, listResp.Body.String())
	}
}

func TestConversations_ItemsEndpointsRejectUnsupportedIncludeQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.GET("/v1/conversations/:conversation_id/items", h.GetConversationItems)
	router.POST("/v1/conversations/:conversation_id/items", h.AddConversationItems)
	router.GET("/v1/conversations/:conversation_id/items/:item_id", h.GetConversationItem)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{
		"items":[
			{"id":"msg_seed","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}
	conversationID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()

	testCases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{
			name:   "list items",
			method: http.MethodGet,
			target: "/v1/conversations/" + conversationID + "/items?include=step_details.tool_calls[*].file_search.results[*].content",
		},
		{
			name:   "add items",
			method: http.MethodPost,
			target: "/v1/conversations/" + conversationID + "/items?include=step_details.tool_calls[*].file_search.results[*].content",
			body: `{
				"items":[
					{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
				]
			}`,
		},
		{
			name:   "get item",
			method: http.MethodGet,
			target: "/v1/conversations/" + conversationID + "/items/msg_seed?include=step_details.tool_calls[*].file_search.results[*].content",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "include", "unsupported_parameter", "include")
		})
	}
}

func TestConversations_AddItemsRejectsEmptyItemsArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.POST("/v1/conversations/:conversation_id/items", h.AddConversationItems)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	conversationID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conversationID+"/items", strings.NewReader(`{"items":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "items", "invalid_value", "at least one item")
}

func TestConversations_UpdateRejectsMissingMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.POST("/v1/conversations/:conversation_id", h.UpdateConversation)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	conversationID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conversationID, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	assertSurfaceOpenAIErrorBody(t, resp.Body.String(), "metadata", "missing_required_parameter", "metadata")
}

func TestParseOpenAIConversationReferenceRejectsInvalidValues(t *testing.T) {
	testCases := []struct {
		name        string
		raw         string
		wantParam   string
		wantCode    string
		wantMessage string
	}{
		{
			name:        "empty string",
			raw:         `{"conversation":""}`,
			wantParam:   "conversation",
			wantCode:    "invalid_value",
			wantMessage: "conversation",
		},
		{
			name:        "array",
			raw:         `{"conversation":[]}`,
			wantParam:   "conversation",
			wantCode:    "invalid_type",
			wantMessage: "conversation",
		},
		{
			name:        "empty object id",
			raw:         `{"conversation":{"id":""}}`,
			wantParam:   "conversation.id",
			wantCode:    "invalid_value",
			wantMessage: "conversation.id",
		},
		{
			name:        "non string object id",
			raw:         `{"conversation":{"id":1}}`,
			wantParam:   "conversation.id",
			wantCode:    "invalid_type",
			wantMessage: "conversation.id",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, errMsg := parseOpenAIConversationReference([]byte(tc.raw), "responses")
			if errMsg == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), tc.wantParam, tc.wantCode, tc.wantMessage)
		})
	}
}

func TestParseOpenAIConversationRequestRejectsInvalidMetadataAndItemsShapes(t *testing.T) {
	testCases := []struct {
		name        string
		raw         string
		parse       func([]byte) *interfaces.ErrorMessage
		wantParam   string
		wantCode    string
		wantMessage string
	}{
		{
			name: "metadata type",
			raw:  `{"metadata":"topic"}`,
			parse: func(raw []byte) *interfaces.ErrorMessage {
				_, _, errMsg := parseOpenAIConversationCreateRequest(raw)
				return errMsg
			},
			wantParam:   "metadata",
			wantCode:    "invalid_type",
			wantMessage: "metadata",
		},
		{
			name: "items type",
			raw:  `{"items":"hello"}`,
			parse: func(raw []byte) *interfaces.ErrorMessage {
				_, _, errMsg := parseOpenAIConversationCreateRequest(raw)
				return errMsg
			},
			wantParam:   "items",
			wantCode:    "invalid_type",
			wantMessage: "items",
		},
		{
			name: "items too many",
			raw:  `{"items":[` + strings.Repeat(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},`, maxOpenAIConversationReqItems) + `{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
			parse: func(raw []byte) *interfaces.ErrorMessage {
				_, _, errMsg := parseOpenAIConversationCreateRequest(raw)
				return errMsg
			},
			wantParam:   "items",
			wantCode:    "invalid_value",
			wantMessage: "items",
		},
		{
			name: "items entry type",
			raw:  `{"items":["hello"]}`,
			parse: func(raw []byte) *interfaces.ErrorMessage {
				_, _, errMsg := parseOpenAIConversationCreateRequest(raw)
				return errMsg
			},
			wantParam:   "items[0]",
			wantCode:    "invalid_type",
			wantMessage: "items[0]",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := tc.parse([]byte(tc.raw))
			if errMsg == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			assertSurfaceOpenAIErrorBody(t, errMsg.Error.Error(), tc.wantParam, tc.wantCode, tc.wantMessage)
		})
	}
}

func TestResponses_ConversationPrependsStoredItemsAndAppendsOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	resetStoredOpenAIConversationsForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_conversation",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_conversation_out",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hello back","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.GET("/v1/conversations/:conversation_id/items", h.GetConversationItems)
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id/input_items", h.GetResponseInputItems)

	createConversationReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{
		"items":[
			{"id":"msg_seed","type":"message","role":"user","content":[{"type":"input_text","text":"seed"}]}
		]
	}`))
	createConversationReq.Header.Set("Content-Type", "application/json")
	createConversationResp := httptest.NewRecorder()
	router.ServeHTTP(createConversationResp, createConversationReq)
	if createConversationResp.Code != http.StatusOK {
		t.Fatalf("create conversation status = %d, want %d; body=%s", createConversationResp.Code, http.StatusOK, createConversationResp.Body.String())
	}
	conversationID := gjson.GetBytes(createConversationResp.Body.Bytes(), "id").String()

	responseReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"claude-opus-4-6",
		"conversation":"`+conversationID+`",
		"input":"follow up"
	}`))
	responseReq.Header.Set("Content-Type", "application/json")
	responseResp := httptest.NewRecorder()
	router.ServeHTTP(responseResp, responseReq)

	if responseResp.Code != http.StatusOK {
		t.Fatalf("response status = %d, want %d; body=%s", responseResp.Code, http.StatusOK, responseResp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(executor.lastPayload, "input.#").Int(); got != 2 {
		t.Fatalf("upstream input length = %d, want 2; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "input.0.content.0.text").String(); got != "seed" {
		t.Fatalf("upstream input[0] text = %q, want seed; payload=%s", got, executor.lastPayload)
	}
	if got := gjson.GetBytes(executor.lastPayload, "input.1.content.0.text").String(); got != "follow up" {
		t.Fatalf("upstream input[1] text = %q, want follow up; payload=%s", got, executor.lastPayload)
	}

	inputItemsReq := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_conversation/input_items?order=asc", nil)
	inputItemsResp := httptest.NewRecorder()
	router.ServeHTTP(inputItemsResp, inputItemsReq)

	if inputItemsResp.Code != http.StatusOK {
		t.Fatalf("input_items status = %d, want %d; body=%s", inputItemsResp.Code, http.StatusOK, inputItemsResp.Body.String())
	}
	if got := gjson.GetBytes(inputItemsResp.Body.Bytes(), "data.#").Int(); got != 2 {
		t.Fatalf("response input_items length = %d, want 2; body=%s", got, inputItemsResp.Body.String())
	}
	if got := gjson.GetBytes(inputItemsResp.Body.Bytes(), "data.0.content.0.text").String(); got != "seed" {
		t.Fatalf("response input_items[0] text = %q, want seed; body=%s", got, inputItemsResp.Body.String())
	}
	if got := gjson.GetBytes(inputItemsResp.Body.Bytes(), "data.1.content.0.text").String(); got != "follow up" {
		t.Fatalf("response input_items[1] text = %q, want follow up; body=%s", got, inputItemsResp.Body.String())
	}

	conversationItemsReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID+"/items?order=asc", nil)
	conversationItemsResp := httptest.NewRecorder()
	router.ServeHTTP(conversationItemsResp, conversationItemsReq)

	if conversationItemsResp.Code != http.StatusOK {
		t.Fatalf("conversation items status = %d, want %d; body=%s", conversationItemsResp.Code, http.StatusOK, conversationItemsResp.Body.String())
	}
	if got := gjson.GetBytes(conversationItemsResp.Body.Bytes(), "data.#").Int(); got != 3 {
		t.Fatalf("conversation items length = %d, want 3; body=%s", got, conversationItemsResp.Body.String())
	}
	if got := gjson.GetBytes(conversationItemsResp.Body.Bytes(), "data.0.content.0.text").String(); got != "seed" {
		t.Fatalf("conversation items[0] text = %q, want seed; body=%s", got, conversationItemsResp.Body.String())
	}
	if got := gjson.GetBytes(conversationItemsResp.Body.Bytes(), "data.1.content.0.text").String(); got != "follow up" {
		t.Fatalf("conversation items[1] text = %q, want follow up; body=%s", got, conversationItemsResp.Body.String())
	}
	if got := gjson.GetBytes(conversationItemsResp.Body.Bytes(), "data.2.content.0.text").String(); got != "hello back" {
		t.Fatalf("conversation items[2] text = %q, want hello back; body=%s", got, conversationItemsResp.Body.String())
	}
}

func TestResponses_RejectsUnknownConversationBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

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
	router.POST("/v1/responses", h.Responses)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"claude-opus-4-6",
		"conversation":"conv_missing",
		"input":"hello"
	}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	if executor.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", executor.executeCalls)
	}
	if !strings.Contains(resp.Body.String(), `"type":"invalid_request_error"`) {
		t.Fatalf("expected invalid_request_error body, got %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "conv_missing") {
		t.Fatalf("expected missing conversation id in body, got %s", resp.Body.String())
	}
}

func TestConversations_UpdateRetrieveDeleteItemAndDeleteConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIConversationsForTest()

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.POST("/v1/conversations/:conversation_id", h.UpdateConversation)
	router.GET("/v1/conversations/:conversation_id/items/:item_id", h.GetConversationItem)
	router.DELETE("/v1/conversations/:conversation_id/items/:item_id", h.DeleteConversationItem)
	router.GET("/v1/conversations/:conversation_id/items", h.GetConversationItems)
	router.DELETE("/v1/conversations/:conversation_id", h.DeleteConversation)
	router.GET("/v1/conversations/:conversation_id", h.GetConversation)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{
		"metadata":{"topic":"weather"},
		"items":[
			{"id":"msg_seed","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}
	conversationID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()

	updateReq := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conversationID, strings.NewReader(`{
		"metadata":{"topic":"billing","priority":"high"}
	}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	router.ServeHTTP(updateResp, updateReq)

	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}
	if got := gjson.GetBytes(updateResp.Body.Bytes(), "metadata.topic").String(); got != "billing" {
		t.Fatalf("updated metadata.topic = %q, want billing; body=%s", got, updateResp.Body.String())
	}
	if got := gjson.GetBytes(updateResp.Body.Bytes(), "metadata.priority").String(); got != "high" {
		t.Fatalf("updated metadata.priority = %q, want high; body=%s", got, updateResp.Body.String())
	}

	itemReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID+"/items/msg_seed", nil)
	itemResp := httptest.NewRecorder()
	router.ServeHTTP(itemResp, itemReq)

	if itemResp.Code != http.StatusOK {
		t.Fatalf("item status = %d, want %d; body=%s", itemResp.Code, http.StatusOK, itemResp.Body.String())
	}
	if got := gjson.GetBytes(itemResp.Body.Bytes(), "id").String(); got != "msg_seed" {
		t.Fatalf("item id = %q, want msg_seed; body=%s", got, itemResp.Body.String())
	}
	if got := gjson.GetBytes(itemResp.Body.Bytes(), "role").String(); got != "user" {
		t.Fatalf("item role = %q, want user; body=%s", got, itemResp.Body.String())
	}

	deleteItemReq := httptest.NewRequest(http.MethodDelete, "/v1/conversations/"+conversationID+"/items/msg_seed", nil)
	deleteItemResp := httptest.NewRecorder()
	router.ServeHTTP(deleteItemResp, deleteItemReq)

	if deleteItemResp.Code != http.StatusOK {
		t.Fatalf("delete item status = %d, want %d; body=%s", deleteItemResp.Code, http.StatusOK, deleteItemResp.Body.String())
	}
	if got := gjson.GetBytes(deleteItemResp.Body.Bytes(), "object").String(); got != "conversation" {
		t.Fatalf("delete item object = %q, want conversation; body=%s", got, deleteItemResp.Body.String())
	}
	if got := gjson.GetBytes(deleteItemResp.Body.Bytes(), "id").String(); got != conversationID {
		t.Fatalf("delete item response id = %q, want %q; body=%s", got, conversationID, deleteItemResp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID+"/items?order=asc", nil)
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)

	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	if got := gjson.GetBytes(listResp.Body.Bytes(), "data.#").Int(); got != 0 {
		t.Fatalf("items length after delete = %d, want 0; body=%s", got, listResp.Body.String())
	}

	deleteConversationReq := httptest.NewRequest(http.MethodDelete, "/v1/conversations/"+conversationID, nil)
	deleteConversationResp := httptest.NewRecorder()
	router.ServeHTTP(deleteConversationResp, deleteConversationReq)

	if deleteConversationResp.Code != http.StatusOK {
		t.Fatalf("delete conversation status = %d, want %d; body=%s", deleteConversationResp.Code, http.StatusOK, deleteConversationResp.Body.String())
	}
	if got := gjson.GetBytes(deleteConversationResp.Body.Bytes(), "object").String(); got != "conversation.deleted" {
		t.Fatalf("delete conversation object = %q, want conversation.deleted; body=%s", got, deleteConversationResp.Body.String())
	}
	if got := gjson.GetBytes(deleteConversationResp.Body.Bytes(), "deleted").Bool(); !got {
		t.Fatalf("delete conversation deleted = %v, want true; body=%s", got, deleteConversationResp.Body.String())
	}

	getDeletedReq := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversationID, nil)
	getDeletedResp := httptest.NewRecorder()
	router.ServeHTTP(getDeletedResp, getDeletedReq)

	if getDeletedResp.Code != http.StatusNotFound {
		t.Fatalf("get deleted conversation status = %d, want %d; body=%s", getDeletedResp.Code, http.StatusNotFound, getDeletedResp.Body.String())
	}
}

func TestResponses_ConversationObjectFormIncludesConversationReferenceAndStoredRetrieval(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	resetStoredOpenAIConversationsForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_conversation_object",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_conversation_object_out",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hello back","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/conversations", h.CreateConversation)
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createConversationReq := httptest.NewRequest(http.MethodPost, "/v1/conversations", strings.NewReader(`{
		"items":[
			{"id":"msg_seed","type":"message","role":"user","content":[{"type":"input_text","text":"seed"}]}
		]
	}`))
	createConversationReq.Header.Set("Content-Type", "application/json")
	createConversationResp := httptest.NewRecorder()
	router.ServeHTTP(createConversationResp, createConversationReq)
	if createConversationResp.Code != http.StatusOK {
		t.Fatalf("create conversation status = %d, want %d; body=%s", createConversationResp.Code, http.StatusOK, createConversationResp.Body.String())
	}
	conversationID := gjson.GetBytes(createConversationResp.Body.Bytes(), "id").String()

	responseReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"claude-opus-4-6",
		"conversation":{"id":"`+conversationID+`"},
		"input":"follow up"
	}`))
	responseReq.Header.Set("Content-Type", "application/json")
	responseResp := httptest.NewRecorder()
	router.ServeHTTP(responseResp, responseReq)

	if responseResp.Code != http.StatusOK {
		t.Fatalf("response status = %d, want %d; body=%s", responseResp.Code, http.StatusOK, responseResp.Body.String())
	}
	if got := gjson.GetBytes(responseResp.Body.Bytes(), "conversation.id").String(); got != conversationID {
		t.Fatalf("response conversation.id = %q, want %q; body=%s", got, conversationID, responseResp.Body.String())
	}
	if got := gjson.GetBytes(executor.lastPayload, "input.#").Int(); got != 2 {
		t.Fatalf("upstream input length = %d, want 2; payload=%s", got, executor.lastPayload)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_conversation_object", nil)
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusOK {
		t.Fatalf("stored get status = %d, want %d; body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "conversation.id").String(); got != conversationID {
		t.Fatalf("stored response conversation.id = %q, want %q; body=%s", got, conversationID, getResp.Body.String())
	}
}

func TestAttachOpenAIConversationToStreamChunkAddsConversationToTerminalResponseEvent(t *testing.T) {
	chunk := []byte("event: response.completed\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream_conv\",\"object\":\"response\",\"output\":[{\"id\":\"msg_out\",\"type\":\"message\"}]}}\n\ndata: [DONE]\n")

	updated := attachOpenAIConversationToStreamChunk(&openAIConversationExecutionContext{ID: "conv_123"}, chunk)

	if !strings.Contains(string(updated), `"conversation":{"id":"conv_123"}`) {
		t.Fatalf("expected conversation linkage in terminal chunk, got %s", updated)
	}
}
