package responses

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func parseSSEEvent(t *testing.T, chunk string) (string, gjson.Result) {
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

func assertGeminiOfficialDefaultResponseScaffold(t *testing.T, payload gjson.Result, prefix string, wantModel string) {
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

func TestConvertGeminiResponseToOpenAIResponses_UnwrapAndAggregateText(t *testing.T) {
	// Vertex-style Gemini stream wraps the actual response payload under "response".
	// This test ensures we unwrap and that output_text.done contains the full text.
	in := []string{
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"让"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"我先"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"了解"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"mcp__serena__list_dir","args":{"recursive":false,"relative_path":"internal"},"id":"toolu_1"}}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15,"cachedContentTokenCount":2},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
	}

	originalReq := []byte(`{"instructions":"test instructions","model":"gpt-5","max_output_tokens":123}`)

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertGeminiResponseToOpenAIResponses(context.Background(), "test-model", originalReq, nil, []byte(line), &param)...)
	}

	var (
		gotTextDone     bool
		gotMessageDone  bool
		gotResponseDone bool
		gotFuncDone     bool

		textDone     string
		messageText  string
		responseID   string
		instructions string
		cachedTokens int64

		funcName string
		funcArgs string

		posTextDone    = -1
		posPartDone    = -1
		posMessageDone = -1
		posFuncAdded   = -1
	)

	for i, chunk := range out {
		ev, data := parseSSEEvent(t, chunk)
		switch ev {
		case "response.output_text.done":
			gotTextDone = true
			if posTextDone == -1 {
				posTextDone = i
			}
			textDone = data.Get("text").String()
		case "response.content_part.done":
			if posPartDone == -1 {
				posPartDone = i
			}
		case "response.output_item.done":
			switch data.Get("item.type").String() {
			case "message":
				gotMessageDone = true
				if posMessageDone == -1 {
					posMessageDone = i
				}
				messageText = data.Get("item.content.0.text").String()
			case "function_call":
				gotFuncDone = true
				funcName = data.Get("item.name").String()
				funcArgs = data.Get("item.arguments").String()
			}
		case "response.output_item.added":
			if data.Get("item.type").String() == "function_call" && posFuncAdded == -1 {
				posFuncAdded = i
			}
		case "response.completed":
			gotResponseDone = true
			responseID = data.Get("response.id").String()
			instructions = data.Get("response.instructions").String()
			cachedTokens = data.Get("response.usage.input_tokens_details.cached_tokens").Int()
		}
	}

	if !gotTextDone {
		t.Fatalf("missing response.output_text.done event")
	}
	if posTextDone == -1 || posPartDone == -1 || posMessageDone == -1 || posFuncAdded == -1 {
		t.Fatalf("missing ordering events: textDone=%d partDone=%d messageDone=%d funcAdded=%d", posTextDone, posPartDone, posMessageDone, posFuncAdded)
	}
	if !(posTextDone < posPartDone && posPartDone < posMessageDone && posMessageDone < posFuncAdded) {
		t.Fatalf("unexpected message/function ordering: textDone=%d partDone=%d messageDone=%d funcAdded=%d", posTextDone, posPartDone, posMessageDone, posFuncAdded)
	}
	if !gotMessageDone {
		t.Fatalf("missing message response.output_item.done event")
	}
	if !gotFuncDone {
		t.Fatalf("missing function_call response.output_item.done event")
	}
	if !gotResponseDone {
		t.Fatalf("missing response.completed event")
	}

	if textDone != "让我先了解" {
		t.Fatalf("unexpected output_text.done text: got %q", textDone)
	}
	if messageText != "让我先了解" {
		t.Fatalf("unexpected message done text: got %q", messageText)
	}

	if responseID != "resp_req_vrtx_1" {
		t.Fatalf("unexpected response id: got %q", responseID)
	}
	if instructions != "test instructions" {
		t.Fatalf("unexpected instructions echo: got %q", instructions)
	}
	if cachedTokens != 2 {
		t.Fatalf("unexpected cached token count: got %d", cachedTokens)
	}

	if funcName != "mcp__serena__list_dir" {
		t.Fatalf("unexpected function name: got %q", funcName)
	}
	if !gjson.Valid(funcArgs) {
		t.Fatalf("invalid function arguments JSON: %q", funcArgs)
	}
	if gjson.Get(funcArgs, "recursive").Bool() != false {
		t.Fatalf("unexpected recursive arg: %v", gjson.Get(funcArgs, "recursive").Value())
	}
	if gjson.Get(funcArgs, "relative_path").String() != "internal" {
		t.Fatalf("unexpected relative_path arg: %q", gjson.Get(funcArgs, "relative_path").String())
	}
}

