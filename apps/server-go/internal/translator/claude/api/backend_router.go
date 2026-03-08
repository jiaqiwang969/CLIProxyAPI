package api

import (
	"encoding/json"
	"os"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/router"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// ClaudeBackendRouter 处理Claude后端路由
type ClaudeBackendRouter struct {
	directAPIHandler *ClaudeDirectAPIHandler
}

// NewClaudeBackendRouter 创建新的Claude后端路由器
func NewClaudeBackendRouter() *ClaudeBackendRouter {
	return &ClaudeBackendRouter{
		directAPIHandler: NewClaudeDirectAPIHandler(),
	}
}

// ShouldUseDirectAPI 检查是否应该使用Claude直接API
func (r *ClaudeBackendRouter) ShouldUseDirectAPI(modelName string) bool {
	// 检查是否是Claude模型
	if !router.IsClaudeModel(modelName) {
		return false
	}

	// 检查Claude API密钥是否设置
	apiKey := os.Getenv("CLAUDE_API_KEY")
	if apiKey == "" {
		log.Debugf("CLAUDE_API_KEY not set, falling back to Antigravity for model %s", modelName)
		return false
	}

	// 检查是否启用了Claude直接API
	if !r.directAPIHandler.IsAvailable() {
		log.Debugf("Claude direct API not available, falling back to Antigravity for model %s", modelName)
		return false
	}

	return true
}

// RouteRequest 根据模型名称和请求内容路由到合适的后端
func (r *ClaudeBackendRouter) RouteRequest(modelName string, rawJSON []byte) router.BackendType {
	if r.ShouldUseDirectAPI(modelName) {
		log.Debugf("Routing Claude model %s to direct API", modelName)
		return router.BackendClaudeAPI
	}

	log.Debugf("Routing Claude model %s to Antigravity", modelName)
	return router.BackendAntigravity
}

// GetDirectAPIHandler 获取Claude直接API处理器
func (r *ClaudeBackendRouter) GetDirectAPIHandler() *ClaudeDirectAPIHandler {
	return r.directAPIHandler
}

// ValidateRequest 验证请求是否有效
func (r *ClaudeBackendRouter) ValidateRequest(rawJSON []byte) error {
	// 检查是否是有效的JSON
	var req map[string]interface{}
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return err
	}

	// 检查是否包含必要的字段
	if _, ok := req["model"]; !ok {
		return ErrInvalidRequest
	}

	if _, ok := req["messages"]; !ok {
		return ErrInvalidRequest
	}

	return nil
}

// ExtractModelName 从请求中提取模型名称
func (r *ClaudeBackendRouter) ExtractModelName(rawJSON []byte) string {
	return gjson.GetBytes(rawJSON, "model").String()
}

// ExtractStream 从请求中提取stream标志
func (r *ClaudeBackendRouter) ExtractStream(rawJSON []byte) bool {
	streamResult := gjson.GetBytes(rawJSON, "stream")
	return streamResult.Exists() && streamResult.Type != gjson.False
}
