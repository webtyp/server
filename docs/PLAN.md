---
PLAN: "feat(httpd)!: HTTPS by default — the zero TLSConfig serves the local CA, plain HTTP only with PlainHTTP"
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — `httpd` serves HTTPS by default, also on a LAN

Master plan: [PWA_ARTIFACTS_MASTER_PLAN.md](https://github.com/webtyp/app/blob/main/docs/PWA_ARTIFACTS_MASTER_PLAN.md), decision **D-PWA-13**.

## Why

A service worker, the Origin Private File System and `navigator.storage.persist()` exist only in a
*secure context* (HTTPS, or `localhost`). Today `httpd.Config` with a zero `TLS` field serves plain
HTTP. A clinic opening its own server as `http://192.168.1.20:8080` gets a page with no service
worker and no OPFS, so the in-browser assistant cannot store its models. The fix is that the
**zero value is HTTPS**, using the local certificate authority that today is only used in
development (`DevTLS`). Plain HTTP becomes an explicit opt-out.

Read before starting: [docs/ARCHITECTURE.md](ARCHITECTURE.md) section "Development TLS" and
[docs/DESIGN.md](DESIGN.md) (why `smallstep/truststore`). The certificate chain itself (CA + leaf with
`localhost`, loopback and every LAN IPv4, regenerated when the address set changes) does **not**
change. What changes is when it is used, what it is called, two additions (host name SAN, renewal
without restart), and where `/__webtyp/ca` is mounted.

## Design gate

1. **Prior art.**
   - **Caddy** serves HTTPS by default for every site; for hosts that cannot get a public
     certificate (`localhost`, IPs, internal names) it uses its *internal* CA, installs it in the
     system trust store, and HTTP only happens when the address is written with `http://`
     explicitly. This plan copies that rule.
   - **mkcert** is the same two-level chain (local CA installed with the trust store library this
     repo already uses, leaf with IP SANs) but is a separate tool the developer runs by hand; we
     already generate in-process, so nothing to adopt.
   - **Go `net/http`** and most frameworks (Express, Rails `rails s`) default to plain HTTP and
     leave TLS to a proxy. That default is exactly what produced a clinic server without a secure
     context; we follow Caddy, not them.
2. **Novice-name test.** `TLSConfig{PlainHTTP: true}` reads "serve plain HTTP" — the only way to
   get it, greppable. `LocalCA()` = "the certificate authority of this local server";
   `LocalCertFiles()` and `LocalCertSPKI()` follow. `Dev*` names are wrong once production uses
   them, so they are renamed in this change (all consumers are inside this repo — verified with
   `grep -rn "DevCA\|DevCertFiles\|DevCertSPKI\|DevTLS"` across the ecosystem).
3. **Complexity ledger.**
   ```
   Concepts the developer must learn   +1 (PlainHTTP) / −1 (DevTLS)
   Files they must touch to do X       −1 (no CA route to mount by hand)
   Lines at the call site              0 for HTTPS (zero value), +1 to opt out
   Ways to do the same thing           0  (DevTLS dies; CA route mounted only by httpd)
   ```
4. **Where it belongs.** `httpd` owns TLS modes and the CA route; the generated main and the dev
   strategy stop mounting the CA route themselves (glue written once, in the owner).
5. **What it deletes.** `TLSConfig.DevTLS`; `DevCA`, `DevCertFiles`, `DevCertSPKI` (renamed); the
   hand-written `CAPath` route in `maingen.go`'s template and in `strategies.go`;
   `MainConfig.DevTLS` and `mainTemplateData.DevTLS` (replaced by `PlainHTTP`).

## Constraints

- This repo is **server-side Go** (`//go:build !wasm` where present) and legitimately uses the
  standard library (`net/http`, `crypto/*`, `os`, `time`). Do **not** replace those imports with
  `webtyp.com/*` packages.
- Tests: follow [AGENTS.md](../AGENTS.md) — black-box tests in `tests/`, white-box tests next to
  the code in `httpd/` (`package httpd`). Every test that generates certificates calls
  `t.Setenv("HOME", t.TempDir())` (pattern: `httpd/devcert_internal_test.go`). `EnvSkipTruststore`
  is already set in `httpd/main_test.go` and `tests/main_test.go`; keep it.
- No hardcoded strings in logic: every new path, name or message is a named constant.
- Run `gotest` (install: `go install webtyp.com/devflow/cmd/gotest@latest`). Never run `gopush`
  or `codejob`.

## Stage 1 — `TLSConfig`: zero value is the local CA

File `httpd/tls.go`.

1. Replace the struct with:
   ```go
   // TLSConfig picks how the server is reached. The zero value serves HTTPS with the
   // local certificate authority (see LocalCA): a page served by httpd is always a
   // secure context, on localhost and on a LAN. Set at most one of the fields.
   type TLSConfig struct {
       AutoCert  bool   // public certificate from Let's Encrypt for Domain
       Domain    string
       CertFile  string // certificate and key provided by the operator
       KeyFile   string
       PlainHTTP bool   // plain HTTP: only when a proxy in front terminates TLS
   }
   ```
   `DevTLS` is deleted.
2. Add an unexported mode resolver used by validation, listening and `New`:
   ```go
   type tlsMode uint8
   const (
       tlsLocal tlsMode = iota // zero value
       tlsAutoCert
       tlsFiles
       tlsPlain
   )
   func (c TLSConfig) mode() tlsMode
   ```
   `mode()` returns the single mode set; `validateTLS` keeps rejecting more than one. Update the
   error text to exactly:
   `multiple TLS modes enabled; choose at most one (AutoCert, Cert/Key, or PlainHTTP)` — as a named
   constant `errMultipleTLSModes`.
3. `listenAndServe`: `tlsAutoCert` and `tlsFiles` unchanged; `tlsPlain` → `srv.ListenAndServe()`;
   `tlsLocal` → serve with `srv.TLSConfig = &tls.Config{GetCertificate: s.localCert.get}` and
   `srv.ListenAndServeTLS("", "")` (see Stage 3 for `localCert`).

## Stage 2 — rename `Dev*` to `Local*`, add the host name

File `httpd/devcert.go` → rename the file to `httpd/localcert.go`; `httpd/spki.go` stays.

1. Renames (exported): `DevCA` → `LocalCA`, `DevCertFiles` → `LocalCertFiles`, `DevCertSPKI` →
   `LocalCertSPKI`. Unexported: `ensureDevCA` → `ensureLocalCA`, `ensureDevCert` →
   `ensureLocalCert`, `devCertDir` → `localCertDir`, `devCertLeafDER` → `localCertLeafDER`,
   `getOrCreateDevCert` deleted (Stage 3 replaces its only use), `errDevCertDecode` →
   `errLocalCertDecode` with message `httpd: local certificate is not valid PEM`. Rename the
   `devCert*`/`devCA*`/`devKey*` constants to `localCert*`/`localCA*`/`localKey*`.
2. `devCertOrg = "WebTyp Dev CA"` → `localCertOrg = "WebTyp Local CA"`. The CA on disk is **not**
   regenerated because of this: `ensureLocalCA` keeps loading any valid CA it finds.
3. **Keep the directory** `~/.webtyp/httpd/certs` and the file names (`ca.crt`, `ca.key`,
   `localhost.crt`, `localhost.key`, `localhost.sans`) exactly as they are: devices that already
   trust the CA must keep trusting it.
4. `EnvSkipTruststore` keeps its Go name; its value becomes `"WEBTYP_LOCALCERT_SKIP_TRUSTSTORE"`.
5. **Host name SAN.** Add `var hostname = os.Hostname` (indirected like `interfaceAddrs`, for
   tests). `desiredSANs` adds `strings.ToLower(name)` to `dns` when `hostname()` returns no error,
   a non-empty name, and a name different from `localhost`. It is part of the fingerprint, so a
   rename of the machine regenerates the leaf. Reason, as a comment: clinic PCs open the server by
   its Windows computer name (`https://servidor:8080`), not only by IP.

## Stage 3 — renewal without restart

The leaf is valid one year and today is regenerated only at start. A clinic server runs for
months without restarting.

1. In `localcert.go` add `const localCertRenewBefore = 30 * 24 * time.Hour` and
   `var now = time.Now` (for tests). `fresh(...)` returns false when
   `now().Add(localCertRenewBefore).After(cert.NotAfter)`.
2. New unexported type in `httpd/localcert.go`:
   ```go
   // localCert hands the TLS stack the current leaf, regenerating it when it is close to
   // expiry or the host's address set changed.
   type localCert struct {
       mu       sync.Mutex
       cert     *tls.Certificate
       notAfter time.Time
       checked  time.Time
       logf     func(...any)
   }
   func (l *localCert) get(*tls.ClientHelloInfo) (*tls.Certificate, error)
   ```
   `get` re-runs `ensureLocalCert(l.logf)` and reloads with `tls.LoadX509KeyPair` when `cert` is
   nil, when `now()` is past `notAfter - localCertRenewBefore`, or when more than
   `localCertRecheck = time.Hour` passed since `checked` (this also picks up a new LAN address
   without a restart). Otherwise it returns the cached certificate. Any error is returned (the
   handshake fails loudly; never fall back to plain HTTP).
3. `Server` gets a field `localCert *localCert`, created in `New` with `logf: s.log`.

## Stage 4 — `httpd` mounts the CA route itself

1. In `New` (`httpd/httpd.go`), after creating the router: when `c.TLS.mode() == tlsLocal`,
   register `r.PublicAsset(CAPath, serveLocalCA)` where `serveLocalCA` (new, in `httpd/ca.go`)
   writes `LocalCA()` with `Content-Type: CADownloadContentType`, or status 503 when it errors —
   the body that both copies have today.
2. Update the doc comment of `CAPath` in `httpd/ca.go`: httpd serves it whenever it serves the
   local CA; add one sentence for Windows: open the downloaded file and install it in "Trusted Root
   Certification Authorities".
3. `maingen.go`: delete the `s.Router().PublicAsset(httpd.CAPath, ...)` block from
   `mainTemplateText` (and the `router` import if it becomes unused there). Replace
   `MainConfig.DevTLS` / `mainTemplateData.DevTLS` with `PlainHTTP bool` and the template line with
   `TLS: httpd.TLSConfig{PlainHTTP: {{.PlainHTTP}}},`. `main_decision.go`: `PlainHTTP: !h.Https`.
4. `strategies.go`: delete the `if s.handler.Https { r.PublicAsset(httpd.CAPath, ...) }` block;
   set `TLS: httpd.TLSConfig{PlainHTTP: !s.handler.Https}` in `hcfg`; replace
   `httpd.DevCertFiles()` with `httpd.LocalCertFiles()`. Any other `httpd.DevCA`/`DevCertSPKI` call
   in the repo is renamed.
5. Registering `CAPath` twice must still fail loudly as any duplicate route does today — do not
   add a guard that silently skips it.

## Stage 5 — tests

Update every existing test that used `DevTLS`, `DevCA`, `DevCertFiles`, `DevCertSPKI` or the old
error text. Every test that calls `ListenAndServe` and talks plain `http://` (at least
`httpd/tls_test.go`, `httpd/concurrency_test.go`, `tests/router_test.go`,
`tests/restart_cleanup_test.go`, `tests/envfile_integration_test.go`,
`tests/startserver_integration_test.go`, `tests/port_conflict_test.go` — check each) either sets
`PlainHTTP: true` (when HTTPS is not what it tests) or switches to an `https://` client.

New tests (names exact):

| Test | Where | Proves |
|---|---|---|
| `TestZeroTLSConfig_ServesHTTPSWithLocalCA` | `httpd/tls_test.go` | `New(Config{Port: p, Health: true})` + `ListenAndServe`; a client whose `RootCAs` contains only `LocalCA()` gets 200 from `https://127.0.0.1:<p>/health` **without** `InsecureSkipVerify` |
| `TestPlainHTTP_ServesHTTP` | `httpd/tls_test.go` | `PlainHTTP: true` answers `http://`, and `/__webtyp/ca` is 404 |
| `TestValidateTLS_PlainHTTPWithAutoCertRejected` | `httpd/tls_test.go` | error text equals `errMultipleTLSModes` |
| `TestLocalCA_RouteMountedByDefault` | `tests/` | `New(Config{})` + `Handler()` + `httptest`: `GET /__webtyp/ca` → 200, `Content-Type` = `CADownloadContentType`, body parses with `x509.ParseCertificate` and `IsCA` |
| `TestLocalCA_RouteAbsentWithOperatorCert` | `tests/` | with `CertFile`/`KeyFile` set, `/__webtyp/ca` → 404 |
| `TestLocalCert_IncludesHostname` | `httpd/localcert_internal_test.go` | stub `hostname` → `"Servidor"`; leaf `DNSNames` contains `"servidor"` |
| `TestLocalCert_RenewsBeforeExpiry` | `httpd/localcert_internal_test.go` | generate; stub `now` to `NotAfter − 29 days`; `localCert.get` returns a leaf with a later `NotAfter` |
| `TestLocalCert_ReusesFreshLeaf` | `httpd/localcert_internal_test.go` | two `get` calls within an hour return the same leaf serial |
| `TestGenerateMain_PlainHTTPAndNoCARoute` | `tests/` | generated main contains `PlainHTTP: false` (dev default) and does not contain `CAPath` |

Rename `httpd/devcert_internal_test.go` → `httpd/localcert_internal_test.go` and its `TestDev*`
functions to `TestLocal*`.

## Stage 6 — docs

- `docs/ARCHITECTURE.md`: rename section "Development TLS" → "TLS: HTTPS by default"; first
  paragraph states the zero value serves the local CA in development **and** production, `PlainHTTP`
  is the only way to plain HTTP, the CA route is mounted by `httpd`, the leaf carries the host name
  and renews itself 30 days before expiry. Update every `DevTLS`/`DevCA`/`DevCertSPKI` mention and
  the env var name. Keep the iOS two-step note and add the Windows one.
- `docs/DESIGN.md`: env var name; add the Caddy/net/http comparison of the design gate as "Why
  HTTPS is the zero value".
- `README.md`: the TLS line lists `AutoCert`, `Cert/Key`, `PlainHTTP`, and "default: local CA".

## Acceptance

- `gotest` green.
- `grep -rn "DevTLS\|DevCA\|DevCertFiles\|DevCertSPKI\|WEBTYP_DEVCERT\|WebTyp Dev CA" --include=*.go --include=*.md .` → empty
  (except `docs/LAST_PLAN_EXECUTED.md`).
- `grep -n "CAPath" maingen.go strategies.go` → empty.

| Stage | Files | Done when |
|---|---|---|
| 1 | `httpd/tls.go` | zero value = local mode, `PlainHTTP` opt-out |
| 2 | `httpd/localcert.go`, `httpd/spki.go` | renamed, host name SAN |
| 3 | `httpd/localcert.go`, `httpd/httpd.go` | renewal via `GetCertificate` |
| 4 | `httpd/httpd.go`, `httpd/ca.go`, `maingen.go`, `main_decision.go`, `strategies.go` | CA route mounted only by `httpd` |
| 5 | tests | table above green |
| 6 | docs | no `Dev*` names left |
