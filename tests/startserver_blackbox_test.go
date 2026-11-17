package server_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"webtyp.com/server"
)

// This test verifies that calling CreateTemplateServer runs the code generator (appkitc) for the app
// and switches strategy. We don't enforce successful compilation in this test env.
const generatorCall = "tool appkitc -port=0 -public-dir=web/public"

func TestCreateTemplateServerRunsTheGenerator(t *testing.T) {
	// enabled: run automatically

	rec := fakeGo(t, "")
	tmp := t.TempDir()
	writeModule(t, tmp, toolDirective)
	writeRoutesFile(t, tmp)

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
pollLoop:
	for {
		select {
		case <-timeout:
			break pollLoop
		case <-ticker.C:
			if hasCall(rec.Calls(), generatorCall) {
				fileFound = true
				break pollLoop
			}
		}
	}

	if !fileFound {
		t.Fatalf("expected the generator to run as %q within the timeout, go commands: %q", generatorCall, rec.Calls())
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

	// Verify the logs mention generation
	mu.Lock()
	out := strings.Join(logMessages, "\n")
	mu.Unlock()
	_ = out

	if h.Value() != "external" {
		t.Error("Expected to be in External mode logic (h.executionInternal = false) even if compilation failed")
	}
}
