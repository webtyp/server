---
PLAN: "feat(httpd): TLS by DNS-01 — a real Let's Encrypt certificate for a server that is not reachable from the internet (lego)"
EXECUTOR: jules
REVIEWER: none
STATUS: running
SESSION: 4497181084279041639
---

> Read AGENTS.md at the repo root before any change.

# PLAN — the DNS-01 TLS mode

## Why

An app served inside a private network (behind a router with no open ports) still needs a
certificate every phone and PC trusts with nothing installed: a passkey only works over HTTPS on a
trusted origin, and its `rp.id` must be the site's domain. `AutoCert` (HTTP-01/TLS-ALPN) cannot
work there: Let's Encrypt cannot reach the server. DNS-01 can: «You can use this challenge to
validate domain names whose webservers aren't exposed to the public internet»
(https://letsencrypt.org/docs/challenge-types/). Decision D29 of
`veltylabs/mjosefa-cms/docs/MVP_DEPLOYMENT_ROADMAP.md`: use the lego library and its DNS
providers (https://go-acme.github.io/lego/dns/cloudflare/) instead of writing the ACME flow here.
Examples and tests use `example.com` only (RFC 2606) — never a client's domain.

## Design gate (api-design)

1. **Prior art.** lego is the ACME client behind production servers' DNS-01 support; its
   providers publish the TXT record, wait for propagation and clean up. The app chooses the
   provider (Cloudflare, Route53, …); this package does not know any.
2. **Names.** `TLSConfig.DNS01` — «get the certificate by DNS-01 with this provider». Same
   `Domain` field `AutoCert` already uses.
3. **Ledger.** One field; a fifth TLS mode; zero lines for apps that do not use it.
4. **Where.** `httpd/tls.go`, next to the other modes; the provider is injected (the app builds it
   with its token), so no DNS vendor is imported here.
5. **Deletes.** Nothing.

## APIs used (lego v4.35.2 — verified on pkg.go.dev)

| Symbol | Exact |
|---|---|
| `challenge.Provider` | the DNS provider interface of `github.com/go-acme/lego/v4/challenge` |
| `registration.User` | `interface { GetEmail() string; GetRegistration() *registration.Resource; GetPrivateKey() crypto.PrivateKey }` |
| `lego.NewConfig` | `func NewConfig(user registration.User) *Config`; `Config{CADirURL string; …}` |
| `lego.NewClient` | `func NewClient(config *Config) (*Client, error)`; `Client{Certificate *certificate.Certifier; Challenge *resolver.SolverManager; Registration *registration.Registrar}` |
| `SetDNS01Provider` | `func (c *SolverManager) SetDNS01Provider(p challenge.Provider, opts ...dns01.ChallengeOption) error` |
| `Register` | `func (r *Registrar) Register(options RegisterOptions) (*Resource, error)`; `RegisterOptions{TermsOfServiceAgreed bool}` |
| `Obtain` | `func (c *Certifier) Obtain(request ObtainRequest) (*Resource, error)`; `ObtainRequest{Domains []string; Bundle bool; …}` |
| `certificate.Resource` | `{Domain, CertURL, CertStableURL string; PrivateKey, Certificate, IssuerCertificate, CSR []byte}` (PEM) |
| Production directory | `lego.LEDirectoryProduction` (constant of package `lego`; check its exact name in v4.35.2 and say so in the PR if different) |

## Tools

```
go get github.com/go-acme/lego/v4@v4.35.2
```

## Stage 1 — `httpd/tls.go`

- `TLSConfig` gets `DNS01 challenge.Provider // a real certificate for Domain by DNS-01; nil = off` and
  a new mode `tlsDNS01`. `mode()` returns it when `DNS01 != nil`. `validateTLS` counts it as a
  mode and requires `Domain` (error text in a named constant, like the others). Update
  `errMultipleTLSModes` to name it.
- New file `httpd/dns01.go` (`//go:build !wasm` like the rest of the transport): a small type that
  keeps the current certificate behind a mutex and serves it from `tls.Config.GetCertificate`.
  - Account key: ECDSA P-256, generated once and stored PEM in `certs/acme-account.key` (same
    `certs` directory `AutoCert` uses with `autocert.DirCache("certs")`).
  - Certificate: stored as `certs/<domain>.crt` and `certs/<domain>.key`; loaded at start if
    present and not expiring within 30 days, otherwise obtained (`Register` with
    `TermsOfServiceAgreed: true` the first time, then `Obtain` with `Domains: []string{Domain}`,
    `Bundle: true`).
  - A goroutine checks once a day and obtains a new certificate when fewer than 30 days remain
    (re-`Obtain` is enough; no need for the deprecated `Renew`). A failure is logged through
    `s.log` and retried the next day; the old certificate keeps serving.
  - The obtain call is a field `obtain func(domain string) (*certificate.Resource, error)` built
    from lego in production and replaced in the package's own tests.
- `listenAndServe`: `case tlsDNS01:` set `srv.TLSConfig = &tls.Config{GetCertificate: <that type>.get}`
  and `ListenAndServeTLS("", "")`, after the first certificate is ready (fail the start if it
  cannot be obtained and none is stored).

## Stage 2 — tests (`httpd/dns01_test.go`, package `httpd`)

No network: `obtain` returns a self-signed certificate for `app.example.com` built in the test
(`crypto/x509`), with the `NotAfter` each case needs; files go to `t.TempDir()`.

| Test | Setup | Expected |
|---|---|---|
| `TestDNS01_RequiresDomain` | `TLSConfig{DNS01: fakeProvider}` without `Domain` | `Handler()` returns the named error |
| `TestDNS01_ExclusiveWithOtherModes` | `DNS01` + `AutoCert` | `errMultipleTLSModes` |
| `TestDNS01_ObtainsWhenNoneStored` | empty dir | `obtain` called once; files written; `GetCertificate` serves it |
| `TestDNS01_ReusesStoredCertificate` | stored cert valid 60 days | `obtain` not called |
| `TestDNS01_RenewsWhenExpiringSoon` | stored cert valid 10 days | `obtain` called; new cert served |
| `TestDNS01_FailedRenewKeepsServing` | stored cert valid 10 days, `obtain` errors | old cert still served; error logged |

`fakeProvider` implements `challenge.Provider` with empty methods (it is never called because
`obtain` is replaced).

## Stage 3 — docs

`README.md` (TLS section): the new mode, a full example with
`cloudflare.NewDNSProviderConfig` and a token read from the environment (`CF_DNS_API_TOKEN`, Zone
Read + DNS Edit for the one zone), `Domain: "app.example.com"`, where certificates are stored, the
30-day renewal, and «the token is a production secret: only on the server» (skill **secrets**).
`docs/ARCHITECTURE.md`: why DNS-01 (server not reachable) and why the provider is injected.

## Delivery check

```
gotest
```

- A PR that does not compile, or whose tests could not run, is a failed delivery.
- Never pause to ask permission: when the check passes, commit, push and open the PR. Stuck 30
  minutes: open the PR listing what fails.
- `git diff --stat main` lists only: `httpd/tls.go`, `httpd/dns01.go`, `httpd/dns01_test.go`,
  `README.md`, `docs/ARCHITECTURE.md`, `go.mod`, `go.sum`, this plan.
- No client domain anywhere: only `example.com`.

## Stages

| # | Files | Done when |
|---|---|---|
| 1 | `httpd/tls.go`, `httpd/dns01.go`, `go.mod`, `go.sum` | compiles |
| 2 | `httpd/dns01_test.go` | 6 tests pass |
| 3 | `README.md`, `docs/ARCHITECTURE.md` | sections present |
