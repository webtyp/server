---
PLAN: "feat!: the generated browser main calls layoutapp.Browser — the app is the only one that chooses layout"
EXECUTOR: antigravity
REVIEWER: none
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit after every stage and stop. Never ask whether to
> proceed. This plan runs **locally** and is **published as soon as the delivery check passes** (no `replace` anywhere:
> every dependency is a published version). Never run `git reset`, `git restore`, `git checkout -- <file>` or `git stash` on your own work.

# Plan: generated `web/browser/main.go` on `layoutapp.Browser`

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
```
Runs after `webtyp/layout` (plan L3) is published: `layoutapp.Browser` must exist at `webtyp.com/layout@latest`
(`go doc webtyp.com/layout/app Browser`); no `replace` in `go.mod`.

## Why

Decision D16 of `webtyp/docs/ARCHITECTURE_MASTER_PLAN.md`: `appkit.Browser` moved to `webtyp.com/layout/app`
(`layoutapp.Browser`, plan L3 in `webtyp/layout`) so that `appkit` — imported by every module — no longer requires
`layout`. The app's generated browser main is now the one place that imports the chassis.

## APIs used

| Symbol | Signature | From |
|---|---|---|
| `layoutapp.Browser` | `func Browser(app appkit.App, tools app.Tools)` — package `webtyp.com/layout/app`, `//go:build wasm` | plan L3 |
| `devtools.Menu` | `func Menu() app.Tools` (`webtyp.com/devtools`, `//go:build dev`) | unchanged |

## Stage 1 — `maingen.go`

1. `appBrowserTemplateText` (line 194): the import block becomes

```go
import (
	layoutapp "webtyp.com/layout/app"
	"webtyp.com/devtools"

	"{{.ModulePath}}/config"
)

func main() {
	layoutapp.Browser(config.App, devtools.Menu())
}
```
   and the `// Why:` line ends with «the chassis (webtyp.com/layout) is chosen here, by the app, and by no module».
2. `generatedLibraries` (line 411): add `"webtyp.com/layout"` (the template now calls it; the app's `go.mod` must require
   it at `@latest`).

## Stage 2 — tests

`tests/maingen_test.go:296-297`: the expected text becomes `layoutapp.Browser(config.App, devtools.Menu())` and the
message says so. New row in `docs/TESTS.md` if the file lists that test.

## Stage 3 — documentation

`README.md`, `docs/ARCHITECTURE.md`, `docs/DESIGN.md`: every `appkit.Browser(config.App, devtools.Menu())` →
`layoutapp.Browser(config.App, devtools.Menu())` (`grep -rn "appkit.Browser" .` → empty).

## Delivery check

```bash
gotest
grep -rn "appkit.Browser" .   # empty
```

A PR that does not compile, or whose tests could not run, is a failed delivery.

| Stage | Files |
|---|---|
| 1 | `maingen.go` |
| 2 | `tests/maingen_test.go` |
| 3 | `README.md`, `docs/*.md` |
