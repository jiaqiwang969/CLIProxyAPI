// Package gemini provides HTTP handlers for Gemini API endpoints.
// This package implements handlers for managing Gemini model operations including
// model listing, content generation, streaming content generation, and token counting.
// It serves as a proxy layer between clients and the Gemini backend service,
// handling request translation, client management, and response processing.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// GeminiAPIHandler contains the handlers for Gemini API endpoints.
// It holds a pool of clients to interact with the backend service.
type GeminiAPIHandler struct {
	*handlers.BaseAPIHandler
}

const (
	geminiThoughtSignatureSkipSentinel = "skip_thought_signature_validator"
	geminiThoughtSignatureDocsURL      = "https://ai.google.dev/gemini-api/docs/thought-signatures"
)

// NewGeminiAPIHandler creates a new Gemini API handlers instance.
// It takes an BaseAPIHandler instance as input and returns a GeminiAPIHandler.
func NewGeminiAPIHandler(apiHandlers *handlers.BaseAPIHandler) *GeminiAPIHandler {
	return &GeminiAPIHandler{
		BaseAPIHandler: apiHandlers,
	}
}

// HandlerType returns the identifier for this handler implementation.
func (h *GeminiAPIHandler) HandlerType() string {
	return Gemini
}

// Models returns the Gemini-compatible model metadata supported by this handler.
func (h *GeminiAPIHandler) Models() []map[string]any {
	publicModels := registry.GetPublicGeminiModels()
	normalizedModels := make([]map[string]any, 0, len(publicModels))
	for _, model := range publicModels {
		if normalizedModel := normalizeGeminiPublicModel(model); normalizedModel != nil {
			normalizedModels = append(normalizedModels, normalizedModel)
		}
	}
	return normalizedModels
}

// GeminiModels handles the Gemini models listing endpoint.
// It returns a JSON response containing available Gemini models and their specifications.
func (h *GeminiAPIHandler) GeminiModels(c *gin.Context) {
	allModels := h.Models()
	filteredModels := make([]map[string]any, 0, len(allModels))
	for _, model := range allModels {
		name := strings.TrimSpace(fmt.Sprint(model["name"]))
		modelID := strings.TrimPrefix(name, "models/")
		if modelID == "" {
			continue
		}
		if !handlers.ModelVisibleForRequest(c, modelID) {
			continue
		}
		filteredModels = append(filteredModels, model)
	}
	c.JSON(http.StatusOK, gin.H{
		"models": filteredModels,
	})
}

// GeminiGetHandler handles GET requests for specific Gemini model information.
// It returns detailed information about a specific Gemini model based on the action parameter.
func (h *GeminiAPIHandler) GeminiGetHandler(c *gin.Context) {
	var request struct {
		Action string `uri:"action" binding:"required"`
	}
	if err := c.ShouldBindUri(&request); err != nil {
		h.writeGeminiError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err), nil)
		return
	}
	action := strings.TrimPrefix(request.Action, "/")

	targetModel, ok := findPublicGeminiModel(action)
	if ok {
		if !handlers.ModelVisibleForRequest(c, targetModel.ID) {
			h.writeGeminiError(c, http.StatusNotFound, geminiGetModelNotFoundMessage(action), nil)
			return
		}
		c.JSON(http.StatusOK, normalizeGeminiPublicModel(targetModel))
		return
	}

	h.writeGeminiError(c, http.StatusNotFound, geminiGetModelNotFoundMessage(action), nil)
}

