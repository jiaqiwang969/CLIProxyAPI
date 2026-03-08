package openai

import (
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
)

func validateOpenAISurfaceModel(modelID string) *interfaces.ErrorMessage {
	caps := handlers.ResolvePublicModelSurface(modelID)
	if caps.Available && caps.SupportsOpenAI {
		return nil
	}

	return &interfaces.ErrorMessage{
		StatusCode: http.StatusBadRequest,
		Error:      fmt.Errorf("model %q is not available on this endpoint", modelID),
	}
}
