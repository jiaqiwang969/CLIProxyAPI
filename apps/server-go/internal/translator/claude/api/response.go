package api

import (
	"encoding/json"
	"fmt"
	"time"
)

// ConvertClaudeToOpenAI 将 Claude 响应转换为 OpenAI 格式
func ConvertClaudeToOpenAI(claudeResp *ClaudeResponse, modelName string) map[string]interface{} {
	if claudeResp == nil {
		return nil
	}

	// 提取思维内容和文本内容
	thinkingContent := ExtractThinkingContent(claudeResp)
	textContent := ExtractTextContent(claudeResp)

	// 构建 OpenAI 格式响应
	return map[string]interface{}{
		"id":      claudeResp.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   modelName,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":               "assistant",
					"content":            textContent,
					"reasoning_content":  thinkingContent, // 思维内容映射到 reasoning_content
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     claudeResp.Usage.InputTokens,
			"completion_tokens": claudeResp.Usage.OutputTokens,
			"total_tokens":      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
		},
	}
}

// ConvertClaudeToOpenAIJSON 将 Claude 响应转换为 OpenAI 格式的 JSON
func ConvertClaudeToOpenAIJSON(claudeResp *ClaudeResponse, modelName string) ([]byte, error) {
	openaiResp := ConvertClaudeToOpenAI(claudeResp, modelName)
	return json.Marshal(openaiResp)
}

// ConvertClaudeToOpenAIFromJSON 从 JSON 字符串转换 Claude 响应为 OpenAI 格式
func ConvertClaudeToOpenAIFromJSON(jsonData []byte, modelName string) ([]byte, error) {
	var claudeResp ClaudeResponse
	if err := json.Unmarshal(jsonData, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Claude response: %w", err)
	}
	return ConvertClaudeToOpenAIJSON(&claudeResp, modelName)
}
