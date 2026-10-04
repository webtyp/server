package server

import (
	"errors"
	"path/filepath"
	"sync"

	"webtyp.com/router"
)

// NOTE: circular dep prevents importing app — mirror the interface locally.
var _ serverInterface = (*ServerHandler)(nil)

type serverInterface interface {
	StartServer(wg *sync.WaitGroup)
	StopServer() error
	RestartServer() error
	NewFileEvent(fileName, extension, filePath, event string) error
	UnobservedFiles() []string
	SupportedExtensions() []string
	Name() string
	Label() string
	Value() string
	Change(v string)
	RefreshUI()
	MainInputFileRelativePath() string
	RegisterRoutes(fn func(router.Router))
}

// Store defines the minimal interface for persistent storage
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// UI defines the minimal interface for UI interaction
type UI interface {
	RefreshUI()
}

const (
	EnvKeyServerPort  = "SERVER_PORT"
	EnvKeyServerHttps = "SERVER_HTTPS"

	execModeInternal = "internal"
	execModeExternal = "external"
)

// TestMode is a global flag to indicate the server is running in a test environment.
// This is used to disable aggressive cleanups and other disruptive behaviors.
var TestMode bool

type noopStore struct{}

func (noopStore) Get(string) (string, error) { return "", nil }
func (noopStore) Set(string, string) error   { return nil }

type noopUI struct{}

func (noopUI) RefreshUI() {}

type ServerHandler struct {
	*Config
	mainFileExternalServer string // eg: main.server.go
	strategy               ServerStrategy
	strategyMu             sync.RWMutex // protects strategy field
	portMu                 sync.RWMutex // protects AppPort (read from goroutines started by strategies)
	executionInternal      bool         // true = embedded server (internal), false = external process
	onLog                  func(message ...any)

	// Internal route list
	routes []func(router.Router)
}

type Config struct {
	AppRootDir                  string               // e.g., /home/user/project (application root directory)
	SourceDir                   string               // directory location of main.go e.g., src/cmd/appserver (relative to AppRootDir)
	OutputDir                   string               // compilation and execution directory e.g., deploy/appserver (relative to AppRootDir)
	PublicDir                   string               // default public dir for generated server (e.g., src/web/public)
	MainInputFile               string               // main input file name (default: "main.go", can be "server.go", etc.)
	ArgumentsForCompilingServer func() []string      // e.g., []string{"-X 'main.version=v1.0.0'"}
	ArgumentsToRunServer        func() []string      // e.g., []string{"dev"}
	AppPort                     string               // e.g., 8080
	Https                       bool                 // true if HTTPS is enabled
	DisableGlobalCleanup        bool                 // If true, disables global cleanup in gorun during restarts
	Logger                      func(message ...any) // Logger function
	ExitChan                    chan bool            // Global channel to signal shutdown
	Store                       Store                    // Persistent storage for modes
	UI                          UI                       // UI for refresh notifications
	BeforeExternalServerStart   func() error             // Called synchronously before external strategy starts
	GitIgnoreAdd                func(entry string) error // Callback to add entries to .gitignore
}

// New creates a new ServerHandler with default configuration.
func New() *ServerHandler {
	c := &Config{
		AppRootDir:                  ".",
		SourceDir:                   "web",
		OutputDir:                   "web",
		PublicDir:                   "web/public",
		MainInputFile:               "main.go",
		AppPort:                     "8080",
		Https:                       true, // HTTPS-by-default in dev; SetHTTPS(false) is an explicit opt-out
		Logger:                      nil,
		ExitChan:                    make(chan bool),
		ArgumentsForCompilingServer: func() []string { return nil },
		ArgumentsToRunServer:        func() []string { return nil },
		BeforeExternalServerStart:   func() error { return nil },
		GitIgnoreAdd:                func(string) error { return nil },
	}

	sh := &ServerHandler{
		Config:                 c,
		mainFileExternalServer: c.MainInputFile,
		onLog:                  c.Logger,
		routes:                 make([]func(router.Router), 0),
	}

	sh.Store = noopStore{}
	sh.UI = noopUI{}

	// Default to Internal Execution Mode
	sh.executionInternal = true
	sh.strategy = newInternalStrategy(sh)

	return sh
}

// SetAppRootDir sets the application root directory
func (h *ServerHandler) SetAppRootDir(dir string) {
	h.Config.AppRootDir = dir
}

// SetSourceDir sets the source directory relative to AppRootDir
func (h *ServerHandler) SetSourceDir(dir string) {
	h.Config.SourceDir = dir
}

// SetOutputDir sets the output directory relative to AppRootDir
func (h *ServerHandler) SetOutputDir(dir string) {
	h.Config.OutputDir = dir
}

// SetPublicDir sets the public directory
func (h *ServerHandler) SetPublicDir(dir string) *ServerHandler {
	h.Config.PublicDir = dir
	return h
}

// SetMainInputFile sets the main input file name
func (h *ServerHandler) SetMainInputFile(name string) {
	h.Config.MainInputFile = name
	h.mainFileExternalServer = name
}

