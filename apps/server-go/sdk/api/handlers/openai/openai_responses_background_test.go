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

func TestResponses_BackgroundCreateReturnsQueuedResponseAndCompletesStoredResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.payload = []byte(`{
		"id":"resp_upstream_background_complete",
		"object":"response",
		"created_at":456,
		"status":"completed",
		"background":true,
		"output":[
			{
				"id":"msg_background_complete",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hello from background","annotations":[],"logprobs":[]}]
			}
		]
	}`)
	executor.executeStarted = make(chan struct{}, 1)
	executor.executeRelease = make(chan struct{})

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
	if !strings.HasPrefix(responseID, "resp_") {
		t.Fatalf("create response id = %q, want resp_*; body=%s", responseID, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "object").String(); got != "response" {
		t.Fatalf("create object = %q, want response; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "status").String(); got != "queued" {
		t.Fatalf("create status = %q, want queued; body=%s", got, createResp.Body.String())
	}
	if !gjson.GetBytes(createResp.Body.Bytes(), "background").Bool() {
		t.Fatalf("create background = false, want true; body=%s", createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "output.#").Int(); got != 0 {
		t.Fatalf("create output count = %d, want 0; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "usage"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("create usage = %s, want null; body=%s", got.Raw, createResp.Body.String())
	}

	select {
	case <-executor.executeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background execution did not start")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/responses/"+responseID, nil)
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "id").String(); got != responseID {
		t.Fatalf("retrieved id = %q, want %q; body=%s", got, responseID, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "status").String(); got != "queued" && got != "in_progress" {
		t.Fatalf("retrieved status = %q, want queued or in_progress; body=%s", got, getResp.Body.String())
	}

	close(executor.executeRelease)

	var completedBody []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		getReq = httptest.NewRequest(http.MethodGet, "/v1/responses/"+responseID, nil)
		getResp = httptest.NewRecorder()
		router.ServeHTTP(getResp, getReq)
		if getResp.Code == http.StatusOK && gjson.GetBytes(getResp.Body.Bytes(), "status").String() == "completed" {
			completedBody = append([]byte(nil), getResp.Body.Bytes()...)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(completedBody) == 0 {
		t.Fatalf("background response %q did not reach completed state", responseID)
	}
	if got := gjson.GetBytes(completedBody, "id").String(); got != responseID {
		t.Fatalf("completed id = %q, want %q; body=%s", got, responseID, string(completedBody))
	}
	if got := gjson.GetBytes(completedBody, "output.0.content.0.text").String(); got != "hello from background" {
		t.Fatalf("completed output text = %q, want hello from background; body=%s", got, string(completedBody))
	}
}

func TestResponses_BackgroundQueuedResponseEchoesPromptFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.payload = []byte(`{
		"id":"resp_upstream_background_prompt",
		"object":"response",
		"created_at":456,
		"status":"completed",
		"background":true,
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_retention":"24h",
		"output":[
			{
				"id":"msg_background_prompt",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hello from background","annotations":[],"logprobs":[]}]
			}
		]
	}`)
	executor.executeStarted = make(chan struct{}, 1)
	executor.executeRelease = make(chan struct{})

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

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"claude-opus-4-6",
		"input":"hello",
		"background":true,
		"max_tool_calls":3,
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_key":"cache-key-1",
		"prompt_cache_retention":"24h",
		"top_logprobs":2
	}`))
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
	if got := gjson.GetBytes(createResp.Body.Bytes(), "prompt.id").String(); got != "pmpt_test" {
		t.Fatalf("queued prompt.id = %q, want pmpt_test; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "prompt.version").String(); got != "3" {
		t.Fatalf("queued prompt.version = %q, want 3; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "prompt.variables.city").String(); got != "Boston" {
		t.Fatalf("queued prompt.variables.city = %q, want Boston; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("queued prompt_cache_retention = %q, want 24h; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "prompt_cache_key").String(); got != "cache-key-1" {
		t.Fatalf("queued prompt_cache_key = %q, want cache-key-1; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "max_tool_calls").Int(); got != 3 {
		t.Fatalf("queued max_tool_calls = %d, want 3; body=%s", got, createResp.Body.String())
	}
	if got := gjson.GetBytes(createResp.Body.Bytes(), "top_logprobs").Int(); got != 2 {
		t.Fatalf("queued top_logprobs = %d, want 2; body=%s", got, createResp.Body.String())
	}

	select {
	case <-executor.executeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background execution did not start")
	}
	close(executor.executeRelease)
}

func TestResponses_BackgroundCancelCancelsActiveResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.executeStarted = make(chan struct{}, 1)
	executor.executeFinished = make(chan struct{}, 1)
	executor.waitForCancel = true

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
	router.POST("/v1/responses/:response_id/cancel", h.CancelResponse)
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

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/responses/"+responseID+"/cancel", nil)
	cancelResp := httptest.NewRecorder()
	router.ServeHTTP(cancelResp, cancelReq)

	if cancelResp.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d; body=%s", cancelResp.Code, http.StatusOK, cancelResp.Body.String())
	}
	if got := gjson.GetBytes(cancelResp.Body.Bytes(), "status").String(); got != "cancelled" {
		t.Fatalf("cancel status = %q, want cancelled; body=%s", got, cancelResp.Body.String())
	}

	select {
	case <-executor.executeFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("background execution did not stop after cancel")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/responses/"+responseID, nil)
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}
	if got := gjson.GetBytes(getResp.Body.Bytes(), "status").String(); got != "cancelled" {
		t.Fatalf("retrieved status = %q, want cancelled; body=%s", got, getResp.Body.String())
	}
}

func TestResponses_BackgroundCreateStoresReplayEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.payload = []byte(`{
		"id":"resp_upstream_background_replay_store",
		"object":"response",
		"created_at":456,
		"status":"completed",
		"background":true,
		"output":[
			{
				"id":"msg_background_replay_store",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hello from background replay store","annotations":[],"logprobs":[]}]
			}
		]
	}`)
	executor.executeStarted = make(chan struct{}, 1)
	executor.executeRelease = make(chan struct{})

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

	stored, ok := defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		t.Fatalf("expected stored response %q", responseID)
	}

	gotTypes := responseReplayEventTypes(stored.ReplayEvents)
	wantTypes := []string{"response.created", "response.queued", "response.in_progress", "response.completed", "response.done"}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("replay event types = %v, want %v", gotTypes, wantTypes)
	}
}

func TestResponses_BackgroundCancelStoresCancelledReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetStoredOpenAIResponsesForTest()
	t.Cleanup(resetStoredOpenAIResponsesForTest)

	executor, manager, auth := newOpenAISurfaceTestHarness(t)
	executor.executeStarted = make(chan struct{}, 1)
	executor.executeFinished = make(chan struct{}, 1)
	executor.waitForCancel = true

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
	router.POST("/v1/responses/:response_id/cancel", h.CancelResponse)

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

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/responses/"+responseID+"/cancel", nil)
	cancelResp := httptest.NewRecorder()
	router.ServeHTTP(cancelResp, cancelReq)

	if cancelResp.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d; body=%s", cancelResp.Code, http.StatusOK, cancelResp.Body.String())
	}

	select {
	case <-executor.executeFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("background execution did not stop after cancel")
	}

	stored, ok := defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		t.Fatalf("expected stored response %q", responseID)
	}

	gotTypes := responseReplayEventTypes(stored.ReplayEvents)
	wantTypes := []string{"response.created", "response.queued", "response.in_progress", "response.cancelled", "response.done"}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("replay event types = %v, want %v", gotTypes, wantTypes)
	}
}
