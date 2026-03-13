package chat_completions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var auggieToolCallIDCounter uint64
var auggieChatCompletionIDCounter uint64

func newAuggieChatCompletionID() string {
	return fmt.Sprintf("chatcmpl-%x_%d", time.Now().UnixNano(), atomic.AddUint64(&auggieChatCompletionIDCounter, 1))
}

type convertAuggieResponseToOpenAIParams struct {
	Created       int64
	ID            string
	FunctionIndex int
	SawToolCall   bool
}

// ConvertAuggieResponseToOpenAI converts a single Auggie chat-stream JSON line
// into an OpenAI chat.completion.chunk payload.
func ConvertAuggieResponseToOpenAI(_ context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	_ = originalRequestRawJSON
	_ = requestRawJSON

	rawJSON = bytes.TrimSpace(rawJSON)
	if len(rawJSON) == 0 || bytes.Equal(rawJSON, []byte("[DONE]")) {
		return nil
	}

	if *param == nil {
		now := time.Now().Unix()
		*param = &convertAuggieResponseToOpenAIParams{
			Created: now,
			ID:      newAuggieChatCompletionID(),
		}
	}
	state := (*param).(*convertAuggieResponseToOpenAIParams)

	text := gjson.GetBytes(rawJSON, "text").String()
	stopReason := strings.TrimSpace(gjson.GetBytes(rawJSON, "stop_reason").String())

	// Check for tool_use and token_usage in nodes array
	var toolUses []gjson.Result
	var tokenUsage gjson.Result
	var nodeTextSegments []string
	var thinkingTexts []string
	var thinkingEncryptedContent string
	var thinkingItemID string
	includeReasoningEncryptedContent := requestIncludesReasoningEncryptedContent(originalRequestRawJSON)
	nodes := gjson.GetBytes(rawJSON, "nodes")
	if nodes.Exists() && nodes.IsArray() {
		nodes.ForEach(func(_, node gjson.Result) bool {
			tu := node.Get("tool_use")
			if tu.Exists() && tu.Type != gjson.Null {
				toolUses = append(toolUses, tu)
			}
			if th := node.Get("thinking"); th.Exists() && th.Type != gjson.Null {
				if text := auggieThinkingText(th); text != "" {
					thinkingTexts = append(thinkingTexts, text)
				}
				if includeReasoningEncryptedContent {
					if encrypted := auggieThinkingEncryptedContent(th); encrypted != "" {
						thinkingEncryptedContent = encrypted
					}
				}
				if itemID := auggieThinkingItemID(th); itemID != "" {
					thinkingItemID = itemID
				}
			}
			if tu := node.Get("token_usage"); tu.Exists() && tu.Type != gjson.Null {
				tokenUsage = tu
			}
			if strings.TrimSpace(text) == "" {
				if nodeText := auggieTextFromNode(node); nodeText != "" {
					nodeTextSegments = append(nodeTextSegments, nodeText)
				}
			}
			return true
		})
	}
	if strings.TrimSpace(text) == "" && len(nodeTextSegments) > 0 {
		text = strings.Join(nodeTextSegments, "")
	}

	hasToolUse := len(toolUses) > 0
	hasUsage := tokenUsage.Exists() && tokenUsage.Type != gjson.Null
	hasThinking := len(thinkingTexts) > 0
	hasThinkingEncryptedContent := thinkingEncryptedContent != ""
	hasThinkingItemID := thinkingItemID != ""
	if strings.TrimSpace(text) == "" && stopReason == "" && !hasToolUse && !hasUsage && !hasThinking && !hasThinkingEncryptedContent && !hasThinkingItemID {
		return nil
	}

	template := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"role":null,"content":null},"finish_reason":null,"native_finish_reason":null}]}`
	template, _ = sjson.Set(template, "id", state.ID)
	template, _ = sjson.Set(template, "created", state.Created)
	template, _ = sjson.Set(template, "model", modelName)

	if text != "" {
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.Set(template, "choices.0.delta.content", text)
	}
	if hasThinking {
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.Set(template, "choices.0.delta.reasoning_content", strings.Join(thinkingTexts, "\n"))
	}
	if hasThinkingEncryptedContent {
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.Set(template, "choices.0.delta.reasoning_encrypted_content", thinkingEncryptedContent)
	}
	if hasThinkingItemID {
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.Set(template, "choices.0.delta.reasoning_item_id", thinkingItemID)
	}

	if hasToolUse {
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		var tcArray []map[string]any
		for _, tu := range toolUses {
			// Auggie uses tool_use_id / tool_name / input_json
			// but also support id / name / input as fallback
			toolID := tu.Get("tool_use_id").String()
			if toolID == "" {
				toolID = tu.Get("id").String()
			}
			toolName := tu.Get("tool_name").String()
			if toolName == "" {
				toolName = tu.Get("name").String()
			}
			if toolID == "" {
				seq := atomic.AddUint64(&auggieToolCallIDCounter, 1)
				toolID = fmt.Sprintf("call_%s_%d_%d", toolName, time.Now().UnixMilli(), seq)
			}

			// Auggie sends input_json as a JSON string; fall back to input (object)
			var argsStr string
			if ij := tu.Get("input_json"); ij.Exists() && ij.Type == gjson.String && strings.TrimSpace(ij.String()) != "" {
				argsStr = ij.String()
			} else if inputRaw := tu.Get("input"); inputRaw.Exists() && inputRaw.Type != gjson.Null {
				argsStr = inputRaw.Raw
			} else {
				argsStr = "{}"
			}

			// Auggie generates unified-diff patches for apply_patch, but
			// Codex CLI expects the *** Begin Patch format. Convert here.
			if toolName == "apply_patch" {
				argsStr = convertApplyPatchArgs(argsStr)
			}

			tcArray = append(tcArray, map[string]any{
				"index": state.FunctionIndex,
				"id":    toolID,
				"type":  "function",
				"function": map[string]any{
					"name":      toolName,
					"arguments": argsStr,
				},
			})
			state.FunctionIndex++
		}
		state.SawToolCall = true
		tcJSON, _ := json.Marshal(tcArray)
		template, _ = sjson.SetRaw(template, "choices.0.delta.tool_calls", string(tcJSON))
	}

	if stopReason != "" {
		fr := mapAuggieStopReason(stopReason)
		if state.SawToolCall {
			fr = "tool_calls"
		}
		template, _ = sjson.Set(template, "choices.0.finish_reason", fr)
		template, _ = sjson.Set(template, "choices.0.native_finish_reason", strings.ToLower(stopReason))
		if text == "" && !hasToolUse {
			template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		}
	} else if state.SawToolCall && !hasToolUse && strings.TrimSpace(text) == "" {
		// If we saw tool calls previously but this chunk has no new data, skip
	}

	// Extract usage from nodes[].token_usage
	if hasUsage {
		usageObj := map[string]any{}
		if v := tokenUsage.Get("input_tokens"); v.Exists() {
			usageObj["prompt_tokens"] = v.Int()
			usageObj["input_tokens"] = v.Int()
		}
		if v := tokenUsage.Get("output_tokens"); v.Exists() {
			usageObj["completion_tokens"] = v.Int()
			usageObj["output_tokens"] = v.Int()
		}
		promptTokens, _ := usageObj["prompt_tokens"].(int64)
		completionTokens, _ := usageObj["completion_tokens"].(int64)
		usageObj["total_tokens"] = promptTokens + completionTokens
		if v := tokenUsage.Get("cache_read_input_tokens"); v.Exists() && v.Int() > 0 {
			usageObj["cache_read_input_tokens"] = v.Int()
		}
		if v := tokenUsage.Get("cache_creation_input_tokens"); v.Exists() && v.Int() > 0 {
			usageObj["cache_creation_input_tokens"] = v.Int()
		}
		usageJSON, _ := json.Marshal(usageObj)
		template, _ = sjson.SetRaw(template, "usage", string(usageJSON))
	}

	return []string{template}
}

func mapAuggieStopReason(stopReason string) string {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "tool_use", "tool_calls", "3":
		return "tool_calls"
	case "max_tokens", "max_output_tokens", "2":
		return "length"
	case "end_turn", "stop", "1":
		return "stop"
	default:
		return "stop"
	}
}

func auggieThinkingText(thinking gjson.Result) string {
	if !thinking.Exists() {
		return ""
	}
	if thinking.Type == gjson.String {
		return strings.TrimSpace(thinking.String())
	}
	if !thinking.IsObject() {
		return ""
	}
	for _, path := range []string{"content", "text", "thinking", "summary"} {
		if result := thinking.Get(path); result.Exists() && result.Type == gjson.String {
			if text := strings.TrimSpace(result.String()); text != "" {
				return text
			}
		}
	}
	return ""
}

func auggieThinkingEncryptedContent(thinking gjson.Result) string {
	if !thinking.Exists() || !thinking.IsObject() {
		return ""
	}
	result := thinking.Get("encrypted_content")
	if !result.Exists() || result.Type != gjson.String {
		return ""
	}
	if strings.TrimSpace(result.String()) == "" {
		return ""
	}
	return result.String()
}

func auggieThinkingItemID(thinking gjson.Result) string {
	if !thinking.Exists() || !thinking.IsObject() {
		return ""
	}
	result := thinking.Get("openai_responses_api_item_id")
	if !result.Exists() || result.Type != gjson.String {
		return ""
	}
	return strings.TrimSpace(result.String())
}

func auggieTextFromNode(node gjson.Result) string {
	if !node.Exists() || !node.IsObject() {
		return ""
	}
	// Type 0 nodes are final accumulated text summaries that duplicate content
	// already streamed via the top-level "text" field. Skip them to avoid
	// emitting the same content twice.
	if nodeType := node.Get("type"); nodeType.Exists() && nodeType.Int() == 0 {
		return ""
	}
	if tu := node.Get("tool_use"); tu.Exists() && tu.Type != gjson.Null {
		return ""
	}
	if tu := node.Get("token_usage"); tu.Exists() && tu.Type != gjson.Null {
		return ""
	}
	if th := node.Get("thinking"); th.Exists() && th.Type != gjson.Null {
		return ""
	}
	content := node.Get("content")
	if !content.Exists() || content.Type != gjson.String {
		return ""
	}
	if strings.TrimSpace(content.String()) == "" {
		return ""
	}
	return content.String()
}

// convertApplyPatchArgs rewrites the apply_patch arguments so that the
// freeform "input" field uses the *** Begin Patch format that Codex CLI
// expects, instead of the unified-diff format that Auggie produces.
func convertApplyPatchArgs(argsStr string) string {
	argsStr = strings.TrimSpace(argsStr)
	if argsStr == "" || argsStr == "{}" {
		return argsStr
	}

	// Extract the raw patch text from the JSON wrapper {"input": "..."}
	var patchText string
	if gjson.Valid(argsStr) {
		parsed := gjson.Parse(argsStr)
		if parsed.IsObject() {
			if input := parsed.Get("input"); input.Exists() && input.Type == gjson.String {
				patchText = input.String()
			}
		}
		if patchText == "" && parsed.Type == gjson.String {
			patchText = parsed.String()
		}
	}
	if patchText == "" {
		patchText = argsStr
	}

	// Already in Codex format – nothing to do.
	if strings.HasPrefix(strings.TrimSpace(patchText), "*** Begin Patch") {
		return argsStr
	}

	// Only convert if it looks like a unified diff.
	trimmed := strings.TrimSpace(patchText)
	if !strings.HasPrefix(trimmed, "--- ") && !strings.HasPrefix(trimmed, "diff ") {
		return argsStr
	}

	converted := unifiedDiffToCodexPatch(patchText)
	if converted == "" {
		return argsStr
	}

	result, err := sjson.Set("{}", "input", converted)
	if err != nil {
		return argsStr
	}
	return result
}

// unifiedDiffToCodexPatch converts a unified-diff string into the
// *** Begin Patch / *** End Patch format expected by Codex CLI.
func unifiedDiffToCodexPatch(diff string) string {
	lines := strings.Split(diff, "\n")
	var out strings.Builder
	out.WriteString("*** Begin Patch\n")

	i := 0
	for i < len(lines) {
		line := lines[i]

		// Skip "diff --git ..." header lines
		if strings.HasPrefix(line, "diff ") {
			i++
			continue
		}

		// Detect file header: --- a/path or --- /dev/null
		if strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
			oldFile := strings.TrimPrefix(line, "--- ")
			newFile := strings.TrimPrefix(lines[i+1], "+++ ")
			// Strip a/ b/ prefixes
			oldFile = strings.TrimPrefix(strings.TrimPrefix(oldFile, "a/"), "/dev/null")
			newFile = strings.TrimPrefix(strings.TrimPrefix(newFile, "b/"), "/dev/null")
			oldFile = strings.TrimSpace(oldFile)
			newFile = strings.TrimSpace(newFile)
			i += 2

			isNew := oldFile == "" || strings.TrimSpace(line) == "--- /dev/null"
			isDelete := newFile == "" || strings.TrimSpace(lines[i-1]) == "+++ /dev/null"

			if isDelete {
				out.WriteString("*** Delete File: " + oldFile + "\n")
				// Skip remaining hunks for this file
				for i < len(lines) && !strings.HasPrefix(lines[i], "--- ") && !strings.HasPrefix(lines[i], "diff ") {
					i++
				}
				continue
			}

			if isNew {
				out.WriteString("*** Add File: " + newFile + "\n")
				// Skip @@ line
				if i < len(lines) && strings.HasPrefix(lines[i], "@@") {
					i++
				}
				// Collect all + lines
				for i < len(lines) {
					l := lines[i]
					if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "diff ") || l == "" && i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "--- ") || strings.HasPrefix(lines[i+1], "diff ")) {
						break
					}
					if strings.HasPrefix(l, "+") {
						out.WriteString(l + "\n")
					} else if strings.HasPrefix(l, "@@") {
						// Another hunk in the same new file – shouldn't happen but handle it
						i++
						continue
					}
					i++
				}
				continue
			}

			// Update file
			out.WriteString("*** Update File: " + newFile + "\n")
			// Process hunks
			for i < len(lines) {
				l := lines[i]
				if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "diff ") {
					break
				}
				if strings.HasPrefix(l, "@@") {
					// Extract context from @@ -n,m +n,m @@ context
					parts := strings.SplitN(l, "@@", 3)
					ctx := ""
					if len(parts) >= 3 {
						ctx = strings.TrimSpace(parts[2])
					}
					if ctx != "" {
						out.WriteString("@@ " + ctx + "\n")
					} else {
						out.WriteString("@@\n")
					}
					i++
					continue
				}
				if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, " ") {
					out.WriteString(l + "\n")
				}
				i++
			}
			continue
		}

		i++
	}

	out.WriteString("*** End Patch\n")
	return out.String()
}

func requestIncludesReasoningEncryptedContent(rawJSON []byte) bool {
	include := gjson.GetBytes(rawJSON, "include")
	if !include.Exists() || !include.IsArray() {
		return false
	}
	for _, item := range include.Array() {
		if item.Type == gjson.String && strings.TrimSpace(item.String()) == "reasoning.encrypted_content" {
			return true
		}
	}
	return false
}
