package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/tidwall/gjson"
)

var supportedOpenAIResponsesIncludeValues = []string{
	"code_interpreter_call.outputs",
	"computer_call_output.output.image_url",
	"file_search_call.results",
	"message.input_image.image_url",
	"message.output_text.logprobs",
	"reasoning.encrypted_content",
	"web_search_call.action.sources",
	"web_search_call.results",
}

var supportedOpenAIResponsesIncludeValueSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(supportedOpenAIResponsesIncludeValues))
	for _, value := range supportedOpenAIResponsesIncludeValues {
		out[value] = struct{}{}
	}
	return out
}()

var supportedOpenAIAuggieIncludeValues = []string{
	"reasoning.encrypted_content",
	"web_search_call.action.sources",
	"web_search_call.results",
}

var supportedOpenAIAuggieIncludeValueSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(supportedOpenAIAuggieIncludeValues))
	for _, value := range supportedOpenAIAuggieIncludeValues {
		out[value] = struct{}{}
	}
	return out
}()

var supportedOpenAIAuggieReasoningEffortValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedOpenAIChatCompletionsVerbosityValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedOpenAIChatCompletionsWebSearchContextSizeValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedOpenAIResponsesVerbosityValues = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var supportedOpenAIResponsesReasoningEffortValues = map[string]struct{}{
	"none":    {},
	"minimal": {},
	"low":     {},
	"medium":  {},
	"high":    {},
	"xhigh":   {},
}

var supportedOpenAIResponsesServiceTierValues = map[string]struct{}{
	"auto":     {},
	"default":  {},
	"flex":     {},
	"scale":    {},
	"priority": {},
}

const (
	openAIMetadataMaxPairs       = 16
	openAIMetadataKeyMaxLength   = 64
	openAIMetadataValueMaxLength = 512
)

func validateOpenAISurfaceModel(modelID string) *interfaces.ErrorMessage {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return missingOpenAIRequiredParameter("model")
	}

	caps := handlers.ResolvePublicModelSurface(modelID)
	if caps.Available && caps.SupportsOpenAI {
		return nil
	}

	return invalidOpenAIValue("model", "Invalid value for 'model': model %q is not available on this endpoint.", modelID)
}

func invalidOpenAIRequestf(format string, args ...any) *interfaces.ErrorMessage {
	return &interfaces.ErrorMessage{
		StatusCode: http.StatusBadRequest,
		Error:      fmt.Errorf(format, args...),
	}
}

func invalidOpenAIRequestWithDetail(message, param, code string) *interfaces.ErrorMessage {
	payload := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
		},
	}
	if param != "" {
		payload["error"].(map[string]any)["param"] = param
	}
	if code != "" {
		payload["error"].(map[string]any)["code"] = code
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &interfaces.ErrorMessage{
			StatusCode: http.StatusBadRequest,
			Error:      errors.New(message),
		}
	}
	return &interfaces.ErrorMessage{
		StatusCode: http.StatusBadRequest,
		Error:      errors.New(string(body)),
	}
}

func invalidOpenAIRequestWithDetailf(param, code, format string, args ...any) *interfaces.ErrorMessage {
	return invalidOpenAIRequestWithDetail(fmt.Sprintf(format, args...), param, code)
}

func missingOpenAIRequiredParameter(param string) *interfaces.ErrorMessage {
	return invalidOpenAIRequestWithDetailf(param, "missing_required_parameter", "Missing required parameter: '%s'.", param)
}

func invalidOpenAIType(param, expected string, value gjson.Result) *interfaces.ErrorMessage {
	return invalidOpenAIRequestWithDetailf(
		param,
		"invalid_type",
		"Invalid type for '%s': expected %s, but got %s instead.",
		param,
		expected,
		openAIJSONTypeName(value),
	)
}

func invalidOpenAIValue(param, format string, args ...any) *interfaces.ErrorMessage {
	return invalidOpenAIRequestWithDetail(fmt.Sprintf(format, args...), param, "invalid_value")
}

func openAIJSONTypeName(value gjson.Result) string {
	switch value.Type {
	case gjson.Null:
		return "null"
	case gjson.False, gjson.True:
		return "a boolean"
	case gjson.Number:
		raw := strings.TrimSpace(value.Raw)
		if raw != "" && !strings.ContainsAny(raw, ".eE") {
			return "an integer"
		}
		return "a number"
	case gjson.String:
		return "a string"
	case gjson.JSON:
		if value.IsArray() {
			return "an array"
		}
		return "an object"
	default:
		return "an unknown value"
	}
}

func validateOpenAIStoreSupport(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	store := gjson.GetBytes(rawJSON, "store")
	if !store.Exists() {
		return nil
	}
	if store.Type != gjson.True && store.Type != gjson.False {
		return invalidOpenAIType("store", "a boolean", store)
	}
	if endpoint == "responses" {
		return nil
	}
	if store.Bool() {
		return invalidOpenAIRequestWithDetailf(
			"store",
			"invalid_value",
			"store=true is not supported on /v1/%s because stored object retrieval endpoints are not implemented",
			endpoint,
		)
	}
	return nil
}

func validateOpenAIResponsesConversationSupport(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	conversationID, errMsg := parseOpenAIConversationReference(rawJSON, endpoint)
	if errMsg != nil {
		return errMsg
	}
	if conversationID == "" {
		return nil
	}
	if previousResponseID := strings.TrimSpace(gjson.GetBytes(rawJSON, "previous_response_id").String()); previousResponseID != "" {
		return invalidOpenAIRequestWithDetailf(
			"conversation",
			"invalid_value",
			"conversation cannot be used together with previous_response_id on /v1/%s",
			endpoint,
		)
	}
	return nil
}

func validateOpenAIChatCompletionsConversationSupport(rawJSON []byte) *interfaces.ErrorMessage {
	conversationID, errMsg := parseOpenAIConversationReference(rawJSON, "chat/completions")
	if errMsg != nil {
		return errMsg
	}
	if conversationID == "" {
		return nil
	}
	return invalidOpenAIRequestWithDetail(
		"conversation is not supported on /v1/chat/completions",
		"conversation",
		"invalid_value",
	)
}

func validateOpenAIChatCompletionsVerbosity(rawJSON []byte) *interfaces.ErrorMessage {
	verbosity := gjson.GetBytes(rawJSON, "verbosity")
	if !verbosity.Exists() || verbosity.Type == gjson.Null {
		return nil
	}
	if verbosity.Type != gjson.String {
		return invalidOpenAIType("verbosity", "a string", verbosity)
	}

	value := strings.ToLower(strings.TrimSpace(verbosity.String()))
	if _, ok := supportedOpenAIChatCompletionsVerbosityValues[value]; ok {
		return nil
	}
	return invalidOpenAIValue("verbosity", "Invalid value for 'verbosity': expected one of low, medium, or high, but got %q instead.", verbosity.String())
}

func validateOpenAIChatCompletionsWebSearchOptions(rawJSON []byte) *interfaces.ErrorMessage {
	options := gjson.GetBytes(rawJSON, "web_search_options")
	if !options.Exists() || options.Type == gjson.Null {
		return nil
	}
	if !options.IsObject() {
		return invalidOpenAIType("web_search_options", "an object", options)
	}

	searchContextSize := options.Get("search_context_size")
	if searchContextSize.Exists() && searchContextSize.Type != gjson.Null {
		if searchContextSize.Type != gjson.String {
			return invalidOpenAIType("web_search_options.search_context_size", "a string", searchContextSize)
		}
		value := strings.ToLower(strings.TrimSpace(searchContextSize.String()))
		if _, ok := supportedOpenAIChatCompletionsWebSearchContextSizeValues[value]; !ok {
			return invalidOpenAIValue(
				"web_search_options.search_context_size",
				"Invalid value for 'web_search_options.search_context_size': expected one of low, medium, or high, but got %q instead.",
				searchContextSize.String(),
			)
		}
	}

	userLocation := options.Get("user_location")
	if !userLocation.Exists() || userLocation.Type == gjson.Null {
		return nil
	}
	if !userLocation.IsObject() {
		return invalidOpenAIType("web_search_options.user_location", "an object", userLocation)
	}

	locationType := userLocation.Get("type")
	if locationType.Exists() && locationType.Type != gjson.Null {
		if locationType.Type != gjson.String {
			return invalidOpenAIType("web_search_options.user_location.type", "a string", locationType)
		}
		if value := strings.ToLower(strings.TrimSpace(locationType.String())); value != "approximate" {
			return invalidOpenAIValue(
				"web_search_options.user_location.type",
				"Invalid value for 'web_search_options.user_location.type': expected %q, but got %q instead.",
				"approximate",
				locationType.String(),
			)
		}
	}

	approximate := userLocation.Get("approximate")
	if approximate.Exists() && approximate.Type != gjson.Null {
		if !approximate.IsObject() {
			return invalidOpenAIType("web_search_options.user_location.approximate", "an object", approximate)
		}
		for _, field := range []string{"city", "country", "region", "timezone"} {
			value := approximate.Get(field)
			if !value.Exists() || value.Type == gjson.Null {
				continue
			}
			if value.Type != gjson.String {
				return invalidOpenAIType("web_search_options.user_location.approximate."+field, "a string", value)
			}
		}
	}

	return nil
}

func validateOpenAIIncludeSupport(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	include := gjson.GetBytes(rawJSON, "include")
	if !include.Exists() {
		return nil
	}
	if !include.IsArray() {
		return invalidOpenAIRequestWithDetailf("include", "invalid_type", "include must be an array on /v1/%s", endpoint)
	}
	if len(include.Array()) == 0 {
		return nil
	}

	values, errMsg := parseOpenAIIncludeValues(include, fmt.Sprintf("/v1/%s", endpoint))
	if errMsg != nil {
		return errMsg
	}
	if endpoint != "responses" {
		return invalidOpenAIRequestWithDetailf("include", "unsupported_parameter", "include is not supported on /v1/%s", endpoint)
	}
	return validateSupportedOpenAIResponsesIncludeValues(values, fmt.Sprintf("/v1/%s", endpoint))
}

func parseOpenAIIncludeValues(include gjson.Result, endpointPath string) ([]string, *interfaces.ErrorMessage) {
	items := include.Array()
	values := make([]string, 0, len(items))
	for index, item := range items {
		if item.Type != gjson.String {
			return nil, invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("include[%d]", index),
				"invalid_type",
				"include[%d] must be a string on %s",
				index,
				endpointPath,
			)
		}
		value := strings.TrimSpace(item.String())
		if value == "" {
			return nil, invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("include[%d]", index),
				"invalid_value",
				"include[%d] must be a non-empty string on %s",
				index,
				endpointPath,
			)
		}
		values = append(values, value)
	}
	return values, nil
}

