# OpenAI Claude Dual-Surface Alignment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Align the proxy's `/v1` compatibility layer so GPT and Claude models share one OpenAI-style public catalog, Claude remains available on Anthropic-native `/v1/messages`, and each surface returns the correct official-style error contract.

**Architecture:** Keep `antigravity` and `auggie` as separate internal providers, but move public-surface validation and error normalization into the handler layer. Use a unified `/v1/models` contract, OpenAI-surface capability checks for `/v1/chat/completions` and `/v1/responses`, and Anthropic-surface capability checks for `/v1/messages`.

**Tech Stack:** Go, Gin, existing registry/auth-manager/executor pipeline, Go unit tests, curl characterization tests

---

### Task 1: Replace `/v1/models` User-Agent Splitting with One Unified Catalog

**Files:**
- Modify: `apps/server-go/internal/api/server.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_handlers.go`
- Modify: `apps/server-go/internal/registry/model_registry.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_models_test.go`
- Create: `apps/server-go/internal/api/server_models_contract_test.go`

**Step 1: Write the failing test**

Add tests that prove:

- `/v1/models` no longer depends on `User-Agent`
- one response can contain both GPT and Claude model IDs
- Auggie aliases stay collapsed to canonical IDs

Suggested test names:

```go
func TestUnifiedModelsHandler_DoesNotSwitchOnUserAgent(t *testing.T) {}

func TestOpenAIModels_ReturnsUnifiedGPTAndClaudeCatalog(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/api ./sdk/api/handlers/openai -run 'Test(UnifiedModelsHandler|OpenAIModels_)' -count=1
```

Expected: FAIL because `/v1/models` still switches handler behavior by `User-Agent`.

**Step 3: Write minimal implementation**

Update:

- `apps/server-go/internal/api/server.go`
  remove the `User-Agent` split in `unifiedModelsHandler`
- `apps/server-go/sdk/api/handlers/openai/openai_handlers.go`
  keep OpenAI-compatible output shape but include Claude-compatible IDs in the same catalog
- `apps/server-go/internal/registry/model_registry.go`
  only if needed to expose the right runtime-visible model set without leaking aliases or duplicates

Do not change executor logic in this task.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/api ./sdk/api/handlers/openai -run 'Test(UnifiedModelsHandler|OpenAIModels_)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/internal/api/server.go apps/server-go/sdk/api/handlers/openai/openai_handlers.go apps/server-go/internal/registry/model_registry.go apps/server-go/sdk/api/handlers/openai/openai_models_test.go apps/server-go/internal/api/server_models_contract_test.go
git commit -m "feat: unify v1 models catalog across GPT and Claude"
```

### Task 2: Add Explicit Surface Capability Validation Helpers

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/claude/code_handlers.go`
- Create: `apps/server-go/sdk/api/handlers/openai/openai_surface_validation_test.go`
- Create: `apps/server-go/sdk/api/handlers/claude/claude_surface_validation_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- Claude IDs are accepted on `/v1/chat/completions`
- Claude IDs are accepted on `/v1/responses`
- GPT IDs are rejected on `/v1/messages`
- unknown IDs are rejected at the handler boundary before downstream execution

Suggested test names:

```go
func TestChatCompletions_AllowsClaudeModelIDs(t *testing.T) {}

func TestResponses_AllowsClaudeModelIDs(t *testing.T) {}

