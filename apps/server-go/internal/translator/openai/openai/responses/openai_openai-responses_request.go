package responses

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertOpenAIResponsesRequestToOpenAIChatCompletions converts OpenAI responses format to OpenAI chat completions format.
// It transforms the OpenAI responses API format (with instructions and input array) into the standard
// OpenAI chat completions format (with messages array and system content).
//
// The conversion handles:
// 1. Model name and streaming configuration
// 2. Instructions to system message conversion
// 3. Input array to messages array transformation
// 4. Tool definitions and tool choice conversion
// 5. Function calls and function results handling
// 6. Generation parameters mapping (max_tokens, reasoning, etc.)
//
// Parameters:
//   - modelName: The name of the model to use for the request
//   - rawJSON: The raw JSON request data in OpenAI responses format
//   - stream: A boolean indicating if the request is for a streaming response
//
// Returns:
//   - []byte: The transformed request data in OpenAI chat completions format
func ConvertOpenAIResponsesRequestToOpenAIChatCompletions(modelName string, inputRawJSON []byte, stream bool) []byte {
	rawJSON := inputRawJSON
	// Base OpenAI chat completions template with default values
	out := `{"model":"","messages":[],"stream":false}`

	root := gjson.ParseBytes(rawJSON)

	// Set model name
	out, _ = sjson.Set(out, "model", modelName)

	// Set stream configuration
	out, _ = sjson.Set(out, "stream", stream)

	// Map generation parameters from responses format to chat completions format
	if maxTokens := root.Get("max_output_tokens"); maxTokens.Exists() {
		out, _ = sjson.Set(out, "max_completion_tokens", maxTokens.Int())
	}
	if temperature := root.Get("temperature"); temperature.Exists() {
		out, _ = sjson.Set(out, "temperature", temperature.Value())
	}
	if topP := root.Get("top_p"); topP.Exists() {
		out, _ = sjson.Set(out, "top_p", topP.Value())
	}
	if user := root.Get("user"); user.Exists() {
		out, _ = sjson.Set(out, "user", user.Value())
	}
	if metadata := root.Get("metadata"); metadata.Exists() {
		out, _ = sjson.Set(out, "metadata", metadata.Value())
	}
	if promptCacheKey := root.Get("prompt_cache_key"); promptCacheKey.Exists() {
		out, _ = sjson.Set(out, "prompt_cache_key", promptCacheKey.Value())
	}
	if safetyIdentifier := root.Get("safety_identifier"); safetyIdentifier.Exists() {
		out, _ = sjson.Set(out, "safety_identifier", safetyIdentifier.Value())
	}
	if serviceTier := root.Get("service_tier"); serviceTier.Exists() {
		out, _ = sjson.Set(out, "service_tier", serviceTier.Value())
	}
	if store := root.Get("store"); store.Exists() {
		out, _ = sjson.Set(out, "store", store.Value())
	}
	if promptCacheRetention := root.Get("prompt_cache_retention"); promptCacheRetention.Exists() {
		out, _ = sjson.Set(out, "prompt_cache_retention", promptCacheRetention.Value())
	}

	if parallelToolCalls := root.Get("parallel_tool_calls"); parallelToolCalls.Exists() {
		out, _ = sjson.Set(out, "parallel_tool_calls", parallelToolCalls.Bool())
	}
	if topLogprobs := root.Get("top_logprobs"); topLogprobs.Exists() {
		out, _ = sjson.Set(out, "top_logprobs", topLogprobs.Int())
		out, _ = sjson.Set(out, "logprobs", true)
	}
	if includeObfuscation := root.Get("stream_options.include_obfuscation"); includeObfuscation.Exists() {
		out, _ = sjson.Set(out, "stream_options.include_obfuscation", includeObfuscation.Value())
	}
	if verbosity := root.Get("text.verbosity"); verbosity.Exists() {
		value := strings.ToLower(strings.TrimSpace(verbosity.String()))
		if value != "" {
			out, _ = sjson.Set(out, "verbosity", value)
		}
	}
	out = applyResponsesTextFormatToChatResponseFormat(out, root.Get("text.format"))

	// Convert instructions to system message
	if instructions := root.Get("instructions"); instructions.Exists() {
		systemMessage := `{"role":"system","content":""}`
		systemMessage, _ = sjson.Set(systemMessage, "content", instructions.String())
		out, _ = sjson.SetRaw(out, "messages.-1", systemMessage)
	}

	// Convert input array to messages
	if input := root.Get("input"); input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			itemType := item.Get("type").String()
			if itemType == "" && item.Get("role").String() != "" {
				itemType = "message"
			}

			switch itemType {
			case "message", "":
				// Handle regular message conversion
				role := item.Get("role").String()
				message := `{"role":"","content":[]}`
				message, _ = sjson.Set(message, "role", role)

				if content := item.Get("content"); content.Exists() && content.IsArray() {
					var messageContent string
					var toolCalls []interface{}

					content.ForEach(func(_, contentItem gjson.Result) bool {
						contentType := contentItem.Get("type").String()
						if contentType == "" {
							contentType = "input_text"
						}

						switch contentType {
						case "input_text", "output_text":
							text := contentItem.Get("text").String()
							contentPart := `{"type":"text","text":""}`
							contentPart, _ = sjson.Set(contentPart, "text", text)
							message, _ = sjson.SetRaw(message, "content.-1", contentPart)
						case "input_image":
							imageURL := contentItem.Get("image_url").String()
							contentPart := `{"type":"image_url","image_url":{"url":""}}`
							contentPart, _ = sjson.Set(contentPart, "image_url.url", imageURL)
							message, _ = sjson.SetRaw(message, "content.-1", contentPart)
						}
						return true
					})

					if messageContent != "" {
						message, _ = sjson.Set(message, "content", messageContent)
					}

					if len(toolCalls) > 0 {
						message, _ = sjson.Set(message, "tool_calls", toolCalls)
					}
				} else if content.Type == gjson.String {
					message, _ = sjson.Set(message, "content", content.String())
				}

				out, _ = sjson.SetRaw(out, "messages.-1", message)

			case "function_call":
				// Handle function call conversion to assistant message with tool_calls
				assistantMessage := `{"role":"assistant","tool_calls":[]}`

				toolCall := `{"id":"","type":"function","function":{"name":"","arguments":""}}`

				if callId := item.Get("call_id"); callId.Exists() {
					toolCall, _ = sjson.Set(toolCall, "id", callId.String())
				}

				if name := item.Get("name"); name.Exists() {
					toolCall, _ = sjson.Set(toolCall, "function.name", name.String())
				}

				if arguments := item.Get("arguments"); arguments.Exists() {
					toolCall, _ = sjson.Set(toolCall, "function.arguments", arguments.String())
				}

				assistantMessage, _ = sjson.SetRaw(assistantMessage, "tool_calls.0", toolCall)
				out, _ = sjson.SetRaw(out, "messages.-1", assistantMessage)

			case "custom_tool_call":
				assistantMessage := `{"role":"assistant","tool_calls":[]}`
				toolCall := `{"id":"","type":"function","function":{"name":"","arguments":"{}"}}`

				if callID := item.Get("call_id"); callID.Exists() {
					toolCall, _ = sjson.Set(toolCall, "id", callID.String())
				}
				if name := item.Get("name"); name.Exists() {
					toolCall, _ = sjson.Set(toolCall, "function.name", name.String())
				}
				if input := item.Get("input"); input.Exists() {
					toolCall, _ = sjson.Set(toolCall, "function.arguments", marshalResponsesCustomToolShimInput(input))
				}

				assistantMessage, _ = sjson.SetRaw(assistantMessage, "tool_calls.0", toolCall)
				out, _ = sjson.SetRaw(out, "messages.-1", assistantMessage)

			case "function_call_output":
				// Handle function call output conversion to tool message
				toolMessage := `{"role":"tool","tool_call_id":"","content":""}`

				if callId := item.Get("call_id"); callId.Exists() {
					toolMessage, _ = sjson.Set(toolMessage, "tool_call_id", callId.String())
				}

				if output := item.Get("output"); output.Exists() {
					toolMessage, _ = sjson.Set(toolMessage, "content", convertResponsesFunctionCallOutputToOpenAIContent(output))
				}

				out, _ = sjson.SetRaw(out, "messages.-1", toolMessage)

			case "custom_tool_call_output":
				toolMessage := `{"role":"tool","tool_call_id":"","content":""}`

				if callID := item.Get("call_id"); callID.Exists() {
					toolMessage, _ = sjson.Set(toolMessage, "tool_call_id", callID.String())
				}

				if output := item.Get("output"); output.Exists() {
					toolMessage, _ = sjson.Set(toolMessage, "content", convertResponsesCustomToolOutputToOpenAIContent(output))
				}

				out, _ = sjson.SetRaw(out, "messages.-1", toolMessage)
			}

			return true
		})
	} else if input.Type == gjson.String {
		msg := "{}"
		msg, _ = sjson.Set(msg, "role", "user")
		msg, _ = sjson.Set(msg, "content", input.String())
		out, _ = sjson.SetRaw(out, "messages.-1", msg)
	}

	// Convert tools from responses format to chat completions format
	if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		var chatCompletionsTools []interface{}

		tools.ForEach(func(_, tool gjson.Result) bool {
			// Built-in tools (e.g. {"type":"web_search"}) are already compatible with the Chat Completions schema.
			// Only function tools need structural conversion because Chat Completions nests details under "function".
			toolType := tool.Get("type").String()
			if toolType == "custom" {
				chatTool := `{"type":"function","function":{}}`
				function := `{"name":"","description":"","strict":true,"parameters":{}}`

				if name := tool.Get("name"); name.Exists() {
					function, _ = sjson.Set(function, "name", name.String())
				}
				if description := tool.Get("description"); description.Exists() {
					function, _ = sjson.Set(function, "description", description.String())
				}
				function, _ = sjson.SetRaw(function, "parameters", responsesCustomToolShimParametersRaw())

				chatTool, _ = sjson.SetRaw(chatTool, "function", function)
				chatCompletionsTools = append(chatCompletionsTools, gjson.Parse(chatTool).Value())
				return true
			}
			if toolType != "" && toolType != "function" && tool.IsObject() {
				// Pass through built-in tools (web_search, etc.) as-is
				chatCompletionsTools = append(chatCompletionsTools, tool.Value())
				return true
			}

			chatTool := `{"type":"function","function":{}}`

			// Convert tool structure from responses format to chat completions format
			function := `{"name":"","description":"","parameters":{}}`

			if name := tool.Get("name"); name.Exists() {
				function, _ = sjson.Set(function, "name", name.String())
			}

			if description := tool.Get("description"); description.Exists() {
				function, _ = sjson.Set(function, "description", description.String())
			}

			strictMode := responsesFunctionToolStrictMode(tool)
			function, _ = sjson.Set(function, "strict", strictMode)

			if parameters := tool.Get("parameters"); parameters.Exists() {
				parametersRaw := parameters.Raw
				if strictMode {
					parametersRaw = normalizeResponsesStrictJSONSchema(parametersRaw)
				}
				function, _ = sjson.SetRaw(function, "parameters", parametersRaw)
			} else if strictMode {
				function, _ = sjson.SetRaw(function, "parameters", normalizeResponsesStrictJSONSchema(""))
			}

			chatTool, _ = sjson.SetRaw(chatTool, "function", function)
			chatCompletionsTools = append(chatCompletionsTools, gjson.Parse(chatTool).Value())

			return true
		})

		if len(chatCompletionsTools) > 0 {
			out, _ = sjson.Set(out, "tools", chatCompletionsTools)
		}
	}

	if reasoningEffort := root.Get("reasoning.effort"); reasoningEffort.Exists() {
		effort := strings.ToLower(strings.TrimSpace(reasoningEffort.String()))
		if effort != "" {
			out, _ = sjson.Set(out, "reasoning_effort", effort)
		}
	}

	// Convert tool_choice if present.
	// Responses API uses {"type":"function","name":"..."} for forced function tools,
	// while Chat Completions expects {"type":"function","function":{"name":"..."}}.
	if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
		switch {
		case toolChoice.Type == gjson.String:
			out, _ = sjson.Set(out, "tool_choice", toolChoice.String())
		case toolChoice.IsObject():
			if toolChoiceType := strings.TrimSpace(toolChoice.Get("type").String()); toolChoiceType == "function" || toolChoiceType == "custom" {
				choice := `{"type":"function","function":{}}`
				name := strings.TrimSpace(toolChoice.Get("name").String())
				if name == "" {
					name = strings.TrimSpace(toolChoice.Get("function.name").String())
				}
				if name != "" {
					choice, _ = sjson.Set(choice, "function.name", name)
				}
				out, _ = sjson.SetRaw(out, "tool_choice", choice)
			} else {
				out, _ = sjson.SetRaw(out, "tool_choice", toolChoice.Raw)
			}
		}
	}

	return []byte(out)
}

