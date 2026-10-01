package server

import (
	"os"
	"path/filepath"

	"webtyp.com/router/routescan"
)

// Startup decision, decided by one file with no configuration:
//
//	routes/routes.go present  -> generate, compile and run the server main
//	web/server.go present     -> compile and run the user's own main (escape hatch)
//	neither                   -> internal in-memory asset server, nothing compiled
//
// The escape hatch always wins: the tool never overwrites a main the user wrote.

// hasHandWrittenMain reports whether the user supplied their own server entry
// point at <AppRootDir>/<SourceDir>/<MainInputFile> (canonically web/server.go).
func (h *ServerHandler) hasHandWrittenMain() bool {
	_, err := os.Stat(filepath.Join(h.AppRootDir, h.SourceDir, h.mainFileExternalServer))
	return err == nil
}

// usesGeneratedMain reports whether this project's server process is the
// generated artifact: it declares routes/routes.go and has no hand-written main.
func (h *ServerHandler) usesGeneratedMain() bool {
	return HasRoutes(h.AppRootDir) && !h.hasHandWrittenMain()
}

// needsExternalProcess reports whether StartServer must compile and run a child
// process (the generated main, or the user's own) rather than serving assets
// from memory.
func (h *ServerHandler) needsExternalProcess() bool {
	return h.hasHandWrittenMain() || h.usesGeneratedMain()
}

// serverMainRelPath is the compiler input, relative to AppRootDir: the user's
// own main when present, otherwise the generated artifact.
func (h *ServerHandler) serverMainRelPath() string {
	if h.hasHandWrittenMain() {
		return filepath.Join(h.SourceDir, h.mainFileExternalServer)
	}
	return filepath.Join(GeneratedMainDir, GeneratedMainFilename)
}

// routesManifestPath is the absolute path of routes/routes.go for this project.
func (h *ServerHandler) routesManifestPath() string {
	return filepath.Join(h.AppRootDir, routescan.DefaultFile)
}

// ensureServerMain writes the generated server main when it is needed and not
// already on disk, and records .build/ in the project .gitignore through the
// SetGitIgnoreAdd hook.
//
// It never overwrites a hand-written main (the escape hatch). With force set —
// used by the explicit "switch to external" entry points, where the caller has
// declared intent — it generates even without routes/routes.go; otherwise it
// only generates when the project declares routes.
func (h *ServerHandler) ensureServerMain(force bool) error {
	if h.hasHandWrittenMain() {
		return nil
	}
	if !force && !HasRoutes(h.AppRootDir) {
		return nil
	}
	cfg := MainConfig{
		Port:      h.Port(),
		PublicDir: h.PublicDir,
		PlainHTTP: !h.Https,
	}
	if _, err := GenerateMain(h.AppRootDir, readModulePath(h.AppRootDir), cfg); err != nil {
		return err
	}
	if h.GitIgnoreAdd != nil {
		_ = h.GitIgnoreAdd(BuildDirGitIgnore)
	}
	return nil
}
