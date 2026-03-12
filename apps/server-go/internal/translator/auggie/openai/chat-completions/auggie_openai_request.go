package chat_completions

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type auggieChatHistoryEntry struct {
	RequestMessage string                   `json:"request_message,omitempty"`
	ResponseText   string                   `json:"response_text,omitempty"`
	ResponseNodes  []auggieChatResponseNode `json:"response_nodes,omitempty"`
}

type auggieToolDefinition struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	InputSchemaJSON string `json:"input_schema_json,omitempty"`
}

type auggieChatToolUse struct {
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	InputJSON string `json:"input_json"`
	IsPartial bool   `json:"is_partial"`
}

type auggieChatResponseNode struct {
	ID      int                `json:"id"`
	Type    int                `json:"type"`
	Content string             `json:"content,omitempty"`
	ToolUse *auggieChatToolUse `json:"tool_use,omitempty"`
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
	Model                 string                       `json:"model"`
	Mode                  string                       `json:"mode"`
	ReasoningEffort       string                       `json:"reasoning_effort,omitempty"`
	EnableParallelToolUse *bool                        `json:"enable_parallel_tool_use,omitempty"`
	ConversationID        string                       `json:"conversation_id,omitempty"`
	TurnID                string                       `json:"turn_id,omitempty"`
	RootConversationID    string                       `json:"root_conversation_id,omitempty"`
	Message               string                       `json:"message"`
	SystemPrompt          string                       `json:"system_prompt,omitempty"`
	SystemPromptAppend    string                       `json:"system_prompt_append,omitempty"`
	FeatureFlags          *auggieFeatureDetectionFlags `json:"feature_detection_flags,omitempty"`
	ChatHistory           []auggieChatHistoryEntry     `json:"chat_history"`
	Nodes                 []auggieChatRequestNode      `json:"nodes,omitempty"`
	ToolDefinitions       []auggieToolDefinition       `json:"tool_definitions"`
}

