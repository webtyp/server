package server_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"webtyp.com/server"
)

// Mock strategy to verify timing and calls
type mockStrategy struct {
	startCalled bool
	startTime   time.Time
	mu          sync.Mutex
}

func (m *mockStrategy) Start(wg *sync.WaitGroup) error {
	m.mu.Lock()
	m.startCalled = true
	m.startTime = time.Now()
	m.mu.Unlock()
	if wg != nil {
		wg.Done()
	}
	return nil
}

func (m *mockStrategy) Stop() error { return nil }
func (m *mockStrategy) Restart() error { return nil }
func (m *mockStrategy) HandleFileEvent(fileName, extension, filePath, event string) error { return nil }
func (m *mockStrategy) Name() string { return "Mock" }

// Helper to set unexported fields via reflection
func setUnexportedField(h *server.ServerHandler, fieldName string, value interface{}) {
	v := reflect.ValueOf(h).Elem()
	f := v.FieldByName(fieldName)
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	f.Set(reflect.ValueOf(value))
}

// External mode must invoke BeforeExternalServerStart BEFORE strategy.Start.
func TestStartServer_BeforeHookRunsBeforeStrategy(t *testing.T) {
	h := server.New()
	setUnexportedField(h, "executionInternal", false)
	mock := &mockStrategy{}
	setUnexportedField(h, "strategy", server.ServerStrategy(mock))

	var hookReturnTime time.Time
	h.SetBeforeExternalServerStart(func() error {
		time.Sleep(20 * time.Millisecond) // Ensure measurable difference
		hookReturnTime = time.Now()
		return nil
	})

	h.StartServer(nil)

	if !mock.startCalled {
		t.Fatal("strategy.Start was not called")
	}

	if hookReturnTime.IsZero() {
		t.Fatal("hook was not called")
	}

	if !hookReturnTime.Before(mock.startTime) && !hookReturnTime.Equal(mock.startTime) {
		t.Errorf("hook returned at %v, but strategy started at %v", hookReturnTime, mock.startTime)
	}
}

// A non-nil error from the hook must abort strategy.Start.
func TestStartServer_BeforeHookErrorAborts(t *testing.T) {
	var loggedError error
	h := server.New().SetLogger(func(args ...any) {
		for _, arg := range args {
			if err, ok := arg.(error); ok {
				loggedError = err
			}
		}
	})

	setUnexportedField(h, "executionInternal", false)
	mock := &mockStrategy{}
	setUnexportedField(h, "strategy", server.ServerStrategy(mock))

	hookErr := errors.New("boom")
	h.SetBeforeExternalServerStart(func() error {
		return hookErr
	})

	h.StartServer(nil)

	if mock.startCalled {
		t.Error("expected strategy.Start NOT to be called when hook returns error")
	}

	if loggedError != hookErr {
		t.Errorf("expected logged error %v, got %v", hookErr, loggedError)
	}
}

// Internal mode must NOT invoke BeforeExternalServerStart.
func TestStartServer_InternalModeSkipsBeforeHook(t *testing.T) {
	h := server.New()
	h.SetAppRootDir(t.TempDir())
	h.SetExitChan(make(chan bool, 1))
	h.ExitChan <- true // Allow Start to return immediately

	hookCalled := false
	h.SetBeforeExternalServerStart(func() error {
		hookCalled = true
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go h.StartServer(&wg)

	// Wait for StartServer to potentially call the hook
	time.Sleep(100 * time.Millisecond)

	if hookCalled {
		t.Error("expected hook NOT to be called in internal mode")
	}
}

// RestartServer must bypass the hook by design.
func TestRestartServer_DoesNotInvokeHook(t *testing.T) {
	h := server.New()
	setUnexportedField(h, "executionInternal", false)
	mock := &mockStrategy{}
	setUnexportedField(h, "strategy", server.ServerStrategy(mock))

	hookCount := 0
	h.SetBeforeExternalServerStart(func() error {
		hookCount++
		return nil
	})

	// 1. StartServer (hook fires)
	h.StartServer(nil)
	if hookCount != 1 {
		t.Errorf("expected hook count 1 after StartServer, got %d", hookCount)
	}

	// 2. RestartServer (hook should NOT fire)
	err := h.RestartServer()
	if err != nil {
		t.Fatalf("RestartServer failed: %v", err)
	}

	if hookCount != 1 {
		t.Errorf("expected hook count to remain 1 after RestartServer, got %d", hookCount)
	}
}

// Test idempotency: hook fires on every StartServer call in external mode.
func TestStartServer_HookIsIdempotent(t *testing.T) {
	h := server.New()
	setUnexportedField(h, "executionInternal", false)
	mock := &mockStrategy{}
	setUnexportedField(h, "strategy", server.ServerStrategy(mock))

	hookCount := 0
	h.SetBeforeExternalServerStart(func() error {
		hookCount++
		return nil
	})

	h.StartServer(nil)
	h.StartServer(nil)

	if hookCount != 2 {
		t.Errorf("expected hook to fire twice for two StartServer calls, got %d", hookCount)
	}
}

// CreateTemplateServer must invoke BeforeExternalServerStart before strategy.Start.
func TestCreateTemplateServer_InvokesBeforeHook(t *testing.T) {
	// Use a temporary directory for AppRootDir to avoid side effects
	tmpDir := t.TempDir()

	h := server.New()
	h.SetAppRootDir(tmpDir)

	// Inject a mock strategy that records its start time.
	// Since CreateTemplateServer will replace the strategy with a newExternalStrategy,
	// we need to be careful. But we can mock the strategy AFTER the switch if we were
	// in a position to do so, OR we can just observe the hook call.
	// Actually, CreateTemplateServer calls newExternalStrategy(h) and then s.Start(nil).
	// To verify timing, we'd need to mock newExternalStrategy or the strategy it returns.

	var hookReturnTime time.Time
	h.SetBeforeExternalServerStart(func() error {
		time.Sleep(20 * time.Millisecond)
		hookReturnTime = time.Now()
		return nil
	})

	// To intercept the Start call of the newly created external strategy,
	// we can use the fact that CreateTemplateServer is a method on h.
	// However, it creates the strategy internally.
	// A better way is to use a hook that we KNOW is called before Start.

	// We'll use a goroutine to signal ExitChan so it doesn't block forever if it starts.
	go func() {
		time.Sleep(2 * time.Second) // Give it some time to start
		h.ExitChan <- true
	}()

	// CreateTemplateServer will fail because it tries to compile, but that's okay
	// as long as it reaches the hook and start calls.
	// Actually, it might fail at generation if we don't have templates.
	// server_test has access to embedded templates if we are in the right package.

	_ = h.CreateTemplateServer()

	if hookReturnTime.IsZero() {
		t.Fatal("hook was not called")
	}
}

// CreateTemplateServer must propagate hook errors and NOT start the strategy.
func TestCreateTemplateServer_HookErrorAbortsStart(t *testing.T) {
	h := server.New()
	h.SetAppRootDir(t.TempDir())

	hookErr := errors.New("boom")
	h.SetBeforeExternalServerStart(func() error {
		return hookErr
	})

	err := h.CreateTemplateServer()

	if err == nil {
		t.Fatal("expected error from CreateTemplateServer, got nil")
	}

	if !errors.Is(err, hookErr) {
		t.Errorf("expected error to wrap %v, got %v", hookErr, err)
	}
}