// GeminiHandler handles POST requests for Gemini API operations.
// It routes requests to appropriate handlers based on the action parameter (model:method format).
func (h *GeminiAPIHandler) GeminiHandler(c *gin.Context) {
	var request struct {
		Action string `uri:"action" binding:"required"`
	}
	if err := c.ShouldBindUri(&request); err != nil {
		h.writeGeminiError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err), nil)
		return
	}
	action := strings.Split(strings.TrimPrefix(request.Action, "/"), ":")
	if len(action) != 2 {
		h.writeGeminiError(c, http.StatusNotFound, fmt.Sprintf("%s not found.", c.Request.URL.Path), nil)
		return
	}

	modelName, ok := normalizePublicGeminiModelID(action[0])
	if !ok {
		h.writeGeminiError(c, http.StatusNotFound, geminiModelMethodNotFoundMessage(action[0], action[1]), nil)
		return
	}

	method := action[1]
	rawJSON, _ := c.GetRawData()
	if (method == "generateContent" || method == "streamGenerateContent") && len(rawJSON) > 0 {
		if message, ok := validateGeminiToolCallThoughtSignatures(rawJSON); ok {
			h.writeGeminiErrorWithoutDefaultDetails(c, http.StatusBadRequest, message, nil)
			return
		}
	}

	switch method {
	case "generateContent":
		h.handleGenerateContent(c, modelName, rawJSON)
	case "streamGenerateContent":
		h.handleStreamGenerateContent(c, modelName, rawJSON)
	case "countTokens":
		h.handleCountTokens(c, modelName, rawJSON)
	}
}

func findPublicGeminiModel(action string) (*registry.ModelInfo, bool) {
	modelID, ok := normalizePublicGeminiModelID(action)
	if !ok {
		return nil, false
	}

	for _, model := range registry.GetPublicGeminiModels() {
		if model == nil || strings.TrimSpace(model.ID) == "" {
			continue
		}
		if model.ID == modelID {
			return model, true
		}
	}
	return nil, false
}

func normalizePublicGeminiModelID(name string) (string, bool) {
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	name = strings.TrimPrefix(name, "models/")
	if name == "" {
		return "", false
	}

	for _, model := range registry.GetPublicGeminiModels() {
		if model == nil || strings.TrimSpace(model.ID) == "" {
			continue
		}
		if model.ID == name {
			return model.ID, true
		}
	}
	return "", false
}

func normalizeGeminiPublicModel(model *registry.ModelInfo) map[string]any {
	if model == nil {
		return nil
	}

	name := strings.TrimSpace(model.Name)
	if name == "" {
		name = "models/" + strings.TrimSpace(model.ID)
	}
	if name == "models/" {
		return nil
	}

	displayName := strings.TrimSpace(model.DisplayName)
	description := strings.TrimSpace(model.Description)
	normalizedModel := map[string]any{
		"name": name,
	}
	if displayName != "" {
		normalizedModel["displayName"] = displayName
	}
	if len(model.SupportedGenerationMethods) > 0 {
		normalizedModel["supportedGenerationMethods"] = model.SupportedGenerationMethods
	}
	if model.Version != "" {
		normalizedModel["version"] = model.Version
	}
	if description != "" {
		normalizedModel["description"] = description
	}
	if model.InputTokenLimit > 0 {
		normalizedModel["inputTokenLimit"] = model.InputTokenLimit
	}
	if model.OutputTokenLimit > 0 {
		normalizedModel["outputTokenLimit"] = model.OutputTokenLimit
	}
	if len(model.SupportedInputModalities) > 0 {
		normalizedModel["supportedInputModalities"] = model.SupportedInputModalities
	}
	if len(model.SupportedOutputModalities) > 0 {
		normalizedModel["supportedOutputModalities"] = model.SupportedOutputModalities
	}
	if model.GeminiTemperature != nil {
		normalizedModel["temperature"] = *model.GeminiTemperature
	}
	if model.GeminiTopP != nil {
		normalizedModel["topP"] = *model.GeminiTopP
	}
	if model.GeminiTopK != nil {
		normalizedModel["topK"] = *model.GeminiTopK
	}
	if model.GeminiMaxTemperature != nil {
		normalizedModel["maxTemperature"] = *model.GeminiMaxTemperature
	}
	if model.GeminiPublicThinking != nil {
		normalizedModel["thinking"] = *model.GeminiPublicThinking
	}
	return normalizedModel
}