func TestConvertGeminiResponseToOpenAIResponses_ReasoningEncryptedContent(t *testing.T) {
	sig := "RXE0RENrZ0lDeEFDR0FJcVFOZDdjUzlleGFuRktRdFcvSzNyZ2MvWDNCcDQ4RmxSbGxOWUlOVU5kR1l1UHMrMGdkMVp0Vkg3ekdKU0g4YVljc2JjN3lNK0FrdGpTNUdqamI4T3Z0VVNETzdQd3pmcFhUOGl3U3hXUEJvTVFRQ09mWTFyMEtTWGZxUUlJakFqdmFGWk83RW1XRlBKckJVOVpkYzdDKw=="
	in := []string{
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"thoughtSignature":"` + sig + `","text":""}]}}],"modelVersion":"test-model","responseId":"req_vrtx_sig"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"a"}]}}],"modelVersion":"test-model","responseId":"req_vrtx_sig"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"modelVersion":"test-model","responseId":"req_vrtx_sig"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"modelVersion":"test-model","responseId":"req_vrtx_sig"},"traceId":"t1"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertGeminiResponseToOpenAIResponses(context.Background(), "test-model", nil, nil, []byte(line), &param)...)
	}

	var (
		addedEnc string
		doneEnc  string
	)
	for _, chunk := range out {
		ev, data := parseSSEEvent(t, chunk)
		switch ev {
		case "response.output_item.added":
			if data.Get("item.type").String() == "reasoning" {
				addedEnc = data.Get("item.encrypted_content").String()
			}
		case "response.output_item.done":
			if data.Get("item.type").String() == "reasoning" {
				doneEnc = data.Get("item.encrypted_content").String()
			}
		}
	}

	if addedEnc != sig {
		t.Fatalf("unexpected encrypted_content in response.output_item.added: got %q", addedEnc)
	}
	if doneEnc != sig {
		t.Fatalf("unexpected encrypted_content in response.output_item.done: got %q", doneEnc)
	}
}

func TestConvertGeminiResponseToOpenAIResponses_StreamSequenceStartsAtZero(t *testing.T) {
	var param any

	chunks := ConvertGeminiResponseToOpenAIResponses(
		context.Background(),
		"test-model",
		nil,
		nil,
		[]byte(`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"modelVersion":"test-model","responseId":"req_vrtx_seq_1"},"traceId":"t1"}`),
		&param,
	)

	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want at least 2; chunks=%v", len(chunks), chunks)
	}

	createdEvent, created := parseSSEEvent(t, chunks[0])
	if createdEvent != "response.created" {
		t.Fatalf("first event = %q, want response.created; chunk=%s", createdEvent, chunks[0])
	}
	if got := created.Get("sequence_number").Int(); got != 0 {
		t.Fatalf("response.created sequence_number = %d, want 0; chunk=%s", got, created.Raw)
	}

	inProgressEvent, inProgress := parseSSEEvent(t, chunks[1])
	if inProgressEvent != "response.in_progress" {
		t.Fatalf("second event = %q, want response.in_progress; chunk=%s", inProgressEvent, chunks[1])
	}
	if got := inProgress.Get("sequence_number").Int(); got != 1 {
		t.Fatalf("response.in_progress sequence_number = %d, want 1; chunk=%s", got, inProgress.Raw)
	}
}

