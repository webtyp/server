package server_test

import (
	"testing"
	"time"

	"webtyp.com/server"
)

// The webtyp dev TUI reads Value() from the same mutex its log relay takes, so
// a logger that re-enters the handler is what production does. Any log line
// emitted while strategyMu is held deadlocks against it (2026-10-12: webtyp dev
// froze before the first SERVER line, port 8080 never opened).

// reentrantHandler returns a handler whose logger calls Value() on every line,
// with ExitChan pre-filled so a started strategy returns instead of blocking.
func reentrantHandler(t *testing.T) *server.ServerHandler {
	t.Helper()
	exitChan := make(chan bool, 1)
	exitChan <- true

	h := server.New()
	h.SetAppRootDir(t.TempDir())
	h.SetSourceDir("web")
	h.SetPort("0")
	h.SetExitChan(exitChan)
	h.SetDisableGlobalCleanup(true)
	h.SetLogger(func(...any) { _ = h.Value() })
	return h
}

// mustReturn fails the test when fn does not return within d.
func mustReturn(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s deadlocked: the logger re-entered Value() while strategyMu was held", what)
	}
}

func TestStartServerLoggerMayReadValue(t *testing.T) {
	h := reentrantHandler(t)
	mustReturn(t, 5*time.Second, "StartServer", func() { h.StartServer(nil) })
}

func TestRestartLoggerMayReadValue(t *testing.T) {
	h := reentrantHandler(t)
	mustReturn(t, 10*time.Second, "Restart", func() { _ = h.Restart() })
}

func TestCreateTemplateServerLoggerMayReadValue(t *testing.T) {
	h := reentrantHandler(t)
	// The generated main does not compile in an empty temp dir; only the
	// return matters here, not the outcome.
	mustReturn(t, 10*time.Second, "CreateTemplateServer", func() { _ = h.CreateTemplateServer() })
}

func TestSetExternalServerModeLoggerMayReadValue(t *testing.T) {
	h := reentrantHandler(t)
	mustReturn(t, 10*time.Second, "SetExternalServerMode", func() { _ = h.SetExternalServerMode(true) })
}
