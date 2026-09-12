---
PLAN: "fix: unify the unsupported-file-event sentinel with devwatch, and make the external server's .env visible"
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — two independent fixes in the external-mode server strategy

Part of `DOTENV_VISIBILITY_MASTER_PLAN.md` (orchestrator: `webtyp.com/docs`,
`docs/DOTENV_VISIBILITY_MASTER_PLAN.md`). This file has two stages with
different gating:

- **Stage A has no dependencies — dispatch it now.**
- **Stage B depends on `webtyp.com/gorun` publishing `Config.EnvFile`
  (`gorun/docs/PLAN.md`). Do not dispatch Stage B until that tag exists;
  bump `go.mod` to it as the first line of Stage B's work.**

Do these as two separate PRs/commits even though they live in the same
file — they fix unrelated defects and must be reviewable independently.

---

## Stage A — return `devwatch.ErrUnsupportedEvent`, delete the local duplicate

### Why

`webtyp.com/devwatch` already declares the sentinel a `FilesEventHandlers`
implementor must return to say "this event doesn't apply to me, don't log
it, don't reload" — `devwatch.ErrUnsupportedEvent`
(`devwatch/devwatch.go:17`), and `devwatch`'s own event-handling code checks
for it correctly with `errors.Is` (`devwatch/watchEvents.go:167`).

`webtyp/server` never imports `devwatch`. Instead it declares its **own**
sentinel with the same intent (`server/strategies.go:22`):

```go
var ErrUnsupportedEvent = errors.New("server: unsupported file event, no rebuild triggered")
```

and returns it from `externalStrategy.HandleFileEvent`
(`server/strategies.go:548`). Because `errors.New` produces a distinct value
each time, `errors.Is(err, devwatch.ErrUnsupportedEvent)` is **always false**
for an error returned by `server` — even though both sides believe they are
using "the" unsupported-event sentinel. Two consequences, both currently
live in production:

1. `devwatch`'s initial directory scan (`devwatch/devwatch.go:316-319`) logs
   every one of these as `"InitialRegistration file error: server:
   unsupported file event, no rebuild triggered"` — one line per `.go` file
   in the project on every `webtyp dev` startup (18 lines observed in
   `veltylabs/mjosefa-cms`, a mid-sized project). None of these are errors;
   they are the expected "this handler doesn't act on scan events" case.
2. The **live** file-watch path (`devwatch/watchEvents.go:167`) also fails
   to filter it, so the same noise repeats on every file edit the `server`
   handler doesn't act on, permanently — not just at startup.

This is exactly the "missing contract at a boundary" `CONSTRUCTION_HARNESS.md`
prohibits: an implementor of an interface re-declared, locally, a symbol the
interface's own package already exports for this purpose.

### What to change

`server/go.mod` — add `webtyp.com/devwatch` to `require` (check the latest
published tag with `go list -m -versions webtyp.com/devwatch`, or use
`go get webtyp.com/devwatch@latest`).

`server/strategies.go`:
- Delete line 22: `var ErrUnsupportedEvent = errors.New("server: unsupported file event, no rebuild triggered")`
- Add `"webtyp.com/devwatch"` to the import block (alongside `"webtyp.com/gobuild"`, `"webtyp.com/gorun"`, `"webtyp.com/router"`).
- Line 548, change `return ErrUnsupportedEvent` to `return devwatch.ErrUnsupportedEvent`.
- Confirm `errors` is still used elsewhere in the file after removing this
  line before deciding whether to drop the `"errors"` import — grep the file
  for other `errors.` usages first (there is at least one, in
  `Stop()`/mode-switch error handling — check before removing the import).

`server/handle_file_event_test.go`:
- Add `"webtyp.com/devwatch"` to the import block.
- Line 63, change `expectErr: ErrUnsupportedEvent` to
  `expectErr: devwatch.ErrUnsupportedEvent`. The comparison at line 104 is
  by `.Error()` string, so this is the only change needed for that test to
  keep passing with the new message text.

### Acceptance

- `grep -rn "server: unsupported file event" server/` → empty (the old
  string is gone from source).
- `go test ./...` in `webtyp/server` passes.
- Manually: run any project in external mode with `webtyp dev`, confirm the
  startup log no longer prints `InitialRegistration file error` lines for
  ordinary `.go` files the `server` handler doesn't act on.

