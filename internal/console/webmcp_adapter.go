package console

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// WebMCPAdapter 将WebMCP功能集成到控制台
type WebMCPAdapter struct {
	tools          map[string]*ToolDefinition
	workflows      map[string]*WorkflowDefinition
	executions     map[string]*ExecutionRecord
	permissions    map[string]*PermissionDef
	metrics        map[string]*ToolMetrics
	mu             sync.RWMutex
}

// ToolDefinition 工具定义
type ToolDefinition struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Category    string                 `json:"category"`
	Tags        []string               `json:"tags"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Permissions []string               `json:"permissions"`
	Version     string                 `json:"version"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
}

// WorkflowDefinition 工作流定义
type WorkflowDefinition struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Steps       []WorkflowStep `json:"steps"`
	ErrorHandle string        `json:"errorHandle"` // stop, continue, retry
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
}

// WorkflowStep 工作流步骤
type WorkflowStep struct {
	ID        string                 `json:"id"`
	ToolID    string                 `json:"toolId"`
	Params    map[string]interface{} `json:"params"`
	Condition map[string]interface{} `json:"condition,omitempty"`
}

// ExecutionRecord 执行记录
type ExecutionRecord struct {
	ID         string                 `json:"id"`
	WorkflowID string                 `json:"workflowId"`
	Status     string                 `json:"status"` // running, completed, failed
	StartTime  time.Time              `json:"startTime"`
	EndTime    time.Time              `json:"endTime,omitempty"`
	Duration   int64                  `json:"duration,omitempty"` // milliseconds
	Steps      []StepExecution        `json:"steps"`
	Error      string                 `json:"error,omitempty"`
}

