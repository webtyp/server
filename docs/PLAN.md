---
PLAN: "feat(httpd): two doors — the server listens on Port (local) and InternetPort (internet), marks each request, and demands a passkey session on the internet door"
EXECUTOR: jules
REVIEWER: none
---

> Read AGENTS.md at the repo root before any change.

# PLAN — the local door and the internet door in `httpd`

## Why

`veltylabs/mjosefa-cms` serves one app on two ports: the clinic's network (login by RUT on a
registered device) and the internet through Cloudflare Tunnel (only a passkey). Same code, same
modules; only what is allowed changes (decisions D8 and D25 of
`veltylabs/mjosefa-cms/docs/MVP_DEPLOYMENT_ROADMAP.md`). Through the tunnel every request arrives
from `127.0.0.1`, so only the **port** tells the doors apart. `webtyp.com/router` v0.4.12 added the
key the transport writes (`router.ContextKeyNetwork`, values `router.NetworkLocal` /
`router.NetworkInternet`) and the reader `router.FromInternet(ctx)` (unmarked = internet, strict).
This plan makes `httpd` write it and enforce the one rule that belongs to the transport: on the
internet door, every route that needs an identity needs a **passkey** session
(`model.AssuranceMultiFactor`). The next plan (`webtyp/auth`) closes the RUT login, the first-admin
setup and passkey enrolment on the internet door.

## Design gate (api-design)

1. **Prior art.** Spring Boot `management.server.port`: a second port with its own rules from the
   same application. Microsoft Entra Conditional Access «trusted locations» and Okta «network
   zones»: outside the trusted location, MFA is required. Here the location is the listener.
2. **Names.** `Config.InternetPort` — «the port of the internet door»; empty means there is none.
   `InternetHandler()` mirrors `Handler()`.
3. **Ledger.** Concepts +1 (the internet port), lines in the app +1 (`InternetPort: env…`), ways +0.
4. **Where.** `httpd`: only the transport knows on which port a request arrived.
5. **Deletes.** Nothing.

## APIs used

| Symbol | Exact | Tag |
|---|---|---|
| `router.ContextKeyNetwork` | `const ContextKeyNetwork = "Network"` | router v0.4.12 |
| `router.NetworkLocal`, `router.NetworkInternet` | `"local"`, `"internet"` | router v0.4.12 |
| `router.FromInternet` | `func FromInternet(ctx Context) bool` | router v0.4.12 |

## Tools

```
go get webtyp.com/router@v0.4.12
```

## Stage 1 — `httpd/httpd.go`

- `Config` gets one field after `Port`:
  `InternetPort string // the internet door (e.g. behind Cloudflare Tunnel); "" = no internet door`.
- Build the inner handler **once**: move the current body of `Handler()` into
  `func (s *Server) base() (http.Handler, error)` that caches its result in two new fields
  (`baseHandler http.Handler`, `baseErr error`) and returns them on later calls. (Building twice
  would register `PublicDir("/")` twice on the mux, which panics.)
- `Handler()` returns `markNetwork(base, router.NetworkLocal)`.
- New `func (s *Server) InternetHandler() (http.Handler, error)` returns
  `markNetwork(base, router.NetworkInternet)`. Doc: «the handler for the internet door; the same
  routes as Handler, marked as coming from the internet».
- New, unexported, in `httpd/network.go`:

  ```go
  // markNetwork records on every request which door it came through. The adapter's
  // Context.Value reads it back from the request context (adapter.go, Value).
  func markNetwork(h http.Handler, network string) http.Handler {
  	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), router.ContextKeyNetwork, network)))
  	})
  }
  ```

  `httpContext.Value` already falls back to `c.r.Context().Value(key).(string)`
  (`adapter.go:119`), so nothing else in the adapter changes. If `go vet` rejects a string key for
  `context.WithValue`, stop and say so in the PR; do not invent a key type.
