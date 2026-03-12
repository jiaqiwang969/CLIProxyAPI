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

## Local Auggie `/v1/responses` test

This is the shortest reliable path for testing:

1. Prepare a local config with a proxy API key.
2. Complete Auggie upstream authentication.
3. Start the backend on `127.0.0.1:8317`.
4. Verify `GET /v1/models`.
5. Verify `POST /v1/responses`.

### 1. Prepare `config.yaml`

Start from the example:

```bash
cp config.example.yaml config.yaml
```

Use a minimal local config like this:

```yaml
host: "127.0.0.1"
port: 8317
auth-dir: "~/.cli-proxy-api"
api-keys:
  - "sk-local-auggie-test"
remote-management:
  allow-remote: false
  secret-key: ""
debug: false
```

Key points:

- `host: "127.0.0.1"` keeps the test surface local-only.
- `port: 8317` matches the `curl` examples below.
- `auth-dir` is where proxy-managed auth files are stored.
- `api-keys` is the local client credential list for your `curl` requests.

Important:

- The `Authorization: Bearer ...` header used against `http://127.0.0.1:8317` must use a value from `api-keys`.
- Do not paste the Auggie upstream access token into `curl`.

### 2. Complete Auggie upstream authentication

There are two supported paths.

#### Option A: use the official Auggie CLI session

If you already use Auggie locally, this is the fastest path.

```bash
npm install -g @augmentcode/auggie
auggie login
ls -l ~/.augment/session.json
```

What this does:

- `auggie login` completes the Auggie OAuth flow in the browser.
- On success, Auggie writes `~/.augment/session.json`.
- Current proxy code can use that session file to reach the Auggie upstream.

This is the important distinction:

- `~/.augment/session.json` is the upstream Auggie session.
- `config.yaml -> api-keys` is the local proxy API auth.
- They are different credentials serving different layers.

#### Option B: use the proxy's own login flow

This is the most deterministic path when you want the proxy to materialize a local auth record under `auth-dir`.

```bash
go run ./cmd/server -config ./config.yaml -auggie-login
```

Expected behavior:

- The command opens a browser for Auggie OAuth.
- On success, it saves a file like `~/.cli-proxy-api/auggie-<tenant>.json`.
- The CLI prints `Authentication saved to ...` and `Auggie authentication successful!`

Useful variants:

```bash
go run ./cmd/server -config ./config.yaml -auggie-login -no-browser
go run ./cmd/server -config ./config.yaml -auggie-login -oauth-callback-port 43199
```

Notes:

- If localhost callback is unavailable, the login flow can fall back to a manual JSON paste path.
- Auggie tenant URLs must resolve to `*.augmentcode.com`.

### 3. Start the backend

```bash
go run ./cmd/server -config ./config.yaml
```

Expected startup log includes:

```text
API server started successfully on: 127.0.0.1:8317
```

### 4. Verify `GET /v1/models`

Use the local proxy API key from `config.yaml`:

```bash
curl -sS http://127.0.0.1:8317/v1/models \
  -H "Authorization: Bearer sk-local-auggie-test" | jq '.'
```

You should see model IDs such as:

- `gpt-5.4`
- `gpt-5`
- `claude-sonnet-4-6`

Recommended quick check:

```bash
curl -sS http://127.0.0.1:8317/v1/models \
  -H "Authorization: Bearer sk-local-auggie-test" | jq '.data[].id'
```

Important:

- `GET /v1/models` being available is necessary.
- It is not the final proof that Auggie execution is usable.
- The final proof is a real `POST /v1/responses`.

### 5. Verify `POST /v1/responses`

Minimal request:

```bash
curl -sS http://127.0.0.1:8317/v1/responses \
  -H "Authorization: Bearer sk-local-auggie-test" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "input": "hi"
  }' | jq '.'
```

A slightly more explicit smoke test:

```bash
curl -sS http://127.0.0.1:8317/v1/responses \
  -H "Authorization: Bearer sk-local-auggie-test" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "input": "请只回答 ok，不要解释。"
  }' | jq '{id,status,model,output_text,error}'
```

If the upstream account is healthy, you should get a normal OpenAI-style response object with fields such as:

- `id`
- `status`
- `model`
- `output`
- `output_text`

### 6. Common failure modes

#### `Missing API key` or `Invalid API key`

Meaning:

- Your `curl` request did not use a valid local proxy API key.

Fix:

- Confirm the bearer token matches one entry under `config.yaml -> api-keys`.

#### `auth_unavailable: no auth available`

Meaning:

- Your local API key is accepted by the proxy.
- But the proxy has no usable Auggie upstream credential at execution time.

Fix:

- Run `auggie login` and confirm `~/.augment/session.json` exists.
- Or run `go run ./cmd/server -config ./config.yaml -auggie-login` once to create a proxy auth file.
- Then restart the backend you are actually using.

Practical note:

- If `GET /v1/models` works but `POST /v1/responses` returns `auth_unavailable`, you are often hitting an older running binary or a process that was started before authentication was completed.

#### `insufficient_quota`

Meaning:

- The request did reach the Auggie upstream.
- But the upstream account is suspended, out of quota, or requires an active subscription.

This is not the same problem as missing auth.

#### `403` / permission-style upstream failures after login

Meaning:

- The session exists, but the upstream account state does not currently allow generation.

Fix:

- Check the Auggie account/subscription state in the upstream product.

### 7. Practical test checklist

Use this exact order when debugging:

```bash
ls -l ~/.augment/session.json
curl -sS http://127.0.0.1:8317/v1/models -H "Authorization: Bearer sk-local-auggie-test" | jq '.data[].id'
curl -sS http://127.0.0.1:8317/v1/responses -H "Authorization: Bearer sk-local-auggie-test" -H "Content-Type: application/json" -d '{"model":"gpt-5.4","input":"hi"}' | jq '.'
```

Interpretation:

- If step 1 fails: Auggie upstream login has not been completed on this machine.
- If step 2 fails: your local proxy auth or server startup is wrong.
- If step 2 passes but step 3 returns `auth_unavailable`: the runtime still does not have a usable Auggie credential loaded.
- If step 3 returns `insufficient_quota`: auth is loaded and the request reached Auggie, but the upstream account itself is blocked by quota or subscription state.
