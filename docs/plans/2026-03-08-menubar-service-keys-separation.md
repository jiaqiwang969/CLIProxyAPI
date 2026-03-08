# Menubar Service/Keys Separation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move provider/account/status information into the Service tab while keeping the Keys tab focused on key binding and key operations.

**Architecture:** Add a dedicated service grouping model in the Swift menubar view model, render it in the Service tab, and simplify the Keys tab presentation so it no longer duplicates service-state metadata. Keep all data sourced from existing management endpoints.

**Tech Stack:** SwiftUI, Swift Testing

---

### Task 1: Add failing tests for service grouping

**Files:**
- Modify: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/UsageMonitorViewModelTests.swift`

**Step 1: Write the failing test**

- Add a test asserting the view model exposes provider/account service groups with status and bound key counts.

**Step 2: Run test to verify it fails**

Run: `cd apps/menubar-swift && swift test --filter serviceProviderGroups`
Expected: FAIL because the property/model does not exist yet.

**Step 3: Write minimal implementation**

- Add service grouping models and a computed property in the view model.

**Step 4: Run test to verify it passes**

Run: `cd apps/menubar-swift && swift test --filter serviceProviderGroups`
Expected: PASS

### Task 2: Add failing tests for layout sizing

**Files:**
- Modify: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/MenuBarLayoutTests.swift`

**Step 1: Write the failing test**

- Add a test for service account group height growth and clamping.

**Step 2: Run test to verify it fails**

Run: `cd apps/menubar-swift && swift test --filter serviceAccountGroupsHeight`
Expected: FAIL because the layout helper does not exist yet.

**Step 3: Write minimal implementation**

- Add a service account group height helper in `MenuBarLayout.swift`.

**Step 4: Run test to verify it passes**

Run: `cd apps/menubar-swift && swift test --filter serviceAccountGroupsHeight`
Expected: PASS

### Task 3: Update the menubar views

**Files:**
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/KeyManagementModels.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/UsageMonitorViewModel.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarLayout.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarDashboardView.swift`

**Step 1: Implement service tab account grouping**

- Render provider/account cards in the Service tab using the new grouped model.

**Step 2: Simplify Keys tab metadata**

- Remove status lights, status text, secondary account metadata, and other service-state duplication from the Keys tab.

**Step 3: Keep key operations intact**

- Preserve add/generate/copy/delete/toggle/note behaviors unchanged.

### Task 4: Verify

**Files:**
- Test: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/*.swift`

**Step 1: Run targeted Swift tests**

Run: `cd apps/menubar-swift && swift test --filter UsageMonitorViewModelTests`

**Step 2: Run full Swift tests**

Run: `cd apps/menubar-swift && swift test`

**Step 3: Manual runtime check**

- Restart the menubar app and confirm:
  - 服务页能看到 antigravity / auggie 的账号状态
  - Keys 页只负责 key 绑定和 key 管理
  - 窗口高度仍按内容自适应
