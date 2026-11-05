---
PLAN: "feat: webtyp dev migrates the app's database before starting its server, with a progress line in the TUI"
EXECUTOR: jules
REVIEWER: none
STATUS: running
SESSION: 5353264496857617316
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit, push and open the pull request. Never ask whether to
> proceed or finalize — nobody answers, and a session that waits ends without your work.

# Plan — the database follows the modules in development, and the developer sees it happen

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
go get webtyp.com/gorun@v0.0.28
```

Environment: `GOPRIVATE=webtyp.com/*,github.com/veltylabs/*`, `GOFLAGS=-mod=mod`.

## Why

An app built on `webtyp.com/appkit` has a generated `migrate/main.go` at its root (it calls `appkit.Migrate`, which runs
every module's `ddl.Sync`: creates missing tables, adds columns, drops only empty columns, never rows). Nothing runs it
in development: `webtyp dev` compiles and starts `web/server`, the database keeps a schema weeks old and the app fails at
the first query (`pq: column "hold_until" does not exist`). Prior art (unverified links): Rails stops a development app
with «Migrations are pending»; Django's `runserver` warns «unapplied migrations». Here the tool does it.

## Ecosystem search

- `gorun.ParseEnvFile(path string, logger func(...any)) []string` (gorun v0.0.28, `envfile.go`) → **reuse**: the
  migration gets the same `.env` as the server (`gorun.Config.EnvFile`, `strategies.go:371`).
- Progress line: the TUI animates a log line that starts with `"[..."` and stops it with `"...]"`
  (`webtyp.com/tui` `LogOpen`/`LogClose`, `tui/interfaces.go:170`). This repo does not import `tui`: use the same
  literals as unexported constants `logOpen = "[..."`, `logClose = "...]"` with a comment naming `tui.LogOpen`.
- New: `migrate.go` (no equivalent in `server`).

## Design gate

1. Prior art: above.
2. Names: `migrateApp` (unexported), log texts below.
3. Ledger: +0 exported names; steps a developer must remember: 1 → 0.
4. Belongs here: `server` owns starting the app process in development.
5. Deletes: nothing (new capability).

## Stage 1 — `migrate.go` (new, `//go:build !wasm`)

```go
package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"webtyp.com/gorun"
)

// Prefijos que la TUI anima en una sola línea (los mismos de tui.LogOpen y tui.LogClose).
const (
	logOpen  = "[..."
	logClose = "...]"
)

// errMigrate: la migración falló; el servidor no arranca con una base que no calza.
var errMigrate = errors.New("database migration failed — the server was not started; fix the error above and save a file to retry")

// migrateApp corre migrate/main.go de la app (si existe) con el mismo .env que el servidor, antes de compilarlo.
// Sin migrate/main.go no hace nada: la app no tiene base de datos.
func (h *ServerHandler) migrateApp() error {
	root := h.Config.AppRootDir
	if _, err := os.Stat(filepath.Join(root, "migrate", "main.go")); err != nil {
		return nil
	}
	h.log(logOpen, "Migrating database")
	cmd := exec.Command("go", "run", "./migrate")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), gorun.ParseEnvFile(filepath.Join(root, ".env"), h.log)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.log(logClose, "Database migration failed")
		if len(out) > 0 {
			h.log(string(out))
		}
		return errMigrate
	}
	h.log(logClose, "Database up to date")
	return nil
}
```

## Stage 2 — call it before every compile of the external server (`strategies.go`)

- `externalStrategy.startServer` (`strategies.go:414`): first line `if err := s.handler.migrateApp(); err != nil { return errors.Join(e, err) }`.
- `externalStrategy.Restart` (`strategies.go:444`): after `waitForPortFree(...)`, before `CompileProgram`:
  `if err := s.handler.migrateApp(); err != nil { s.handler.log(err.Error()); return err }`.
  A restart follows a saved model file, so the new column reaches the database before the new binary runs.
- Do not touch `internalStrategy` (it has no app database).

## Stage 3 — tests (`migrate_test.go`, `//go:build !wasm`)

Use temp dirs with a fake `migrate/main.go` (a `package main` that prints or exits) and a minimal `go.mod`
(`module tmpapp`, `go 1.22`). Capture logs with `ServerHandler` built as other tests in this repo build it, with its
log callback recording messages.

| Test | Setup → call | Expected |
|---|---|---|
| `TestMigrateAppWithoutMigrate` | no `migrate/` → `migrateApp()` | `nil`; no log line |
| `TestMigrateAppOK` | `main.go` prints "ok" → `migrateApp()` | `nil`; logs[0] = `["[...", "Migrating database"]`, last = `["...]", "Database up to date"]` |
| `TestMigrateAppFails` | `main.go` does `os.Exit(1)` after printing "boom" → `migrateApp()` | `errMigrate`; a log contains "boom"; a log is `["...]", "Database migration failed"]` |
| `TestMigrateAppReadsEnvFile` | `.env` with `FOO=bar`; `main.go` exits 1 unless `os.Getenv("FOO") == "bar"` | `nil` |

## Stage 4 — docs

`README.md` (Step-by-Step) and `docs/ARCHITECTURE.md`: in external mode, before compiling (start and restart), the
server runs the app's `migrate/main.go` when it exists; failure stops the start and shows the output.

## Delivery check

1. `gotest` passes. A PR that does not compile, or whose tests could not run, is a failed delivery.
2. `grep -n "migrateApp" strategies.go` → 2 lines.

Commit after every stage. Never run `git reset`, `git restore`, `git checkout -- <file>` or `git stash` on your own
work. Stuck 30 minutes: open the PR anyway listing what fails. If `gorun.ParseEnvFile` has another signature in
v0.0.28, stop and name it in the PR; never adapt it by guessing.

## Stages

| Stage | Files | Done when |
|---|---|---|
| 1 | `migrate.go` | compiles |
| 2 | `strategies.go` | compiles |
| 3 | `migrate_test.go` | tests pass |
| 4 | `README.md`, `docs/ARCHITECTURE.md` | done |
