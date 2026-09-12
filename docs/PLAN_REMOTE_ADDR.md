---
PLAN: "fix(httpd): expose the request RemoteAddr through router.Context"
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.
>
> **Phase R2 (GATE)** of
> [`LAN_RUT_AUTH_MASTER_PLAN.md`](https://github.com/tinywasm/app/blob/main/docs/LAN_RUT_AUTH_MASTER_PLAN.md).
> **Depends on `webtyp.com/router` publishing
> `router.ContextKeyRemoteAddr`** (phase R1) — as the first line of work,
> `go get webtyp.com/router@latest` and verify the constant exists. Never add
> a `replace`, never invent a version.

# Plan — `webtyp.com/server/httpd`: produce the client address

## 0. Context

`auth.ClientIP(ctx, trustProxy)` reads `ctx.Value("RemoteAddr")` when not
behind a proxy. `httpd`'s `httpContext.Value` only consults its `values` map
and the Go request context — **neither ever carries the remote address**, so
over real HTTP the call returns `""`. Any IP-bound authentication
(`webtyp.com/auth/trusted_ip`, LAN deployments) is therefore broken against
the only production transport, and it fails *silently* (empty IP simply never
matches an allowlist). The producer side of the seam lives here.

## Stage 1 — `Value` answers the remote-address key

**File:** `httpd/adapter.go`, `httpContext.Value`.

```go
func (c *httpContext) Value(key string) string {
	if v, ok := c.values[key]; ok {
		return v
	}
	if key == router.ContextKeyRemoteAddr {
		return c.r.RemoteAddr
	}
	if v, ok := c.r.Context().Value(key).(string); ok {
		return v
	}
	return ""
}
```

Deliberately inside `Value` (not at the two `httpContext{…}` construction
sites in `adapter.go` and `static.go`): one place, both paths covered, no
per-request map allocation. The value is `net/http`'s `host:port` form,
unparsed — `auth.ClientIP` already splits it. An explicit `SetValue` under
the same key still wins (first branch), preserving test-double behavior.

## Stage 2 — consumer-shaped test

**File:** `httpd/adapter_test.go` (extend).

Through a real `httptest` server and a handler receiving `router.Context`:

1. `ctx.Value(router.ContextKeyRemoteAddr)` equals `r.RemoteAddr` of the
   arriving request (non-empty, ends with the client port suffix `":<port>"`
   — assert prefix `127.0.0.1:`).
2. `SetValue(router.ContextKeyRemoteAddr, "10.0.0.9:1")` before reading →
   the explicit value wins.
3. The same handler reached via the static-file path (`static.go`) also
   answers the key (covers the second construction site).

## Stage 3 — docs

`docs/ARCHITECTURE.md`: one sentence in the context-adapter section — httpd
produces `router.ContextKeyRemoteAddr` from `Request.RemoteAddr`. VERIFY
against the implementation.

## Acceptance criteria

1. `go build ./...`, `go vet ./...`, `gotest ./...` green.
2. `grep -rn '"RemoteAddr"' httpd/` → empty (only the router constant).
3. The static-path test proves both construction sites answer the key.

| Stage | File | Action |
|---|---|---|
| 0 | `go.mod` | bump `webtyp.com/router` to the R1 tag |
| 1 | `httpd/adapter.go` | `Value` produces the remote address |
| 2 | `httpd/adapter_test.go` | three consumer-shaped cases |
| 3 | `docs/ARCHITECTURE.md` | verify docs |
