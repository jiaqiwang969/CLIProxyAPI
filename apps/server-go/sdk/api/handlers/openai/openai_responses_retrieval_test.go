package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

func TestResponses_GetResponseStreamReplaysStoredBackgroundLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_upstream_background_replay",
		"object":"response",
		"created_at":456,
		"status":"completed",
		"background":true,
		"output":[
			{
				"id":"msg_background_replay",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hello from replay","annotations":[],"logprobs":[]}]
			}
		]
	}`)
	executor.executeStarted = make(chan struct{}, 1)
	executor.executeRelease = make(chan struct{})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","background":true}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(createResp, createReq)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background create did not return before upstream execution completed")
	}
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	responseID := gjson.GetBytes(createResp.Body.Bytes(), "id").String()
	if responseID == "" {
		t.Fatalf("missing response id in create body=%s", createResp.Body.String())
	}

	select {
	case <-executor.executeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background execution did not start")
	}

	close(executor.executeRelease)
	waitForStoredOpenAIResponseStatus(t, router, responseID, "completed")

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+responseID+"?stream=true", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream; body=%s", got, resp.Body.String())
	}

	payloads, sawDone := parseResponseStreamPayloads(t, resp.Body.String())
	gotTypes := responseStreamPayloadTypes(payloads)
	wantTypes := []string{"response.created", "response.queued", "response.in_progress", "response.completed", "response.done"}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("stream event types = %v, want %v; body=%s", gotTypes, wantTypes, resp.Body.String())
	}
	if !sawDone {
		t.Fatalf("expected [DONE] marker in body=%s", resp.Body.String())
	}
	if got := payloads[len(payloads)-1].Get("response.status").String(); got != "completed" {
		t.Fatalf("terminal replay status = %q, want completed; body=%s", got, resp.Body.String())
	}
	if got := payloads[len(payloads)-1].Get("response.output.0.content.0.text").String(); got != "hello from replay" {
		t.Fatalf("terminal replay text = %q, want hello from replay; body=%s", got, resp.Body.String())
	}
}

func TestResponses_GetResponseStreamSynthesizesLifecycleForLegacyStoredResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	defaultStoredOpenAIResponseStore.Store(
		"resp_test_stream_legacy",
		[]byte(`{
			"id":"resp_test_stream_legacy",
			"object":"response",
			"created_at":123,
			"model":"claude-opus-4-6",
			"status":"completed",
			"background":false,
			"output":[
				{
					"id":"msg_test_stream_legacy",
					"type":"message",
					"status":"completed",
					"role":"assistant",
					"content":[{"type":"output_text","text":"legacy hello","annotations":[],"logprobs":[]}]
				}
			]
		}`),
		[]byte(`[{"id":"msg_in","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]`),
	)

	_, manager, _ := newOpenAISurfaceTestHarness(t)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses/:response_id", h.GetResponse)

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_stream_legacy?stream=true", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream; body=%s", got, resp.Body.String())
	}

	payloads, sawDone := parseResponseStreamPayloads(t, resp.Body.String())
	gotTypes := responseStreamPayloadTypes(payloads)
	wantTypes := []string{"response.created", "response.in_progress", "response.completed", "response.done"}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("stream event types = %v, want %v; body=%s", gotTypes, wantTypes, resp.Body.String())
	}
	if !sawDone {
		t.Fatalf("expected [DONE] marker in body=%s", resp.Body.String())
	}
	if got := payloads[0].Get("response.id").String(); got != "resp_test_stream_legacy" {
		t.Fatalf("created replay response.id = %q, want resp_test_stream_legacy; body=%s", got, resp.Body.String())
	}
	if got := payloads[len(payloads)-1].Get("response.output.0.content.0.text").String(); got != "legacy hello" {
		t.Fatalf("terminal replay text = %q, want legacy hello; body=%s", got, resp.Body.String())
	}
}

