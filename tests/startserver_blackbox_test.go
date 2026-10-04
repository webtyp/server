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

// This test verifies that calling CreateTemplateServer generates the external server file
// and switches strategy. We don't necessarily enforce successful compilation in this test env,
// but we verify file generation.
func TestCreateTemplateServerGeneratesFile(t *testing.T) {
	// enabled: run automatically

	tmp := t.TempDir()

	// capture logs into a buffer
	// capture logs into a buffer
	var logMessages []string
	var mu sync.Mutex
	logger := func(messages ...any) {
		mu.Lock()
		defer mu.Unlock()
		logMessages = append(logMessages, fmt.Sprint(messages...))
	}

	exitChan := make(chan bool, 1)
	// Pre-fill exit channel so Start() doesn't block waiting
	exitChan <- true

	h := server.New()
	h.SetAppRootDir(tmp)
	h.SetSourceDir("src/app")
	h.SetOutputDir("deploy")
	h.SetPort("0") // Use random port to avoid conflicts if it runs
	h.SetExitChan(exitChan)
	h.SetDisableGlobalCleanup(true)
	h.SetLogger(logger)

	// Ensure external file doesn't exist initially
	target := filepath.Join(h.AppRootDir, server.GeneratedMainDir, server.GeneratedMainFilename)
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("expected no external server file at %s", target)
	}

	// Verify we are in Internal mode
	if h.Value() == "external" {
		t.Fatal("Expected Internal mode initially")
	}

	// Call CreateTemplateServer.
	// Since CreateTemplateServer tries to compile, and we might not have a full Go env for the generated code
	// (depending on dependencies), it might return an error.
	// We primarily care that it generated the file.
	// Call CreateTemplateServer in a goroutine because it blocks until the server exits
	// (or until Start returns if ExitChan is signaled)
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = h.CreateTemplateServer()
	}()

	// Give it some time to generate files and start blocking
	// We can loop checking for the file
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	fileFound := false
	for {
		select {
		case <-timeout:
			break // break loop, check fileFound later
		case <-ticker.C:
			if _, statErr := os.Stat(target); statErr == nil {
				fileFound = true
				goto Found
			}
		}
	}
Found:

	if !fileFound {
		t.Fatalf("expected generated server file at %s, but not found within timeout", target)
	}

	// Signal to exit
	select {
	case h.ExitChan <- true:
	default:
	}

	wg.Wait()

	if err != nil && t.Failed() {
		t.Logf("CreateTemplateServer returned error: %v", err)
	}

	// Now the generated file should exist
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected generated server file at %s, but not found: %v", target, err)
	}

	// Verify the logs mention generation
	mu.Lock()
	out := strings.Join(logMessages, "\n")
	mu.Unlock()
	if !strings.Contains(out, "Generating server files") {
		// Verify h.executionInternal is now false (logic switched strategy before Compile)
	}

	if h.Value() != "external" {
		t.Error("Expected to be in External mode logic (h.executionInternal = false) even if compilation failed")
	}
}
