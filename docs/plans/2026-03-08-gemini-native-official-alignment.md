# Gemini Native Official Alignment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Restore the Gemini native `/v1beta/models*` API surface so it only exposes official Gemini models and never leaks private upstream model IDs.

**Architecture:** Add a public Gemini catalog helper derived from static official Gemini definitions plus runtime availability, validate Gemini model IDs at the handler boundary, then rewrite native Gemini response model identity back to the requested public model ID. Reuse the existing auth-manager alias mapping for internal routing so executors do not need a redesign.

**Tech Stack:** Go, Gin, existing registry/auth-manager/executor pipeline, Go unit tests

---

### Task 1: Add a Public Official Gemini Catalog Helper

**Files:**
- Modify: `apps/server-go/internal/registry/model_definitions.go`
- Create: `apps/server-go/internal/registry/gemini_public_catalog_test.go`

**Step 1: Write the failing test**

Create tests that verify:

- official Gemini static definitions are filtered by actual runtime availability
- non-Gemini runtime models never enter the public Gemini catalog
- an official alias such as `gemini-3.1-pro-preview` can appear when registered as an available runtime model

Suggested test cases:

```go
func TestGetPublicGeminiModels_FiltersToOfficialAvailableModels(t *testing.T) {}

func TestGetPublicGeminiModels_ExcludesNonGeminiRuntimeEntries(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/registry -run 'TestGetPublicGeminiModels' -count=1
```

Expected: FAIL because the helper does not exist yet.

**Step 3: Write minimal implementation**

Add a helper in `apps/server-go/internal/registry/model_definitions.go` that:

- starts from `GetGeminiModels()`
- checks runtime availability using the global registry by official ID
- returns only the official Gemini entries that are actually routable

Do not reuse `GetAvailableModels("gemini")`.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/registry -run 'TestGetPublicGeminiModels' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/internal/registry/model_definitions.go apps/server-go/internal/registry/gemini_public_catalog_test.go
git commit -m "feat: add public Gemini catalog helper"
```

### Task 2: Make Gemini Handlers Use the Public Catalog and Reject Private Model IDs

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go`
- Create: `apps/server-go/sdk/api/handlers/gemini/gemini_handlers_test.go`

**Step 1: Write the failing test**

Create handler tests that verify:

- `GeminiModels` returns only official Gemini entries
- `GeminiGetHandler` returns 404 for a private model ID like `gemini-3.1-pro-high`
- `GeminiHandler` rejects private model IDs before execution

Suggested test names:

```go
func TestGeminiModels_ReturnsOnlyOfficialGeminiCatalog(t *testing.T) {}

func TestGeminiGetHandler_RejectsPrivateModelID(t *testing.T) {}

func TestGeminiHandler_RejectsPrivateModelIDBeforeExecution(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/gemini -run 'TestGemini(Models|GetHandler|Handler)' -count=1
```

Expected: FAIL because the current handler still exposes mixed runtime models and accepts private IDs.

**Step 3: Write minimal implementation**

Update `apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go` to:

- use the public catalog helper instead of `GetAvailableModels("gemini")`
- normalize model lookup against official Gemini IDs only
- reject non-official or unavailable model IDs with a Gemini-style not-found response

Keep the downstream execution path unchanged after validation succeeds.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/gemini -run 'TestGemini(Models|GetHandler|Handler)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go apps/server-go/sdk/api/handlers/gemini/gemini_handlers_test.go
git commit -m "fix: restrict Gemini handlers to official native models"
```

### Task 3: Rewrite Native Gemini Responses to the Public Official Model ID

**Files:**
- Modify: `apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go`
- Create: `apps/server-go/sdk/api/handlers/gemini/gemini_response_rewrite_test.go`

**Step 1: Write the failing test**

Create focused tests for helper logic that rewrites native Gemini response model identity:

- non-streaming JSON `modelVersion` rewrites from private upstream model to requested official public model
- streaming chunks rewrite `modelVersion` without breaking other Gemini-native fields
- payloads without `modelVersion` remain unchanged

Suggested test names:

```go
func TestRewriteGeminiResponseModelVersion_NonStream(t *testing.T) {}

func TestRewriteGeminiResponseModelVersion_StreamChunk(t *testing.T) {}

func TestRewriteGeminiResponseModelVersion_IgnoresPayloadWithoutModelVersion(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./sdk/api/handlers/gemini -run 'TestRewriteGeminiResponseModelVersion' -count=1
```

Expected: FAIL because the rewrite helper does not exist yet.

**Step 3: Write minimal implementation**

Add a small helper near the Gemini handler that:

- rewrites `modelVersion` to the requested public Gemini model ID
- is used in both `handleGenerateContent` and `handleStreamGenerateContent`
- leaves `usageMetadata`, `thoughtSignature`, and other Gemini-native fields untouched

Do not change count-tokens payloads unless a failing test proves it is necessary.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./sdk/api/handlers/gemini -run 'TestRewriteGeminiResponseModelVersion' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go apps/server-go/sdk/api/handlers/gemini/gemini_response_rewrite_test.go
git commit -m "fix: rewrite Gemini native responses to public model ids"
```

### Task 4: Run Focused Verification and Live Curl Regression

**Files:**
- Modify: `docs/plans/2026-03-08-gemini-native-official-alignment-design.md`

**Step 1: Run focused Go tests**

Run:

```bash
go test ./internal/registry ./sdk/api/handlers/gemini ./sdk/cliproxy ./internal/translator/antigravity/gemini ./internal/runtime/executor -count=1
```

Expected: PASS.

**Step 2: Run live local curl verification**

Run the local server and verify:

```bash
curl -sS http://127.0.0.1:8317/v1beta/models -H "Authorization: Bearer $KEY"
curl -sS http://127.0.0.1:8317/v1beta/models/gemini-3.1-pro-preview:generateContent -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}'
curl -sS http://127.0.0.1:8317/v1beta/models/gemini-3.1-pro-high:generateContent -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}'
```

Expected:

- first command returns only official Gemini models
- second command succeeds and returns public `modelVersion`
- third command fails at the public Gemini boundary

**Step 3: Update design doc with verification notes if needed**

Add a short verification note only if the live behavior required a design adjustment.

**Step 4: Commit**

```bash
git add docs/plans/2026-03-08-gemini-native-official-alignment-design.md
git commit -m "docs: record Gemini native alignment verification"
```
