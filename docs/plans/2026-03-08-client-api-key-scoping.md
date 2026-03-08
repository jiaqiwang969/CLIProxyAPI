# Client API Key Scoping Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add provider/account-scoped downstream client API keys, enforce them in the proxy, and expose the scope clearly in the menubar.

**Architecture:** Introduce a structured `client-api-keys` config alongside legacy `api-keys`, have request auth return scope metadata, apply scope via provider filtering plus pinned auth IDs during execution, then move the menubar key management flow onto management APIs that return accounts with embedded models and the current structured keys.

**Tech Stack:** Go, Gin, existing auth-manager/registry pipeline, SwiftUI menubar app, Swift Testing

---

### Task 1: Add Structured Client API Key Config Support

**Files:**
- Modify: `apps/server-go/internal/config/sdk_config.go`
- Modify: `apps/server-go/internal/config/config.go`
- Modify: `apps/server-go/sdk/config/config.go`
- Test: `apps/server-go/internal/config/client_api_keys_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- structured `client-api-keys` entries load from YAML
- legacy `api-keys` are still surfaced as effective unscoped keys
- structured entries are normalized and trimmed

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/config -run 'Test(ClientAPIKeys|EffectiveClientAPIKeys)' -count=1
```

Expected: FAIL because the config types/helpers do not exist yet.

**Step 3: Write minimal implementation**

Add:

- `ClientAPIKey`
- `ClientAPIKeyScope`
- config normalization helpers
- effective-key merge logic for legacy + structured entries

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/config -run 'Test(ClientAPIKeys|EffectiveClientAPIKeys)' -count=1
```

Expected: PASS.

### Task 2: Make Request Auth Return Scope Metadata

**Files:**
- Modify: `apps/server-go/internal/access/config_access/provider.go`
- Test: `apps/server-go/internal/access/config_access/provider_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- structured scoped keys authenticate successfully
- disabled structured keys are rejected
- returned metadata includes provider/auth-id scope
- legacy keys still authenticate

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/access/config_access -run 'TestAuthenticate' -count=1
```

Expected: FAIL because the provider only knows flat keys today.

**Step 3: Write minimal implementation**

Update the config access provider so it authenticates against effective client keys and returns string metadata for:

- source
- note
- scope provider
- scope auth-id
- scope models

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/access/config_access -run 'TestAuthenticate' -count=1
```

Expected: PASS.

### Task 3: Enforce Scoped Keys in Model Listings and Execution

**Files:**
- Modify: `apps/server-go/internal/api/server.go`
- Modify: `apps/server-go/sdk/api/handlers/handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/openai/openai_handlers.go`
- Modify: `apps/server-go/sdk/api/handlers/gemini/gemini_handlers.go`
- Test: `apps/server-go/internal/api/server_models_contract_test.go`
- Test: `apps/server-go/sdk/api/handlers/openai/openai_models_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- `/v1/models` returns only models for the scoped auth when a scoped key is used
- legacy unscoped keys still see the unified catalog
- execution metadata is pinned to the scoped auth and unsupported models are rejected

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/api ./sdk/api/handlers/openai -run 'Test(OpenAIModels|UnifiedModelsHandler|Scoped)' -count=1
```

Expected: FAIL because scoped keys do not affect listings or execution yet.

**Step 3: Write minimal implementation**

Implement scope handling by:

- reading scoped metadata in `AuthMiddleware`
- exposing scope to handlers through gin context
- filtering provider resolution for scoped requests
- pinning execution to `auth-id` when present
- filtering model listings to scoped auth-visible models

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/api ./sdk/api/handlers/openai -run 'Test(OpenAIModels|UnifiedModelsHandler|Scoped)' -count=1
```

Expected: PASS.

### Task 4: Add Management Endpoints for Scoped Keys and Auth Inventory

**Files:**
- Modify: `apps/server-go/internal/api/server.go`
- Modify: `apps/server-go/internal/api/handlers/management/auth_files.go`
- Modify: `apps/server-go/internal/api/handlers/management/config_lists.go`
- Test: `apps/server-go/internal/api/handlers/management/client_api_keys_test.go`

**Step 1: Write the failing test**

Add tests that verify:

- `GET /v0/management/client-api-keys` returns structured effective keys
- `PUT /v0/management/client-api-keys` persists structured keys
- `GET /v0/management/auth-files?include_models=true` embeds models on each auth entry

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/api/handlers/management -run 'Test(ClientAPIKeys|ListAuthFilesIncludeModels)' -count=1
```

Expected: FAIL because the endpoints/behavior do not exist yet.

**Step 3: Write minimal implementation**

Add:

- new structured key management endpoints
- auth-files `include_models=true` support
- migration behavior that writes GUI-managed keys into `client-api-keys`

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/api/handlers/management -run 'Test(ClientAPIKeys|ListAuthFilesIncludeModels)' -count=1
```

Expected: PASS.

### Task 5: Move Menubar Key Management to Management APIs

**Files:**
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/APIClient.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/UsageMonitorViewModel.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarDashboardView.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/APIKeyStore.swift`
- Test: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/APIClientTests.swift`
- Test: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/UsageMonitorViewModelTests.swift`

**Step 1: Write the failing test**

Add tests that verify:

- the API client can decode auth inventory with embedded models
- the API client can decode structured client keys
- the view model updates key/account state from management responses

**Step 2: Run test to verify it fails**

Run:

```bash
swift test --package-path apps/menubar-swift --filter 'APIClientTests|UsageMonitorViewModelTests'
```

Expected: FAIL because the API client/view model only know flat local keys today.

**Step 3: Write minimal implementation**

Update the menubar so it:

- loads auth inventory from management API
- loads/scopes client keys from management API
- saves the full structured key list back through `PUT /v0/management/client-api-keys`
- shows provider/account/model hierarchy and key binding state

Keep local key generation helper if useful, but stop using YAML edits for scoped key management.

**Step 4: Run test to verify it passes**

Run:

```bash
swift test --package-path apps/menubar-swift --filter 'APIClientTests|UsageMonitorViewModelTests'
```

Expected: PASS.

### Task 6: Focused End-to-End Verification

**Files:**
- Modify: `docs/plans/2026-03-08-client-api-key-scoping-design.md`

**Step 1: Run focused Go tests**

Run:

```bash
go test ./internal/config ./internal/access/config_access ./internal/api ./internal/api/handlers/management ./sdk/api/handlers/openai -count=1
```

Expected: PASS.

**Step 2: Run focused Swift tests**

Run:

```bash
swift test --package-path apps/menubar-swift
```

Expected: PASS.

**Step 3: Run local live verification**

Verify:

```bash
curl -sS http://127.0.0.1:8317/v0/management/auth-files?include_models=true -H "Authorization: Bearer $MANAGEMENT_KEY"
curl -sS http://127.0.0.1:8317/v0/management/client-api-keys -H "Authorization: Bearer $MANAGEMENT_KEY"
curl -sS http://127.0.0.1:8317/v1/models -H "Authorization: Bearer $SCOPED_KEY"
```

Expected:

- auth inventory contains provider/account/models
- client API keys contain provider/auth scope
- scoped key only sees its bound account models

**Step 4: Update design doc with verification notes if needed**

Record any constraint discovered during live verification.
