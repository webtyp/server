package server

import (
	"net"
	"path/filepath"
	"sync"
	"time"

	"webtyp.com/fmt/lang"
)

// Startup-decision log lines. Kept as constants so the decision a project took
// is greppable in a captured log. Each is logged with the absolute path that
// drove the decision.
const (
	LogExternalUserMain  = "External mode: user-written server main at"
	LogExternalGenerated = "External mode: config/routes.go present, generated main from"
	LogInternalNoRoutes  = "Internal mode: no config/routes.go and no server main, checked"
)

// StartServer initiates the server using the current strategy (In-Memory or External)
func (h *ServerHandler) StartServer(wg *sync.WaitGroup) {
	if h.needsExternalProcess() {
		if err := h.ensureServerMain(false); err != nil {
			h.log("Failed to generate server main:", err)
			if wg != nil {
				wg.Done()
			}
			return
		}
	}

	h.strategyMu.Lock()
	if h.needsExternalProcess() {
		if h.executionInternal {
			h.executionInternal = false
			h.strategy = newExternalStrategy(h)
		}
		if h.hasHandWrittenMain() {
			h.log(lang.Translate(LogExternalUserMain).String(), filepath.Join(h.AppRootDir, h.serverMainRelPath()))
		} else {
			h.log(lang.Translate(LogExternalGenerated).String(), h.routesManifestPath())
		}
	} else {
		h.log(lang.Translate(LogInternalNoRoutes).String(), h.routesManifestPath())
	}
	isInternal := h.executionInternal
	strategy := h.strategy
	h.strategyMu.Unlock()

	if !isInternal {
		if err := h.BeforeExternalServerStart(); err != nil {
			h.log("BeforeExternalServerStart failed, aborting transition:", err)
			if wg != nil {
				wg.Done()
			}
			return
		}
	}

	if err := strategy.Start(wg); err != nil {
		h.log("StartServer error:", err)
	}
}

// StopServer stops the server and waits for the port to be released.
func (h *ServerHandler) StopServer() error {
	h.log("Stopping server...")
	err := h.strategy.Stop()
	if err != nil {
		h.log("StopServer error:", err)
	}

	// Wait for port to be released (up to 5 seconds)
	addr := ":" + h.Port()
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			h.log("Warning: Port", h.Port(), "still seems occupied after timeout")
			return err
		case <-ticker.C:
			ln, dialErr := net.Listen("tcp", addr)
			if dialErr == nil {
				ln.Close()
				h.log("Port", h.Port(), "is now free")
				return err
			}
		}
	}
}

func (h *ServerHandler) RestartServer() error {
	return h.strategy.Restart()
}

// Restart restarts the server.
// It delegates to the current strategy's Restart method.
func (h *ServerHandler) Restart() error {
	h.strategyMu.RLock()
	defer h.strategyMu.RUnlock()

	if h.strategy != nil {
		h.log("Restarting server strategy:", h.strategy.Name())
		return h.strategy.Restart()
	}
	return nil
}