func validateSupportedOpenAIResponsesIncludeValues(values []string, endpointPath string) *interfaces.ErrorMessage {
	for index, value := range values {
		if _, ok := supportedOpenAIResponsesIncludeValueSet[value]; ok {
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			fmt.Sprintf("include[%d]", index),
			"invalid_value",
			"include[%d]=%q is not supported on %s; supported values are %s",
			index,
			value,
			endpointPath,
			strings.Join(supportedOpenAIResponsesIncludeValues, ", "),
		)
	}
	return nil
}

func validateOpenAIResponsesUnsupportedExecutionControls(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	background := gjson.GetBytes(rawJSON, "background")
	if background.Exists() && background.Type != gjson.Null {
		if background.Type != gjson.True && background.Type != gjson.False {
			return invalidOpenAIType("background", "a boolean", background)
		}
		if background.Bool() {
			if endpoint != "responses" {
				return invalidOpenAIValue(
					"background",
					"Invalid value for 'background': background=true is not supported on /v1/%s because asynchronous response lifecycle handling is not implemented.",
					endpoint,
				)
			}
			if gjson.GetBytes(rawJSON, "stream").Bool() {
				return invalidOpenAIValue(
					"background",
					"Invalid value for 'background': background=true cannot be combined with stream=true on /v1/%s until asynchronous event replay is implemented.",
					endpoint,
				)
			}
			store := gjson.GetBytes(rawJSON, "store")
			if store.Exists() && store.Type == gjson.False {
				return invalidOpenAIValue(
					"background",
					"Invalid value for 'background': background=true requires store=true on /v1/%s because polling and cancellation depend on a stored response object.",
					endpoint,
				)
			}
		}
	}

	if errMsg := validateOpenAIResponsesTruncation(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	return nil
}

func validateOpenAIResponsesTruncation(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	truncation := gjson.GetBytes(rawJSON, "truncation")
	if !truncation.Exists() || truncation.Type == gjson.Null {
		return nil
	}
	if truncation.Type != gjson.String {
		return invalidOpenAIType("truncation", "a string", truncation)
	}

	value := strings.ToLower(strings.TrimSpace(truncation.String()))
	switch value {
	case "":
		return invalidOpenAIValue("truncation", "Invalid value for 'truncation': expected one of auto or disabled, but got %q instead.", truncation.String())
	case "disabled":
		return nil
	case "auto":
		if endpoint == "responses" {
			return nil
		}
		return invalidOpenAIValue(
			"truncation",
			"Invalid value for 'truncation': truncation=auto is not supported on /v1/%s because proxy-side context trimming is not implemented.",
			endpoint,
		)
	default:
		return invalidOpenAIValue("truncation", "Invalid value for 'truncation': expected one of auto or disabled, but got %q instead.", truncation.String())
	}
}

func validateOpenAIResponsesUnsupportedRequestFeatures(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIResponsesPrompt(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesContextManagement(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesPromptCacheKey(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesPromptCacheRetention(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesSafetyIdentifier(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesUser(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesTextOptions(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesReasoning(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	if errMsg := validateOpenAIResponsesStreamOptions(rawJSON, endpoint); errMsg != nil {
		return errMsg
	}

	return nil
}

func validateOpenAIResponsesParallelToolCalls(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	parallelToolCalls := gjson.GetBytes(rawJSON, "parallel_tool_calls")
	if !parallelToolCalls.Exists() || parallelToolCalls.Type == gjson.Null {
		return nil
	}
	if parallelToolCalls.Type != gjson.True && parallelToolCalls.Type != gjson.False {
		return invalidOpenAIType("parallel_tool_calls", "a boolean", parallelToolCalls)
	}
	return nil
}

func validateOpenAIOptionalIntegerField(rawJSON []byte, field string) *interfaces.ErrorMessage {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if value.Type != gjson.Number || float64(value.Int()) != value.Float() {
		return invalidOpenAIType(field, "an integer", value)
	}
	return nil
}

func validateOpenAIOptionalNumberField(rawJSON []byte, field string) *interfaces.ErrorMessage {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if value.Type != gjson.Number {
		return invalidOpenAIType(field, "a number", value)
	}
	return nil
}

func validateOpenAIOptionalBooleanField(rawJSON []byte, field string) *interfaces.ErrorMessage {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if value.Type != gjson.True && value.Type != gjson.False {
		return invalidOpenAIType(field, "a boolean", value)
	}
	return nil
}

func validateOpenAIOptionalStopField(rawJSON []byte) *interfaces.ErrorMessage {
	value := gjson.GetBytes(rawJSON, "stop")
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}

	if value.Type == gjson.String {
		return nil
	}
	if value.Type != gjson.JSON || !value.IsArray() {
		return invalidOpenAIType("stop", "a string or an array of strings", value)
	}

	items := value.Array()
	if len(items) > 4 {
		return invalidOpenAIValue("stop", "Invalid value for 'stop': expected at most 4 stop sequences, but got %d instead.", len(items))
	}
	for index, item := range items {
		if item.Type != gjson.String {
			return invalidOpenAIType(fmt.Sprintf("stop[%d]", index), "a string", item)
		}
	}
	return nil
}

func validateOpenAIOptionalIntegerRangeField(rawJSON []byte, field string, minValue int64, maxValue int64) *interfaces.ErrorMessage {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if errMsg := validateOpenAIOptionalIntegerField(rawJSON, field); errMsg != nil {
		return errMsg
	}

	intValue := value.Int()
	if intValue < minValue || intValue > maxValue {
		return invalidOpenAIValue(field, "Invalid value for '%s': expected an integer between %d and %d, but got %d instead.", field, minValue, maxValue, intValue)
	}
	return nil
}

func validateOpenAIOptionalNumberRangeField(rawJSON []byte, field string, minValue float64, maxValue float64) *interfaces.ErrorMessage {
	value := gjson.GetBytes(rawJSON, field)
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if errMsg := validateOpenAIOptionalNumberField(rawJSON, field); errMsg != nil {
		return errMsg
	}

	numberValue := value.Float()
	if numberValue < minValue || numberValue > maxValue {
		return invalidOpenAIValue(field, "Invalid value for '%s': expected a number between %g and %g, but got %g instead.", field, minValue, maxValue, numberValue)
	}
	return nil
}

func validateOpenAIResponsesMaxToolCalls(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	return validateOpenAIOptionalIntegerField(rawJSON, "max_tool_calls")
}

func validateOpenAIResponsesMaxOutputTokens(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIOptionalIntegerField(rawJSON, "max_output_tokens"); errMsg != nil {
		return errMsg
	}
	return validateOpenAIOptionalIntegerField(rawJSON, "max_tokens")
}

func validateOpenAIResponsesTopLogprobs(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	return validateOpenAIOptionalIntegerRangeField(rawJSON, "top_logprobs", 0, 20)
}

func validateOpenAIResponsesTemperature(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	return validateOpenAIOptionalNumberRangeField(rawJSON, "temperature", 0, 2)
}

func validateOpenAIResponsesTopP(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	return validateOpenAIOptionalNumberRangeField(rawJSON, "top_p", 0, 1)
}

func validateOpenAIResponsesServiceTier(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	serviceTier := gjson.GetBytes(rawJSON, "service_tier")
	if !serviceTier.Exists() || serviceTier.Type == gjson.Null {
		return nil
	}
	if serviceTier.Type != gjson.String {
		return invalidOpenAIType("service_tier", "a string", serviceTier)
	}

	value := strings.ToLower(strings.TrimSpace(serviceTier.String()))
	if _, ok := supportedOpenAIResponsesServiceTierValues[value]; ok {
		return nil
	}
	return invalidOpenAIValue("service_tier", "Invalid value for 'service_tier': expected one of auto, default, flex, scale, or priority, but got %q instead.", serviceTier.String())
}

func validateOpenAIResponsesPrompt(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	prompt := gjson.GetBytes(rawJSON, "prompt")
	if !prompt.Exists() || prompt.Type == gjson.Null {
		return nil
	}
	if endpoint != "responses" {
		return invalidOpenAIValue(
			"prompt",
			"Invalid value for 'prompt': prompt is not supported on /v1/%s because prompt template resolution is not implemented.",
			endpoint,
		)
	}
	if !prompt.IsObject() {
		return invalidOpenAIType("prompt", "an object", prompt)
	}
	if id := prompt.Get("id"); !id.Exists() || id.Type == gjson.Null {
		return missingOpenAIRequiredParameter("prompt.id")
	} else if id.Type != gjson.String {
		return invalidOpenAIType("prompt.id", "a string", id)
	} else if strings.TrimSpace(id.String()) == "" {
		return invalidOpenAIValue("prompt.id", "Invalid value for 'prompt.id': expected a non-empty string.")
	}
	if version := prompt.Get("version"); version.Exists() && version.Type != gjson.String {
		return invalidOpenAIType("prompt.version", "a string", version)
	}
	if variables := prompt.Get("variables"); variables.Exists() && !variables.IsObject() {
		return invalidOpenAIType("prompt.variables", "an object", variables)
	}
	return nil
}

func validateOpenAIResponsesContextManagement(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	contextManagement := gjson.GetBytes(rawJSON, "context_management")
	if !contextManagement.Exists() || contextManagement.Type == gjson.Null {
		return nil
	}
	if !contextManagement.IsArray() {
		return invalidOpenAIType("context_management", "an array", contextManagement)
	}

	items := contextManagement.Array()
	if len(items) == 0 {
		return nil
	}
	if endpoint != "responses" {
		return invalidOpenAIValue("context_management", "Invalid value for 'context_management': context_management is only supported on /v1/responses.")
	}

	for index, item := range items {
		param := fmt.Sprintf("context_management[%d]", index)
		if !item.IsObject() {
			return invalidOpenAIType(param, "an object", item)
		}
		itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
		if itemType != "compaction" {
			return invalidOpenAIValue(
				param+".type",
				"Invalid value for '%s': expected 'compaction', but got %q instead.",
				param+".type",
				item.Get("type").String(),
			)
		}
		threshold := item.Get("compact_threshold")
		if !threshold.Exists() {
			return missingOpenAIRequiredParameter(param + ".compact_threshold")
		}
		if threshold.Type != gjson.Number {
			return invalidOpenAIType(param+".compact_threshold", "an integer", threshold)
		}
		if threshold.Int() <= 0 || float64(threshold.Int()) != threshold.Float() {
			return invalidOpenAIValue(
				param+".compact_threshold",
				"Invalid value for '%s': expected a positive integer, but got %s instead.",
				param+".compact_threshold",
				strings.TrimSpace(threshold.Raw),
			)
		}
	}

	return nil
}

func validateOpenAIResponsesPromptCacheKey(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	promptCacheKey := gjson.GetBytes(rawJSON, "prompt_cache_key")
	if !promptCacheKey.Exists() || promptCacheKey.Type == gjson.Null {
		return nil
	}
	if promptCacheKey.Type != gjson.String {
		return invalidOpenAIType("prompt_cache_key", "a string", promptCacheKey)
	}
	return nil
}

func validateOpenAIResponsesPromptCacheRetention(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	promptCacheRetention := gjson.GetBytes(rawJSON, "prompt_cache_retention")
	if !promptCacheRetention.Exists() || promptCacheRetention.Type == gjson.Null {
		return nil
	}
	if promptCacheRetention.Type != gjson.String {
		return invalidOpenAIType("prompt_cache_retention", "a string", promptCacheRetention)
	}

	value := strings.ToLower(strings.TrimSpace(promptCacheRetention.String()))
	switch value {
	case "":
		return invalidOpenAIValue("prompt_cache_retention", "Invalid value for 'prompt_cache_retention': expected one of in-memory or 24h, but got %q instead.", promptCacheRetention.String())
	case "in-memory", "24h":
		if endpoint == "responses" {
			return nil
		}
		return invalidOpenAIValue(
			"prompt_cache_retention",
			"Invalid value for 'prompt_cache_retention': prompt_cache_retention is not supported on /v1/%s because prompt cache retention controls are not implemented.",
			endpoint,
		)
	default:
		return invalidOpenAIValue("prompt_cache_retention", "Invalid value for 'prompt_cache_retention': expected one of in-memory or 24h, but got %q instead.", promptCacheRetention.String())
	}
}

func validateOpenAIResponsesSafetyIdentifier(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	safetyIdentifier := gjson.GetBytes(rawJSON, "safety_identifier")
	if !safetyIdentifier.Exists() || safetyIdentifier.Type == gjson.Null {
		return nil
	}
	if safetyIdentifier.Type != gjson.String {
		return invalidOpenAIType("safety_identifier", "a string", safetyIdentifier)
	}
	return nil
}

func validateOpenAIResponsesUser(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	user := gjson.GetBytes(rawJSON, "user")
	if !user.Exists() || user.Type == gjson.Null {
		return nil
	}
	if user.Type != gjson.String {
		return invalidOpenAIType("user", "a string", user)
	}
	return nil
}

func validateOpenAIResponsesTextOptions(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	text := gjson.GetBytes(rawJSON, "text")
	if !text.Exists() || text.Type == gjson.Null {
		return nil
	}
	if !text.IsObject() {
		return invalidOpenAIType("text", "an object", text)
	}
	if errMsg := validateOpenAIResponsesTextFormat(text.Get("format")); errMsg != nil {
		return errMsg
	}

	verbosity := text.Get("verbosity")
	if !verbosity.Exists() || verbosity.Type == gjson.Null {
		return nil
	}
	if verbosity.Type != gjson.String {
		return invalidOpenAIType("text.verbosity", "a string", verbosity)
	}

	value := strings.ToLower(strings.TrimSpace(verbosity.String()))
	if _, ok := supportedOpenAIResponsesVerbosityValues[value]; ok {
		return nil
	}
	return invalidOpenAIValue("text.verbosity", "Invalid value for 'text.verbosity': expected one of low, medium, or high, but got %q instead.", verbosity.String())
}

func validateOpenAIResponsesTextFormat(format gjson.Result) *interfaces.ErrorMessage {
	if !format.Exists() || format.Type == gjson.Null {
		return nil
	}
	if !format.IsObject() {
		return invalidOpenAIType("text.format", "an object", format)
	}

	formatType := format.Get("type")
	if !formatType.Exists() || formatType.Type == gjson.Null {
		return missingOpenAIRequiredParameter("text.format.type")
	}
	if formatType.Type != gjson.String {
		return invalidOpenAIType("text.format.type", "a string", formatType)
	}

	value := strings.ToLower(strings.TrimSpace(formatType.String()))
	switch value {
	case "text", "json_object":
		return nil
	case "json_schema":
		name := format.Get("name")
		if !name.Exists() || name.Type == gjson.Null {
			return missingOpenAIRequiredParameter("text.format.name")
		}
		if name.Type != gjson.String {
			return invalidOpenAIType("text.format.name", "a string", name)
		}
		if strings.TrimSpace(name.String()) == "" {
			return invalidOpenAIValue("text.format.name", "Invalid value for 'text.format.name': expected a non-empty string.")
		}

		schema := format.Get("schema")
		if !schema.Exists() || schema.Type == gjson.Null {
			return missingOpenAIRequiredParameter("text.format.schema")
		}
		if !schema.IsObject() {
			return invalidOpenAIType("text.format.schema", "an object", schema)
		}
		return nil
	case "":
		return invalidOpenAIValue("text.format.type", "Invalid value for 'text.format.type': expected a non-empty string.")
	default:
		return invalidOpenAIValue(
			"text.format.type",
			"Invalid value for 'text.format.type': expected one of text, json_object, or json_schema, but got %q instead.",
			formatType.String(),
		)
	}
}

func validateOpenAIResponsesReasoning(rawJSON []byte, _ string) *interfaces.ErrorMessage {
	reasoning := gjson.GetBytes(rawJSON, "reasoning")
	if !reasoning.Exists() || reasoning.Type == gjson.Null {
		return nil
	}
	if !reasoning.IsObject() {
		return invalidOpenAIType("reasoning", "an object", reasoning)
	}

	effort := reasoning.Get("effort")
	if !effort.Exists() || effort.Type == gjson.Null {
		return nil
	}
	if effort.Type != gjson.String {
		return invalidOpenAIType("reasoning.effort", "a string", effort)
	}

	value := strings.ToLower(strings.TrimSpace(effort.String()))
	if _, ok := supportedOpenAIResponsesReasoningEffortValues[value]; ok {
		return nil
	}
	return invalidOpenAIValue("reasoning.effort", "Invalid value for 'reasoning.effort': expected one of none, minimal, low, medium, high, or xhigh, but got %q instead.", effort.String())
}

func validateOpenAIResponsesStreamOptions(rawJSON []byte, endpoint string) *interfaces.ErrorMessage {
	streamOptions := gjson.GetBytes(rawJSON, "stream_options")
	if !streamOptions.Exists() || streamOptions.Type == gjson.Null {
		return nil
	}
	if !streamOptions.IsObject() {
		return invalidOpenAIType("stream_options", "an object", streamOptions)
	}

	options := streamOptions.Map()
	if len(options) == 0 {
		return invalidOpenAIValue(
			"stream_options",
			"Invalid value for 'stream_options': stream_options is not supported on /v1/%s because response stream obfuscation controls are not implemented.",
			endpoint,
		)
	}
	includeObfuscation, ok := options["include_obfuscation"]
	if !ok || len(options) != 1 {
		return invalidOpenAIValue(
			"stream_options",
			"Invalid value for 'stream_options': stream_options is not supported on /v1/%s because response stream obfuscation controls are not implemented.",
			endpoint,
		)
	}
	if includeObfuscation.Type != gjson.True && includeObfuscation.Type != gjson.False {
		return invalidOpenAIType("stream_options.include_obfuscation", "a boolean", includeObfuscation)
	}
	if endpoint != "responses" {
		return invalidOpenAIValue(
			"stream_options",
			"Invalid value for 'stream_options': stream_options is not supported on /v1/%s because response stream obfuscation controls are not implemented.",
			endpoint,
		)
	}
	if !gjson.GetBytes(rawJSON, "stream").Bool() {
		return invalidOpenAIValue(
			"stream_options",
			"Invalid value for 'stream_options': stream_options requires stream=true on /v1/%s.",
			endpoint,
		)
	}
	return nil
}

func validateOpenAIResponsesNonEmptyStringField(item gjson.Result, index int, _ string, field string) *interfaces.ErrorMessage {
	value := item.Get(field)
	param := fmt.Sprintf("input[%d].%s", index, field)
	if !value.Exists() {
		return missingOpenAIRequiredParameter(param)
	}
	if value.Type != gjson.String {
		return invalidOpenAIType(param, "a string", value)
	}
	if strings.TrimSpace(value.String()) == "" {
		return invalidOpenAIValue(param, "Invalid value for '%s': expected a non-empty string.", param)
	}
	return nil
}

func validateOpenAIResponsesStringField(item gjson.Result, index int, _ string, field string) *interfaces.ErrorMessage {
	value := item.Get(field)
	param := fmt.Sprintf("input[%d].%s", index, field)
	if !value.Exists() {
		return missingOpenAIRequiredParameter(param)
	}
	if value.Type != gjson.String {
		return invalidOpenAIType(param, "a string", value)
	}
	return nil
}

func validateOpenAIResponsesInput(rawJSON []byte) *interfaces.ErrorMessage {
	input := gjson.GetBytes(rawJSON, "input")
	if !input.Exists() || !input.IsArray() {
		return nil
	}

	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		switch itemType {
		case "function_call":
			if errMsg := validateOpenAIResponsesNonEmptyStringField(item, index, itemType, "call_id"); errMsg != nil {
				return errMsg
			}
			if errMsg := validateOpenAIResponsesNonEmptyStringField(item, index, itemType, "name"); errMsg != nil {
				return errMsg
			}
			if errMsg := validateOpenAIResponsesStringField(item, index, itemType, "arguments"); errMsg != nil {
				return errMsg
			}
		case "function_call_output":
			if errMsg := validateOpenAIResponsesNonEmptyStringField(item, index, itemType, "call_id"); errMsg != nil {
				return errMsg
			}
			output := item.Get("output")
			param := fmt.Sprintf("input[%d].output", index)
			if !output.Exists() {
				return missingOpenAIRequiredParameter(param)
			}
			if output.Type != gjson.String && !output.IsArray() {
				return invalidOpenAIType(param, "one of a string or array of objects", output)
			}
		case "custom_tool_call":
			if errMsg := validateOpenAIResponsesNonEmptyStringField(item, index, itemType, "call_id"); errMsg != nil {
				return errMsg
			}
			if errMsg := validateOpenAIResponsesNonEmptyStringField(item, index, itemType, "name"); errMsg != nil {
				return errMsg
			}
			if errMsg := validateOpenAIResponsesStringField(item, index, itemType, "input"); errMsg != nil {
				return errMsg
			}
		case "custom_tool_call_output":
			if errMsg := validateOpenAIResponsesNonEmptyStringField(item, index, itemType, "call_id"); errMsg != nil {
				return errMsg
			}
			output := item.Get("output")
			param := fmt.Sprintf("input[%d].output", index)
			if !output.Exists() {
				return missingOpenAIRequiredParameter(param)
			}
			if output.Type != gjson.String && !output.IsArray() {
				return invalidOpenAIType(param, "one of a string or array of objects", output)
			}
		}
	}

	return nil
}

func validateOpenAIResponsesInputItemTypeSupport(rawJSON []byte, modelID string, providers []string) *interfaces.ErrorMessage {
	input := gjson.GetBytes(rawJSON, "input")
	if !input.Exists() || !input.IsArray() {
		return nil
	}
	if openAIResponsesRouteSupportsNativeInputItems(modelID, providers) {
		return nil
	}
	supportsBridgedCustomTools := openAIResponsesRouteSupportsBridgedCustomTools(modelID, providers)

	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "" && strings.TrimSpace(item.Get("role").String()) != "" {
			itemType = "message"
		}

		switch itemType {
		case "", "message", "function_call", "function_call_output":
			continue
		case "custom_tool_call", "custom_tool_call_output":
			if supportsBridgedCustomTools {
				continue
			}
		case "item_reference":
			return invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("input[%d].type", index),
				"invalid_value",
				"input[%d].type=%q is not supported on /v1/responses for the selected model route because this provider cannot resolve prior response item references; use previous_response_id for native continuation or a native OpenAI Responses route for manual item replay",
				index,
				itemType,
			)
		case "reasoning":
			return invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("input[%d].type", index),
				"invalid_value",
				"input[%d].type=%q is not supported on /v1/responses for the selected model route because this provider cannot accept prior reasoning items as input; use previous_response_id for native continuation or a native OpenAI Responses route for manual item replay",
				index,
				itemType,
			)
		default:
			return invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("input[%d].type", index),
				"invalid_value",
				"input[%d].type=%q is not supported on /v1/responses for the selected model route; supported item types are %s",
				index,
				itemType,
				strings.Join(openAIResponsesSupportedInputItemTypes(modelID, providers), ", "),
			)
		}
	}

	return nil
}

func validateOpenAIResponsesMessageContentTypeSupport(rawJSON []byte, modelID string, providers []string) *interfaces.ErrorMessage {
	input := gjson.GetBytes(rawJSON, "input")
	if !input.Exists() || !input.IsArray() {
		return nil
	}
	if openAIResponsesRouteSupportsNativeInputItems(modelID, providers) {
		return nil
	}

	supported := openAIResponsesSupportedMessageContentTypes(modelID, providers)
	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "" && strings.TrimSpace(item.Get("role").String()) != "" {
			itemType = "message"
		}
		if itemType != "message" {
			continue
		}
		if errMsg := validateOpenAIResponsesMessageContentItemSupport(item, index, supported); errMsg != nil {
			return errMsg
		}
	}

	return nil
}

func validateOpenAIResponsesToolOutputItemSupport(rawJSON []byte, modelID string, providers []string) *interfaces.ErrorMessage {
	input := gjson.GetBytes(rawJSON, "input")
	if !input.Exists() || !input.IsArray() {
		return nil
	}
	if openAIResponsesRouteSupportsNativeInputItems(modelID, providers) {
		return nil
	}

	supported := []string{"input_text", "output_text"}
	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
			continue
		}

		output := item.Get("output")
		if !output.Exists() || !output.IsArray() {
			continue
		}

		for outputIndex, outputItem := range output.Array() {
			outputType := strings.TrimSpace(outputItem.Get("type").String())
			if outputType == "" && outputItem.Get("text").Exists() {
				outputType = "input_text"
			}
			if outputType == "input_text" || outputType == "output_text" {
				continue
			}
			return invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("input[%d].output[%d].type", index, outputIndex),
				"invalid_value",
				"input[%d].output[%d].type=%q is not supported on /v1/responses for the selected model route because bridged tool outputs can only preserve text items; supported tool output item types are %s",
				index,
				outputIndex,
				outputType,
				strings.Join(supported, ", "),
			)
		}
	}

	return nil
}