func TestConvertGeminiResponseToOpenAIResponses_StreamCompletedEchoesPromptFields(t *testing.T) {
	request := []byte(`{
		"model":"gpt-5",
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_retention":"24h"
	}`)

	in := []string{
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"modelVersion":"test-model","responseId":"req_vrtx_prompt"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"modelVersion":"test-model","responseId":"req_vrtx_prompt"},"traceId":"t1"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertGeminiResponseToOpenAIResponses(context.Background(), "test-model", request, nil, []byte(line), &param)...)
	}

	var completed gjson.Result
	for _, chunk := range out {
		ev, data := parseSSEEvent(t, chunk)
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

func TestConvertGeminiResponseToOpenAIResponsesNonStream_EchoesPromptFields(t *testing.T) {
	request := []byte(`{
		"model":"gpt-5",
		"prompt":{"id":"pmpt_test","version":"3","variables":{"city":"Boston"}},
		"prompt_cache_retention":"24h"
	}`)

	resp := ConvertGeminiResponseToOpenAIResponsesNonStream(
		context.Background(),
		"test-model",
		request,
		nil,
		[]byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"modelVersion":"test-model","responseId":"req_vrtx_prompt_nonstream"}}`),
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

func TestConvertGeminiResponseToOpenAIResponses_StreamLifecycleUsesOfficialDefaultResponseScaffold(t *testing.T) {
	request := []byte(`{"model":"gpt-5"}`)
	in := []string{
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"modelVersion":"test-model","responseId":"req_vrtx_default_scaffold"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"modelVersion":"test-model","responseId":"req_vrtx_default_scaffold"},"traceId":"t1"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertGeminiResponseToOpenAIResponses(context.Background(), "test-model", request, nil, []byte(line), &param)...)
	}

	var (
		created    gjson.Result
		inProgress gjson.Result
		completed  gjson.Result
	)
	for _, chunk := range out {
		ev, data := parseSSEEvent(t, chunk)
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
	assertGeminiOfficialDefaultResponseScaffold(t, created, "response", "gpt-5")
	if got := created.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("response.created completed_at = %s, want null; event=%s", got.Raw, created.Raw)
	}
	if got := created.Get("response.output.#").Int(); got != 0 {
		t.Fatalf("response.created output count = %d, want 0; event=%s", got, created.Raw)
	}

	if !inProgress.Exists() {
		t.Fatalf("missing response.in_progress event")
	}
	assertGeminiOfficialDefaultResponseScaffold(t, inProgress, "response", "gpt-5")
	if got := inProgress.Get("response.completed_at"); !got.Exists() || got.Type != gjson.Null {
		t.Fatalf("response.in_progress completed_at = %s, want null; event=%s", got.Raw, inProgress.Raw)
	}
	if got := inProgress.Get("response.output.#").Int(); got != 0 {
		t.Fatalf("response.in_progress output count = %d, want 0; event=%s", got, inProgress.Raw)
	}

	if !completed.Exists() {
		t.Fatalf("missing response.completed event")
	}
	assertGeminiOfficialDefaultResponseScaffold(t, completed, "response", "gpt-5")
	if got := completed.Get("response.completed_at").Int(); got <= 0 {
		t.Fatalf("response.completed completed_at = %d, want > 0; event=%s", got, completed.Raw)
	}
	if got := completed.Get("response.output.#").Int(); got != 1 {
		t.Fatalf("response.completed output count = %d, want 1; event=%s", got, completed.Raw)
	}
}

func TestConvertGeminiResponseToOpenAIResponsesNonStream_UsesOfficialDefaultResponseScaffold(t *testing.T) {
	resp := ConvertGeminiResponseToOpenAIResponsesNonStream(
		context.Background(),
		"test-model",
		[]byte(`{"model":"gpt-5"}`),
		nil,
		[]byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"modelVersion":"test-model","responseId":"req_vrtx_default_nonstream"}}`),
		nil,
	)

	parsed := gjson.Parse(resp)
	assertGeminiOfficialDefaultResponseScaffold(t, parsed, "", "gpt-5")
	if got := parsed.Get("completed_at").Int(); got <= 0 {
		t.Fatalf("completed_at = %d, want > 0; resp=%s", got, resp)
	}
	if got := parsed.Get("output.#").Int(); got != 1 {
		t.Fatalf("output count = %d, want 1; resp=%s", got, resp)
	}
}

