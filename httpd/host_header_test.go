package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/router"
)

// Go moves the Host header out of r.Header into r.Host; GetHeader("Host")
// must still answer it, or a handler cannot tell user2.localhost from localhost.
func TestGetHeader_HostIsTheRequestHost(t *testing.T) {
	s := New(Config{TLS: TLSConfig{PlainHTTP: true}})
	var got string
	s.Router().Get("/host", func(ctx router.Context) {
		got = ctx.GetHeader("Host")
		ctx.WriteStatus(http.StatusOK)
	}).Public()
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/host", nil)
	req.Host = "user2.localhost:6060"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "user2.localhost:6060" {
		t.Fatalf(`GetHeader("Host") = %q, want "user2.localhost:6060"`, got)
	}
}
