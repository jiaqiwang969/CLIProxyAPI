package models

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Handler 模型 HTTP 处理程序
type Handler struct {
	registry   *ModelRegistry
	keyManager *KeyManager
}

// NewHandler 创建新的处理程序
func NewHandler(registry *ModelRegistry, keyManager *KeyManager) *Handler {
	return &Handler{
		registry:   registry,
		keyManager: keyManager,
	}
}

// RegisterRoutes 注册路由
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// 模型相关路由
	v1 := router.Group("/v1")
	{
		// 获取模型列表
		v1.GET("/models", h.ListModels)
		v1.GET("/models/:id", h.GetModel)

		// 获取 Claude 模型
		v1.GET("/models/provider/claude", h.GetClaudeModels)

		// 获取 Gemini 模型
		v1.GET("/models/provider/gemini", h.GetGeminiModels)
	}

	// API 密钥相关路由
	keys := router.Group("/api/keys")
	{
		// 生成新密钥
		keys.POST("/generate", h.GenerateKey)

		// 获取密钥列表
		keys.GET("", h.ListKeys)

		// 获取密钥详情
		keys.GET("/:id", h.GetKey)

		// 删除密钥
		keys.DELETE("/:id", h.DeleteKey)

		// 禁用密钥
		keys.POST("/:id/disable", h.DisableKey)

		// 启用密钥
		keys.POST("/:id/enable", h.EnableKey)

		// 撤销密钥
		keys.POST("/:id/revoke", h.RevokeKey)
	}

	log.Info("模型和密钥管理路由已注册")
}

// ListModels 获取模型列表
// @Summary 获取所有可用模型
// @Description 获取所有已启用的模型列表
// @Tags Models
// @Produce json
// @Success 200 {array} ModelInfo
// @Router /v1/models [get]
func (h *Handler) ListModels(c *gin.Context) {
	models := h.registry.GetAllModels()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": models,
	})
}

// GetModel 获取模型详情
// @Summary 获取单个模型详情
// @Description 获取指定模型的详细信息
// @Tags Models
// @Produce json
// @Param id path string true "模型 ID"
// @Success 200 {object} ModelInfo
// @Router /v1/models/{id} [get]
func (h *Handler) GetModel(c *gin.Context) {
	id := c.Param("id")
	model := h.registry.GetModel(id)

	if model == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "模型不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": model,
	})
}

// GetClaudeModels 获取 Claude 模型列表
// @Summary 获取所有 Claude 模型
// @Description 获取所有可用的 Claude 模型
// @Tags Models
// @Produce json
// @Success 200 {array} ModelInfo
// @Router /v1/models/provider/claude [get]
func (h *Handler) GetClaudeModels(c *gin.Context) {
	models := h.registry.GetClaudeModels()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": models,
	})
}

// GetGeminiModels 获取 Gemini 模型列表
// @Summary 获取所有 Gemini 模型
// @Description 获取所有可用的 Gemini 模型
// @Tags Models
// @Produce json
// @Success 200 {array} ModelInfo
// @Router /v1/models/provider/gemini [get]
func (h *Handler) GetGeminiModels(c *gin.Context) {
	models := h.registry.GetGeminiModels()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": models,
	})
}

// GenerateKey 生成新的 API 密钥
// @Summary 生成新的 API 密钥
// @Description 生成一个新的 API 密钥用于认证
// @Tags Keys
// @Accept json
// @Produce json
// @Param request body map[string]interface{} true "请求体"
// @Success 200 {object} APIKey
// @Router /api/keys/generate [post]
func (h *Handler) GenerateKey(c *gin.Context) {
	var req struct {
		Name      string `json:"name" binding:"required"`
		ExpiresIn int    `json:"expires_in"` // 秒数
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误",
		})
		return
	}

	expiresIn := time.Duration(0)
	if req.ExpiresIn > 0 {
		expiresIn = time.Duration(req.ExpiresIn) * time.Second
	}

	key, err := h.keyManager.GenerateKey(req.Name, expiresIn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "生成密钥失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": key,
	})
}

// ListKeys 获取密钥列表
// @Summary 获取所有 API 密钥
// @Description 获取已创建的所有 API 密钥
// @Tags Keys
// @Produce json
// @Success 200 {array} APIKey
// @Router /api/keys [get]
func (h *Handler) ListKeys(c *gin.Context) {
	keys := h.keyManager.ListKeys()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": keys,
	})
}

// GetKey 获取密钥详情
// @Summary 获取单个密钥详情
// @Description 获取指定密钥的详细信息
// @Tags Keys
// @Produce json
// @Param id path string true "密钥 ID"
// @Success 200 {object} APIKey
// @Router /api/keys/{id} [get]
func (h *Handler) GetKey(c *gin.Context) {
	id := c.Param("id")
	key := h.keyManager.GetKey(id)

	if key == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "密钥不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": key,
	})
}

// DeleteKey 删除密钥
// @Summary 删除 API 密钥
// @Description 删除指定的 API 密钥
// @Tags Keys
// @Produce json
// @Param id path string true "密钥 ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/keys/{id} [delete]
func (h *Handler) DeleteKey(c *gin.Context) {
	id := c.Param("id")
	err := h.keyManager.DeleteKey(id)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "密钥不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "密钥已删除",
	})
}

// DisableKey 禁用密钥
// @Summary 禁用 API 密钥
// @Description 禁用指定的 API 密钥
// @Tags Keys
// @Produce json
// @Param id path string true "密钥 ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/keys/{id}/disable [post]
func (h *Handler) DisableKey(c *gin.Context) {
	id := c.Param("id")
	err := h.keyManager.DisableKey(id)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "密钥不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "密钥已禁用",
	})
}

// EnableKey 启用密钥
// @Summary 启用 API 密钥
// @Description 启用指定的 API 密钥
// @Tags Keys
// @Produce json
// @Param id path string true "密钥 ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/keys/{id}/enable [post]
func (h *Handler) EnableKey(c *gin.Context) {
	id := c.Param("id")
	err := h.keyManager.EnableKey(id)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "密钥不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "密钥已启用",
	})
}

// RevokeKey 撤销密钥
// @Summary 撤销 API 密钥
// @Description 撤销指定的 API 密钥（禁用）
// @Tags Keys
// @Produce json
// @Param id path string true "密钥 ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/keys/{id}/revoke [post]
func (h *Handler) RevokeKey(c *gin.Context) {
	id := c.Param("id")
	err := h.keyManager.RevokeKey(id)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "密钥不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "密钥已撤销",
	})
}
