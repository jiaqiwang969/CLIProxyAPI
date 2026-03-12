# Auggie Responses Store Stateful Alignment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Align the Auggie-backed `/v1/responses` continuation behavior with the official OpenAI Responses stateful contract so `previous_response_id` only works when the prior response was stored/stateful.

**Architecture:** Reuse the existing `store` request flag as the single proxy-side statefulness switch. Keep the public API unchanged, but gate Auggie continuation-state persistence behind `store != false`, and return a clear `400` when a caller tries to continue from a non-stored prior response.

**Tech Stack:** Go, Gin, internal Auggie executor tests, OpenAI Responses compatibility layer.

---

### Task 1: Add the failing continuation test

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`

**Step 1: Write the failing test**

Add a test that:
- sends a first-turn Auggie `/v1/responses` request with `"store": false`
- verifies the first turn still returns a public `response.id` and `function_call`
- sends a second-turn request with `previous_response_id + function_call_output`
- expects a `400` instead of a successful continuation

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime/executor -run TestAuggieResponses_PreviousResponseContinuationRequiresStoredPriorResponse -count=1`

Expected: FAIL because the current executor still resumes from `store:false` turns.

### Task 2: Gate Auggie continuation state persistence

**Files:**
- Modify: `apps/server-go/internal/runtime/executor/auggie_executor.go`

**Step 1: Write minimal implementation**

Add a helper that decides whether a Responses request should persist Auggie continuation state:
- default `true`
- explicit `"store": false` disables state persistence

Use that helper in both places that currently persist Auggie response-ID continuation state:
- stream aggregation completion path
- final translated Responses payload path

**Step 2: Return clearer continuation error**

When `previous_response_id` cannot be resolved, keep returning `400` but add guidance that Auggie continuation requires a stored/stateful prior response, meaning omit `store` or set `store=true` on the previous turn.

### Task 3: Verify the fix

**Files:**
- Test: `apps/server-go/internal/runtime/executor/auggie_executor_stream_test.go`

**Step 1: Run focused tests**

Run:
- `go test ./internal/runtime/executor -run TestAuggieResponses_PreviousResponseContinuationRequiresStoredPriorResponse -count=1`
- `go test ./internal/runtime/executor -run 'TestAuggieResponses_(PublicResponseIDSupportsPreviousResponseContinuation|StreamPublicResponseIDSupportsPreviousResponseContinuation)' -count=1`

Expected:
- new `store:false` continuation test passes with `400`
- existing `store:true/default` continuation tests still pass

**Step 2: Run a broader executor regression slice**

Run: `go test ./internal/runtime/executor -count=1`

Expected: PASS