func validateOpenAIResponsesToolDefinitionSupport(rawJSON []byte, modelID string, providers []string) *interfaces.ErrorMessage {
	if !openAIResponsesRouteSupportsBridgedCustomTools(modelID, providers) || openAIResponsesRouteSupportsNativeInputItems(modelID, providers) {
		return nil
	}

	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return nil
	}

	for index, tool := range tools.Array() {
		switch strings.ToLower(strings.TrimSpace(tool.Get("type").String())) {
		case "", "function":
			if errMsg := validateOpenAIAuggieToolDeferredLoading(tool.Get("defer_loading"), index); errMsg != nil {
				return errMsg
			}
			if errMsg := validateOpenAIAuggieFunctionToolStrictMode(tool.Get("strict"), index); errMsg != nil {
				return errMsg
			}
		case "custom":
			if errMsg := validateOpenAIAuggieToolDeferredLoading(tool.Get("defer_loading"), index); errMsg != nil {
				return errMsg
			}
			if errMsg := validateOpenAIAuggieCustomToolFormat(tool.Get("format"), index); errMsg != nil {
				return errMsg
			}
		case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26":
			if errMsg := validateOpenAIAuggieBuiltInWebSearchToolConfig(tool, index); errMsg != nil {
				return errMsg
			}
		}
	}

	return nil
}

