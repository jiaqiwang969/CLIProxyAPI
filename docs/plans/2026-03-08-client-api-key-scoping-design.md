# Client API Key Scoping Design

Date: 2026-03-08

## Goal

Add first-class client API key scoping so the proxy and menubar can distinguish:

- provider: `antigravity` vs `auggie`
- account: a concrete upstream auth credential
- models visible through that account

This pass does not introduce account pools yet.

## Problem Summary

The current proxy authenticates downstream client API keys from a flat top-level `api-keys` list. Once a key is accepted, it can reach any model exposed by the server.

The menubar has the same limitation:

- it only reads flat local keys
- it does not show provider/account/model hierarchy
- it cannot bind a key to a specific upstream account

The backend already knows provider/account/model relationships through the auth manager and registry, but that information is not connected to downstream API key auth.

## Chosen Strategy

Introduce a new structured top-level config block:

- `client-api-keys`

Each entry carries:

- raw client key
- note
- enabled flag
- scope

The scope for this pass is:

- `provider`
- `auth-id`
- optional `models` field reserved for a later model allowlist pass

Legacy `api-keys` remains readable for backward compatibility, but new GUI management writes only `client-api-keys`.

## Scope Rules

### 1. Legacy keys remain global

Existing top-level `api-keys` continue to work as unscoped keys.

They are treated as:

- enabled
- no provider restriction
- no account restriction

### 2. Structured keys can pin provider and account

A structured key may specify:

- provider only
- provider + auth-id

For this pass, the GUI will create keys using `provider + auth-id`.

### 3. Scoped execution must be enforced server-side

When a client key is bound to an auth account:

- model listings must only show models available through that auth
- request execution must be pinned to that auth
- requests for models not supported by that auth must be rejected

This is not only a GUI grouping feature. The proxy must enforce it at runtime.

## Config Shape

New config shape:

```yaml
client-api-keys:
  - key: sk-example
    note: demo
    enabled: true
    scope:
      provider: antigravity
      auth-id: antigravity-user@example.com.json
      models: []
```

Notes:

- `models` is reserved for future allowlists; it is optional and not exposed in the GUI yet
- `auth-id` uses the stable auth record ID, not `auth_index`

## Backend Architecture

### 1. Config

Add structured client key types to the shared SDK config:

- `ClientAPIKey`
- `ClientAPIKeyScope`

Add helper normalization so effective client keys are the union of:

- legacy flat `api-keys`
- structured `client-api-keys`

### 2. Request authentication

Update the config access provider so authentication can return scope metadata with the matched client key:

- provider
- auth-id
- models
- note

### 3. Request enforcement

Use the existing pinned-auth execution path rather than redesigning auth selection.

For scoped keys:

- if `auth-id` is present, pin execution to that auth
- if `provider` is present, restrict provider resolution to that provider
- if the pinned auth does not support the requested model, reject the request

This keeps the current auth manager and executor flow intact.

### 4. Model listing filtering

Model listing endpoints must respect scope:

- OpenAI `/v1/models`
- unified model surfaces that rely on the same registry view
- Gemini native `/v1beta/models`

If a key is scoped to `auth-id`, list only models registered for that auth.

## Management API

Add a new management endpoint for structured keys:

- `GET /v0/management/client-api-keys`
- `PUT /v0/management/client-api-keys`

The response should include effective key entries ready for GUI display.

The `PUT` operation replaces the structured list and migrates GUI-managed keys away from legacy flat `api-keys`.

Extend auth file listing so the GUI can fetch models in a single call:

- `GET /v0/management/auth-files?include_models=true`

Each auth entry should include:

- provider
- label
- email
- account metadata
- models

## Menubar Architecture

The menubar stops managing scoped keys by directly editing YAML.

Instead it uses the management API for:

- loading scoped client keys
- loading auth inventory with models
- saving updated key bindings

The GUI shows:

- provider groups
- accounts under each provider
- models under each account
- keys with their current bound provider/account

## Auggie Constraint

This pass does not solve multi-profile Auggie session isolation.

The GUI and config may still bind a key to a specific current Auggie auth record, but no account-pool or per-profile session source is introduced here.

That is intentionally deferred.

## Error Handling

Required behavior:

- unknown scoped auth-id returns a clear forbidden-style error
- model not available for scoped auth returns a clear forbidden-style error
- disabled scoped key is rejected like an invalid credential

The proxy must not silently fall through to another provider/account when a scoped key is used.

## Testing Strategy

### Backend

Add focused tests for:

- structured scoped key authentication metadata
- `/v1/models` filtering by scoped auth
- request execution metadata pinning / rejection path
- management auth file listing with embedded models

### Menubar

Add focused tests for:

- decoding management auth inventory
- decoding structured client keys
- key scope display / state transitions in the view model

## Non-Goals

This pass does not add:

- account pools
- weighted failover
- per-profile Auggie session directories
- model allowlist editing in the GUI

## Success Criteria

The change is successful when all of the following are true:

1. the menubar can show `antigravity` and `auggie` accounts separately
2. the menubar can create/update keys bound to a specific auth account
3. a scoped key only lists models from its bound account
4. a scoped key cannot execute against another account's models
5. legacy flat `api-keys` continue to work
