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

func TestHandleStreamResult_UsesPermissionErrorBodyForAuggieSuspension(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIAPIHandler(base)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("expected gin writer to implement http.Flusher")
	}

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)
	errs <- &interfaces.ErrorMessage{StatusCode: http.StatusForbidden, Error: errors.New("account suspended")}
	close(errs)

	h.handleStreamResult(c, flusher, func(error) {}, data, errs)
	body := recorder.Body.String()
	if !strings.Contains(body, `data: {"error":`) {
		t.Fatalf("expected chat completions SSE error body, got: %q", body)
	}

	chunkText := strings.TrimSpace(strings.TrimPrefix(strings.Split(body, "data: ")[1], ""))
	chunkText = strings.TrimSpace(strings.TrimSuffix(chunkText, "\n"))
	var payload map[string]any
	if err := json.Unmarshal([]byte(chunkText), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v; body=%q", err, body)
	}

	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error payload missing: %v", payload)
	}
	if errorPayload["type"] != "permission_error" {
		t.Fatalf("error.type = %v, want %q", errorPayload["type"], "permission_error")
	}
	if errorPayload["code"] != "insufficient_quota" {
		t.Fatalf("error.code = %v, want %q", errorPayload["code"], "insufficient_quota")
	}
	if errorPayload["message"] != "account suspended" {
		t.Fatalf("error.message = %v, want %q", errorPayload["message"], "account suspended")
	}
	if got, exists := errorPayload["param"]; !exists || got != nil {
		t.Fatalf("error.param = %v (exists=%v), want null", got, exists)
	}
}
