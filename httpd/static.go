package httpd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"webtyp.com/router"
)

func (s *Server) wrapWithBatteries(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a proxy ResponseWriter to catch 404
		rec := newStatusRecorder(w)

		handler.ServeHTTP(rec, r)

		if rec.notFound && !rec.wroteHeader {
			// The 404 we swallowed left its headers behind: drop them before anyone
			// else writes a response.
			rec.discardSuppressed()

			// Try dynamic fallbacks from PublicDir routes
			s.router.mu.RLock()
			routes := make([]*httpRoute, len(s.router.routes))
			copy(routes, s.router.routes)
			s.router.mu.RUnlock()

			for _, route := range routes {
				if route.info.Dir == "" {
					continue
				}

				prefix := route.info.Path
				if !strings.HasPrefix(r.URL.Path, prefix) {
					continue
				}

				relPath := strings.TrimPrefix(r.URL.Path, prefix)
				fullPath := filepath.Join(route.info.Dir, filepath.Clean(relPath))

				// Path traversal protection
				absDir, err := filepath.Abs(route.info.Dir)
				if err != nil {
					continue
				}
				absPath, err := filepath.Abs(fullPath)
				if err != nil {
					continue
				}
				rel, err := filepath.Rel(absDir, absPath)
				if err != nil || strings.HasPrefix(rel, "..") {
					continue
				}

				info, err := os.Stat(fullPath)
				shouldServe := false
				if err == nil && !info.IsDir() {
					shouldServe = true
				} else if err == nil && info.IsDir() {
					indexPath := filepath.Join(fullPath, "index.html")
					if _, err := os.Stat(indexPath); err == nil {
						fullPath = indexPath
						shouldServe = true
					}
				}

				if shouldServe {
					// The application shell is the one file under PublicDir that is
					// not public — see shell.go.
					if s.denyShellWithoutSession(w, r, absDir, fullPath) {
						return
					}
					// Serve the file directly using the original response writer.
					// wrapWithGlobalBatteries (applied in Handler()) will handle compression.
					http.ServeFile(w, r, fullPath)
					return
				}
			}

			// If still not found, send the original 404
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("404 page not found\n"))
		}
	})
}

func (s *Server) wrapWithGlobalBatteries(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := func(ctx router.Context) {
			hctx := ctx.(*httpContext)
			handler.ServeHTTP(hctx.w, r)
		}
		if s.config.NoCache {
			h = NoCache(h)
		}
		if s.config.Gzip {
			h = Gzip(h)
		}
		h(&httpContext{w: w, r: r})
	})
}

// statusRecorder swallows a 404 so a PublicDir fallback can answer instead.
//
// Swallowing the STATUS is not enough: http.NotFound already wrote its own headers
// into the shared header map ("Content-Type: text/plain", "X-Content-Type-Options:
// nosniff") before calling WriteHeader(404). http.ServeFile then honours the
// Content-Type it finds and never corrects it, so every file in web/public shipped
// as text/plain — the browser printed index.html as text inside a <pre> and ignored
// the stylesheet. A suppressed response must leave nothing behind, so the recorder
// snapshots the headers and restores them when it discards the 404. Deleting the two
// headers we happen to know about would just move the trap one header along.
type statusRecorder struct {
	http.ResponseWriter
	notFound    bool
	wroteHeader bool
	snapshot    http.Header // headers as they were before the wrapped handler ran
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, snapshot: w.Header().Clone()}
}

// discardSuppressed undoes every header written by the response we swallowed.
func (r *statusRecorder) discardSuppressed() {
	h := r.ResponseWriter.Header()
	for k := range h {
		delete(h, k)
	}
	for k, v := range r.snapshot {
		h[k] = v
	}
}

func (r *statusRecorder) WriteHeader(status int) {
	if status == http.StatusNotFound && !r.wroteHeader {
		r.notFound = true
		return
	}
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.notFound && !r.wroteHeader {
		return len(b), nil
	}
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}
