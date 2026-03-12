package responses

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type oaiToResponsesStateReasoning struct {
	ReasoningID      string
	ReasoningData    string
	EncryptedContent string
	OutputIndex      int
}

type oaiToResponsesStateWebSearchCall struct {
	ID          string
	Status      string
	Query       string
	Output      string
	OutputIndex int
}

type oaiToResponsesWebSearchResult struct {
	Title string
	URL   string
	Text  string
}

type oaiToResponsesState struct {
	Seq             int
	ResponseID      string
	Created         int64
	Started         bool
	ReasoningID     string
	ReasoningIDHint string
	ReasoningIndex  int
	NextOutputIndex int
	// aggregation buffers for response.output
	// Per-output message text buffers by index
	MsgTextBuf                map[int]*strings.Builder
	MsgOutputIndex            map[int]int
	ReasoningBuf              strings.Builder
	ReasoningEncryptedContent string
	Reasonings                []oaiToResponsesStateReasoning
	FuncArgsBuf               map[string]*strings.Builder // function key -> args
	FuncNames                 map[string]string           // function key -> name
	FuncCallIDs               map[string]string           // function key -> call_id
	FuncOutputs               map[string]int              // function key -> output_index
	// message item state per output index
	MsgItemAdded    map[int]bool // whether response.output_item.added emitted for message
	MsgContentAdded map[int]bool // whether response.content_part.added emitted for message
	MsgItemDone     map[int]bool // whether message done events were emitted
	// function item done state
	FuncArgsDone   map[string]bool
	FuncItemDone   map[string]bool
	WebSearchCalls map[string]oaiToResponsesStateWebSearchCall
	WebSearchOrder []string
	WebSearchDone  map[string]bool
	// Accumulated annotations (url_citation) from OpenAI web search
	AnnotationsRaw []string
	// usage aggregation
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	ReasoningTokens  int64
	UsageSeen        bool
}

// responseIDCounter provides a process-wide unique counter for synthesized response identifiers.
var responseIDCounter uint64

var responsesWebSearchMarkdownLinkPattern = regexp.MustCompile(`\[(.*?)\]\((https?://[^)]+)\)(.*)`)

func emitRespEvent(event string, payload string) string {
	return fmt.Sprintf("event: %s\ndata: %s", event, payload)
}

func newOpenAIResponsesID() string {
	return fmt.Sprintf("resp_%x_%d", time.Now().UnixNano(), atomic.AddUint64(&responseIDCounter, 1))
}

func publicOpenAIResponsesID(chatCompletionID string) string {
	chatCompletionID = strings.TrimSpace(chatCompletionID)
	if chatCompletionID == "" {
		return newOpenAIResponsesID()
	}
	if strings.HasPrefix(chatCompletionID, "resp_") {
		return chatCompletionID
	}
	if strings.HasPrefix(chatCompletionID, "chatcmpl-") {
		return "resp_" + strings.TrimPrefix(chatCompletionID, "chatcmpl-")
	}
	if strings.HasPrefix(chatCompletionID, "chatcmpl_") {
		return "resp_" + strings.TrimPrefix(chatCompletionID, "chatcmpl_")
	}

	var b strings.Builder
	b.Grow(len(chatCompletionID))
	for _, r := range chatCompletionID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	sanitized := strings.Trim(b.String(), "_")
	if sanitized == "" {
		return newOpenAIResponsesID()
	}
	return "resp_" + sanitized
}

type responsesTerminalState struct {
	Event            string
	PayloadType      string
	Status           string
	IncompleteReason string
}

func responsesTerminalStateFromFinishReason(finishReason string) responsesTerminalState {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens", "max_output_tokens":
		return responsesTerminalState{
			Event:            "response.incomplete",
			PayloadType:      "response.incomplete",
			Status:           "incomplete",
			IncompleteReason: "max_output_tokens",
		}
	case "content_filter":
		return responsesTerminalState{
			Event:            "response.incomplete",
			PayloadType:      "response.incomplete",
			Status:           "incomplete",
			IncompleteReason: "content_filter",
		}
	default:
		return responsesTerminalState{
			Event:       "response.completed",
			PayloadType: "response.completed",
			Status:      "completed",
		}
	}
}

func responseFunctionKey(choiceIndex, toolCallIndex int) string {
	return fmt.Sprintf("%d:%d", choiceIndex, toolCallIndex)
}

func responseObjectPath(prefix, field string) string {
	if prefix == "" {
		return field
	}
	return prefix + "." + field
}

func responsesRequestIncludes(rawJSON []byte, want string) bool {
	include := gjson.GetBytes(rawJSON, "include")
	if !include.Exists() || !include.IsArray() {
		return false
	}
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, item := range include.Array() {
		if item.Type == gjson.String && strings.TrimSpace(item.String()) == want {
			return true
		}
	}
	return false
}

func responsesStreamIncludesObfuscation(rawJSON []byte) bool {
	includeObfuscation := gjson.GetBytes(rawJSON, "stream_options.include_obfuscation")
	if !includeObfuscation.Exists() || includeObfuscation.Type == gjson.Null {
		return true
	}
	return includeObfuscation.Bool()
}

func appendResponsesStreamObfuscation(payload string, rawJSON []byte) string {
	if !responsesStreamIncludesObfuscation(rawJSON) {
		return payload
	}

	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Keep streaming resilient even if entropy is unavailable.
		payload, _ = sjson.Set(payload, "obfuscation", fmt.Sprintf("%d", time.Now().UnixNano()))
		return payload
	}

	payload, _ = sjson.Set(payload, "obfuscation", hex.EncodeToString(buf[:]))
	return payload
}

func parseResponsesBuiltInToolOutputs(root gjson.Result) []oaiToResponsesStateWebSearchCall {
	items := root.Get("_cliproxy_builtin_tool_outputs")
	if !items.Exists() || !items.IsArray() {
		return nil
	}

	out := make([]oaiToResponsesStateWebSearchCall, 0, len(items.Array()))
	for _, item := range items.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "web_search_call" {
			continue
		}
		id := strings.TrimSpace(item.Get("id").String())
		if id == "" {
			continue
		}
		status := strings.TrimSpace(item.Get("status").String())
		if status == "" {
			status = "completed"
		}
		out = append(out, oaiToResponsesStateWebSearchCall{
			ID:     id,
			Status: status,
			Query:  strings.TrimSpace(item.Get("query").String()),
			Output: strings.TrimSpace(item.Get("output").String()),
		})
	}
	return out
}

func parseResponsesWebSearchResults(output string) []oaiToResponsesWebSearchResult {
	if strings.TrimSpace(output) == "" {
		return nil
	}

	output = strings.ReplaceAll(output, `\r\n`, "\n")
	output = strings.ReplaceAll(output, `\n`, "\n")
	output = strings.ReplaceAll(output, `\r`, "\n")

	lines := strings.Split(output, "\n")
	results := make([]oaiToResponsesWebSearchResult, 0, len(lines))
	for _, line := range lines {
		match := responsesWebSearchMarkdownLinkPattern.FindStringSubmatch(line)
		if len(match) != 4 {
			continue
		}

		title := strings.TrimSpace(match[1])
		url := strings.TrimSpace(match[2])
		text := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(match[3]), "-:"))
		if title == "" || url == "" {
			continue
		}

		results = append(results, oaiToResponsesWebSearchResult{
			Title: title,
			URL:   url,
			Text:  text,
		})
	}

	return results
}

