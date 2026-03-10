package chat_completions

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

type auggieChatHistoryEntry struct {
	RequestMessage string `json:"request_message,omitempty"`
	ResponseText   string `json:"response_text,omitempty"`
}

type auggieToolDefinition struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	InputSchemaJSON string `json:"input_schema_json,omitempty"`
}

type auggieChatRequestToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

type auggieChatRequestNode struct {
	ID             int                          `json:"id"`
	Type           int                          `json:"type"`
	ToolResultNode *auggieChatRequestToolResult `json:"tool_result_node,omitempty"`
}

type auggieFeatureDetectionFlags struct {
	SupportParallelToolUse *bool `json:"support_parallel_tool_use,omitempty"`
}

type auggieChatRequest struct {
	Model              string                       `json:"model"`
	Mode               string                       `json:"mode"`
	Message            string                       `json:"message"`
	SystemPrompt       string                       `json:"system_prompt,omitempty"`
	SystemPromptAppend string                       `json:"system_prompt_append,omitempty"`
	FeatureFlags       *auggieFeatureDetectionFlags `json:"feature_detection_flags,omitempty"`
	ChatHistory        []auggieChatHistoryEntry     `json:"chat_history"`
	Nodes              []auggieChatRequestNode      `json:"nodes,omitempty"`
	ToolDefinitions    []auggieToolDefinition       `json:"tool_definitions"`
}

// ConvertOpenAIRequestToAuggie converts an OpenAI chat-completions payload into
// the minimal Auggie chat-stream request used by the v1 executor.
func ConvertOpenAIRequestToAuggie(modelName string, rawJSON []byte, _ bool) []byte {
	systemPrompt, systemPromptAppend := buildAuggieSystemPrompts(rawJSON)
	out := auggieChatRequest{
		Model:              modelName,
		Mode:               "CHAT",
		Message:            lastOpenAIUserMessage(rawJSON),
		SystemPrompt:       systemPrompt,
		SystemPromptAppend: systemPromptAppend,
		FeatureFlags:       buildAuggieFeatureDetectionFlags(rawJSON),
		ChatHistory:        buildAuggieChatHistory(rawJSON),
		Nodes:              buildAuggieRequestNodes(rawJSON),
		ToolDefinitions:    buildAuggieToolDefinitions(rawJSON),
	}
	if out.ChatHistory == nil {
		out.ChatHistory = []auggieChatHistoryEntry{}
	}
	if out.ToolDefinitions == nil {
		out.ToolDefinitions = []auggieToolDefinition{}
	}

	body, err := json.Marshal(out)
	if err != nil {
		return []byte(`{"model":"","mode":"CHAT","message":"","chat_history":[],"tool_definitions":[]}`)
	}
	return body
}

func buildAuggieFeatureDetectionFlags(rawJSON []byte) *auggieFeatureDetectionFlags {
	parallelToolCalls := gjson.GetBytes(rawJSON, "parallel_tool_calls")
	if !parallelToolCalls.Exists() {
		return nil
	}

	supportParallelToolUse := parallelToolCalls.Bool()
	return &auggieFeatureDetectionFlags{
		SupportParallelToolUse: &supportParallelToolUse,
	}
}

func buildAuggieSystemPrompts(rawJSON []byte) (string, string) {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return "", ""
	}

	prompts := make([]string, 0, len(messages.Array()))
	for _, message := range messages.Array() {
		role := strings.TrimSpace(message.Get("role").String())
		if role != "system" && role != "developer" {
			continue
		}

		text := openAIMessageText(message.Get("content"))
		if text == "" {
			continue
		}
		prompts = append(prompts, text)
	}

	if len(prompts) == 0 {
		return "", ""
	}
	if len(prompts) == 1 {
		return prompts[0], ""
	}
	return prompts[0], strings.Join(prompts[1:], "\n\n")
}

func lastOpenAIUserMessage(rawJSON []byte) string {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return ""
	}

	last := ""
	for _, message := range messages.Array() {
		if message.Get("role").String() != "user" {
			continue
		}
		text := openAIMessageText(message.Get("content"))
		if text != "" {
			last = text
		}
	}
	return last
}

