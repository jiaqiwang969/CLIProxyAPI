package console

import (
	"testing"
)

// TestConsoleManager 测试 ConsoleManager
func TestConsoleManager(t *testing.T) {
	cm := NewConsoleManager()

	// 测试初始状态
	stats := cm.GetStats()
	if stats.TotalTokens != 100000 {
		t.Errorf("Expected total tokens 100000, got %d", stats.TotalTokens)
	}

	// 测试记录日志
	cm.RecordLog("POST", "/v1/chat/completions", "claude-sonnet-4-6", 200, 1000, 1200)

	stats = cm.GetStats()
	if stats.APICallCount != 1235 {
		t.Errorf("Expected API call count 1235, got %d", stats.APICallCount)
	}

	if stats.UsedTokens != 66432 {
		t.Errorf("Expected used tokens 66432, got %d", stats.UsedTokens)
	}

	// 测试获取日志
	logs := cm.GetLogs(10)
	if len(logs) == 0 {
		t.Error("Expected logs, got empty")
	}

	// 测试创建 API 密钥
	key := cm.CreateAPIKey("test-key", "test description")
	if key.Name != "test-key" {
		t.Errorf("Expected key name 'test-key', got %s", key.Name)
	}

	// 测试获取 API 密钥
	keys := cm.GetAPIKeys()
	if len(keys) == 0 {
		t.Error("Expected keys, got empty")
	}

	// 测试删除 API 密钥
	err := cm.DeleteAPIKey(key.ID)
	if err != nil {
		t.Errorf("Failed to delete key: %v", err)
	}

	keys = cm.GetAPIKeys()
	if len(keys) != 0 {
		t.Errorf("Expected 0 keys after deletion, got %d", len(keys))
	}

	// 测试更新密钥最后使用时间
	key2 := cm.CreateAPIKey("test-key-2", "test")
	cm.UpdateKeyLastUsed(key2.ID)
	keys = cm.GetAPIKeys()
	if keys[0].LastUsed.IsZero() {
		t.Error("Expected last used time to be updated")
	}

	// 测试获取使用趋势
	trend := cm.GetUsageTrend(7)
	if trend["labels"] == nil {
		t.Error("Expected labels in trend data")
	}

	// 测试导出统计数据
	data, err := cm.ExportStats()
	if err != nil {
		t.Errorf("Failed to export stats: %v", err)
	}
	if len(data) == 0 {
		t.Error("Expected exported data, got empty")
	}
}

// TestConcurrency 测试并发访问
func TestConcurrency(t *testing.T) {
	cm := NewConsoleManager()

	// 并发记录日志
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func(index int) {
			cm.RecordLog("POST", "/v1/chat/completions", "claude-sonnet-4-6", 200, int64(index*100), 1200)
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	stats := cm.GetStats()
	if stats.APICallCount != 1334 {
		t.Errorf("Expected API call count 1334, got %d", stats.APICallCount)
	}
}

// TestLogLimit 测试日志数量限制
func TestLogLimit(t *testing.T) {
	cm := NewConsoleManager()
	cm.maxLogs = 10

	// 记录超过限制的日志
	for i := 0; i < 20; i++ {
		cm.RecordLog("POST", "/v1/chat/completions", "claude-sonnet-4-6", 200, 100, 1200)
	}

	logs := cm.GetLogs(100)
	if len(logs) != 10 {
		t.Errorf("Expected 10 logs (max limit), got %d", len(logs))
	}
}

// BenchmarkRecordLog 基准测试：记录日志
func BenchmarkRecordLog(b *testing.B) {
	cm := NewConsoleManager()
	for i := 0; i < b.N; i++ {
		cm.RecordLog("POST", "/v1/chat/completions", "claude-sonnet-4-6", 200, 1000, 1200)
	}
}

// BenchmarkGetStats 基准测试：获取统计信息
func BenchmarkGetStats(b *testing.B) {
	cm := NewConsoleManager()
	for i := 0; i < b.N; i++ {
		cm.GetStats()
	}
}

// BenchmarkGetLogs 基准测试：获取日志
func BenchmarkGetLogs(b *testing.B) {
	cm := NewConsoleManager()
	// 预先记录一些日志
	for i := 0; i < 100; i++ {
		cm.RecordLog("POST", "/v1/chat/completions", "claude-sonnet-4-6", 200, 1000, 1200)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.GetLogs(50)
	}
}