func buildResponsesWebSearchAction(call oaiToResponsesStateWebSearchCall, requestRawJSON []byte, includeExpandedOutput bool) map[string]any {
	action := map[string]any{
		"type":  "search",
		"query": strings.TrimSpace(call.Query),
	}
	if query := strings.TrimSpace(call.Query); query != "" {
		action["queries"] = []string{query}
	}

	if includeExpandedOutput && responsesRequestIncludes(requestRawJSON, "web_search_call.action.sources") {
		results := parseResponsesWebSearchResults(call.Output)
		sources := make([]map[string]any, 0, len(results))
		seenURLs := make(map[string]struct{}, len(results))
		for _, result := range results {
			if result.URL == "" {
				continue
			}
			if _, seen := seenURLs[result.URL]; seen {
				continue
			}
			seenURLs[result.URL] = struct{}{}
			sources = append(sources, map[string]any{
				"type": "url",
				"url":  result.URL,
			})
		}
		if len(sources) > 0 {
			action["sources"] = sources
		}
	}

	return action
}

func buildResponsesWebSearchCallItem(call oaiToResponsesStateWebSearchCall, requestRawJSON []byte, status string, includeExpandedOutput bool) string {
	if strings.TrimSpace(status) == "" {
		status = strings.TrimSpace(call.Status)
	}
	if strings.TrimSpace(status) == "" {
		status = "completed"
	}

	item := map[string]any{
		"id":     call.ID,
		"type":   "web_search_call",
		"status": status,
		"action": buildResponsesWebSearchAction(call, requestRawJSON, includeExpandedOutput),
	}

	if includeExpandedOutput && responsesRequestIncludes(requestRawJSON, "web_search_call.results") {
		parsedResults := parseResponsesWebSearchResults(call.Output)
		if len(parsedResults) > 0 {
			results := make([]map[string]any, 0, len(parsedResults))
			for _, result := range parsedResults {
				entry := map[string]any{
					"title": result.Title,
					"url":   result.URL,
				}
				if result.Text != "" {
					entry["text"] = result.Text
				}
				results = append(results, entry)
			}
			item["results"] = results
		}
	}

	raw, err := json.Marshal(item)
	if err != nil {
		return `{"id":"","type":"web_search_call","status":"completed","action":{"type":"search","query":""}}`
	}
	return string(raw)
}

func deepMergeJSONObject(dst, src any) any {
	srcMap, ok := src.(map[string]any)
	if !ok {
		return src
	}

	dstMap, _ := dst.(map[string]any)
	merged := make(map[string]any, len(dstMap)+len(srcMap))
	for k, v := range dstMap {
		merged[k] = v
	}
	for k, v := range srcMap {
		merged[k] = deepMergeJSONObject(merged[k], v)
	}
	return merged
}

func applyMergedRequestField(payload string, objectPath string, field string, value any) string {
	path := responseObjectPath(objectPath, field)
	merged := deepMergeJSONObject(gjson.Get(payload, path).Value(), value)
	payload, _ = sjson.Set(payload, path, merged)
	return payload
}

func applyDefaultFieldsToResponsesObject(payload string, objectPath string, fallbackModel string) string {
	defaults := []struct {
		field string
		value any
	}{
		{field: "background", value: false},
		{field: "completed_at", value: nil},
		{field: "error", value: nil},
		{field: "incomplete_details", value: nil},
		{field: "instructions", value: nil},
		{field: "max_output_tokens", value: nil},
		{field: "metadata", value: map[string]any{}},
		{field: "output", value: []any{}},
		{field: "parallel_tool_calls", value: true},
		{field: "previous_response_id", value: nil},
		{field: "reasoning", value: map[string]any{"effort": nil, "summary": nil}},
		{field: "store", value: true},
		{field: "temperature", value: 1.0},
		{field: "text", value: map[string]any{"format": map[string]any{"type": "text"}}},
		{field: "tool_choice", value: "auto"},
		{field: "tools", value: []any{}},
		{field: "top_p", value: 1.0},
		{field: "truncation", value: "disabled"},
		{field: "usage", value: nil},
		{field: "user", value: nil},
	}
	for _, def := range defaults {
		payload, _ = sjson.Set(payload, responseObjectPath(objectPath, def.field), def.value)
	}
	if fallbackModel != "" {
		payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "model"), fallbackModel)
	}
	return payload
}

func applyRequestFieldsToResponsesObject(payload string, objectPath string, requestRawJSON []byte, fallbackModel string) string {
	if len(requestRawJSON) > 0 {
		req := gjson.ParseBytes(requestRawJSON)
		if v := req.Get("instructions"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "instructions"), v.Value())
		}
		if v := req.Get("max_output_tokens"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "max_output_tokens"), v.Int())
		} else if v = req.Get("max_tokens"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "max_output_tokens"), v.Int())
		}
		if v := req.Get("max_tool_calls"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "max_tool_calls"), v.Int())
		}
		if v := req.Get("model"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "model"), v.String())
		} else if fallbackModel != "" {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "model"), fallbackModel)
		}
		if v := req.Get("parallel_tool_calls"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "parallel_tool_calls"), v.Bool())
		}
		if v := req.Get("previous_response_id"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "previous_response_id"), v.String())
		}
		if v := req.Get("prompt"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "prompt"), v.Value())
		}
		if v := req.Get("prompt_cache_key"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "prompt_cache_key"), v.String())
		}
		if v := req.Get("prompt_cache_retention"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "prompt_cache_retention"), v.String())
		}
		if v := req.Get("reasoning"); v.Exists() {
			payload = applyMergedRequestField(payload, objectPath, "reasoning", v.Value())
		}
		if v := req.Get("store"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "store"), v.Bool())
		}
		if v := req.Get("safety_identifier"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "safety_identifier"), v.String())
		}
		if v := req.Get("service_tier"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "service_tier"), v.String())
		}
		if v := req.Get("background"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "background"), v.Bool())
		}
		if v := req.Get("temperature"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "temperature"), v.Float())
		}
		if v := req.Get("text"); v.Exists() {
			payload = applyMergedRequestField(payload, objectPath, "text", v.Value())
		}
		if v := req.Get("tool_choice"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "tool_choice"), v.Value())
		}
		if v := req.Get("tools"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "tools"), normalizeResponsesToolsForResponseObject(v))
		}
		if v := req.Get("top_logprobs"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "top_logprobs"), v.Int())
		}
		if v := req.Get("top_p"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "top_p"), v.Float())
		}
		if v := req.Get("truncation"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "truncation"), v.String())
		}
		if v := req.Get("user"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "user"), v.Value())
		}
		if v := req.Get("metadata"); v.Exists() {
			payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "metadata"), v.Value())
		}
		return payload
	}
	if fallbackModel != "" {
		payload, _ = sjson.Set(payload, responseObjectPath(objectPath, "model"), fallbackModel)
	}
	return payload
}

func normalizeResponsesToolsForResponseObject(tools gjson.Result) any {
	if !tools.Exists() || !tools.IsArray() {
		return tools.Value()
	}

	out := make([]any, 0, len(tools.Array()))
	for _, tool := range tools.Array() {
		if strings.TrimSpace(tool.Get("type").String()) != "function" || !tool.IsObject() {
			out = append(out, tool.Value())
			continue
		}

		normalized := map[string]any{
			"type":   "function",
			"strict": responsesFunctionToolStrictMode(tool),
		}

		if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
			normalized["name"] = name
		}
		if description := strings.TrimSpace(tool.Get("description").String()); description != "" {
			normalized["description"] = description
		}

		strictMode := normalized["strict"].(bool)
		if parameters := tool.Get("parameters"); parameters.Exists() {
			parametersRaw := parameters.Raw
			if strictMode {
				parametersRaw = normalizeResponsesStrictJSONSchema(parametersRaw)
			}
			normalized["parameters"] = gjson.Parse(parametersRaw).Value()
		} else if strictMode {
			normalized["parameters"] = gjson.Parse(normalizeResponsesStrictJSONSchema("")).Value()
		}

		out = append(out, normalized)
	}

	return out
}