// handleStreamGenerateContent handles streaming content generation requests for Gemini models.
// This function establishes a Server-Sent Events connection and streams the generated content
// back to the client in real-time. It supports both SSE format and direct streaming based
// on the 'alt' query parameter.
//
// Parameters:
//   - c: The Gin context for the request
//   - modelName: The name of the Gemini model to use for content generation
//   - rawJSON: The raw JSON request body containing generation parameters
func (h *GeminiAPIHandler) handleStreamGenerateContent(c *gin.Context, modelName string, rawJSON []byte) {
	alt := h.GetAlt(c)

	// Get the http.Flusher interface to manually flush the response.
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		h.writeGeminiError(c, http.StatusInternalServerError, "Streaming not supported", nil)
		return
	}

	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	dataChan, upstreamHeaders, errChan := h.ExecuteStreamWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, alt)

	setSSEHeaders := func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
	}

	// Peek at the first chunk
	for {
		select {
		case <-c.Request.Context().Done():
			cliCancel(c.Request.Context().Err())
			return
		case errMsg, ok := <-errChan:
			if !ok {
				// Err channel closed cleanly; wait for data channel.
				errChan = nil
				continue
			}
			// Upstream failed immediately. Return proper error status and JSON.
			h.writeGeminiErrorMessage(c, errMsg)
			if errMsg != nil {
				cliCancel(errMsg.Error)
			} else {
				cliCancel(nil)
			}
			return
		case chunk, ok := <-dataChan:
			if !ok {
				// Closed without data
				if alt == "" {
					setSSEHeaders()
				}
				handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
				flusher.Flush()
				cliCancel(nil)
				return
			}

			// Success! Set headers.
			if alt == "" {
				setSSEHeaders()
			}
			handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)

			// Write first chunk
			chunk = rewriteGeminiResponseModelVersion(chunk, modelName)

			if alt == "" {
				_, _ = c.Writer.Write([]byte("data: "))
				_, _ = c.Writer.Write(chunk)
				_, _ = c.Writer.Write([]byte("\n\n"))
			} else {
				_, _ = c.Writer.Write(chunk)
			}
			flusher.Flush()

			// Continue
			h.forwardGeminiStream(c, flusher, alt, modelName, func(err error) { cliCancel(err) }, dataChan, errChan)
			return
		}
	}
}

// handleCountTokens handles token counting requests for Gemini models.
// This function counts the number of tokens in the provided content without
// generating a response. It's useful for quota management and content validation.
//
// Parameters:
//   - c: The Gin context for the request
//   - modelName: The name of the Gemini model to use for token counting
//   - rawJSON: The raw JSON request body containing the content to count
func (h *GeminiAPIHandler) handleCountTokens(c *gin.Context, modelName string, rawJSON []byte) {
	c.Header("Content-Type", "application/json")
	alt := h.GetAlt(c)
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	resp, upstreamHeaders, errMsg := h.ExecuteCountWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, alt)
	if errMsg != nil {
		h.writeGeminiErrorMessage(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

// handleGenerateContent handles non-streaming content generation requests for Gemini models.
// This function processes the request synchronously and returns the complete generated
// response in a single API call. It supports various generation parameters and
// response formats.
//
// Parameters:
//   - c: The Gin context for the request
//   - modelName: The name of the Gemini model to use for content generation
//   - rawJSON: The raw JSON request body containing generation parameters and content
func (h *GeminiAPIHandler) handleGenerateContent(c *gin.Context, modelName string, rawJSON []byte) {
	c.Header("Content-Type", "application/json")
	alt := h.GetAlt(c)
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	stopKeepAlive := h.StartNonStreamingKeepAlive(c, cliCtx)
	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, alt)
	stopKeepAlive()
	if errMsg != nil {
		h.writeGeminiErrorMessage(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(rewriteGeminiResponseModelVersion(resp, modelName))
	cliCancel()
}

func (h *GeminiAPIHandler) forwardGeminiStream(c *gin.Context, flusher http.Flusher, alt, modelName string, cancel func(error), data <-chan []byte, errs <-chan *interfaces.ErrorMessage) {
	var keepAliveInterval *time.Duration
	if alt != "" {
		keepAliveInterval = new(time.Duration(0))
	}

	h.ForwardStream(c, flusher, cancel, data, errs, handlers.StreamForwardOptions{
		KeepAliveInterval: keepAliveInterval,
		WriteChunk: func(chunk []byte) {
			chunk = rewriteGeminiResponseModelVersion(chunk, modelName)
			if alt == "" {
				_, _ = c.Writer.Write([]byte("data: "))
				_, _ = c.Writer.Write(chunk)
				_, _ = c.Writer.Write([]byte("\n\n"))
			} else {
				_, _ = c.Writer.Write(chunk)
			}
		},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) {
			if errMsg == nil {
				return
			}
			status := http.StatusInternalServerError
			if errMsg.StatusCode > 0 {
				status = errMsg.StatusCode
			}
			errText := http.StatusText(status)
			if errMsg.Error != nil && errMsg.Error.Error() != "" {
				errText = errMsg.Error.Error()
			}
			body := buildGeminiErrorResponseBody(status, errText, nil)
			if alt == "" {
				_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", string(body))
			} else {
				_, _ = c.Writer.Write(body)
			}
		},
	})
}

func (h *GeminiAPIHandler) writeGeminiError(c *gin.Context, status int, message string, details []any) {
	body := buildGeminiErrorResponseBody(status, message, details)
	h.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: status,
		Error:      errors.New(string(body)),
	})
}

