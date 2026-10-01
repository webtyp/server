package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"webtyp.com/pwa"
	"webtyp.com/server/httpd"
)

// Files under PublicDir carry the Cache-Control of the one contract the compiler follows
// (webtyp.com/pwa): hashed names immutable, large artifacts never HTTP-cached (and still
// resumable with Range), fixed names revalidated.
func TestPublicDir_CacheControlFollowsPWAContract(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("style.3f9a1c2b.css", "body{}")
	write("sw.js", "self")
	write("artifacts/decider.q4.wtypw", "0123456789")

	h, err := httpd.New(httpd.Config{PublicDir: dir, TLS: httpd.TLSConfig{PlainHTTP: true}}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, rng string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	cases := []struct{ path, want string }{
		{"/style.3f9a1c2b.css", pwa.CacheImmutable},
		{"/sw.js", pwa.CacheRevalidate},
		{"/artifacts/decider.q4.wtypw", pwa.CacheNoStore},
	}
	for _, c := range cases {
		rec := get(c.path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", c.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.want {
			t.Errorf("GET %s Cache-Control = %q, want %q", c.path, got, c.want)
		}
	}

	rec := get("/artifacts/decider.q4.wtypw", "bytes=2-4")
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" {
		t.Errorf("Range on an artifact = %d %q, want 206 \"234\"", rec.Code, rec.Body.String())
	}
}

// In development NoCache forbids caching altogether, hashed names included.
func TestPublicDir_NoCacheWinsInDevelopment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "style.3f9a1c2b.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := httpd.New(httpd.Config{PublicDir: dir, NoCache: true, TLS: httpd.TLSConfig{PlainHTTP: true}}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/style.3f9a1c2b.css", nil))
	if got := rec.Header().Get("Cache-Control"); got == pwa.CacheImmutable {
		t.Errorf("dev server sent %q for a hashed name; NoCache must win", got)
	}
}