func TestResponses_RetrievesStoredResponseByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_default_store",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_default_store",
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
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)
	router.GET("/v1/responses/:response_id/input_items", h.GetResponseInputItems)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls after create = %d, want 1", executor.executeCalls)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_default_store", nil)
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}
	if executor.executeCalls != 1 {
		t.Fatalf("execute calls after get = %d, want 1", executor.executeCalls)
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "id").String(); got != "resp_test_default_store" {
		t.Fatalf("retrieved id = %q, want resp_test_default_store; body=%s", got, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "output.0.content.0.text").String(); got != "hello back" {
		t.Fatalf("retrieved text = %q, want hello back; body=%s", got, getResp.Body.String())
	}

	inputReq := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_default_store/input_items", nil)
	inputResp := httptest.NewRecorder()
	router.ServeHTTP(inputResp, inputReq)

	if inputResp.Code != http.StatusOK {
		t.Fatalf("input_items status = %d, want %d; body=%s", inputResp.Code, http.StatusOK, inputResp.Body.String())
	}
	if got := gjson.GetBytes(inputResp.Body.Bytes(), "object").String(); got != "list" {
		t.Fatalf("input_items object = %q, want list; body=%s", got, inputResp.Body.String())
	}
	if got := gjson.GetBytes(inputResp.Body.Bytes(), "data.0.type").String(); got != "message" {
		t.Fatalf("input_items data[0].type = %q, want message; body=%s", got, inputResp.Body.String())
	}
	if got := gjson.GetBytes(inputResp.Body.Bytes(), "data.0.role").String(); got != "user" {
		t.Fatalf("input_items data[0].role = %q, want user; body=%s", got, inputResp.Body.String())
	}
	if got := gjson.GetBytes(inputResp.Body.Bytes(), "data.0.content.0.type").String(); got != "input_text" {
		t.Fatalf("input_items content type = %q, want input_text; body=%s", got, inputResp.Body.String())
	}
	if got := gjson.GetBytes(inputResp.Body.Bytes(), "data.0.content.0.text").String(); got != "hello" {
		t.Fatalf("input_items text = %q, want hello; body=%s", got, inputResp.Body.String())
	}
}

func TestResponses_StoreFalseSkipsStoredRetrieval(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_store_false",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"store":false,
		"output":[
			{
				"id":"msg_test_store_false",
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
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello","store":false}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_store_false", nil)
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusNotFound {
		t.Fatalf("get status = %d, want %d; body=%s", getResp.Code, http.StatusNotFound, getResp.Body.String())
	}
	if !strings.Contains(getResp.Body.String(), "resp_test_store_false") {
		t.Fatalf("expected missing response id in body, got %s", getResp.Body.String())
	}
}

func TestResponses_InputItemsAppliesPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_paginate",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_paginate",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id/input_items", h.GetResponseInputItems)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"claude-opus-4-6",
		"input":[
			{"id":"msg_1","type":"message","role":"user","content":[{"type":"input_text","text":"one"}]},
			{"id":"msg_2","type":"message","role":"user","content":[{"type":"input_text","text":"two"}]},
			{"id":"msg_3","type":"message","role":"user","content":[{"type":"input_text","text":"three"}]}
		]
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_paginate/input_items?order=asc&after=msg_1&limit=1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "data.#").Int(); got != 1 {
		t.Fatalf("data length = %d, want 1; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "data.0.id").String(); got != "msg_2" {
		t.Fatalf("data[0].id = %q, want msg_2; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "has_more").Bool(); !got {
		t.Fatalf("has_more = %v, want true; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "first_id").String(); got != "msg_2" {
		t.Fatalf("first_id = %q, want msg_2; body=%s", got, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "last_id").String(); got != "msg_2" {
		t.Fatalf("last_id = %q, want msg_2; body=%s", got, resp.Body.String())
	}
}

func TestResponses_GetResponseRejectsUnsupportedReplayQueryOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_query_reject",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_query_reject",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	testCases := []struct {
		name        string
		query       string
		wantParam   string
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid stream",
			query:       "stream=maybe",
			wantParam:   "stream",
			wantCode:    "invalid_value",
			wantMessage: "stream",
		},
		{
			name:        "unsupported starting_after",
			query:       "starting_after=evt_123",
			wantParam:   "starting_after",
			wantCode:    "invalid_value",
			wantMessage: "starting_after",
		},
		{
			name:        "unsupported include_obfuscation",
			query:       "include_obfuscation=true",
			wantParam:   "include_obfuscation",
			wantCode:    "invalid_value",
			wantMessage: "include_obfuscation",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_query_reject?"+tc.query, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantMessage)
		})
	}
}