func (h *GeminiAPIHandler) writeGeminiErrorWithoutDefaultDetails(c *gin.Context, status int, message string, details []any) {
	body := buildGeminiErrorResponseBodyExact(status, message, details)
	h.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: status,
		Error:      errors.New(string(body)),
	})
}

func (h *GeminiAPIHandler) writeGeminiErrorMessage(c *gin.Context, msg *interfaces.ErrorMessage) {
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

	body := []byte(errText)
	if json.Valid(body) {
		body = normalizeGeminiStructuredErrorBody(body)
	} else {
		body = buildGeminiErrorResponseBody(status, errText, nil)
	}

	h.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: status,
		Error:      errors.New(string(body)),
		Addon:      msg.Addon,
	})
}

func buildGeminiErrorResponseBody(status int, message string, details []any) []byte {
	return buildGeminiErrorResponseBodyInternal(status, message, details, true)
}

func buildGeminiErrorResponseBodyExact(status int, message string, details []any) []byte {
	return buildGeminiErrorResponseBodyInternal(status, message, details, false)
}

func buildGeminiErrorResponseBodyInternal(status int, message string, details []any, addDefaultBadRequestDetails bool) []byte {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(status)
	}
	if addDefaultBadRequestDetails && status == http.StatusBadRequest && len(details) == 0 {
		details = geminiBadRequestDetails(message)
	}

	payload := map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": message,
			"status":  geminiErrorStatus(status),
		},
	}
	if len(details) > 0 {
		payload["error"].(map[string]any)["details"] = details
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return []byte(fmt.Sprintf(`{"error":{"code":%d,"message":%q,"status":%q}}`, status, message, geminiErrorStatus(status)))
	}
	return body
}

func geminiBadRequestDetails(message string) []any {
	return []any{
		map[string]any{
			"@type": "type.googleapis.com/google.rpc.BadRequest",
			"fieldViolations": []map[string]any{
				{
					"description": message,
				},
			},
		},
	}
}

func geminiErrorStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusRequestTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusInternalServerError, http.StatusBadGateway:
		return "INTERNAL"
	default:
		if status >= http.StatusInternalServerError {
			return "INTERNAL"
		}
		return "UNKNOWN"
	}
}

func geminiGetModelNotFoundMessage(modelName string) string {
	return fmt.Sprintf("Model is not found: models/%s for api version v1beta", strings.TrimPrefix(strings.TrimSpace(modelName), "models/"))
}

func geminiModelMethodNotFoundMessage(modelName, method string) string {
	modelName = strings.TrimPrefix(strings.TrimSpace(modelName), "models/")
	method = strings.TrimSpace(method)
	return fmt.Sprintf("models/%s is not found for API version v1beta, or is not supported for %s. Call ListModels to see the list of available models and their supported methods.", modelName, method)
}

