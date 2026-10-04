package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"webtyp.com/server"
)

func newTestHandler(t *testing.T, sourceDir, outputDir, appRootDir string) *server.ServerHandler {
	t.Helper()
	h := server.New()
	h.SetAppRootDir(appRootDir)
	h.SetSourceDir(sourceDir)
	h.SetOutputDir(outputDir)
	h.SetPort("9090")
	h.SetHTTPS(false) // these tests probe plain HTTP; TLS is covered in httpd/ and https_test.go
	h.SetExitChan(make(chan bool, 10))
	h.SetLogger(safeTestLogger(t))
	return h
}

// safeTestLogger forwards to t.Log during the test and becomes a no-op once the
// test completes. Several coverage tests hand the server a logger and then
// return while a detached restart/compile goroutine is still running; calling
// t.Log from that goroutine after the test finished is a data race with the
// testing framework. The guard closes that window without losing log output
// during the test itself.
func safeTestLogger(t *testing.T) func(...any) {
	t.Helper()
	var mu sync.Mutex
	finished := false
	t.Cleanup(func() {
		mu.Lock()
		finished = true
		mu.Unlock()
	})
	return func(args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if !finished {
			t.Log(args...)
		}
	}
}

// writeRoutesFile creates a minimal routes/routes.go under root so that
// HasRoutes(root) is true and the generated-main path is exercised.
func writeRoutesFile(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	const src = "package config\n\nimport \"webtyp.com/router\"\n\nfunc Register(r router.Router) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "routes.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing routes.go: %v", err)
	}
}

func TestGenerateCreatesFile(t *testing.T) {
	tmp := t.TempDir()
	sourceDir := "web"
	outputDir := "web"
	h := newTestHandler(t, sourceDir, outputDir, tmp)
	writeRoutesFile(t, tmp)

	target := filepath.Join(tmp, server.GeneratedMainDir, server.GeneratedMainFilename)
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("expected no existing file at %s", target)
	}

	close(h.ExitChan)

	if err := h.CreateTemplateServer(); err != nil {
		t.Logf("CreateTemplateServer returned error: %v", err)
	}

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "package main") {
		t.Errorf("generated file missing package main")
	}
	if !strings.Contains(content, "9090") {
		t.Errorf("generated file missing substituted AppPort (9090)")
	}
	if !strings.Contains(content, `httpd.New`) {
		t.Errorf("generated file missing httpd.New")
	}
	if !strings.Contains(content, `web/public`) {
		t.Errorf("generated file missing default public dir (web/public)")
	}
}

func TestGenerateDoesNotOverwriteExistingServerFile(t *testing.T) {
	tmp := t.TempDir()
	sourceDir := "web"
	outputDir := "web"
	fullSourcePath := filepath.Join(tmp, sourceDir)
	if err := os.MkdirAll(fullSourcePath, 0755); err != nil {
		t.Fatalf("creating source dir: %v", err)
	}
	h := newTestHandler(t, sourceDir, outputDir, tmp)
	target := filepath.Join(fullSourcePath, "server.go")

	orig := "__ORIGINAL__"
	if err := os.WriteFile(target, []byte(orig), 0644); err != nil {
		t.Fatalf("writing original file: %v", err)
	}

	close(h.ExitChan)

	if err := h.CreateTemplateServer(); err != nil {
		t.Logf("CreateTemplateServer returned error: %v", err)
	}

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading file after generate: %v", err)
	}
	if string(b) != orig {
		t.Fatalf("file was overwritten, expected original content")
	}
}
