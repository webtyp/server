package httpd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"webtyp.com/router"
	"webtyp.com/router/conformance"
)

// conformanceUserHeader is this adapter's authentication seam for the suite: the header the
// Authn middleware turns into an identity. A production deployment reads a session cookie or
// a bearer token instead — the seam is the same one, which is the point.
const conformanceUserHeader = "X-Conformance-User"

// TestHTTPDConformance holds this adapter to the router contract — the executable one, shared
// with every other implementation (see router/conformance).
//
// It is what would have caught, on the day it was written, the bug this adapter shipped:
// routes were registered in the ServeMux by PATH ALONE, so three methods on one path were the
// same pattern and Go panicked at startup. The contract offers Get/Post/Options on any path,
// the edge implementation honoured it, and this one could not.
func TestHTTPDConformance(t *testing.T) {
	// Verify receives the Router the suite was handed, so it needs a way back to the Server
	// that owns it. Handler() is where this adapter validates its RBAC wiring.
	var mu sync.Mutex
	servers := map[router.Router]*Server{}

	conformance.Run(t, conformance.Factory{
		New: func(t *testing.T, s conformance.Setup) (router.Router, conformance.ServeFunc) {
			srv := New(Config{
				TLS:       TLSConfig{PlainHTTP: true},
				Authorize: s.Authorize,
				Authn: func(next router.HandlerFunc) router.HandlerFunc {
					return func(ctx router.Context) {
						if id := ctx.GetHeader(conformanceUserHeader); id != "" {
							ctx.SetUserID(id)
						}
						next(ctx)
					}
				},
			})

			r := srv.Router()

			mu.Lock()
			servers[r] = srv
			mu.Unlock()

			// The suite registers its routes AFTER New returns, and Handler() freezes the
			// wiring — so build it on first use, not here.
			var once sync.Once
			var handler http.Handler
			var buildErr error

			serve := func(method, path string, body []byte, userID string) conformance.Response {
				once.Do(func() { handler, buildErr = srv.Handler() })
				if buildErr != nil {
					t.Fatalf("building the handler failed: %v", buildErr)
				}

				req := httptest.NewRequest(method, path, bytes.NewReader(body))
				if userID != "" {
					req.Header.Set(conformanceUserHeader, userID)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()
				out, _ := io.ReadAll(res.Body)
				return conformance.Response{Status: res.StatusCode, Body: out}
			}
			return r, serve
		},

		// A guarded route with no Authorize configured must not start: it would deny every
		// caller, forever, on a route that looks protected. validateRBAC already says so.
		Verify: func(r router.Router) error {
			mu.Lock()
			srv := servers[r]
			mu.Unlock()
			return srv.validateRBAC()
		},
	})
}
