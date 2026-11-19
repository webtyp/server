package server

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	gobuildmock "webtyp.com/gobuild/mock"
	gorunmock "webtyp.com/gorun/mock"
)

func TestHandleFileEvent_Fast(t *testing.T) {
	TestMode = true
	h := New()
	h.SetPort("0")                    // Use port 0 to avoid conflicts
	h.SetHTTPS(false)                 // fake listener below speaks plain HTTP
	h.SetLogger(func(args ...any) {}) // No-op logger

	compiler := &gobuildmock.FakeCompiler{}
	runner := &gorunmock.FakeRunner{}

	s := &externalStrategy{
		handler:    h,
		goCompiler: compiler,
		goRun:      runner,
	}

	tests := []struct {
		name             string
		event            string
		compileErr       error
		expectCompile    int
		expectRun        int
		expectErr        error
		expectRestartMsg bool
	}{
		{
			name:          "write event - success",
			event:         "write",
			expectCompile: 1, // Restart calls CompileProgram once
			expectRun:     1,
			expectErr:     nil,
		},
		{
			name:          "create event - success",
			event:         "create",
			expectCompile: 1,
			expectRun:     1,
			expectErr:     nil,
		},
		{
			name:          "rename event - success",
			event:         "rename",
			expectCompile: 1,
			expectRun:     1,
			expectErr:     nil,
		},
		{
			name:          "remove event - unsupported",
			event:         "remove",
			expectCompile: 0,
			expectRun:     0,
			expectErr:     ErrUnsupportedEvent,
		},
		{
			name:          "write event - compile error",
			event:         "write",
			compileErr:    errors.New("compile failed"),
			expectCompile: 1,
			expectRun:     0,
			expectErr:     errors.New("compile failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A real HTTP server on h.AppPort so WaitForPortListening succeeds. It reads each request before it
			// answers: a fake that writes on accept answers before the client asks, and net/http logs it as
			// "Unsolicited response received on idle HTTP channel".
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
			defer srv.Close()
			_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
			h.SetPort(port)

			compiler.CompileCallCount = 0
			compiler.CompileErr = tt.compileErr
			runner.RunCallCount = 0
			runner.StopCallCount = 0

			err := s.HandleFileEvent("main.go", ".go", "web/main.go", tt.event)

			if tt.expectErr != nil {
				if err == nil || err.Error() != tt.expectErr.Error() {
					t.Errorf("expected error %v, got %v", tt.expectErr, err)
				}
			} else if err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if compiler.CompileCallCount != tt.expectCompile {
				t.Errorf("expected %d compile calls, got %d", tt.expectCompile, compiler.CompileCallCount)
			}

			if runner.RunCallCount != tt.expectRun {
				t.Errorf("expected %d run calls, got %d", tt.expectRun, runner.RunCallCount)
			}
		})
	}
}
