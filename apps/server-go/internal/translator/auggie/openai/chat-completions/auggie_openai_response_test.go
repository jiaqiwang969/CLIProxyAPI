package chat_completions

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertAuggieResponseToOpenAI_EmitsTextAndFinishReason(t *testing.T) {
	var param any

	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		[]byte(`{"text":"hello","stop_reason":"end_turn","nodes":[{"ignored":true}]}`),
		&param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], `"content":"hello"`) {
		t.Fatalf("expected content delta in %s", lines[0])
	}
	if !strings.Contains(lines[0], `"finish_reason":"stop"`) {
		t.Fatalf("expected finish_reason stop in %s", lines[0])
	}
	if !strings.Contains(lines[0], `"native_finish_reason":"end_turn"`) {
		t.Fatalf("expected native_finish_reason end_turn in %s", lines[0])
	}
}

func TestConvertAuggieResponseToOpenAI_FallsBackToNodeContentWhenTopLevelTextIsEmpty(t *testing.T) {
	var param any

	first := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		[]byte(`{"text":"I'll create a Snake game in HTML with embedded CSS and JavaScript."}`),
		&param,
	)
	if len(first) != 1 {
		t.Fatalf("first chunk lines = %d, want 1", len(first))
	}
	if got := gjson.Get(first[0], "choices.0.delta.content").String(); got == "" {
		t.Fatalf("first chunk content is empty: %s", first[0])
	}

	second := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		[]byte(`{"text":"","nodes":[{"id":2,"type":2,"content":"\n<canvas id=\"game\"></canvas>\n","tool_use":null,"thinking":null,"token_usage":null}]}`),
		&param,
	)
	if len(second) != 1 {
		t.Fatalf("second chunk lines = %d, want 1", len(second))
	}
	if got := gjson.Get(second[0], "choices.0.delta.content").String(); !strings.Contains(got, "<canvas id=\"game\"></canvas>") {
		t.Fatalf("second chunk content = %q, want node content fallback; chunk=%s", got, second[0])
	}

	third := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		[]byte(`{"text":"","stop_reason":"end_turn"}`),
		&param,
	)
	if len(third) != 1 {
		t.Fatalf("third chunk lines = %d, want 1", len(third))
	}
	if got := gjson.Get(third[0], "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("third chunk finish_reason = %q, want stop; chunk=%s", got, third[0])
	}
}

