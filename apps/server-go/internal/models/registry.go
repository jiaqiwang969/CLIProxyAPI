package models

// ModelProvider 模型提供商
type ModelProvider string

const (
	ProviderClaude      ModelProvider = "claude"
	ProviderGemini      ModelProvider = "gemini"
	ProviderOpenAI      ModelProvider = "openai"
	ProviderAntigravity ModelProvider = "antigravity"
)

// ModelInfo 模型信息
type ModelInfo struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	DisplayName     string        `json:"display_name"`
	Description     string        `json:"description"`
	Provider        ModelProvider `json:"provider"`
	MaxTokens       int           `json:"max_tokens"`
	ContextWindow   int           `json:"context_window"`
	CostPer1KInput  float64       `json:"cost_per_1k_input"`
	CostPer1KOutput float64       `json:"cost_per_1k_output"`
	Enabled         bool          `json:"enabled"`
	CreatedAt       string        `json:"created_at"`
	UpdatedAt       string        `json:"updated_at"`
}

// ModelRegistry 模型注册表
type ModelRegistry struct {
	models map[string]*ModelInfo
}

// NewModelRegistry 创建新的模型注册表
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		models: make(map[string]*ModelInfo),
	}
}

// RegisterModel 注册模型
func (mr *ModelRegistry) RegisterModel(model *ModelInfo) {
	if model != nil {
		mr.models[model.ID] = model
	}
}

// GetModel 获取模型信息
func (mr *ModelRegistry) GetModel(id string) *ModelInfo {
	return mr.models[id]
}

// GetModelsByProvider 按提供商获取模型
func (mr *ModelRegistry) GetModelsByProvider(provider ModelProvider) []*ModelInfo {
	var result []*ModelInfo
	for _, model := range mr.models {
		if model.Provider == provider && model.Enabled {
			result = append(result, model)
		}
	}
	return result
}

// GetAllModels 获取所有模型
func (mr *ModelRegistry) GetAllModels() []*ModelInfo {
	var result []*ModelInfo
	for _, model := range mr.models {
		if model.Enabled {
			result = append(result, model)
		}
	}
	return result
}

// GetClaudeModels 获取所有 Claude 模型
func (mr *ModelRegistry) GetClaudeModels() []*ModelInfo {
	return mr.GetModelsByProvider(ProviderClaude)
}

// GetGeminiModels 获取所有 Gemini 模型
func (mr *ModelRegistry) GetGeminiModels() []*ModelInfo {
	return mr.GetModelsByProvider(ProviderGemini)
}

// InitializeDefaultModels 初始化默认模型
func (mr *ModelRegistry) InitializeDefaultModels() {
	// Claude 模型
	mr.RegisterModel(&ModelInfo{
		ID:              "claude-opus-4-6",
		Name:            "claude-opus-4-6",
		DisplayName:     "Claude Opus 4.6",
		Description:     "最强大的 Claude 模型，适合复杂任务和深度分析",
		Provider:        ProviderClaude,
		MaxTokens:       200000,
		ContextWindow:   200000,
		CostPer1KInput:  0.015,
		CostPer1KOutput: 0.075,
		Enabled:         true,
		CreatedAt:       "2026-02-20T00:00:00Z",
		UpdatedAt:       "2026-02-20T00:00:00Z",
	})

	mr.RegisterModel(&ModelInfo{
		ID:              "claude-sonnet-4-6",
		Name:            "claude-sonnet-4-6",
		DisplayName:     "Claude Sonnet 4.6",
		Description:     "平衡性能和成本的 Claude 模型，推荐用于大多数任务",
		Provider:        ProviderClaude,
		MaxTokens:       200000,
		ContextWindow:   200000,
		CostPer1KInput:  0.003,
		CostPer1KOutput: 0.015,
		Enabled:         true,
		CreatedAt:       "2026-02-20T00:00:00Z",
		UpdatedAt:       "2026-02-20T00:00:00Z",
	})

	mr.RegisterModel(&ModelInfo{
		ID:              "claude-haiku-4-5-20251001",
		Name:            "claude-haiku-4-5-20251001",
		DisplayName:     "Claude Haiku 4.5",
		Description:     "快速且经济的 Claude 模型，适合简单任务",
		Provider:        ProviderClaude,
		MaxTokens:       200000,
		ContextWindow:   200000,
		CostPer1KInput:  0.0008,
		CostPer1KOutput: 0.004,
		Enabled:         true,
		CreatedAt:       "2026-02-20T00:00:00Z",
		UpdatedAt:       "2026-02-20T00:00:00Z",
	})

	// Gemini 模型
	mr.RegisterModel(&ModelInfo{
		ID:              "gemini-3.1-pro-high",
		Name:            "gemini-3.1-pro-high",
		DisplayName:     "Gemini 3.1 Pro High",
		Description:     "高精度的 Gemini 模型，适合需要高准确度的任务",
		Provider:        ProviderGemini,
		MaxTokens:       1000000,
		ContextWindow:   1000000,
		CostPer1KInput:  0.0075,
		CostPer1KOutput: 0.03,
		Enabled:         true,
		CreatedAt:       "2026-02-20T00:00:00Z",
		UpdatedAt:       "2026-02-20T00:00:00Z",
	})

	mr.RegisterModel(&ModelInfo{
		ID:              "gemini-3.1-pro",
		Name:            "gemini-3.1-pro",
		DisplayName:     "Gemini 3.1 Pro",
		Description:     "标准的 Gemini 模型，推荐用于大多数任务",
		Provider:        ProviderGemini,
		MaxTokens:       1000000,
		ContextWindow:   1000000,
		CostPer1KInput:  0.0075,
		CostPer1KOutput: 0.03,
		Enabled:         true,
		CreatedAt:       "2026-02-20T00:00:00Z",
		UpdatedAt:       "2026-02-20T00:00:00Z",
	})

	mr.RegisterModel(&ModelInfo{
		ID:              "gemini-3.1-flash",
		Name:            "gemini-3.1-flash",
		DisplayName:     "Gemini 3.1 Flash",
		Description:     "快速的 Gemini 模型，适合需要快速响应的任务",
		Provider:        ProviderGemini,
		MaxTokens:       1000000,
		ContextWindow:   1000000,
		CostPer1KInput:  0.0075,
		CostPer1KOutput: 0.03,
		Enabled:         true,
		CreatedAt:       "2026-02-20T00:00:00Z",
		UpdatedAt:       "2026-02-20T00:00:00Z",
	})
}
