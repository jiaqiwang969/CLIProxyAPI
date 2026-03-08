package api

import (
	"encoding/json"
	"fmt"
)

// ConvertOpenAIToClaude 将 OpenAI 请求转换为 Claude 格式
func ConvertOpenAIToClaude(openaiReq map[string]interface{}) *ClaudeRequest {
	claudeReq := &ClaudeRequest{
		Stream: false,
	}

	// 提取模型名称
	if model, ok := openaiReq["model"].(string); ok {
		claudeReq.Model = model
	}

	// 提取最大令牌数
	if maxTokens, ok := openaiReq["max_tokens"].(float64); ok {
		claudeReq.MaxTokens = int(maxTokens)
	}

	// 转换消息
	if messages, ok := openaiReq["messages"].([]interface{}); ok {
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				role := ""
				content := ""
				if r, ok := msgMap["role"].(string); ok {
					role = r
				}
				if c, ok := msgMap["content"].(string); ok {
					content = c
				}
				if role != "" && content != "" {
					claudeReq.Messages = append(claudeReq.Messages, Message{
						Role:    role,
						Content: content,
					})
				}
			}
		}
	}

	// 转换温度参数
	if temp, ok := openaiReq["temperature"].(float64); ok {
		claudeReq.Temperature = temp
	}

	// 转换 top_p 参数
	if topP, ok := openaiReq["top_p"].(float64); ok {
		claudeReq.TopP = topP
	}

	// 转换 top_k 参数
	if topK, ok := openaiReq["top_k"].(float64); ok {
		claudeReq.TopK = int(topK)
	}

	// 启用思维功能
	claudeReq.Thinking = &ThinkingConfig{
		Type:         "enabled",
		BudgetTokens: 10000, // 默认思维令牌预算
	}

	return claudeReq
}

// ConvertOpenAIToClaudeJSON 将 OpenAI 请求转换为 Claude 格式的 JSON
func ConvertOpenAIToClaudeJSON(openaiReq map[string]interface{}) ([]byte, error) {
	claudeReq := ConvertOpenAIToClaude(openaiReq)
	return json.Marshal(claudeReq)
}

// ConvertOpenAIToClaudeFromJSON 从 JSON 字符串转换 OpenAI 请求为 Claude 格式
func ConvertOpenAIToClaudeFromJSON(jsonData []byte) (*ClaudeRequest, error) {
	var openaiReq map[string]interface{}
	if err := json.Unmarshal(jsonData, &openaiReq); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI request: %w", err)
	}
	return ConvertOpenAIToClaude(openaiReq), nil
}