func TestConvertAuggieResponseToOpenAI_ExtractsToolUseFromNodes(t *testing.T) {
	var param any

	raw := []byte(`{"text":"","stop_reason":"tool_use","nodes":[{"tool_use":{"id":"tooluse_abc123","name":"get_weather","input":{"location":"San Francisco"}}}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw, &param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	line := lines[0]

	// Should have tool_calls in delta
	tc := gjson.Get(line, "choices.0.delta.tool_calls")
	if !tc.Exists() || !tc.IsArray() {
		t.Fatalf("expected tool_calls array in delta, got: %s", line)
	}
	if tc.Get("#").Int() != 1 {
		t.Fatalf("expected 1 tool_call, got %d: %s", tc.Get("#").Int(), line)
	}

	first := tc.Get("0")
	if got := first.Get("id").String(); got != "tooluse_abc123" {
		t.Fatalf("tool_call id = %q, want tooluse_abc123", got)
	}
	if got := first.Get("type").String(); got != "function" {
		t.Fatalf("tool_call type = %q, want function", got)
	}
	if got := first.Get("function.name").String(); got != "get_weather" {
		t.Fatalf("function.name = %q, want get_weather", got)
	}
	if got := first.Get("function.arguments").String(); !strings.Contains(got, "San Francisco") {
		t.Fatalf("function.arguments = %q, want to contain San Francisco", got)
	}

	// finish_reason should be tool_calls
	if got := gjson.Get(line, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
}

func TestConvertAuggieResponseToOpenAI_SkipsNullToolUseInNodes(t *testing.T) {
	var param any

	// nodes with null tool_use should be ignored; text is empty, stop_reason is empty → return nil
	raw := []byte(`{"text":"","nodes":[{"tool_use":null},{"tool_use":null}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw, &param,
	)

	if lines != nil {
		t.Fatalf("expected nil for null tool_use nodes, got %v", lines)
	}
}

func TestConvertAuggieResponseToOpenAI_GeneratesIDWhenMissing(t *testing.T) {
	var param any

	raw := []byte(`{"text":"","stop_reason":"tool_use","nodes":[{"tool_use":{"name":"read_file","input":{"path":"/tmp"}}}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw, &param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}

	tc := gjson.Get(lines[0], "choices.0.delta.tool_calls.0")
	id := tc.Get("id").String()
	if id == "" {
		t.Fatal("expected generated tool_call id, got empty")
	}
	if !strings.HasPrefix(id, "call_read_file_") {
		t.Fatalf("generated id = %q, want prefix call_read_file_", id)
	}
}

func TestConvertAuggieResponseToOpenAI_MultipleToolUsesInSingleChunk(t *testing.T) {
	var param any

	raw := []byte(`{"text":"","stop_reason":"tool_use","nodes":[{"tool_use":{"id":"tc1","name":"func_a","input":{"x":1}}},{"tool_use":{"id":"tc2","name":"func_b","input":{"y":2}}}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw, &param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}

	tc := gjson.Get(lines[0], "choices.0.delta.tool_calls")
	if tc.Get("#").Int() != 2 {
		t.Fatalf("expected 2 tool_calls, got %d: %s", tc.Get("#").Int(), lines[0])
	}
	if got := tc.Get("0.function.name").String(); got != "func_a" {
		t.Fatalf("first tool name = %q, want func_a", got)
	}
	if got := tc.Get("0.index").Int(); got != 0 {
		t.Fatalf("first tool index = %d, want 0", got)
	}
	if got := tc.Get("1.function.name").String(); got != "func_b" {
		t.Fatalf("second tool name = %q, want func_b", got)
	}
	if got := tc.Get("1.index").Int(); got != 1 {
		t.Fatalf("second tool index = %d, want 1", got)
	}
}

func TestConvertAuggieResponseToOpenAI_SawToolCallForcesFinishReason(t *testing.T) {
	var param any

	// First chunk: tool_use
	raw1 := []byte(`{"text":"","nodes":[{"tool_use":{"id":"tc1","name":"get_weather","input":{"loc":"NYC"}}}]}`)
	lines1 := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw1, &param,
	)
	if len(lines1) != 1 {
		t.Fatalf("first chunk: lines = %d, want 1", len(lines1))
	}

	// Second chunk: stop_reason=end_turn (should be overridden to tool_calls)
	raw2 := []byte(`{"text":"","stop_reason":"end_turn"}`)
	lines2 := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw2, &param,
	)
	if len(lines2) != 1 {
		t.Fatalf("second chunk: lines = %d, want 1", len(lines2))
	}
	if got := gjson.Get(lines2[0], "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls (should override end_turn)", got)
	}
}

func TestConvertAuggieResponseToOpenAI_NumericStopReason3MapsToToolCalls(t *testing.T) {
	var param any

	// Auggie upstream returns stop_reason as numeric 3 for tool_use
	raw := []byte(`{"text":"","nodes":[{"tool_use":{"id":"tooluse_abc","name":"get_weather","input":{"location":"SF"}}},{"id":0,"type":10,"token_usage":{"input_tokens":310,"output_tokens":31}}],"stop_reason":3}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw, &param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}

	line := lines[0]
	if got := gjson.Get(line, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls for numeric stop_reason 3", got)
	}
	tc := gjson.Get(line, "choices.0.delta.tool_calls")
	if !tc.Exists() || !tc.IsArray() {
		t.Fatalf("expected tool_calls array, got: %s", line)
	}
	if got := tc.Get("0.function.name").String(); got != "get_weather" {
		t.Fatalf("function.name = %q, want get_weather", got)
	}
}

func TestConvertAuggieResponseToOpenAI_NumericStopReason1MapsToStop(t *testing.T) {
	var param any

	raw := []byte(`{"text":"done","stop_reason":1}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw, &param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if got := gjson.Get(lines[0], "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop for numeric stop_reason 1", got)
	}
}

func TestConvertAuggieResponseToOpenAI_MaxOutputTokensStopReasonMapsToLength(t *testing.T) {
	var param any

	for _, raw := range [][]byte{
		[]byte(`{"text":"partial","stop_reason":"max_tokens"}`),
		[]byte(`{"text":"partial","stop_reason":"max_output_tokens"}`),
		[]byte(`{"text":"partial","stop_reason":2}`),
	} {
		lines := ConvertAuggieResponseToOpenAI(
			context.Background(),
			"gpt-5.4",
			nil, nil, raw, &param,
		)

		if len(lines) != 1 {
			t.Fatalf("lines = %d, want 1 for raw=%s", len(lines), raw)
		}
		if got := gjson.Get(lines[0], "choices.0.finish_reason").String(); got != "length" {
			t.Fatalf("finish_reason = %q, want length for raw=%s", got, raw)
		}
	}
}

func TestConvertAuggieResponseToOpenAI_NumericStopReason3WithoutToolUseInSameChunk(t *testing.T) {
	var param any

	// First chunk has tool_use
	raw1 := []byte(`{"text":"","nodes":[{"tool_use":{"id":"tc1","name":"read_file","input":{"path":"/tmp"}}}]}`)
	lines1 := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw1, &param,
	)
	if len(lines1) != 1 {
		t.Fatalf("first chunk: lines = %d, want 1", len(lines1))
	}

	// Second chunk has numeric stop_reason 3 but no tool_use (usage node only)
	raw2 := []byte(`{"text":"","nodes":[{"id":0,"type":10,"token_usage":{"input_tokens":100,"output_tokens":20}}],"stop_reason":3}`)
	lines2 := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil, nil, raw2, &param,
	)
	if len(lines2) != 1 {
		t.Fatalf("second chunk: lines = %d, want 1", len(lines2))
	}
	if got := gjson.Get(lines2[0], "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
}

