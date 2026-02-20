package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// ClaudeDirectAPIHandler 处理Claude直接API的请求
type ClaudeDirectAPIHandler struct {
	adapter *Adapter
}

// NewClaudeDirectAPIHandler 创建新的Claude直接API处理器
func NewClaudeDirectAPIHandler() *ClaudeDirectAPIHandler {
	apiKey := os.Getenv("CLAUDE_API_KEY")
	if apiKey == "" {
		log.Warn("CLAUDE_API_KEY not set, Claude direct API will not be available")
	}
	return &ClaudeDirectAPIHandler{
		adapter: NewAdapter(apiKey),
	}
}

// HandleChatCompletion 处理非流式聊天完成请求
func (h *ClaudeDirectAPIHandler) HandleChatCompletion(c *gin.Context, rawJSON []byte) {
	c.Header("Content-Type", "application/json")

	// 转换请求格式
	var openaiReq map[string]interface{}
	if err := json.Unmarshal(rawJSON, &openaiReq); err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	modelName := gjson.GetBytes(rawJSON, "model").String()

	// 转换为Claude格式
	claudeReq := ConvertOpenAIToClaude(openaiReq)

	// 发送请求
	claudeResp, err := h.adapter.SendRequest(claudeReq)
	if err != nil {
		log.Errorf("Claude API error: %v", err)
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Claude API error: %v", err),
				Type:    "server_error",
			},
		})
		return
	}

	// 转换响应格式
	openaiResp := ConvertClaudeToOpenAI(claudeResp, modelName)

	// 返回响应
	c.JSON(http.StatusOK, openaiResp)
}

// HandleChatCompletionStream 处理流式聊天完成请求
func (h *ClaudeDirectAPIHandler) HandleChatCompletionStream(c *gin.Context, rawJSON []byte) {
	// 获取 http.Flusher 接口
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

	// 转换请求格式
	var openaiReq map[string]interface{}
	if err := json.Unmarshal(rawJSON, &openaiReq); err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	modelName := gjson.GetBytes(rawJSON, "model").String()

	// 转换为Claude格式
	claudeReq := ConvertOpenAIToClaude(openaiReq)

	// 发送流式请求
	stream, err := h.adapter.SendStreamRequest(claudeReq)
	if err != nil {
		log.Errorf("Claude API stream error: %v", err)
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Claude API error: %v", err),
				Type:    "server_error",
			},
		})
		return
	}
	defer stream.Close()

	// 设置SSE响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// 处理流式响应
	h.forwardClaudeStream(c, flusher, stream, modelName)
}

// forwardClaudeStream 转发Claude流式响应
func (h *ClaudeDirectAPIHandler) forwardClaudeStream(c *gin.Context, flusher http.Flusher, stream io.ReadCloser, modelName string) {
	decoder := json.NewDecoder(stream)

	for {
		select {
		case <-c.Request.Context().Done():
			return
		default:
		}

		var event StreamEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				// 流结束
				fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			log.Errorf("Failed to decode stream event: %v", err)
			return
		}

		// 转换为OpenAI格式的流事件
		openaiEvent := h.convertStreamEvent(&event, modelName)
		if openaiEvent != "" {
			fmt.Fprintf(c.Writer, "data: %s\n\n", openaiEvent)
			flusher.Flush()
		}
	}
}

// convertStreamEvent 将Claude流事件转换为OpenAI格式
func (h *ClaudeDirectAPIHandler) convertStreamEvent(event *StreamEvent, modelName string) string {
	if event == nil {
		return ""
	}

	// 处理不同类型的事件
	switch event.Type {
	case "content_block_start":
		// 内容块开始
		return ""

	case "content_block_delta":
		// 内容块增量
		if event.Delta == nil {
			return ""
		}

		// 构建OpenAI格式的流事件
		openaiChunk := map[string]interface{}{
			"id":      "chatcmpl-" + generateID(),
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   modelName,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"role":    "assistant",
						"content": event.Delta.Text,
					},
					"finish_reason": nil,
				},
			},
		}

		data, _ := json.Marshal(openaiChunk)
		return string(data)

	case "message_stop":
		// 消息停止
		openaiChunk := map[string]interface{}{
			"id":      "chatcmpl-" + generateID(),
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   modelName,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         map[string]interface{}{},
					"finish_reason": "stop",
				},
			},
		}

		data, _ := json.Marshal(openaiChunk)
		return string(data)

	default:
		return ""
	}
}

// generateID 生成随机ID
func generateID() string {
	return fmt.Sprintf("%d", os.Getpid())
}

// IsAvailable 检查Claude直接API是否可用
func (h *ClaudeDirectAPIHandler) IsAvailable() bool {
	return h.adapter != nil && h.adapter.apiKey != ""
}
