---
PLAN: "feat: httpd decodes the arguments of a GET from its query; one query reader (router.QueryReader) replaces the private form reader"
EXECUTOR: jules
REVIEWER: none
STATUS: running
SESSION: 1175233284267127082
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit after every stage, push and open the pull request. Never ask whether to
> proceed or finalize — nobody answers, and a session that waits ends without your work.
> Never run `git reset`, `git restore`, `git checkout -- <file>` or `git stash` on your own work.

## Goal

Operations become REST (`GET /api/<module>/<entity>?search=…` reads). A `GET` has no body, so `httpContext.Decode` must read its query; the rule "a GET reads its query, every other method its body" lives once, in `router.DecodeArgs`, and `httpd` and the edge both call it. The package
`server/httpd` already has a private reader over `net/url` for form bodies (`formReader`, `formdecode.go`); `router` now has a WASM-safe reader
(`router.QueryReader`, router plan) that does the same and also reads a nested object (`filter.name=x`). One reader replaces the other.
Nothing else in `httpd` changes: it already registers any method through `Handle(method, path, h)` (the `PATCH` of an update) and gates it like every other route.

## Ecosystem search

Searched `QueryReader`, `formReader`, `url.ParseQuery` over `webtyp/*`: `server/httpd/formdecode.go` (private, flat, `net/url`) and `router/query.go` (`QueryParam`, and the new codec). **Reuse** `router.QueryReader`; delete `formReader`.
`httpd` does not log request URLs or queries (`grep -rn "RawQuery\|RequestURI" server` finds only `Path()` at `adapter.go:43`), so a patient's search text is not written to any log here.

## Design gate

