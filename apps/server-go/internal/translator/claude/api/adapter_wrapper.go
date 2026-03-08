package api

import (
	"io"
	"os"
)

// Adapter Claude API 适配器包装类
type Adapter struct {
	apiKey  string
	adapter *ClaudeAPIAdapter
}

// NewAdapter 创建新的适配器
func NewAdapter(apiKey string) *Adapter {
	if apiKey == "" {
		apiKey = os.Getenv("CLAUDE_API_KEY")
	}

	var adapter *ClaudeAPIAdapter
	if apiKey != "" {
		adapter = NewClaudeAPIAdapter(apiKey)
	}

	return &Adapter{
		apiKey:  apiKey,
		adapter: adapter,
	}
}

// SendRequest 发送请求到Claude API
func (a *Adapter) SendRequest(req *ClaudeRequest) (*ClaudeResponse, error) {
	if a.adapter == nil {
		return nil, ErrClaudeAPINotAvailable
	}
	return a.adapter.SendRequest(req)
}

// SendStreamRequest 发送流式请求到Claude API
func (a *Adapter) SendStreamRequest(req *ClaudeRequest) (io.ReadCloser, error) {
	if a.adapter == nil {
		return nil, ErrClaudeAPINotAvailable
	}
	return a.adapter.SendStreamRequest(req)
}

// IsAvailable 检查适配器是否可用
func (a *Adapter) IsAvailable() bool {
	return a.adapter != nil && a.apiKey != ""
}
