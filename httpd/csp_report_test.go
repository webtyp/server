package httpd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtyp.com/events/inprocess"
	"webtyp.com/router"
	"webtyp.com/router/security"
	"webtyp.com/securityevent"
)

func TestHTTPD_AlwaysEmitsStrictCSP(t *testing.T) {
	pubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pubDir, "hello.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	srv := New(Config{PublicDir: pubDir})
	srv.Router().Get("/api/ping", func(ctx router.Context) {
		ctx.Write([]byte("pong"))
	}).Public()

	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/ping", "/hello.txt", "/missing"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		csp := w.Header().Get(security.HeaderContentSecurityPolicy)
		if !strings.Contains(csp, "require-trusted-types-for 'script'") {
			t.Errorf("%s missing Trusted Types in CSP: %q", path, csp)
		}
		if !strings.Contains(csp, "report-to csp") {
			t.Errorf("%s missing report-to csp in CSP: %q", path, csp)
		}
		if got := w.Header().Get(security.HeaderReportingEndpoints); got != `csp="/_csp-report"` {
			t.Errorf("%s Reporting-Endpoints = %q, want %q", path, got, `csp="/_csp-report"`)
		}
	}
}

func TestHTTPD_LoadsScriptHashesFromCSPJSON(t *testing.T) {
	pubDir := t.TempDir()
	raw, _ := json.Marshal(cspManifest{ScriptHashes: []string{"sha384-xyz123"}})
	if err := os.WriteFile(filepath.Join(pubDir, cspManifestFile), raw, 0644); err != nil {
		t.Fatal(err)
	}

	srv := New(Config{PublicDir: pubDir})
	srv.Router().Get("/api/ping", func(ctx router.Context) {
		ctx.Write([]byte("ok"))
	}).Public()

	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	csp := w.Header().Get(security.HeaderContentSecurityPolicy)
	want := "script-src 'sha384-xyz123' 'strict-dynamic' 'wasm-unsafe-eval'"
	if !strings.Contains(csp, want) {
		t.Fatalf("CSP = %q, want %q", csp, want)
	}
}

func TestHTTPD_CSPReportEndpoint_ReportsV1(t *testing.T) {
	broker := &inprocess.Broker{}
	var got []securityevent.Event
	if _, err := securityevent.Listen(broker, func(e securityevent.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := securityevent.New(broker)
	if err != nil {
		t.Fatal(err)
	}

	srv := New(Config{Report: rep})
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	body := `[{"type":"csp-violation","body":{"blockedURL":"https://evil.example/x.js","effectiveDirective":"script-src-elem"}}]`
	req := httptest.NewRequest(http.MethodPost, CSPReportPath, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/reports+json")
	req.RemoteAddr = "203.0.113.9:4321"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if len(got) != 1 {
		t.Fatalf("events = %+v, want 1", got)
	}
	if got[0].Kind != securityevent.ContentBlocked || got[0].Resource != "https://evil.example/x.js" || got[0].Reason != "script-src-elem" || got[0].IP != "203.0.113.9" {
		t.Fatalf("unexpected event: %+v", got[0])
	}
}

func TestHTTPD_CSPReportEndpoint_LegacyFormat(t *testing.T) {
	broker := &inprocess.Broker{}
	var got []securityevent.Event
	if _, err := securityevent.Listen(broker, func(e securityevent.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatal(err)
	}
	rep, _ := securityevent.New(broker)

	srv := New(Config{Report: rep})
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	body := `{"csp-report":{"blocked-uri":"inline","violated-directive":"style-src-attr"}}`
	req := httptest.NewRequest(http.MethodPost, CSPReportPath, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/csp-report")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if len(got) != 1 || got[0].Kind != securityevent.ContentBlocked || got[0].Resource != "inline" || got[0].Reason != "style-src-attr" {
		t.Fatalf("events = %+v, want 1 ContentBlocked", got)
	}
}

func TestHTTPD_CSPReportEndpoint_MalformedBody(t *testing.T) {
	broker := &inprocess.Broker{}
	var got []securityevent.Event
	if _, err := securityevent.Listen(broker, func(e securityevent.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatal(err)
	}
	rep, _ := securityevent.New(broker)

	srv := New(Config{Report: rep})
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, CSPReportPath, bytes.NewBufferString("{broken-json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events on malformed body, got %+v", got)
	}
}
