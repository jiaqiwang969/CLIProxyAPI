package responses

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func parseClaudeResponsesSSEEvent(t *testing.T, chunk string) (string, gjson.Result) {
	t.Helper()

	lines := strings.Split(chunk, "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected SSE chunk: %q", chunk)
	}

	event := strings.TrimSpace(strings.TrimPrefix(lines[0], "event:"))
	dataLine := strings.TrimSpace(strings.TrimPrefix(lines[1], "data:"))
	if !gjson.Valid(dataLine) {
		t.Fatalf("invalid SSE data JSON: %q", dataLine)
	}
	return event, gjson.Parse(dataLine)
}

func assertClaudeOfficialDefaultResponseScaffold(t *testing.T, payload gjson.Result, prefix string, wantModel string) {
	t.Helper()

	path := func(field string) string {
		if prefix == "" {
			return field
		}
		return prefix + "." + field
	}

	if got := payload.Get(path("instructions")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("instructions"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("max_output_tokens")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("max_output_tokens"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("model")).String(); got != wantModel {
		t.Fatalf("%s = %q, want %q; payload=%s", path("model"), got, wantModel, payload.Raw)
	}
	if !payload.Get(path("parallel_tool_calls")).Bool() {
		t.Fatalf("%s missing/false; payload=%s", path("parallel_tool_calls"), payload.Raw)
	}
	if got := payload.Get(path("previous_response_id")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("previous_response_id"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("reasoning.effort")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("reasoning.effort"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("reasoning.summary")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("reasoning.summary"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("store")); !got.Exists() || !got.Bool() {
		t.Fatalf("%s = %s, want true; payload=%s", path("store"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("temperature")).Float(); got != 1 {
		t.Fatalf("%s = %v, want 1; payload=%s", path("temperature"), got, payload.Raw)
	}
	if got := payload.Get(path("text.format.type")).String(); got != "text" {
		t.Fatalf("%s = %q, want text; payload=%s", path("text.format.type"), got, payload.Raw)
	}
	if got := payload.Get(path("tool_choice")).String(); got != "auto" {
		t.Fatalf("%s = %q, want auto; payload=%s", path("tool_choice"), got, payload.Raw)
	}
	if got := payload.Get(path("tools.#")).Int(); got != 0 {
		t.Fatalf("%s = %d, want 0; payload=%s", path("tools.#"), got, payload.Raw)
	}
	if got := payload.Get(path("top_p")).Float(); got != 1 {
		t.Fatalf("%s = %v, want 1; payload=%s", path("top_p"), got, payload.Raw)
	}
	if got := payload.Get(path("truncation")).String(); got != "disabled" {
		t.Fatalf("%s = %q, want disabled; payload=%s", path("truncation"), got, payload.Raw)
	}
	if got := payload.Get(path("user")); got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("user"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("metadata")); got.Type != gjson.JSON || got.Raw != "{}" {
		t.Fatalf("%s = %s, want {}; payload=%s", path("metadata"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("usage")); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("%s = %s, want null; payload=%s", path("usage"), got.Raw, payload.Raw)
	}
	if got := payload.Get(path("output_text")); got.Exists() {
		t.Fatalf("%s = %s, want field omitted; payload=%s", path("output_text"), got.Raw, payload.Raw)
	}
}

func TestConvertClaudeResponseToOpenAIResponses_StreamSequenceStartsAtZero(t *testing.T) {
	var param any

	chunks := ConvertClaudeResponseToOpenAIResponses(
		context.Background(),
		"claude-test",
		nil,
		nil,
		[]byte(`data: {"type":"message_start","message":{"id":"msg_seq_1","usage":{"input_tokens":1,"output_tokens":0}}}`),
		&param,
	)

	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want at least 2; chunks=%v", len(chunks), chunks)
	}

	createdEvent, created := parseClaudeResponsesSSEEvent(t, chunks[0])
	if createdEvent != "response.created" {
		t.Fatalf("first event = %q, want response.created; chunk=%s", createdEvent, chunks[0])
	}
	if got := created.Get("sequence_number").Int(); got != 0 {
		t.Fatalf("response.created sequence_number = %d, want 0; chunk=%s", got, created.Raw)
	}

	inProgressEvent, inProgress := parseClaudeResponsesSSEEvent(t, chunks[1])
	if inProgressEvent != "response.in_progress" {
		t.Fatalf("second event = %q, want response.in_progress; chunk=%s", inProgressEvent, chunks[1])
	}
	if got := inProgress.Get("sequence_number").Int(); got != 1 {
		t.Fatalf("response.in_progress sequence_number = %d, want 1; chunk=%s", got, inProgress.Raw)
	}
}

func TestConvertClaudeResponseToOpenAIResponses_StreamCompletedEchoesPromptFields(t *testing.T) {
	request := []byte(`{
		"model":"gpt-5",
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_retention":"24h"
	}`)

	in := []string{
		`data: {"type":"message_start","message":{"id":"msg_prompt_1","usage":{"input_tokens":1,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_stop"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertClaudeResponseToOpenAIResponses(context.Background(), "claude-test", request, nil, []byte(line), &param)...)
	}

	var completed gjson.Result
	for _, chunk := range out {
		ev, data := parseClaudeResponsesSSEEvent(t, chunk)
		if ev == "response.completed" {
			completed = data
			break
		}
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event")
	}
	if got := completed.Get("response.prompt.id").String(); got != "pmpt_test" {
		t.Fatalf("response.prompt.id = %q, want pmpt_test; event=%s", got, completed.Raw)
	}
	if got := completed.Get("response.prompt.version").String(); got != "3" {
		t.Fatalf("response.prompt.version = %q, want 3; event=%s", got, completed.Raw)
	}
	if got := completed.Get("response.prompt.variables.city").String(); got != "Boston" {
		t.Fatalf("response.prompt.variables.city = %q, want Boston; event=%s", got, completed.Raw)
	}
	if got := completed.Get("response.prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("response.prompt_cache_retention = %q, want 24h; event=%s", got, completed.Raw)
	}
}

func TestConvertClaudeResponseToOpenAIResponsesNonStream_EchoesPromptFields(t *testing.T) {
	request := []byte(`{
		"model":"gpt-5",
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_retention":"24h"
	}`)

	resp := ConvertClaudeResponseToOpenAIResponsesNonStream(
		context.Background(),
		"claude-test",
		request,
		nil,
		[]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_prompt_nonstream","usage":{"input_tokens":1,"output_tokens":0}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_stop"}`,
		}, "\n")),
		nil,
	)

	parsed := gjson.Parse(resp)
	if got := parsed.Get("prompt.id").String(); got != "pmpt_test" {
		t.Fatalf("prompt.id = %q, want pmpt_test; resp=%s", got, resp)
	}
	if got := parsed.Get("prompt.version").String(); got != "3" {
		t.Fatalf("prompt.version = %q, want 3; resp=%s", got, resp)
	}
	if got := parsed.Get("prompt.variables.city").String(); got != "Boston" {
		t.Fatalf("prompt.variables.city = %q, want Boston; resp=%s", got, resp)
	}
	if got := parsed.Get("prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("prompt_cache_retention = %q, want 24h; resp=%s", got, resp)
	}
}

func TestConvertClaudeResponseToOpenAIResponses_StreamLifecycleUsesOfficialDefaultResponseScaffold(t *testing.T) {
	in := []string{
		`data: {"type":"message_start","message":{"id":"msg_default_scaffold"}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_stop"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertClaudeResponseToOpenAIResponses(context.Background(), "claude-test", []byte(`{"model":"gpt-5"}`), nil, []byte(line), &param)...)
	}

	var (
		created    gjson.Result
		inProgress gjson.Result
		completed  gjson.Result
	)
	for _, chunk := range out {
		ev, data := parseClaudeResponsesSSEEvent(t, chunk)
		switch ev {
		case "response.created":
			created = data
		case "response.in_progress":
			inProgress = data
		case "response.completed":
			completed = data
		}
	}

	if !created.Exists() {
		t.Fatalf("missing response.created event")
	}
	assertClaudeOfficialDefaultResponseScaffold(t, created, "response", "gpt-5")
	if got := created.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("response.created completed_at = %s, want null; event=%s", got.Raw, created.Raw)
	}
	if got := created.Get("response.output.#").Int(); got != 0 {
		t.Fatalf("response.created output count = %d, want 0; event=%s", got, created.Raw)
	}

	if !inProgress.Exists() {
		t.Fatalf("missing response.in_progress event")
	}
	assertClaudeOfficialDefaultResponseScaffold(t, inProgress, "response", "gpt-5")
	if got := inProgress.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("response.in_progress completed_at = %s, want null; event=%s", got.Raw, inProgress.Raw)
	}
	if got := inProgress.Get("response.output.#").Int(); got != 0 {
		t.Fatalf("response.in_progress output count = %d, want 0; event=%s", got, inProgress.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event")
	}
	assertClaudeOfficialDefaultResponseScaffold(t, completed, "response", "gpt-5")
	if got := completed.Get("response.completed_at").Int(); got <= 0 {
		t.Fatalf("response.completed completed_at = %d, want > 0; event=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.#").Int(); got != 1 {
		t.Fatalf("response.completed output count = %d, want 1; event=%s", got, completed.Raw)
	}
}

func TestConvertClaudeResponseToOpenAIResponsesNonStream_UsesOfficialDefaultResponseScaffold(t *testing.T) {
	resp := ConvertClaudeResponseToOpenAIResponsesNonStream(
		context.Background(),
		"claude-test",
		[]byte(`{"model":"gpt-5"}`),
		nil,
		[]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_default_nonstream"}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_stop"}`,
		}, "\n")),
		nil,
	)

	parsed := gjson.Parse(resp)
	assertClaudeOfficialDefaultResponseScaffold(t, parsed, "", "gpt-5")
	if got := parsed.Get("completed_at").Int(); got <= 0 {
		t.Fatalf("completed_at = %d, want > 0; resp=%s", got, resp)
	}
	if got := parsed.Get("output.#").Int(); got != 1 {
		t.Fatalf("output count = %d, want 1; resp=%s", got, resp)
	}
}