func validateOpenAIResponsesProviderRequestFeatureSupport(rawJSON []byte, modelID string, providers []string) *interfaces.ErrorMessage {
	if !openAIResponsesRouteSupportsBridgedCustomTools(modelID, providers) || openAIResponsesRouteSupportsNativeInputItems(modelID, providers) {
		return nil
	}
	if errMsg := validateOpenAIAuggieIncludeRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieReasoningRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggiePromptCacheAndSafetyRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieServiceTierRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieTruncationRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggiePromptTemplateRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieContextManagementRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieTextFormatRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieToolChoiceRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	return validateOpenAIAuggieSamplingControlRequestSupport(rawJSON)
}

func validateOpenAIAuggieIncludeRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	include := gjson.GetBytes(rawJSON, "include")
	if !include.Exists() || !include.IsArray() {
		return nil
	}

	for index, item := range include.Array() {
		if item.Type != gjson.String {
			continue
		}
		value := strings.TrimSpace(item.String())
		if value == "message.output_text.logprobs" {
			return invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("include[%d]", index),
				"invalid_value",
				"include[%d]=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve output text logprobs; use a native OpenAI Responses route",
				index,
				value,
			)
		}
		if _, ok := supportedOpenAIAuggieIncludeValueSet[value]; ok {
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			fmt.Sprintf("include[%d]", index),
			"invalid_value",
			"include[%d]=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve this expanded include shape; supported include values on this route are %s, or use a native OpenAI Responses route",
			index,
			value,
			strings.Join(supportedOpenAIAuggieIncludeValues, ", "),
		)
	}
	return nil
}

func validateOpenAIAuggieReasoningRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	reasoning := gjson.GetBytes(rawJSON, "reasoning")
	if !reasoning.Exists() || reasoning.Type == gjson.Null || !reasoning.IsObject() {
		return nil
	}

	// Accept and ignore reasoning summary controls for compatibility with
	// native OpenAI Responses payloads. The Auggie bridge only preserves
	// reasoning effort.
	if effort := reasoning.Get("effort"); effort.Exists() && effort.Type == gjson.String {
		value := strings.ToLower(strings.TrimSpace(effort.String()))
		if value == "" {
			return nil
		}
		if _, ok := supportedOpenAIAuggieReasoningEffortValues[value]; ok {
			return nil
		}
		return invalidOpenAIRequestWithDetailf(
			"reasoning.effort",
			"invalid_value",
			"reasoning.effort=%q is not supported on the selected /v1/responses model route because Auggie's bridge only preserves low, medium, or high native reasoning effort; use one of those values or a native OpenAI Responses route",
			effort.String(),
		)
	}
	return nil
}

func validateOpenAIAuggieServiceTierRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	serviceTier := gjson.GetBytes(rawJSON, "service_tier")
	if !serviceTier.Exists() || serviceTier.Type == gjson.Null {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"service_tier",
		"invalid_value",
		"service_tier is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve service tier controls; use a native OpenAI Responses route",
	)
}

func validateOpenAIAuggieTruncationRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	truncation := strings.ToLower(strings.TrimSpace(gjson.GetBytes(rawJSON, "truncation").String()))
	if truncation != "auto" {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"truncation",
		"invalid_value",
		"truncation=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve automatic truncation controls; use truncation=%q or a native OpenAI Responses route",
		truncation,
		"disabled",
	)
}

func validateOpenAIAuggiePromptTemplateRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	prompt := gjson.GetBytes(rawJSON, "prompt")
	if !prompt.Exists() || prompt.Type == gjson.Null {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"prompt",
		"invalid_value",
		"prompt is not supported on the selected /v1/responses model route because Auggie's bridge cannot resolve or preserve prompt template references; inline the prompt content in instructions/input or use a native OpenAI Responses route",
	)
}

func validateOpenAIAuggieContextManagementRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	contextManagement := gjson.GetBytes(rawJSON, "context_management")
	if !contextManagement.Exists() || contextManagement.Type == gjson.Null {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"context_management",
		"invalid_value",
		"context_management is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve native compaction controls on regular responses; use /v1/responses/compact or a native OpenAI Responses route",
	)
}

func validateOpenAIAuggiePromptCacheAndSafetyRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	// Accept and ignore prompt-cache / safety attribution controls for
	// compatibility with native OpenAI Responses payloads. The Auggie bridge
	// does not preserve these controls end-to-end.
	return nil
}

func validateOpenAIAuggieTextFormatRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	formatType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(rawJSON, "text.format.type").String()))
	switch formatType {
	case "", "text":
		return nil
	case "json_schema", "json_object":
		return invalidOpenAIRequestWithDetailf(
			"text.format.type",
			"invalid_value",
			"text.format.type=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve structured output response formats; use text.format.type=%q or a native OpenAI Responses route",
			formatType,
			"text",
		)
	default:
		return nil
	}
}

func validateOpenAIAuggieToolChoiceRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return nil
	}

	if toolChoice.Type == gjson.String {
		switch value := strings.ToLower(strings.TrimSpace(toolChoice.String())); value {
		case "", "auto", "none":
			return nil
		case "required":
			return invalidOpenAIRequestWithDetailf(
				"tool_choice",
				"invalid_value",
				"tool_choice=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve native forced tool-use semantics; use tool_choice=%q, tool_choice=%q, allowed_tools with mode=%q, or a native OpenAI Responses route",
				value,
				"auto",
				"none",
				"auto",
			)
		default:
			return invalidOpenAIRequestWithDetailf(
				"tool_choice",
				"invalid_value",
				"tool_choice=%q is not supported on the selected /v1/responses model route; supported values are %s",
				value,
				"auto, none, allowed_tools with mode=auto, or omitted tool_choice",
			)
		}
	}

	if !toolChoice.IsObject() {
		return invalidOpenAIType("tool_choice", "a string or an object", toolChoice)
	}

	toolChoiceType := strings.ToLower(strings.TrimSpace(toolChoice.Get("type").String()))
	switch {
	case toolChoiceType == "function", toolChoiceType == "custom", isOpenAIAuggieBuiltInWebSearchType(toolChoiceType):
		return invalidOpenAIRequestWithDetailf(
			"tool_choice.type",
			"invalid_value",
			"tool_choice.type=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve native forced tool-use semantics; use tool_choice=%q, tool_choice=%q, allowed_tools with mode=%q, or a native OpenAI Responses route",
			toolChoiceType,
			"auto",
			"none",
			"auto",
		)
	case toolChoiceType == "allowed_tools":
		container := toolChoice.Get("allowed_tools")
		param := "tool_choice.mode"
		if container.Exists() && container.IsObject() {
			param = "tool_choice.allowed_tools.mode"
		} else {
			container = toolChoice
		}
		mode := strings.ToLower(strings.TrimSpace(container.Get("mode").String()))
		if mode == "required" {
			return invalidOpenAIRequestWithDetailf(
				param,
				"invalid_value",
				"%s=%q is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve native forced tool-use semantics; use %s=%q or a native OpenAI Responses route",
				param,
				mode,
				param,
				"auto",
			)
		}
		return validateOpenAIAuggieAllowedToolsToolChoice(toolChoice, true, "/v1/responses")
	}

	if toolChoiceType == "" {
		toolChoiceType = "object"
	}
	return invalidOpenAIRequestWithDetailf(
		"tool_choice.type",
		"invalid_value",
		"tool_choice.type=%q is not supported on the selected /v1/responses model route; supported values are %s",
		toolChoiceType,
		"allowed_tools with mode=auto, or omitted object tool_choice",
	)
}

func validateOpenAIAuggieSamplingControlRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	builtInWebSearch := openAIAuggieRequestUsesBuiltInWebSearchTool(rawJSON)
	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "max_tool_calls", param: "max_tool_calls", description: "tool-call budget controls"},
		{path: "max_output_tokens", param: "max_output_tokens", description: "token budget controls"},
		{path: "max_tokens", param: "max_tokens", description: "token budget controls"},
		{path: "top_logprobs", param: "top_logprobs", description: "log probability controls"},
		{path: "temperature", param: "temperature", description: "sampling controls"},
		{path: "top_p", param: "top_p", description: "sampling controls"},
	}

	for _, control := range unsupportedControls {
		if control.param == "max_tool_calls" && builtInWebSearch {
			continue
		}
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			control.param,
			"invalid_value",
			"%s is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve %s; use a native OpenAI Responses route",
			control.param,
			control.description,
		)
	}
	return nil
}

func openAIAuggieRequestUsesBuiltInWebSearchTool(rawJSON []byte) bool {
	tools := gjson.GetBytes(rawJSON, "tools")
	if tools.Exists() && tools.IsArray() {
		for _, tool := range tools.Array() {
			if isOpenAIAuggieBuiltInWebSearchType(tool.Get("type").String()) {
				return true
			}
		}
	}

	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	return toolChoice.IsObject() && isOpenAIAuggieBuiltInWebSearchType(toolChoice.Get("type").String())
}

func isOpenAIAuggieBuiltInWebSearchType(toolType string) bool {
	switch strings.ToLower(strings.TrimSpace(toolType)) {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26":
		return true
	default:
		return false
	}
}

func validateOpenAIChatCompletionsProviderRequestFeatureSupport(rawJSON []byte, modelID string, providers []string) *interfaces.ErrorMessage {
	if !openAIRouteIncludesAuggieProvider(modelID, providers) {
		return nil
	}
	if errMsg := validateOpenAIRequestMetadata(rawJSON, "metadata"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsVerbosityRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsWebSearchOptionsRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsSamplingControlRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsNStopAndSeedRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsStreamOptionsRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsServiceTierRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsPromptCacheAndUserRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsAudioOutputRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsResponseFormatRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsReasoningEffortRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsToolDefinitionSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIAuggieChatCompletionsLegacyFunctionRequestSupport(rawJSON); errMsg != nil {
		return errMsg
	}
	return validateOpenAIAuggieChatCompletionsToolChoiceRequestSupport(rawJSON)
}

func validateOpenAIAuggieChatCompletionsVerbosityRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIChatCompletionsVerbosity(rawJSON); errMsg != nil {
		return errMsg
	}

	verbosity := gjson.GetBytes(rawJSON, "verbosity")
	if !verbosity.Exists() || verbosity.Type == gjson.Null {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"verbosity",
		"invalid_value",
		"verbosity is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve official chat verbosity controls; use a native OpenAI-compatible route",
	)
}

func validateOpenAIAuggieChatCompletionsWebSearchOptionsRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIChatCompletionsWebSearchOptions(rawJSON); errMsg != nil {
		return errMsg
	}

	options := gjson.GetBytes(rawJSON, "web_search_options")
	if !options.Exists() || options.Type == gjson.Null {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"web_search_options",
		"invalid_value",
		"web_search_options is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve official chat web-search activation and configuration semantics; use a native OpenAI-compatible route",
	)
}

func validateOpenAIAuggieChatCompletionsSamplingControlRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIResponsesMaxOutputTokens(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIOptionalIntegerField(rawJSON, "max_completion_tokens"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIResponsesTopLogprobs(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIOptionalBooleanField(rawJSON, "logprobs"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIResponsesTemperature(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIOptionalNumberRangeField(rawJSON, "frequency_penalty", -2, 2); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIOptionalNumberRangeField(rawJSON, "presence_penalty", -2, 2); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIResponsesTopP(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIChatCompletionsLogitBias(rawJSON); errMsg != nil {
		return errMsg
	}

	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "max_output_tokens", param: "max_output_tokens", description: "token budget controls"},
		{path: "max_tokens", param: "max_tokens", description: "token budget controls"},
		{path: "max_completion_tokens", param: "max_completion_tokens", description: "token budget controls"},
		{path: "top_logprobs", param: "top_logprobs", description: "log probability controls"},
		{path: "logprobs", param: "logprobs", description: "log probability controls"},
		{path: "temperature", param: "temperature", description: "sampling controls"},
		{path: "frequency_penalty", param: "frequency_penalty", description: "penalty controls"},
		{path: "presence_penalty", param: "presence_penalty", description: "penalty controls"},
		{path: "top_p", param: "top_p", description: "sampling controls"},
		{path: "logit_bias", param: "logit_bias", description: "token logit bias controls"},
	}

	for _, control := range unsupportedControls {
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			control.param,
			"invalid_value",
			"%s is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve %s; use a native OpenAI-compatible route",
			control.param,
			control.description,
		)
	}
	return nil
}

func validateOpenAIRequestMetadata(rawJSON []byte, field string) *interfaces.ErrorMessage {
	metadata := gjson.GetBytes(rawJSON, field)
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return nil
	}
	if !metadata.IsObject() {
		return invalidOpenAIType(field, "an object", metadata)
	}

	items := metadata.Map()
	if len(items) > openAIMetadataMaxPairs {
		return invalidOpenAIValue(
			field,
			"Invalid value for '%s': expected at most %d key-value pairs, but got %d instead.",
			field,
			openAIMetadataMaxPairs,
			len(items),
		)
	}
	for key, value := range items {
		itemField := field + "." + key
		if len(key) > openAIMetadataKeyMaxLength {
			return invalidOpenAIValue(
				itemField,
				"Invalid value for '%s': expected a key with maximum length %d, but got length %d instead.",
				itemField,
				openAIMetadataKeyMaxLength,
				len(key),
			)
		}
		if value.Type != gjson.String {
			return invalidOpenAIType(itemField, "a string", value)
		}
		if len(value.String()) > openAIMetadataValueMaxLength {
			return invalidOpenAIValue(
				itemField,
				"Invalid value for '%s': expected a string with maximum length %d, but got length %d instead.",
				itemField,
				openAIMetadataValueMaxLength,
				len(value.String()),
			)
		}
	}
	return nil
}

func validateOpenAIChatCompletionsLogitBias(rawJSON []byte) *interfaces.ErrorMessage {
	logitBias := gjson.GetBytes(rawJSON, "logit_bias")
	if !logitBias.Exists() || logitBias.Type == gjson.Null {
		return nil
	}
	if !logitBias.IsObject() {
		return invalidOpenAIType("logit_bias", "an object", logitBias)
	}

	for key, value := range logitBias.Map() {
		field := "logit_bias." + key
		if value.Type != gjson.Number {
			return invalidOpenAIType(field, "a number", value)
		}
		numberValue := value.Float()
		if numberValue < -100 || numberValue > 100 {
			return invalidOpenAIValue(field, "Invalid value for '%s': expected a number between -100 and 100, but got %g instead.", field, numberValue)
		}
	}
	return nil
}

func validateOpenAIAuggieChatCompletionsNStopAndSeedRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIOptionalIntegerField(rawJSON, "n"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIOptionalStopField(rawJSON); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIOptionalIntegerField(rawJSON, "seed"); errMsg != nil {
		return errMsg
	}

	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "n", param: "n", description: "choice count controls"},
		{path: "stop", param: "stop", description: "stop sequence controls"},
		{path: "seed", param: "seed", description: "determinism controls"},
	}

	for _, control := range unsupportedControls {
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			control.param,
			"invalid_value",
			"%s is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve %s; use a native OpenAI-compatible route",
			control.param,
			control.description,
		)
	}
	return nil
}

func validateOpenAIAuggieChatCompletionsStreamOptionsRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	streamOptions := gjson.GetBytes(rawJSON, "stream_options")
	if !streamOptions.Exists() || streamOptions.Type == gjson.Null {
		return nil
	}
	if !streamOptions.IsObject() {
		return invalidOpenAIType("stream_options", "an object", streamOptions)
	}
	if !gjson.GetBytes(rawJSON, "stream").Bool() {
		return invalidOpenAIValue("stream_options", "stream_options requires stream=true on /v1/chat/completions.")
	}

	for key, value := range streamOptions.Map() {
		switch key {
		case "include_obfuscation", "include_usage":
			if value.Type != gjson.True && value.Type != gjson.False {
				return invalidOpenAIType("stream_options."+key, "a boolean", value)
			}
		default:
			return invalidOpenAIValue("stream_options."+key, "Invalid value for '%s': supported chat stream_options fields are include_obfuscation and include_usage.", "stream_options."+key)
		}
	}

	return invalidOpenAIRequestWithDetailf(
		"stream_options",
		"invalid_value",
		"stream_options is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve chat streaming controls; use a native OpenAI-compatible route",
	)
}

func validateOpenAIAuggieChatCompletionsServiceTierRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIResponsesServiceTier(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}

	serviceTier := gjson.GetBytes(rawJSON, "service_tier")
	if !serviceTier.Exists() || serviceTier.Type == gjson.Null {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"service_tier",
		"invalid_value",
		"service_tier is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve service tier controls; use a native OpenAI-compatible route",
	)
}

