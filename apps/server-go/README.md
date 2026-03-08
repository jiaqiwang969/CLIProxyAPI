# server-go

This directory hosts the CLIProxy Go backend used by the menubar app.

## Current scope

- Runtime providers: `auggie`, `antigravity`
- Login commands: `go run ./cmd/server -auggie-login` and `go run ./cmd/server -antigravity-login`
- Management surface kept for the menubar: `/v0/management/*`
- Removed from the active server surface: TUI mode, web token console, desktop frontend bootstrap, non-target provider login entrypoints

## Build

```bash
go build -o cli-proxy-api ./cmd/server
```

## Verify

```bash
go test ./internal/translator ./internal/cmd ./sdk/auth ./sdk/cliproxy
go build ./cmd/server
```
