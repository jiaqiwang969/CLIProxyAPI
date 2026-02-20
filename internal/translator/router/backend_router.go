package router

import (
	"strings"
)

type BackendType string

const (
	BackendClaudeAPI   BackendType = "claude-api"
	BackendAntigravity BackendType = "antigravity"
	BackendDefault     BackendType = "default"
)

// RouteRequest 根据模型名称路由到合适的后端
func RouteRequest(modelName string) BackendType {
	modelLower := strings.ToLower(modelName)

	// Claude 模型使用 Claude 直接 API
	if strings.Contains(modelLower, "claude") {
		return BackendClaudeAPI
	}

	// Gemini 模型使用 Antigravity API
	if strings.Contains(modelLower, "gemini") {
		return BackendAntigravity
	}

	return BackendDefault
}

// IsClaudeModel 检查是否是 Claude 模型
func IsClaudeModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "claude")
}

// IsGeminiModel 检查是否是 Gemini 模型
func IsGeminiModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "gemini")
}
