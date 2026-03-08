package chat_completions

import (
	"context"
	"strings"
	"testing"
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
