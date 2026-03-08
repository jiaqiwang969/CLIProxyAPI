package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestRequestExecutionMetadata_PinsScopedAuthIDFromGinContext(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Set("accessScopeAuthID", "auggie-main")

	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	meta := requestExecutionMetadata(ctx)

	if got := meta[coreexecutor.PinnedAuthMetadataKey]; got != "auggie-main" {
		t.Fatalf("PinnedAuthMetadataKey = %#v, want %q", got, "auggie-main")
	}
}

func TestGetRequestDetailsForContext_RestrictsScopedProviderAndAuth(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	reg := registry.GetGlobalRegistry()
	const scopedAuthID = "handlers-scope-auggie"
	const otherAuthID = "handlers-scope-antigravity"
	reg.UnregisterClient(scopedAuthID)
	reg.UnregisterClient(otherAuthID)
	t.Cleanup(func() {
		reg.UnregisterClient(scopedAuthID)
		reg.UnregisterClient(otherAuthID)
	})

	reg.RegisterClient(scopedAuthID, "auggie", []*registry.ModelInfo{
		{ID: "gpt-5-4", DisplayName: "GPT-5.4", Version: "gpt-5-4"},
	})
	reg.RegisterClient(otherAuthID, "antigravity", []*registry.ModelInfo{
		{ID: "gemini-3.1-pro-preview"},
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	ctx := newScopedHandlerContext(t, "auggie", scopedAuthID)

	providers, model, errMsg := handler.getRequestDetailsForContext(ctx, "gpt-5.4")
	if errMsg != nil {
		t.Fatalf("getRequestDetailsForContext() unexpected error = %v", errMsg)
	}
	if len(providers) != 1 || providers[0] != "auggie" {
		t.Fatalf("providers = %v, want [auggie]", providers)
	}
	if model != "gpt-5.4" {
		t.Fatalf("model = %q, want %q", model, "gpt-5.4")
	}

	_, _, denied := handler.getRequestDetailsForContext(ctx, "gemini-3.1-pro-preview")
	if denied == nil {
		t.Fatal("expected scoped auth to reject unsupported model")
	}
	if denied.StatusCode != 403 {
		t.Fatalf("status = %d, want %d", denied.StatusCode, 403)
	}
}

func newScopedHandlerContext(t *testing.T, provider, authID string) context.Context {
	t.Helper()

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	if provider != "" {
		ginCtx.Set("accessScopeProvider", provider)
	}
	if authID != "" {
		ginCtx.Set("accessScopeAuthID", authID)
	}
	return context.WithValue(context.Background(), "gin", ginCtx)
}
