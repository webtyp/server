package server_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtyp.com/server"
)

func writeModule(t *testing.T, root, modulePath string) {
	t.Helper()
	src := "module " + modulePath + "\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
}

func writeRoutes(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "routes.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing routes.go: %v", err)
	}
}

func generatedImports(t *testing.T, file string) []string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading generated main: %v", err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("generated main does not parse: %v", err)
	}
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		out = append(out, imp.Path.Value)
	}
	return out
}

// Test 1 — GenerateMain writes a file that parses and imports <modulePath>/config.
func TestGenerateMain_ParsesAndImportsRoutes(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/app"
	writeModule(t, root, modulePath)
	writeRoutes(t, root, "package config\n\nimport \"webtyp.com/router\"\n\nfunc Register(r router.Router) {}\n")

	got, err := server.GenerateMain(root, modulePath, server.MainConfig{
		Port:      "8080",
		PublicDir: "web/public",
		PlainHTTP: false,
	})
	if err != nil {
		t.Fatalf("GenerateMain: %v", err)
	}

	want := filepath.Join(root, server.GeneratedMainDir, server.GeneratedMainFilename)
	if got != want {
		t.Errorf("GenerateMain returned %q, want %q", got, want)
	}

	imports := generatedImports(t, got)
	wantImport := `"` + modulePath + `/config"`
	found := false
	for _, imp := range imports {
		if imp == wantImport {
			found = true
		}
	}
	if !found {
		t.Errorf("generated main does not import %s; imports: %v", wantImport, imports)
	}

	src, _ := os.ReadFile(got)
	if !strings.Contains(string(src), "DO NOT EDIT") {
		t.Errorf("generated main missing the generated-code marker")
	}
}

func TestGenerateMain_PlainHTTPAndNoCARoute(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/app"
	writeModule(t, root, modulePath)
	writeRoutes(t, root, "package routes\n\nimport \"webtyp.com/router\"\n\nfunc Register(r router.Router) {}\n")

	got, err := server.GenerateMain(root, modulePath, server.MainConfig{
		Port:      "8080",
		PublicDir: "web/public",
		PlainHTTP: false,
	})
	if err != nil {
		t.Fatalf("GenerateMain: %v", err)
	}

	src, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("reading generated main: %v", err)
	}

	code := string(src)
	if !strings.Contains(code, "PlainHTTP: false") {
		t.Errorf("generated main does not contain PlainHTTP: false:\n%s", code)
	}
	if strings.Contains(code, "CAPath") {
		t.Errorf("generated main unexpectedly contains CAPath:\n%s", code)
	}
}

// Test 5 — GenerateMain is idempotent: a second run produces the same bytes and
// no error.
func TestGenerateMain_Idempotent(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writeRoutes(t, root, "package routes\n\nimport \"webtyp.com/router\"\n\nfunc Register(r router.Router) {}\n")

	cfg := server.MainConfig{Port: "8080", PublicDir: "web/public"}
	first, err := server.GenerateMain(root, "example.com/app", cfg)
	if err != nil {
		t.Fatalf("GenerateMain #1: %v", err)
	}
	a, _ := os.ReadFile(first)

	if _, err := server.GenerateMain(root, "example.com/app", cfg); err != nil {
		t.Fatalf("GenerateMain #2: %v", err)
	}
	b, _ := os.ReadFile(first)

	if string(a) != string(b) {
		t.Errorf("GenerateMain not idempotent:\n--- first ---\n%s\n--- second ---\n%s", a, b)
	}
}

// Test 2 — HasRoutes is false without routes/routes.go and true with it.
func TestHasRoutes(t *testing.T) {
	root := t.TempDir()
	if server.HasRoutes(root) {
		t.Fatal("HasRoutes = true for an empty project")
	}
	writeRoutes(t, root, "package routes\n")
	if !server.HasRoutes(root) {
		t.Fatal("HasRoutes = false after creating routes/routes.go")
	}
}

