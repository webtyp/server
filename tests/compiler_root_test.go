package server_test

import (
	"os"
	"path/filepath"
	"testing"

	"webtyp.com/server"
)

// TestExternalServerCompilerUsesAppRootDir ensures the external compiler sets
// its working directory to AppRootDir so multi-module builds resolve correctly
// even when the daemon runs in another directory.
func TestExternalServerCompilerUsesAppRootDir(t *testing.T) {
	tmp := t.TempDir()

	gomod := `module externalapp
go 1.22
`
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(tmp, "web", "server")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainSrc := `package main
func main() {}
`
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}

	h := server.New()
	h.SetAppRootDir(tmp)
	h.SetSourceDir("web/server")
	h.SetOutputDir("web/server")
	h.SetMainInputFile("main.go")
	h.SetExitChan(make(chan bool, 1))

	if err := h.SetExternalServerMode(true); err != nil {
		t.Fatalf("SetExternalServerMode(true) failed: %v", err)
	}
	defer h.StopServer()
}