func responsesRequestedToolType(requestRawJSON []byte, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	tools := gjson.GetBytes(requestRawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return ""
	}

	for _, tool := range tools.Array() {
		toolName := strings.TrimSpace(tool.Get("name").String())
		if toolName == "" {
			toolName = strings.TrimSpace(tool.Get("function.name").String())
		}
		if !strings.EqualFold(toolName, name) {
			continue
		}
		return strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
	}

	return ""
}

func responsesIsCustomTool(requestRawJSON []byte, name string) bool {
	return responsesRequestedToolType(requestRawJSON, name) == "custom"
}

func responsesToolCallItemID(callID string, isCustom bool) string {
	if isCustom {
		return "ctc_" + callID
	}
	return "fc_" + callID
}

func responsesCustomToolInput(rawArguments string) string {
	rawArguments = strings.TrimSpace(rawArguments)
	if rawArguments == "" {
		return ""
	}
	if gjson.Valid(rawArguments) {
		parsed := gjson.Parse(rawArguments)
		if parsed.Type == gjson.String {
			return parsed.String()
		}
		if parsed.IsObject() {
			input := parsed.Get("input")
			if input.Exists() && input.Type == gjson.String {
				return input.String()
			}
		}
	}
	return rawArguments
}

// ConvertOpenAIChatCompletionsResponseToOpenAIResponses converts OpenAI Chat Completions streaming chunks
// to OpenAI Responses SSE events (response.*).
func ConvertOpenAIChatCompletionsResponseToOpenAIResponses(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	if *param == nil {
		*param = &oaiToResponsesState{
			FuncArgsBuf:     make(map[string]*strings.Builder),
			FuncNames:       make(map[string]string),
			FuncCallIDs:     make(map[string]string),
			FuncOutputs:     make(map[string]int),
			MsgTextBuf:      make(map[int]*strings.Builder),
			MsgOutputIndex:  make(map[int]int),
			MsgItemAdded:    make(map[int]bool),
			MsgContentAdded: make(map[int]bool),
			MsgItemDone:     make(map[int]bool),
			FuncArgsDone:    make(map[string]bool),
			FuncItemDone:    make(map[string]bool),
			WebSearchCalls:  make(map[string]oaiToResponsesStateWebSearchCall),
			WebSearchDone:   make(map[string]bool),
			Reasonings:      make([]oaiToResponsesStateReasoning, 0),
		}
	}
	st := (*param).(*oaiToResponsesState)

	if bytes.HasPrefix(rawJSON, []byte("data:")) {
		rawJSON = bytes.TrimSpace(rawJSON[5:])
	}

	rawJSON = bytes.TrimSpace(rawJSON)
	if len(rawJSON) == 0 {
		return []string{}
	}
	if bytes.Equal(rawJSON, []byte("[DONE]")) {
		return []string{}
	}

	root := gjson.ParseBytes(rawJSON)
	obj := root.Get("object")
	if obj.Exists() && obj.String() != "" && obj.String() != "chat.completion.chunk" {
		return []string{}
	}
	if !root.Get("choices").Exists() || !root.Get("choices").IsArray() {
		return []string{}
	}

	if usage := root.Get("usage"); usage.Exists() {
		if v := usage.Get("prompt_tokens"); v.Exists() {
			st.PromptTokens = v.Int()
			st.UsageSeen = true
		}
		if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
			st.CachedTokens = v.Int()
			st.UsageSeen = true
		}
		if v := usage.Get("completion_tokens"); v.Exists() {
			st.CompletionTokens = v.Int()
			st.UsageSeen = true
		} else if v := usage.Get("output_tokens"); v.Exists() {
			st.CompletionTokens = v.Int()
			st.UsageSeen = true
		}
		if v := usage.Get("output_tokens_details.reasoning_tokens"); v.Exists() {
			st.ReasoningTokens = v.Int()
			st.UsageSeen = true
		} else if v := usage.Get("completion_tokens_details.reasoning_tokens"); v.Exists() {
			st.ReasoningTokens = v.Int()
			st.UsageSeen = true
		}
		if v := usage.Get("total_tokens"); v.Exists() {
			st.TotalTokens = v.Int()
			st.UsageSeen = true
		}
	}

	nextSeq := func() int {
		current := st.Seq
		st.Seq++
		return current
	}
	nextOutputIndex := func() int {
		index := st.NextOutputIndex
		st.NextOutputIndex++
		return index
	}
	var out []string

	if !st.Started {
		st.Seq = 0
		st.ResponseID = publicOpenAIResponsesID(root.Get("id").String())
		st.Created = root.Get("created").Int()
		// reset aggregation state for a new streaming response
		st.MsgTextBuf = make(map[int]*strings.Builder)
		st.MsgOutputIndex = make(map[int]int)
		st.ReasoningBuf.Reset()
		st.ReasoningID = ""
		st.ReasoningIDHint = ""
		st.ReasoningIndex = -1
		st.NextOutputIndex = 0
		st.FuncArgsBuf = make(map[string]*strings.Builder)
		st.FuncNames = make(map[string]string)
		st.FuncCallIDs = make(map[string]string)
		st.FuncOutputs = make(map[string]int)
		st.MsgItemAdded = make(map[int]bool)
		st.MsgContentAdded = make(map[int]bool)
		st.MsgItemDone = make(map[int]bool)
		st.FuncArgsDone = make(map[string]bool)
		st.FuncItemDone = make(map[string]bool)
		st.WebSearchCalls = make(map[string]oaiToResponsesStateWebSearchCall)
		st.WebSearchOrder = nil
		st.WebSearchDone = make(map[string]bool)
		st.PromptTokens = 0
		st.CachedTokens = 0
		st.CompletionTokens = 0
		st.TotalTokens = 0
		st.ReasoningTokens = 0
		st.UsageSeen = false
		// response.created
		created := `{"type":"response.created","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"in_progress","background":false,"error":null,"output":[]}}`
		created, _ = sjson.Set(created, "sequence_number", nextSeq())
		created, _ = sjson.Set(created, "response.id", st.ResponseID)
		created, _ = sjson.Set(created, "response.created_at", st.Created)
		created = applyDefaultFieldsToResponsesObject(created, "response", root.Get("model").String())
		created = applyRequestFieldsToResponsesObject(created, "response", requestRawJSON, root.Get("model").String())
		out = append(out, emitRespEvent("response.created", created))

		inprog := `{"type":"response.in_progress","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"in_progress"}}`
		inprog, _ = sjson.Set(inprog, "sequence_number", nextSeq())
		inprog, _ = sjson.Set(inprog, "response.id", st.ResponseID)
		inprog, _ = sjson.Set(inprog, "response.created_at", st.Created)
		inprog = applyDefaultFieldsToResponsesObject(inprog, "response", root.Get("model").String())
		inprog = applyRequestFieldsToResponsesObject(inprog, "response", requestRawJSON, root.Get("model").String())
		out = append(out, emitRespEvent("response.in_progress", inprog))
		st.Started = true
	}

	builtInToolOutputs := parseResponsesBuiltInToolOutputs(root)
	for _, builtInCall := range builtInToolOutputs {
		existing, seen := st.WebSearchCalls[builtInCall.ID]
		if !seen {
			builtInCall.OutputIndex = nextOutputIndex()
			st.WebSearchCalls[builtInCall.ID] = builtInCall
			st.WebSearchOrder = append(st.WebSearchOrder, builtInCall.ID)

			item := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{}}`
			item, _ = sjson.Set(item, "sequence_number", nextSeq())
			item, _ = sjson.Set(item, "output_index", builtInCall.OutputIndex)
			item, _ = sjson.SetRaw(item, "item", buildResponsesWebSearchCallItem(builtInCall, requestRawJSON, "in_progress", false))
			out = append(out, emitRespEvent("response.output_item.added", item))

			inProgress := `{"type":"response.web_search_call.in_progress","sequence_number":0,"item_id":"","output_index":0}`
			inProgress, _ = sjson.Set(inProgress, "sequence_number", nextSeq())
			inProgress, _ = sjson.Set(inProgress, "item_id", builtInCall.ID)
			inProgress, _ = sjson.Set(inProgress, "output_index", builtInCall.OutputIndex)
			out = append(out, emitRespEvent("response.web_search_call.in_progress", inProgress))

			searching := `{"type":"response.web_search_call.searching","sequence_number":0,"item_id":"","output_index":0}`
			searching, _ = sjson.Set(searching, "sequence_number", nextSeq())
			searching, _ = sjson.Set(searching, "item_id", builtInCall.ID)
			searching, _ = sjson.Set(searching, "output_index", builtInCall.OutputIndex)
			out = append(out, emitRespEvent("response.web_search_call.searching", searching))
			continue
		}

		if builtInCall.Status != "" {
			existing.Status = builtInCall.Status
		}
		if builtInCall.Query != "" {
			existing.Query = builtInCall.Query
		}
		if builtInCall.Output != "" {
			existing.Output = builtInCall.Output
		}
		st.WebSearchCalls[builtInCall.ID] = existing
	}

	includeReasoningEncryptedContent := responsesRequestIncludes(requestRawJSON, "reasoning.encrypted_content")
	ensureReasoningStarted := func(choiceIndex int) {
		if st.ReasoningID != "" {
			return
		}
		if hinted := strings.TrimSpace(st.ReasoningIDHint); hinted != "" {
			st.ReasoningID = hinted
		} else {
			st.ReasoningID = fmt.Sprintf("rs_%s_%d", st.ResponseID, choiceIndex)
		}
		st.ReasoningIndex = nextOutputIndex()
		item := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"reasoning","status":"in_progress","summary":[]}}`
		item, _ = sjson.Set(item, "sequence_number", nextSeq())
		item, _ = sjson.Set(item, "output_index", st.ReasoningIndex)
		item, _ = sjson.Set(item, "item.id", st.ReasoningID)
		out = append(out, emitRespEvent("response.output_item.added", item))
		contentPart := `{"type":"response.content_part.added","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`
		contentPart, _ = sjson.Set(contentPart, "sequence_number", nextSeq())
		contentPart, _ = sjson.Set(contentPart, "item_id", st.ReasoningID)
		contentPart, _ = sjson.Set(contentPart, "output_index", st.ReasoningIndex)
		contentPart, _ = sjson.Set(contentPart, "content_index", 0)
		out = append(out, emitRespEvent("response.content_part.added", contentPart))
		part := `{"type":"response.reasoning_summary_part.added","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`
		part, _ = sjson.Set(part, "sequence_number", nextSeq())
		part, _ = sjson.Set(part, "item_id", st.ReasoningID)
		part, _ = sjson.Set(part, "output_index", st.ReasoningIndex)
		out = append(out, emitRespEvent("response.reasoning_summary_part.added", part))
	}

	stopReasoning := func(text string) {
		// Emit reasoning done events
		contentDone := `{"type":"response.content_part.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`
		contentDone, _ = sjson.Set(contentDone, "sequence_number", nextSeq())
		contentDone, _ = sjson.Set(contentDone, "item_id", st.ReasoningID)
		contentDone, _ = sjson.Set(contentDone, "output_index", st.ReasoningIndex)
		contentDone, _ = sjson.Set(contentDone, "content_index", 0)
		contentDone, _ = sjson.Set(contentDone, "part.text", text)
		out = append(out, emitRespEvent("response.content_part.done", contentDone))

		reasoningTextDone := `{"type":"response.reasoning_text.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"text":""}`
		reasoningTextDone, _ = sjson.Set(reasoningTextDone, "sequence_number", nextSeq())
		reasoningTextDone, _ = sjson.Set(reasoningTextDone, "item_id", st.ReasoningID)
		reasoningTextDone, _ = sjson.Set(reasoningTextDone, "output_index", st.ReasoningIndex)
		reasoningTextDone, _ = sjson.Set(reasoningTextDone, "content_index", 0)
		reasoningTextDone, _ = sjson.Set(reasoningTextDone, "text", text)
		out = append(out, emitRespEvent("response.reasoning_text.done", reasoningTextDone))

		textDone := `{"type":"response.reasoning_summary_text.done","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"text":""}`
		textDone, _ = sjson.Set(textDone, "sequence_number", nextSeq())
		textDone, _ = sjson.Set(textDone, "item_id", st.ReasoningID)
		textDone, _ = sjson.Set(textDone, "output_index", st.ReasoningIndex)
		textDone, _ = sjson.Set(textDone, "text", text)
		out = append(out, emitRespEvent("response.reasoning_summary_text.done", textDone))
		partDone := `{"type":"response.reasoning_summary_part.done","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`
		partDone, _ = sjson.Set(partDone, "sequence_number", nextSeq())
		partDone, _ = sjson.Set(partDone, "item_id", st.ReasoningID)
		partDone, _ = sjson.Set(partDone, "output_index", st.ReasoningIndex)
		partDone, _ = sjson.Set(partDone, "part.text", text)
		out = append(out, emitRespEvent("response.reasoning_summary_part.done", partDone))
		outputItemDone := `{"type":"response.output_item.done","item":{"id":"","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":""}],"content":[{"type":"reasoning_text","text":""}]},"output_index":0,"sequence_number":0}`
		outputItemDone, _ = sjson.Set(outputItemDone, "sequence_number", nextSeq())
		outputItemDone, _ = sjson.Set(outputItemDone, "item.id", st.ReasoningID)
		outputItemDone, _ = sjson.Set(outputItemDone, "output_index", st.ReasoningIndex)
		outputItemDone, _ = sjson.Set(outputItemDone, "item.summary.text", text)
		outputItemDone, _ = sjson.Set(outputItemDone, "item.content.0.text", text)
		if encrypted := strings.TrimSpace(st.ReasoningEncryptedContent); encrypted != "" && responsesRequestIncludes(requestRawJSON, "reasoning.encrypted_content") {
			outputItemDone, _ = sjson.Set(outputItemDone, "item.encrypted_content", encrypted)
		}
		out = append(out, emitRespEvent("response.output_item.done", outputItemDone))

		st.Reasonings = append(st.Reasonings, oaiToResponsesStateReasoning{
			ReasoningID:      st.ReasoningID,
			ReasoningData:    text,
			EncryptedContent: st.ReasoningEncryptedContent,
			OutputIndex:      st.ReasoningIndex,
		})
		st.ReasoningID = ""
		st.ReasoningIDHint = ""
		st.ReasoningIndex = -1
		st.ReasoningEncryptedContent = ""
	}

	// choices[].delta content / tool_calls / reasoning_content
	if choices := root.Get("choices"); choices.Exists() && choices.IsArray() {
		choices.ForEach(func(_, choice gjson.Result) bool {
			idx := int(choice.Get("index").Int())
			delta := choice.Get("delta")
			if delta.Exists() {
				if reasoningItemID := strings.TrimSpace(delta.Get("reasoning_item_id").String()); reasoningItemID != "" {
					st.ReasoningIDHint = reasoningItemID
				}
				if c := delta.Get("content"); c.Exists() && c.String() != "" {
					// Ensure the message item and its first content part are announced before any text deltas
					if st.ReasoningID != "" {
						stopReasoning(st.ReasoningBuf.String())
						st.ReasoningBuf.Reset()
					}
					if !st.MsgItemAdded[idx] {
						outputIndex, ok := st.MsgOutputIndex[idx]
						if !ok {
							outputIndex = nextOutputIndex()
							st.MsgOutputIndex[idx] = outputIndex
						}
						item := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"message","status":"in_progress","content":[],"role":"assistant"}}`
						item, _ = sjson.Set(item, "sequence_number", nextSeq())
						item, _ = sjson.Set(item, "output_index", outputIndex)
						item, _ = sjson.Set(item, "item.id", fmt.Sprintf("msg_%s_%d", st.ResponseID, idx))
						out = append(out, emitRespEvent("response.output_item.added", item))
						st.MsgItemAdded[idx] = true
					}
					if !st.MsgContentAdded[idx] {
						outputIndex := st.MsgOutputIndex[idx]
						part := `{"type":"response.content_part.added","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}`
						part, _ = sjson.Set(part, "sequence_number", nextSeq())
						part, _ = sjson.Set(part, "item_id", fmt.Sprintf("msg_%s_%d", st.ResponseID, idx))
						part, _ = sjson.Set(part, "output_index", outputIndex)
						part, _ = sjson.Set(part, "content_index", 0)
						out = append(out, emitRespEvent("response.content_part.added", part))
						st.MsgContentAdded[idx] = true
					}

					outputIndex := st.MsgOutputIndex[idx]
					msg := `{"type":"response.output_text.delta","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"delta":"","logprobs":[]}`
					msg, _ = sjson.Set(msg, "sequence_number", nextSeq())
					msg, _ = sjson.Set(msg, "item_id", fmt.Sprintf("msg_%s_%d", st.ResponseID, idx))
					msg, _ = sjson.Set(msg, "output_index", outputIndex)
					msg, _ = sjson.Set(msg, "content_index", 0)
					msg, _ = sjson.Set(msg, "delta", c.String())
					msg = appendResponsesStreamObfuscation(msg, requestRawJSON)
					out = append(out, emitRespEvent("response.output_text.delta", msg))
					// aggregate for response.output
					if st.MsgTextBuf[idx] == nil {
						st.MsgTextBuf[idx] = &strings.Builder{}
					}
					st.MsgTextBuf[idx].WriteString(c.String())
				}

				// Handle annotations (url_citation from web search)
				if annotations := delta.Get("annotations"); annotations.Exists() && annotations.IsArray() {
					annotations.ForEach(func(_, ann gjson.Result) bool {
						compacted := strings.ReplaceAll(strings.ReplaceAll(ann.Raw, "\n", ""), "\r", "")
						st.AnnotationsRaw = append(st.AnnotationsRaw, compacted)
						return true
					})
				}

				// reasoning_content (OpenAI reasoning incremental text)
				if includeReasoningEncryptedContent {
					if encrypted := strings.TrimSpace(delta.Get("reasoning_encrypted_content").String()); encrypted != "" {
						ensureReasoningStarted(idx)
						st.ReasoningEncryptedContent = encrypted
					}
				}
				if rc := delta.Get("reasoning_content"); rc.Exists() && rc.String() != "" {
					ensureReasoningStarted(idx)
					// Append incremental text to reasoning buffer
					st.ReasoningBuf.WriteString(rc.String())
					reasoningDelta := `{"type":"response.reasoning_text.delta","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"delta":""}`
					reasoningDelta, _ = sjson.Set(reasoningDelta, "sequence_number", nextSeq())
					reasoningDelta, _ = sjson.Set(reasoningDelta, "item_id", st.ReasoningID)
					reasoningDelta, _ = sjson.Set(reasoningDelta, "output_index", st.ReasoningIndex)
					reasoningDelta, _ = sjson.Set(reasoningDelta, "content_index", 0)
					reasoningDelta, _ = sjson.Set(reasoningDelta, "delta", rc.String())
					reasoningDelta = appendResponsesStreamObfuscation(reasoningDelta, requestRawJSON)
					out = append(out, emitRespEvent("response.reasoning_text.delta", reasoningDelta))
					msg := `{"type":"response.reasoning_summary_text.delta","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"delta":""}`
					msg, _ = sjson.Set(msg, "sequence_number", nextSeq())
					msg, _ = sjson.Set(msg, "item_id", st.ReasoningID)
					msg, _ = sjson.Set(msg, "output_index", st.ReasoningIndex)
					msg, _ = sjson.Set(msg, "delta", rc.String())
					msg = appendResponsesStreamObfuscation(msg, requestRawJSON)
					out = append(out, emitRespEvent("response.reasoning_summary_text.delta", msg))
				}

				// tool calls
				if tcs := delta.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
					if st.ReasoningID != "" {
						stopReasoning(st.ReasoningBuf.String())
						st.ReasoningBuf.Reset()
					}
					// Before emitting any function events, if a message is open for this index,
					// close its text/content to match Codex expected ordering.
					if st.MsgItemAdded[idx] && !st.MsgItemDone[idx] {
						fullText := ""
						if b := st.MsgTextBuf[idx]; b != nil {
							fullText = b.String()
						}
						outputIndex := st.MsgOutputIndex[idx]
						done := `{"type":"response.output_text.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"text":"","logprobs":[]}`
						done, _ = sjson.Set(done, "sequence_number", nextSeq())
						done, _ = sjson.Set(done, "item_id", fmt.Sprintf("msg_%s_%d", st.ResponseID, idx))
						done, _ = sjson.Set(done, "output_index", outputIndex)
						done, _ = sjson.Set(done, "content_index", 0)
						done, _ = sjson.Set(done, "text", fullText)
						out = append(out, emitRespEvent("response.output_text.done", done))

						partDone := `{"type":"response.content_part.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}`
						partDone, _ = sjson.Set(partDone, "sequence_number", nextSeq())
						partDone, _ = sjson.Set(partDone, "item_id", fmt.Sprintf("msg_%s_%d", st.ResponseID, idx))
						partDone, _ = sjson.Set(partDone, "output_index", outputIndex)
						partDone, _ = sjson.Set(partDone, "content_index", 0)
						partDone, _ = sjson.Set(partDone, "part.text", fullText)
						out = append(out, emitRespEvent("response.content_part.done", partDone))

						itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":""}],"role":"assistant"}}`
						itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
						itemDone, _ = sjson.Set(itemDone, "output_index", outputIndex)
						itemDone, _ = sjson.Set(itemDone, "item.id", fmt.Sprintf("msg_%s_%d", st.ResponseID, idx))
						itemDone, _ = sjson.Set(itemDone, "item.content.0.text", fullText)
						out = append(out, emitRespEvent("response.output_item.done", itemDone))
						st.MsgItemDone[idx] = true
					}

					tcsArray := tcs.Array()
					for toolPos, tc := range tcsArray {
						toolCallIndex := toolPos
						if rawIndex := tc.Get("index"); rawIndex.Exists() {
							toolCallIndex = int(rawIndex.Int())
						}
						funcKey := responseFunctionKey(idx, toolCallIndex)
						if _, exists := st.FuncOutputs[funcKey]; !exists {
							st.FuncOutputs[funcKey] = nextOutputIndex()
						}
						outputIndex := st.FuncOutputs[funcKey]

						nameChunk := tc.Get("function.name").String()
						if nameChunk != "" {
							st.FuncNames[funcKey] = nameChunk
						}

						newCallID := tc.Get("id").String()
						existingCallID := st.FuncCallIDs[funcKey]
						effectiveCallID := existingCallID
						shouldEmitItem := false
						if existingCallID == "" && newCallID != "" {
							effectiveCallID = newCallID
							st.FuncCallIDs[funcKey] = newCallID
							shouldEmitItem = true
						}

						if shouldEmitItem && effectiveCallID != "" {
							isCustom := responsesIsCustomTool(requestRawJSON, st.FuncNames[funcKey])
							o := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"function_call","status":"in_progress","arguments":"","call_id":"","name":""}}`
							if isCustom {
								o = `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"id":"","type":"custom_tool_call","status":"in_progress","input":"","call_id":"","name":""}}`
							}
							o, _ = sjson.Set(o, "sequence_number", nextSeq())
							o, _ = sjson.Set(o, "output_index", outputIndex)
							o, _ = sjson.Set(o, "item.id", responsesToolCallItemID(effectiveCallID, isCustom))
							o, _ = sjson.Set(o, "item.call_id", effectiveCallID)
							o, _ = sjson.Set(o, "item.name", st.FuncNames[funcKey])
							out = append(out, emitRespEvent("response.output_item.added", o))
						}

						if st.FuncArgsBuf[funcKey] == nil {
							st.FuncArgsBuf[funcKey] = &strings.Builder{}
						}

						if args := tc.Get("function.arguments"); args.Exists() && args.String() != "" {
							refCallID := st.FuncCallIDs[funcKey]
							if refCallID == "" {
								refCallID = newCallID
							}
							if refCallID != "" {
								isCustom := responsesIsCustomTool(requestRawJSON, st.FuncNames[funcKey])
								eventName := "response.function_call_arguments.delta"
								ad := `{"type":"response.function_call_arguments.delta","sequence_number":0,"item_id":"","output_index":0,"delta":""}`
								deltaValue := args.String()
								if isCustom {
									eventName = "response.custom_tool_call_input.delta"
									ad = `{"type":"response.custom_tool_call_input.delta","sequence_number":0,"item_id":"","output_index":0,"delta":""}`
									deltaValue = responsesCustomToolInput(args.String())
								}
								ad, _ = sjson.Set(ad, "sequence_number", nextSeq())
								ad, _ = sjson.Set(ad, "item_id", responsesToolCallItemID(refCallID, isCustom))
								ad, _ = sjson.Set(ad, "output_index", outputIndex)
								ad, _ = sjson.Set(ad, "delta", deltaValue)
								ad = appendResponsesStreamObfuscation(ad, requestRawJSON)
								out = append(out, emitRespEvent(eventName, ad))
							}
							st.FuncArgsBuf[funcKey].WriteString(args.String())
						}
					}
				}
			}

			// finish_reason triggers finalization, including text done/content done/item done,
			// reasoning done/part.done, function args done/item done, and completed
			if fr := choice.Get("finish_reason"); fr.Exists() && fr.String() != "" {
				if len(st.WebSearchOrder) > 0 {
					for _, itemID := range st.WebSearchOrder {
						call, ok := st.WebSearchCalls[itemID]
						if !ok || st.WebSearchDone[itemID] {
							continue
						}
						if call.Status == "" {
							call.Status = "completed"
						}

						completedEvent := `{"type":"response.web_search_call.completed","sequence_number":0,"item_id":"","output_index":0}`
						completedEvent, _ = sjson.Set(completedEvent, "sequence_number", nextSeq())
						completedEvent, _ = sjson.Set(completedEvent, "item_id", call.ID)
						completedEvent, _ = sjson.Set(completedEvent, "output_index", call.OutputIndex)
						out = append(out, emitRespEvent("response.web_search_call.completed", completedEvent))

						itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{}}`
						itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
						itemDone, _ = sjson.Set(itemDone, "output_index", call.OutputIndex)
						itemDone, _ = sjson.SetRaw(itemDone, "item", buildResponsesWebSearchCallItem(call, requestRawJSON, call.Status, true))
						out = append(out, emitRespEvent("response.output_item.done", itemDone))

						st.WebSearchDone[itemID] = true
						st.WebSearchCalls[itemID] = call
					}
				}

				// Emit message done events for all indices that started a message
				if len(st.MsgItemAdded) > 0 {
					// sort indices for deterministic order
					idxs := make([]int, 0, len(st.MsgItemAdded))
					for i := range st.MsgItemAdded {
						idxs = append(idxs, i)
					}
					for i := 0; i < len(idxs); i++ {
						for j := i + 1; j < len(idxs); j++ {
							if idxs[j] < idxs[i] {
								idxs[i], idxs[j] = idxs[j], idxs[i]
							}
						}
					}
					for _, i := range idxs {
						if st.MsgItemAdded[i] && !st.MsgItemDone[i] {
							fullText := ""
							if b := st.MsgTextBuf[i]; b != nil {
								fullText = b.String()
							}
							outputIndex := st.MsgOutputIndex[i]
							done := `{"type":"response.output_text.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"text":"","logprobs":[]}`
							done, _ = sjson.Set(done, "sequence_number", nextSeq())
							done, _ = sjson.Set(done, "item_id", fmt.Sprintf("msg_%s_%d", st.ResponseID, i))
							done, _ = sjson.Set(done, "output_index", outputIndex)
							done, _ = sjson.Set(done, "content_index", 0)
							done, _ = sjson.Set(done, "text", fullText)
							out = append(out, emitRespEvent("response.output_text.done", done))

							partDone := `{"type":"response.content_part.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}`
							partDone, _ = sjson.Set(partDone, "sequence_number", nextSeq())
							partDone, _ = sjson.Set(partDone, "item_id", fmt.Sprintf("msg_%s_%d", st.ResponseID, i))
							partDone, _ = sjson.Set(partDone, "output_index", outputIndex)
							partDone, _ = sjson.Set(partDone, "content_index", 0)
							partDone, _ = sjson.Set(partDone, "part.text", fullText)
							if len(st.AnnotationsRaw) > 0 {
								for _, raw := range st.AnnotationsRaw {
									partDone, _ = sjson.SetRaw(partDone, "part.annotations.-1", raw)
								}
							}
							out = append(out, emitRespEvent("response.content_part.done", partDone))

							itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":""}],"role":"assistant"}}`
							itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
							itemDone, _ = sjson.Set(itemDone, "output_index", outputIndex)
							itemDone, _ = sjson.Set(itemDone, "item.id", fmt.Sprintf("msg_%s_%d", st.ResponseID, i))
							itemDone, _ = sjson.Set(itemDone, "item.content.0.text", fullText)
							if len(st.AnnotationsRaw) > 0 {
								for _, raw := range st.AnnotationsRaw {
									itemDone, _ = sjson.SetRaw(itemDone, "item.content.0.annotations.-1", raw)
								}
							}
							out = append(out, emitRespEvent("response.output_item.done", itemDone))
							st.MsgItemDone[i] = true
						}
					}
				}

				if st.ReasoningID != "" {
					stopReasoning(st.ReasoningBuf.String())
					st.ReasoningBuf.Reset()
				}

				// Emit function call done events for any active function calls
				if len(st.FuncOutputs) > 0 {
					funcKeys := make([]string, 0, len(st.FuncOutputs))
					for funcKey := range st.FuncOutputs {
						funcKeys = append(funcKeys, funcKey)
					}
					for i := 0; i < len(funcKeys); i++ {
						for j := i + 1; j < len(funcKeys); j++ {
							left := st.FuncOutputs[funcKeys[i]]
							right := st.FuncOutputs[funcKeys[j]]
							if right < left || (right == left && funcKeys[j] < funcKeys[i]) {
								funcKeys[i], funcKeys[j] = funcKeys[j], funcKeys[i]
							}
						}
					}
					for _, funcKey := range funcKeys {
						callID := st.FuncCallIDs[funcKey]
						if callID == "" || st.FuncItemDone[funcKey] {
							continue
						}
						args := "{}"
						if b := st.FuncArgsBuf[funcKey]; b != nil && b.Len() > 0 {
							args = b.String()
						}
						outputIndex := st.FuncOutputs[funcKey]
						isCustom := responsesIsCustomTool(requestRawJSON, st.FuncNames[funcKey])
						itemID := responsesToolCallItemID(callID, isCustom)
						if isCustom {
							customInput := responsesCustomToolInput(args)
							customDone := `{"type":"response.custom_tool_call_input.done","sequence_number":0,"item_id":"","output_index":0,"input":""}`
							customDone, _ = sjson.Set(customDone, "sequence_number", nextSeq())
							customDone, _ = sjson.Set(customDone, "item_id", itemID)
							customDone, _ = sjson.Set(customDone, "output_index", outputIndex)
							customDone, _ = sjson.Set(customDone, "input", customInput)
							out = append(out, emitRespEvent("response.custom_tool_call_input.done", customDone))

							itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"custom_tool_call","status":"completed","input":"","call_id":"","name":""}}`
							itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
							itemDone, _ = sjson.Set(itemDone, "output_index", outputIndex)
							itemDone, _ = sjson.Set(itemDone, "item.id", itemID)
							itemDone, _ = sjson.Set(itemDone, "item.input", customInput)
							itemDone, _ = sjson.Set(itemDone, "item.call_id", callID)
							itemDone, _ = sjson.Set(itemDone, "item.name", st.FuncNames[funcKey])
							out = append(out, emitRespEvent("response.output_item.done", itemDone))
						} else {
							fcDone := `{"type":"response.function_call_arguments.done","sequence_number":0,"item_id":"","output_index":0,"arguments":"","name":""}`
							fcDone, _ = sjson.Set(fcDone, "sequence_number", nextSeq())
							fcDone, _ = sjson.Set(fcDone, "item_id", itemID)
							fcDone, _ = sjson.Set(fcDone, "output_index", outputIndex)
							fcDone, _ = sjson.Set(fcDone, "arguments", args)
							fcDone, _ = sjson.Set(fcDone, "name", st.FuncNames[funcKey])
							out = append(out, emitRespEvent("response.function_call_arguments.done", fcDone))

							itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"function_call","status":"completed","arguments":"","call_id":"","name":""}}`
							itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
							itemDone, _ = sjson.Set(itemDone, "output_index", outputIndex)
							itemDone, _ = sjson.Set(itemDone, "item.id", itemID)
							itemDone, _ = sjson.Set(itemDone, "item.arguments", args)
							itemDone, _ = sjson.Set(itemDone, "item.call_id", callID)
							itemDone, _ = sjson.Set(itemDone, "item.name", st.FuncNames[funcKey])
							out = append(out, emitRespEvent("response.output_item.done", itemDone))
						}
						st.FuncItemDone[funcKey] = true
						st.FuncArgsDone[funcKey] = true
					}
				}
				terminalState := responsesTerminalStateFromFinishReason(fr.String())
				completed := `{"type":"","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"","background":false,"error":null}}`
				completed, _ = sjson.Set(completed, "type", terminalState.PayloadType)
				completed, _ = sjson.Set(completed, "sequence_number", nextSeq())
				completed, _ = sjson.Set(completed, "response.id", st.ResponseID)
				completed, _ = sjson.Set(completed, "response.created_at", st.Created)
				completed = applyDefaultFieldsToResponsesObject(completed, "response", root.Get("model").String())
				completed, _ = sjson.Set(completed, "response.status", terminalState.Status)
				if terminalState.Status == "completed" {
					completed, _ = sjson.Set(completed, "response.completed_at", time.Now().Unix())
				}
				if terminalState.IncompleteReason != "" {
					completed, _ = sjson.Set(completed, "response.incomplete_details.reason", terminalState.IncompleteReason)
				}
				completed = applyRequestFieldsToResponsesObject(completed, "response", requestRawJSON, root.Get("model").String())
				// Build response.output using the same output_index ordering used by the streamed events.
				type indexedOutputItem struct {
					Index int
					Raw   string
				}
				var outputItems []indexedOutputItem
				if len(st.WebSearchOrder) > 0 {
					for _, itemID := range st.WebSearchOrder {
						call, ok := st.WebSearchCalls[itemID]
						if !ok {
							continue
						}
						if call.Status == "" {
							call.Status = "completed"
						}
						item := buildResponsesWebSearchCallItem(call, requestRawJSON, call.Status, true)
						outputItems = append(outputItems, indexedOutputItem{Index: call.OutputIndex, Raw: item})
					}
				}
				if len(st.Reasonings) > 0 {
					for _, r := range st.Reasonings {
						item := `{"id":"","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":""}],"content":[{"type":"reasoning_text","text":""}]}`
						item, _ = sjson.Set(item, "id", r.ReasoningID)
						item, _ = sjson.Set(item, "summary.0.text", r.ReasoningData)
						item, _ = sjson.Set(item, "content.0.text", r.ReasoningData)
						if encrypted := strings.TrimSpace(r.EncryptedContent); encrypted != "" && includeReasoningEncryptedContent {
							item, _ = sjson.Set(item, "encrypted_content", encrypted)
						}
						outputItems = append(outputItems, indexedOutputItem{Index: r.OutputIndex, Raw: item})
					}
				}
				if len(st.MsgItemAdded) > 0 {
					for i := range st.MsgItemAdded {
						outputIndex, ok := st.MsgOutputIndex[i]
						if !ok {
							continue
						}
						txt := ""
						if b := st.MsgTextBuf[i]; b != nil {
							txt = b.String()
						}
						item := `{"id":"","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":""}],"role":"assistant"}`
						item, _ = sjson.Set(item, "id", fmt.Sprintf("msg_%s_%d", st.ResponseID, i))
						item, _ = sjson.Set(item, "content.0.text", txt)
						if len(st.AnnotationsRaw) > 0 {
							for _, raw := range st.AnnotationsRaw {
								item, _ = sjson.SetRaw(item, "content.0.annotations.-1", raw)
							}
						}
						outputItems = append(outputItems, indexedOutputItem{Index: outputIndex, Raw: item})
					}
				}
				if len(st.FuncOutputs) > 0 {
					for funcKey, outputIndex := range st.FuncOutputs {
						callID := st.FuncCallIDs[funcKey]
						if callID == "" {
							continue
						}
						args := ""
						if b := st.FuncArgsBuf[funcKey]; b != nil {
							args = b.String()
						}
						isCustom := responsesIsCustomTool(requestRawJSON, st.FuncNames[funcKey])
						item := `{"id":"","type":"function_call","status":"completed","arguments":"","call_id":"","name":""}`
						if isCustom {
							item = `{"id":"","type":"custom_tool_call","status":"completed","input":"","call_id":"","name":""}`
						}
						item, _ = sjson.Set(item, "id", responsesToolCallItemID(callID, isCustom))
						if isCustom {
							item, _ = sjson.Set(item, "input", responsesCustomToolInput(args))
						} else {
							item, _ = sjson.Set(item, "arguments", args)
						}
						item, _ = sjson.Set(item, "call_id", callID)
						item, _ = sjson.Set(item, "name", st.FuncNames[funcKey])
						outputItems = append(outputItems, indexedOutputItem{Index: outputIndex, Raw: item})
					}
				}
				for i := 0; i < len(outputItems); i++ {
					for j := i + 1; j < len(outputItems); j++ {
						if outputItems[j].Index < outputItems[i].Index {
							outputItems[i], outputItems[j] = outputItems[j], outputItems[i]
						}
					}
				}
				outputsWrapper := `{"arr":[]}`
				for _, item := range outputItems {
					outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", item.Raw)
				}
				if gjson.Get(outputsWrapper, "arr.#").Int() > 0 {
					completed, _ = sjson.SetRaw(completed, "response.output", gjson.Get(outputsWrapper, "arr").Raw)
				}
				if st.UsageSeen {
					completed, _ = sjson.Set(completed, "response.usage.input_tokens", st.PromptTokens)
					completed, _ = sjson.Set(completed, "response.usage.input_tokens_details.cached_tokens", st.CachedTokens)
					completed, _ = sjson.Set(completed, "response.usage.output_tokens", st.CompletionTokens)
					completed, _ = sjson.Set(completed, "response.usage.output_tokens_details.reasoning_tokens", st.ReasoningTokens)
					total := st.TotalTokens
					if total == 0 {
						total = st.PromptTokens + st.CompletionTokens
					}
					completed, _ = sjson.Set(completed, "response.usage.total_tokens", total)
				}
				out = append(out, emitRespEvent(terminalState.Event, completed))
			}

			return true
		})
	}

	return out
}

// ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream builds a single Responses JSON
// from a non-streaming OpenAI Chat Completions response.
func ConvertOpenAIChatCompletionsResponseToOpenAIResponsesNonStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) string {
	root := gjson.ParseBytes(rawJSON)
	terminalState := responsesTerminalState{
		Event:       "response.completed",
		PayloadType: "response.completed",
		Status:      "completed",
	}
	if choices := root.Get("choices"); choices.Exists() && choices.IsArray() {
		choices.ForEach(func(_, choice gjson.Result) bool {
			candidate := responsesTerminalStateFromFinishReason(choice.Get("finish_reason").String())
			if candidate.Status == "incomplete" {
				terminalState = candidate
				return false
			}
			return true
		})
	}

	// Basic response scaffold
	resp := `{"id":"","object":"response","created_at":0,"status":"completed","background":false,"error":null,"incomplete_details":null}`
	resp = applyDefaultFieldsToResponsesObject(resp, "", root.Get("model").String())
	resp, _ = sjson.Set(resp, "status", terminalState.Status)
	if terminalState.Status == "completed" {
		resp, _ = sjson.Set(resp, "completed_at", time.Now().Unix())
	}
	if terminalState.IncompleteReason != "" {
		resp, _ = sjson.Set(resp, "incomplete_details.reason", terminalState.IncompleteReason)
	}

	// id: use provider id if present, otherwise synthesize
	id := publicOpenAIResponsesID(root.Get("id").String())
	resp, _ = sjson.Set(resp, "id", id)

	// created_at: map from chat.completion created
	created := root.Get("created").Int()
	if created == 0 {
		created = time.Now().Unix()
	}
	resp, _ = sjson.Set(resp, "created_at", created)

	resp = applyRequestFieldsToResponsesObject(resp, "", requestRawJSON, root.Get("model").String())

	// Build output list from choices[...]
	outputsWrapper := `{"arr":[]}`
	// Detect and capture reasoning content if present
	rcText := gjson.GetBytes(rawJSON, "choices.0.message.reasoning_content").String()
	rcItemID := strings.TrimSpace(gjson.GetBytes(rawJSON, "choices.0.message.reasoning_item_id").String())
	rcEncrypted := gjson.GetBytes(rawJSON, "choices.0.message.reasoning_encrypted_content").String()
	includeReasoningEncryptedContent := responsesRequestIncludes(requestRawJSON, "reasoning.encrypted_content")
	includeReasoning := rcText != "" || (includeReasoningEncryptedContent && strings.TrimSpace(rcEncrypted) != "")
	if includeReasoning {
		reasoningItem := `{"id":"","type":"reasoning","status":"completed","summary":[],"content":[]}`
		if rcItemID != "" {
			reasoningItem, _ = sjson.Set(reasoningItem, "id", rcItemID)
		} else {
			rid := id
			if strings.HasPrefix(rid, "resp_") {
				rid = strings.TrimPrefix(rid, "resp_")
			}
			reasoningItem, _ = sjson.Set(reasoningItem, "id", fmt.Sprintf("rs_%s", rid))
		}
		if rcText != "" {
			reasoningItem, _ = sjson.Set(reasoningItem, "summary.0.type", "summary_text")
			reasoningItem, _ = sjson.Set(reasoningItem, "summary.0.text", rcText)
			reasoningItem, _ = sjson.Set(reasoningItem, "content.0.type", "reasoning_text")
			reasoningItem, _ = sjson.Set(reasoningItem, "content.0.text", rcText)
		}
		if includeReasoningEncryptedContent && strings.TrimSpace(rcEncrypted) != "" {
			reasoningItem, _ = sjson.Set(reasoningItem, "encrypted_content", rcEncrypted)
		}
		outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", reasoningItem)
	}

	for _, builtInCall := range parseResponsesBuiltInToolOutputs(root) {
		item := buildResponsesWebSearchCallItem(builtInCall, requestRawJSON, builtInCall.Status, true)
		outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", item)
	}

	if choices := root.Get("choices"); choices.Exists() && choices.IsArray() {
		choices.ForEach(func(_, choice gjson.Result) bool {
			msg := choice.Get("message")
			if msg.Exists() {
				// Text message part
				if c := msg.Get("content"); c.Exists() && c.String() != "" {
					item := `{"id":"","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":""}],"role":"assistant"}`
					item, _ = sjson.Set(item, "id", fmt.Sprintf("msg_%s_%d", id, int(choice.Get("index").Int())))
					item, _ = sjson.Set(item, "content.0.text", c.String())
					// Populate annotations from message if present
					if annotations := msg.Get("annotations"); annotations.Exists() && annotations.IsArray() && len(annotations.Array()) > 0 {
						annotations.ForEach(func(_, ann gjson.Result) bool {
							compacted := strings.ReplaceAll(strings.ReplaceAll(ann.Raw, "\n", ""), "\r", "")
							item, _ = sjson.SetRaw(item, "content.0.annotations.-1", compacted)
							return true
						})
					}
					outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", item)
				}

				// Function/tool calls
				if tcs := msg.Get("tool_calls"); tcs.Exists() && tcs.IsArray() {
					tcs.ForEach(func(_, tc gjson.Result) bool {
						callID := tc.Get("id").String()
						name := tc.Get("function.name").String()
						args := tc.Get("function.arguments").String()
						isCustom := responsesIsCustomTool(requestRawJSON, name)
						item := `{"id":"","type":"function_call","status":"completed","arguments":"","call_id":"","name":""}`
						if isCustom {
							item = `{"id":"","type":"custom_tool_call","status":"completed","input":"","call_id":"","name":""}`
						}
						item, _ = sjson.Set(item, "id", responsesToolCallItemID(callID, isCustom))
						if isCustom {
							item, _ = sjson.Set(item, "input", responsesCustomToolInput(args))
						} else {
							item, _ = sjson.Set(item, "arguments", args)
						}
						item, _ = sjson.Set(item, "call_id", callID)
						item, _ = sjson.Set(item, "name", name)
						outputsWrapper, _ = sjson.SetRaw(outputsWrapper, "arr.-1", item)
						return true
					})
				}
			}
			return true
		})
	}
	if gjson.Get(outputsWrapper, "arr.#").Int() > 0 {
		resp, _ = sjson.SetRaw(resp, "output", gjson.Get(outputsWrapper, "arr").Raw)
	}

	// usage mapping
	if usage := root.Get("usage"); usage.Exists() {
		// Map common tokens
		if usage.Get("prompt_tokens").Exists() || usage.Get("completion_tokens").Exists() || usage.Get("total_tokens").Exists() {
			resp, _ = sjson.Set(resp, "usage.input_tokens", usage.Get("prompt_tokens").Int())
			cachedTokens := int64(0)
			if d := usage.Get("prompt_tokens_details.cached_tokens"); d.Exists() {
				cachedTokens = d.Int()
			}
			resp, _ = sjson.Set(resp, "usage.input_tokens_details.cached_tokens", cachedTokens)
			resp, _ = sjson.Set(resp, "usage.output_tokens", usage.Get("completion_tokens").Int())
			reasoningTokens := int64(0)
			if d := usage.Get("output_tokens_details.reasoning_tokens"); d.Exists() {
				reasoningTokens = d.Int()
			} else if d := usage.Get("completion_tokens_details.reasoning_tokens"); d.Exists() {
				reasoningTokens = d.Int()
			}
			resp, _ = sjson.Set(resp, "usage.output_tokens_details.reasoning_tokens", reasoningTokens)
			resp, _ = sjson.Set(resp, "usage.total_tokens", usage.Get("total_tokens").Int())
		} else {
			// Fallback to raw usage object if structure differs
			resp, _ = sjson.Set(resp, "usage", usage.Value())
		}
	}

	return resp
}
