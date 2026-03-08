package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
)

func TestAuthMiddleware_StoresScopedAccessFieldsInContext(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{stubContextProvider{}})

	router := gin.New()
	router.Use(AuthMiddleware(manager))
	router.GET("/check", func(c *gin.Context) {
		if got, ok := c.Get("apiKey"); !ok || got != "sk-test" {
			t.Fatalf("apiKey = %#v, ok=%v", got, ok)
		}
		if got, ok := c.Get("accessScopeProvider"); !ok || got != "auggie" {
			t.Fatalf("accessScopeProvider = %#v, ok=%v", got, ok)
		}
		if got, ok := c.Get("accessScopeAuthID"); !ok || got != "auggie-main" {
			t.Fatalf("accessScopeAuthID = %#v, ok=%v", got, ok)
		}
		if got, ok := c.Get("accessScopeModels"); !ok || got != "gpt-5-4,claude-sonnet-4-6" {
			t.Fatalf("accessScopeModels = %#v, ok=%v", got, ok)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/check", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNoContent)
	}
}

type stubContextProvider struct{}

func (stubContextProvider) Identifier() string { return "stub" }

func (stubContextProvider) Authenticate(_ context.Context, _ *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	return &sdkaccess.Result{
		Provider:  "stub",
		Principal: "sk-test",
		Metadata: map[string]string{
			"scope_provider": "auggie",
			"scope_auth_id":  "auggie-main",
			"scope_models":   "gpt-5-4,claude-sonnet-4-6",
		},
	}, nil
}
