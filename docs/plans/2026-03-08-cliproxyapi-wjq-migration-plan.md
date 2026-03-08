# CLIProxyAPI-wjq Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a clean new repository at `CLIProxyAPI-wjq` that contains only the Swift menubar frontend and the Go backend, with only `auggie` and `antigravity` retained as providers.

**Architecture:** Copy the known-good backend from the verified Auggie worktree and the existing Swift menubar into the new repository, then prune unrelated products and providers. Keep runtime behavior stable first, add pragmatic DDD context entry points second, and normalize external model naming to provider-prefixed canonical IDs without breaking existing callers immediately.

**Tech Stack:** Go 1.26, Gin, SwiftUI/AppKit menubar app, shell scripts, Git, macOS local process control

---

### Task 1: Scaffold the new repository layout

**Files:**
- Create: `README.md`
- Create: `.gitignore`
- Create: `scripts/smoke.sh`
- Create: `apps/server-go/README.md`
- Create: `apps/menubar-swift/README.md`
- Test: `scripts/smoke.sh`

**Step 1: Write the failing smoke script**

Create `scripts/smoke.sh` to assert that these paths exist:

- `apps/server-go`
- `apps/menubar-swift`
- `docs/plans/2026-03-08-cliproxyapi-wjq-ddd-design.md`

Exit non-zero when any path is missing.

**Step 2: Run the smoke script to verify it fails**

Run:

```bash
bash scripts/smoke.sh
```

Expected: FAIL because `apps/server-go` and `apps/menubar-swift` do not exist yet.

**Step 3: Write minimal repository metadata**

Create:

- `README.md` with a one-paragraph project overview
- `.gitignore` covering `.DS_Store`, build outputs, logs, Swift `.build`, Go binaries, and temp files
- `apps/server-go/README.md`
- `apps/menubar-swift/README.md`

**Step 4: Create the minimal directory layout**

Create:

- `apps/server-go/`
- `apps/menubar-swift/`
- `scripts/`

**Step 5: Re-run the smoke script**

Run:

```bash
bash scripts/smoke.sh
```

Expected: PASS for directory existence checks.

**Step 6: Commit**

```bash
git add README.md .gitignore scripts/smoke.sh apps/server-go/README.md apps/menubar-swift/README.md
git commit -m "chore: scaffold CLIProxyAPI-wjq layout"
```

### Task 2: Copy the verified Go backend baseline

**Files:**
- Create: `apps/server-go/**`
- Source: `/Users/jqwang/05-api-代理/CLIProxyAPI/.codex/worktrees/agent/auggie-provider-plan/**`
- Test: `apps/server-go/cmd/server/main.go`

**Step 1: Verify the target backend is absent**

Run:

```bash
test -f apps/server-go/cmd/server/main.go
```

Expected: FAIL because the backend has not been copied yet.

**Step 2: Copy the backend from the verified Auggie worktree**

Run:

```bash
rsync -a \
  --exclude '.git' \
  --exclude '.codex' \
  --exclude 'tmp' \
  --exclude 'logs' \
  /Users/jqwang/05-api-代理/CLIProxyAPI/.codex/worktrees/agent/auggie-provider-plan/ \
  apps/server-go/
```

**Step 3: Verify the copy landed**

Run:

```bash
test -f apps/server-go/cmd/server/main.go
```

Expected: PASS.

**Step 4: Run the smallest meaningful backend verification**

Run:

```bash
cd apps/server-go && go test ./internal/runtime/executor && go build ./cmd/server
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go
git commit -m "chore: copy verified backend baseline"
```

### Task 3: Copy the Swift menubar application

**Files:**
- Create: `apps/menubar-swift/**`
- Source: `/Users/jqwang/05-api-代理/cliProxyAPI-Dashboard/macos-menubar/CLIProxyMenuBar/**`
- Test: `apps/menubar-swift/Package.swift`

**Step 1: Verify the target menubar app is absent**

Run:

```bash
test -f apps/menubar-swift/Package.swift
```

