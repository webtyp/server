package server

import (
	"errors"
	"testing"
)

type retryCompiler struct {
	calls    int
	firstErr error
}

func (c *retryCompiler) CompileProgram() error {
	c.calls++
	if c.calls == 1 && c.firstErr != nil {
		return c.firstErr
	}
	return nil
}

func (c *retryCompiler) FinalOutputPath() string   { return "" }
func (c *retryCompiler) UnobservedFiles() []string { return nil }

func TestExternalStrategy_CompileWithTidyRetry(t *testing.T) {
	appDir := t.TempDir()
	h := New()
	h.SetAppRootDir(appDir)

	var tidiedDir string
	var tidyCalls int
	h.SetGoModTidier(func(moduleDir, buildOutput string) (bool, error) {
		tidyCalls++
		tidiedDir = moduleDir
		return true, nil
	})

	comp := &retryCompiler{
		firstErr: errors.New("missing go.sum entry for module providing package example.com/x"),
	}
	ext := &externalStrategy{
		handler:    h,
		goCompiler: comp,
		goRun:      &stubRunner{},
	}

	if err := ext.startServer(); err != nil {
		t.Fatalf("startServer failed after tidy retry: %v", err)
	}
	if tidyCalls != 1 {
		t.Fatalf("expected 1 tidy call, got %d", tidyCalls)
	}
	if tidiedDir != appDir {
		t.Errorf("tidiedDir = %q, want %q", tidiedDir, appDir)
	}
	if comp.calls != 2 {
		t.Errorf("CompileProgram calls = %d, want 2", comp.calls)
	}
}
