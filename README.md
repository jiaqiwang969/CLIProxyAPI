# CLIProxyAPI-wjq

CLIProxyAPI-wjq is the trimmed CLIProxy workspace focused on two apps only: a Swift menubar frontend and a Go backend. The backend retains only the `auggie` and `antigravity` runtime providers, while the old desktop app, web console, TUI, and non-target CLI login entrypoints are removed from the active surface.

## Layout

- `apps/server-go`: Go backend
- `apps/menubar-swift`: Swift menubar app
- `docs/plans`: migration and architecture notes
- `scripts`: repository smoke checks

## Nix / Darwin

The repository now exports a Darwin-friendly flake so it can be imported from `nixos-config` just like `endpoint-sec`.

- package output: `.#packages.aarch64-darwin.default`
- Darwin module: `.#darwinModules.default`

The Darwin module manages:

- `~/.cliproxyapi/config.yaml`
- `~/.cliproxyapi/auth`
- a symlinked backend binary at `~/.cliproxyapi/cli-proxy-api`
- a `launchd` agent for the local backend

### Standalone flake checks

```bash
nix flake show
nix build .#packages.aarch64-darwin.default
```

## Backend scope

- Supported login flows: `-auggie-login`, `-antigravity-login`
- Supported runtime providers: `auggie`, `antigravity`
- Removed from the active entry surface: Gemini/Codex/Claude/Qwen/iFlow/Kimi login commands, desktop app, web token console, TUI mode

### Managed login wrappers

After installing the package through nix-darwin, these commands are available in the system profile:

- `cliproxy-auggie-login`
- `cliproxy-antigravity-login`

Both wrappers automatically target `~/.cliproxyapi/config.yaml`.

## Menubar install

The menubar app is intentionally installed with a pragmatic local script instead of full Nix packaging.

```bash
make install-menubar
make open-menubar
```

The app bundle is installed at:

- `~/Applications/CLIProxyMenuBar.app`

## nixos-config integration

The intended deployment path is:

1. add this repository as a flake input in `nixos-config`
2. import `inputs.cliproxy-api.darwinModules.default`
3. enable `services.cliproxy-api` on `macbook-pro-m4`
4. run `make macbook-pro-m4`

That keeps the backend declarative while still refreshing the Swift menubar app after each Darwin switch.
