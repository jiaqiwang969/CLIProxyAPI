// Package openai provides HTTP handlers for OpenAIResponses API endpoints.
// This package implements the OpenAIResponses-compatible API interface, including model listing
// and chat completion functionality. It supports both streaming and non-streaming responses,
// and manages a pool of clients to interact with backend services.
// The handlers translate OpenAIResponses API requests to the appropriate backend format and
// convert responses back to OpenAIResponses-compatible format.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAIResponsesAPIHandler contains the handlers for OpenAIResponses API endpoints.
// It holds a pool of clients to interact with the backend service.
type OpenAIResponsesAPIHandler struct {
	*handlers.BaseAPIHandler
}

// NewOpenAIResponsesAPIHandler creates a new OpenAIResponses API handlers instance.
// It takes an BaseAPIHandler instance as input and returns an OpenAIResponsesAPIHandler.
//
// Parameters:
//   - apiHandlers: The base API handlers instance
//
// Returns:
//   - *OpenAIResponsesAPIHandler: A new OpenAIResponses API handlers instance
func NewOpenAIResponsesAPIHandler(apiHandlers *handlers.BaseAPIHandler) *OpenAIResponsesAPIHandler {
	return &OpenAIResponsesAPIHandler{
		BaseAPIHandler: apiHandlers,
	}
}

// HandlerType returns the identifier for this handler implementation.
func (h *OpenAIResponsesAPIHandler) HandlerType() string {
	return OpenaiResponse
}

// Models returns the OpenAIResponses-compatible model metadata supported by this handler.
func (h *OpenAIResponsesAPIHandler) Models() []map[string]any {
	// Get dynamic models from the global registry
	modelRegistry := registry.GetGlobalRegistry()
	return modelRegistry.GetAvailableModels("openai")
}

// OpenAIResponsesModels handles the /v1/models endpoint.
// It returns a list of available AI models with their capabilities
// and specifications in OpenAIResponses-compatible format.
func (h *OpenAIResponsesAPIHandler) OpenAIResponsesModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   h.Models(),
	})
}

