---
PLAN: "feat: generated migrate/main.go takes -plan and -drop and reads config/removed.go"
EXECUTOR: jules
REVIEWER: none
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit, push and open the pull request. Never ask whether to
> proceed or finalize — nobody answers, and a session that waits ends without your work.

# server: the generated `migrate/main.go` gets `-plan` and `-drop`; `config/removed.go` is created once

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
```

Environment: `GOPRIVATE=webtyp.com/*,github.com/veltylabs/*`, `GOFLAGS=-mod=mod`. This repo is dev tooling: stdlib
`fmt`, `os`, `text/template` are legitimate here, do NOT replace them with `webtyp.com/fmt`.

## Why

`webtyp.com/migrate` v0.1.0 (published separately) changes its API: `App` takes the app's `[]ddl.Removed` and returns
the leftover columns, and it adds `Drop`, `PlanLine`, `FlagPlan` ("plan") and `FlagDrop` ("drop"). The
`migrate/main.go` this repo generates for every appkit app must call the new API, parse the two flags, and read
`config.Removed` — so the generator also creates `config/removed.go` with an empty list **once**: webtyp dev later
rewrites that file with the developer's answers, and a regeneration must never put the empty list back.

This repo does not import `webtyp.com/migrate`: the code below is template text, and the tests check it as text.

## APIs used (by the generated code, not by this repo)

| Symbol | Exact signature | Tag |
|---|---|---|
| `migrate.App` | `func App(open func(dsn string) (storage.Conn, error), log func(...any), removed []ddl.Removed, steps ...ddl.Step) ([]ddl.Leftover, error)` | `webtyp.com/migrate` v0.1.0 |
| `migrate.Drop` | `func Drop(open func(dsn string) (storage.Conn, error), removed []ddl.Removed, target string) error` | v0.1.0 |
| `migrate.PlanLine` | `func PlanLine(l ddl.Leftover) string` | v0.1.0 |
| `migrate.FlagPlan`, `migrate.FlagDrop` | `const` strings `"plan"`, `"drop"` | v0.1.0 |
| `ddl.Removed` | `struct { Table, Column, MovedTo, Note string }` | `webtyp.com/ddl` v0.0.45 |

Already in this repo, used as is: `writeGenerated`, `IsGenerated`, `dirPerm`, `filePerm`, `GeneratedMigrateDir`,
`GeneratedMainFilename` (`maingen.go`); `writeModule`, `server.GenerateMain`, `server.MainConfig` (tests).

## Stage 1 — test first: `tests/maingen_test.go`

Apply exactly this diff (it extends `TestGenerateMain_App` and adds `TestMainGen_RemovedWrittenOnce`):

```diff
--- tests/maingen_test.go	2026-11-05 15:12:01.111029524 -0300
+++ /tmp/claude-1000/-home-cesar-Dev-Project/e59cecb4-6ed1-4672-87c4-e29eb57f83fb/scratchpad/server-proto/tests/maingen_test.go	2026-11-05 21:57:21.679739704 -0300
@@ -278,7 +278,9 @@
 	if err != nil {
 		t.Fatalf("migrate/main.go not generated: %v", err)
 	}
-	if !strings.Contains(string(mig), "migrate.App(config.App.Server.Database, log.Println, config.Migrations...)") {
+	if !strings.Contains(string(mig), "migrate.App(config.App.Server.Database, log.Println, config.Removed, config.Migrations...)") ||
+		!strings.Contains(string(mig), "migrate.Drop(config.App.Server.Database, config.Removed, *drop)") ||
+		!strings.Contains(string(mig), "flag.Bool(migrate.FlagPlan,") {
 		t.Errorf("migrate main must call migrate.App:\n%s", mig)
 	}
 
@@ -453,3 +455,41 @@
 		t.Errorf("hand-written browser main was overwritten:\n%s", string(dataBrowser))
 	}
 }
+
+// TestMainGen_RemovedWrittenOnce: an app gets config/removed.go with an empty list, and a later
+// generation never overwrites what webtyp dev wrote there.
+func TestMainGen_RemovedWrittenOnce(t *testing.T) {
+	root := t.TempDir()
+	const modulePath = "example.com/app"
+	writeModule(t, root, modulePath)
+	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
+		t.Fatal(err)
+	}
+	appSrc := "package config\n\nimport \"webtyp.com/appkit\"\n\nvar App = appkit.App{}\n"
+	if err := os.WriteFile(filepath.Join(root, "config", "app.go"), []byte(appSrc), 0o644); err != nil {
+		t.Fatal(err)
+	}
+	removed := filepath.Join(root, "config", server.GeneratedRemovedFile)
+
+	if _, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"}); err != nil {
+		t.Fatal(err)
+	}
+	first, err := os.ReadFile(removed)
+	if err != nil {
+		t.Fatalf("config/removed.go not generated: %v", err)
+	}
+	if !strings.Contains(string(first), "var Removed = []ddl.Removed{}") || !server.IsGenerated(string(first)) {
+		t.Fatalf("config/removed.go must hold an empty generated list:\n%s", first)
+	}
+
+	answered := strings.Replace(string(first), "[]ddl.Removed{}", `[]ddl.Removed{{Table: "holiday", Column: "label", MovedTo: "name"}}`, 1)
+	if err := os.WriteFile(removed, []byte(answered), 0o644); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"}); err != nil {
+		t.Fatal(err)
+	}
+	if got, _ := os.ReadFile(removed); string(got) != answered {
+		t.Fatalf("a second generation overwrote config/removed.go:\n%s", got)
+	}
+}
```

Commit (red: `server.GeneratedRemovedFile` does not exist yet).

## Stage 2 — code: `maingen.go`

Apply exactly this diff: the new `migrateTemplateText`, the new `removedTemplateText` and `GeneratedRemovedFile`,
the `writeOnce` call in `GenerateMain` right after `migrate/main.go` is written, and the `writeOnce` function.

```diff
--- maingen.go	2026-11-05 15:49:49.329545199 -0300
+++ /tmp/claude-1000/-home-cesar-Dev-Project/e59cecb4-6ed1-4672-87c4-e29eb57f83fb/scratchpad/server-proto/maingen.go	2026-11-05 21:57:21.679739704 -0300
@@ -140,6 +140,8 @@
 package main
 
 import (
+	"flag"
+	"fmt"
 	"log"
 
 	"webtyp.com/migrate"
@@ -148,12 +150,46 @@
 )
 
 func main() {
-	if err := migrate.App(config.App.Server.Database, log.Println, config.Migrations...); err != nil {
+	plan := flag.Bool(migrate.FlagPlan, false, "print one JSON line per column the models no longer have but that still holds data")
+	drop := flag.String(migrate.FlagDrop, "", "drop one column, data included: table.column")
+	flag.Parse()
+	if *drop != "" {
+		if err := migrate.Drop(config.App.Server.Database, config.Removed, *drop); err != nil {
+			log.Fatal(err)
+		}
+		return
+	}
+	left, err := migrate.App(config.App.Server.Database, log.Println, config.Removed, config.Migrations...)
+	if err != nil {
 		log.Fatal(err)
 	}
+	if *plan {
+		for _, l := range left {
+			fmt.Println(migrate.PlanLine(l))
+		}
+	}
 }
 `
 
+// removedTemplateText is config/removed.go as the generator first writes it: no column removed yet.
+// webtyp dev's DATABASE tab rewrites it with the developer's answers; writeOnce never puts it back.
+const removedTemplateText = `// Code generated by webtyp.com/server (maingen.go). DO NOT EDIT: webtyp dev's DATABASE tab writes it.
+// Why: the columns the models no longer have and where their data went; migrate/main.go reads it, and production repeats each copy without dropping (webtyp.com/ddl Removed).
+// Architecture: https://github.com/webtyp/appkit/blob/main/docs/ARCHITECTURE.md
+// To change what is generated: https://github.com/webtyp/server/blob/main/maingen.go
+
+//go:build !wasm
+
+package config
+
+import "webtyp.com/ddl"
+
+var Removed = []ddl.Removed{}
+`
+
+// GeneratedRemovedFile is config/removed.go: the app's []ddl.Removed, read by migrate/main.go.
+const GeneratedRemovedFile = "removed.go"
+
 const appBrowserTemplateText = `// Code generated by webtyp.com/server (maingen.go) from config/app.go. DO NOT EDIT.
 // Why: the browser (wasm) binary of this app; the «Developer tools» menu comes from webtyp.com/devtools and exists only with -tags dev.
 // Architecture: https://github.com/webtyp/appkit/blob/main/docs/ARCHITECTURE.md
@@ -319,6 +355,9 @@
 		if err := writeGenerated(filepath.Join(rootDir, GeneratedMigrateDir, GeneratedMainFilename), migrateTemplateText, data); err != nil {
 			return "", err
 		}
+		if err := writeOnce(filepath.Join(rootDir, "config", GeneratedRemovedFile), removedTemplateText); err != nil {
+			return "", err
+		}
 	} else if hasRunServer(rootDir) {
 		tmplText = runServerTemplateText
 	} else {
@@ -362,6 +401,20 @@
 	return os.WriteFile(path, buf.Bytes(), filePerm)
 }
 
+// writeOnce writes text to path only when no file is there: config/removed.go holds the developer's answers
+// once webtyp dev writes it, and the generator must never put the empty list back.
+func writeOnce(path, text string) error {
+	if _, err := os.Stat(path); err == nil {
+		return nil
+	} else if !os.IsNotExist(err) {
+		return fmt.Errorf("reading %s: %w", path, err)
+	}
+	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
+		return fmt.Errorf("creating directory: %w", err)
+	}
+	return os.WriteFile(path, []byte(text), filePerm)
+}
+
 // IsGenerated reports whether a file's content was written by webtyp (its header says so), so it may be rewritten;
 // a file without that header is the developer's and is never touched. The one place that knows the header.
 //
```

`writeOnce` is not `writeGenerated`: `writeGenerated` rewrites any file whose header says generated, and
`config/removed.go` carries that header (webtyp dev rewrites it too). `gotest` green. Commit.

## Stage 3 — `docs/TESTS.md`

If the file exists, add one row; if not, create it with header `| Test | Rule |` and this row:
`| [TestMainGen_RemovedWrittenOnce](../tests/maingen_test.go) | config/removed.go is created empty once and never overwritten |`.

## Stage 4 — documentation

- `docs/ARCHITECTURE.md`: in the section that lists the files generated for an appkit app, add `config/removed.go`
  (created once with `var Removed = []ddl.Removed{}`, then owned by webtyp dev's DATABASE tab) and say that
  `migrate/main.go` takes `-plan` (one JSON line per leftover column on stdout) and `-drop table.column`.
- `README.md`: if it lists the generated files, add `config/removed.go` in the same list; keep ≤ 150 lines and the 4
  sections (`Why This Exists`, `Before You Start`, `Step-by-Step Guide`, `Documentation Map`). Do not rewrite other
  sections.

## Delivery check

- `gotest` green. A PR that does not compile, or whose tests could not run, is a failed delivery.
- `grep -n 'config.Removed, config.Migrations' maingen.go` → one line; `grep -n 'func writeOnce' maingen.go` → one line.
- `git diff --stat main` lists `maingen.go`, `tests/maingen_test.go`, `docs/TESTS.md`, `docs/ARCHITECTURE.md`
  (and `README.md` only if it listed the generated files); nothing deleted.
- Never pause to ask permission or report progress. Commit after every stage; never run `git reset`, `git restore`,
  `git checkout -- <file>` or `git stash` on your own work. Stuck for 30 minutes: open the PR anyway listing what fails.

## Stages

| Stage | Files |
|---|---|
| 1 | `tests/maingen_test.go` |
| 2 | `maingen.go` |
| 3 | `docs/TESTS.md` |
| 4 | `docs/ARCHITECTURE.md`, `README.md` |