func validateOpenAIAuggieChatCompletionsPromptCacheAndUserRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIResponsesPromptCacheKey(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIResponsesPromptCacheRetention(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIResponsesSafetyIdentifier(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}
	if errMsg := validateOpenAIResponsesUser(rawJSON, "responses"); errMsg != nil {
		return errMsg
	}

	unsupportedControls := []struct {
		path        string
		param       string
		description string
	}{
		{path: "prompt_cache_key", param: "prompt_cache_key", description: "prompt cache controls"},
		{path: "prompt_cache_retention", param: "prompt_cache_retention", description: "prompt cache controls"},
		{path: "safety_identifier", param: "safety_identifier", description: "safety attribution controls"},
		{path: "user", param: "user", description: "end-user attribution controls"},
	}

	for _, control := range unsupportedControls {
		value := gjson.GetBytes(rawJSON, control.path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			control.param,
			"invalid_value",
			"%s is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve %s; use a native OpenAI-compatible route",
			control.param,
			control.description,
		)
	}
	return nil
}

func validateOpenAIAuggieChatCompletionsAudioOutputRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	if errMsg := validateOpenAIChatCompletionsModalities(rawJSON); errMsg != nil {
		return errMsg
	}

	audio := gjson.GetBytes(rawJSON, "audio")
	if audio.Exists() && audio.Type != gjson.Null {
		if !audio.IsObject() {
			return invalidOpenAIType("audio", "an object", audio)
		}
		return invalidOpenAIRequestWithDetailf(
			"audio",
			"invalid_value",
			"audio is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve audio output controls; omit audio and use text output only or a native OpenAI-compatible route",
		)
	}

	prediction := gjson.GetBytes(rawJSON, "prediction")
	if prediction.Exists() && prediction.Type != gjson.Null {
		if !prediction.IsObject() {
			return invalidOpenAIType("prediction", "an object", prediction)
		}
		return invalidOpenAIRequestWithDetailf(
			"prediction",
			"invalid_value",
			"prediction is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve predicted output controls; use a native OpenAI-compatible route",
		)
	}

	return nil
}

func validateOpenAIChatCompletionsModalities(rawJSON []byte) *interfaces.ErrorMessage {
	modalities := gjson.GetBytes(rawJSON, "modalities")
	if !modalities.Exists() || modalities.Type == gjson.Null {
		return nil
	}
	if !modalities.IsArray() {
		return invalidOpenAIType("modalities", "an array", modalities)
	}

	items := modalities.Array()
	if len(items) == 0 {
		return invalidOpenAIValue("modalities", "Invalid value for 'modalities': expected a non-empty array containing text and/or audio.")
	}

	hasAudio := false
	for index, item := range items {
		field := fmt.Sprintf("modalities[%d]", index)
		if item.Type != gjson.String {
			return invalidOpenAIType(field, "a string", item)
		}
		switch value := strings.ToLower(strings.TrimSpace(item.String())); value {
		case "text":
		case "audio":
			hasAudio = true
		default:
			return invalidOpenAIValue(field, "Invalid value for '%s': expected one of text or audio, but got %q instead.", field, item.String())
		}
	}

	if hasAudio {
		return invalidOpenAIRequestWithDetailf(
			"modalities",
			"invalid_value",
			"modalities including %q are not supported on the selected /v1/chat/completions model route because Auggie's bridge only preserves text output; use modalities=%s or omit the field entirely",
			"audio",
			`["text"]`,
		)
	}

	return nil
}

func validateOpenAIAuggieChatCompletionsResponseFormatRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	responseFormat := gjson.GetBytes(rawJSON, "response_format")
	if !responseFormat.Exists() || responseFormat.Type == gjson.Null || !responseFormat.IsObject() {
		return nil
	}

	formatType := strings.ToLower(strings.TrimSpace(responseFormat.Get("type").String()))
	switch formatType {
	case "", "text":
		return nil
	case "json_schema", "json_object":
		return invalidOpenAIRequestWithDetailf(
			"response_format.type",
			"invalid_value",
			"response_format.type=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve structured output response formats; use response_format.type=%q or a native OpenAI-compatible route",
			formatType,
			"text",
		)
	default:
		return nil
	}
}

func validateOpenAIAuggieChatCompletionsReasoningEffortRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	reasoningEffort := gjson.GetBytes(rawJSON, "reasoning_effort")
	if !reasoningEffort.Exists() || reasoningEffort.Type == gjson.Null {
		return nil
	}
	if reasoningEffort.Type != gjson.String {
		return invalidOpenAIRequestWithDetailf(
			"reasoning_effort",
			"invalid_type",
			"reasoning_effort must be a string on the selected /v1/chat/completions model route",
		)
	}

	value := strings.ToLower(strings.TrimSpace(reasoningEffort.String()))
	if _, ok := supportedOpenAIAuggieReasoningEffortValues[value]; ok {
		return nil
	}
	return invalidOpenAIRequestWithDetailf(
		"reasoning_effort",
		"invalid_value",
		"reasoning_effort=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge only preserves low, medium, or high native reasoning effort; use one of those values or a native OpenAI-compatible route",
		reasoningEffort.String(),
	)
}

func validateOpenAIAuggieChatCompletionsToolDefinitionSupport(rawJSON []byte) *interfaces.ErrorMessage {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return nil
	}

	for index, tool := range tools.Array() {
		toolType := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		if toolType == "" || toolType == "function" {
			if errMsg := validateOpenAIAuggieChatFunctionToolStrictMode(tool.Get("function").Get("strict"), index); errMsg != nil {
				return errMsg
			}
			continue
		}
		return invalidOpenAIRequestWithDetailf(
			fmt.Sprintf("tools[%d].type", index),
			"invalid_value",
			"tools[%d].type=%q is not supported on the selected /v1/chat/completions model route because Auggie's chat-completions bridge only preserves function tools; use function tools or a native OpenAI-compatible route",
			index,
			tool.Get("type").String(),
		)
	}

	return nil
}

func validateOpenAIAuggieChatFunctionToolStrictMode(strict gjson.Result, index int) *interfaces.ErrorMessage {
	field := fmt.Sprintf("tools[%d].function.strict", index)
	if !strict.Exists() || strict.Type == gjson.Null {
		return nil
	}
	if strict.Type != gjson.True && strict.Type != gjson.False {
		return invalidOpenAIType(field, "a boolean", strict)
	}
	if !strict.Bool() {
		return nil
	}

	return invalidOpenAIRequestWithDetailf(
		field,
		"invalid_value",
		"%s=%t is not supported on the selected /v1/chat/completions model route because Auggie's chat-completions bridge cannot preserve official function-tool strict schema semantics; set %s=%t or omit the field",
		field,
		true,
		field,
		false,
	)
}

func validateOpenAIAuggieChatCompletionsLegacyFunctionRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	tools := gjson.GetBytes(rawJSON, "tools")
	functions := gjson.GetBytes(rawJSON, "functions")
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	functionCall := gjson.GetBytes(rawJSON, "function_call")

	if tools.Exists() && tools.Type != gjson.Null && functions.Exists() && functions.Type != gjson.Null {
		return invalidOpenAIRequestWithDetailf(
			"functions",
			"invalid_value",
			"functions cannot be used together with tools on the selected /v1/chat/completions model route; use either deprecated functions/function_call or modern tools/tool_choice semantics, not both",
		)
	}
	if toolChoice.Exists() && toolChoice.Type != gjson.Null && functionCall.Exists() && functionCall.Type != gjson.Null {
		return invalidOpenAIRequestWithDetailf(
			"function_call",
			"invalid_value",
			"function_call cannot be used together with tool_choice on the selected /v1/chat/completions model route; use either deprecated functions/function_call or modern tools/tool_choice semantics, not both",
		)
	}
	if errMsg := validateOpenAIAuggieLegacyFunctionsField(functions); errMsg != nil {
		return errMsg
	}
	return validateOpenAIAuggieLegacyFunctionCallField(functionCall)
}

func validateOpenAIAuggieLegacyFunctionsField(functions gjson.Result) *interfaces.ErrorMessage {
	if !functions.Exists() || functions.Type == gjson.Null {
		return nil
	}
	if !functions.IsArray() {
		return invalidOpenAIType("functions", "an array", functions)
	}

	for index, function := range functions.Array() {
		if !function.IsObject() {
			return invalidOpenAIType(fmt.Sprintf("functions[%d]", index), "an object", function)
		}

		name := function.Get("name")
		if !name.Exists() {
			return invalidOpenAIRequestWithDetailf(
				fmt.Sprintf("functions[%d].name", index),
				"missing_required_parameter",
				"Missing required parameter: 'functions[%d].name'.",
				index,
			)
		}
		if name.Type != gjson.String {
			return invalidOpenAIType(fmt.Sprintf("functions[%d].name", index), "a string", name)
		}
		if strings.TrimSpace(name.String()) == "" {
			return invalidOpenAIValue(fmt.Sprintf("functions[%d].name", index), "Invalid value for '%s': expected a non-empty string.", fmt.Sprintf("functions[%d].name", index))
		}

		if description := function.Get("description"); description.Exists() && description.Type != gjson.Null && description.Type != gjson.String {
			return invalidOpenAIType(fmt.Sprintf("functions[%d].description", index), "a string", description)
		}
		if parameters := function.Get("parameters"); parameters.Exists() && parameters.Type != gjson.Null && !parameters.IsObject() {
			return invalidOpenAIType(fmt.Sprintf("functions[%d].parameters", index), "an object", parameters)
		}
	}

	return nil
}

func validateOpenAIAuggieLegacyFunctionCallField(functionCall gjson.Result) *interfaces.ErrorMessage {
	if !functionCall.Exists() || functionCall.Type == gjson.Null {
		return nil
	}
	if functionCall.Type == gjson.String {
		switch value := strings.ToLower(strings.TrimSpace(functionCall.String())); value {
		case "", "auto", "none":
			return nil
		default:
			return invalidOpenAIRequestWithDetailf(
				"function_call",
				"invalid_value",
				"function_call=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge only preserves deprecated function_call=%q or function_call=%q; use one of those values or a native OpenAI-compatible route",
				functionCall.String(),
				"auto",
				"none",
			)
		}
	}
	if !functionCall.IsObject() {
		return invalidOpenAIType("function_call", "a string or object", functionCall)
	}

	name := functionCall.Get("name")
	if !name.Exists() {
		return invalidOpenAIRequestWithDetailf(
			"function_call",
			"invalid_value",
			"function_call objects are not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve native forced function-use semantics; use function_call=%q, function_call=%q, or a native OpenAI-compatible route",
			"auto",
			"none",
		)
	}
	if name.Type != gjson.String {
		return invalidOpenAIType("function_call.name", "a string", name)
	}
	if strings.TrimSpace(name.String()) == "" {
		return invalidOpenAIValue("function_call.name", "Invalid value for '%s': expected a non-empty string.", "function_call.name")
	}
	return invalidOpenAIRequestWithDetailf(
		"function_call",
		"invalid_value",
		"function_call.name=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve native forced function-use semantics; use function_call=%q, function_call=%q, or a native OpenAI-compatible route",
		name.String(),
		"auto",
		"none",
	)
}