func TestConvertGeminiResponseToOpenAIResponses_FunctionCallEventOrder(t *testing.T) {
	in := []string{
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"tool0"}}]}}],"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"tool1"}}]}}],"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"tool2","args":{"a":1}}}]}}],"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_1"},"traceId":"t1"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertGeminiResponseToOpenAIResponses(context.Background(), "test-model", nil, nil, []byte(line), &param)...)
	}

	posAdded := []int{-1, -1, -1}
	posArgsDelta := []int{-1, -1, -1}
	posArgsDone := []int{-1, -1, -1}
	posItemDone := []int{-1, -1, -1}
	posCompleted := -1
	deltaByIndex := map[int]string{}

	for i, chunk := range out {
		ev, data := parseSSEEvent(t, chunk)
		switch ev {
		case "response.output_item.added":
			if data.Get("item.type").String() != "function_call" {
				continue
			}
			idx := int(data.Get("output_index").Int())
			if idx >= 0 && idx < len(posAdded) {
				posAdded[idx] = i
			}
		case "response.function_call_arguments.delta":
			idx := int(data.Get("output_index").Int())
			if idx >= 0 && idx < len(posArgsDelta) {
				posArgsDelta[idx] = i
				deltaByIndex[idx] = data.Get("delta").String()
			}
		case "response.function_call_arguments.done":
			idx := int(data.Get("output_index").Int())
			if idx >= 0 && idx < len(posArgsDone) {
				posArgsDone[idx] = i
			}
		case "response.output_item.done":
			if data.Get("item.type").String() != "function_call" {
				continue
			}
			idx := int(data.Get("output_index").Int())
			if idx >= 0 && idx < len(posItemDone) {
				posItemDone[idx] = i
			}
		case "response.completed":
			posCompleted = i

			output := data.Get("response.output")
			if !output.Exists() || !output.IsArray() {
				t.Fatalf("missing response.output in response.completed")
			}
			if len(output.Array()) != 3 {
				t.Fatalf("unexpected response.output length: got %d", len(output.Array()))
			}
			if data.Get("response.output.0.name").String() != "tool0" || data.Get("response.output.0.arguments").String() != "{}" {
				t.Fatalf("unexpected output[0]: %s", data.Get("response.output.0").Raw)
			}
			if data.Get("response.output.1.name").String() != "tool1" || data.Get("response.output.1.arguments").String() != "{}" {
				t.Fatalf("unexpected output[1]: %s", data.Get("response.output.1").Raw)
			}
			if data.Get("response.output.2.name").String() != "tool2" {
				t.Fatalf("unexpected output[2] name: %s", data.Get("response.output.2").Raw)
			}
			if !gjson.Valid(data.Get("response.output.2.arguments").String()) {
				t.Fatalf("unexpected output[2] arguments: %q", data.Get("response.output.2.arguments").String())
			}
		}
	}

	if posCompleted == -1 {
		t.Fatalf("missing response.completed event")
	}
	for idx := 0; idx < 3; idx++ {
		if posAdded[idx] == -1 || posArgsDelta[idx] == -1 || posArgsDone[idx] == -1 || posItemDone[idx] == -1 {
			t.Fatalf("missing function call events for output_index %d: added=%d argsDelta=%d argsDone=%d itemDone=%d", idx, posAdded[idx], posArgsDelta[idx], posArgsDone[idx], posItemDone[idx])
		}
		if !(posAdded[idx] < posArgsDelta[idx] && posArgsDelta[idx] < posArgsDone[idx] && posArgsDone[idx] < posItemDone[idx]) {
			t.Fatalf("unexpected ordering for output_index %d: added=%d argsDelta=%d argsDone=%d itemDone=%d", idx, posAdded[idx], posArgsDelta[idx], posArgsDone[idx], posItemDone[idx])
		}
		if idx > 0 && !(posItemDone[idx-1] < posAdded[idx]) {
			t.Fatalf("function call events overlap between %d and %d: prevDone=%d nextAdded=%d", idx-1, idx, posItemDone[idx-1], posAdded[idx])
		}
	}

	if deltaByIndex[0] != "{}" {
		t.Fatalf("unexpected delta for output_index 0: got %q", deltaByIndex[0])
	}
	if deltaByIndex[1] != "{}" {
		t.Fatalf("unexpected delta for output_index 1: got %q", deltaByIndex[1])
	}
	if deltaByIndex[2] == "" || !gjson.Valid(deltaByIndex[2]) || gjson.Get(deltaByIndex[2], "a").Int() != 1 {
		t.Fatalf("unexpected delta for output_index 2: got %q", deltaByIndex[2])
	}
	if !(posItemDone[2] < posCompleted) {
		t.Fatalf("response.completed should be after last output_item.done: last=%d completed=%d", posItemDone[2], posCompleted)
	}
}

