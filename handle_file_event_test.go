package server

import (
	"errors"
	"net"
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
			// Pre-start a fake server listening on h.AppPort so WaitForPortListening succeeds
			ln, err := net.Listen("tcp", ":0")
			if err != nil {
				t.Fatalf("failed to listen: %v", err)
			}
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			h.SetPort(port)
			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
					conn.Close()
				}
			}()
			defer ln.Close()

			compiler.CompileCallCount = 0
			compiler.CompileErr = tt.compileErr
			runner.RunCallCount = 0
			runner.StopCallCount = 0

			err = s.HandleFileEvent("main.go", ".go", "web/main.go", tt.event)

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
