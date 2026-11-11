---
PLAN: "feat!: httpd Context.Path() is the path only and RawQuery() is the query (router v0.5.0)"
EXECUTOR: jules
REVIEWER: none
STATUS: running
SESSION: 661305067297468528
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit, push and open the pull request. Never ask whether to
> proceed or finalize — nobody answers, and a session that waits ends without your work. Commit after every stage.
> Never run `git reset`, `git restore`, `git checkout -- <file>` or `git stash` on your own work.
> **Publishing:** you publish only by OPENING THE PULL REQUEST (a `git push` from your sandbox does not reach GitHub). Open it after Stage 2 with whatever compiles and keep committing to it.
> **Never ask a yes/no question about progress**; ask only when a signature in this plan is wrong, and name the symbol.

# Plan: `httpd` follows the router Context contract

## Why (read once)

`webtyp.com/router` v0.5.0 changed the `router.Context` contract: `Path()` is the path only and the new `RawQuery() string` is the query (`"a=1&b=two"`, `""` when none), read with `router.QueryReader(ctx.RawQuery())`. This repo implements `Context`, so it stops compiling until it does the same. The contract is held by `router/conformance` (`Run` for transports served by a `Router`); a transport that disagrees fails it.

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
go get webtyp.com/router@v0.5.0
go mod tidy
```
No `replace` in `go.mod`. If a module cannot be fetched, stop and say so in the PR; never write a hash by hand.

## APIs used

| symbol | exact signature | tag | file |
|---|---|---|---|
| `router.Context.RawQuery` | `RawQuery() string` | router v0.5.0 | `router.go` |
| `router.PathOf` | `func PathOf(target string) string` | router v0.5.0 | `query_codec.go` |
| `router.QueryOf` | `func QueryOf(path string) string` | router v0.5.0 | `query_codec.go` |
| `router.DecodeArgs` | `func DecodeArgs(method, query string, body []byte, into model.Decodable, fromBody func(body []byte, into model.Decodable) error) error` | router v0.5.0 | `query_codec.go` |
If a signature in the published tag differs from this table, stop and name the symbol in the PR; never adapt it by guessing.

## Stage 1 — `httpd/adapter.go`

Replace (lines 41–47)

```go
// Path returns the request path with its query string, if any: router.QueryParam reads it from here.
func (c *httpContext) Path() string {
	if c.r.URL.RawQuery == "" {
		return c.r.URL.Path
	}
	return c.r.URL.Path + "?" + c.r.URL.RawQuery
}
```

with

```go
// Path returns the path of the request only, never its query (the router contract).
func (c *httpContext) Path() string { return c.r.URL.Path }

// RawQuery returns the query of the request without the "?", "" when there is none.
func (c *httpContext) RawQuery() string { return c.r.URL.RawQuery }
```

and in `Decode` (line 95) replace `router.QueryOf(c.Path())` by `c.RawQuery()`:

```go
return router.DecodeArgs(c.r.Method, c.RawQuery(), c.Body(), into, func(_ []byte, d model.Decodable) error {
```

(keep the rest of that call exactly as it is). Check: `grep -rn "QueryOf(c.Path())\|Path() + \"?\"" --include=*.go . | grep -v _test` → empty.

## Stage 2 — tests and docs

| Test | setup → call → expected |
|---|---|
| `TestHTTPContext_PathAndRawQuery` (`httpd/adapter_test.go`) | a request `POST /api/q?a=1&b=two` through the adapter: `ctx.Path()` is `/api/q`, `ctx.RawQuery()` is `a=1&b=two`; without query `RawQuery()` is `""` |
| `TestHTTPDConformance` (exists) | passes with the five new clauses of router v0.5.0 (no edit) |

Add the row of each new test to `docs/TESTS.md` (`| [Name](../httpd/file_test.go) | rule |`, same style as the file). `docs/ARCHITECTURE.md` and the README of `httpd`: if they say `Path()` carries the query, change it to the contract above; otherwise add one sentence.

## Delivery check

`gotest` passes (it builds, vets, races and runs every destination); every other `Path()`/`QueryOf` use of the repo agrees with the contract; the pull request is open. A tree that does not compile or whose tests did not run is a failed delivery.
