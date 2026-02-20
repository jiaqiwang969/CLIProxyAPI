package api

import "errors"

var (
	// ErrClaudeAPINotAvailable Claude API 不可用
	ErrClaudeAPINotAvailable = errors.New("Claude API is not available")

	// ErrInvalidRequest 无效的请求
	ErrInvalidRequest = errors.New("invalid request")

	// ErrAPIKeyNotSet API密钥未设置
	ErrAPIKeyNotSet = errors.New("CLAUDE_API_KEY not set")
)