func TestConvertAuggieResponseToOpenAI_RealAuggieToolUseFields(t *testing.T) {
	var param any

	// Real auggie upstream uses tool_use_id / tool_name / input_json
	raw := []byte(`{"text":"","nodes":[{"id":1,"type":5,"content":"","tool_use":{"tool_use_id":"toolu_vrtx_015RWVLh2gFoJvie9htx9e1T","tool_name":"get_weather","input_json":"{\"location\": \"San Francisco\"}","is_partial":false},"thinking":null,"billing_metadata":null,"metadata":null,"token_usage":null}],"stop_reason":null}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"claude-sonnet-4-6",
		nil, nil, raw, &param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	line := lines[0]

	tc := gjson.Get(line, "choices.0.delta.tool_calls")
	if !tc.Exists() || !tc.IsArray() {
		t.Fatalf("expected tool_calls array, got: %s", line)
	}
	first := tc.Get("0")
	if got := first.Get("id").String(); got != "toolu_vrtx_015RWVLh2gFoJvie9htx9e1T" {
		t.Fatalf("tool_call id = %q, want toolu_vrtx_015RWVLh2gFoJvie9htx9e1T", got)
	}
	if got := first.Get("function.name").String(); got != "get_weather" {
		t.Fatalf("function.name = %q, want get_weather", got)
	}
	if got := first.Get("function.arguments").String(); !strings.Contains(got, "San Francisco") {
		t.Fatalf("function.arguments = %q, want to contain San Francisco", got)
	}
}

func TestConvertAuggieResponseToOpenAI_RealAuggieFullSequence(t *testing.T) {
	var param any

	// Simulate real auggie 3-chunk sequence: tool_use → token_usage → stop_reason:3
	chunk1 := []byte(`{"text":"","nodes":[{"id":1,"type":5,"content":"","tool_use":{"tool_use_id":"toolu_abc","tool_name":"get_weather","input_json":"{\"location\":\"NYC\"}","is_partial":false},"thinking":null,"billing_metadata":null,"metadata":null,"token_usage":null}],"stop_reason":null}`)
	chunk2 := []byte(`{"text":"","nodes":[{"id":1,"type":10,"content":"","tool_use":null,"thinking":null,"billing_metadata":null,"metadata":null,"token_usage":{"input_tokens":500,"output_tokens":30,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}],"stop_reason":null}`)
	chunk3 := []byte(`{"text":"","nodes":[{"id":4,"type":2,"content":"","tool_use":null,"thinking":null,"billing_metadata":null,"metadata":null,"token_usage":null}],"stop_reason":3}`)

	lines1 := ConvertAuggieResponseToOpenAI(context.Background(), "claude-sonnet-4-6", nil, nil, chunk1, &param)
	if len(lines1) != 1 {
		t.Fatalf("chunk1: lines = %d, want 1", len(lines1))
	}
	tc := gjson.Get(lines1[0], "choices.0.delta.tool_calls.0")
	if got := tc.Get("function.name").String(); got != "get_weather" {
		t.Fatalf("chunk1: function.name = %q, want get_weather", got)
	}
	if got := tc.Get("function.arguments").String(); !strings.Contains(got, "NYC") {
		t.Fatalf("chunk1: function.arguments = %q, want to contain NYC", got)
	}

	lines2 := ConvertAuggieResponseToOpenAI(context.Background(), "claude-sonnet-4-6", nil, nil, chunk2, &param)
	if len(lines2) != 1 {
		t.Fatalf("chunk2: lines = %d, want 1", len(lines2))
	}
	if got := gjson.Get(lines2[0], "usage.prompt_tokens").Int(); got != 500 {
		t.Fatalf("chunk2: usage.prompt_tokens = %d, want 500", got)
	}

	lines3 := ConvertAuggieResponseToOpenAI(context.Background(), "claude-sonnet-4-6", nil, nil, chunk3, &param)
	if len(lines3) != 1 {
		t.Fatalf("chunk3: lines = %d, want 1", len(lines3))
	}
	if got := gjson.Get(lines3[0], "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("chunk3: finish_reason = %q, want tool_calls", got)
	}
}

func TestConvertAuggieResponseToOpenAI_EmitsReasoningContentFromThinkingNodes(t *testing.T) {
	var param any

	raw := []byte(`{"text":"","nodes":[{"id":1,"type":9,"thinking":{"content":"Need to inspect the tool result before answering."}}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		raw,
		&param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if got := gjson.Get(lines[0], "choices.0.delta.reasoning_content").String(); got != "Need to inspect the tool result before answering." {
		t.Fatalf("reasoning_content = %q, want thinking text; line=%s", got, lines[0])
	}
}

func TestConvertAuggieResponseToOpenAI_IncludesEncryptedReasoningWhenRequested(t *testing.T) {
	var param any

	raw := []byte(`{"text":"","nodes":[{"id":1,"type":9,"thinking":{"content":"Need to inspect the tool result before answering.","encrypted_content":"enc:auggie:123"}}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		[]byte(`{"model":"gpt-5.4","include":["reasoning.encrypted_content"]}`),
		nil,
		raw,
		&param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if got := gjson.Get(lines[0], "choices.0.delta.reasoning_content").String(); got != "Need to inspect the tool result before answering." {
		t.Fatalf("reasoning_content = %q, want thinking text; line=%s", got, lines[0])
	}
	if got := gjson.Get(lines[0], "choices.0.delta.reasoning_encrypted_content").String(); got != "enc:auggie:123" {
		t.Fatalf("reasoning_encrypted_content = %q, want enc:auggie:123; line=%s", got, lines[0])
	}
}

func TestConvertAuggieResponseToOpenAI_PreservesReasoningItemIDFromThinkingNodes(t *testing.T) {
	var param any

	raw := []byte(`{"text":"","nodes":[{"id":1,"type":9,"thinking":{"content":"Need to inspect the tool result before answering.","openai_responses_api_item_id":"rs_native_1"}}]}`)
	lines := ConvertAuggieResponseToOpenAI(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		raw,
		&param,
	)

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if got := gjson.Get(lines[0], "choices.0.delta.reasoning_item_id").String(); got != "rs_native_1" {
		t.Fatalf("reasoning_item_id = %q, want rs_native_1; line=%s", got, lines[0])
	}
}
