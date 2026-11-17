package server_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"webtyp.com/command"
	"webtyp.com/server"
)

const (
	toolDirective = "tool " + server.GeneratorToolPath + "\n"
	getToolCall   = "get -tool " + server.GeneratorToolPath + "@latest"
)

// goRecorder holds every `go` command the handler ran instead of running it (no network in tests).
type goRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *goRecorder) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// fakeGo records `go` commands without running them; a command whose arguments contain failing exits with an error.
// Other commands run.
func fakeGo(t *testing.T, failing string) *goRecorder {
	t.Helper()
	rec := &goRecorder{}
	orig := command.Exec
	command.Exec = func(name string, args ...string) *exec.Cmd {
		if name != "go" {
			return orig(name, args...)
		}
		joined := strings.Join(args, " ")
		rec.mu.Lock()
		rec.calls = append(rec.calls, joined)
		rec.mu.Unlock()
		if failing != "" && strings.Contains(joined, failing) {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { command.Exec = orig })
	return rec
}

func writeModule(t *testing.T, root, extra string) {
	t.Helper()
	src := "module example.com/app\n\ngo 1.25\n\n" + extra
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
}

func hasCall(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// An app whose go.mod has no tool line gets it first, then the generator runs with the flags the handler holds.
func TestRunGenerator_AddsTheToolThenRunsIt(t *testing.T) {
	rec := fakeGo(t, "")
	root := t.TempDir()
	writeModule(t, root, "")

	cfg := server.MainConfig{Port: "9090", PublicDir: "web/public", PlainHTTP: true}
	if err := server.RunGenerator(root, cfg); err != nil {
		t.Fatalf("RunGenerator: %v", err)
	}
	want := []string{getToolCall, "tool appkitc -port=9090 -public-dir=web/public -plain-http"}
	got := rec.Calls()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("go commands: want %q, got %q", want, got)
	}
}

// The tool line is added once: when the app already pins appkitc, only the generator runs and without -plain-http.
func TestRunGenerator_DoesNotAddTheToolTwice(t *testing.T) {
	rec := fakeGo(t, "")
	root := t.TempDir()
	writeModule(t, root, toolDirective)

	if err := server.RunGenerator(root, server.MainConfig{Port: "8080", PublicDir: "web/public"}); err != nil {
		t.Fatalf("RunGenerator: %v", err)
	}
	got := rec.Calls()
	if len(got) != 1 || got[0] != "tool appkitc -port=8080 -public-dir=web/public" {
		t.Fatalf("go commands: want only the generator, got %q", got)
	}
}

func TestRunGenerator_ReportsAGeneratorFailure(t *testing.T) {
	fakeGo(t, "tool appkitc")
	root := t.TempDir()
	writeModule(t, root, toolDirective)

	err := server.RunGenerator(root, server.MainConfig{Port: "8080"})
	if !errors.Is(err, server.ErrGeneratorFailed) {
		t.Fatalf("want ErrGeneratorFailed, got %v", err)
	}
}

func TestRunGenerator_ReportsAToolThatCannotBeAdded(t *testing.T) {
	fakeGo(t, "get -tool")
	root := t.TempDir()
	writeModule(t, root, "")

	err := server.RunGenerator(root, server.MainConfig{Port: "8080"})
	if !errors.Is(err, server.ErrGeneratorNotInstalled) {
		t.Fatalf("want ErrGeneratorNotInstalled, got %v", err)
	}
}

func TestRunGenerator_RefusesAFolderWithoutGoMod(t *testing.T) {
	fakeGo(t, "")
	err := server.RunGenerator(t.TempDir(), server.MainConfig{Port: "8080"})
	if !errors.Is(err, server.ErrGeneratorNotInstalled) {
		t.Fatalf("want ErrGeneratorNotInstalled, got %v", err)
	}
}

func TestUpgradeGenerator_AsksForTheNewestAppkitc(t *testing.T) {
	rec := fakeGo(t, "")
	if err := server.UpgradeGenerator(t.TempDir()); err != nil {
		t.Fatalf("UpgradeGenerator: %v", err)
	}
	got := rec.Calls()
	if len(got) != 1 || got[0] != "get "+server.GeneratorModulePath+"@latest" {
		t.Fatalf("go commands: want one go get of appkitc, got %q", got)
	}
}

// HasRoutes is false without config/routes.go and true with it.
func TestHasRoutes(t *testing.T) {
	root := t.TempDir()
	if server.HasRoutes(root) {
		t.Fatal("HasRoutes = true for an empty project")
	}
	writeRoutesFile(t, root)
	if !server.HasRoutes(root) {
		t.Fatal("HasRoutes = false after creating config/routes.go")
	}
}

func TestReadModulePath_ReadsTheModuleLine(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "")
	if got := server.ReadModulePath(root); got != "example.com/app" {
		t.Fatalf("ReadModulePath = %q, want example.com/app", got)
	}
}

func TestHasRunServer_NeedsTheFunctionInConfig(t *testing.T) {
	root := t.TempDir()
	if server.HasRunServer(root) {
		t.Fatal("HasRunServer = true for an empty project")
	}
	dir := filepath.Join(root, server.ConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte("package config\n\nfunc RunServer() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !server.HasRunServer(root) {
		t.Fatal("HasRunServer = false with func RunServer in config/")
	}
}
