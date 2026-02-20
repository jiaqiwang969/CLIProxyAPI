package console

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TokenStats Token 使用统计
type TokenStats struct {
	TotalTokens     int64   `json:"total_tokens"`
	UsedTokens      int64   `json:"used_tokens"`
	RemainingTokens int64   `json:"remaining_tokens"`
	UsagePercent    float64 `json:"usage_percent"`
	APICallCount    int64   `json:"api_call_count"`
	Models          []ModelStats `json:"models"`
}

// ModelStats 模型统计
type ModelStats struct {
	Name        string `json:"name"`
	CallCount   int64  `json:"call_count"`
	TokenCount  int64  `json:"token_count"`
	AvgTime     int64  `json:"avg_time"`
	SuccessRate float64 `json:"success_rate"`
}

// APILog API 调用日志
type APILog struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Method    string    `json:"method"`
	Endpoint  string    `json:"endpoint"`
	Status    int       `json:"status"`
	Tokens    int64     `json:"tokens"`
	Duration  int64     `json:"duration"`
	Model     string    `json:"model"`
	Error     string    `json:"error,omitempty"`
}

// APIKey API 密钥
type APIKey struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
	Enabled   bool      `json:"enabled"`
}

// ConsoleManager Token 看板管理器
type ConsoleManager struct {
	mu              sync.RWMutex
	stats           *TokenStats
	logs            []APILog
	keys            []APIKey
	maxLogs         int
	logID           int64
	keyID           int64
}

// NewConsoleManager 创建新的看板管理器
func NewConsoleManager() *ConsoleManager {
	return &ConsoleManager{
		stats: &TokenStats{
			TotalTokens:     100000,
			UsedTokens:      65432,
			RemainingTokens: 34568,
			UsagePercent:    65.43,
			APICallCount:    1234,
			Models: []ModelStats{
				{
					Name:        "claude-sonnet-4-6",
					CallCount:   456,
					TokenCount:  23456,
					AvgTime:     1200,
					SuccessRate: 99.8,
				},
				{
					Name:        "claude-opus-4-6",
					CallCount:   234,
					TokenCount:  18765,
					AvgTime:     1500,
					SuccessRate: 99.9,
				},
				{
					Name:        "gemini-3.1-pro",
					CallCount:   345,
					TokenCount:  15234,
					AvgTime:     800,
					SuccessRate: 99.5,
				},
			},
		},
		logs:    make([]APILog, 0),
		keys:    make([]APIKey, 0),
		maxLogs: 1000,
		logID:   0,
		keyID:   0,
	}
}

// GetStats 获取统计信息
func (cm *ConsoleManager) GetStats() *TokenStats {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 深拷贝
	stats := &TokenStats{
		TotalTokens:     cm.stats.TotalTokens,
		UsedTokens:      cm.stats.UsedTokens,
		RemainingTokens: cm.stats.RemainingTokens,
		UsagePercent:    cm.stats.UsagePercent,
		APICallCount:    cm.stats.APICallCount,
		Models:          make([]ModelStats, len(cm.stats.Models)),
	}
	copy(stats.Models, cm.stats.Models)
	return stats
}

// RecordLog 记录 API 调用日志
func (cm *ConsoleManager) RecordLog(method, endpoint, model string, status int, tokens, duration int64) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.logID++
	log := APILog{
		ID:        cm.logID,
		Timestamp: time.Now(),
		Method:    method,
		Endpoint:  endpoint,
		Status:    status,
		Tokens:    tokens,
		Duration:  duration,
		Model:     model,
	}

	cm.logs = append(cm.logs, log)

	// 保持日志数量在限制内
	if len(cm.logs) > cm.maxLogs {
		cm.logs = cm.logs[1:]
	}

	// 更新统计信息
	cm.stats.APICallCount++
	cm.stats.UsedTokens += tokens
	cm.stats.RemainingTokens = cm.stats.TotalTokens - cm.stats.UsedTokens
	cm.stats.UsagePercent = float64(cm.stats.UsedTokens) / float64(cm.stats.TotalTokens) * 100

	// 更新模型统计
	for i, m := range cm.stats.Models {
		if m.Name == model {
			cm.stats.Models[i].CallCount++
			cm.stats.Models[i].TokenCount += tokens
			break
		}
	}
}

// GetLogs 获取日志列表
func (cm *ConsoleManager) GetLogs(limit int) []APILog {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if limit <= 0 || limit > len(cm.logs) {
		limit = len(cm.logs)
	}

	// 返回最新的日志
	start := len(cm.logs) - limit
	if start < 0 {
		start = 0
	}

	result := make([]APILog, limit)
	copy(result, cm.logs[start:])

	// 反转顺序，最新的在前
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

// CreateAPIKey 创建 API 密钥
func (cm *ConsoleManager) CreateAPIKey(name, description string) *APIKey {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.keyID++
	key := &APIKey{
		ID:        cm.keyID,
		Name:      name,
		Value:     fmt.Sprintf("sk-prod-%d-%d", time.Now().Unix(), cm.keyID),
		CreatedAt: time.Now(),
		LastUsed:  time.Time{},
		Enabled:   true,
	}

	cm.keys = append(cm.keys, *key)
	return key
}

// GetAPIKeys 获取所有 API 密钥
func (cm *ConsoleManager) GetAPIKeys() []APIKey {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]APIKey, len(cm.keys))
	copy(result, cm.keys)
	return result
}

// DeleteAPIKey 删除 API 密钥
func (cm *ConsoleManager) DeleteAPIKey(id int64) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, key := range cm.keys {
		if key.ID == id {
			cm.keys = append(cm.keys[:i], cm.keys[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("key not found")
}

// UpdateKeyLastUsed 更新密钥最后使用时间
func (cm *ConsoleManager) UpdateKeyLastUsed(id int64) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, key := range cm.keys {
		if key.ID == id {
			cm.keys[i].LastUsed = time.Now()
			break
		}
	}
}

// GetUsageTrend 获取使用趋势数据
func (cm *ConsoleManager) GetUsageTrend(days int) map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 生成示例数据
	labels := []string{}
	tokenData := []int64{}
	callData := []int64{}

	now := time.Now()
	for i := days - 1; i >= 0; i-- {
		date := now.AddDate(0, 0, -i)
		labels = append(labels, date.Format("01-02"))

		// 模拟数据
		tokenData = append(tokenData, int64(7000+i*1000))
		callData = append(callData, int64(100+i*10))
	}

	return map[string]interface{}{
		"labels":      labels,
		"tokenData":   tokenData,
		"callData":    callData,
	}
}

// ExportStats 导出统计数据为 JSON
func (cm *ConsoleManager) ExportStats() ([]byte, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	data := map[string]interface{}{
		"stats": cm.stats,
		"logs":  cm.logs,
		"keys":  cm.keys,
	}

	return json.MarshalIndent(data, "", "  ")
}