// StepExecution 步骤执行记录
type StepExecution struct {
	StepID    string                 `json:"stepId"`
	Status    string                 `json:"status"` // completed, failed, skipped
	Result    map[string]interface{} `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Duration  int64                  `json:"duration,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// PermissionDef 权限定义
type PermissionDef struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	RiskLevel   string    `json:"riskLevel"` // low, medium, high
	Category    string    `json:"category"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ToolMetrics 工具指标
type ToolMetrics struct {
	ToolID           string    `json:"toolId"`
	TotalExecutions  int       `json:"totalExecutions"`
	SuccessCount     int       `json:"successCount"`
	FailureCount     int       `json:"failureCount"`
	AvgDuration      float64   `json:"avgDuration"`
	MaxDuration      int64     `json:"maxDuration"`
	MinDuration      int64     `json:"minDuration"`
	LastExecution    time.Time `json:"lastExecution"`
	SuccessRate      float64   `json:"successRate"`
}

// NewWebMCPAdapter 创建新的WebMCP适配器
func NewWebMCPAdapter() *WebMCPAdapter {
	return &WebMCPAdapter{
		tools:      make(map[string]*ToolDefinition),
		workflows:  make(map[string]*WorkflowDefinition),
		executions: make(map[string]*ExecutionRecord),
		permissions: make(map[string]*PermissionDef),
		metrics:    make(map[string]*ToolMetrics),
	}
}

// RegisterTool 注册工具
func (w *WebMCPAdapter) RegisterTool(tool *ToolDefinition) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if tool.ID == "" {
		return fmt.Errorf("tool ID cannot be empty")
	}

	tool.CreatedAt = time.Now()
	tool.UpdatedAt = time.Now()
	w.tools[tool.ID] = tool

	// 初始化指标
	w.metrics[tool.ID] = &ToolMetrics{
		ToolID:      tool.ID,
		MinDuration: 9999999,
	}

	return nil
}

// GetTool 获取工具
func (w *WebMCPAdapter) GetTool(toolID string) (*ToolDefinition, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	tool, exists := w.tools[toolID]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", toolID)
	}
	return tool, nil
}

// ListTools 列出所有工具
func (w *WebMCPAdapter) ListTools() []*ToolDefinition {
	w.mu.RLock()
	defer w.mu.RUnlock()

	tools := make([]*ToolDefinition, 0, len(w.tools))
	for _, tool := range w.tools {
		tools = append(tools, tool)
	}
	return tools
}

// SearchTools 搜索工具
func (w *WebMCPAdapter) SearchTools(query string, category string, tags []string) []*ToolDefinition {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var results []*ToolDefinition
	for _, tool := range w.tools {
		// 按类别过滤
		if category != "" && tool.Category != category {
			continue
		}

		// 按标签过滤
		if len(tags) > 0 {
			hasTag := false
			for _, tag := range tags {
				for _, toolTag := range tool.Tags {
					if toolTag == tag {
						hasTag = true
						break
					}
				}
				if hasTag {
					break
				}
			}
			if !hasTag {
				continue
			}
		}

		// 按查询字符串过滤
		if query != "" {
			if !contains(tool.Name, query) && !contains(tool.Description, query) {
				continue
			}
		}

		results = append(results, tool)
	}
	return results
}

// DefineWorkflow 定义工作流
func (w *WebMCPAdapter) DefineWorkflow(workflow *WorkflowDefinition) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if workflow.ID == "" {
		return fmt.Errorf("workflow ID cannot be empty")
	}

	workflow.CreatedAt = time.Now()
	workflow.UpdatedAt = time.Now()
	w.workflows[workflow.ID] = workflow
	return nil
}

// GetWorkflow 获取工作流
func (w *WebMCPAdapter) GetWorkflow(workflowID string) (*WorkflowDefinition, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	workflow, exists := w.workflows[workflowID]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}
	return workflow, nil
}

// ListWorkflows 列出所有工作流
func (w *WebMCPAdapter) ListWorkflows() []*WorkflowDefinition {
	w.mu.RLock()
	defer w.mu.RUnlock()

	workflows := make([]*WorkflowDefinition, 0, len(w.workflows))
	for _, workflow := range w.workflows {
		workflows = append(workflows, workflow)
	}
	return workflows
}

// ExecuteWorkflow 执行工作流
func (w *WebMCPAdapter) ExecuteWorkflow(workflowID string, input map[string]interface{}) (*ExecutionRecord, error) {
	w.mu.Lock()
	workflow, exists := w.workflows[workflowID]
	w.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}

	executionID := fmt.Sprintf("%s-%d", workflowID, time.Now().UnixNano())
	execution := &ExecutionRecord{
		ID:        executionID,
		WorkflowID: workflowID,
		Status:    "running",
		StartTime: time.Now(),
		Steps:     make([]StepExecution, 0),
	}

	// 执行工作流步骤
	for _, step := range workflow.Steps {
		stepStart := time.Now()
		stepExec := StepExecution{
			StepID:    step.ID,
			Timestamp: stepStart,
		}

		// 获取工具
		tool, err := w.GetTool(step.ToolID)
		if err != nil {
			stepExec.Status = "failed"
			stepExec.Error = err.Error()
			execution.Steps = append(execution.Steps, stepExec)
			if workflow.ErrorHandle == "stop" {
				execution.Status = "failed"
				execution.Error = err.Error()
				break
			}
			continue
		}

		// 模拟工具执行
		stepExec.Status = "completed"
		stepExec.Result = map[string]interface{}{
			"toolId": tool.ID,
			"input":  step.Params,
		}
		stepExec.Duration = time.Since(stepStart).Milliseconds()

		execution.Steps = append(execution.Steps, stepExec)

		// 更新工具指标
		w.updateToolMetrics(tool.ID, stepExec.Duration, stepExec.Status == "completed")
	}

	execution.EndTime = time.Now()
	execution.Duration = execution.EndTime.Sub(execution.StartTime).Milliseconds()
	if execution.Status == "running" {
		execution.Status = "completed"
	}

	// 保存执行记录
	w.mu.Lock()
	w.executions[executionID] = execution
	w.mu.Unlock()

	return execution, nil
}

// GetExecution 获取执行记录
func (w *WebMCPAdapter) GetExecution(executionID string) (*ExecutionRecord, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	execution, exists := w.executions[executionID]
	if !exists {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}
	return execution, nil
}

// ListExecutions 列出执行记录
func (w *WebMCPAdapter) ListExecutions(workflowID string, limit int) []*ExecutionRecord {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var results []*ExecutionRecord
	for _, exec := range w.executions {
		if workflowID != "" && exec.WorkflowID != workflowID {
			continue
		}
		results = append(results, exec)
	}

	// 按时间倒序排列
	if len(results) > limit && limit > 0 {
		results = results[len(results)-limit:]
	}
	return results
}

// DefinePermission 定义权限
func (w *WebMCPAdapter) DefinePermission(perm *PermissionDef) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if perm.ID == "" {
		return fmt.Errorf("permission ID cannot be empty")
	}

	perm.CreatedAt = time.Now()
	w.permissions[perm.ID] = perm
	return nil
}

// GetToolMetrics 获取工具指标
func (w *WebMCPAdapter) GetToolMetrics(toolID string) (*ToolMetrics, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	metrics, exists := w.metrics[toolID]
	if !exists {
		return nil, fmt.Errorf("metrics not found for tool: %s", toolID)
	}
	return metrics, nil
}

// GetAllMetrics 获取所有指标
func (w *WebMCPAdapter) GetAllMetrics() map[string]*ToolMetrics {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make(map[string]*ToolMetrics)
	for k, v := range w.metrics {
		result[k] = v
	}
	return result
}

// updateToolMetrics 更新工具指标
func (w *WebMCPAdapter) updateToolMetrics(toolID string, duration int64, success bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	metrics, exists := w.metrics[toolID]
	if !exists {
		return
	}

	metrics.TotalExecutions++
	if success {
		metrics.SuccessCount++
	} else {
		metrics.FailureCount++
	}

	if duration > metrics.MaxDuration {
		metrics.MaxDuration = duration
	}
	if duration < metrics.MinDuration {
		metrics.MinDuration = duration
	}

	metrics.AvgDuration = float64(metrics.MaxDuration+metrics.MinDuration) / 2
	metrics.SuccessRate = float64(metrics.SuccessCount) / float64(metrics.TotalExecutions) * 100
	metrics.LastExecution = time.Now()
}

// Export 导出数据
func (w *WebMCPAdapter) Export() map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return map[string]interface{}{
		"tools":      w.tools,
		"workflows":  w.workflows,
		"executions": w.executions,
		"permissions": w.permissions,
		"metrics":    w.metrics,
	}
}

// Import 导入数据
func (w *WebMCPAdapter) Import(data map[string]interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if tools, ok := data["tools"].(map[string]interface{}); ok {
		for k, v := range tools {
			if b, err := json.Marshal(v); err == nil {
				var tool ToolDefinition
				if err := json.Unmarshal(b, &tool); err == nil {
					w.tools[k] = &tool
				}
			}
		}
	}

	return nil
}

// contains 检查字符串是否包含子字符串
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0)
}
