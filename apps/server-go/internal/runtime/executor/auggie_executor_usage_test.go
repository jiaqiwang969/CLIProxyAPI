package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

func TestAuggieExecute_RecordsUsageRequestForCanonicalModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	internalusage.SetStatisticsEnabled(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, `{"text":"hello"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":" world","stop_reason":"end_turn"}`)
		flusher.Flush()
		_, _ = fmt.Fprintln(w, `{"text":"","nodes":[{"id":0,"type":10,"content":"","tool_use":null,"thinking":null,"billing_metadata":null,"metadata":null,"token_usage":{"input_tokens":310,"output_tokens":31,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}],"stop_reason":1}`)
		flusher.Flush()
	}))
	defer server.Close()

	apiKey := "test-auggie-usage-key"
	before := internalusage.GetRequestStatistics().Snapshot()

	ctx := newAuggieUsageTestContext(apiKey)
	auth := newAuggieStreamTestAuth("token-1")
	auth.Metadata[AuggieShortNameAliasesMetadataKey] = map[string]any{
		"gpt-5.4": "gpt-5-4",
	}
	if _, err := executeAuggieNonStreamForTest(t, ctx, auth, server.URL); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	after := waitForUsageSnapshot(t, func(snapshot internalusage.StatisticsSnapshot) bool {
		return usageModelRequests(snapshot, apiKey, "gpt-5-4")-usageModelRequests(before, apiKey, "gpt-5-4") == 1 &&
			usageModelTokens(snapshot, apiKey, "gpt-5-4")-usageModelTokens(before, apiKey, "gpt-5-4") > 0
	})
	if got := usageModelRequests(after, apiKey, "gpt-5-4") - usageModelRequests(before, apiKey, "gpt-5-4"); got != 1 {
		t.Fatalf("canonical model request delta = %d, want 1; before=%+v after=%+v", got, before.APIs[apiKey], after.APIs[apiKey])
	}
	if got := usageModelTokens(after, apiKey, "gpt-5-4") - usageModelTokens(before, apiKey, "gpt-5-4"); got != 341 {
		t.Fatalf("canonical model token delta = %d, want 341; before=%+v after=%+v", got, before.APIs[apiKey], after.APIs[apiKey])
	}
}

func newAuggieUsageTestContext(apiKey string) context.Context {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Set("apiKey", apiKey)
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func usageModelRequests(snapshot internalusage.StatisticsSnapshot, apiKey, model string) int64 {
	apiStats, ok := snapshot.APIs[apiKey]
	if !ok {
		return 0
	}
	modelStats, ok := apiStats.Models[model]
	if !ok {
		return 0
	}
	return modelStats.TotalRequests
}

func usageModelTokens(snapshot internalusage.StatisticsSnapshot, apiKey, model string) int64 {
	apiStats, ok := snapshot.APIs[apiKey]
	if !ok {
		return 0
	}
	modelStats, ok := apiStats.Models[model]
	if !ok {
		return 0
	}
	return modelStats.TotalTokens
}

func waitForUsageSnapshot(t *testing.T, ready func(internalusage.StatisticsSnapshot) bool) internalusage.StatisticsSnapshot {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := internalusage.GetRequestStatistics().Snapshot()
		if ready(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}

	return internalusage.GetRequestStatistics().Snapshot()
}
