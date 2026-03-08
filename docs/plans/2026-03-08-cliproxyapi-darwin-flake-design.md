# CLIProxyAPI Darwin Flake Design

## Goal

Make `CLIProxyAPI-wjq` consumable from `nixos-config` the same way `endpoint-sec` is consumed today, so `make macbook-pro-m4` can install and start the local CLIProxy backend and refresh the Swift menubar app.

## Constraints

- Keep the repository focused on two apps only:
  - `apps/server-go`
  - `apps/menubar-swift`
- Prefer the fastest maintainable path over full packaging purity.
- Keep `auggie` and `antigravity` as the active providers.
- Do not make the menubar app depend on repo-relative runtime paths after deployment.
- Preserve the existing local login flows:
  - `-auggie-login`
  - `-antigravity-login`

## Chosen Approach

Use a hybrid deployment model:

1. `CLIProxyAPI-wjq` exports a Go backend package and a `darwinModules.default` module.
2. The Darwin module manages the backend as a user launch agent.
3. The Swift menubar remains pragmatically installed by a repository script that builds a local `.app` bundle and places it in `~/Applications`.
4. `nixos-config` imports the new module and enables it on `macbook-pro-m4`.
5. `make macbook-pro-m4` continues to be the single entrypoint and refreshes the menubar app after the nix-darwin switch.

This keeps the backend declarative and repeatable while avoiding the complexity of fully packaging, signing, and distributing the Swift menubar via Nix on the first pass.

## Runtime Layout

- Backend config file: `~/.cliproxyapi/config.yaml`
- Backend auth directory: `~/.cliproxyapi/auth`
- Menubar app bundle: `~/Applications/CLIProxyMenuBar.app`
- Backend launch agent label: `com.jiaqi.cliproxy-api`
- Backend binary path exposed by the package: `${package}/bin/cli-proxy-api`

## Darwin Module Shape

The module will expose these pragmatic options:

- `services.cliproxy-api.enable`
- `services.cliproxy-api.package`
- `services.cliproxy-api.user`
- `services.cliproxy-api.host`
- `services.cliproxy-api.port`
- `services.cliproxy-api.managementKey`
- `services.cliproxy-api.authDir`
- `services.cliproxy-api.workDir`
- `services.cliproxy-api.apiKeys`
- `services.cliproxy-api.disableControlPanel`
- `services.cliproxy-api.usageStatisticsEnabled`

The module will:

- create the runtime directory under the target user home
- materialize `config.yaml`
- ensure the auth directory exists
- install login helper wrappers that always pass `-config ~/.cliproxyapi/config.yaml`
- start the backend with `launchd.user.agents`

`launchd.user.agents` is the correct level because the service needs normal access to the user home directory and OAuth artifacts, and it does not require root privileges.

## Menubar Packaging Strategy

The current menubar source is a SwiftPM app, not an Xcode project or existing `.app` bundle. The repository will therefore add two scripts:

- `scripts/build-menubar-app.sh`
- `scripts/install-menubar.sh`

They will:

1. run `swift build`
2. assemble a minimal `.app` bundle
3. write a minimal `Info.plist`
4. copy the SwiftPM binary into `Contents/MacOS`
5. ad-hoc sign the bundle
6. install it to `~/Applications`

This keeps notifications and launch-at-login behavior compatible with real app-bundle execution without forcing a full Xcode packaging migration.

## nixos-config Integration

`nixos-config` will:

- add `CLIProxyAPI` as a flake input
- import `inputs.cliproxy-api.darwinModules.default` for Darwin systems
- enable `services.cliproxy-api` in `machines/macbook-pro-m4.nix`
- update `Makefile` so `make macbook-pro-m4` refreshes the menubar app after `darwin-rebuild switch`

## Verification Plan

The implementation is complete when the following are true:

1. `nix flake show` works in `CLIProxyAPI-wjq`
2. `nix build .#packages.aarch64-darwin.default` builds the backend package
3. `make install-menubar` creates `~/Applications/CLIProxyMenuBar.app`
4. `nix build .#darwinConfigurations.macbook-pro-m4.system` works in `nixos-config`
5. the generated launch agent references the CLIProxy package and generated config
6. `cliproxy-auggie-login` and `cliproxy-antigravity-login` point at the managed config path

## Deferred Work

These are intentionally out of scope for this pass:

- fully packaging the Swift menubar in Nix
- notarization and production signing
- secret management via sops/agenix
- multi-machine abstraction for other hosts
