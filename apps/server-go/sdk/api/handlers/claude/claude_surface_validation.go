package claude

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
)

func validateClaudeNativeModel(modelID string) *interfaces.ErrorMessage {
	caps := handlers.ResolvePublicModelSurface(modelID)
	if caps.Available && caps.SupportsClaudeNative {
		return nil
	}

	return &interfaces.ErrorMessage{
		StatusCode: http.StatusBadRequest,
		Error:      fmt.Errorf("model %q is not available on this endpoint", modelID),
	}
}

func (h *ClaudeCodeAPIHandler) writeClaudeErrorResponse(c *gin.Context, msg *interfaces.ErrorMessage) {
	status := http.StatusInternalServerError
	if msg != nil && msg.StatusCode > 0 {
		status = msg.StatusCode
	}

	errText := http.StatusText(status)
	if msg != nil && msg.Error != nil {
		if text := strings.TrimSpace(msg.Error.Error()); text != "" {
			errText = text
		}
	}

	c.JSON(status, h.toClaudeError(status, errText))
}

func (h *ClaudeCodeAPIHandler) marshalClaudeError(msg *interfaces.ErrorMessage) []byte {
	status := http.StatusInternalServerError
	if msg != nil && msg.StatusCode > 0 {
		status = msg.StatusCode
	}
	errText := http.StatusText(status)
	if msg != nil && msg.Error != nil {
		if text := strings.TrimSpace(msg.Error.Error()); text != "" {
			errText = text
		}
	}

	body, err := json.Marshal(h.toClaudeError(status, errText))
	if err != nil {
		return []byte(`{"type":"error","error":{"type":"api_error","message":"internal server error"}}`)
	}
	return body
}