func TestClaudeMessages_RejectsGPTModelID(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/openai ./sdk/api/handlers/claude -run 'Test(ChatCompletions_|Responses_|ClaudeMessages_)' -count=1
```

Expected: FAIL because capability validation is currently implicit and surface-specific rejection is not enforced consistently.

**Step 3: Write minimal implementation**

Add small validation helpers that:

- classify public model IDs by surface capability
- gate `/v1/chat/completions` and `/v1/responses` to OpenAI-surface models
- gate `/v1/messages` to Claude-only models

Keep routing decisions after validation in the existing auth-manager flow.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/openai ./sdk/api/handlers/claude -run 'Test(ChatCompletions_|Responses_|ClaudeMessages_)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/sdk/api/handlers/openai/openai_handlers.go apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go apps/server-go/sdk/api/handlers/claude/code_handlers.go apps/server-go/sdk/api/handlers/openai/openai_surface_validation_test.go apps/server-go/sdk/api/handlers/claude/claude_surface_validation_test.go
git commit -m "fix: validate model capabilities per public API surface"
```

### Task 3: Normalize OpenAI-Surface Error Contracts for GPT and Claude Models

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_responses_handlers_stream_error_test.go`
- Create: `apps/server-go/sdk/api/handlers/openai/openai_error_contract_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- Claude-backed failures on `/v1/chat/completions` still produce OpenAI-style error bodies
- Claude-backed failures on `/v1/responses` still produce OpenAI-style JSON or stream error chunks
- invalid surface usage does not leak Anthropic error envelopes through OpenAI endpoints

Suggested test names:

```go
func TestChatCompletions_UsesOpenAIErrorEnvelopeForClaudeFailures(t *testing.T) {}

func TestResponses_UsesOpenAIErrorEnvelopeForClaudeFailures(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'Test(ChatCompletions_UsesOpenAIErrorEnvelope|Responses_UsesOpenAIErrorEnvelope)' -count=1
```

Expected: FAIL if handler paths currently pass through mixed upstream error shapes.

**Step 3: Write minimal implementation**

Add or refine handler-local error normalization so:

- JSON errors are always OpenAI-compatible on OpenAI surfaces
- stream terminal errors use OpenAI stream error format
- surface validation failures reuse the same OpenAI contract

Do not alter Anthropic handlers in this task.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/openai -run 'Test(ChatCompletions_UsesOpenAIErrorEnvelope|Responses_UsesOpenAIErrorEnvelope)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/sdk/api/handlers/openai/openai_handlers.go apps/server-go/sdk/api/handlers/openai/openai_responses_handlers.go apps/server-go/sdk/api/handlers/openai/openai_responses_handlers_stream_error_test.go apps/server-go/sdk/api/handlers/openai/openai_error_contract_test.go
git commit -m "fix: normalize OpenAI error contracts for mixed model families"
```

### Task 4: Normalize Anthropic-Surface Validation and Errors for `/v1/messages`

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/claude/code_handlers.go`
- Create: `apps/server-go/sdk/api/handlers/claude/claude_error_contract_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- `gpt-5.4` on `/v1/messages` returns Anthropic-style invalid request error
- unknown model IDs on `/v1/messages` return Anthropic-style error bodies
- Claude message stream terminal errors remain Anthropic-style

Suggested test names:

```go
func TestClaudeMessages_RejectsGPTModelWithAnthropicError(t *testing.T) {}

func TestClaudeMessages_UsesAnthropicErrorEnvelopeForValidationFailures(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/claude -run 'TestClaudeMessages_' -count=1
```

Expected: FAIL because the handler currently does not explicitly enforce this contract boundary.

**Step 3: Write minimal implementation**

Update `apps/server-go/sdk/api/handlers/claude/code_handlers.go` to:

- validate model IDs before execution
- reject non-Claude models immediately
- use the Anthropic-style error envelope for validation and execution failures

Reuse the existing `toClaudeError` path where possible.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/claude -run 'TestClaudeMessages_' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/sdk/api/handlers/claude/code_handlers.go apps/server-go/sdk/api/handlers/claude/claude_error_contract_test.go
git commit -m "fix: enforce Anthropic contract on v1 messages"
```

### Task 5: Run Focused Verification and Live Curl Regression

**Files:**
- Modify: `docs/plans/2026-03-08-openai-claude-dual-surface-alignment-design.md`

**Step 1: Run focused Go tests**

Run:

```bash
go test ./internal/api ./internal/registry ./sdk/api/handlers/openai ./sdk/api/handlers/claude ./sdk/cliproxy -count=1
```

Expected: PASS.

**Step 2: Run live local curl verification**

Verify at least these cases:

```bash
curl -sS http://127.0.0.1:8327/v1/models -H "Authorization: Bearer $KEY"
curl -sS http://127.0.0.1:8327/v1/chat/completions -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}'
curl -sS http://127.0.0.1:8327/v1/chat/completions -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}'
curl -sS http://127.0.0.1:8327/v1/responses -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","input":"hi"}'
curl -sS http://127.0.0.1:8327/v1/responses -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"model":"claude-opus-4-6","input":"hi"}'
curl -sS http://127.0.0.1:8327/v1/messages -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d '{"model":"claude-opus-4-6","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}'
curl -sS http://127.0.0.1:8327/v1/messages -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d '{"model":"gpt-5.4","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}'
```

Expected:

- `/v1/models` shows GPT and Claude IDs in one response
- GPT and Claude both work on OpenAI surfaces
- Claude works on Anthropic surface
- GPT is rejected on Anthropic surface with Anthropic-style error

**Step 3: Compare against external characterization baseline**

Run the same core requests against `https://code.ppchat.vip` and compare:

- model listing shape
- acceptance or rejection behavior per surface
- error body family

Document any intentionally preserved differences.

**Step 4: Update design doc with verification notes if needed**

Add a short verification note only if live behavior required a design-level adjustment.

**Step 5: Commit**

```bash
git add docs/plans/2026-03-08-openai-claude-dual-surface-alignment-design.md
git commit -m "docs: record OpenAI Claude dual-surface alignment verification"
```