---

## Stage B — forward the project's `.env` to the external server process

**Gate: requires `webtyp.com/gorun` to have published `Config.EnvFile`
(see `gorun/docs/PLAN.md`). Bump `server/go.mod`'s `webtyp.com/gorun`
requirement to that version before writing any other line of this stage.**

### Why

`server/strategies.go:378-386` (`newExternalStrategy`) builds the
`gorun.Config` that runs the project's external server binary with
`WorkingDir: filepath.Join(h.AppRootDir, h.OutputDir)` — the build output
directory, not the project root. Any project reading configuration via
`webtyp.com/env` (`env.Get`/`env.Require`, which fall back to a **relative**
`os.ReadFile(".env")`) never sees its own root `.env` in external mode,
because the child process's CWD is never the root. The observed symptom in
`veltylabs/mjosefa-cms`: `DATABASE_URL` is correctly set in the project's
`.env`, yet the external server logs `DATABASE_URL is not set` and exits,
which the dev loop then reports three more times, unhelpfully, as `Server
port not responding after 30s` and a browser `ERR_CONNECTION_REFUSED` — none
of which names the real cause.

`gorun.Config.EnvFile` (Stage 1's deliverable, one repo over) exists exactly
to close this: point it at the project's `.env` and the child process sees
those variables through its real OS environment, independent of its CWD.

### What to change

`server/strategies.go`, inside `newExternalStrategy` (currently lines
367-376), add `EnvFile` to the `gobuild.Config` — no, to the **`gorun.Config`
literal** (the second one, currently lines 378-386):

```go
	runner := gorun.New(&gorun.Config{
		ExecProgramPath:      compiler.FinalOutputPath(),
		RunArguments:         h.ArgumentsToRunServer,
		ExitChan:             h.ExitChan,
		Logger:               h.log,
		KillAllOnStop:        true,
		DisableGlobalCleanup: h.Config.DisableGlobalCleanup,
		WorkingDir:           filepath.Join(h.AppRootDir, h.OutputDir),
		EnvFile:              filepath.Join(h.AppRootDir, ".env"),
	})
```

`h.AppRootDir` is already in scope in this function (used two lines above
for `WorkingDir`); `".env"` is the same literal `webtyp.com/env` uses as
`defaultDotEnvPath` — do not import `webtyp.com/env` here just to reuse
that constant, it is unexported. Use the literal `".env"` directly; if a
shared constant is wanted later, that is `webtyp.com/env`'s call to export
one, not `server`'s call to duplicate its internal.

### Tests

Add a case to `server/tests/startserver_blackbox_test.go` (or the nearest
existing external-strategy integration test — check that file first for the
established pattern before adding a new one): start an external-mode
project whose `web/server.go` main does `fmt.Println(os.Getenv("PROBE_KEY"))`
(or reads it via `webtyp.com/env` to match the real-world path), with a
`.env` at the fake project root containing `PROBE_KEY=hello`, and assert the
captured process output contains `hello`. This is the "consumer-shaped test
inside the owning library" `CONSTRUCTION_HARNESS.md` requires — it must go
through `newExternalStrategy` → `gorun.RunProgram`, not call `parseEnvFile`
directly (that unit-level test already lives in `webtyp/gorun`).

### Acceptance

- The new integration test passes.
- Existing `server` tests still pass — `EnvFile` is additive, no existing
  `gorun.Config` literal elsewhere in this repo needs to change.
- Manually: with the fix built into `veltylabs/mjosefa-cms`'s `go.mod`
  (`go get webtyp.com/server@<new tag>`), `webtyp dev` starts successfully
  without editing anything in that project — its `.env` is already correct
  today; this whole plan changes only the framework's ability to see it.

## Stages

| Stage | Gate | Files | Done when |
|---|---|---|---|
| A | none — dispatch now | `strategies.go`, `handle_file_event_test.go`, `go.mod` | Acceptance criteria under Stage A pass |
| B | `webtyp.com/gorun` tag with `Config.EnvFile` published | `strategies.go`, `go.mod`, a new/extended integration test | Acceptance criteria under Stage B pass |
