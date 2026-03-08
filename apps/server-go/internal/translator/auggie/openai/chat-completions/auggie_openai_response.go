package chat_completions

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type convertAuggieResponseToOpenAIParams struct {
	Created int64
	ID      string
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
			ID:      fmt.Sprintf("auggie-%d", now),
		}
	}
	state := (*param).(*convertAuggieResponseToOpenAIParams)

	text := gjson.GetBytes(rawJSON, "text").String()
	stopReason := strings.TrimSpace(gjson.GetBytes(rawJSON, "stop_reason").String())
	if strings.TrimSpace(text) == "" && stopReason == "" {
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
	if stopReason != "" {
		template, _ = sjson.Set(template, "choices.0.finish_reason", mapAuggieStopReason(stopReason))
		template, _ = sjson.Set(template, "choices.0.native_finish_reason", strings.ToLower(stopReason))
		if text == "" {
			template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		}
	}

	return []string{template}
}

func mapAuggieStopReason(stopReason string) string {
	switch strings.ToLower(strings.TrimSpace(stopReason)) {
	case "tool_use", "tool_calls":
		return "tool_calls"
	case "max_tokens", "max_output_tokens":
		return "max_tokens"
	default:
		return "stop"
	}
}
