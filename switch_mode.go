package server

import (
	"errors"
)

// CreateTemplateServer switches from Internal to External mode.
// It generates the server files (if not present), compiles, and runs them.
// This implements the transition from "Internal" to "External" mode.
func (h *ServerHandler) CreateTemplateServer() error {
	// strategyMu guards the fields only: onLog, Stop and newExternalStrategy
	// (which logs) all run outside it, or a logger that re-enters the handler
	// deadlocks.
	h.strategyMu.RLock()
	isInternal := h.executionInternal
	current := h.strategy
	h.strategyMu.RUnlock()

	if !isInternal {
		h.log("Server is already in external mode.")
		return nil
	}

	h.log("Stopping Internal Server...")
	// Stop the current internal server
	if err := current.Stop(); err != nil {
		return errors.Join(errors.New("failed to stop internal server"), err)
	}

	h.log("Generating server files...")
	if err := h.ensureServerMain(true); err != nil {
		return errors.Join(errors.New("failed to generate server main"), err)
	}

	h.log("Switching to External Process Strategy...")
	s := newExternalStrategy(h)
	h.strategyMu.Lock()
	if !h.executionInternal {
		// A concurrent transition won; its strategy is the one running.
		h.strategyMu.Unlock()
		return nil
	}
	h.executionInternal = false
	h.strategy = s
	h.strategyMu.Unlock()

	h.log("Starting External Server...")

	// Gate the external strategy on the same hook that StartServer uses.
	// CreateTemplateServer is an external-mode transition entrypoint; the hook
	// must fire here too, by the same idempotency contract documented in
	// SetBeforeExternalServerStart.
	if err := h.BeforeExternalServerStart(); err != nil {
		return errors.Join(errors.New("BeforeExternalServerStart failed"), err)
	}

	// Start the new external server (compiles and runs)
	// We pass nil for wg because this is a runtime transition, not application startup
	if err := s.Start(nil); err != nil {
		// If start fails, should we try to revert?
		// For now, return the error.
		return errors.Join(errors.New("failed to start external server"), err)
	}

	h.log("Server successfully switched to External mode.")
	return nil
}