func TestConvertGeminiResponseToOpenAIResponses_ResponseOutputOrdering(t *testing.T) {
	in := []string{
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"tool0","args":{"x":"y"}}}]}}],"modelVersion":"test-model","responseId":"req_vrtx_2"},"traceId":"t2"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}],"modelVersion":"test-model","responseId":"req_vrtx_2"},"traceId":"t2"}`,
		`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"cachedContentTokenCount":0},"modelVersion":"test-model","responseId":"req_vrtx_2"},"traceId":"t2"}`,
	}

	var param any
	var out []string
	for _, line := range in {
		out = append(out, ConvertGeminiResponseToOpenAIResponses(context.Background(), "test-model", nil, nil, []byte(line), &param)...)
	}

	posFuncDone := -1
	posMsgAdded := -1
	posCompleted := -1

	for i, chunk := range out {
		ev, data := parseSSEEvent(t, chunk)
		switch ev {
		case "response.output_item.done":
			if data.Get("item.type").String() == "function_call" && data.Get("output_index").Int() == 0 {
				posFuncDone = i
			}
		case "response.output_item.added":
			if data.Get("item.type").String() == "message" && data.Get("output_index").Int() == 1 {
				posMsgAdded = i
			}
		case "response.completed":
			posCompleted = i
			if data.Get("response.output.0.type").String() != "function_call" {
				t.Fatalf("expected response.output[0] to be function_call: %s", data.Get("response.output.0").Raw)
			}
			if data.Get("response.output.1.type").String() != "message" {
				t.Fatalf("expected response.output[1] to be message: %s", data.Get("response.output.1").Raw)
			}
			if data.Get("response.output.1.content.0.text").String() != "hi" {
				t.Fatalf("unexpected message text in response.output[1]: %s", data.Get("response.output.1").Raw)
			}
		}
	}

	if posFuncDone == -1 || posMsgAdded == -1 || posCompleted == -1 {
		t.Fatalf("missing required events: funcDone=%d msgAdded=%d completed=%d", posFuncDone, posMsgAdded, posCompleted)
	}
	if !(posFuncDone < posMsgAdded) {
		t.Fatalf("expected function_call to complete before message is added: funcDone=%d msgAdded=%d", posFuncDone, posMsgAdded)
	}
	if !(posMsgAdded < posCompleted) {
		t.Fatalf("expected response.completed after message added: msgAdded=%d completed=%d", posMsgAdded, posCompleted)
	}
}