1. **Prior art** (unverified in this session): `net/http` handlers read `r.URL.Query()` for a `GET` and the body for the other methods.
2. **Novice-name test**: no new name.
3. **Ledger**: concepts 0; one reader instead of two (−1 type, −1 `net/url` dependency in this path); ways to read form data 2 → 1.
4. **Where**: the decoding of a request into a typed value belongs to the transport (`httpd`), the reader to `router`.
5. **Deletes**: `formReader` and its methods (`formdecode.go:14-84`).

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
```

Dependencies (all published, no `replace`): `go get webtyp.com/router@v0.4.20 webtyp.com/model@v0.2.18 && go mod tidy`.

## APIs used

| symbol | exact signature | file |
|---|---|---|
| `router.QueryOf` | `func QueryOf(path string) string` (query of a path, without `?` or fragment) | `router/query_codec.go` (router plan) |
| `router.QueryReader` | `func QueryReader(query string) model.FieldReader` | `router/query_codec.go` (router plan) |
| `router.DecodeArgs` | `func DecodeArgs(method, query string, body []byte, into model.Decodable, fromBody func(body []byte, into model.Decodable) error) error` (a GET decodes its query, any other method calls `fromBody`) | `router/query_codec.go` (router plan) |
| `httpContext` | fields `w http.ResponseWriter`, `r *http.Request`; methods `Method() string`, `Path() string` (path **with** `?query`), `Body() []byte` | `adapter.go:37-65` |
| `router.Context.Decode` | `Decode(into model.Decodable) error` | `router/router.go:61` |

If a signature differs from this table, stop and name the symbol in the PR; never adapt it by guessing.

## Stage 1 — tests first (red)

In `httpd/formdecode_test.go` add a helper next to `newTestContext` and the tests (package `httpd`; reuse `testDecodable`):

```go
func newTestContextAt(method, target string, body []byte) *httpContext {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	return &httpContext{w: httptest.NewRecorder(), r: req}
}
```

| Test | setup → call → expected |
|---|---|
| `TestDecode_GetReadsTheQuery` | `newTestContextAt(GET, "/api/m/e?code=abc&qty=3&active=true", nil)` → `Decode(&t)` → `Code=="abc"`, `Qty==3`, `Active==true` |
| `TestDecode_GetIgnoresTheBody` | `GET /x?code=q` with body `{"code":"body"}` → `Code=="q"` (the query wins; a body on a GET is never trusted) |
| `TestDecode_GetWithoutQueryLeavesZeroValues` | `GET /x` → `Decode` returns nil and the target is untouched (`Qty` set beforehand stays) |
| `TestDecode_GetEscapedValues` | `GET /x?code=a%20b%26c` → `Code=="a b&c"` |
| `TestDecode_PatchAndDeleteReadTheBody` | `PATCH` and `DELETE` with JSON body `{"code":"abc","qty":3,"active":true}` and `Content-Type: application/json` → decoded; a query on those requests is ignored |
| `TestDecode_GetNestedObjectUsesPrefix` | a target whose `DecodeFields` calls `r.Object("filter", &inner)`; `GET /x?filter.name=x` → `inner.Name=="x"` |
| `TestRoute_PatchIsGated` | register `r.Handle("PATCH", "/api/m/e", h).Requires("e", model.Update)` on an `httpRouter` with an `Authorize` that denies → a `PATCH` answers 403 and `h` is not called; with an authorizer that allows → `h` runs (proves an update is gated like every other route) |
| `TestFormDecode_MalformedEscapeIsOpaque` | a form body `code=%zz` decodes without error and the handler receives the value exactly as written (`%zz`), never a different value and never a panic |

The existing `TestFormDecode_*` tests (form bodies, charset, JSON, missing keys, `active=on`) must stay green unchanged: they prove `router.QueryReader` replaces `formReader` with the same behaviour.
Run `gotest`: the new tests fail. Commit: `test: GET decodes its query (red)`.

## Stage 2 — the code

`adapter.go`, replace `Decode`:

```go
// Decode reads the arguments of the request into a typed destination. A GET carries them in its query (and never in a body);
// every other method carries them in its body.
func (c *httpContext) Decode(into model.Decodable) error {
	return router.DecodeArgs(c.r.Method, router.QueryOf(c.Path()), c.Body(), into, func(_ []byte, d model.Decodable) error {
		return decodeBody(c, d) // JSON or form, by Content-Type
	})
}
```

`formdecode.go`: delete the whole `formReader` type and its methods, the `net/url` and `strconv` imports, and change the form branch of `decodeBody` to:

```go
	if strings.EqualFold(ct, contentTypeFormUrlEncoded) {
		into.DecodeFields(router.QueryReader(string(c.Body())))
		return nil
	}
```

(`router` is already imported by `adapter.go`; add the import to `formdecode.go`. Remove `strings.TrimSpace` imports only if they become unused.) `gotest` passes. Commit: `feat: GET decodes its query; router.QueryReader replaces formReader`.

## Stage 3 — documentation

- `docs/ARCHITECTURE.md` (four sections): in the transport section add the rule "a GET is decoded from its query, every other method from its body; a form body goes through the same reader" and that `httpd` never logs a query.
- `README.md` (≤ 150 lines, four ordered sections): one sentence under *Documentation Map* or the guide stating how `Decode` picks its source; keep the compilable example.
- Do not link `docs/PLAN.md` from permanent docs.

Commit: `docs: Decode source per method`.

## Delivery check

1. `gotest` (builds, vets, runs; a PR that does not compile, or whose tests could not run, is a failed delivery).
2. `grep -rn "formReader\|url.ParseQuery" --include=*.go server/httpd` → empty.
3. `git diff --stat main` lists only `httpd/adapter.go`, `httpd/formdecode.go`, `httpd/formdecode_test.go`, `go.mod`, `go.sum`, `README.md`, `docs/ARCHITECTURE.md`.

| Stage | Files | Commit |
|---|---|---|
| 1 | `httpd/formdecode_test.go` | `test: … (red)` |
| 2 | `httpd/adapter.go`, `httpd/formdecode.go` | `feat: …` |
| 3 | `README.md`, `docs/ARCHITECTURE.md` | `docs: …` |