// Responses handles the /v1/responses endpoint.
// It determines whether the request is for a streaming or non-streaming response
// and calls the appropriate handler based on the model provider.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
func (h *OpenAIResponsesAPIHandler) Responses(c *gin.Context) {
	requestCtx := ensureGinRequestContext(c)

	rawJSON, err := c.GetRawData()
	// If data retrieval fails, return a 400 Bad Request error.
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	if errMsg := validateOpenAISurfaceModel(gjson.GetBytes(rawJSON, "model").String()); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	providers, normalizedModel, detailsErr := h.GetRequestDetailsForContext(requestCtx, gjson.GetBytes(rawJSON, "model").String())
	if detailsErr != nil {
		h.WriteErrorResponse(c, detailsErr)
		return
	}
	if errMsg := validateOpenAIStoreSupport(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesConversationSupport(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIIncludeSupport(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesUnsupportedExecutionControls(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesUnsupportedRequestFeatures(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesParallelToolCalls(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMaxToolCalls(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMaxOutputTokens(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTopLogprobs(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTemperature(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTopP(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesServiceTier(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesInput(rawJSON); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesInputItemTypeSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesToolOutputItemSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMessageContentTypeSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesToolDefinitionSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesProviderRequestFeatureSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	var errMsg *interfaces.ErrorMessage
	conversationCtx := (*openAIConversationExecutionContext)(nil)
	if rawJSON, conversationCtx, errMsg = prepareOpenAIResponsesConversationRequest(rawJSON); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	// Check if the client requested a streaming response.
	backgroundRequested := gjson.GetBytes(rawJSON, "background").Bool()
	streamResult := gjson.GetBytes(rawJSON, "stream")
	if backgroundRequested {
		h.handleBackgroundResponse(c, rawJSON, conversationCtx, providers, normalizedModel)
	} else if streamResult.Type == gjson.True {
		h.handleStreamingResponse(c, rawJSON, conversationCtx)
	} else {
		h.handleNonStreamingResponse(c, rawJSON, conversationCtx)
	}

}

func (h *OpenAIResponsesAPIHandler) GetResponse(c *gin.Context) {
	if errMsg := validateStoredOpenAIResponseGetQuery(c); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	responseID := c.Param("response_id")
	stored, ok := defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		writeStoredOpenAIResponseNotFound(c, responseID)
		return
	}

	if strings.EqualFold(strings.TrimSpace(c.Query("stream")), "true") {
		h.writeStoredOpenAIResponseReplay(c, stored)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(stored.Response)
}

func (h *OpenAIResponsesAPIHandler) GetResponseInputItems(c *gin.Context) {
	if errMsg := validateStoredOpenAIResponseInputItemsQuery(c); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	responseID := c.Param("response_id")
	stored, ok := defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		writeStoredOpenAIResponseNotFound(c, responseID)
		return
	}

	body, errMsg := buildOpenAIResponseInputItemsList(stored.InputItems, c)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(body)
}

func (h *OpenAIResponsesAPIHandler) DeleteResponse(c *gin.Context) {
	responseID := c.Param("response_id")
	if ok := defaultStoredOpenAIResponseStore.Delete(responseID); !ok {
		writeStoredOpenAIResponseNotFound(c, responseID)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIResponseDeleted(responseID))
}

func (h *OpenAIResponsesAPIHandler) CancelResponse(c *gin.Context) {
	responseID := c.Param("response_id")
	stored, ok := defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		writeStoredOpenAIResponseNotFound(c, responseID)
		return
	}

	if !gjson.GetBytes(stored.Response, "background").Bool() {
		h.WriteErrorResponse(c, invalidOpenAIValue("background", "Invalid value for 'background': only responses created with background=true can be cancelled on POST /v1/responses/{response_id}/cancel."))
		return
	}

	responseBody := cloneBytesOrDefault(stored.Response, []byte(`{}`))
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, "status").String()))
	switch status {
	case "", "queued", "in_progress":
		_ = defaultStoredOpenAIResponseStore.CancelBackgroundTask(responseID, context.Canceled)
		responseBody = normalizeOpenAIBackgroundResponseCancelled(responseBody)
		defaultStoredOpenAIResponseStore.StoreIfPresentWithReplay(
			responseID,
			responseBody,
			stored.InputItems,
			marshalStoredOpenAIResponseReplayEvent("response.cancelled", responseBody),
			marshalStoredOpenAIResponseReplayEvent("response.done", responseBody),
		)
	case "cancelled", "completed", "failed", "incomplete":
	default:
		responseBody = normalizeOpenAIBackgroundResponseCancelled(responseBody)
		defaultStoredOpenAIResponseStore.StoreIfPresentWithReplay(
			responseID,
			responseBody,
			stored.InputItems,
			marshalStoredOpenAIResponseReplayEvent("response.cancelled", responseBody),
			marshalStoredOpenAIResponseReplayEvent("response.done", responseBody),
		)
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(responseBody)
}

func (h *OpenAIResponsesAPIHandler) InputTokens(c *gin.Context) {
	requestCtx := ensureGinRequestContext(c)

	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	modelName := gjson.GetBytes(rawJSON, "model").String()
	if errMsg := validateOpenAISurfaceModel(modelName); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	providers, normalizedModel, detailsErr := h.GetRequestDetailsForContext(requestCtx, modelName)
	if detailsErr != nil {
		h.WriteErrorResponse(c, detailsErr)
		return
	}
	if errMsg := validateOpenAIStoreSupport(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesConversationSupport(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesUnsupportedExecutionControls(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesUnsupportedRequestFeatures(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesParallelToolCalls(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMaxToolCalls(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMaxOutputTokens(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTopLogprobs(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTemperature(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTopP(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesServiceTier(rawJSON, "responses/input_tokens"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesInput(rawJSON); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesInputItemTypeSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesToolOutputItemSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMessageContentTypeSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesToolDefinitionSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesProviderRequestFeatureSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if rawJSON, _, detailsErr = prepareOpenAIConversationRequest(rawJSON, "responses/input_tokens"); detailsErr != nil {
		h.WriteErrorResponse(c, detailsErr)
		return
	}

	c.Header("Content-Type", "application/json")
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	resp, upstreamHeaders, errMsg := h.ExecuteCountWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, "")
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	resp = normalizeOpenAIResponseInputTokensBody(resp)
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

func (h *OpenAIResponsesAPIHandler) CreateConversation(c *gin.Context) {
	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	metadata, items, errMsg := parseOpenAIConversationCreateRequest(rawJSON)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	conversation := defaultStoredOpenAIConversationStore.Create(metadata, items)
	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIConversation(conversation))
}

func (h *OpenAIResponsesAPIHandler) GetConversation(c *gin.Context) {
	conversationID := c.Param("conversation_id")
	conversation, ok := defaultStoredOpenAIConversationStore.Load(conversationID)
	if !ok {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIConversation(conversation))
}

func (h *OpenAIResponsesAPIHandler) UpdateConversation(c *gin.Context) {
	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	metadata, errMsg := parseOpenAIConversationUpdateRequest(rawJSON)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	conversationID := c.Param("conversation_id")
	conversation, ok := defaultStoredOpenAIConversationStore.UpdateMetadata(conversationID, metadata)
	if !ok {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIConversation(conversation))
}

func (h *OpenAIResponsesAPIHandler) GetConversationItems(c *gin.Context) {
	if errMsg := validateStoredOpenAIConversationItemsQuery(c); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	conversationID := c.Param("conversation_id")
	conversation, ok := defaultStoredOpenAIConversationStore.Load(conversationID)
	if !ok {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}

	body, errMsg := buildOpenAIConversationItemsList(conversation.Items, c)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(body)
}

func (h *OpenAIResponsesAPIHandler) GetConversationItem(c *gin.Context) {
	if errMsg := validateStoredOpenAIConversationItemIncludeQuery(c, "GET /v1/conversations/{conversation_id}/items/{item_id}"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	conversationID := c.Param("conversation_id")
	itemID := c.Param("item_id")
	item, conversationExists, itemExists := defaultStoredOpenAIConversationStore.GetItem(conversationID, itemID)
	if !conversationExists {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}
	if !itemExists {
		writeStoredOpenAIConversationItemNotFound(c, conversationID, itemID)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIConversationItem(item))
}

func (h *OpenAIResponsesAPIHandler) AddConversationItems(c *gin.Context) {
	if errMsg := validateStoredOpenAIConversationItemIncludeQuery(c, "POST /v1/conversations/{conversation_id}/items"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	items, errMsg := parseOpenAIConversationAddItemsRequest(rawJSON)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	conversationID := c.Param("conversation_id")
	_, added, ok := defaultStoredOpenAIConversationStore.AddItems(conversationID, items)
	if !ok {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}

	body, errMarshal := json.Marshal(map[string]any{
		"object": "list",
		"data":   added,
	})
	if errMarshal != nil {
		body = []byte(`{"object":"list","data":[]}`)
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(body)
}

func (h *OpenAIResponsesAPIHandler) DeleteConversationItem(c *gin.Context) {
	conversationID := c.Param("conversation_id")
	itemID := c.Param("item_id")
	conversation, conversationExists, itemExists := defaultStoredOpenAIConversationStore.DeleteItem(conversationID, itemID)
	if !conversationExists {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}
	if !itemExists {
		writeStoredOpenAIConversationItemNotFound(c, conversationID, itemID)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIConversation(conversation))
}

func (h *OpenAIResponsesAPIHandler) DeleteConversation(c *gin.Context) {
	conversationID := c.Param("conversation_id")
	if ok := defaultStoredOpenAIConversationStore.Delete(conversationID); !ok {
		writeStoredOpenAIConversationNotFound(c, conversationID)
		return
	}

	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(marshalOpenAIConversationDeleted(conversationID))
}

func (h *OpenAIResponsesAPIHandler) Compact(c *gin.Context) {
	requestCtx := ensureGinRequestContext(c)

	rawJSON, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	if errMsg := validateOpenAISurfaceModel(gjson.GetBytes(rawJSON, "model").String()); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	providers, normalizedModel, detailsErr := h.GetRequestDetailsForContext(requestCtx, gjson.GetBytes(rawJSON, "model").String())
	if detailsErr != nil {
		h.WriteErrorResponse(c, detailsErr)
		return
	}
	if errMsg := validateOpenAIStoreSupport(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesConversationSupport(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIIncludeSupport(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesInput(rawJSON); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesParallelToolCalls(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMaxToolCalls(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMaxOutputTokens(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTopLogprobs(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTemperature(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesTopP(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesServiceTier(rawJSON, "responses"); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesInputItemTypeSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesToolOutputItemSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesMessageContentTypeSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesToolDefinitionSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	if errMsg := validateOpenAIResponsesProviderRequestFeatureSupport(rawJSON, normalizedModel, providers); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}
	var errMsg *interfaces.ErrorMessage
	conversationCtx := (*openAIConversationExecutionContext)(nil)
	if rawJSON, conversationCtx, errMsg = prepareOpenAIResponsesConversationRequest(rawJSON); errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		return
	}

	streamResult := gjson.GetBytes(rawJSON, "stream")
	if streamResult.Type == gjson.True {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Streaming not supported for compact responses",
				Type:    "invalid_request_error",
			},
		})
		return
	}
	if streamResult.Exists() {
		if updated, err := sjson.DeleteBytes(rawJSON, "stream"); err == nil {
			rawJSON = updated
		}
	}

	c.Header("Content-Type", "application/json")
	modelName := gjson.GetBytes(rawJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	stopKeepAlive := h.StartNonStreamingKeepAlive(c, cliCtx)
	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, "responses/compact")
	stopKeepAlive()
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	resp = attachOpenAIConversationToResponseBody(conversationCtx, resp)
	maybeStoreOpenAIConversationResponse(conversationCtx, resp)
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

// handleNonStreamingResponse handles non-streaming chat completion responses
// for Gemini models. It selects a client from the pool, sends the request, and
// aggregates the response before sending it back to the client in OpenAIResponses format.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
//   - rawJSON: The raw JSON bytes of the OpenAIResponses-compatible request
func (h *OpenAIResponsesAPIHandler) handleNonStreamingResponse(c *gin.Context, rawJSON []byte, conversationCtx *openAIConversationExecutionContext) {
	c.Header("Content-Type", "application/json")

	modelName := gjson.GetBytes(rawJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	stopKeepAlive := h.StartNonStreamingKeepAlive(c, cliCtx)

	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, "")
	stopKeepAlive()
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	resp = attachOpenAIConversationToResponseBody(conversationCtx, resp)
	maybeStoreOpenAIResponse(rawJSON, resp)
	maybeStoreOpenAIConversationResponse(conversationCtx, resp)
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

func (h *OpenAIResponsesAPIHandler) handleBackgroundResponse(c *gin.Context, rawJSON []byte, conversationCtx *openAIConversationExecutionContext, providers []string, normalizedModel string) {
	c.Header("Content-Type", "application/json")

	responseID := newStoredOpenAIResponseID()
	responseBody := marshalQueuedOpenAIResponse(rawJSON, responseID, time.Now().Unix())
	responseBody = attachOpenAIConversationToResponseBody(conversationCtx, responseBody)
	inputItems := normalizeOpenAIResponseInputItems(rawJSON)
	defaultStoredOpenAIResponseStore.StoreWithReplay(
		responseID,
		responseBody,
		inputItems,
		marshalStoredOpenAIResponseReplayEvent("response.created", responseBody),
		marshalStoredOpenAIResponseReplayEvent("response.queued", responseBody),
	)

	bgCtx, cancel := context.WithCancelCause(context.Background())
	if !defaultStoredOpenAIResponseStore.RegisterBackgroundTask(responseID, cancel) {
		cancel(errOpenAIBackgroundResponseDeleted)
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "failed to initialize background response lifecycle",
				Type:    "server_error",
			},
		})
		return
	}

	scope := handlers.AccessScopeFromGin(c)
	sessionKey := strings.TrimSpace(c.GetHeader("X-Session-Key"))
	requestJSON := cloneBytesOrDefault(rawJSON, nil)
	conversationCopy := cloneOpenAIConversationExecutionContext(conversationCtx)
	providerCopy := append([]string(nil), providers...)

	go h.executeBackgroundResponse(bgCtx, responseID, requestJSON, providerCopy, normalizedModel, conversationCopy, scope.AuthID, sessionKey)

	_, _ = c.Writer.Write(responseBody)
}

func (h *OpenAIResponsesAPIHandler) executeBackgroundResponse(ctx context.Context, responseID string, rawJSON []byte, providers []string, normalizedModel string, conversationCtx *openAIConversationExecutionContext, pinnedAuthID string, sessionKey string) {
	defer defaultStoredOpenAIResponseStore.FinishBackgroundTask(responseID)

	stored, ok := defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		return
	}
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(stored.Response, "status").String()), "cancelled") {
		return
	}

	inProgress := normalizeOpenAIBackgroundResponseInProgress(stored.Response)
	if !defaultStoredOpenAIResponseStore.StoreIfPresentWithReplay(
		responseID,
		inProgress,
		stored.InputItems,
		marshalStoredOpenAIResponseReplayEvent("response.in_progress", inProgress),
	) {
		return
	}

	reqMeta := map[string]any{
		coreexecutor.RequestedModelMetadataKey: normalizedModel,
	}
	if pinnedAuthID = strings.TrimSpace(pinnedAuthID); pinnedAuthID != "" {
		reqMeta[coreexecutor.PinnedAuthMetadataKey] = pinnedAuthID
	}
	if sessionKey = strings.TrimSpace(sessionKey); sessionKey != "" {
		reqMeta[coreexecutor.SessionKeyMetadataKey] = sessionKey
	}

	req := coreexecutor.Request{
		Model:   normalizedModel,
		Payload: cloneBytesOrDefault(rawJSON, nil),
	}
	opts := coreexecutor.Options{
		Stream:          false,
		OriginalRequest: cloneBytesOrDefault(rawJSON, nil),
		SourceFormat:    sdktranslator.FromString(h.HandlerType()),
		Metadata:        reqMeta,
	}

	resp, err := h.AuthManager.Execute(ctx, providers, req, opts)

	stored, ok = defaultStoredOpenAIResponseStore.Load(responseID)
	if !ok {
		return
	}
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(stored.Response, "status").String()), "cancelled") {
		return
	}

	if err != nil {
		if ctx.Err() != nil {
			return
		}
		failed := marshalFailedOpenAIBackgroundResponse(rawJSON, stored.Response, responseID, err)
		failed = attachOpenAIConversationToResponseBody(conversationCtx, failed)
		if defaultStoredOpenAIResponseStore.StoreIfPresentWithReplay(
			responseID,
			failed,
			stored.InputItems,
			marshalStoredOpenAIResponseReplayEvent("response.failed", failed),
			marshalStoredOpenAIResponseReplayEvent("response.done", failed),
		) {
			maybeStoreOpenAIConversationResponse(conversationCtx, failed)
		}
		return
	}

	finalResponse := normalizeOpenAIBackgroundTerminalResponse(rawJSON, stored.Response, resp.Payload, responseID)
	finalResponse = attachOpenAIConversationToResponseBody(conversationCtx, finalResponse)
	if defaultStoredOpenAIResponseStore.StoreIfPresentWithReplay(
		responseID,
		finalResponse,
		stored.InputItems,
		marshalStoredOpenAIResponseReplayEvent(openAIResponseTerminalReplayEventType(finalResponse), finalResponse),
		marshalStoredOpenAIResponseReplayEvent("response.done", finalResponse),
	) {
		maybeStoreOpenAIConversationResponse(conversationCtx, finalResponse)
	}
}

func cloneOpenAIConversationExecutionContext(src *openAIConversationExecutionContext) *openAIConversationExecutionContext {
	if src == nil {
		return nil
	}
	return &openAIConversationExecutionContext{
		ID:                src.ID,
		CurrentInputItems: cloneBytesOrDefault(src.CurrentInputItems, []byte("[]")),
	}
}

// handleStreamingResponse handles streaming responses for Gemini models.
// It establishes a streaming connection with the backend service and forwards
// the response chunks to the client in real-time using Server-Sent Events.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
//   - rawJSON: The raw JSON bytes of the OpenAIResponses-compatible request
func (h *OpenAIResponsesAPIHandler) handleStreamingResponse(c *gin.Context, rawJSON []byte, conversationCtx *openAIConversationExecutionContext) {
	// Get the http.Flusher interface to manually flush the response.
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Streaming not supported",
				Type:    "server_error",
			},
		})
		return
	}

	// New core execution path
	modelName := gjson.GetBytes(rawJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	dataChan, upstreamHeaders, errChan := h.ExecuteStreamWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, "")

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
			h.WriteErrorResponse(c, errMsg)
			if errMsg != nil {
				cliCancel(errMsg.Error)
			} else {
				cliCancel(nil)
			}
			return
		case chunk, ok := <-dataChan:
			if !ok {
				// Stream closed without data? Send headers and done.
				setSSEHeaders()
				handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
				_, _ = c.Writer.Write([]byte("\n"))
				flusher.Flush()
				cliCancel(nil)
				return
			}

			// Success! Set headers.
			setSSEHeaders()
			handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
			chunk = attachOpenAIConversationToStreamChunk(conversationCtx, chunk)
			maybeStoreOpenAIResponseFromStreamChunk(rawJSON, chunk)
			maybeStoreOpenAIConversationResponseFromStreamChunk(conversationCtx, chunk)

			// Write first chunk logic (matching forwardResponsesStream)
			if bytes.HasPrefix(chunk, []byte("event:")) {
				_, _ = c.Writer.Write([]byte("\n"))
			}
			_, _ = c.Writer.Write(chunk)
			_, _ = c.Writer.Write([]byte("\n"))
			flusher.Flush()

			// Continue
			h.forwardResponsesStream(c, flusher, func(err error) { cliCancel(err) }, rawJSON, conversationCtx, dataChan, errChan)
			return
		}
	}
}

func (h *OpenAIResponsesAPIHandler) forwardResponsesStream(c *gin.Context, flusher http.Flusher, cancel func(error), rawRequestJSON []byte, conversationCtx *openAIConversationExecutionContext, data <-chan []byte, errs <-chan *interfaces.ErrorMessage) {
	lastSequenceNumber := 0
	hasSequenceNumber := false

	h.ForwardStream(c, flusher, cancel, data, errs, handlers.StreamForwardOptions{
		WriteChunk: func(chunk []byte) {
			for _, payload := range websocketJSONPayloadsFromChunk(chunk) {
				seq := gjson.GetBytes(payload, "sequence_number")
				if !seq.Exists() {
					continue
				}
				value := int(seq.Int())
				if !hasSequenceNumber || value > lastSequenceNumber {
					lastSequenceNumber = value
					hasSequenceNumber = true
				}
			}
			chunk = attachOpenAIConversationToStreamChunk(conversationCtx, chunk)
			maybeStoreOpenAIResponseFromStreamChunk(rawRequestJSON, chunk)
			maybeStoreOpenAIConversationResponseFromStreamChunk(conversationCtx, chunk)
			if bytes.HasPrefix(chunk, []byte("event:")) {
				_, _ = c.Writer.Write([]byte("\n"))
			}
			_, _ = c.Writer.Write(chunk)
			_, _ = c.Writer.Write([]byte("\n"))
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
			errorSequenceNumber := 0
			if hasSequenceNumber {
				errorSequenceNumber = lastSequenceNumber + 1
			}
			chunk := handlers.BuildOpenAIResponsesStreamErrorChunk(status, errText, errorSequenceNumber)
			_, _ = fmt.Fprintf(c.Writer, "\nevent: error\ndata: %s\n\n", string(chunk))
		},
		WriteDone: func() {
			_, _ = c.Writer.Write([]byte("\n"))
		},
	})
}

func (h *OpenAIResponsesAPIHandler) writeStoredOpenAIResponseReplay(c *gin.Context, stored storedOpenAIResponse) {
	events := storedOpenAIResponseReplayEvents(stored)
	if len(events) == 0 {
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "stored response replay is unavailable",
				Type:    "server_error",
			},
		})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	flusher, _ := c.Writer.(http.Flusher)
	for _, event := range events {
		if eventType := strings.TrimSpace(gjson.GetBytes(event, "type").String()); eventType != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\n", eventType)
		}
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", event)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
