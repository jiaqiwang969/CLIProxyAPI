package api

// ExtractThinkingContent 从Claude响应中提取思维内容
func ExtractThinkingContent(claudeResp *ClaudeResponse) string {
	if claudeResp == nil {
		return ""
	}
	for _, block := range claudeResp.Content {
		if block.Type == "thinking" {
			return block.Thinking
		}
	}
	return ""
}

// ExtractTextContent 从Claude响应中提取文本内容
func ExtractTextContent(claudeResp *ClaudeResponse) string {
	if claudeResp == nil {
		return ""
	}
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

// HasThinkingContent 检查响应中是否包含思维内容
func HasThinkingContent(claudeResp *ClaudeResponse) bool {
	if claudeResp == nil {
		return false
	}
	for _, block := range claudeResp.Content {
		if block.Type == "thinking" && block.Thinking != "" {
			return true
		}
	}
	return false
}
