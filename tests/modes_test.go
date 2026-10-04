package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"webtyp.com/server"
)

type mockStore struct {
	data map[string]string
}

func (m *mockStore) Get(key string) (string, error) { return m.data[key], nil }
func (m *mockStore) Set(key, value string) error    { m.data[key] = value; return nil }

func isExternal(val string) bool {
	return val == "external"
}

func TestServerHandler_Value(t *testing.T) {
	tmpData := t.TempDir()
	exitChan := make(chan bool, 1)
	exitChan <- true

	h := server.New()
	h.SetAppRootDir(tmpData)
	h.SetSourceDir("src")
	h.SetExitChan(exitChan)

	// Case 1: Default (Internal)
	if got := h.Value(); got != "internal" {
		t.Errorf("default Value() = %q, want internal", got)
	}

	// Case 2: Modified state
	h.SetExternalServerMode(true)
	if got := h.Value(); got != "external" {
		t.Errorf("modified Value() = %q, want external", got)
	}
}

func TestServerHandler_Options(t *testing.T) {
	h := server.New()
	opts := h.Options()
	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}

	if _, ok := opts[0]["internal"]; !ok {
		t.Errorf("expected first option to be internal, got %v", opts[0])
	}
	if _, ok := opts[1]["external"]; !ok {
		t.Errorf("expected second option to be external, got %v", opts[1])
	}
}

func TestServerHandler_Change(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantExec bool // true = internal, false = external
	}{
		{"external", "external", false},
		{"internal", "internal", true}, // Technically no-op if internal already
		{"bogus", "bogus", true},       // Should not change mode
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fresh handler for each subtest to avoid race conditions
			tmpData := t.TempDir()
			exitChan := make(chan bool, 1)
			exitChan <- true

			db := &mockStore{data: make(map[string]string)}
			h := server.New()
			h.SetAppRootDir(tmpData)
			h.SetSourceDir("src")
			h.SetExitChan(exitChan)
			h.SetStore(db)

			h.Change(tt.input)

			isExt := isExternal(h.Value())
			isInt := !isExt
			if isInt != tt.wantExec {
				t.Errorf("executionInternal = %v, want %v", isInt, tt.wantExec)
			}
		})
	}
}

func TestSetExternalServerMode_SwitchesToExternal(t *testing.T) {
	tmpData := t.TempDir()
	exitChan := make(chan bool, 1)
	exitChan <- true

	h := server.New()
	h.SetAppRootDir(tmpData)
	h.SetSourceDir("src")
	h.SetOutputDir("bin")
	h.SetExitChan(exitChan)

	if isExternal(h.Value()) {
		t.Fatal("expected initial executionInternal to be true (internal)")
	}

	if err := h.SetExternalServerMode(true); err != nil {
		t.Logf("SetExternalServerMode returned error: %v", err)
	}

	if !isExternal(h.Value()) {
		t.Fatal("expected executionInternal to be false (external) after switching to external")
	}

	// Verify file was generated
	targetPath := filepath.Join(tmpData, server.GeneratedMainDir, server.GeneratedMainFilename)
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		t.Fatalf("expected server file to be generated at %s", targetPath)
	}
}

func TestSetExternalServerMode_PreventsSwitchingToInternal(t *testing.T) {
	tmpData := t.TempDir()
	exitChan := make(chan bool, 1)
	exitChan <- true

	h := server.New()
	h.SetAppRootDir(tmpData)
	h.SetSourceDir("src")
	h.SetExitChan(exitChan)

	// 1. Switch to External
	if err := h.SetExternalServerMode(true); err != nil {
		t.Logf("switch to external error: %v", err)
	}

	// 2. Attempt switch back to Internal should fail
	if err := h.SetExternalServerMode(false); err == nil {
		t.Fatal("expected error preventing switch back to internal, got nil")
	} else if !strings.Contains(err.Error(), "cannot switch back") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 3. Verify state remains External
	if !isExternal(h.Value()) {
		t.Fatal("expected executionInternal to remain false (external) after failed switch")
	}
}