func responsesFunctionToolStrictMode(tool gjson.Result) bool {
	strict := tool.Get("strict")
	if !strict.Exists() {
		return true
	}
	return strict.Bool()
}

func normalizeResponsesStrictJSONSchema(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || !gjson.Valid(raw) {
		raw = `{"type":"object","properties":{}}`
	}

	var schema any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
	}

	schema = normalizeResponsesStrictJSONSchemaValue(schema)

	normalized, err := json.Marshal(schema)
	if err != nil {
		return `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
	}
	return string(normalized)
}

func normalizeResponsesStrictJSONSchemaValue(value any) any {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			switch key {
			case "properties", "$defs", "definitions":
				if m, ok := child.(map[string]any); ok {
					for nestedKey, nestedValue := range m {
						m[nestedKey] = normalizeResponsesStrictJSONSchemaValue(nestedValue)
					}
					node[key] = m
					continue
				}
			case "items":
				node[key] = normalizeResponsesStrictJSONSchemaValue(child)
				continue
			case "anyOf", "allOf", "oneOf", "prefixItems":
				if arr, ok := child.([]any); ok {
					for i := range arr {
						arr[i] = normalizeResponsesStrictJSONSchemaValue(arr[i])
					}
					node[key] = arr
					continue
				}
			}

			switch child.(type) {
			case map[string]any, []any:
				node[key] = normalizeResponsesStrictJSONSchemaValue(child)
			}
		}

		objectLike := false
		if schemaType, ok := node["type"].(string); ok && strings.TrimSpace(schemaType) == "object" {
			objectLike = true
		}
		if _, ok := node["properties"]; ok {
			objectLike = true
		}
		if !objectLike {
			return node
		}

		node["type"] = "object"
		properties, ok := node["properties"].(map[string]any)
		if !ok || properties == nil {
			properties = map[string]any{}
			node["properties"] = properties
		}
		node["additionalProperties"] = false

		requiredNames := make([]string, 0, len(properties))
		for name, property := range properties {
			properties[name] = normalizeResponsesStrictJSONSchemaValue(property)
			requiredNames = append(requiredNames, name)
		}
		sort.Strings(requiredNames)

		required := make([]any, 0, len(requiredNames))
		for _, name := range requiredNames {
			required = append(required, name)
		}
		node["required"] = required
		return node

	case []any:
		for i := range node {
			node[i] = normalizeResponsesStrictJSONSchemaValue(node[i])
		}
		return node

	default:
		return value
	}
}

func applyResponsesTextFormatToChatResponseFormat(out string, textFormat gjson.Result) string {
	if !textFormat.Exists() || textFormat.Type == gjson.Null || !textFormat.IsObject() {
		return out
	}

	formatType := strings.ToLower(strings.TrimSpace(textFormat.Get("type").String()))
	switch formatType {
	case "text", "json_object":
		out, _ = sjson.Set(out, "response_format.type", formatType)
	case "json_schema":
		out, _ = sjson.Set(out, "response_format.type", "json_schema")
		if value := strings.TrimSpace(textFormat.Get("name").String()); value != "" {
			out, _ = sjson.Set(out, "response_format.json_schema.name", value)
		}
		if description := strings.TrimSpace(textFormat.Get("description").String()); description != "" {
			out, _ = sjson.Set(out, "response_format.json_schema.description", description)
		}
		if strict := textFormat.Get("strict"); strict.Exists() {
			out, _ = sjson.Set(out, "response_format.json_schema.strict", strict.Value())
		}
		if schema := textFormat.Get("schema"); schema.Exists() {
			out, _ = sjson.SetRaw(out, "response_format.json_schema.schema", schema.Raw)
		}
	}

	return out
}

func convertResponsesFunctionCallOutputToOpenAIContent(output gjson.Result) any {
	if output.Type == gjson.String {
		return output.String()
	}
	if !output.IsArray() {
		return strings.TrimSpace(output.Raw)
	}

	contentItems := make([]map[string]any, 0, len(output.Array()))
	for _, contentItem := range output.Array() {
		contentType := strings.TrimSpace(contentItem.Get("type").String())
		switch contentType {
		case "input_text", "output_text":
			contentItems = append(contentItems, map[string]any{
				"type": "text",
				"text": contentItem.Get("text").String(),
			})
		default:
			// Preserve unsupported multimodal output as raw JSON instead of silently dropping it.
			return strings.TrimSpace(output.Raw)
		}
	}

	return contentItems
}

func responsesCustomToolShimParametersRaw() string {
	return `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`
}

func marshalResponsesCustomToolShimInput(input gjson.Result) string {
	raw, err := json.Marshal(map[string]any{
		"input": input.Value(),
	})
	if err != nil {
		return `{"input":""}`
	}
	return string(raw)
}

func convertResponsesCustomToolOutputToOpenAIContent(output gjson.Result) any {
	return convertResponsesFunctionCallOutputToOpenAIContent(output)
}
