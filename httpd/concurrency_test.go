package httpd

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"webtyp.com/model"
	"webtyp.com/router"
)

func TestConcurrentRequests_NoRace(t *testing.T) {
	tmpDir := t.TempDir()
	publicDir := filepath.Join(tmpDir, "public")
	os.MkdirAll(publicDir, 0755)
	os.WriteFile(filepath.Join(publicDir, "test.txt"), []byte("static"), 0644)

	// Find free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ln.Addr().String(), ":")[1]
	ln.Close()

	cfg := Config{
		Port:           port,
		PublicDir:      publicDir,
		Health:         true,
		Gzip:           true,
		RoutesEndpoint: true,
		TLS:            TLSConfig{PlainHTTP: true},
		Authn: func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				ctx.SetUserID(ctx.GetHeader("X-User"))
				next(ctx)
			}
		},
		Authorize: func(userID string, resource model.Resource, action model.Action) bool {
			return userID == "admin"
		},
	}
	s := New(cfg)

	s.Router().Get("/api/hello", func(ctx router.Context) {
		ctx.Write([]byte("hi"))
	}).Public()
	s.Router().Get("/api/secret", func(ctx router.Context) {
		ctx.Write([]byte("secret"))
	}).Requires(model.Resource("top"), model.Read)

	go func() {
		s.ListenAndServe()
	}()

	time.Sleep(500 * time.Millisecond)

	var wg sync.WaitGroup
	n := 200
	// DisableKeepAlives: 200 concurrent requests hammering a local server can
	// race a pooled idle connection against the server closing/reusing it,
	// which net/http logs as "Unsolicited response received on idle HTTP
	// channel". A fresh connection per request avoids that race entirely.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	urls := []struct {
		url  string
		user string
		code int
	}{
		{"http://127.0.0.1:" + port + "/health", "", 200},
		{"http://127.0.0.1:" + port + "/test.txt", "", 200},
		{"http://127.0.0.1:" + port + "/api/hello", "", 200},
		{"http://127.0.0.1:" + port + "/api/secret", "admin", 200},
		{"http://127.0.0.1:" + port + "/api/secret", "guest", 403},
		{"http://127.0.0.1:" + port + "/_routes", "", 200},
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			item := urls[i%len(urls)]
			req, _ := http.NewRequest("GET", item.url, nil)
			if item.user != "" {
				req.Header.Set("X-User", item.user)
			}
			if i%2 == 0 {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("Request %d failed: %v", i, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != item.code {
				t.Errorf("Request %d to %s expected %d, got %d", i, item.url, item.code, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentRouteRegistration(t *testing.T) {
	mux := http.NewServeMux()
	r := NewRouter(mux)

	var wg sync.WaitGroup
	n := 100

	// Concurrent writes
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := strings.Repeat("a", i+1)
			r.Get("/"+path, func(ctx router.Context) {})
		}(i)
	}

	// Concurrent reads
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Routes()
		}()
	}

	wg.Wait()

	routes := r.Routes()
	if len(routes) != n {
		t.Errorf("Expected %d routes, got %d", n, len(routes))
	}
}

func TestConcurrentAuthorizerCalls(t *testing.T) {
	// Find free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ln.Addr().String(), ":")[1]
	ln.Close()

	cfg := Config{
		Port: port,
		TLS:  TLSConfig{PlainHTTP: true},
		Authn: func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				ctx.SetUserID(ctx.GetHeader("X-User"))
				next(ctx)
			}
		},
		Authorize: func(userID string, resource model.Resource, action model.Action) bool {
			// Simulate some work or shared state access
			return userID == "admin"
		},
	}
	s := New(cfg)

	s.Router().Get("/secret", func(ctx router.Context) {
		ctx.Write([]byte("ok"))
	}).Requires(model.Resource("res"), model.Read)

	go func() {
		s.ListenAndServe()
	}()

	time.Sleep(200 * time.Millisecond)

	var wg sync.WaitGroup
	n := 100
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/secret", nil)
			user := "guest"
			expected := 403
			if i%2 == 0 {
				user = "admin"
				expected = 200
			}
			req.Header.Set("X-User", user)
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != expected {
				t.Errorf("Iteration %d: expected %d, got %d", i, expected, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
}
