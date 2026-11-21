package server

import (
	"fmt"
	"net"
	"net/http"
	"testing"
)

type stubCompiler struct{}

func (stubCompiler) CompileProgram() error     { return nil }
func (stubCompiler) FinalOutputPath() string   { return "" }
func (stubCompiler) UnobservedFiles() []string { return nil }

type stubRunner struct {
	onRun func() error
}

func (r *stubRunner) RunProgram() error {
	if r.onRun != nil {
		return r.onRun()
	}
	return nil
}
func (r *stubRunner) StopProgram() error { return nil }

// TestExternalRestart_KeepsOperationIDThroughStartup proves that Restart keeps
// defaultServerOpID active while the child server starts (so "Starting server on
// port ..." collapses onto the same live line as "Restarting server..." and
// "Server running on port ..."), then clears OperationID once Restart returns
// so subsequent runtime logs remain discrete.
func TestExternalRestart_KeepsOperationIDThroughStartup(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go srv.Serve(ln)
	defer srv.Close()

	port := fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)

	type logEntry struct {
		opID string
		msg  string
	}
	var entries []logEntry

	h := New()
	h.SetHTTPS(false)
	h.SetPort(port)
	h.SetLogger(func(args ...any) {
		entries = append(entries, logEntry{
			opID: h.GetLastOperationID(),
			msg:  fmt.Sprint(args...),
		})
	})

	ext := &externalStrategy{
		handler:    h,
		goCompiler: stubCompiler{},
		goRun: &stubRunner{
			onRun: func() error {
				// Simulate the child server printing its startup banner via gorun.
				h.log("Starting server on port", port)
				return nil
			},
		},
	}

	if err := ext.Restart(); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 log entries (open, child startup, close), got %d: %+v", len(entries), entries)
	}
	for i, e := range entries {
		if e.opID != defaultServerOpID {
			t.Errorf("entry[%d] (%q) opID = %q, want %q — child startup banner must share restart OperationID", i, e.msg, e.opID, defaultServerOpID)
		}
	}

	if got := h.GetLastOperationID(); got != "" {
		t.Errorf("after Restart returns, OperationID = %q, want empty so runtime logs stay discrete", got)
	}
}