func buildAuggieChatHistory(rawJSON []byte) []auggieChatHistoryEntry {
	messagesResult := gjson.GetBytes(rawJSON, "messages")
	if !messagesResult.IsArray() {
		return nil
	}

	messages := messagesResult.Array()
	lastUserIndex := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Get("role").String() != "user" {
			continue
		}
		if openAIMessageText(messages[i].Get("content")) == "" {
			continue
		}
		lastUserIndex = i
		break
	}
	if lastUserIndex <= 0 {
		return nil
	}

	history := make([]auggieChatHistoryEntry, 0, lastUserIndex/2)
	pendingRequest := ""
	for i := 0; i < lastUserIndex; i++ {
		message := messages[i]
		text := openAIMessageText(message.Get("content"))
		if text == "" {
			continue
		}

		switch message.Get("role").String() {
		case "user":
			if pendingRequest != "" {
				history = append(history, auggieChatHistoryEntry{RequestMessage: pendingRequest})
			}
			pendingRequest = text
		case "assistant":
			if pendingRequest == "" {
				continue
			}
			history = append(history, auggieChatHistoryEntry{
				RequestMessage: pendingRequest,
				ResponseText:   text,
			})
			pendingRequest = ""
		}
	}

	if pendingRequest != "" {
		history = append(history, auggieChatHistoryEntry{RequestMessage: pendingRequest})
	}
	return history
}

func buildAuggieToolDefinitions(rawJSON []byte) []auggieToolDefinition {
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(rawJSON, "tool_choice").String()), "none") {
		return nil
	}

	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() {
		return nil
	}

	out := make([]auggieToolDefinition, 0, len(tools.Array()))
	for _, tool := range tools.Array() {
		switch strings.TrimSpace(tool.Get("type").String()) {
		case "function":
			name := strings.TrimSpace(tool.Get("function.name").String())
			if name == "" {
				continue
			}

			inputSchemaJSON := "{}"
			if parameters := tool.Get("function.parameters"); parameters.Exists() {
				inputSchemaJSON = parameters.Raw
			}

			out = append(out, auggieToolDefinition{
				Name:            name,
				Description:     strings.TrimSpace(tool.Get("function.description").String()),
				InputSchemaJSON: inputSchemaJSON,
			})
		case "web_search":
			out = append(out, auggieToolDefinition{
				Name: "web-search",
			})
		}
	}
	return out
}

func buildAuggieRequestNodes(rawJSON []byte) []auggieChatRequestNode {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return nil
	}

	nodes := make([]auggieChatRequestNode, 0, len(messages.Array()))
	nodeID := 1
	for _, message := range messages.Array() {
		if message.Get("role").String() != "tool" {
			continue
		}

		toolCallID := strings.TrimSpace(message.Get("tool_call_id").String())
		if toolCallID == "" {
			continue
		}

		content := openAIMessageText(message.Get("content"))
		if content == "" && message.Get("content").Type == gjson.String {
			content = strings.TrimSpace(message.Get("content").String())
		}
		if content == "" {
			content = strings.TrimSpace(message.Get("content").Raw)
		}

		nodes = append(nodes, auggieChatRequestNode{
			ID:   nodeID,
			Type: 1,
			ToolResultNode: &auggieChatRequestToolResult{
				ToolUseID: toolCallID,
				Content:   content,
				IsError:   message.Get("is_error").Bool(),
			},
		})
		nodeID++
	}

	if len(nodes) == 0 {
		return nil
	}
	return nodes
}

func openAIMessageText(content gjson.Result) string {
	switch {
	case content.Type == gjson.String:
		return strings.TrimSpace(content.String())
	case content.IsObject():
		if content.Get("type").String() == "text" {
			return strings.TrimSpace(content.Get("text").String())
		}
	case content.IsArray():
		parts := make([]string, 0, len(content.Array()))
		for _, item := range content.Array() {
			if item.Get("type").String() != "text" {
				continue
			}
			text := strings.TrimSpace(item.Get("text").String())
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
