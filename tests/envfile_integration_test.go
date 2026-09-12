//go:build integration
// +build integration

package server_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"webtyp.com/server"
)

// Integration test: black-box verification that the project's root .env is
// visible to a hand-written external server main (the escape hatch), even
// though the compiled binary's working directory is the output directory,
// not the project root. This is the exact defect DOTENV_VISIBILITY_MASTER_PLAN.md
// diagnoses: gorun.Config.EnvFile (wired in strategies.go's newExternalStrategy)
// must forward the root .env into the child's real OS environment.
func TestExternalStrategy_RootEnvFileVisibleToHandWrittenMain(t *testing.T) {
	// enabled: run automatically

	tmp := t.TempDir()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getting free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	var logBuf bytes.Buffer
	var mu sync.Mutex
	logger := func(messages ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(&logBuf, messages...)
	}

	// Project root .env — the file the external process must see.
	envPath := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envPath, []byte("ENV_PROBE_KEY=hello_from_root_env\n"), 0644); err != nil {
		t.Fatalf("writing .env: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module temp\n\ngo 1.20\n"), 0644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	webDir := filepath.Join(tmp, "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatalf("creating web dir: %v", err)
	}

	// A hand-written main (the escape hatch) that reads ENV_PROBE_KEY the
	// same way an app's own web/server.go would — via os.Getenv, no
	// webtyp.com/env dependency needed to prove the environment reaches the
	// child process at all — and serves /health so the test can poll like
	// the other external-mode integration test in this package.
	mainSrc := fmt.Sprintf(`package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	fmt.Println("ENV_PROBE=" + os.Getenv("ENV_PROBE_KEY"))
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	http.ListenAndServe(":%d", nil)
}
`, port)
	if err := os.WriteFile(filepath.Join(webDir, "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatalf("writing hand-written main: %v", err)
	}

	h := server.New()
	h.SetAppRootDir(tmp)
	h.SetPort(fmt.Sprintf("%d", port))
	h.SetHTTPS(false)
	h.SetExitChan(make(chan bool, 1))
	h.SetDisableGlobalCleanup(true)
	h.SetLogger(logger)

	if err := h.SetExternalServerMode(true); err != nil {
		t.Fatalf("failed to set external server mode: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				select {
				case h.ExitChan <- true:
				default:
				}
				h.StopServer()

				mu.Lock()
				got := logBuf.String()
				mu.Unlock()
				if !bytes.Contains([]byte(got), []byte("ENV_PROBE=hello_from_root_env")) {
					t.Fatalf("child process did not see ENV_PROBE_KEY from the project's root .env; logs: %s", got)
				}
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	mu.Lock()
	logs := logBuf.String()
	mu.Unlock()
	t.Fatalf("hand-written external server did not respond on /health within timeout; logs: %s", logs)
}
