package server_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"webtyp.com/server"
)

// genRecorder is a generator that records how the handler called it instead of writing files.
type genRecorder struct {
	mu    sync.Mutex
	calls []server.MainConfig
	roots []string
}

func (r *genRecorder) Generate(rootDir string, cfg server.MainConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roots = append(r.roots, rootDir)
	r.calls = append(r.calls, cfg)
	return nil
}

func (r *genRecorder) Calls() []server.MainConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]server.MainConfig(nil), r.calls...)
}

// useRecorder connects a recording generator to h.
func useRecorder(h *server.ServerHandler) *genRecorder {
	rec := &genRecorder{}
	h.SetMainGenerator(rec.Generate)
	return rec
}

func writeModule(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
}

// The handler asks the connected generator for the app's main files with the port, the public folder and the protocol it holds.
func TestCreateTemplateServer_AsksTheConnectedGenerator(t *testing.T) {
	tmp := t.TempDir()
	h := newTestHandler(t, "web/server", "web/server", tmp)
	rec := useRecorder(h)
	writeRoutesFile(t, tmp)
	close(h.ExitChan)

	if err := h.CreateTemplateServer(); err != nil {
		t.Logf("CreateTemplateServer returned error: %v", err)
	}
	calls := rec.Calls()
	want := server.MainConfig{Port: "9090", PublicDir: "web/public", PlainHTTP: true}
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("generator calls: want one with %+v, got %+v", want, calls)
	}
}

// Without a connected generator the handler fails loudly instead of leaving the app without its main files.
func TestCreateTemplateServer_FailsWithoutAGenerator(t *testing.T) {
	tmp := t.TempDir()
	h := newBareHandler(t, "web/server", "web/server", tmp)
	writeRoutesFile(t, tmp)
	close(h.ExitChan)

	err := h.CreateTemplateServer()
	if !errors.Is(err, server.ErrNoMainGenerator) {
		t.Fatalf("want ErrNoMainGenerator, got %v", err)
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
	writeModule(t, root)
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
