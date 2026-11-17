package server

import "errors"

// MainGenerator writes the generated main files of the app at rootDir for the server the handler runs.
// webtyp.com/appkitc is the one that does; the program that starts the handler (webtyp dev) connects it, because
// server cannot import it: appkitc imports server.
//
// Example: h.SetMainGenerator(func(rootDir string, cfg server.MainConfig) error { return writeMains(rootDir, cfg) })
type MainGenerator func(rootDir string, cfg MainConfig) error

// ErrNoMainGenerator: the handler was asked to write the app's main files and nobody connected a generator.
var ErrNoMainGenerator = errors.New("server: no code generator is connected, so the app's main files were not written; nothing of yours was changed. Connect one with SetMainGenerator before starting the server (webtyp dev does it with webtyp.com/appkitc)")

// SetMainGenerator connects the generator that writes the app's main files.
//
// Example: h.SetMainGenerator(generate)
func (h *ServerHandler) SetMainGenerator(g MainGenerator) *ServerHandler {
	h.mainGenerator = g
	return h
}
