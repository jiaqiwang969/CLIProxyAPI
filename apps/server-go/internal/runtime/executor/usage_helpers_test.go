package executor

import "testing"

func TestParseOpenAIUsageChatCompletions(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":5}}}`)
	detail := parseOpenAIUsage(data)
	if detail.InputTokens != 1 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 1)
	}
	if detail.OutputTokens != 2 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 2)
	}
	if detail.TotalTokens != 3 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 3)
	}
	if detail.CachedTokens != 4 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 4)
	}
	if detail.ReasoningTokens != 5 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 5)
	}
}

func TestParseOpenAIUsageResponses(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":9}}}`)
	detail := parseOpenAIUsage(data)
	if detail.InputTokens != 10 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 10)
	}
	if detail.OutputTokens != 20 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 20)
	}
	if detail.TotalTokens != 30 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 30)
	}
	if detail.CachedTokens != 7 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 7)
	}
	if detail.ReasoningTokens != 9 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 9)
	}
}

func TestParseAuggieUsageNodeTokenUsage(t *testing.T) {
	data := []byte(`{
		"text":"",
		"nodes":[
			{
				"id":0,
				"type":10,
				"token_usage":{
					"input_tokens":310,
					"output_tokens":31,
					"cache_read_input_tokens":2
				}
			}
		],
		"stop_reason":1
	}`)

	detail, ok := parseAuggieUsage(data)
	if !ok {
		t.Fatal("parseAuggieUsage() ok = false, want true")
	}
	if detail.InputTokens != 310 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 310)
	}
	if detail.OutputTokens != 31 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 31)
	}
	if detail.CachedTokens != 2 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 2)
	}
}

func TestSanityCheckResponse_AllowsStructuredErrorPayload(t *testing.T) {
	t.Parallel()

	data := []byte(`{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest"}]}}`)
	if err := SanityCheckResponse(data); err != nil {
		t.Fatalf("SanityCheckResponse() error = %v", err)
	}
}
