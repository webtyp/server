package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/model"
	"webtyp.com/router"
)

func TestSecurityContract_DefaultDeny(t *testing.T) {
	srv := New(Config{
		Authorize: func(userID string, resource model.Resource, action model.Action) bool {
			return userID == "admin"
		},
	})
	srv.Router().Get("/guarded", func(ctx router.Context) {
		ctx.Write([]byte("ok"))
	}).Requires(model.Resource("data"), model.Read)

	handler, err := srv.Handler()
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}

	// 1. No identity -> 403
	req := httptest.NewRequest("GET", "/guarded", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for anonymous, got %d", w.Code)
	}

	// 2. Identity without permission -> 403
	srv.baseHandler = nil // Clear cache for dynamic config update in test
	srv.config.Authn = func(next router.HandlerFunc) router.HandlerFunc {
		return func(ctx router.Context) {
			ctx.SetUserID("guest")
			next(ctx)
		}
	}
	handler, _ = srv.Handler()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for guest, got %d", w.Code)
	}

	// 3. Identity with permission -> 200
	srv.baseHandler = nil // Clear cache for dynamic config update in test
	srv.config.Authn = func(next router.HandlerFunc) router.HandlerFunc {
		return func(ctx router.Context) {
			ctx.SetUserID("admin")
			next(ctx)
		}
	}
	handler, _ = srv.Handler()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for admin, got %d", w.Code)
	}
}

func TestSecurityContract_Authenticated(t *testing.T) {
	authorizerCalled := false
	srv := New(Config{
		Authn: func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				if ctx.GetHeader("X-User") != "" {
					ctx.SetUserID(ctx.GetHeader("X-User"))
				}
				next(ctx)
			}
		},
		Authorize: func(userID string, resource model.Resource, action model.Action) bool {
			authorizerCalled = true
			return false
		},
	})

	srv.Router().Get("/auth", func(ctx router.Context) {
		ctx.Write([]byte("ok"))
	}).Authenticated()

	handler, err := srv.Handler()
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}

	// 1. Anonymous -> 403
	req := httptest.NewRequest("GET", "/auth", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for anonymous, got %d", w.Code)
	}

	// 2. Authenticated -> 200, no authorizer call
	req.Header.Set("X-User", "anybody")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for authenticated, got %d", w.Code)
	}
	if authorizerCalled {
		t.Error("Authorizer should NOT have been called for .Authenticated() route")
	}
}

func TestSecurityContract_Public(t *testing.T) {
	srv := New(Config{})
	srv.Router().Get("/public", func(ctx router.Context) {
		ctx.Write([]byte("ok"))
	}).Public()

	handler, _ := srv.Handler()
	req := httptest.NewRequest("GET", "/public", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for public, got %d", w.Code)
	}
}

func TestSecurityContract_NilAuthorizeDeniesGuarded(t *testing.T) {
	// We need a way to bypass validateRBAC which fails if Authorize is nil.
	// But the verja itself should also deny if it somehow gets called.
	// Actually, validateRBAC prevents this at startup.
	// Let's test that it fails at startup.
	srv := New(Config{Authorize: nil})
	srv.Router().Get("/guarded", func(ctx router.Context) {}).Requires(model.Resource("res"), model.Read)

	_, err := srv.Handler()
	if err == nil {
		t.Fatal("Expected error at startup for guarded route with nil Authorize")
	}
}

func TestSecurityContract_Contradictions(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(r router.Router)
		config Config
	}{
		{
			name: "Guarded without resource",
			setup: func(r router.Router) {
				r.Get("/bad", func(ctx router.Context) {}) // Defaults to Guarded
			},
			config: Config{Authorize: func(string, model.Resource, model.Action) bool { return true }},
		},
		{
			name: "Public with resource",
			setup: func(r router.Router) {
				route := r.Get("/bad", func(ctx router.Context) {})
				route.Requires(model.Resource("res"), model.Read)
				route.Public() // This overwrites Access to Public, but Resource is still there
			},
			config: Config{Authorize: func(string, model.Resource, model.Action) bool { return true }},
		},
		{
			name: "Authenticated with resource",
			setup: func(r router.Router) {
				route := r.Get("/bad", func(ctx router.Context) {})
				route.Requires(model.Resource("res"), model.Read)
				route.Authenticated() // This overwrites Access to Authenticated
			},
			config: Config{Authorize: func(string, model.Resource, model.Action) bool { return true }},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(tt.config)
			tt.setup(srv.Router())
			_, err := srv.Handler()
			if err == nil {
				t.Errorf("Expected error for %s, but got nil", tt.name)
			}
		})
	}
}

func TestSecurityContract_HealthIsUnprotected(t *testing.T) {
	srv := New(Config{Health: true})
	handler, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Health should be unprotected, got %d", w.Code)
	}
}
