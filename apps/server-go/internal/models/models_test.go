package models

import (
	"testing"
)

// TestModelRegistry 测试模型注册表
func TestModelRegistry(t *testing.T) {
	registry := NewModelRegistry()
	registry.InitializeDefaultModels()

	// 测试获取所有模型
	allModels := registry.GetAllModels()
	if len(allModels) != 6 {
		t.Errorf("Expected 6 models, got %d", len(allModels))
	}

	// 测试获取 Claude 模型
	claudeModels := registry.GetClaudeModels()
	if len(claudeModels) != 3 {
		t.Errorf("Expected 3 Claude models, got %d", len(claudeModels))
	}

	// 测试获取 Gemini 模型
	geminiModels := registry.GetGeminiModels()
	if len(geminiModels) != 3 {
		t.Errorf("Expected 3 Gemini models, got %d", len(geminiModels))
	}

	// 测试获取单个模型
	model := registry.GetModel("claude-opus-4-6")
	if model == nil {
		t.Error("Expected model, got nil")
	}
	if model.DisplayName != "Claude Opus 4.6" {
		t.Errorf("Expected display name 'Claude Opus 4.6', got %s", model.DisplayName)
	}
}

// TestKeyManager 测试密钥管理器
func TestKeyManager(t *testing.T) {
	km := NewKeyManager()

	// 测试生成密钥
	key, err := km.GenerateKey("test-key", 0)
	if err != nil {
		t.Errorf("Failed to generate key: %v", err)
	}
	if key.Name != "test-key" {
		t.Errorf("Expected key name 'test-key', got %s", key.Name)
	}

	// 测试获取密钥
	retrievedKey := km.GetKey(key.ID)
	if retrievedKey == nil {
		t.Error("Expected key, got nil")
	}

	// 测试验证密钥
	validatedKey, err := km.ValidateKey(key.Key)
	if err != nil {
		t.Errorf("Failed to validate key: %v", err)
	}
	if validatedKey.ID != key.ID {
		t.Errorf("Expected key ID %s, got %s", key.ID, validatedKey.ID)
	}

	// 测试列出密钥
	keys := km.ListKeys()
	if len(keys) != 1 {
		t.Errorf("Expected 1 key, got %d", len(keys))
	}

	// 测试禁用密钥
	err = km.DisableKey(key.ID)
	if err != nil {
		t.Errorf("Failed to disable key: %v", err)
	}

	// 验证禁用后无法验证
	_, err = km.ValidateKey(key.Key)
	if err == nil {
		t.Error("Expected error for disabled key")
	}

	// 测试启用密钥
	err = km.EnableKey(key.ID)
	if err != nil {
		t.Errorf("Failed to enable key: %v", err)
	}

	// 测试删除密钥
	err = km.DeleteKey(key.ID)
	if err != nil {
		t.Errorf("Failed to delete key: %v", err)
	}

	// 验证删除后无法获取
	retrievedKey = km.GetKey(key.ID)
	if retrievedKey != nil {
		t.Error("Expected nil after deletion")
	}
}

// TestDefaultKey 测试默认密钥
func TestDefaultKey(t *testing.T) {
	km := NewKeyManager()

	// 创建默认密钥
	key, err := km.CreateDefaultKey()
	if err != nil {
		t.Errorf("Failed to create default key: %v", err)
	}
	if key.Name != "default" {
		t.Errorf("Expected key name 'default', got %s", key.Name)
	}
}

// BenchmarkGenerateKey 基准测试：生成密钥
func BenchmarkGenerateKey(b *testing.B) {
	km := NewKeyManager()
	for i := 0; i < b.N; i++ {
		km.GenerateKey("test-key", 0)
	}
}

// BenchmarkValidateKey 基准测试：验证密钥
func BenchmarkValidateKey(b *testing.B) {
	km := NewKeyManager()
	key, _ := km.GenerateKey("test-key", 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		km.ValidateKey(key.Key)
	}
}
