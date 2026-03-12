package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestForwardResponsesStreamTerminalErrorUsesResponsesErrorChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIResponsesAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)
	errs <- &interfaces.ErrorMessage{StatusCode: http.StatusInternalServerError, Error: errors.New("unexpected EOF")}
	close(errs)

	h.forwardResponsesStream(c, flusher, func(error) {}, nil, nil, data, errs)
	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Fatalf("expected responses error chunk, got: %q", body)
	}
	if strings.Contains(body, `"error":{`) {
		t.Fatalf("expected streaming error chunk (top-level type), got HTTP error body: %q", body)
	}
}

func TestForwardResponsesStreamTerminalError_UsesPermissionErrorForAuggieSuspension(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIResponsesAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)
	errs <- &interfaces.ErrorMessage{StatusCode: http.StatusForbidden, Error: errors.New("account suspended")}
	close(errs)

	h.forwardResponsesStream(c, flusher, func(error) {}, nil, nil, data, errs)
	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected SSE error event, got: %q", body)
	}
	chunkText := strings.TrimSpace(strings.TrimPrefix(strings.Split(body, "data: ")[1], ""))
	chunkText = strings.TrimSpace(strings.TrimSuffix(chunkText, "\n"))
	var payload map[string]any
	if err := json.Unmarshal([]byte(chunkText), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v; body=%q", err, body)
	}
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want %q", payload["type"], "error")
	}
	if payload["code"] != "insufficient_quota" {
		t.Fatalf("code = %v, want %q", payload["code"], "insufficient_quota")
	}
	if payload["message"] != "account suspended" {
		t.Fatalf("message = %v, want %q", payload["message"], "account suspended")
	}
	if got, exists := payload["param"]; !exists || got != nil {
		t.Fatalf("param = %v (exists=%v), want null", got, exists)
	}
}

func TestForwardResponsesStreamTerminalError_UsesNextSequenceNumberAfterPriorChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIResponsesAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)
	go func() {
		data <- []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":5,\"delta\":\"hi\"}\n")
		errs <- &interfaces.ErrorMessage{StatusCode: http.StatusInternalServerError, Error: errors.New("unexpected EOF")}
		close(errs)
		close(data)
	}()

	h.forwardResponsesStream(c, flusher, func(error) {}, nil, nil, data, errs)
	payloads := websocketJSONPayloadsFromChunk(recorder.Body.Bytes())
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d, want 2; body=%q", len(payloads), recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(payloads[1], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v; body=%q", err, recorder.Body.String())
	}
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want %q", payload["type"], "error")
	}
	if payload["sequence_number"] != float64(6) {
		t.Fatalf("sequence_number = %v, want %v; body=%q", payload["sequence_number"], 6, recorder.Body.String())
	}
}
