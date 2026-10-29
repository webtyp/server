package httpd

import (
	"context"
	"net/http"

	"webtyp.com/router"
)

// markNetwork records on every request which door it came through. The adapter's
// Context.Value reads it back from the request context (adapter.go, Value).
func markNetwork(h http.Handler, network string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), router.ContextKeyNetwork, network)))
	})
}