Expected: FAIL.

**Step 2: Copy the menubar source**

Run:

```bash
rsync -a \
  --exclude '.build' \
  --exclude '*.app' \
  /Users/jqwang/05-api-代理/cliProxyAPI-Dashboard/macos-menubar/CLIProxyMenuBar/ \
  apps/menubar-swift/
```

**Step 3: Verify the copy landed**

Run:

```bash
test -f apps/menubar-swift/Package.swift
```

Expected: PASS.

**Step 4: Build the menubar app**

Run:

```bash
cd apps/menubar-swift && swift build
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/menubar-swift
git commit -m "chore: copy menubar app"
```

### Task 4: Remove non-target products and packages

**Files:**
- Delete: `apps/server-go/desktop/**`
- Delete: `apps/server-go/web/**`
- Delete: `apps/server-go/examples/**`
- Delete: `apps/server-go/CLIPROXY控制台.app/**`
- Delete: `apps/server-go/internal/tui/**`
- Delete: `apps/server-go/internal/browser/**`
- Delete: `apps/server-go/internal/console/**`
- Delete: `apps/server-go/internal/auth/{claude,codex,empty,gemini,iflow,kimi,qwen,vertex}/**`
- Delete: non-target executors and translators under `apps/server-go/internal/runtime/` and `apps/server-go/internal/translator/`
- Modify: `apps/server-go/go.mod`
- Test: `apps/server-go/go.mod`

**Step 1: Prove that banned surfaces are still present**

Run:

```bash
find apps/server-go -maxdepth 3 \( \
  -path '*/desktop*' -o \
  -path '*/web*' -o \
  -path '*/examples*' -o \
  -path '*/internal/tui*' -o \
  -path '*/internal/browser*' -o \
  -path '*/internal/console*' \
\) | head
```

Expected: non-empty output.

**Step 2: Delete the non-target surfaces**

Remove all non-target products and provider packages listed above.

**Step 3: Tidy Go dependencies**

Run:

```bash
cd apps/server-go && go mod tidy
```

**Step 4: Verify the backend still builds**

Run:

