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

type auggieChatRequest struct {
	Model           string                   `json:"model"`
	Mode            string                   `json:"mode"`
	Message         string                   `json:"message"`
	ChatHistory     []auggieChatHistoryEntry `json:"chat_history"`
	ToolDefinitions []auggieToolDefinition   `json:"tool_definitions"`
}

// ConvertOpenAIRequestToAuggie converts an OpenAI chat-completions payload into
// the minimal Auggie chat-stream request used by the v1 executor.
func ConvertOpenAIRequestToAuggie(modelName string, rawJSON []byte, _ bool) []byte {
	out := auggieChatRequest{
		Model:           modelName,
		Mode:            "CHAT",
		Message:         lastOpenAIUserMessage(rawJSON),
		ChatHistory:     buildAuggieChatHistory(rawJSON),
		ToolDefinitions: buildAuggieToolDefinitions(rawJSON),
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
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() {
		return nil
	}

	out := make([]auggieToolDefinition, 0, len(tools.Array()))
	for _, tool := range tools.Array() {
		if tool.Get("type").String() != "function" {
			continue
		}

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
	}
	return out
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