- `ListenAndServe`: build `local, err := s.Handler()`. If `s.config.InternetPort == ""`, behave
  exactly as today with `local`. Otherwise also build `internet, err := s.InternetHandler()`, create
  a second `http.Server` with the same timeouts and `Addr: ":" + s.config.InternetPort`, log
  `"Starting internet door on port", s.config.InternetPort`, run both through `s.listenAndServe`
  in two goroutines and return the first error:

  ```go
	errs := make(chan error, 2)
	go func() { errs <- s.listenAndServe(srv) }()
	go func() { errs <- s.listenAndServe(internetSrv) }()
	return <-errs
  ```

  Both doors use the same TLS mode (the tunnel verifies the local CA, D18 of the roadmap).

## Stage 2 — `httpd/adapter.go`: the internet door demands a passkey session

In the gate (`adapter.go`, the `switch route.info.Access` around lines 460-500), in **both** the
`model.AccessAuthenticated` and the default (`AccessGuarded`) branches, replace

```go
if route.info.MFA && ctx.Assurance() < model.AssuranceMultiFactor {
```

with

```go
if (route.info.MFA || router.FromInternet(ctx)) && ctx.Assurance() < model.AssuranceMultiFactor {
```

`AccessPublic` is unchanged: anonymous visitors (a patient booking online) still reach public
routes on the internet door. Update the comment above the gate: «On the internet door every route
that needs an identity needs a passkey session».

**Existing tests.** Tests that serve a mux built with `NewRouter(mux)` directly (not through
`Handler()`) now count as internet. Where such a test expects a single-factor session to pass a
guarded or authenticated route, wrap its handler with `markNetwork(mux, router.NetworkLocal)`
(the test files are in package `httpd`). Do not change any expected status otherwise.

## Stage 3 — `httpd/network_test.go` (new, package `httpd`)

One `Server` with an `Authn` middleware that sets `UserID("u1")` and an `Assurance` read from a
request header in the test (`X-Test-Assurance`: `1` single, `2` multi), `Authorize` allowing all,
and three routes: `GET /pub` `.Public()`, `GET /me` `.Authenticated()`, `GET /data`
`.Requires(model.Resource("data"), model.Read)`. Serve `Handler()` and `InternetHandler()` with
`httptest.NewServer`.

| Test | Door | Request | Expected |
|---|---|---|---|
| `TestLocalDoor_SingleFactorPasses` | local | `/data`, single | 200 |
| `TestInternetDoor_SingleFactorRefused` | internet | `/data`, single | 401 and `WWW-Authenticate` = `router.ChallengeMFARequired` |
| `TestInternetDoor_AuthenticatedSingleFactorRefused` | internet | `/me`, single | 401 |
| `TestInternetDoor_PasskeyPasses` | internet | `/data`, multi | 200 |
| `TestInternetDoor_PublicPasses` | internet | `/pub`, no identity | 200 |
| `TestDoors_HandlerSeesNetwork` | both | a public route that writes `router.FromInternet(ctx)` | local `false`, internet `true` |
| `TestInternetHandler_SameRoutes` | internet | `GET /missing` | 404, as on the local door |

## Stage 4 — docs

- `httpd` section of `README.md`: `InternetPort`, what each door marks, the passkey rule on the
  internet door, public routes unchanged, and `InternetHandler()` for tests.
- `docs/ARCHITECTURE.md`: why the door is the listener (the tunnel delivers every request as
  `127.0.0.1`; `X-Forwarded-For` is forgeable) and why the passkey rule lives in the transport's gate.

## Delivery check

```
gotest
```

- A PR that does not compile, or whose tests could not run, is a failed delivery.
- Never pause to ask permission: when the check passes, commit, push and open the PR. Stuck 30
  minutes: open the PR listing what fails.
- `git diff --stat main` lists only: `httpd/httpd.go`, `httpd/network.go`, `httpd/adapter.go`,
  `httpd/network_test.go`, existing `httpd/*_test.go` adjusted per Stage 2, `README.md`,
  `docs/ARCHITECTURE.md`, `go.mod`, `go.sum`, this plan.

## Stages

| # | Files | Done when |
|---|---|---|
| 1 | `httpd/httpd.go`, `httpd/network.go`, `go.mod`, `go.sum` | compiles |
| 2 | `httpd/adapter.go`, existing tests | suite green |
| 3 | `httpd/network_test.go` | 7 tests pass |
| 4 | `README.md`, `docs/ARCHITECTURE.md` | sections present |
