package console

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebMCPHandler WebMCP API处理器
type WebMCPHandler struct {
	adapter *WebMCPAdapter
}

// NewWebMCPHandler 创建新的WebMCP处理器
func NewWebMCPHandler(adapter *WebMCPAdapter) *WebMCPHandler {
	return &WebMCPHandler{
		adapter: adapter,
	}
}

// RegisterRoutes 注册WebMCP路由
func (h *WebMCPHandler) RegisterRoutes(router *gin.Engine) {
	webmcp := router.Group("/api/webmcp")

	// 工具相关
	webmcp.POST("/tools", h.RegisterTool)
	webmcp.GET("/tools", h.ListTools)
	webmcp.GET("/tools/:id", h.GetTool)
	webmcp.GET("/tools/search", h.SearchTools)

	// 工作流相关
	webmcp.POST("/workflows", h.DefineWorkflow)
	webmcp.GET("/workflows", h.ListWorkflows)
	webmcp.GET("/workflows/:id", h.GetWorkflow)
	webmcp.POST("/workflows/:id/execute", h.ExecuteWorkflow)

	// 执行记录相关
	webmcp.GET("/executions", h.ListExecutions)
	webmcp.GET("/executions/:id", h.GetExecution)

	// 权限相关
	webmcp.POST("/permissions", h.DefinePermission)

	// 指标相关
	webmcp.GET("/metrics", h.GetAllMetrics)
	webmcp.GET("/metrics/:toolId", h.GetToolMetrics)

	// 数据导入导出
	webmcp.GET("/export", h.Export)
	webmcp.POST("/import", h.Import)
}

// RegisterTool 注册工具
func (h *WebMCPHandler) RegisterTool(c *gin.Context) {
	var tool ToolDefinition
	if err := c.ShouldBindJSON(&tool); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.adapter.RegisterTool(&tool); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"tool":    tool,
	})
}

// ListTools 列出工具
func (h *WebMCPHandler) ListTools(c *gin.Context) {
	tools := h.adapter.ListTools()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"tools":   tools,
		"count":   len(tools),
	})
}

// GetTool 获取工具
func (h *WebMCPHandler) GetTool(c *gin.Context) {
	toolID := c.Param("id")
	tool, err := h.adapter.GetTool(toolID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"tool":    tool,
	})
}

// SearchTools 搜索工具
func (h *WebMCPHandler) SearchTools(c *gin.Context) {
	query := c.Query("q")
	category := c.Query("category")
	tags := c.QueryArray("tags")

	tools := h.adapter.SearchTools(query, category, tags)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"tools":   tools,
		"count":   len(tools),
	})
}

// DefineWorkflow 定义工作流
func (h *WebMCPHandler) DefineWorkflow(c *gin.Context) {
	var workflow WorkflowDefinition
	if err := c.ShouldBindJSON(&workflow); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.adapter.DefineWorkflow(&workflow); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"workflow": workflow,
	})
}

// ListWorkflows 列出工作流
func (h *WebMCPHandler) ListWorkflows(c *gin.Context) {
	workflows := h.adapter.ListWorkflows()
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"workflows": workflows,
		"count":     len(workflows),
	})
}

// GetWorkflow 获取工作流
func (h *WebMCPHandler) GetWorkflow(c *gin.Context) {
	workflowID := c.Param("id")
	workflow, err := h.adapter.GetWorkflow(workflowID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"workflow": workflow,
	})
}

// ExecuteWorkflow 执行工作流
func (h *WebMCPHandler) ExecuteWorkflow(c *gin.Context) {
	workflowID := c.Param("id")

	var input map[string]interface{}
	if err := c.ShouldBindJSON(&input); err != nil {
		input = make(map[string]interface{})
	}

	execution, err := h.adapter.ExecuteWorkflow(workflowID, input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"execution": execution,
	})
}

// ListExecutions 列出执行记录
func (h *WebMCPHandler) ListExecutions(c *gin.Context) {
	workflowID := c.Query("workflowId")
	limit := 50

	executions := h.adapter.ListExecutions(workflowID, limit)
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"executions": executions,
		"count":      len(executions),
	})
}

// GetExecution 获取执行记录
func (h *WebMCPHandler) GetExecution(c *gin.Context) {
	executionID := c.Param("id")
	execution, err := h.adapter.GetExecution(executionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"execution": execution,
	})
}

// DefinePermission 定义权限
func (h *WebMCPHandler) DefinePermission(c *gin.Context) {
	var perm PermissionDef
	if err := c.ShouldBindJSON(&perm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.adapter.DefinePermission(&perm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"permission": perm,
	})
}

// GetToolMetrics 获取工具指标
func (h *WebMCPHandler) GetToolMetrics(c *gin.Context) {
	toolID := c.Param("toolId")
	metrics, err := h.adapter.GetToolMetrics(toolID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"metrics": metrics,
	})
}

// GetAllMetrics 获取所有指标
func (h *WebMCPHandler) GetAllMetrics(c *gin.Context) {
	metrics := h.adapter.GetAllMetrics()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"metrics": metrics,
	})
}

// Export 导出数据
func (h *WebMCPHandler) Export(c *gin.Context) {
	data := h.adapter.Export()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// Import 导入数据
func (h *WebMCPHandler) Import(c *gin.Context) {
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.adapter.Import(data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Data imported successfully",
	})
}
