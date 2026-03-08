# Auggie Usage And Menubar Contribution Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix Auggie token accounting from real upstream `nodes[].token_usage`, then rebuild the menubar contribution view as `provider -> account -> key -> model`.

**Architecture:** Keep the existing runtime and management endpoints, extend Auggie usage extraction at the executor boundary, and enrich the Swift management models so the UI can join usage data with scoped client keys and auth targets.

**Tech Stack:** Go, SwiftUI, Swift Testing

---

### Task 1: Lock Auggie usage root cause with failing tests

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/usage_helpers_test.go`
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_usage_test.go`

**Step 1: Write the failing unit test**

- Add a test for `parseAuggieUsage(...)` using a real Auggie terminal payload that only contains `nodes[].token_usage`.

**Step 2: Run test to verify it fails**

Run: `cd apps/server-go && go test ./internal/runtime/executor -run 'TestParseAuggieUsage|TestAuggieExecute_'`
Expected: FAIL because nested `nodes[].token_usage` is ignored.

**Step 3: Write the failing integration assertion**

- Extend the Auggie usage stats test so a streamed request must increase both request count and token count.

**Step 4: Run test to verify it fails**

Run: `cd apps/server-go && go test ./internal/runtime/executor -run TestAuggieExecute_RecordsUsage`
Expected: FAIL with token delta still equal to 0.

### Task 2: Implement Auggie usage extraction minimally

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/usage_helpers.go`

**Step 1: Parse nested Auggie node usage**

- Read `nodes.#.token_usage`.
- Map:
  - `input_tokens`
  - `output_tokens`
  - `cache_read_input_tokens` / `cache_creation_input_tokens`
  - derived `total_tokens`

**Step 2: Avoid overcounting**

- When multiple nodes expose token usage in one payload, merge by field-wise max instead of sum.

**Step 3: Run targeted tests**

Run: `cd apps/server-go && go test ./internal/runtime/executor -run 'TestParseAuggieUsage|TestAuggieExecute_RecordsUsage'`
Expected: PASS

### Task 3: Add failing menubar contribution grouping tests

**Files:**
- Modify: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/APIClientManagementTests.swift`
- Modify: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/UsageMonitorViewModelTests.swift`
- Modify: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/MenuBarLayoutTests.swift`

**Step 1: Write the failing usage decode test**

- Assert `fetchUsageSummary(...)` preserves model-level request/token totals.

**Step 2: Write the failing grouping test**

- Assert the view model exposes contribution groups ordered as provider -> account -> key -> model.

**Step 3: Write the failing layout test**

- Assert the contribution panel height grows for grouped cards and clamps at max height.

**Step 4: Run tests to verify they fail**

Run: `cd apps/menubar-swift && swift test --filter APIClientManagementTests && swift test --filter UsageMonitorViewModelTests && swift test --filter MenuBarLayoutTests`
Expected: FAIL because the richer usage/grouping models do not exist yet.

### Task 4: Implement grouped contribution data and UI

**Files:**
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/APIClient.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/KeyManagementModels.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/UsageMonitorViewModel.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarDashboardView.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarLayout.swift`

**Step 1: Enrich usage decode models**

- Decode model-level `total_requests` and `total_tokens`.

**Step 2: Build grouped contribution view models**

- Join usage keys with managed keys and auth targets.
- Group into provider/account/key/model.

**Step 3: Render grouped contribution cards**

- Replace flat key list with grouped cards.
- Keep provider separation explicit.

**Step 4: Run targeted tests**

Run: `cd apps/menubar-swift && swift test --filter APIClientManagementTests && swift test --filter UsageMonitorViewModelTests && swift test --filter MenuBarLayoutTests`
Expected: PASS

### Task 5: Full verification

**Files:**
- Test: `apps/server-go/internal/runtime/executor/...`
- Test: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/...`

**Step 1: Run Go verification**

Run: `cd apps/server-go && go test ./internal/runtime/executor`

**Step 2: Run Swift verification**

Run: `cd apps/menubar-swift && swift test`

**Step 3: Manual runtime verification**

- Confirm `/v0/management/usage` now shows non-zero `total_tokens` for Auggie keys.
- Confirm menubar “贡献”页按 `antigravity` 和 `auggie` 分组，且每组能展开看到 account / key / model。
