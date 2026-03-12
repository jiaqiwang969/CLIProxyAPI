package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_FunctionCallOutputTextArrayBecomesToolContentArray(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":[
			{
				"type":"function_call_output",
				"call_id":"call-1",
				"output":[
					{"type":"input_text","text":"part 1"},
					{"type":"input_text","text":"part 2"}
				]
			}
		]
	}`), false)

	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "tool" {
		t.Fatalf("messages.0.role = %q, want tool; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_call_id").String(); got != "call-1" {
		t.Fatalf("messages.0.tool_call_id = %q, want call-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content").Type; got == gjson.String {
		t.Fatalf("messages.0.content unexpectedly downgraded to string; payload=%s", out)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.type").String(); got != "text" {
		t.Fatalf("messages.0.content.0.type = %q, want text; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); got != "part 1" {
		t.Fatalf("messages.0.content.0.text = %q, want part 1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.text").String(); got != "part 2" {
		t.Fatalf("messages.0.content.1.text = %q, want part 2; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_PreservesFunctionToolChoiceObject(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}
		],
		"tool_choice":{"type":"function","name":"get_weather"}
	}`), false)

	if got := gjson.GetBytes(out, "tool_choice.type").String(); got != "function" {
		t.Fatalf("tool_choice.type = %q, want function; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_choice.function.name").String(); got != "get_weather" {
		t.Fatalf("tool_choice.function.name = %q, want get_weather; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_PreservesDeveloperRole(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":[
			{
				"type":"message",
				"role":"developer",
				"content":[{"type":"input_text","text":"Be terse."}]
			},
			{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"hello"}]
			}
		]
	}`), false)

	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "developer" {
		t.Fatalf("messages.0.role = %q, want developer; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); got != "Be terse." {
		t.Fatalf("messages.0.content.0.text = %q, want %q; payload=%s", got, "Be terse.", out)
	}
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "user" {
		t.Fatalf("messages.1.role = %q, want user; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_PreservesSharedSamplingFields(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":"hello",
		"temperature":0.2,
		"top_p":0.9,
		"user":"user_123"
	}`), false)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 0.2 {
		t.Fatalf("temperature = %v, want 0.2; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "top_p").Float(); got != 0.9 {
		t.Fatalf("top_p = %v, want 0.9; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "user").String(); got != "user_123" {
		t.Fatalf("user = %q, want user_123; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_UsesMaxCompletionTokens(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":"hello",
		"max_output_tokens":321
	}`), false)

	if got := gjson.GetBytes(out, "max_completion_tokens").Int(); got != 321 {
		t.Fatalf("max_completion_tokens = %d, want 321; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "max_tokens"); got.Exists() {
		t.Fatalf("max_tokens unexpectedly present = %s; payload=%s", got.Raw, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_TopLogprobsEnablesChatLogprobs(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":"hello",
		"top_logprobs":5
	}`), false)

	if got := gjson.GetBytes(out, "top_logprobs").Int(); got != 5 {
		t.Fatalf("top_logprobs = %d, want 5; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "logprobs"); got.Type != gjson.True {
		t.Fatalf("logprobs = %s, want true when top_logprobs is requested; payload=%s", got.Raw, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_PreservesSharedCachingAndStreamingFields(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":"hello",
		"prompt_cache_retention":"24h",
		"stream_options":{"include_obfuscation":true}
	}`), true)

	if got := gjson.GetBytes(out, "prompt_cache_retention").String(); got != "24h" {
		t.Fatalf("prompt_cache_retention = %q, want 24h; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "stream_options.include_obfuscation"); got.Type != gjson.True {
		t.Fatalf("stream_options.include_obfuscation = %s, want true; payload=%s", got.Raw, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_PreservesSharedChatControlFields(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":"hello",
		"metadata":{"trace_id":"trace-1"},
		"prompt_cache_key":"cache-key-1",
		"safety_identifier":"safe-user-1",
		"service_tier":"priority",
		"store":true,
		"text":{"verbosity":"low"}
	}`), false)

	if got := gjson.GetBytes(out, "metadata.trace_id").String(); got != "trace-1" {
		t.Fatalf("metadata.trace_id = %q, want trace-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "prompt_cache_key").String(); got != "cache-key-1" {
		t.Fatalf("prompt_cache_key = %q, want cache-key-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "safety_identifier").String(); got != "safe-user-1" {
		t.Fatalf("safety_identifier = %q, want safe-user-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "store"); got.Type != gjson.True {
		t.Fatalf("store = %s, want true; payload=%s", got.Raw, out)
	}
	if got := gjson.GetBytes(out, "verbosity").String(); got != "low" {
		t.Fatalf("verbosity = %q, want low; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_MapsStructuredTextFormatToResponseFormat(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":"hello",
		"text":{
			"format":{
				"type":"json_schema",
				"name":"pwd_result",
				"strict":true,
				"schema":{
					"type":"object",
					"properties":{
						"cwd":{"type":"string"}
					},
					"required":["cwd"],
					"additionalProperties":false
				}
			}
		}
	}`), false)

	if got := gjson.GetBytes(out, "response_format.type").String(); got != "json_schema" {
		t.Fatalf("response_format.type = %q, want json_schema; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "response_format.json_schema.name").String(); got != "pwd_result" {
		t.Fatalf("response_format.json_schema.name = %q, want pwd_result; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "response_format.json_schema.strict"); got.Type != gjson.True {
		t.Fatalf("response_format.json_schema.strict = %s, want true; payload=%s", got.Raw, out)
	}
	if got := gjson.GetBytes(out, "response_format.json_schema.schema.properties.cwd.type").String(); got != "string" {
		t.Fatalf("response_format.json_schema.schema.properties.cwd.type = %q, want string; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_FunctionToolsDefaultToStrict(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}
		]
	}`), false)

	if got := gjson.GetBytes(out, "tools.0.type").String(); got != "function" {
		t.Fatalf("tools.0.type = %q, want function; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.strict"); got.Type != gjson.True {
		t.Fatalf("tools.0.function.strict = %s, want true by default for Responses tools; payload=%s", got.Raw, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_NormalizesDefaultStrictSchema(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{
				"type":"function",
				"name":"get_weather",
				"parameters":{
					"type":"object",
					"properties":{
						"location":{"type":"string"},
						"unit":{"type":"string"}
					},
					"required":["location"]
				}
			}
		]
	}`), false)

	if got := gjson.GetBytes(out, "tools.0.function.parameters.additionalProperties"); got.Type != gjson.False {
		t.Fatalf("tools.0.function.parameters.additionalProperties = %s, want false under Responses default strict mode; payload=%s", got.Raw, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.required.#").Int(); got != 2 {
		t.Fatalf("tools.0.function.parameters.required length = %d, want all properties required under default strict mode; payload=%s", got, out)
	}
	if !gjson.GetBytes(out, `tools.0.function.parameters.required.#(=="location")`).Exists() {
		t.Fatalf("required list missing location; payload=%s", out)
	}
	if !gjson.GetBytes(out, `tools.0.function.parameters.required.#(=="unit")`).Exists() {
		t.Fatalf("required list missing unit; payload=%s", out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_PreservesExplicitStrictFalse(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{
				"type":"function",
				"name":"get_weather",
				"strict":false,
				"parameters":{
					"type":"object",
					"properties":{
						"location":{"type":"string"},
						"unit":{"type":"string"}
					},
					"required":["location"]
				}
			}
		]
	}`), false)

	if got := gjson.GetBytes(out, "tools.0.function.strict"); got.Type != gjson.False {
		t.Fatalf("tools.0.function.strict = %s, want explicit false preserved; payload=%s", got.Raw, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.additionalProperties"); got.Exists() {
		t.Fatalf("tools.0.function.parameters.additionalProperties unexpectedly synthesized under strict=false; payload=%s", out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.required.#").Int(); got != 1 {
		t.Fatalf("tools.0.function.parameters.required length = %d, want original non-strict schema preserved; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_NormalizesNestedStrictObjectSchemas(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{
				"type":"function",
				"name":"lookup_weather",
				"parameters":{
					"type":"object",
					"properties":{
						"location":{
							"type":"object",
							"properties":{
								"city":{"type":"string"},
								"country":{"type":"string"}
							}
						}
					}
				}
			}
		]
	}`), false)

	if got := gjson.GetBytes(out, "tools.0.function.parameters.properties.location.additionalProperties"); got.Type != gjson.False {
		t.Fatalf("nested additionalProperties = %s, want false; payload=%s", got.Raw, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.properties.location.required.#").Int(); got != 2 {
		t.Fatalf("nested required length = %d, want 2; payload=%s", got, out)
	}
	if !gjson.GetBytes(out, `tools.0.function.parameters.properties.location.required.#(=="city")`).Exists() {
		t.Fatalf("nested required missing city; payload=%s", out)
	}
	if !gjson.GetBytes(out, `tools.0.function.parameters.properties.location.required.#(=="country")`).Exists() {
		t.Fatalf("nested required missing country; payload=%s", out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_CustomToolBecomesFunctionShim(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{"type":"custom","name":"bash","description":"Run shell commands"}
		],
		"tool_choice":{"type":"custom","name":"bash"}
	}`), false)

	if got := gjson.GetBytes(out, "tools.0.type").String(); got != "function" {
		t.Fatalf("tools.0.type = %q, want function; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.name").String(); got != "bash" {
		t.Fatalf("tools.0.function.name = %q, want bash; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.description").String(); got != "Run shell commands" {
		t.Fatalf("tools.0.function.description = %q, want Run shell commands; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.type").String(); got != "object" {
		t.Fatalf("tools.0.function.parameters.type = %q, want object; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.properties.input.type").String(); got != "string" {
		t.Fatalf("tools.0.function.parameters.properties.input.type = %q, want string; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.additionalProperties"); got.Type != gjson.False {
		t.Fatalf("tools.0.function.parameters.additionalProperties = %s, want false; payload=%s", got.Raw, out)
	}
	if !gjson.GetBytes(out, `tools.0.function.parameters.required.#(=="input")`).Exists() {
		t.Fatalf("tools.0.function.parameters.required missing input; payload=%s", out)
	}
	if got := gjson.GetBytes(out, "tool_choice.type").String(); got != "function" {
		t.Fatalf("tool_choice.type = %q, want function; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_choice.function.name").String(); got != "bash" {
		t.Fatalf("tool_choice.function.name = %q, want bash; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_CustomToolGrammarRegexSetsInputPattern(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"tools":[
			{
				"type":"custom",
				"name":"bash",
				"format":{
					"type":"grammar",
					"syntax":"regex",
					"definition":"^(pwd|ls)(\\s+[-\\w./]+)?$"
				}
			}
		]
	}`), false)

	if got := gjson.GetBytes(out, "tools.0.type").String(); got != "function" {
		t.Fatalf("tools.0.type = %q, want function; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.properties.input.type").String(); got != "string" {
		t.Fatalf("tools.0.function.parameters.properties.input.type = %q, want string; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.0.function.parameters.properties.input.pattern").String(); got != "^(pwd|ls)(\\s+[-\\w./]+)?$" {
		t.Fatalf("tools.0.function.parameters.properties.input.pattern = %q, want regex definition; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_CustomToolItemsUseInputWrapper(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":[
			{"type":"custom_tool_call","call_id":"call-1","name":"bash","input":"pwd"},
			{"type":"custom_tool_call_output","call_id":"call-1","output":"/tmp/project"}
		]
	}`), false)

	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "assistant" {
		t.Fatalf("messages.0.role = %q, want assistant; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.id").String(); got != "call-1" {
		t.Fatalf("messages.0.tool_calls.0.id = %q, want call-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.function.name").String(); got != "bash" {
		t.Fatalf("messages.0.tool_calls.0.function.name = %q, want bash; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.function.arguments").String(); got != `{"input":"pwd"}` {
		t.Fatalf("messages.0.tool_calls.0.function.arguments = %q, want %q; payload=%s", got, `{"input":"pwd"}`, out)
	}
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "tool" {
		t.Fatalf("messages.1.role = %q, want tool; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.tool_call_id").String(); got != "call-1" {
		t.Fatalf("messages.1.tool_call_id = %q, want call-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content").String(); got != "/tmp/project" {
		t.Fatalf("messages.1.content = %q, want /tmp/project; payload=%s", got, out)
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_CustomToolOutputArrayUsesChatContentItems(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("gpt-5", []byte(`{
		"input":[
			{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_text","text":"pwd"}]}
		]
	}`), false)

	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "tool" {
		t.Fatalf("messages.0.role = %q, want tool; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_call_id").String(); got != "call-1" {
		t.Fatalf("messages.0.tool_call_id = %q, want call-1; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.type").String(); got != "text" {
		t.Fatalf("messages.0.content.0.type = %q, want text; payload=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); got != "pwd" {
		t.Fatalf("messages.0.content.0.text = %q, want pwd; payload=%s", got, out)
	}
}
