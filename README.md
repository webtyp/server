# webtyp/server
<img src="docs/img/badges.svg">

Short summary
 - `server` provides a specialized HTTP server handler for WebTyp applications.
 - It operates in two execution modes:
    1. **Internal (Default)**: Runs a lightweight `net/http` server within the application process, routed via `httpd.NewRouter`. Best for development speed and zero-file generation start.
    2. **External**: Generates a standalone main Go file, compiles it, and runs it as a separate process. Best for customization and production-like validation.
 - It also supports two compilation modes: **In-Memory** (default) and **On-Disk**.
 - It seamlessly handles the transition between execution modes via `SetExternalServerMode`.
 - The `httpd` subpackage (`webtyp.com/server/httpd`) is the batteries-included `router.Router` adapter used internally and available for production use (see below).

Public API (types and functions)

- func `New() *ServerHandler` — creates a handler with default `Config` (`AppRootDir: "."`, `SourceDir/OutputDir: "web"`, `AppPort: "8080"`). Configure it with the setters below (fluent methods return `*ServerHandler` where noted).

- type `ServerHandler`
	- Setters:
		- `SetAppRootDir(dir string)`
		- `SetSourceDir(dir string)`
		- `SetOutputDir(dir string)`
		- `SetPublicDir(dir string) *ServerHandler`
		- `SetMainInputFile(name string)`
		- `SetPort(port string)` / `Port() string`
		- `SetHTTPS(enabled bool) *ServerHandler`
		- `SetLogger(fn func(...any)) *ServerHandler`
		- `SetExitChan(ch chan bool) *ServerHandler`
		- `SetOpenBrowser(fn func(port string, https bool)) *ServerHandler`
		- `SetStore(s Store) *ServerHandler`
		- `SetUI(ui UI) *ServerHandler`
		- `SetBeforeExternalServerStart(fn func() error) *ServerHandler` — hook invoked synchronously before every external-mode start.
		- `SetGitIgnoreAdd(fn func(string) error) *ServerHandler`
		- `SetCompileArgs(fn func() []string)`
		- `SetRunArgs(fn func() []string)`
		- `SetDisableGlobalCleanup(disable bool)`
	- Routing:
		- `RegisterRoutes(fn func(router.Router))` — appends a route-registration callback (using `webtyp.com/router`'s `Router` contract). Call before `StartServer`. Used by both Internal and External modes.
	- Lifecycle:
		- `StartServer(wg *sync.WaitGroup)` — starts the server (async).
		- `StopServer() error`
		- `RestartServer() error`
		- `SetExternalServerMode(external bool) error` — switches between Internal and External execution modes. When switching to External, it generates files (if missing), compiles, and starts the process. Cannot switch back to Internal.
		- `NewFileEvent(fileName, extension, filePath, event string) error` — handles hot-reloads (recompiles external server or no-op for internal).
		- `MainInputFileRelativePath() string`
		- `UnobservedFiles() []string`

Notes and behaviour
- **Routes Registration**: Use `RegisterRoutes` to register handlers via the `router.Router` contract (e.g., `r.Get("/path", handler)`) so they work immediately in Internal mode and are carried over to External mode.
- **Port Management**: When switching modes, the handler automatically waits for the port to be free before starting the new strategy.

Minimal usage example

```go
package main

import (
    "fmt"
    "os"
    "sync"

    "webtyp.com/router"
    "webtyp.com/server"
)

func main() {
    handler := server.New()
    handler.SetAppRootDir(".")
    handler.SetLogger(func(messages ...any) { fmt.Fprintln(os.Stdout, messages...) })

    handler.RegisterRoutes(func(r router.Router) {
        r.Get("/hello", func(ctx router.Context) {
            fmt.Fprint(ctx, "Hello from Internal Server!")
        }).Public()

        r.Get("/api/items/{id}", func(ctx router.Context) {
            fmt.Fprintf(ctx, "Item ID: %s", ctx.Param("id"))
        }).Public()
    })

    var wg sync.WaitGroup
    wg.Add(1)
    go handler.StartServer(&wg)
    wg.Wait()

    // To switch to external mode later:
    // handler.SetExternalServerMode(true)
}
```

## `httpd` subpackage

`webtyp.com/server/httpd` implements `router.Router`/`router.Context` on top of
`net/http`, with production batteries built in: gzip, no-cache headers, static file
serving, TLS (AutoCert/custom cert/DevTLS), a `/health` endpoint, an optional `/_routes`
JSON listing, and closed-by-default RBAC enforcement (`Config.Authn` for global identity,
`Config.Authorize` for per-route `Requires(resource, action)` checks, and `Config.Policy`
to report which roles hold each route's required permission; `Authorize` answers "may this user do this?",
while `Policy` answers "who may do this?"; routes must opt into `Public()` to allow anonymous access).

```go
s := httpd.New(httpd.Config{
    Port:   "8080",
    Health: true,
})
s.Mount(myAPIModule) // webtyp.com/router.APIModule
if err := s.ListenAndServe(); err != nil {
    log.Fatal(err)
}
```

`Handler()` returns the fully wired `http.Handler` (static files, batteries, routes, RBAC)
without opening a port, for use with `httptest`. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
for design details.

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — what the `httpd` subpackage is and why:
  the generated server entry point, RBAC enforcement, and the development TLS chain.
- [`docs/DESIGN.md`](docs/DESIGN.md) — decisions and rejected alternatives, including
  [why the dev certificate uses `smallstep/truststore` and not `mkcert`](docs/DESIGN.md#why-the-dev-certificate-uses-smallsteptruststore-and-not-mkcert).
- [`docs/handoff-protocol.md`](docs/handoff-protocol.md) — the graceful module-swap
  sequence used when a WASM module is recompiled while the server is running.

## [Contributing](https://github.com/cdvelop/cdvelop/blob/main/CONTRIBUTING.md)
