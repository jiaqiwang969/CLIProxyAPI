# CLIProxyAPI Darwin Flake Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a Darwin-consumable flake to `CLIProxyAPI-wjq`, wire it into `nixos-config`, and make `make macbook-pro-m4` install the CLIProxy backend plus refresh the Swift menubar app.

**Architecture:** Build the Go backend as the flake package, expose a `darwinModules.default` module that writes a stable user-scoped runtime config and launch agent, and keep the Swift menubar on a pragmatic script-based `.app` installation path. Integrate the module into `nixos-config` so the local Darwin configuration can enable the service declaratively while Makefile glue refreshes the GUI after each switch.

**Tech Stack:** Nix flakes, nix-darwin, Go, SwiftPM, shell scripts, launchd

---

### Task 1: Prove the current repository lacks the Darwin flake entrypoints

**Files:**
- Create: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/flake.nix`
- Create: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/nix/modules/cliproxy-api-darwin.nix`
- Modify: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/README.md`

**Step 1: Write the failing verification command**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && nix flake show
```

Expected: FAIL because the repository does not yet contain `flake.nix`.

**Step 2: Add the minimal flake and Darwin module**

- Define `packages.aarch64-darwin.default`
- Define `packages.x86_64-darwin.default`
- Export `darwinModules.default = import ./nix/modules/cliproxy-api-darwin.nix`
- Document the new flake entrypoint in `README.md`

**Step 3: Run the verification command again**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && nix flake show
```

Expected: PASS with package outputs and `darwinModules.default`.

### Task 2: Add a packaged backend plus login helper wrappers

**Files:**
- Modify: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/flake.nix`
- Modify: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/nix/modules/cliproxy-api-darwin.nix`

**Step 1: Write the failing build command**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && nix build .#packages.aarch64-darwin.default
```

Expected: FAIL because the backend package definition does not exist or does not build.

**Step 2: Implement the package**

- Build `apps/server-go/cmd/server`
- Install `cli-proxy-api`
- Install `cliproxy-auggie-login`
- Install `cliproxy-antigravity-login`
- Make the wrappers pass a stable config path under `~/.cliproxyapi/config.yaml`

**Step 3: Run the build command again**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && nix build .#packages.aarch64-darwin.default
```

Expected: PASS and expose the backend binary plus wrappers under `result/bin`.

### Task 3: Add menubar app bundle build and install scripts

**Files:**
- Create: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/scripts/build-menubar-app.sh`
- Create: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/scripts/install-menubar.sh`
- Create: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/Makefile`
- Modify: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/README.md`

**Step 1: Write the failing install command**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && make install-menubar
```

Expected: FAIL because the repository does not yet define the target or scripts.

**Step 2: Implement the scripts**

- Build the SwiftPM menubar binary
- Assemble `CLIProxyMenuBar.app`
- Write `Info.plist`
- Copy the binary to `Contents/MacOS`
- Ad-hoc sign the bundle
- Install it to `~/Applications/CLIProxyMenuBar.app`

**Step 3: Run the install command again**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && make install-menubar
```

Expected: PASS and create `~/Applications/CLIProxyMenuBar.app`.

### Task 4: Materialize the Darwin runtime config and launch agent

**Files:**
- Modify: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/nix/modules/cliproxy-api-darwin.nix`

**Step 1: Write the failing evaluation/build command**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && nix build .#packages.aarch64-darwin.default
```

Expected: FAIL or be incomplete relative to the desired runtime behavior because the module does not yet generate config and launchd settings.

**Step 2: Implement the Darwin module behavior**

- Add `services.cliproxy-api.*` options
- Generate `~/.cliproxyapi/config.yaml`
- Ensure the auth directory exists
- Register `launchd.user.agents.cliproxy-api`
- Start the backend using the package binary and managed config file

**Step 3: Re-run flake output verification**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && nix eval .#darwinModules.default --apply builtins.typeOf
```

Expected: PASS and return `lambda` or equivalent module function type.

### Task 5: Wire `nixos-config` to consume the new flake

**Files:**
- Modify: `/Users/jqwang/00-nixos-config/nixos-config/flake.nix`
- Modify: `/Users/jqwang/00-nixos-config/nixos-config/lib/mksystem.nix`
- Modify: `/Users/jqwang/00-nixos-config/nixos-config/machines/macbook-pro-m4.nix`
- Modify: `/Users/jqwang/00-nixos-config/nixos-config/Makefile`

**Step 1: Write the failing integration verification**

Run:

```bash
cd /Users/jqwang/00-nixos-config/nixos-config && nix build .#darwinConfigurations.macbook-pro-m4.system
```

Expected: PASS today without CLIProxy integration, but missing CLIProxy wiring. Treat the missing `cliproxy-api` input/module references as the failing requirement.

**Step 2: Implement the integration**

- Add the flake input
- Import the Darwin module alongside `agentsmith-rs`
- Enable `services.cliproxy-api` on `macbook-pro-m4`
- Update `make macbook-pro-m4` to refresh the menubar app after switch

**Step 3: Run the build command again**

Run:

```bash
cd /Users/jqwang/00-nixos-config/nixos-config && nix build .#darwinConfigurations.macbook-pro-m4.system
```

Expected: PASS with the CLIProxy module wired into the Darwin system graph.

### Task 6: Verify end-to-end developer workflows and publish

**Files:**
- Modify: `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/README.md`

**Step 1: Verify the package output**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq && ls result/bin
```

Expected: includes `cli-proxy-api`, `cliproxy-auggie-login`, `cliproxy-antigravity-login`.

**Step 2: Verify the menubar bundle**

Run:

```bash
test -d /Users/jqwang/Applications/CLIProxyMenuBar.app
```

Expected: PASS after install.

**Step 3: Verify the nixos-config wiring**

Run:

```bash
cd /Users/jqwang/00-nixos-config/nixos-config && rg -n "cliproxy-api" flake.nix lib/mksystem.nix machines/macbook-pro-m4.nix Makefile
```

Expected: PASS with references in all required integration points.

**Step 4: Commit and push**

Run:

```bash
cd /Users/jqwang/05-api-代理/CLIProxyAPI-wjq
git add .
git commit -m "feat: add darwin flake integration for CLIProxyAPI"
git push -u git@github.com:jiaqiwang969/CLIProxyAPI.git main
```

Expected: PASS, with the commit message including the required `Co-authored-by: Codex <noreply@openai.com>` trailer.