// SetPort sets the server port
func (h *ServerHandler) SetPort(port string) {
	h.portMu.Lock()
	h.Config.AppPort = port
	h.portMu.Unlock()
}

// Port returns the current server port. Safe for concurrent use with SetPort.
func (h *ServerHandler) Port() string {
	h.portMu.RLock()
	defer h.portMu.RUnlock()
	return h.Config.AppPort
}

// SetHTTPS enables or disables HTTPS. The default is true; passing false is an
// explicit, deliberate opt-out — never a silent default, never read from a
// gitignored file.
func (h *ServerHandler) SetHTTPS(enabled bool) *ServerHandler {
	h.Config.Https = enabled
	return h
}

// SetLogger sets the logger function
func (h *ServerHandler) SetLogger(fn func(...any)) *ServerHandler {
	h.Config.Logger = fn
	h.onLog = fn
	return h
}

// SetExitChan sets the exit channel
func (h *ServerHandler) SetExitChan(ch chan bool) *ServerHandler {
	h.Config.ExitChan = ch
	return h
}

// SetStore sets the persistent store
func (h *ServerHandler) SetStore(s Store) *ServerHandler {
	if s != nil {
		h.Config.Store = s
		h.Store = s
	}
	return h
}

// SetUI sets the UI interface
func (h *ServerHandler) SetUI(ui UI) *ServerHandler {
	if ui != nil {
		h.Config.UI = ui
		h.UI = ui
	}
	return h
}

// SetBeforeExternalServerStart registers a function invoked synchronously
// BEFORE strategy.Start in every external-mode StartServer call. Returning
// a non-nil error aborts the transition: strategy.Start is NOT invoked and
// the error is logged.
//
// Idempotency: the hook fires on every external-mode StartServer, not only
// on the internal→external transition (external mode is sticky: once the
// server file decides it, StartServer keeps the external strategy).
// Implementations must be safe to invoke N times.
//
// RestartServer does NOT invoke this hook.
func (h *ServerHandler) SetBeforeExternalServerStart(fn func() error) *ServerHandler {
	h.Config.BeforeExternalServerStart = fn
	return h
}

// SetGitIgnoreAdd sets the callback to add entries to .gitignore
func (h *ServerHandler) SetGitIgnoreAdd(fn func(string) error) *ServerHandler {
	h.Config.GitIgnoreAdd = fn
	return h
}

// SetCompileArgs sets the arguments for compiling the server
func (h *ServerHandler) SetCompileArgs(fn func() []string) {
	h.Config.ArgumentsForCompilingServer = fn
}

// SetRunArgs sets the arguments for running the server
func (h *ServerHandler) SetRunArgs(fn func() []string) {
	h.Config.ArgumentsToRunServer = fn
}

// SetDisableGlobalCleanup enables or disables global cleanup
func (h *ServerHandler) SetDisableGlobalCleanup(disable bool) {
	h.Config.DisableGlobalCleanup = disable
}

// RegisterRoutes appends fn to the internal route list.
// Call before StartServer.
func (h *ServerHandler) RegisterRoutes(fn func(router.Router)) {
	h.routes = append(h.routes, fn)
}

func (h *ServerHandler) log(messages ...any) {
	if h.onLog != nil {
		h.onLog(messages...)
	}
}

// MainInputFileRelativePath returns the path relative to AppRootDir (e.g., "src/cmd/appserver/main.go")
func (h *ServerHandler) MainInputFileRelativePath() string {
	return filepath.Join(h.SourceDir, h.mainFileExternalServer)
}

func (h *ServerHandler) SupportedExtensions() []string {
	return []string{".go"}
}

// UnobservedFiles returns the list of files that should not be tracked by file watchers
func (h *ServerHandler) UnobservedFiles() []string {
	h.strategyMu.RLock()
	defer h.strategyMu.RUnlock()

	if !h.executionInternal {
		if ext, ok := h.strategy.(*externalStrategy); ok {
			return ext.goCompiler.UnobservedFiles()
		}
	}
	return []string{}
}

// SetExternalServerMode switches between Internal and External server strategies.
// When switching to External, it also:
// 1. Generates server template files if they don't exist
// 2. Compiles the server
// 3. Starts the external process
func (h *ServerHandler) SetExternalServerMode(external bool) error {
	h.strategyMu.Lock()
	defer h.strategyMu.Unlock()

	if external {
		if h.executionInternal {
			h.log("Switching to External Server Mode...")

			if err := h.ensureServerMain(true); err != nil {
				return err
			}

			// Stop current internal strategy
			if err := h.strategy.Stop(); err != nil {
				h.log("Warning stopping internal server:", err)
			}

			waitForPortFree(h.Port())

			h.executionInternal = false
			h.strategy = newExternalStrategy(h)

			go h.strategy.Start(nil)
		}
	} else {
		if !h.executionInternal {
			return errors.New("cannot switch back to Internal execution mode once External mode is active")
		}
	}
	return nil
}
