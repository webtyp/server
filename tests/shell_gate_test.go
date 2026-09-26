package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"webtyp.com/router"
	"webtyp.com/server/httpd"
)

// publicDirWithShell lays out what sitec emits for a project that serves static
// pages AND a WASM application: the public page at the root, the shell one level
// down under app/, and the binary next to it.
func publicDirWithShell(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>login</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	appDir := filepath.Join(dir, "app")
	if err := os.Mkdir(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "index.html"), []byte(`<script src="script.js">`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// handlerFor builds a server whose only job is to serve dir statically.
// authenticatedAs != "" makes the Authn middleware produce that user; "" makes
// it produce none, which is how an anonymous visitor arrives. A nil middleware
// (authn == false) is the application that has no authentication at all.
func handlerFor(t *testing.T, dir string, authn bool, authenticatedAs string) http.Handler {
	t.Helper()
	cfg := httpd.Config{PublicDir: dir, Logger: func(...any) {}}
	if authn {
		cfg.Authn = func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				if authenticatedAs != "" {
					ctx.SetUserID(authenticatedAs)
				}
				next(ctx)
			}
		}
	}
	h, err := httpd.New(cfg).Handler()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

// TestShellIsClosedWithoutSession is the guard for the whole pre-login payload
// split: the public page must be reachable by anyone, and the application shell
// — the only document that carries the WASM bootstrap — must not be.
//
// Without this the split is decorative. The shell is a plain file under
// PublicDir, and the static fallback only runs AFTER the router has 404'd, so no
// route guard ever sees the request: typing the URL would hand the binary to an
// anonymous visitor even though the login page ships none.
func TestShellIsClosedWithoutSession(t *testing.T) {
	dir := publicDirWithShell(t)

	t.Run("anonymous is redirected away from the shell", func(t *testing.T) {
		h := handlerFor(t, dir, true, "")
		for _, path := range []string{"/app/", "/app/index.html"} {
			resp := get(t, h, path)
			if resp.StatusCode != http.StatusFound {
				t.Errorf("GET %s: status = %d, want %d", path, resp.StatusCode, http.StatusFound)
			}
			if got := resp.Header.Get("Location"); got != "/" {
				t.Errorf("GET %s: Location = %q, want %q", path, got, "/")
			}
		}
	})

	t.Run("the public page stays public", func(t *testing.T) {
		h := handlerFor(t, dir, true, "")
		if resp := get(t, h, "/"); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /: status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("a session gets the shell", func(t *testing.T) {
		h := handlerFor(t, dir, true, "user-1")
		if resp := get(t, h, "/app/"); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /app/: status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	// The other half of the split: "/" is the landing for a visitor WITHOUT a
	// session. With one, serving it again strands the user on a login form — and
	// the Authn middleware (where DEV_AUTOLOGIN lives) never even runs, because
	// "/" is a plain static file.
	t.Run("a session on the public page goes to the shell", func(t *testing.T) {
		h := handlerFor(t, dir, true, "user-1")
		resp := get(t, h, "/")
		if resp.StatusCode != http.StatusFound {
			t.Errorf("GET /: status = %d, want %d", resp.StatusCode, http.StatusFound)
		}
		if got := resp.Header.Get("Location"); got != "/app/" {
			t.Errorf("GET /: Location = %q, want %q", got, "/app/")
		}
	})

	// A pure static site has no shell to send anyone to.
	t.Run("without a shell the root page stays for a session", func(t *testing.T) {
		siteOnly := t.TempDir()
		if err := os.WriteFile(filepath.Join(siteOnly, "index.html"), []byte("<h1>site</h1>"), 0o644); err != nil {
			t.Fatal(err)
		}
		h := handlerFor(t, siteOnly, true, "user-1")
		if resp := get(t, h, "/"); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /: status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	// An application with no authentication has no session to lack. Gating the
	// shell there would break every static WASM project that never asked for a
	// login — the feature must be invisible to them.
	t.Run("without Authn the shell is an ordinary file", func(t *testing.T) {
		h := handlerFor(t, dir, false, "")
		if resp := get(t, h, "/app/"); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /app/: status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})
}
