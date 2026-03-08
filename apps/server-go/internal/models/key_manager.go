package models

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// APIKey API 密钥信息
type APIKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Secret    string    `json:"secret,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// KeyManager API 密钥管理器
type KeyManager struct {
	mu   sync.RWMutex
	keys map[string]*APIKey
}

// NewKeyManager 创建新的密钥管理器
func NewKeyManager() *KeyManager {
	return &KeyManager{
		keys: make(map[string]*APIKey),
	}
}

// GenerateKey 生成新的 API 密钥
func (km *KeyManager) GenerateKey(name string, expiresIn time.Duration) (*APIKey, error) {
	km.mu.Lock()
	defer km.mu.Unlock()

	// 生成密钥
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	keyStr := "sk-" + hex.EncodeToString(keyBytes)

	// 生成密钥 ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("failed to generate id: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	now := time.Now()
	expiresAt := time.Time{}
	if expiresIn > 0 {
		expiresAt = now.Add(expiresIn)
	}

	key := &APIKey{
		ID:        id,
		Name:      name,
		Key:       keyStr,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
	}

	km.keys[id] = key
	return key, nil
}

// GetKey 获取密钥信息
func (km *KeyManager) GetKey(id string) *APIKey {
	km.mu.RLock()
	defer km.mu.RUnlock()

	key, exists := km.keys[id]
	if !exists {
		return nil
	}

	// 检查是否过期
	if !key.ExpiresAt.IsZero() && key.ExpiresAt.Before(time.Now()) {
		return nil
	}

	return key
}

// ValidateKey 验证密钥
func (km *KeyManager) ValidateKey(keyStr string) (*APIKey, error) {
	km.mu.RLock()
	defer km.mu.RUnlock()

	for _, key := range km.keys {
		if key.Key == keyStr && key.Enabled {
			// 检查是否过期
			if !key.ExpiresAt.IsZero() && key.ExpiresAt.Before(time.Now()) {
				return nil, fmt.Errorf("key expired")
			}
			return key, nil
		}
	}

	return nil, fmt.Errorf("invalid key")
}

// ListKeys 列出所有密钥
func (km *KeyManager) ListKeys() []*APIKey {
	km.mu.RLock()
	defer km.mu.RUnlock()

	var result []*APIKey
	for _, key := range km.keys {
		result = append(result, key)
	}
	return result
}

// DeleteKey 删除密钥
func (km *KeyManager) DeleteKey(id string) error {
	km.mu.Lock()
	defer km.mu.Unlock()

	if _, exists := km.keys[id]; !exists {
		return fmt.Errorf("key not found")
	}

	delete(km.keys, id)
	return nil
}

// DisableKey 禁用密钥
func (km *KeyManager) DisableKey(id string) error {
	km.mu.Lock()
	defer km.mu.Unlock()

	key, exists := km.keys[id]
	if !exists {
		return fmt.Errorf("key not found")
	}

	key.Enabled = false
	key.UpdatedAt = time.Now()
	return nil
}

// EnableKey 启用密钥
func (km *KeyManager) EnableKey(id string) error {
	km.mu.Lock()
	defer km.mu.Unlock()

	key, exists := km.keys[id]
	if !exists {
		return fmt.Errorf("key not found")
	}

	key.Enabled = true
	key.UpdatedAt = time.Now()
	return nil
}

// UpdateKeyLastUsed 更新密钥最后使用时间
func (km *KeyManager) UpdateKeyLastUsed(id string) {
	km.mu.Lock()
	defer km.mu.Unlock()

	if key, exists := km.keys[id]; exists {
		key.LastUsed = time.Now()
	}
}

// RevokeKey 撤销密钥
func (km *KeyManager) RevokeKey(id string) error {
	return km.DisableKey(id)
}

// CreateDefaultKey 创建默认密钥
func (km *KeyManager) CreateDefaultKey() (*APIKey, error) {
	return km.GenerateKey("default", 0)
}
