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

## Local Auggie Responses Smoke Test

The quickest way to validate the OpenAI-compatible surface is to run the Go backend locally and hit `POST /v1/responses`.

From the repository root:

```bash
cd apps/server-go
cp config.example.yaml config.yaml
```

Then set at least these fields in `config.yaml`:

```yaml
host: "127.0.0.1"
port: 8317
auth-dir: "~/.cli-proxy-api"
api-keys:
  - "sk-local-auggie-test"
```

Complete Auggie upstream authentication in one of these ways:

- Official Auggie CLI session: `npm install -g @augmentcode/auggie` then `auggie login`
- Proxy-managed login: `go run ./cmd/server -config ./config.yaml -auggie-login`

Start the backend:

```bash
go run ./cmd/server -config ./config.yaml
```

Then verify models and `responses`:

```bash
curl -sS http://127.0.0.1:8317/v1/models \
  -H "Authorization: Bearer sk-local-auggie-test" | jq '.data[].id'

curl -sS http://127.0.0.1:8317/v1/responses \
  -H "Authorization: Bearer sk-local-auggie-test" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4","input":"hi"}' | jq '.'
```

Important:

- The `Authorization: Bearer ...` value used by `curl` is the local proxy API key from `config.yaml`.
- It is not the Auggie upstream access token.
- Detailed Auggie auth, runtime file locations, and troubleshooting are documented in [apps/server-go/README.md](/Users/jqwang/05-api-代理/CLIProxyAPI-wjq/apps/server-go/README.md).

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
