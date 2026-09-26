package httpd

import (
	"net/http"
	"os"
	"path/filepath"

	"webtyp.com/router"
)

// A project that serves static pages AND a WASM application keeps them in two
// documents: the pages are public, the application shell is not. webtyp.com/sitec
// emits the shell at <OutputDir>/app/index.html — always there, never anywhere
// else, and never named by the application — so the server can recognise it
// without the application configuring anything. That is the whole point: a
// framework removes decisions, and "is my shell private?" is not a question an
// app should be asked.
//
// These constants MUST keep describing the same document sitec emits (see its
// RouteExtractedAssets, which joins OutputDir + "app" + "index.html" and serves
// it at sitec.ShellPath). This package deliberately does NOT import sitec to
// assert it: sitec is a build-time compiler carrying a TinyGo toolchain, an
// image encoder and a minifier, and dragging that into the runtime server —
// even as a test-only dependency, since tests/ shares this module — would be a
// far worse trade than restating one path. The coupling is instead guarded from
// both ends: sitec's own tests pin where the shell is written, and
// tests/shell_gate_test.go here pins that this is the file the server closes.
const (
	shellDir       = "app"
	shellIndexFile = "index.html"

	// shellFallbackPath is where a request without a session is sent instead of
	// the shell. It is "/" because that is the route the public pages own when a
	// shell exists: sitec moves the shell out of "/" precisely so a page can
	// take it. Not configurable, for the same reason shellDir is not.
	shellFallbackPath = "/"

	// shellPath is where a session on the public landing page is sent instead.
	// Symmetric to shellFallbackPath: it MUST keep describing sitec.ShellPath /
	// auth.PathAfterLogin, for the same reason and under the same guard (this
	// package does not import sitec; tests/shell_gate_test.go pins the value).
	shellPath = "/app/"
)

// isShellDocument reports whether fullPath is the application shell inside the
// directory being served.
func isShellDocument(absDir, fullPath string) bool {
	return samePath(fullPath, filepath.Join(absDir, shellDir, shellIndexFile))
}

// isPublicLanding reports whether fullPath is the root index.html of absDir AND
// a shell exists next to it. Only that combination makes "/" a landing page a
// session should be moved off of: without a shell on disk there is nowhere to
// send anyone, so a pure static site is left alone.
func isPublicLanding(absDir, fullPath string) bool {
	if !samePath(fullPath, filepath.Join(absDir, shellIndexFile)) {
		return false
	}
	_, err := os.Stat(filepath.Join(absDir, shellDir, shellIndexFile))
	return err == nil
}

func samePath(fullPath, target string) bool {
	abs, err := filepath.Abs(fullPath)
	if err != nil {
		return false
	}
	return abs == target
}

// hasIdentity runs the configured Authn middleware over this request and
// reports whether it produced a user.
//
// Authn is the only component that knows how identity travels (cookie, bearer,
// header), so asking it is the only way to answer without duplicating that
// knowledge here. It is invoked exactly as the router invokes it, against a
// handler that does nothing but read the result — so a middleware that renews a
// sliding session, or sets a cookie, behaves the same on this path as on any
// route.
func (s *Server) hasIdentity(w http.ResponseWriter, r *http.Request) bool {
	if s.config.Authn == nil {
		return false
	}
	authenticated := false
	s.config.Authn(func(ctx router.Context) {
		authenticated = ctx.UserID() != ""
	})(&httpContext{w: w, r: r})
	return authenticated
}

// routeBySession answers the request itself when the static fallback is about
// to serve the wrong half of the pre-login split for the caller's session
// state, and reports whether it did.
//
// Two symmetric cases, both against the same Authn and the same cookie:
//
//   - The shell without a session: everything the split achieves is undone by
//     typing the URL, since the binary is a plain file under PublicDir and the
//     static fallback runs after the router has already 404'd, so no route
//     guard has ever seen the request. It also gives an expired session the
//     right landing place instead of a shell that boots into nothing.
//   - The public landing WITH a session: "/" is the landing for a visitor
//     without one, so serving it again strands an authenticated user on a
//     login form the Authn middleware (where DEV_AUTOLOGIN lives) never runs
//     for, because "/" is a plain static file.
//
// With no Authn configured there is no session to consult, so both files are
// served like any other: an application without authentication keeps working.
func (s *Server) routeBySession(w http.ResponseWriter, r *http.Request, absDir, fullPath string) bool {
	if s.config.Authn == nil {
		return false
	}
	switch {
	case isShellDocument(absDir, fullPath):
		if s.hasIdentity(w, r) {
			return false
		}
		http.Redirect(w, r, shellFallbackPath, http.StatusFound)
		return true
	case isPublicLanding(absDir, fullPath):
		if !s.hasIdentity(w, r) {
			return false
		}
		http.Redirect(w, r, shellPath, http.StatusFound)
		return true
	default:
		return false
	}
}
