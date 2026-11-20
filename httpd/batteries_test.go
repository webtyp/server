package httpd

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtyp.com/model"
	"webtyp.com/router"
)

func TestHTTPDBatteries(t *testing.T) {
	tmpDir := t.TempDir()
	publicDir := filepath.Join(tmpDir, "public")
	os.MkdirAll(publicDir, 0755)
	os.WriteFile(filepath.Join(publicDir, "test.txt"), []byte("hello world"), 0644)

	conf := Config{
		PublicDir:      publicDir,
		Gzip:           true,
		NoCache:        true,
		Health:         true,
		RoutesEndpoint: true,
	}

	s := New(conf)

	// Register a route
	s.Router().Get("/api/hello", func(ctx router.Context) {
		ctx.Write([]byte("hi"))
	}).Public()

	// Add RBAC
	s.Router().Get("/api/secret", func(ctx router.Context) {
		ctx.Write([]byte("secret"))
	}).Requires(model.Resource("admin"), model.Read)

	s.config.Authn = func(next router.HandlerFunc) router.HandlerFunc {
		return func(ctx router.Context) {
			ctx.SetUserID(ctx.GetHeader("X-User"))
			next(ctx)
		}
	}
	s.config.Authorize = func(userID string, resource model.Resource, action model.Action) bool {
		return userID == "admin"
	}

	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}

	// 1. Test Health
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Errorf("Health failed: %d %s", w.Code, w.Body.String())
	}

	// 2. Test Static + NoCache
	req = httptest.NewRequest("GET", "/test.txt", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "hello world" {
		t.Errorf("Static failed: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-cache") {
		t.Errorf("NoCache failed, got header: %s", w.Header().Get("Cache-Control"))
	}

	// 3. Test Gzip — and verify it strips any pre-set Content-Length (e.g. from http.ServeFile)
	// so HTTP/2 never sees Content-Length (= uncompressed size) disagree with the compressed body,
	// nor cuts off a compressible response before lazyGzipWriter.Close() flushes the gzip trailer.
	largePayload := strings.Repeat("hello world — compressible static file payload\n", 200)
	os.WriteFile(filepath.Join(publicDir, "large.txt"), []byte(largePayload), 0644)
	req = httptest.NewRequest("GET", "/large.txt", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Gzip failed, got header: %s", w.Header().Get("Content-Encoding"))
	}
	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Gzip must delete pre-set Content-Length (got %q for a %d-byte compressed body), or HTTP/2 truncates/aborts the stream", cl, w.Body.Len())
	}

	// 4. Test RBAC - Allow
	req = httptest.NewRequest("GET", "/api/secret", nil)
	req.Header.Set("X-User", "admin")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "secret" {
		t.Errorf("RBAC Allow failed: %d %s", w.Code, w.Body.String())
	}

	// 5. Test RBAC - Deny
	req = httptest.NewRequest("GET", "/api/secret", nil)
	req.Header.Set("X-User", "guest")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("RBAC Deny failed: %d", w.Code)
	}

	// 6. Test Routes Endpoint
	req = httptest.NewRequest("GET", "/_routes", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Routes endpoint failed: %d", w.Code)
	}
	// The posture must be READABLE. This endpoint exists so an operator can see what is
	// exposed, and it used to lie: Access is a numeric type whose ZERO value is
	// AccessGuarded, so the most protected route serialized as `"Access":0` — which reads
	// as "nothing declared", the opposite of the truth. Assert the words, not the count:
	// the old test decoded into a struct and swallowed the error, so it never saw this.
	body := w.Body.String()
	for _, want := range []string{`"access":"guarded"`, `"access":"public"`} {
		if !strings.Contains(body, want) {
			t.Errorf("routes endpoint does not report %s\nbody: %s", want, body)
		}
	}
	if strings.Contains(body, `"access":0`) || strings.Contains(body, `"Access":0`) {
		t.Errorf("the most protected route is reported as 0, which reads as 'unset'\nbody: %s", body)
	}
}
