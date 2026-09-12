# Architecture — `webtyp/server/httpd`

The `httpd` subpackage provides a high-level, "batteries-included" HTTP server implementation that implements the `webtyp.com/router` contract.

## Relationship with `internalStrategy`

The development mode's `internalStrategy` (in-memory server) consumes the `httpd.NewRouter` adapter to provide consistent routing behavior between development and production.

## The server entry point is generated, not written

A project does not contain an HTTP `main()`. `maingen.go` writes one to
`.build/server/main.go` — inside the project, because outside the module tree
the Go toolchain cannot resolve the project's own imports — and the External
strategy compiles and runs it.

The generated main imports the project's `routes` package and calls
`routes.Register` on the server it builds, so development and production serve
**the same route set from the same source**. The previous
`templates/server_basic.md` scaffold is gone: it produced a committed
`web/server.go` whose routes had to be kept in sync by hand with the ones the
dev process registered, and they drifted.

Which strategy runs is decided by one file: a project with `routes/routes.go`
gets the compiled server; a project without one gets the in-memory asset server
and compiles nothing. A `web/server.go` that a developer writes themselves still
wins — the tool never overrides code the user owns — but nothing generates it.

## Components

- **`adapter.go`**: Implementation of `router.Router`, `router.Context`, etc., mapping them to `net/http`. `httpd` produces `router.ContextKeyRemoteAddr` from `Request.RemoteAddr`.
- **`middleware.go`**: Built-in `Gzip` and `NoCache` middlewares.
- **`static.go`**: Static file serving from `PublicDir`.
- **`enforce.go`**: RBAC enforcement based on `Requires` metadata.
- **`tls.go` / `devcert.go` / `ca.go`**: AutoCert (Let's Encrypt), custom
  Cert/Key, and `DevTLS`. See the TLS section below.
- **`spki.go`**: `DevCertSPKI()`, the pin the development browser trusts.
- **`routes_endpoint.go`**: Optional JSON endpoint at `/_routes` listing all registered routes.
- **`httpd.go`**: Core `Server` orchestrator.

## Pattern Handling and Introspection Delegations

1. **Pattern Matching & Extraction**: Delegated to `net/http.ServeMux`. Routes registered as `"GET /api/items/{id}"` natively match parameters in Go 1.22+, accessible via `ctx.Param(name)`.
2. **Pattern Validation**: Delegated to `webtyp/router` via `router.ValidatePattern`. This server rejects patterns with wildcards (e.g., `{name...}`) at registration time so that patterns unsupported by edge runtimes fail early on the dev server.
3. **Introspection Endpoint**: Delegated to `router.MountIntrospection` at `router.IntrospectionPath` (`/_routes`). This repo supplies the `Config.RoutesEndpoint` boolean toggle and the `Config.Policy` describer.

## Design Goals

1. **Simple Entry Point**: `httpd.New(config).Mount(modules).ListenAndServe()` is the only way to start the server.
2. **Standard Library Based**: Uses `net/http` internally but doesn't expose it in the public API.
3. **Fails Fast**: Validates configuration (like TLS modes or RBAC requirements) at startup rather than at runtime.

## RBAC and Public Assets

The server follows a **secure-by-default** model where routes are private unless explicitly marked as `Public()`. However, for a smooth development experience:

- **Static Assets**: Files served from `PublicDir` (e.g., WASM binaries, JS, CSS) are always public. They bypass the RBAC middleware.
- **Root Path (`/`) in Development**: The internal strategy registers a default public route for `/` that serves `PublicDir/index.html` or a diagnostic message. This ensures that the frontend application is accessible immediately without manual RBAC configuration.

## Development TLS

`DevTLS` is on by default in development. Serving HTTP locally and HTTPS in
production hides a class of bugs that only appear after deployment — `Secure`
cookies, `SameSite=None`, HSTS, mixed content — and makes it impossible to test
a PWA from a phone on the LAN.

It generates a **two-level chain**, the same shape `mkcert` and Caddy's internal
PKI use:

- **The CA** (`ca.go`) — `IsCA: true`, `KeyUsageCertSign | KeyUsageCRLSign`,
  `MaxPathLen: 0`, no subject alternative names, long-lived. This is the file a
  device installs to trust the server.
- **The leaf** (`devcert.go`) — signed by the CA, `IsCA: false`,
  `serverAuth`, and a SAN set covering `localhost`, `127.0.0.1`, `::1` **and
  every non-loopback IPv4 of the host's interfaces**. It is regenerated when
  that address set changes, so a laptop moving between networks does not keep
  presenting a certificate for an address it no longer has.

A single self-signed certificate cannot do this job: a leaf has `CA:FALSE`, and
iOS and Android refuse to use it as a trust anchor — the device shows no error,
the page simply keeps failing after the user has followed every instruction.

### Two ways a client trusts it, and they are not interchangeable

| Client | Mechanism | Trusts |
|---|---|---|
| The development browser | `--ignore-certificate-errors-spki-list` with `DevCertSPKI()` | exactly the **leaf's** public key |
| A phone on the LAN | installs the CA from `CAPath` (`/__webtyp/ca`) | anything the CA signs |

`DevCertSPKI()` returns the **leaf's** SPKI hash, never the CA's. Chrome matches
the flag against any certificate in the presented chain, so pinning the CA would
appear to work — and would silently grant the browser trust in every certificate
that CA ever signs, surviving a leaf rotation unnoticed.

Nothing is installed into the developer's own OS trust store. The browser is
launched by `webtyp.com/devbrowser`, so it is configured rather than convinced,
which needs no elevated privileges and leaves nothing behind.

On iOS, installing a profile is **two steps** and the platform announces only
the first: install it (Settings → Profile Downloaded), then enable it under
Settings → General → About → Certificate Trust Settings. A profile installed but
not trusted behaves exactly like no profile at all.