func validateOpenAIAuggieChatCompletionsToolChoiceRequestSupport(rawJSON []byte) *interfaces.ErrorMessage {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return nil
	}

	if toolChoice.Type == gjson.String {
		switch value := strings.ToLower(strings.TrimSpace(toolChoice.String())); value {
		case "", "auto", "none":
			return nil
		case "required":
			return invalidOpenAIRequestWithDetailf(
				"tool_choice",
				"invalid_value",
				"tool_choice=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve native forced tool-use semantics; use tool_choice=%q, tool_choice=%q, allowed_tools with mode=%q, or a native OpenAI-compatible route",
				value,
				"auto",
				"none",
				"auto",
			)
		default:
			return invalidOpenAIRequestWithDetailf(
				"tool_choice",
				"invalid_value",
				"tool_choice=%q is not supported on the selected /v1/chat/completions model route; supported values are %s",
				value,
				"auto, none, allowed_tools with mode=auto, or omitted tool_choice",
			)
		}
	}

	if !toolChoice.IsObject() {
		return invalidOpenAIType("tool_choice", "a string or an object", toolChoice)
	}

	toolChoiceType := strings.ToLower(strings.TrimSpace(toolChoice.Get("type").String()))
	switch toolChoiceType {
	case "function", "custom":
		return invalidOpenAIRequestWithDetailf(
			"tool_choice.type",
			"invalid_value",
			"tool_choice.type=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve native forced tool-use semantics; use tool_choice=%q, tool_choice=%q, allowed_tools with mode=%q, or a native OpenAI-compatible route",
			toolChoiceType,
			"auto",
			"none",
			"auto",
		)
	case "allowed_tools":
		container := toolChoice.Get("allowed_tools")
		param := "tool_choice.mode"
		if container.Exists() && container.IsObject() {
			param = "tool_choice.allowed_tools.mode"
		} else {
			container = toolChoice
		}
		mode := strings.ToLower(strings.TrimSpace(container.Get("mode").String()))
		if mode == "required" {
			return invalidOpenAIRequestWithDetailf(
				param,
				"invalid_value",
				"%s=%q is not supported on the selected /v1/chat/completions model route because Auggie's bridge cannot preserve native forced tool-use semantics; use %s=%q or a native OpenAI-compatible route",
				param,
				mode,
				param,
				"auto",
			)
		}
		return validateOpenAIAuggieAllowedToolsToolChoice(toolChoice, false, "/v1/chat/completions")
	}

	if toolChoiceType == "" {
		toolChoiceType = "object"
	}
	return invalidOpenAIRequestWithDetailf(
		"tool_choice.type",
		"invalid_value",
		"tool_choice.type=%q is not supported on the selected /v1/chat/completions model route; supported values are %s",
		toolChoiceType,
		"allowed_tools with mode=auto, or omitted object tool_choice",
	)
}

func validateOpenAIAuggieAllowedToolsToolChoice(toolChoice gjson.Result, allowResponsesBuiltIns bool, routePath string) *interfaces.ErrorMessage {
	container, paramPrefix, errMsg := openAIAuggieAllowedToolsToolChoiceContainer(toolChoice)
	if errMsg != nil {
		return errMsg
	}

	mode := container.Get("mode")
	modeParam := paramPrefix + ".mode"
	if !mode.Exists() || mode.Type == gjson.Null {
		return invalidOpenAIRequestWithDetailf(
			modeParam,
			"invalid_value",
			"%s is required when tool_choice.type=%q on the selected %s model route",
			modeParam,
			"allowed_tools",
			routePath,
		)
	}
	if mode.Type != gjson.String {
		return invalidOpenAIType(modeParam, "a string", mode)
	}
	modeValue := strings.ToLower(strings.TrimSpace(mode.String()))
	if modeValue != "auto" && modeValue != "required" {
		return invalidOpenAIRequestWithDetailf(
			modeParam,
			"invalid_value",
			"%s=%q is not supported on the selected %s model route; supported values are auto and required",
			modeParam,
			modeValue,
			routePath,
		)
	}

	tools := container.Get("tools")
	toolsParam := paramPrefix + ".tools"
	if !tools.Exists() || tools.Type == gjson.Null {
		return invalidOpenAIRequestWithDetailf(
			toolsParam,
			"invalid_value",
			"%s must be a non-empty array on the selected %s model route",
			toolsParam,
			routePath,
		)
	}
	if !tools.IsArray() {
		return invalidOpenAIType(toolsParam, "an array", tools)
	}
	if len(tools.Array()) == 0 {
		return invalidOpenAIRequestWithDetailf(
			toolsParam,
			"invalid_value",
			"%s must be a non-empty array on the selected %s model route",
			toolsParam,
			routePath,
		)
	}
	if openAIAuggieAllowedToolsHasSupportedSelection(tools, allowResponsesBuiltIns) {
		return nil
	}

	return invalidOpenAIRequestWithDetailf(
		toolsParam,
		"invalid_value",
		"%s must include at least one supported %s tool selection on the selected %s model route",
		toolsParam,
		openAIAuggieAllowedToolsSelectionLabel(allowResponsesBuiltIns),
		routePath,
	)
}

func openAIAuggieAllowedToolsToolChoiceContainer(toolChoice gjson.Result) (gjson.Result, string, *interfaces.ErrorMessage) {
	container := toolChoice.Get("allowed_tools")
	if container.Exists() && container.Type != gjson.Null {
		if !container.IsObject() {
			return gjson.Result{}, "", invalidOpenAIType("tool_choice.allowed_tools", "an object", container)
		}
		return container, "tool_choice.allowed_tools", nil
	}
	return toolChoice, "tool_choice", nil
}

func openAIAuggieAllowedToolsHasSupportedSelection(tools gjson.Result, allowResponsesBuiltIns bool) bool {
	for _, tool := range tools.Array() {
		toolType := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		switch toolType {
		case "function":
			if extractOpenAIAuggieToolChoiceName(tool) != "" {
				return true
			}
		case "custom":
			if allowResponsesBuiltIns && extractOpenAIAuggieToolChoiceName(tool) != "" {
				return true
			}
		default:
			if allowResponsesBuiltIns && isOpenAIAuggieBuiltInWebSearchType(toolType) {
				return true
			}
		}
	}
	return false
}

func openAIAuggieAllowedToolsSelectionLabel(allowResponsesBuiltIns bool) string {
	if allowResponsesBuiltIns {
		return "tool"
	}
	return "function"
}

func extractOpenAIAuggieToolChoiceName(tool gjson.Result) string {
	if name := strings.TrimSpace(tool.Get("function.name").String()); name != "" {
		return name
	}
	if name := strings.TrimSpace(tool.Get("custom.name").String()); name != "" {
		return name
	}
	return strings.TrimSpace(tool.Get("name").String())
}

func validateOpenAIAuggieToolDeferredLoading(deferLoading gjson.Result, index int) *interfaces.ErrorMessage {
	field := fmt.Sprintf("tools[%d].defer_loading", index)
	if !deferLoading.Exists() || deferLoading.Type == gjson.Null {
		return nil
	}
	if deferLoading.Type != gjson.True && deferLoading.Type != gjson.False {
		return invalidOpenAIType(field, "a boolean", deferLoading)
	}
	if !deferLoading.Bool() {
		return nil
	}

	return invalidOpenAIRequestWithDetailf(
		field,
		"invalid_value",
		"%s=true is not supported on the selected /v1/responses model route because Auggie does not implement deferred tool discovery for function or custom tools; use %s=false or omit the field",
		field,
		field,
	)
}

func validateOpenAIAuggieFunctionToolStrictMode(strict gjson.Result, index int) *interfaces.ErrorMessage {
	field := fmt.Sprintf("tools[%d].strict", index)
	if !strict.Exists() {
		return invalidOpenAIRequestWithDetailf(
			field,
			"invalid_value",
			"%s defaults to true for OpenAI Responses function tools and is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve native strict function schema semantics; set %s=%t or use a native OpenAI Responses route",
			field,
			field,
			false,
		)
	}
	if strict.Type != gjson.True && strict.Type != gjson.False {
		return invalidOpenAIType(field, "a boolean", strict)
	}
	if !strict.Bool() {
		return nil
	}

	return invalidOpenAIRequestWithDetailf(
		field,
		"invalid_value",
		"%s=%t is not supported on the selected /v1/responses model route because Auggie's bridge cannot preserve native strict function schema semantics; set %s=%t or use a native OpenAI Responses route",
		field,
		true,
		field,
		false,
	)
}

func validateOpenAIAuggieCustomToolFormat(format gjson.Result, index int) *interfaces.ErrorMessage {
	field := fmt.Sprintf("tools[%d].format", index)
	if !format.Exists() || format.Type == gjson.Null {
		return nil
	}
	if !format.IsObject() {
		return invalidOpenAIType(field, "an object", format)
	}

	formatType := format.Get("type")
	if !formatType.Exists() {
		return missingOpenAIRequiredParameter(field + ".type")
	}
	if formatType.Type != gjson.String {
		return invalidOpenAIType(field+".type", "a string", formatType)
	}

	switch value := strings.ToLower(strings.TrimSpace(formatType.String())); value {
	case "text", "grammar":
		return nil
	case "":
		return invalidOpenAIValue(field+".type", "Invalid value for '%s': expected a non-empty string.", field+".type")
	default:
		return invalidOpenAIRequestWithDetailf(
			field+".type",
			"invalid_value",
			"%s=%q is not supported on the selected /v1/responses model route; Auggie custom tools support format.type=%q, format.type=%q, or omitted %s",
			field+".type",
			value,
			"text",
			"grammar",
			field,
		)
	}
}

func validateOpenAIAuggieBuiltInWebSearchToolConfig(tool gjson.Result, index int) *interfaces.ErrorMessage {
	// Accept and ignore built-in web search configuration fields for compatibility with
	// native OpenAI Responses payloads. The Auggie bridge still only preserves
	// tool availability (type), not detailed web-search semantics.
	return nil
}

func validateOpenAIResponsesMessageContentItemSupport(item gjson.Result, index int, supported map[string]struct{}) *interfaces.ErrorMessage {
	content := item.Get("content")
	if !content.Exists() || content.Type == gjson.Null || content.Type == gjson.String {
		return nil
	}
	if !content.IsArray() {
		return invalidOpenAIType(fmt.Sprintf("input[%d].content", index), "a string or an array", content)
	}

	supportedList := openAIResponsesSupportedMessageContentTypeList(supported)
	for contentIndex, contentItem := range content.Array() {
		contentType := strings.TrimSpace(contentItem.Get("type").String())
		if contentType == "" && contentItem.Get("text").Exists() {
			contentType = "input_text"
		}
		if _, ok := supported[contentType]; ok {
			continue
		}
		param := fmt.Sprintf("input[%d].content[%d].type", index, contentIndex)
		return invalidOpenAIValue(
			param,
			"Invalid value for '%s': %q is not supported on /v1/responses for the selected model route; supported message content types are %s",
			param,
			contentType,
			strings.Join(supportedList, ", "),
		)
	}

	return nil
}

