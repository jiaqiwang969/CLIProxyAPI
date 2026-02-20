package api

import (
	"encoding/json"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/claude/api"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/router"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// ClaudeHandlerIntegration 提供Claude处理器的集成功能
type ClaudeHandlerIntegration struct {
	backendRouter *api.ClaudeBackendRouter
}

// NewClaudeHandlerIntegration 创建新的集成实例
func NewClaudeHandlerIntegration() *ClaudeHandlerIntegration {
	return &ClaudeHandlerIntegration{
		backendRouter: api.NewClaudeBackendRouter(),
	}
}

// ShouldUseDirectAPI 检查是否应该使用Claude直接API
func (i *ClaudeHandlerIntegration) ShouldUseDirectAPI(modelName string) bool {
	return i.backendRouter.ShouldUseDirectAPI(modelName)
}

// HandleClaudeRequest 处理Claude请求，支持后端路由
func (i *ClaudeHandlerIntegration) HandleClaudeRequest(c *gin.Context, rawJSON []byte, isStream bool) bool {
	// 提取模型名称
	modelName := gjson.GetBytes(rawJSON, "model").String()

	// 检查是否应该使用Claude直接API
	if !i.ShouldUseDirectAPI(modelName) {
		log.Debugf("Using Antigravity backend for model %s", modelName)
		return false // 使用Antigravity处理
	}

	log.Debugf("Using Claude direct API for model %s", modelName)

	// 使用Claude直接API处理
	directAPIHandler := i.backendRouter.GetDirectAPIHandler()
	if directAPIHandler == nil {
		log.Warnf("Claude direct API handler not available, falling back to Antigravity")
		return false
	}

	// 处理请求
	if isStream {
		directAPIHandler.HandleChatCompletionStream(c, rawJSON)
	} else {
		directAPIHandler.HandleChatCompletion(c, rawJSON)
	}

	return true // 已处理
}

// ValidateRequest 验证请求
func (i *ClaudeHandlerIntegration) ValidateRequest(rawJSON []byte) error {
	return i.backendRouter.ValidateRequest(rawJSON)
}

// GetBackendType 获取后端类型
func (i *ClaudeHandlerIntegration) GetBackendType(modelName string) router.BackendType {
	return i.backendRouter.RouteRequest(modelName, nil)
}

// IsClaudeDirectAPIAvailable 检查Claude直接API是否可用
func (i *ClaudeHandlerIntegration) IsClaudeDirectAPIAvailable() bool {
	apiKey := os.Getenv("CLAUDE_API_KEY")
	return apiKey != ""
}

// GetDirectAPIHandler 获取Claude直接API处理器
func (i *ClaudeHandlerIntegration) GetDirectAPIHandler() *api.ClaudeDirectAPIHandler {
	return i.backendRouter.GetDirectAPIHandler()
}