func TestGitIgnoreAdd_CalledOnExternalMode(t *testing.T) {
	tmpData := t.TempDir()
	exitChan := make(chan bool, 1)
	exitChan <- true

	var mu sync.Mutex
	var entries []string
	gitIgnoreAdd := func(entry string) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, entry)
		return nil
	}

	h := server.New()
	h.SetAppRootDir(tmpData)
	h.SetSourceDir("web")
	h.SetOutputDir("web")
	h.SetMainInputFile("server.go")
	h.SetExitChan(exitChan)
	h.SetGitIgnoreAdd(gitIgnoreAdd)

	// Switch to external mode - this generates the main and starts a process,
	// each of which registers a .gitignore entry through the hook.
	if err := h.SetExternalServerMode(true); err != nil {
		t.Logf("SetExternalServerMode error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	has := func(want string) bool {
		for _, e := range entries {
			if e == want {
				return true
			}
		}
		return false
	}
	if wantBinary := filepath.Join("web", "server"); !has(wantBinary) {
		t.Errorf("GitIgnoreAdd never received binary path %q; got %v", wantBinary, entries)
	}
}

func TestModeSwitchWhileRunning_DoesNotHang(t *testing.T) {
	tmpData := t.TempDir()
	exitChan := make(chan bool) // Should be unbuffered to test hang if we wanted, but here we test switching

	var logs []string
	var logsMu sync.Mutex
	logger := func(msgs ...any) {
		logsMu.Lock()
		defer logsMu.Unlock()
		for _, m := range msgs {
			logs = append(logs, fmt.Sprintf("%v", m))
		}
	}

	db := &mockStore{data: make(map[string]string)}
	h := server.New()
	h.SetAppRootDir(tmpData)
	h.SetSourceDir("src")
	h.SetOutputDir("bin")
	h.SetPort("18080") // Use non-standard port to avoid conflicts
	h.SetExitChan(exitChan)
	h.SetDisableGlobalCleanup(true)
	h.SetLogger(logger)
	h.SetStore(db)

	// Start the internal server in a goroutine (like app does)
	var wg sync.WaitGroup
	wg.Add(1)
	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		h.StartServer(&wg)
	}()
	<-serverStarted

	// Give server time to start
	time.Sleep(200 * time.Millisecond)

	// Call Change() to switch to external mode
	done := make(chan struct{})
	go func() {
		h.Change("external")
		close(done)
	}()

	select {
	case <-done:
		// Change completed
		t.Log("Change() completed")
	case <-time.After(5 * time.Second):
		t.Fatal("Change() hung - mode switch blocked")
	}

	if t.Failed() {
		logsMu.Lock()
		for _, log := range logs {
			t.Logf("LOG: %s", log)
		}
		logsMu.Unlock()
	}

	// Verify mode changed
	if !isExternal(h.Value()) {
		t.Error("expected executionInternal to be false after Change")
	}

	// Cleanup - signal exit
	close(exitChan)

	// Wait for server goroutine to finish (with timeout)
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		t.Log("Server shutdown cleanly")
	case <-time.After(3 * time.Second):
		t.Log("Server shutdown timed out (expected in some cases)")
	}
}

func TestStartServer_DetectsExistingServerFile(t *testing.T) {
	// Setup
	tmpData := t.TempDir()

	// Create a dummy server file
	svrDir := filepath.Join(tmpData, "web")
	if err := os.MkdirAll(svrDir, 0755); err != nil {
		t.Fatal(err)
	}
	svrFile := filepath.Join(svrDir, "server.go")
	if err := os.WriteFile(svrFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	// Initialize handler
	exitChan := make(chan bool, 1)
	exitChan <- true // Prevent blocking on StartServer

	h := server.New()
	h.SetAppRootDir(tmpData)
	h.SetSourceDir("web")
	h.SetMainInputFile("server.go")
	h.SetExitChan(exitChan)

	// Expectation: Initially internal
	if isExternal(h.Value()) {
		t.Error("New() should initialize in internal mode")
	}

	// Call StartServer to trigger auto-detection
	var wg sync.WaitGroup
	wg.Add(1)
	h.StartServer(&wg)

	if !isExternal(h.Value()) {
		t.Errorf("StartServer() should switch to external mode when server file exists, but got %s", h.Value())
	}
}