```bash
cd apps/server-go && go build ./cmd/server
```

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/server-go
git commit -m "refactor: prune non-target products and providers"
```

### Task 5: Add pragmatic DDD context entry points

**Files:**
- Create: `apps/server-go/internal/context/controlplane/service.go`
- Create: `apps/server-go/internal/context/inference/service.go`
- Create: `apps/server-go/internal/context/modelcatalog/service.go`
- Create: `apps/server-go/internal/context/provideraccess/service.go`
- Create: `apps/server-go/internal/context/usage/service.go`
- Create: `apps/server-go/internal/context/modelcatalog/service_test.go`
- Modify: `apps/server-go/internal/api/server.go`
- Modify: `apps/server-go/cmd/server/main.go`

**Step 1: Write the failing model catalog normalization test**

In `apps/server-go/internal/context/modelcatalog/service_test.go`, write table tests for:

- `gpt-5-4` -> `auggie-gpt-5-4`
- `claude-sonnet-4-6` -> `auggie-claude-sonnet-4-6`
- `gemini-3.1-pro-high` -> `antigravity-gemini-3.1-pro-high`
- `auggie-gpt-5-4` stays unchanged

**Step 2: Run the test to verify it fails**

Run:

```bash
cd apps/server-go && go test ./internal/context/modelcatalog -run TestNormalizeModelID -v
```

Expected: FAIL because the package does not exist yet.

**Step 3: Write the minimal context services**

Create thin service packages for:

- `controlplane`
- `inference`
- `modelcatalog`
- `provideraccess`
- `usage`

The first version only needs:

- constructors
- small interfaces
- model normalization in `modelcatalog`
- adapters that call existing packages instead of rewriting logic

**Step 4: Re-run the model catalog test**

Run:

```bash
cd apps/server-go && go test ./internal/context/modelcatalog -run TestNormalizeModelID -v
```

Expected: PASS.

**Step 5: Wire the new services into server construction**

Modify:

- `apps/server-go/internal/api/server.go`
- `apps/server-go/cmd/server/main.go`

Only add orchestration hooks. Do not rewrite stable handler internals in this task.

**Step 6: Run the package-level verification**

Run:

```bash
cd apps/server-go && go test ./internal/context/... ./internal/api/... && go build ./cmd/server
```

Expected: PASS.

**Step 7: Commit**

```bash
git add apps/server-go/internal/context apps/server-go/internal/api/server.go apps/server-go/cmd/server/main.go
git commit -m "refactor: add pragmatic DDD context entry points"
```

### Task 6: Normalize canonical model naming and compatibility aliases

**Files:**
- Modify: `apps/server-go/internal/context/modelcatalog/service.go`
- Modify: `apps/server-go/internal/registry/model_registry.go`
- Modify: `apps/server-go/internal/config/oauth_model_alias_migration.go`
- Create: `apps/server-go/internal/context/modelcatalog/aliases_test.go`

**Step 1: Write the failing alias compatibility test**

Add tests covering:

- prefixed canonical IDs are returned by the catalog
- legacy bare IDs still resolve
- provider identity is preserved in the normalized result

**Step 2: Run the test to verify it fails**

Run:

```bash
cd apps/server-go && go test ./internal/context/modelcatalog -run 'TestCanonical|TestLegacyAlias' -v
```

Expected: FAIL.

**Step 3: Implement the minimal normalization logic**

Update the model catalog to:

- expose canonical prefixed IDs
- accept legacy aliases temporarily
- map legacy IDs to the correct provider-prefixed canonical IDs

Update config alias migration helpers only where required to keep existing config behavior stable.

**Step 4: Re-run the tests**

Run:

```bash
cd apps/server-go && go test ./internal/context/modelcatalog -run 'TestCanonical|TestLegacyAlias' -v
```

Expected: PASS.

**Step 5: Verify `/v1/models` behavior**

Run:

```bash
cd apps/server-go && go test ./internal/api -run TestServer -v
```

Expected: PASS, with model output ready to be updated in follow-up assertions if needed.

**Step 6: Commit**

```bash
git add apps/server-go/internal/context/modelcatalog apps/server-go/internal/registry/model_registry.go apps/server-go/internal/config/oauth_model_alias_migration.go
git commit -m "feat: normalize provider-prefixed model IDs"
```

### Task 7: Keep only Auggie and Antigravity on the live runtime path

**Files:**
- Modify: `apps/server-go/internal/cmd/auth_manager.go`
- Modify: `apps/server-go/internal/constant/constant.go`
- Modify: `apps/server-go/internal/translator/init.go`
- Modify: `apps/server-go/internal/api/handlers/management/**`
- Modify: `apps/server-go/internal/runtime/executor/**`
- Create: `apps/server-go/internal/api/provider_surface_test.go`

**Step 1: Write the failing provider surface test**

Add a test that asserts:

- provider listings expose only `auggie` and `antigravity`
- model output contains only prefixed IDs for those providers

**Step 2: Run the test to verify it fails**

Run:

```bash
cd apps/server-go && go test ./internal/api -run TestProviderSurface -v
```

Expected: FAIL.

**Step 3: Remove non-target provider registrations from the live path**

Update:

- auth manager registration
- translator registration
- management provider listing
- any runtime registry or route listing that still exposes removed providers

Do not leave dead menu entries or dead management endpoints for removed providers.

**Step 4: Re-run the provider surface test**

Run:

```bash
cd apps/server-go && go test ./internal/api -run TestProviderSurface -v
```

Expected: PASS.

**Step 5: Run focused backend verification**

Run:

```bash
cd apps/server-go && go test ./internal/api ./internal/runtime/executor ./internal/usage && go build ./cmd/server
```

Expected: PASS.

**Step 6: Commit**

```bash
git add apps/server-go/internal/cmd/auth_manager.go apps/server-go/internal/constant/constant.go apps/server-go/internal/translator/init.go apps/server-go/internal/api apps/server-go/internal/runtime/executor apps/server-go/internal/usage
git commit -m "refactor: restrict runtime to auggie and antigravity"
```

### Task 8: Update menubar runtime behavior for the new repository layout

**Files:**
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/RuntimeConfig.swift`
- Modify: `apps/menubar-swift/Sources/CLIProxyMenuBarApp/ServiceControl.swift`
- Create: `apps/menubar-swift/Tests/CLIProxyMenuBarAppTests/RuntimeConfigLoaderTests.swift`

**Step 1: Write the failing Swift runtime config test**

Cover:

- repo-local config discovery from the new layout
- backend binary path resolution under `apps/server-go`
- localhost base URL fallback

**Step 2: Run the test to verify it fails**

Run:

```bash
cd apps/menubar-swift && swift test
```

Expected: FAIL because the test target does not exist yet.

**Step 3: Implement the minimal runtime path updates**

Update the menubar so it:

- looks for config under the new repository structure
- resolves the backend binary path correctly
- keeps the existing localhost control-plane behavior

**Step 4: Re-run Swift tests**

Run:

```bash
cd apps/menubar-swift && swift test
```

Expected: PASS.

**Step 5: Rebuild the menubar app**

Run:

```bash
cd apps/menubar-swift && swift build
```

Expected: PASS.

**Step 6: Commit**

```bash
git add apps/menubar-swift
git commit -m "feat: retarget menubar to new repo layout"
```

### Task 9: Add end-to-end verification and operator docs

**Files:**
- Modify: `README.md`
- Modify: `apps/server-go/README.md`
- Modify: `apps/menubar-swift/README.md`
- Modify: `scripts/smoke.sh`

**Step 1: Extend the smoke script with real checks**

Make `scripts/smoke.sh` verify:

- `go build ./cmd/server`
- `swift build`
- target docs exist
- target apps exist

**Step 2: Run the smoke script to verify it fails before docs are updated**

Run:

```bash
bash scripts/smoke.sh
```

Expected: FAIL until commands and docs are aligned with the final layout.

**Step 3: Write the minimal operator docs**

Update:

- top-level quick start
- backend run/build instructions
- menubar run/build instructions
- source-of-truth note about Auggie worktree provenance

**Step 4: Re-run the smoke script**

Run:

```bash
bash scripts/smoke.sh
```

Expected: PASS.

**Step 5: Run final manual verification**

Run:

```bash
# build backend
cd apps/server-go && go build -o ../../tmp/cli-proxy-api ./cmd/server

# build menubar
cd ../menubar-swift && swift build

# then manually verify:
# 1. /v1/models
# 2. one Auggie request
# 3. one Antigravity request
# 4. /v0/management/usage
# 5. menubar usage visibility
# 6. menubar start/stop
```

Expected: all checks succeed before using `@superpowers:verification-before-completion`.

**Step 6: Commit**

```bash
git add README.md apps/server-go/README.md apps/menubar-swift/README.md scripts/smoke.sh
git commit -m "docs: add migration operator guide and smoke checks"
```

### Task 10: Final cleanup and completion gate

**Files:**
- Modify: any files still needed after verification
- Test: final repo status

**Step 1: Run final verification**

Run:

```bash
git status --short
cd apps/server-go && go test ./...
cd ../menubar-swift && swift test
bash ../../scripts/smoke.sh
```

Expected: all tests pass and only intentional changes remain.

**Step 2: Fix any final regressions**

Only address failures proven by the verification commands above. Do not expand scope.

**Step 3: Re-run verification**

Run the same commands again.

Expected: PASS.

**Step 4: Final commit**

```bash
git add .
git commit -m "feat: migrate CLIProxyAPI-wjq to menubar plus Go backend only"
```

**Step 5: Stop and request review**

Ask for a code review using `@superpowers:requesting-code-review` before any merge or release step.
