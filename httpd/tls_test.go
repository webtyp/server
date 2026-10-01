package httpd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidateTLS_RejectsMultipleModes(t *testing.T) {
	s := New(Config{
		TLS: TLSConfig{
			PlainHTTP: true,
			CertFile:  "cert.pem",
			KeyFile:   "key.pem",
		},
	})

	err := s.validateTLS()
	if err == nil {
		t.Fatal("Expected error for multiple TLS modes, got nil")
	}
	if err.Error() != errMultipleTLSModes {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestValidateTLS_PlainHTTPWithAutoCertRejected(t *testing.T) {
	s := New(Config{
		TLS: TLSConfig{
			PlainHTTP: true,
			AutoCert:  true,
			Domain:    "example.com",
		},
	})

	err := s.validateTLS()
	if err == nil {
		t.Fatal("Expected error for PlainHTTP with AutoCert, got nil")
	}
	if err.Error() != errMultipleTLSModes {
		t.Errorf("Unexpected error message: %v, want %q", err, errMultipleTLSModes)
	}
}

func TestValidateTLS_AutoCertRequiresDomain(t *testing.T) {
	s := New(Config{
		TLS: TLSConfig{
			AutoCert: true,
		},
	})

	err := s.validateTLS()
	if err == nil {
		t.Fatal("Expected error for AutoCert without domain, got nil")
	}
	if err.Error() != "TLS AutoCert requires a Domain" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestValidateTLS_CertKeyMustBePaired(t *testing.T) {
	s := New(Config{
		TLS: TLSConfig{
			CertFile: "cert.pem",
		},
	})

	err := s.validateTLS()
	if err == nil {
		t.Fatal("Expected error for missing KeyFile, got nil")
	}
	if err.Error() != "TLS CertFile and KeyFile must both be provided" {
		t.Errorf("Unexpected error message: %v", err)
	}

	s = New(Config{
		TLS: TLSConfig{
			KeyFile: "key.pem",
		},
	})

	err = s.validateTLS()
	if err == nil {
		t.Fatal("Expected error for missing CertFile, got nil")
	}
	if err.Error() != "TLS CertFile and KeyFile must both be provided" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestZeroTLSConfig_ServesHTTPSWithLocalCA(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ln.Addr().String(), ":")[1]
	ln.Close()

	s := New(Config{
		Port:   port,
		Health: true,
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.ListenAndServe()
	}()

	select {
	case err := <-errChan:
		t.Fatalf("Server failed to start: %v", err)
	case <-time.After(1 * time.Second):
	}

	caDER, err := LocalCA()
	if err != nil {
		t.Fatalf("LocalCA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: roots,
			},
		},
	}

	resp, err := client.Get("https://127.0.0.1:" + port + "/health")
	if err != nil {
		t.Fatalf("HTTPS GET with RootCAs failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestPlainHTTP_ServesHTTP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ln.Addr().String(), ":")[1]
	ln.Close()

	s := New(Config{
		Port:   port,
		Health: true,
		TLS: TLSConfig{
			PlainHTTP: true,
		},
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.ListenAndServe()
	}()

	select {
	case err := <-errChan:
		t.Fatalf("Server failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	client := &http.Client{}

	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		t.Fatalf("Plain HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}

	respCA, err := client.Get("http://127.0.0.1:" + port + "/__webtyp/ca")
	if err != nil {
		t.Fatalf("GET /__webtyp/ca failed: %v", err)
	}
	defer respCA.Body.Close()

	if respCA.StatusCode != http.StatusNotFound {
		t.Errorf("CAPath status = %d, want 404 for PlainHTTP", respCA.StatusCode)
	}
}

func TestTLS_CertFileKeyFile_Serves(t *testing.T) {
	// 1. Generate self-signed cert
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}

	cOut, _ := os.Create(certFile)
	pem.Encode(cOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	cOut.Close()

	kOut, _ := os.Create(keyFile)
	privBytes, _ := x509.MarshalECPrivateKey(priv)
	pem.Encode(kOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
	kOut.Close()

	// 2. Start server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ln.Addr().String(), ":")[1]
	ln.Close()

	s := New(Config{
		Port:   port,
		Health: true,
		TLS: TLSConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.ListenAndServe()
	}()

	// Wait for server or error
	select {
	case err := <-errChan:
		t.Fatalf("Server failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
		// Assume started
	}

	// 3. Test request
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
	}

	resp, err := client.Get("https://127.0.0.1:" + port + "/health")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}

func TestLocalTLS_ServesWithoutBlockingOnTruststoreFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	t.Setenv("HOME", t.TempDir())

	// Find free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ln.Addr().String(), ":")[1]
	ln.Close()

	var mu sync.Mutex
	var logs []string
	s := New(Config{
		Port:   port,
		Health: true,
		Logger: func(args ...any) {
			mu.Lock()
			logs = append(logs, fmt.Sprint(args...))
			mu.Unlock()
		},
	})

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.ListenAndServe()
	}()

	// Wait for server or error
	select {
	case err := <-errChan:
		t.Fatalf("Server failed to start: %v", err)
	case <-time.After(1 * time.Second):
		// Assume started
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get("https://127.0.0.1:" + port + "/health")
	if err != nil {
		mu.Lock()
		currentLogs := append([]string(nil), logs...)
		mu.Unlock()
		t.Fatalf("LocalTLS request failed: %v, logs: %v", err, currentLogs)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}

func _unusedImportsCheck() {
	_ = io.EOF
}