// ConvertOpenAIRequestToAuggie converts an OpenAI chat-completions payload into
// the minimal Auggie chat-stream request used by the v1 executor.
func ConvertOpenAIRequestToAuggie(modelName string, rawJSON []byte, _ bool) []byte {
	systemPrompt, systemPromptAppend := buildAuggieSystemPrompts(rawJSON)
	message, chatHistory := buildAuggieConversation(rawJSON)
	message, chatHistory = inlineAuggieSystemPrompts(message, chatHistory, systemPrompt, systemPromptAppend)
	message = appendAuggieToolChoiceDirectiveToMessage(message, buildAuggieToolChoiceDirective(rawJSON))
	conversationID, turnID := buildAuggieConversationIdentifiers()
	out := auggieChatRequest{
		Model:                 modelName,
		Mode:                  "CHAT",
		ReasoningEffort:       buildAuggieReasoningEffort(rawJSON),
		EnableParallelToolUse: buildAuggieEnableParallelToolUse(rawJSON),
		ConversationID:        conversationID,
		TurnID:                turnID,
		RootConversationID:    conversationID,
		Message:               message,
		FeatureFlags:          buildAuggieFeatureDetectionFlags(rawJSON),
		ChatHistory:           chatHistory,
		Nodes:                 buildAuggieRequestNodes(rawJSON),
		ToolDefinitions:       buildAuggieToolDefinitions(rawJSON),
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

func buildAuggieConversationIdentifiers() (string, string) {
	return uuid.NewString(), uuid.NewString()
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

func buildAuggieEnableParallelToolUse(rawJSON []byte) *bool {
	parallelToolCalls := gjson.GetBytes(rawJSON, "parallel_tool_calls")
	if !parallelToolCalls.Exists() {
		return nil
	}

	enableParallelToolUse := parallelToolCalls.Bool()
	return &enableParallelToolUse
}

func buildAuggieReasoningEffort(rawJSON []byte) string {
	effort := strings.ToLower(strings.TrimSpace(gjson.GetBytes(rawJSON, "reasoning_effort").String()))
	switch effort {
	case "low", "medium", "high":
		return effort
	default:
		return ""
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

func inlineAuggieSystemPrompts(message string, chatHistory []auggieChatHistoryEntry, systemPrompt, systemPromptAppend string) (string, []auggieChatHistoryEntry) {
	segments := make([]string, 0, 2)
	systemPrompt = strings.TrimSpace(systemPrompt)
	systemPromptAppend = strings.TrimSpace(systemPromptAppend)
	if systemPrompt != "" {
		segments = append(segments, systemPrompt)
	}
	if systemPromptAppend != "" {
		segments = append(segments, systemPromptAppend)
	}

	inlinePrompt := strings.TrimSpace(strings.Join(segments, "\n\n"))
	if inlinePrompt == "" {
		return message, chatHistory
	}

	message = strings.TrimSpace(message)
	if message != "" {
		return inlinePrompt + "\n\n" + message, chatHistory
	}

	for i := len(chatHistory) - 1; i >= 0; i-- {
		requestMessage := strings.TrimSpace(chatHistory[i].RequestMessage)
		if requestMessage == "" {
			continue
		}
		chatHistory[i].RequestMessage = inlinePrompt + "\n\n" + requestMessage
		return message, chatHistory
	}

	return inlinePrompt, chatHistory
}

func appendAuggieToolChoiceDirectiveToMessage(message, directive string) string {
	message = strings.TrimSpace(message)
	directive = strings.TrimSpace(directive)
	switch {
	case directive == "":
		return message
	case message == "":
		return message
	default:
		return message + "\n\n" + directive
	}
}

func buildAuggieConversation(rawJSON []byte) (string, []auggieChatHistoryEntry) {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return "", nil
	}

	history := make([]auggieChatHistoryEntry, 0, len(messages.Array())/2)
	pendingRequest := ""
	currentMessage := ""
	for _, message := range messages.Array() {
		role := strings.TrimSpace(message.Get("role").String())
		text := openAIMessageText(message.Get("content"))
		switch role {
		case "user":
			if text == "" {
				continue
			}
			if pendingRequest != "" {
				history = append(history, auggieChatHistoryEntry{RequestMessage: pendingRequest})
			}
			pendingRequest = text
			currentMessage = text
		case "assistant":
			if pendingRequest == "" {
				continue
			}
			hasToolCalls := openAIAssistantHasToolCalls(message)
			if text == "" && !hasToolCalls {
				continue
			}
			entry := auggieChatHistoryEntry{RequestMessage: pendingRequest}
			if text != "" {
				entry.ResponseText = text
			}
			if hasToolCalls {
				entry.ResponseNodes = buildAuggieAssistantResponseNodes(message)
			}
			history = append(history, entry)
			pendingRequest = ""
			currentMessage = ""
		case "tool":
			currentMessage = ""
		}
	}

	if pendingRequest != "" {
		currentMessage = pendingRequest
	}
	if len(history) == 0 {
		return currentMessage, nil
	}
	return currentMessage, history
}

func buildAuggieToolDefinitions(rawJSON []byte) []auggieToolDefinition {
	if shouldOmitAuggieToolDefinitions(rawJSON) {
		return nil
	}
	selectedToolNames := selectedAuggieToolChoiceNames(rawJSON)

	tools := gjson.GetBytes(rawJSON, "tools")
	if tools.IsArray() {
		return buildAuggieToolDefinitionsFromTools(tools.Array(), selectedToolNames)
	}

	functions := gjson.GetBytes(rawJSON, "functions")
	if !functions.IsArray() {
		return nil
	}
	return buildAuggieToolDefinitionsFromLegacyFunctions(functions.Array(), selectedToolNames)
}

func shouldOmitAuggieToolDefinitions(rawJSON []byte) bool {
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(rawJSON, "tool_choice").String()), "none") {
		return true
	}
	functions := gjson.GetBytes(rawJSON, "functions")
	return functions.IsArray() && strings.EqualFold(strings.TrimSpace(gjson.GetBytes(rawJSON, "function_call").String()), "none")
}

func buildAuggieToolDefinitionsFromTools(tools []gjson.Result, selectedToolNames map[string]bool) []auggieToolDefinition {
	out := make([]auggieToolDefinition, 0, len(tools))
	for _, tool := range tools {
		switch toolType := strings.TrimSpace(tool.Get("type").String()); {
		case toolType == "function":
			name := strings.TrimSpace(tool.Get("function.name").String())
			if name == "" {
				continue
			}
			if len(selectedToolNames) > 0 && !selectedToolNames[strings.ToLower(name)] {
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
		case isAuggieSupportedWebSearchToolType(toolType):
			if len(selectedToolNames) > 0 && !selectedToolNames["web_search"] && !selectedToolNames["web-search"] {
				continue
			}
			out = append(out, auggieToolDefinition{
				Name: "web-search",
			})
		}
	}
	return out
}

func buildAuggieToolDefinitionsFromLegacyFunctions(functions []gjson.Result, selectedToolNames map[string]bool) []auggieToolDefinition {
	out := make([]auggieToolDefinition, 0, len(functions))
	for _, function := range functions {
		name := strings.TrimSpace(function.Get("name").String())
		if name == "" {
			continue
		}
		if len(selectedToolNames) > 0 && !selectedToolNames[strings.ToLower(name)] {
			continue
		}

		inputSchemaJSON := "{}"
		if parameters := function.Get("parameters"); parameters.Exists() {
			inputSchemaJSON = parameters.Raw
		}

		out = append(out, auggieToolDefinition{
			Name:            name,
			Description:     strings.TrimSpace(function.Get("description").String()),
			InputSchemaJSON: inputSchemaJSON,
		})
	}
	return out
}

func buildAuggieToolChoiceDirective(rawJSON []byte) string {
	return ""
}

func selectedAuggieFunctionToolChoiceName(rawJSON []byte) string {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if toolChoice.IsObject() {
		toolChoiceType := strings.TrimSpace(toolChoice.Get("type").String())
		if strings.EqualFold(toolChoiceType, "function") || strings.EqualFold(toolChoiceType, "custom") {
			name := strings.TrimSpace(toolChoice.Get("function.name").String())
			if name != "" {
				return name
			}
			if name = strings.TrimSpace(toolChoice.Get("name").String()); name != "" {
				return name
			}
		}
	}

	functionCall := gjson.GetBytes(rawJSON, "function_call")
	if !functionCall.IsObject() {
		return ""
	}
	if name := strings.TrimSpace(functionCall.Get("name").String()); name != "" {
		return name
	}
	return strings.TrimSpace(functionCall.Get("function.name").String())
}

func selectedAuggieToolChoiceNames(rawJSON []byte) map[string]bool {
	if name := selectedAuggieFunctionToolChoiceName(rawJSON); name != "" {
		return map[string]bool{strings.ToLower(name): true}
	}
	_, selected := selectedAuggieAllowedToolsChoice(rawJSON)
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func selectedAuggieAllowedToolsChoice(rawJSON []byte) (string, map[string]bool) {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.IsObject() || !strings.EqualFold(strings.TrimSpace(toolChoice.Get("type").String()), "allowed_tools") {
		return "", nil
	}

	container := toolChoice.Get("allowed_tools")
	if !container.Exists() || !container.IsObject() {
		container = toolChoice
	}

	selectedToolNames := make(map[string]bool)
	if tools := container.Get("tools"); tools.Exists() && tools.IsArray() {
		for _, tool := range tools.Array() {
			toolType := strings.TrimSpace(tool.Get("type").String())
			switch toolType {
			case "function", "custom":
				name := strings.TrimSpace(tool.Get("function.name").String())
				if name == "" {
					name = strings.TrimSpace(tool.Get("name").String())
				}
				if name != "" {
					selectedToolNames[strings.ToLower(name)] = true
				}
			default:
				if isAuggieSupportedWebSearchToolType(toolType) {
					selectedToolNames["web_search"] = true
				}
			}
		}
	}

	return strings.TrimSpace(container.Get("mode").String()), selectedToolNames
}

func openAIAssistantHasToolCalls(message gjson.Result) bool {
	toolCalls := message.Get("tool_calls")
	return toolCalls.Exists() && toolCalls.IsArray() && len(toolCalls.Array()) > 0
}

func isAuggieSupportedWebSearchToolType(toolType string) bool {
	switch strings.ToLower(strings.TrimSpace(toolType)) {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26":
		return true
	default:
		return false
	}
}

func buildAuggieAssistantResponseNodes(message gjson.Result) []auggieChatResponseNode {
	toolCalls := message.Get("tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() {
		return nil
	}

	nodes := make([]auggieChatResponseNode, 0, len(toolCalls.Array()))
	nodeID := 1
	for _, toolCall := range toolCalls.Array() {
		toolUseID := strings.TrimSpace(toolCall.Get("id").String())
		toolName := strings.TrimSpace(toolCall.Get("function.name").String())
		if toolUseID == "" || toolName == "" {
			continue
		}

		inputJSON := strings.TrimSpace(toolCall.Get("function.arguments").String())
		if inputJSON == "" && toolCall.Get("function.arguments").Exists() {
			inputJSON = strings.TrimSpace(toolCall.Get("function.arguments").Raw)
		}
		if inputJSON == "" {
			inputJSON = "{}"
		}

		nodes = append(nodes, auggieChatResponseNode{
			ID:      nodeID,
			Type:    5,
			Content: "",
			ToolUse: &auggieChatToolUse{
				ToolUseID: toolUseID,
				ToolName:  toolName,
				InputJSON: inputJSON,
				IsPartial: false,
			},
		})
		nodeID++
	}

	return nodes
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
