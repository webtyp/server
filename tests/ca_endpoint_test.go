package server_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"webtyp.com/server"
	"webtyp.com/server/httpd"
)

// Test 8 — the internal dev server serves the development CA at /__webtyp/ca,
// DER-encoded, with the content type iOS/Android expect for a CA profile.
func TestCAEndpoint_ServesDER(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving port: %v", err)
	}
	port := fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	exit := make(chan bool, 1)
	h := server.New()
	h.SetAppRootDir(t.TempDir())
	h.SetHTTPS(true) // exercise the TLS path and the CA handler
	h.SetPort(port)
	h.SetExitChan(exit)
	h.SetLogger(t.Log)

	var wg sync.WaitGroup
	wg.Add(1)
	go h.StartServer(&wg)
	t.Cleanup(func() {
		exit <- true
		wg.Wait()
	})

	if !server.WaitForPortListening(port, 5*time.Second, true) {
		t.Fatal("internal TLS server did not come up")
	}

	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Get("https://127.0.0.1:" + port + server.CAPath)
	if err != nil {
		t.Fatalf("GET %s: %v", server.CAPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != server.CADownloadContentType {
		t.Errorf("Content-Type = %q, want %q", ct, server.CADownloadContentType)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if _, err := x509.ParseCertificate(body); err != nil {
		t.Fatalf("body is not a DER certificate: %v", err)
	}
}

func TestLocalCA_RouteMountedByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := httpd.New(httpd.Config{})
	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("s.Handler(): %v", err)
	}

	req := httptest.NewRequest("GET", httpd.CAPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != httpd.CADownloadContentType {
		t.Errorf("Content-Type = %q, want %q", ct, httpd.CADownloadContentType)
	}

	caCert, err := x509.ParseCertificate(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}
	if !caCert.IsCA {
		t.Error("IsCA = false, want true")
	}
}

func TestLocalCA_RouteAbsentWithOperatorCert(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s := httpd.New(httpd.Config{
		TLS: httpd.TLSConfig{
			CertFile: "dummy_cert.pem",
			KeyFile:  "dummy_key.pem",
		},
	})
	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("s.Handler(): %v", err)
	}

	req := httptest.NewRequest("GET", httpd.CAPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