func validateGeminiToolCallThoughtSignatures(rawJSON []byte) (string, bool) {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return "", false
	}

	contents := gjson.GetBytes(rawJSON, "contents")
	if !contents.Exists() || !contents.IsArray() {
		return "", false
	}

	for contentIndex, content := range contents.Array() {
		parts := content.Get("parts")
		if !parts.Exists() || !parts.IsArray() {
			continue
		}

		for _, part := range parts.Array() {
			functionCall := part.Get("functionCall")
			if !functionCall.Exists() {
				continue
			}

			thoughtSignature := strings.TrimSpace(part.Get("thoughtSignature").String())
			if thoughtSignature == "" {
				thoughtSignature = strings.TrimSpace(part.Get("thought_signature").String())
			}
			if thoughtSignature != "" && thoughtSignature != geminiThoughtSignatureSkipSentinel {
				continue
			}

			return geminiMissingThoughtSignatureMessage(functionCall.Get("name").String(), contentIndex+1), true
		}
	}

	return "", false
}

func geminiMissingThoughtSignatureMessage(functionName string, position int) string {
	if position <= 0 {
		position = 1
	}
	return fmt.Sprintf(
		"Function call is missing a thought_signature in functionCall parts. This is required for tools to work correctly, and missing thought_signature may lead to degraded model performance. Additional data, function call `%s` , position %d. Please refer to %s for more details.",
		geminiQualifiedFunctionCallName(functionName),
		position,
		geminiThoughtSignatureDocsURL,
	)
}

func geminiQualifiedFunctionCallName(functionName string) string {
	functionName = strings.TrimSpace(functionName)
	if strings.Contains(functionName, ":") {
		return functionName
	}
	return "default_api:" + functionName
}

func rewriteGeminiResponseModelVersion(chunk []byte, requestedModel string) []byte {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(chunk) == 0 || !gjson.ValidBytes(chunk) {
		return chunk
	}

	rewritten := chunk
	if gjson.GetBytes(rewritten, "modelVersion").Exists() {
		if updated, err := sjson.SetBytes(rewritten, "modelVersion", requestedModel); err == nil {
			rewritten = updated
		}
	}
	if gjson.GetBytes(rewritten, "response.modelVersion").Exists() {
		if updated, err := sjson.SetBytes(rewritten, "response.modelVersion", requestedModel); err == nil {
			rewritten = updated
		}
	}
	return rewritten
}

func normalizeGeminiStructuredErrorBody(body []byte) []byte {
	if len(body) == 0 || !json.Valid(body) {
		return body
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	errPayload, _ := payload["error"].(map[string]any)
	if len(errPayload) == 0 {
		return body
	}

	if message, ok := errPayload["message"].(string); ok {
		errPayload["message"] = normalizeGeminiInvalidJSONMessage(message)
	}

	if details, ok := errPayload["details"].([]any); ok {
		for i, rawDetail := range details {
			detail, ok := rawDetail.(map[string]any)
			if !ok {
				continue
			}
			fieldViolations, ok := detail["fieldViolations"].([]any)
			if !ok {
				continue
			}
			for j, rawViolation := range fieldViolations {
				violation, ok := rawViolation.(map[string]any)
				if !ok {
					continue
				}
				if description, ok := violation["description"].(string); ok {
					violation["description"] = normalizeGeminiInvalidJSONMessage(description)
				}
				if field, ok := violation["field"].(string); ok && strings.EqualFold(strings.TrimSpace(field), "request") {
					delete(violation, "field")
				}
				fieldViolations[j] = violation
			}
			detail["fieldViolations"] = fieldViolations
			details[i] = detail
		}
		errPayload["details"] = details
	}

	payload["error"] = errPayload
	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

func normalizeGeminiInvalidJSONMessage(message string) string {
	if strings.TrimSpace(message) == "" {
		return message
	}
	return strings.Replace(message, " at 'request':", ":", 1)
}
