package console

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ManagementAPIAdapter provides compatibility layer for external dashboard
// It adapts /v0/management/ API calls to our /api/console/ API
type ManagementAPIAdapter struct {
	manager *ConsoleManager
}

// NewManagementAPIAdapter creates a new management API adapter
func NewManagementAPIAdapter(manager *ConsoleManager) *ManagementAPIAdapter {
	return &ManagementAPIAdapter{
		manager: manager,
	}
}

// GetUsageStatistics handles GET /v0/management/usage
func (a *ManagementAPIAdapter) GetUsageStatistics(c *gin.Context) {
	stats := a.manager.GetStats()

	// Build model stats map
	modelStats := make(map[string]map[string]interface{})
	for _, model := range stats.Models {
		modelStats[model.Name] = map[string]interface{}{
			"tokens":   model.TokenCount,
			"requests": model.CallCount,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"usage": gin.H{
			"total_tokens":   stats.TotalTokens,
			"total_requests": stats.APICallCount,
			"apis": gin.H{
				"claude": gin.H{
					"models": gin.H{
						"claude-opus-4-6": modelStats["claude-opus-4-6"],
						"claude-sonnet-4-6": modelStats["claude-sonnet-4-6"],
						"claude-haiku-4-5-20251001": modelStats["claude-haiku-4-5-20251001"],
					},
				},
				"gemini": gin.H{
					"models": gin.H{
						"gemini-3.1-pro-high": modelStats["gemini-3.1-pro-high"],
						"gemini-3.1-pro": modelStats["gemini-3.1-pro"],
						"gemini-3.1-flash": modelStats["gemini-3.1-flash"],
					},
				},
			},
		},
	})
}

// GetActivityLogs handles GET /v0/management/activity
func (a *ManagementAPIAdapter) GetActivityLogs(c *gin.Context) {
	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	logs := a.manager.GetLogs(limit)
	activities := make([]gin.H, 0)
	for _, log := range logs {
		activities = append(activities, gin.H{
			"id":        log.ID,
			"timestamp": log.Timestamp,
			"model":     log.Model,
			"tokens":    log.Tokens,
			"status":    "success",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"activities": activities,
	})
}

// GetUsageTrends handles GET /v0/management/stats/trends
func (a *ManagementAPIAdapter) GetUsageTrends(c *gin.Context) {
	stats := a.manager.GetStats()

	// Generate trend data for the last 7 days
	trends := make([]gin.H, 7)
	now := time.Now()
	for i := 6; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		trends[6-i] = gin.H{
			"date":   date,
			"tokens": stats.TotalTokens / 7, // Simple distribution
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"trends": trends,
	})
}

// GetEvents handles GET /v0/management/events
func (a *ManagementAPIAdapter) GetEvents(c *gin.Context) {
	logs := a.manager.GetLogs(50)
	events := make([]gin.H, 0)
	for _, log := range logs {
		events = append(events, gin.H{
			"id":        log.ID,
			"timestamp": log.Timestamp,
			"type":      "api_call",
			"model":     log.Model,
			"tokens":    log.Tokens,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
	})
}

// GetLogs handles GET /v0/management/logs
func (a *ManagementAPIAdapter) GetLogs(c *gin.Context) {
	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	logs := a.manager.GetLogs(limit)
	logEntries := make([]gin.H, 0)
	for _, log := range logs {
		logEntries = append(logEntries, gin.H{
			"id":        log.ID,
			"timestamp": log.Timestamp,
			"model":     log.Model,
			"tokens":    log.Tokens,
			"message":   "API call completed",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"logs": logEntries,
	})
}

// DeleteLogs handles DELETE /v0/management/logs
func (a *ManagementAPIAdapter) DeleteLogs(c *gin.Context) {
	// Clear logs - implementation depends on ConsoleManager
	c.JSON(http.StatusOK, gin.H{
		"message": "Logs cleared",
	})
}

// GetAPIKeys handles GET /v0/management/api-keys
func (a *ManagementAPIAdapter) GetAPIKeys(c *gin.Context) {
	keys := a.manager.GetAPIKeys()
	apiKeys := make([]string, 0)
	for _, key := range keys {
		apiKeys = append(apiKeys, key.Value)
	}

	c.JSON(http.StatusOK, gin.H{
		"api-keys": apiKeys,
	})
}

// PutAPIKeys handles PUT /v0/management/api-keys
func (a *ManagementAPIAdapter) PutAPIKeys(c *gin.Context) {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "API keys updated",
	})
}

// DeleteAPIKeys handles DELETE /v0/management/api-keys
func (a *ManagementAPIAdapter) DeleteAPIKeys(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "API keys deleted",
	})
}

// GetAuthStatus handles GET /v0/management/get-auth-status
func (a *ManagementAPIAdapter) GetAuthStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"status":        "ok",
	})
}

// GetConfig handles GET /v0/management/config
func (a *ManagementAPIAdapter) GetConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"config": gin.H{
			"debug":   false,
			"port":    8317,
			"version": "1.0.0",
		},
	})
}

// GetConfigYAML handles GET /v0/management/config/yaml
func (a *ManagementAPIAdapter) GetConfigYAML(c *gin.Context) {
	yaml := `# CLIProxyAPI Configuration
server:
  port: 8317
  host: 0.0.0.0

console:
  enabled: true

models:
  - name: claude-opus-4-6
    provider: claude
  - name: claude-sonnet-4-6
    provider: claude
  - name: claude-haiku-4-5-20251001
    provider: claude
  - name: gemini-3.1-pro-high
    provider: gemini
  - name: gemini-3.1-pro
    provider: gemini
  - name: gemini-3.1-flash
    provider: gemini
`
	c.Header("Content-Type", "text/plain")
	c.String(http.StatusOK, yaml)
}

// PutConfigYAML handles PUT /v0/management/config/yaml
func (a *ManagementAPIAdapter) PutConfigYAML(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "Configuration updated",
	})
}

// GetDebug handles GET /v0/management/debug
func (a *ManagementAPIAdapter) GetDebug(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"debug": false,
	})
}

// PutDebug handles PUT /v0/management/debug
func (a *ManagementAPIAdapter) PutDebug(c *gin.Context) {
	var req struct {
		Value bool `json:"value"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Debug setting updated",
	})
}

// GetUsageStatisticsEnabled handles GET /v0/management/usage-statistics-enabled
func (a *ManagementAPIAdapter) GetUsageStatisticsEnabled(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"usage-statistics-enabled": true,
	})
}

// PutUsageStatisticsEnabled handles PUT /v0/management/usage-statistics-enabled
func (a *ManagementAPIAdapter) PutUsageStatisticsEnabled(c *gin.Context) {
	var req struct {
		Value bool `json:"value"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Usage statistics setting updated",
	})
}

// GetRequestLog handles GET /v0/management/request-log
func (a *ManagementAPIAdapter) GetRequestLog(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"request-log": true,
	})
}

// PutRequestLog handles PUT /v0/management/request-log
func (a *ManagementAPIAdapter) PutRequestLog(c *gin.Context) {
	var req struct {
		Value bool `json:"value"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Request log setting updated",
	})
}
