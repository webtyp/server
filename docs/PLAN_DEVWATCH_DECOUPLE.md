---
PLAN: "refactor: drop the webtyp.com/devwatch dependency, keep the unsupported-event contract structurally"
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.
>
> **Depends on `webtyp.com/devwatch` publishing `UnsupportedEventError` and
> `IsUnsupportedEvent`** — sibling plan
> [`devwatch/docs/PLAN.md`](https://github.com/webtyp/devwatch/blob/main/docs/PLAN.md).
> **Do not start until that tag exists.** As the first line of work, run
> `go list -m -versions webtyp.com/devwatch` (or check
> `https://github.com/webtyp/devwatch/releases`) and confirm a published tag
> contains `IsUnsupportedEvent` — `grep -rn "func IsUnsupportedEvent"` the
> downloaded module cache, or read the tagged source on GitHub. Never add a
> `replace`, never invent a version, never start Stage 1 against an
> unpublished devwatch tag.

# Plan — `webtyp.com/server`: remove the `devwatch` import

## 0. Context

`server` currently imports `webtyp.com/devwatch` for exactly one symbol:
`devwatch.ErrUnsupportedEvent`, returned from
`externalStrategy.HandleFileEvent` (`strategies.go:548`) to tell the
`devwatch` watcher "this file event isn't mine, don't log it, don't
reload." This is a backwards dependency direction: `devwatch` is the
dev-mode file watcher that *consumes* `server` (via the
`FilesEventHandlers` interface `server.ServerHandler` implements); `server`
has no business importing the tool that drives it, just to borrow one error
value.

The sibling `devwatch` plan referenced above adds a **structural** (duck-typed)
way to signal "unsupported": any error type — in any package — implementing

```go
type UnsupportedEventError interface {
	error
	Unsupported() bool
}
```

is recognized by `devwatch.IsUnsupportedEvent(err)`, which both of
`devwatch`'s internal call sites now use instead of a direct
`errors.Is(err, devwatch.ErrUnsupportedEvent)`. This plan makes `server`
define its **own** local error type satisfying that method set — recognized
by `devwatch` at runtime without `server` ever importing it.

**Ordering matters.** If `server` switches to a local, unimported sentinel
*before* the running `devwatch` version understands the structural contract,
`devwatch`'s old `errors.Is(err, devwatch.ErrUnsupportedEvent)` check will no
longer match `server`'s new local type at all, and the original bug this
whole effort started from — one spurious "InitialRegistration file error"
log line per `.go` file on every `webtyp dev` startup, repeating on every
live edit — comes back. This is why the gate above is absolute: verify the
structural contract exists in a published `devwatch` tag before writing any
other line of this plan.

## Stage 1 — local, import-free sentinel

**File:** `strategies.go`.

- Delete the import `"webtyp.com/devwatch"` from the import block.
- Add, near the top of the file (alongside other package-level `var`/`type`
  declarations — check the file for an existing convention spot before
  picking one):

```go
// unsupportedFileEventError signals that HandleFileEvent does not act on
// this event — no rebuild, no log line. It satisfies webtyp.com/devwatch's
// UnsupportedEventError interface (Unsupported() bool) structurally:
// server does not import devwatch to participate in that contract.
type unsupportedFileEventError struct{}

func (unsupportedFileEventError) Error() string {
	return "server: unsupported file event, no rebuild triggered"
}

func (unsupportedFileEventError) Unsupported() bool { return true }

// ErrUnsupportedEvent is returned from HandleFileEvent for file events this
// strategy does not act on.
var ErrUnsupportedEvent error = unsupportedFileEventError{}
```

- Line 548: change `return devwatch.ErrUnsupportedEvent` to
  `return ErrUnsupportedEvent`.
- Confirm `errors` is still imported/used elsewhere in the file (there is at
  least one other use, in `Stop()`/mode-switch error handling) before
  deciding whether anything else in the import block needs to change — it
  should not.

## Stage 2 — test file

**File:** `handle_file_event_test.go`.

- Remove `"webtyp.com/devwatch"` from the import block.
- Change `expectErr: devwatch.ErrUnsupportedEvent` back to
  `expectErr: ErrUnsupportedEvent`. The comparison is by `.Error()` string
  (confirm this is still true at the comparison site before editing), so the
  error message text is unchanged and the test keeps passing.

## Stage 3 — drop the module dependency

**File:** `go.mod` (and `go.sum`).

- After Stage 1 and 2 compile with no remaining reference to
  `webtyp.com/devwatch` anywhere in the module (`grep -rln "webtyp.com/devwatch" --include="*.go" .` → empty), run `go mod tidy` to remove the
  now-unused `require webtyp.com/devwatch ...` line and its `go.sum`
  entries.
- Do not hand-edit `go.mod`/`go.sum` — let `go mod tidy` compute the correct
  removal.

## Acceptance criteria

1. `grep -rln "webtyp.com/devwatch" --include="*.go" .` → empty.
2. `grep -n "webtyp.com/devwatch" go.mod` → empty.
3. `go build ./...`, `go vet ./...`, `gotest ./...` green.
4. Manually (or via the existing devwatch-side regression test, not this
   repo's): a project run with `webtyp dev` in external mode still shows no
   `InitialRegistration file error` lines for ordinary `.go` files this
   strategy doesn't act on — the behavior this whole effort preserves, now
   via the structural contract instead of a shared import.

| Stage | File | Action |
|---|---|---|
| 0 | — | verify a published `webtyp.com/devwatch` tag has `IsUnsupportedEvent` / `UnsupportedEventError` |
| 1 | `strategies.go` | local `unsupportedFileEventError` type + `ErrUnsupportedEvent` var, drop the `devwatch` import |
| 2 | `handle_file_event_test.go` | drop the `devwatch` import, revert to the local `ErrUnsupportedEvent` |
| 3 | `go.mod`, `go.sum` | `go mod tidy` removes the now-unused `webtyp.com/devwatch` requirement |