func openAIResponsesSupportedMessageContentTypes(modelID string, providers []string) map[string]struct{} {
	var supported map[string]struct{}
	for _, provider := range providers {
		providerSupported := openAIResponsesProviderSupportedMessageContentTypes(modelID, provider)
		if len(providerSupported) == 0 {
			continue
		}
		if supported == nil {
			supported = providerSupported
			continue
		}
		supported = intersectOpenAIResponsesContentTypes(supported, providerSupported)
	}
	if len(supported) == 0 {
		return newOpenAIResponsesContentTypeSet("input_text", "output_text", "input_image")
	}
	return supported
}

func openAIResponsesProviderSupportedMessageContentTypes(modelID string, provider string) map[string]struct{} {
	provider = strings.ToLower(strings.TrimSpace(provider))
	baseModel := strings.ToLower(strings.TrimSpace(thinking.ParseSuffix(modelID).ModelName))

	typeKey := ""
	ownedBy := ""
	if info := lookupOpenAIResponsesProviderModelInfo(modelID, provider); info != nil {
		typeKey = strings.ToLower(strings.TrimSpace(info.Type))
		ownedBy = strings.ToLower(strings.TrimSpace(info.OwnedBy))
		if baseModel == "" {
			baseModel = strings.ToLower(strings.TrimSpace(info.ID))
		}
	}

	switch {
	case provider == "auggie" || typeKey == "auggie" || ownedBy == "auggie":
		return newOpenAIResponsesContentTypeSet("input_text", "output_text")
	case provider == "claude" || typeKey == "claude" || ownedBy == "claude" || strings.Contains(baseModel, "claude"):
		return newOpenAIResponsesContentTypeSet("input_text", "output_text", "input_image", "input_file")
	case provider == "gemini" || typeKey == "gemini" || ownedBy == "gemini" || strings.Contains(baseModel, "gemini"):
		return newOpenAIResponsesContentTypeSet("input_text", "output_text", "input_image", "input_audio")
	default:
		return newOpenAIResponsesContentTypeSet("input_text", "output_text", "input_image")
	}
}

func newOpenAIResponsesContentTypeSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return set
}

func intersectOpenAIResponsesContentTypes(left, right map[string]struct{}) map[string]struct{} {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	intersection := make(map[string]struct{})
	for value := range left {
		if _, ok := right[value]; ok {
			intersection[value] = struct{}{}
		}
	}
	return intersection
}

func openAIResponsesSupportedMessageContentTypeList(supported map[string]struct{}) []string {
	list := make([]string, 0, len(supported))
	for value := range supported {
		list = append(list, value)
	}
	sort.Strings(list)
	return list
}

func openAIResponsesRouteSupportsNativeInputItems(modelID string, providers []string) bool {
	if len(providers) == 0 {
		return false
	}

	for _, provider := range providers {
		if !openAIResponsesProviderSupportsNativeInputItems(modelID, provider) {
			return false
		}
	}
	return true
}

func openAIResponsesSupportedInputItemTypes(modelID string, providers []string) []string {
	supported := []string{"message", "function_call", "function_call_output"}
	if openAIResponsesRouteSupportsBridgedCustomTools(modelID, providers) {
		supported = append(supported, "custom_tool_call", "custom_tool_call_output")
	}
	return supported
}

func openAIResponsesRouteSupportsBridgedCustomTools(modelID string, providers []string) bool {
	if len(providers) == 0 {
		return false
	}

	for _, provider := range providers {
		if !openAIResponsesProviderSupportsBridgedCustomTools(modelID, provider) {
			return false
		}
	}
	return true
}

func openAIResponsesProviderSupportsNativeInputItems(modelID string, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "codex" {
		return true
	}

	info := lookupOpenAIResponsesProviderModelInfo(modelID, provider)
	if info == nil {
		return false
	}

	typeKey := strings.ToLower(strings.TrimSpace(info.Type))
	ownedBy := strings.ToLower(strings.TrimSpace(info.OwnedBy))
	return typeKey == "codex" || typeKey == "openai" || ownedBy == "openai"
}

func openAIResponsesProviderSupportsBridgedCustomTools(modelID string, provider string) bool {
	return openAIProviderUsesAuggieBridge(modelID, provider)
}

func openAIRouteIncludesAuggieProvider(modelID string, providers []string) bool {
	if len(providers) == 0 {
		return false
	}
	for _, provider := range providers {
		if openAIProviderUsesAuggieBridge(modelID, provider) {
			return true
		}
	}
	return false
}

func openAIProviderUsesAuggieBridge(modelID string, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "auggie" {
		return true
	}

	info := lookupOpenAIResponsesProviderModelInfo(modelID, provider)
	if info == nil {
		return false
	}

	typeKey := strings.ToLower(strings.TrimSpace(info.Type))
	ownedBy := strings.ToLower(strings.TrimSpace(info.OwnedBy))
	return typeKey == "auggie" || ownedBy == "auggie"
}

func lookupOpenAIResponsesProviderModelInfo(modelID string, provider string) *registry.ModelInfo {
	reg := registry.GetGlobalRegistry()
	candidates := make([]string, 0, 2)
	if baseModel := strings.TrimSpace(thinking.ParseSuffix(modelID).ModelName); baseModel != "" {
		candidates = append(candidates, baseModel)
	}
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		candidates = append(candidates, modelID)
	}

	for _, candidate := range candidates {
		if info := reg.GetModelInfo(candidate, provider); info != nil {
			return info
		}
		if info := reg.GetModelInfoByAlias(candidate, provider); info != nil {
			return info
		}
	}

	return nil
}

func validateOpenAIResponsesChatCompletionsBridge(rawJSON []byte) *interfaces.ErrorMessage {
	input := gjson.GetBytes(rawJSON, "input")
	if !input.Exists() {
		return validateOpenAIResponsesChatCompletionsBridgeTextFormat(rawJSON)
	}
	if input.Type == gjson.String {
		return validateOpenAIResponsesChatCompletionsBridgeTextFormat(rawJSON)
	}
	if !input.IsArray() {
		return invalidOpenAIType("input", "a string or an array", input)
	}

	for index, item := range input.Array() {
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "" && strings.TrimSpace(item.Get("role").String()) != "" {
			itemType = "message"
		}

		switch itemType {
		case "", "message":
			if errMsg := validateOpenAIResponsesChatCompletionsBridgeMessage(item, index); errMsg != nil {
				return errMsg
			}
		case "function_call":
			continue
		case "function_call_output":
			if errMsg := validateOpenAIResponsesChatCompletionsBridgeToolOutput(item, index); errMsg != nil {
				return errMsg
			}
		default:
			param := fmt.Sprintf("input[%d].type", index)
			return invalidOpenAIValue(
				param,
				"Invalid value for '%s': %q cannot be represented on /v1/chat/completions; supported bridge item types are message, function_call, and function_call_output",
				param,
				itemType,
			)
		}
	}

	return validateOpenAIResponsesChatCompletionsBridgeTextFormat(rawJSON)
}

func validateOpenAIResponsesChatCompletionsBridgeMessage(item gjson.Result, index int) *interfaces.ErrorMessage {
	content := item.Get("content")
	if !content.Exists() || content.Type == gjson.Null || content.Type == gjson.String {
		return nil
	}
	if !content.IsArray() {
		return invalidOpenAIType(fmt.Sprintf("input[%d].content", index), "a string or an array", content)
	}

	for contentIndex, contentItem := range content.Array() {
		contentType := strings.TrimSpace(contentItem.Get("type").String())
		if contentType == "" && contentItem.Get("text").Exists() {
			contentType = "input_text"
		}
		switch contentType {
		case "input_text", "output_text", "input_image":
			continue
		default:
			param := fmt.Sprintf("input[%d].content[%d].type", index, contentIndex)
			return invalidOpenAIValue(
				param,
				"Invalid value for '%s': %q cannot be represented on /v1/chat/completions; supported message content types are input_text, output_text, and input_image",
				param,
				contentType,
			)
		}
	}

	return nil
}

func validateOpenAIResponsesChatCompletionsBridgeToolOutput(item gjson.Result, index int) *interfaces.ErrorMessage {
	output := item.Get("output")
	if !output.Exists() || output.Type == gjson.String || !output.IsArray() {
		return nil
	}

	for outputIndex, outputItem := range output.Array() {
		outputType := strings.TrimSpace(outputItem.Get("type").String())
		if outputType == "" && outputItem.Get("text").Exists() {
			outputType = "input_text"
		}
		switch outputType {
		case "input_text", "output_text":
			continue
		default:
			param := fmt.Sprintf("input[%d].output[%d].type", index, outputIndex)
			return invalidOpenAIValue(
				param,
				"Invalid value for '%s': %q cannot be represented on /v1/chat/completions; supported tool output item types are input_text and output_text",
				param,
				outputType,
			)
		}
	}

	return nil
}

func validateOpenAIResponsesChatCompletionsBridgeTextFormat(rawJSON []byte) *interfaces.ErrorMessage {
	textFormat := gjson.GetBytes(rawJSON, "text.format")
	if !textFormat.Exists() || textFormat.Type == gjson.Null {
		return nil
	}
	if !textFormat.IsObject() {
		return invalidOpenAIType("text.format", "an object", textFormat)
	}

	formatTypeResult := textFormat.Get("type")
	if !formatTypeResult.Exists() || formatTypeResult.Type == gjson.Null {
		return missingOpenAIRequiredParameter("text.format.type")
	}
	if formatTypeResult.Type != gjson.String {
		return invalidOpenAIType("text.format.type", "a string", formatTypeResult)
	}

	formatType := strings.ToLower(strings.TrimSpace(formatTypeResult.String()))
	switch formatType {
	case "text", "json_object":
		return nil
	case "json_schema":
		name := textFormat.Get("name")
		if !name.Exists() || name.Type == gjson.Null {
			return missingOpenAIRequiredParameter("text.format.name")
		}
		if name.Type != gjson.String {
			return invalidOpenAIType("text.format.name", "a string", name)
		}
		if strings.TrimSpace(name.String()) == "" {
			return invalidOpenAIValue("text.format.name", "Invalid value for 'text.format.name': expected a non-empty string.")
		}
		schema := textFormat.Get("schema")
		if !schema.Exists() || schema.Type == gjson.Null {
			return missingOpenAIRequiredParameter("text.format.schema")
		}
		if !schema.IsObject() {
			return invalidOpenAIType("text.format.schema", "an object", schema)
		}
		return nil
	case "":
		return invalidOpenAIValue("text.format.type", "Invalid value for 'text.format.type': expected a non-empty string.")
	default:
		return invalidOpenAIValue(
			"text.format.type",
			"Invalid value for 'text.format.type': %q cannot be represented on /v1/chat/completions; supported text.format types are text, json_object, and json_schema",
			formatType,
		)
	}
}
