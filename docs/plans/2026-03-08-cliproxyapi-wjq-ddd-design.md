# CLIProxyAPI-wjq Pragmatic DDD Design

Date: 2026-03-08

## Goal

Create a clean new repository at `/Users/jqwang/05-api-代理/CLIProxyAPI-wjq` with:

- a Swift menubar frontend
- a Go backend
- only the providers currently in use: `auggie` and `antigravity`

The design favors a pragmatic DDD style: clear business boundaries, explicit provider isolation, and fast migration over textbook purity.

## Chosen Strategy

The selected migration path is:

- copy the currently working backend and menubar code into the new repository
- prune aggressively after the copy
- keep the first migration focused on running behavior
- add DDD boundaries without forcing a full rewrite on day one

This is intentionally the fastest route.

## Source of Truth

The backend baseline must come from the verified Auggie worktree, not from the main `CLIProxyAPI` working tree.

Backend source:

- `/Users/jqwang/05-api-代理/CLIProxyAPI/.codex/worktrees/agent/auggie-provider-plan`

Reason:

- it contains the validated Auggie executor integration
- it contains the Auggie usage accounting fix
- it already passed targeted executor tests
- it already produced verified real responses

The Swift menubar source comes from:

- `/Users/jqwang/05-api-代理/cliProxyAPI-Dashboard/macos-menubar/CLIProxyMenuBar`

## Architecture Style

The backend remains a modular monolith. It is not split into microservices.

Pragmatic DDD means:

- domain boundaries are explicit
- provider details stay in infrastructure and adapter code
- old packages may remain during migration if they are already stable
- new boundaries are introduced first at the entry points and orchestration layers

## Top-Level Repository Layout

```text
CLIProxyAPI-wjq/
├── README.md
├── .gitignore
├── apps/
│   ├── server-go/
│   └── menubar-swift/
├── docs/
│   └── plans/
└── scripts/
```

Only two applications are kept:

- `apps/server-go`
- `apps/menubar-swift`

Everything else is either deleted during migration or not copied at all.

## Backend Contexts

The Go backend is organized around five bounded contexts.

### 1. Control Plane

Responsibilities:

- management endpoints used by the menubar
- provider status
- auth record visibility
- usage queries
- local management metadata

Examples:

- `/v0/management/usage`
- `/v0/management/auth-files`
- `/v0/management/api-keys`
- `/v0/management/provider-status`

### 2. Inference

Responsibilities:

- request normalization
- provider routing
- streaming and non-streaming inference
- response translation to OpenAI-compatible surfaces

Examples:

- `/v1/models`
- `/v1/chat/completions`
- `/v1/responses`

### 3. Model Catalog

Responsibilities:

- canonical model IDs
- aliases for migration compatibility
- provider namespace ownership
- model metadata used for routing and presentation

### 4. Provider Access

Responsibilities:

- Auggie login and credential loading
- Antigravity login and credential loading
- token/session refresh
- auth health checks

### 5. Usage

Responsibilities:

- request counting
- token accounting
- aggregation by provider
- aggregation by canonical model ID
- management-facing summaries

## Backend Package Direction

The first migration does not require immediate deep refactoring. The initial `apps/server-go` structure should preserve the running code while adding clearer entry points.

Target direction:

```text
apps/server-go/
├── cmd/server/
├── internal/context/
│   ├── controlplane/
│   ├── inference/
│   ├── modelcatalog/
│   ├── provideraccess/
│   └── usage/
├── internal/api/
├── internal/config/
├── internal/logging/
├── internal/registry/
├── internal/runtime/
├── internal/store/
├── internal/thinking/
├── internal/translator/
├── internal/util/
├── internal/watcher/
└── sdk/
```

`internal/context/*` starts as the new boundary and orchestration layer. Legacy packages may still back those contexts until later cleanup.

## Frontend and Backend Boundary

The Swift menubar is a thin control-plane client. It is not a chat client.

The menubar is responsible for:

- querying usage
- querying provider and service status
- querying key and auth state
- starting and stopping the local backend process

The menubar does not directly call provider inference APIs.

This keeps provider-specific logic entirely in the Go backend.

## Provider Isolation Rules

The new repository treats `auggie` and `antigravity` as separate providers at every important layer.

Hard rules:

1. Model namespaces stay separate.
2. Auth lifecycle stays separate.
3. Usage is stored and reported with provider identity.
4. Runtime executors remain one-to-one with providers.
5. Provider base URLs are configured independently.

The design intentionally avoids merging the two providers even if they expose similarly named models.

## Canonical Model Naming

External model naming uses provider-prefixed canonical IDs.

Examples:

- `auggie-gpt-5`
- `auggie-gpt-5-1`
- `auggie-gpt-5-2`
- `auggie-gpt-5-4`
- `auggie-claude-haiku-4-5`
- `auggie-claude-opus-4-5`
- `auggie-claude-opus-4-6`
- `auggie-claude-sonnet-4`
- `auggie-claude-sonnet-4-5`
- `auggie-claude-sonnet-4-6`
- `antigravity-gemini-3.1-pro-high`

Compatibility aliases may still be accepted during migration, but all docs, samples, and UI surfaces should prefer the canonical prefixed names.

Examples:

- `gpt-5-4` -> `auggie-gpt-5-4`
- `claude-sonnet-4-6` -> `auggie-claude-sonnet-4-6`
- `gemini-3.1-pro-high` -> `antigravity-gemini-3.1-pro-high`

## What to Keep in the First Migration

Keep:

- API server code needed for inference and management
- config loading
- logging
- registry
- runtime executors for Auggie and Antigravity
- usage accounting
- storage helpers
- utility packages
- thinking and translator code still required by the live request path
- watcher/session synthesis that is required by Auggie auth loading
- `sdk/` packages that are still part of the runtime compile chain

## What to Remove in the First Migration

Do not keep or do not copy:

- `desktop`
- `web`
- `examples`
- bundled `.app` artifacts
- `internal/tui`
- `internal/browser`
- `internal/console`
- providers other than `auggie` and `antigravity`
- executors and translators that are not required by Auggie or Antigravity

## Migration Sequence

1. Initialize a new git repository in `CLIProxyAPI-wjq`.
2. Copy the verified backend worktree into `apps/server-go`.
3. Copy the Swift menubar into `apps/menubar-swift`.
4. Remove non-target applications and providers.
5. Introduce the new context entry points.
6. Normalize model naming to provider-prefixed canonical IDs.
7. Keep compatibility aliases where required for existing clients.
8. Verify build, runtime behavior, usage reporting, and menubar connectivity.

## Verification Targets

The first successful migration must verify:

- Go backend builds
- Swift menubar builds
- `/v1/models` responds
- an Auggie model returns a real response
- an Antigravity model returns a real response
- `/v0/management/usage` reports activity
- menubar can read management usage
- menubar can start and stop the local backend

## Out of Scope for the First Pass

These are intentionally deferred:

- Rust backend migration
- microservice decomposition
- rewriting all legacy packages into pure DDD tactical patterns
- merging provider semantics
- adding unrelated providers back into the new repository

## Decision Summary

This design optimizes for speed, correctness, and future provider separation:

- fastest migration path
- only two retained applications
- only two retained providers
- provider-prefixed canonical model naming
- modular monolith with pragmatic DDD boundaries
- clear separation between menubar control-plane concerns and backend inference concerns
