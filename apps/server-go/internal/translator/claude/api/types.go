package api

// ClaudeRequest Claude API 请求格式
type ClaudeRequest struct {
	Model       string           `json:"model"`
	MaxTokens   int              `json:"max_tokens"`
	Messages    []Message        `json:"messages"`
	System      string           `json:"system,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
	TopP        float64          `json:"top_p,omitempty"`
	TopK        int              `json:"top_k,omitempty"`
	Thinking    *ThinkingConfig  `json:"thinking,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

// ThinkingConfig 思维配置
type ThinkingConfig struct {
	Type         string `json:"type"`           // "enabled"
	BudgetTokens int    `json:"budget_tokens"` // 思维令牌预算
}

// Message 消息格式
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ClaudeResponse Claude API 响应格式
type ClaudeResponse struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
	Model   string         `json:"model"`
	Usage   Usage          `json:"usage"`
}

// ContentBlock 内容块
type ContentBlock struct {
	Type     string `json:"type"`              // "thinking" 或 "text"
	Thinking string `json:"thinking,omitempty"` // 思维内容
	Text     string `json:"text,omitempty"`    // 文本内容
}

// Usage 使用情况
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamEvent 流式事件
type StreamEvent struct {
	Type    string         `json:"type"`
	Delta   *Delta         `json:"delta,omitempty"`
	Message *ClaudeResponse `json:"message,omitempty"`
}

// Delta 增量更新
type Delta struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking,omitempty"`
	Text     string `json:"text,omitempty"`
}