func TestGenerateMain_WritesWebServerMainGo(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/app"
	writeModule(t, root, modulePath)
	writeRoutes(t, root, "package config\n\nimport \"webtyp.com/router\"\n\nfunc Register(r router.Router) {}\n")

	got, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080", PublicDir: "web/public"})
	if err != nil {
		t.Fatalf("GenerateMain: %v", err)
	}

	if !strings.HasSuffix(filepath.ToSlash(got), "web/server/main.go") {
		t.Errorf("GenerateMain returned %q, want ending in web/server/main.go", got)
	}

	content, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
	if !strings.Contains(string(content), "Code generated by https://webtyp.com") {
		t.Errorf("generated main missing header")
	}

	oldFile := filepath.Join(root, "web", "server.go")
	if _, err := os.Stat(oldFile); err == nil {
		t.Errorf("GenerateMain created legacy file %q", oldFile)
	}
}

// Test 4 — GenerateMain only ever writes the generated artifact, never a
// hand-written web/server/main.go. The strategy layer's decision to skip generation
// entirely when web/server/main.go exists is covered white-box in the root package
// (TestServerMainSelection); this keeps the black-box assertion compile-free.
func TestEscapeHatch_UserMainNotOverwritten(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writeRoutes(t, root, "package routes\n\nimport \"webtyp.com/router\"\n\nfunc Register(r router.Router) {}\n")

	webDir := filepath.Join(root, "web", "server")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const userMain = "package main // hand written\n\nfunc main() {}\n"
	userFile := filepath.Join(webDir, "main.go")
	if err := os.WriteFile(userFile, []byte(userMain), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := server.GenerateMain(root, "example.com/app", server.MainConfig{Port: "8080", PublicDir: "web/public"}); err != nil {
		t.Fatalf("GenerateMain: %v", err)
	}

	if got, _ := os.ReadFile(userFile); string(got) != userMain {
		t.Errorf("user web/server/main.go was modified:\n%s", got)
	}
}

// TestGenerateMain_App: a project whose config/ declares `var App` (one
// appkit.App) gets server main, migrate main, browser main, and config/migrations.go.
func TestGenerateMain_App(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/app"
	writeModule(t, root, modulePath)

	// Create module1 with migrate
	if err := os.MkdirAll(filepath.Join(root, "mod1", "migrate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mod1", "migrate", "migrate.go"), []byte("package migrate\n\nfunc Migrate() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create module2 without migrate
	if err := os.MkdirAll(filepath.Join(root, "mod2", "module"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create module3 with migrate
	if err := os.MkdirAll(filepath.Join(root, "mod3", "migrate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mod3", "migrate", "migrate.go"), []byte("package migrate\n\nfunc Migrate() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create config/app.go
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	appSrc := `package config

import (
	"webtyp.com/appkit"

	mod1 "example.com/app/mod1/module"
	mod2 "example.com/app/mod2/module"
	mod3 "example.com/app/mod3/module"
)

var App = appkit.App{
	Modules: []appkit.Module{mod1.Module, mod2.Module, mod3.Module},
}
`
	if err := os.WriteFile(filepath.Join(root, "config", "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	if !server.HasApp(root) || !server.HasRoutes(root) {
		t.Fatal("a config/ with `var App` is an app with a server")
	}

	got, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"})
	if err != nil {
		t.Fatal(err)
	}

	// 1. web/server/main.go
	src, _ := os.ReadFile(got)
	if !strings.Contains(string(src), "appkit.Server(config.App, devtools.Server(config.Migrations...)...)") {
		t.Errorf("server main must call appkit.Server with devtools.Server(config.Migrations...):\n%s", src)
	}

	// 2. migrate/main.go
	mig, err := os.ReadFile(filepath.Join(root, server.GeneratedMigrateDir, server.GeneratedMainFilename))
	if err != nil {
		t.Fatalf("migrate/main.go not generated: %v", err)
	}
	if !strings.Contains(string(mig), "migrate.App(config.App.Server.Database, log.Println, config.Removed, config.Migrations...)") ||
		!strings.Contains(string(mig), "migrate.Drop(config.App.Server.Database, config.Removed, *drop)") ||
		!strings.Contains(string(mig), "flag.Bool(migrate.FlagPlan,") {
		t.Errorf("migrate main must call migrate.App:\n%s", mig)
	}

	// 3. web/browser/main.go
	browserMain, err := os.ReadFile(filepath.Join(root, "web", "browser", server.GeneratedMainFilename))
	if err != nil {
		t.Fatalf("web/browser/main.go not generated: %v", err)
	}
	if !strings.Contains(string(browserMain), "//go:build wasm") || !strings.Contains(string(browserMain), "appkit.Browser(config.App, devtools.Menu())") {
		t.Errorf("web/browser/main.go must contain //go:build wasm and appkit.Browser(config.App, devtools.Menu()):\n%s", browserMain)
	}

	// 4. config/migrations.go
	migrationsFile, err := os.ReadFile(filepath.Join(root, "config", "migrations.go"))
	if err != nil {
		t.Fatalf("config/migrations.go not generated: %v", err)
	}
	migStr := string(migrationsFile)
	if !strings.Contains(migStr, "//go:build !wasm") {
		t.Errorf("config/migrations.go missing //go:build !wasm:\n%s", migStr)
	}
	if !strings.Contains(migStr, "package config") {
		t.Errorf("config/migrations.go must be package config:\n%s", migStr)
	}
	if !strings.Contains(migStr, "package config") {
		t.Errorf("config/migrations.go must be package config:\n%s", migStr)
	}
	if !strings.Contains(migStr, `{Name: "mod1", Run: mod1migrate.Migrate}`) {
		t.Errorf("config/migrations.go missing mod1:\n%s", migStr)
	}
	if strings.Contains(migStr, "mod2") {
		t.Errorf("config/migrations.go unexpectedly contains mod2 (which has no migrate package):\n%s", migStr)
	}
	if !strings.Contains(migStr, `{Name: "mod3", Run: mod3migrate.Migrate}`) {
		t.Errorf("config/migrations.go missing mod3:\n%s", migStr)
	}
	idx1 := strings.Index(migStr, `{Name: "mod1"`)
	idx3 := strings.Index(migStr, `{Name: "mod3"`)
	if idx1 == -1 || idx3 == -1 || idx1 >= idx3 {
		t.Errorf("expected mod1 before mod3 in config/migrations.go:\n%s", migStr)
	}
	if _, err := os.Stat(filepath.Join(root, server.GeneratedMigrateDir, "migrations.go")); !os.IsNotExist(err) {
		t.Errorf("migrate/migrations.go must NOT exist")
	}

	// Header checks across all 4 generated files
	files := []struct {
		name    string
		content string
		why     string
	}{
		{"web/server/main.go", string(src), "the server binary of this app; dev tools (database diagram, API explorer) come from webtyp.com/devtools and exist only with -tags dev."},
		{"web/browser/main.go", string(browserMain), "the browser (wasm) binary of this app; the «Developer tools» menu comes from webtyp.com/devtools and exists only with -tags dev."},
		{"migrate/main.go", string(mig), "the migration binary: webtyp dev runs it on start and when a model or go.mod changes; the self-host supervisor runs it before each new version (webtyp.com/migrate)."},
		{"config/migrations.go", migStr, "the migrations of App.Modules in the same order, read by migrate/main.go and by the dev database diagram; build tag !wasm keeps it out of the browser."},
	}
	for _, f := range files {
		if !strings.HasPrefix(f.content, "// Code generated by webtyp.com/server (maingen.go)") {
			t.Errorf("%s must start with '// Code generated by webtyp.com/server (maingen.go)':\n%s", f.name, f.content)
		}
		if !strings.Contains(f.content, "Architecture: https://github.com/webtyp/appkit") {
			t.Errorf("%s must contain 'Architecture: https://github.com/webtyp/appkit':\n%s", f.name, f.content)
		}
		if !strings.Contains(f.content, "// Why: "+f.why) {
			t.Errorf("%s must contain '// Why: %s':\n%s", f.name, f.why, f.content)
		}
	}
	if _, err := os.Stat(filepath.Join(root, server.GeneratedMigrateDir, "migrations.go")); !os.IsNotExist(err) {
		t.Errorf("migrate/migrations.go must NOT exist")
	}
}

func TestGenerateMain_OldHeaderRewritten(t *testing.T) {
	root := t.TempDir()
	modulePath := "example.com/app"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	appSrc := `package config
import "webtyp.com/appkit"
var App = appkit.App{}
`
	if err := os.WriteFile(filepath.Join(root, "config", "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-create files with old header
	oldHeader := "// Code generated by https://webtyp.com. DO NOT EDIT.\npackage main\nold content\n"
	serverDir := filepath.Join(root, "web", "server")
	_ = os.MkdirAll(serverDir, 0o755)
	_ = os.WriteFile(filepath.Join(serverDir, "main.go"), []byte(oldHeader), 0o644)

	browserDir := filepath.Join(root, "web", "browser")
	_ = os.MkdirAll(browserDir, 0o755)
	_ = os.WriteFile(filepath.Join(browserDir, "main.go"), []byte(oldHeader), 0o644)

	migrateDir := filepath.Join(root, "migrate")
	_ = os.MkdirAll(migrateDir, 0o755)
	_ = os.WriteFile(filepath.Join(migrateDir, "main.go"), []byte(oldHeader), 0o644)

	_ = os.WriteFile(filepath.Join(root, "config", "migrations.go"), []byte(oldHeader), 0o644)

	got, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"})
	if err != nil {
		t.Fatalf("GenerateMain: %v", err)
	}

	// Assert rewritten with new header
	checkRewritten := func(path, name string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if !strings.HasPrefix(string(data), "// Code generated by webtyp.com/server (maingen.go)") {
			t.Errorf("%s should be rewritten with new header:\n%s", name, string(data))
		}
		if strings.Contains(string(data), "old content") {
			t.Errorf("%s should not contain old content:\n%s", name, string(data))
		}
	}

	checkRewritten(got, "web/server/main.go")
	checkRewritten(filepath.Join(browserDir, "main.go"), "web/browser/main.go")
	checkRewritten(filepath.Join(migrateDir, "main.go"), "migrate/main.go")
	checkRewritten(filepath.Join(root, "config", "migrations.go"), "config/migrations.go")
}

func TestGenerateMain_HandWrittenNotTouched(t *testing.T) {
	root := t.TempDir()
	modulePath := "example.com/app"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	appSrc := `package config
import "webtyp.com/appkit"
var App = appkit.App{}
`
	if err := os.WriteFile(filepath.Join(root, "config", "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	handWrittenServer := "// Hand-written custom server\npackage main\nfunc main() {}\n"
	serverDir := filepath.Join(root, "web", "server")
	_ = os.MkdirAll(serverDir, 0o755)
	serverFile := filepath.Join(serverDir, "main.go")
	_ = os.WriteFile(serverFile, []byte(handWrittenServer), 0o644)

	handWrittenBrowser := "// Hand-written custom browser\npackage main\nfunc main() {}\n"
	browserDir := filepath.Join(root, "web", "browser")
	_ = os.MkdirAll(browserDir, 0o755)
	browserFile := filepath.Join(browserDir, "main.go")
	_ = os.WriteFile(browserFile, []byte(handWrittenBrowser), 0o644)

	got, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"})
	if err != nil {
		t.Fatalf("GenerateMain: %v", err)
	}

	dataServer, _ := os.ReadFile(got)
	if string(dataServer) != handWrittenServer {
		t.Errorf("hand-written server main was overwritten:\n%s", string(dataServer))
	}

	dataBrowser, _ := os.ReadFile(browserFile)
	if string(dataBrowser) != handWrittenBrowser {
		t.Errorf("hand-written browser main was overwritten:\n%s", string(dataBrowser))
	}
}

// TestMainGen_RemovedWrittenOnce: an app gets config/removed.go with an empty list, and a later
// generation never overwrites what webtyp dev wrote there.
func TestMainGen_RemovedWrittenOnce(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/app"
	writeModule(t, root, modulePath)
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	appSrc := "package config\n\nimport \"webtyp.com/appkit\"\n\nvar App = appkit.App{}\n"
	if err := os.WriteFile(filepath.Join(root, "config", "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	removed := filepath.Join(root, "config", server.GeneratedRemovedFile)

	if _, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(removed)
	if err != nil {
		t.Fatalf("config/removed.go not generated: %v", err)
	}
	if !strings.Contains(string(first), "var Removed = []ddl.Removed{}") || !server.IsGenerated(string(first)) {
		t.Fatalf("config/removed.go must hold an empty generated list:\n%s", first)
	}

	answered := strings.Replace(string(first), "[]ddl.Removed{}", `[]ddl.Removed{{Table: "holiday", Column: "label", MovedTo: "name"}}`, 1)
	if err := os.WriteFile(removed, []byte(answered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := server.GenerateMain(root, modulePath, server.MainConfig{Port: "8080"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(removed); string(got) != answered {
		t.Fatalf("a second generation overwrote config/removed.go:\n%s", got)
	}
}
