# MenuBar Adaptive Panel Layout Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the menubar panels grow with content when the content is small, then switch to scrolling at sensible caps so key creation and list growth feel natural across tabs.

**Architecture:** Introduce a small layout helper that computes panel heights from data counts, then route MenuBarDashboardView through those helpers so Keys, Service logs, and Usage share one sizing strategy. Keep the fix view-local and deterministic, and verify the sizing logic with focused unit tests instead of brittle SwiftUI structure tests.

**Tech Stack:** SwiftUI, Swift Testing, AppKit

---

### Task 1: Add layout helper tests

**Files:**
- Create: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/MenuBarLayoutTests.swift`
- Test: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/MenuBarLayoutTests.swift`

**Step 1: Write the failing tests**
- Cover `keysListHeight(for:)` so small key counts expand the panel and large counts clamp to a cap.
- Cover `serviceLogHeight(for:)` so a few logs grow naturally and many logs clamp.
- Cover `usageListHeight(for:)` so usage rows aggregate key and model counts, then clamp.

**Step 2: Run test to verify it fails**
- Run: `cd apps/menubar-swift && swift test --filter MenuBarLayoutTests`
- Expected: FAIL because layout helper does not exist yet.

**Step 3: Write minimal implementation**
- Create a helper such as `MenuBarLayout.swift` with static sizing functions and constants.

**Step 4: Run test to verify it passes**
- Run: `cd apps/menubar-swift && swift test --filter MenuBarLayoutTests`
- Expected: PASS.

### Task 2: Wire adaptive heights into the dashboard

**Files:**
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarDashboardView.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarLayout.swift`

**Step 1: Write the failing behavior test or deterministic assertion**
- Extend `MenuBarLayoutTests.swift` if needed for any final constants used by the view.

**Step 2: Run test to verify it fails**
- Run the targeted layout tests again if a new assertion was added.

**Step 3: Write minimal implementation**
- Replace the hard-coded `maxHeight` values for key list, usage list, and service log list with helper-driven heights.
- Remove unnecessary fixed-height caps for settings or other static sections if they constrain natural layout without adding value.
- Keep a hard upper bound so each section still scrolls once its content gets large.
- Preserve the existing 400pt width unless a concrete width issue appears during verification.

**Step 4: Run tests to verify it passes**
- Run: `cd apps/menubar-swift && swift test`
- Expected: PASS.

### Task 3: Install and verify the live menubar app

**Files:**
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarDashboardView.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/MenuBarLayout.swift`

**Step 1: Rebuild and install the app**
- Run: `cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && make install-menubar`

**Step 2: Restart the live app**
- Quit the current `CLIProxyMenuBar.app` process and reopen `/Users/jqwang/Applications/CLIProxyMenuBar.app`.

**Step 3: Verify runtime behavior**
- Open `Keys`, add keys, and confirm the panel grows while counts are small and only starts scrolling after the cap.
- Check `Service` and `Usage` tabs for the same behavior pattern.

**Step 4: Final verification**
- Run fresh tests before claiming completion: `cd apps/menubar-swift && swift test`.
