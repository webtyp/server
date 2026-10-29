package httpd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certificate"
)

type fakeProvider struct{}

func (p fakeProvider) Present(domain, token, keyAuth string) error { return nil }
func (p fakeProvider) CleanUp(domain, token, keyAuth string) error { return nil }

func genTestCert(domain string, validDays int) (*certificate.Resource, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Duration(validDays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{domain},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	privBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	return &certificate.Resource{
		Domain:      domain,
		Certificate: certPEM,
		PrivateKey:  keyPEM,
	}, nil
}

func TestDNS01_RequiresDomain(t *testing.T) {
	s := New(Config{
		TLS: TLSConfig{
			DNS01: fakeProvider{},
		},
	})

	_, err := s.Handler()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "TLS DNS01 requires a Domain" {
		t.Fatalf("expected 'TLS DNS01 requires a Domain', got %v", err)
	}
}

func TestDNS01_ExclusiveWithOtherModes(t *testing.T) {
	s := New(Config{
		TLS: TLSConfig{
			AutoCert: true,
			Domain:   "app.example.com",
			DNS01:    fakeProvider{},
		},
	})

	_, err := s.Handler()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != errMultipleTLSModes {
		t.Fatalf("expected errMultipleTLSModes, got %v", err)
	}
}

func TestDNS01_ObtainsWhenNoneStored(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	obtainCalled := 0
	d := &dns01Manager{
		domain:   "app.example.com",
		provider: fakeProvider{},
		obtain: func(domain string) (*certificate.Resource, error) {
			obtainCalled++
			return genTestCert(domain, 60)
		},
	}

	if err := d.init(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if obtainCalled != 1 {
		t.Errorf("expected obtain to be called 1 time, got %d", obtainCalled)
	}

	cert, err := d.get(nil)
	if err != nil {
		t.Errorf("expected no error from get, got %v", err)
	}
	if cert == nil {
		t.Error("expected a cert, got nil")
	}

	// Verify files were written
	if _, err := os.Stat(filepath.Join("certs", "app.example.com.crt")); os.IsNotExist(err) {
		t.Error("expected cert file to exist")
	}
}

func TestDNS01_ReusesStoredCertificate(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	if err := os.MkdirAll("certs", 0700); err != nil {
		t.Fatal(err)
	}
	res, err := genTestCert("app.example.com", 60)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join("certs", "app.example.com.crt"), res.Certificate, 0600)
	os.WriteFile(filepath.Join("certs", "app.example.com.key"), res.PrivateKey, 0600)

	obtainCalled := 0
	d := &dns01Manager{
		domain:   "app.example.com",
		provider: fakeProvider{},
		obtain: func(domain string) (*certificate.Resource, error) {
			obtainCalled++
			return genTestCert(domain, 60)
		},
	}

	if err := d.init(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if obtainCalled != 0 {
		t.Errorf("expected obtain not to be called, got %d", obtainCalled)
	}
}

func TestDNS01_RenewsWhenExpiringSoon(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	if err := os.MkdirAll("certs", 0700); err != nil {
		t.Fatal(err)
	}
	res, err := genTestCert("app.example.com", 10)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join("certs", "app.example.com.crt"), res.Certificate, 0600)
	os.WriteFile(filepath.Join("certs", "app.example.com.key"), res.PrivateKey, 0600)

	obtainCalled := 0
	d := &dns01Manager{
		domain:   "app.example.com",
		provider: fakeProvider{},
		obtain: func(domain string) (*certificate.Resource, error) {
			obtainCalled++
			return genTestCert(domain, 60)
		},
	}

	if err := d.init(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if obtainCalled != 1 {
		t.Errorf("expected obtain to be called 1 time, got %d", obtainCalled)
	}
}

func TestDNS01_FailedRenewKeepsServing(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	if err := os.MkdirAll("certs", 0700); err != nil {
		t.Fatal(err)
	}
	res, err := genTestCert("app.example.com", 10)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join("certs", "app.example.com.crt"), res.Certificate, 0600)
	os.WriteFile(filepath.Join("certs", "app.example.com.key"), res.PrivateKey, 0600)

	obtainCalled := 0
	logged := false
	d := &dns01Manager{
		domain:   "app.example.com",
		provider: fakeProvider{},
		logf: func(args ...any) {
			logged = true
		},
		obtain: func(domain string) (*certificate.Resource, error) {
			obtainCalled++
			return nil, errors.New("fake obtain error")
		},
	}

	// It shouldn't return error because it falls back to the old cert.
	if err := d.init(); err != nil {
		t.Fatalf("init failed unexpectedly: %v", err)
	}

	if obtainCalled != 1 {
		t.Errorf("expected obtain to be called 1 time, got %d", obtainCalled)
	}

	if !logged {
		t.Error("expected error to be logged, but it wasn't")
	}

	cert, err := d.get(nil)
	if err != nil {
		t.Errorf("expected get to succeed, got %v", err)
	}
	if cert == nil {
		t.Error("expected a cert, got nil")
	}
}
