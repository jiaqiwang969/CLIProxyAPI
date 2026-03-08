package api

import (
	"encoding/json"
	"testing"
)

// TestConvertOpenAIToClaude 测试OpenAI格式转换为Claude格式
func TestConvertOpenAIToClaude(t *testing.T) {
	openaiReq := map[string]interface{}{
		"model": "claude-sonnet-4-6",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "What is 2+2?",
			},
		},
		"max_tokens":   float64(100),
		"temperature":  0.7,
		"top_p":        0.9,
	}

	claudeReq := ConvertOpenAIToClaude(openaiReq)

	if claudeReq.Model != "claude-sonnet-4-6" {
		t.Errorf("Expected model claude-sonnet-4-6, got %s", claudeReq.Model)
	}

	if claudeReq.MaxTokens != 100 {
		t.Errorf("Expected max_tokens 100, got %d", claudeReq.MaxTokens)
	}

	if len(claudeReq.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(claudeReq.Messages))
	}

	if claudeReq.Thinking == nil {
		t.Error("Expected thinking config to be set")
	}

	if claudeReq.Thinking.Type != "enabled" {
		t.Errorf("Expected thinking type 'enabled', got %s", claudeReq.Thinking.Type)
	}
}

// TestConvertClaudeToOpenAI 测试Claude格式转换为OpenAI格式
func TestConvertClaudeToOpenAI(t *testing.T) {
	claudeResp := &ClaudeResponse{
		ID:   "msg_123",
		Type: "message",
		Role: "assistant",
		Content: []ContentBlock{
			{
				Type:     "thinking",
				Thinking: "Let me think about this...",
			},
			{
				Type: "text",
				Text: "2+2 equals 4",
			},
		},
		Model: "claude-sonnet-4-6",
		Usage: Usage{
			InputTokens:  10,
			OutputTokens: 20,
		},
	}

	openaiResp := ConvertClaudeToOpenAI(claudeResp, "claude-sonnet-4-6")

	if openaiResp == nil {
		t.Error("Expected non-nil response")
		return
	}

	if openaiResp["model"] != "claude-sonnet-4-6" {
		t.Errorf("Expected model claude-sonnet-4-6, got %v", openaiResp["model"])
	}

	choices := openaiResp["choices"].([]map[string]interface{})
	if len(choices) != 1 {
		t.Errorf("Expected 1 choice, got %d", len(choices))
	}

	message := choices[0]["message"].(map[string]interface{})
	if message["content"] != "2+2 equals 4" {
		t.Errorf("Expected content '2+2 equals 4', got %v", message["content"])
	}

	if message["reasoning_content"] != "Let me think about this..." {
		t.Errorf("Expected reasoning_content 'Let me think about this...', got %v", message["reasoning_content"])
	}
}

// TestExtractThinkingContent 测试思维内容提取
func TestExtractThinkingContent(t *testing.T) {
	claudeResp := &ClaudeResponse{
		Content: []ContentBlock{
			{
				Type:     "thinking",
				Thinking: "This is thinking content",
			},
			{
				Type: "text",
				Text: "This is text content",
			},
		},
	}

	thinking := ExtractThinkingContent(claudeResp)
	if thinking != "This is thinking content" {
		t.Errorf("Expected 'This is thinking content', got %s", thinking)
	}
}

// TestExtractTextContent 测试文本内容提取
func TestExtractTextContent(t *testing.T) {
	claudeResp := &ClaudeResponse{
		Content: []ContentBlock{
			{
				Type:     "thinking",
				Thinking: "This is thinking content",
			},
			{
				Type: "text",
				Text: "This is text content",
			},
		},
	}

	text := ExtractTextContent(claudeResp)
	if text != "This is text content" {
		t.Errorf("Expected 'This is text content', got %s", text)
	}
}

// TestHasThinkingContent 测试思维内容检查
func TestHasThinkingContent(t *testing.T) {
	claudeResp := &ClaudeResponse{
		Content: []ContentBlock{
			{
				Type:     "thinking",
				Thinking: "This is thinking content",
			},
		},
	}

	if !HasThinkingContent(claudeResp) {
		t.Error("Expected HasThinkingContent to return true")
	}

	claudeRespNoThinking := &ClaudeResponse{
		Content: []ContentBlock{
			{
				Type: "text",
				Text: "This is text content",
			},
		},
	}

	if HasThinkingContent(claudeRespNoThinking) {
		t.Error("Expected HasThinkingContent to return false")
	}
}

// TestConvertOpenAIToClaudeJSON 测试JSON转换
func TestConvertOpenAIToClaudeJSON(t *testing.T) {
	openaiReq := map[string]interface{}{
		"model": "claude-sonnet-4-6",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
		"max_tokens": 100,
	}

	claudeReqJSON, err := ConvertOpenAIToClaudeJSON(openaiReq)
	if err != nil {
		t.Fatalf("Failed to convert: %v", err)
	}

	var claudeReq ClaudeRequest
	if err := json.Unmarshal(claudeReqJSON, &claudeReq); err != nil {
		t.Fatalf("Failed to unmarshal Claude request: %v", err)
	}

	if claudeReq.Model != "claude-sonnet-4-6" {
		t.Errorf("Expected model claude-sonnet-4-6, got %s", claudeReq.Model)
	}
}