func TestResponses_GetResponseInputItemsRejectsUnsupportedQueryOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_input_items_query_reject",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_input_items_query_reject",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id/input_items", h.GetResponseInputItems)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"claude-opus-4-6",
		"input":[
			{"id":"msg_1","type":"message","role":"user","content":[{"type":"input_text","text":"one"}]},
			{"id":"msg_2","type":"message","role":"user","content":[{"type":"input_text","text":"two"}]}
		]
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	testCases := []struct {
		name        string
		query       string
		wantParam   string
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid order",
			query:       "order=sideways",
			wantParam:   "order",
			wantCode:    "invalid_value",
			wantMessage: "order",
		},
		{
			name:        "invalid limit",
			query:       "limit=101",
			wantParam:   "limit",
			wantCode:    "invalid_value",
			wantMessage: "limit",
		},
		{
			name:        "unsupported include",
			query:       "include[]=message.output_text.logprobs",
			wantParam:   "include",
			wantCode:    "unsupported_parameter",
			wantMessage: "include",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_input_items_query_reject/input_items?"+tc.query, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			assertSurfaceOpenAIErrorBody(t, resp.Body.String(), tc.wantParam, tc.wantCode, tc.wantMessage)
		})
	}
}

func TestResponses_GetResponseAllowsSupportedIncludeQueryOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_query_include",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_query_include",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_query_include?include[]=reasoning.encrypted_content&include[]=message.output_text.logprobs", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "id").String(); got != "resp_test_query_include" {
		t.Fatalf("retrieved id = %q, want resp_test_query_include; body=%s", got, resp.Body.String())
	}
}

func TestResponses_GetResponseRejectsUnsupportedIncludeQueryOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_query_include_reject",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_query_include_reject",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_query_include_reject?include[]=unsupported.include", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "unsupported.include") {
		t.Fatalf("expected unsupported include validation error, got %s", resp.Body.String())
	}
}

func TestResponses_GetResponseRejectsItemReferenceIncludeQueryOption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	registerSurfaceModel(t, auth.ID, auth.Provider, &registry.ModelInfo{
		ID:      "claude-opus-4-6",
		Object:  "model",
		OwnedBy: "antigravity",
		Type:    "antigravity",
	})
	executor.payload = []byte(`{
		"id":"resp_test_query_include_item_reference",
		"object":"response",
		"created_at":123,
		"model":"claude-opus-4-6",
		"output":[
			{
				"id":"msg_test_query_include_item_reference",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]
			}
		]
	}`)

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.POST("/v1/responses", h.Responses)
	router.GET("/v1/responses/:response_id", h.GetResponse)

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-opus-4-6","input":"hello"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusOK, createResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_query_include_item_reference?include[]=item_reference.content", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "item_reference.content") {
		t.Fatalf("expected unsupported include validation error, got %s", resp.Body.String())
	}
}

func waitForStoredOpenAIResponseStatus(t *testing.T, router *gin.Engine, responseID string, wantStatus string) []byte {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+responseID, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code == http.StatusOK && gjson.GetBytes(resp.Body.Bytes(), "status").String() == wantStatus {
			return append([]byte(nil), resp.Body.Bytes()...)
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("stored response %q did not reach %q", responseID, wantStatus)
	return nil
}

func parseResponseStreamPayloads(t *testing.T, body string) ([]gjson.Result, bool) {
	t.Helper()

	payloads := make([]gjson.Result, 0, 4)
	sawDone := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		parsed := gjson.Parse(payload)
		if !parsed.Exists() {
			t.Fatalf("stream payload is not valid json: %q; body=%s", payload, body)
		}
		payloads = append(payloads, parsed)
	}
	if len(payloads) == 0 {
		t.Fatalf("expected replay payloads in body=%s", body)
	}
	return payloads, sawDone
}

func responseStreamPayloadTypes(payloads []gjson.Result) []string {
	types := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		types = append(types, payload.Get("type").String())
	}
	return types
}
