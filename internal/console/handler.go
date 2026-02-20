package console

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Handler Token 看板 HTTP 处理程序
type Handler struct {
	manager *ConsoleManager
}

// NewHandler 创建新的处理程序
func NewHandler(manager *ConsoleManager) *Handler {
	return &Handler{
		manager: manager,
	}
}

// RegisterRoutes 注册路由
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// 静态文件
	router.Static("/console", "./web/token-console/public")

	// API 路由
	api := router.Group("/api/console")
	{
		// 统计信息
		api.GET("/stats", h.GetStats)
		api.GET("/usage-trend", h.GetUsageTrend)

		// 日志
		api.GET("/logs", h.GetLogs)

		// API 密钥
		api.GET("/keys", h.GetAPIKeys)
		api.POST("/keys", h.CreateAPIKey)
		api.DELETE("/keys/:id", h.DeleteAPIKey)

		// 导出
		api.GET("/export", h.ExportStats)
	}

	log.Info("Token 看板路由已注册")
}

// GetStats 获取统计信息
// @Summary 获取 Token 使用统计
// @Description 获取当前 Token 使用情况统计
// @Tags Console
// @Produce json
// @Success 200 {object} TokenStats
// @Router /api/console/stats [get]
func (h *Handler) GetStats(c *gin.Context) {
	stats := h.manager.GetStats()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}

// GetUsageTrend 获取使用趋势
// @Summary 获取使用趋势数据
// @Description 获取指定天数的使用趋势数据
// @Tags Console
// @Produce json
// @Param days query int false "天数" default(7)
// @Success 200 {object} map[string]interface{}
// @Router /api/console/usage-trend [get]
func (h *Handler) GetUsageTrend(c *gin.Context) {
	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 90 {
			days = parsed
		}
	}

	trend := h.manager.GetUsageTrend(days)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": trend,
	})
}

// GetLogs 获取日志列表
// @Summary 获取 API 调用日志
// @Description 获取最近的 API 调用日志
// @Tags Console
// @Produce json
// @Param limit query int false "限制数量" default(100)
// @Success 200 {array} APILog
// @Router /api/console/logs [get]
func (h *Handler) GetLogs(c *gin.Context) {
	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	logs := h.manager.GetLogs(limit)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": logs,
	})
}

// GetAPIKeys 获取 API 密钥列表
// @Summary 获取所有 API 密钥
// @Description 获取已创建的所有 API 密钥（不包含完整值）
// @Tags Console
// @Produce json
// @Success 200 {array} APIKey
// @Router /api/console/keys [get]
func (h *Handler) GetAPIKeys(c *gin.Context) {
	keys := h.manager.GetAPIKeys()

	// 隐藏完整的密钥值
	for i := range keys {
		if len(keys[i].Value) > 10 {
			keys[i].Value = keys[i].Value[:10] + "..." + keys[i].Value[len(keys[i].Value)-4:]
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": keys,
	})
}

// CreateAPIKey 创建 API 密钥
// @Summary 创建新的 API 密钥
// @Description 创建一个新的 API 密钥
// @Tags Console
// @Accept json
// @Produce json
// @Param request body map[string]string true "请求体"
// @Success 200 {object} APIKey
// @Router /api/console/keys [post]
func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "请求参数错误",
		})
		return
	}

	key := h.manager.CreateAPIKey(req.Name, req.Description)
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": key,
	})
}

// DeleteAPIKey 删除 API 密钥
// @Summary 删除 API 密钥
// @Description 删除指定的 API 密钥
// @Tags Console
// @Produce json
// @Param id path int true "密钥 ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/console/keys/{id} [delete]
func (h *Handler) DeleteAPIKey(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "无效的密钥 ID",
		})
		return
	}

	if err := h.manager.DeleteAPIKey(id); err != nil {
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

// ExportStats 导出统计数据
// @Summary 导出统计数据
// @Description 导出所有统计数据为 JSON 格式
// @Tags Console
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/console/export [get]
func (h *Handler) ExportStats(c *gin.Context) {
	data, err := h.manager.ExportStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "导出失败",
		})
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=token-stats.json")
	c.Data(http.StatusOK, "application/json", data)
}

// RecordAPICall 记录 API 调用（中间件）
func (h *Handler) RecordAPICall() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录开始时间
		startTime := c.GetInt64("start_time")
		if startTime == 0 {
			startTime = 1000 // 默认耗时
		}

		// 获取模型名称
		model := c.GetString("model")
		if model == "" {
			model = "unknown"
		}

		// 获取 Token 数
		tokens := c.GetInt64("tokens")

		// 记录日志
		h.manager.RecordLog(
			c.Request.Method,
			c.Request.URL.Path,
			model,
			c.Writer.Status(),
			tokens,
			startTime,
		)

		c.Next()
	}
}
