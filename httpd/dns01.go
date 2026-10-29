//go:build !wasm
// +build !wasm

package httpd

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

type dns01Manager struct {
	domain   string
	provider challenge.Provider
	logf     func(args ...any)

	mu   sync.RWMutex
	cert *tls.Certificate

	// allow overriding the certificate obtainer for tests
	obtain func(domain string) (*certificate.Resource, error)
}

func (m *dns01Manager) get(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cert, nil
}

const (
	certsDir       = "certs"
	accountKeyFile = "acme-account.key"
)

func (m *dns01Manager) init() error {
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	keyPath := filepath.Join(certsDir, accountKeyFile)
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return fmt.Errorf("failed to generate account key: %w", err)
		}
		keyBytes, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return fmt.Errorf("failed to marshal account key: %w", err)
		}
		pemBlock := &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}
		if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
			return fmt.Errorf("failed to save account key: %w", err)
		}
	}

	if m.obtain == nil {
		m.obtain = m.obtainProduction
	}

	certPath := filepath.Join(certsDir, m.domain+".crt")
	keyFile := filepath.Join(certsDir, m.domain+".key")

	err := m.loadCert(certPath, keyFile)
	if err != nil || m.needsRenewal() {
		if err := m.renew(certPath, keyFile); err != nil {
			if m.cert == nil {
				return fmt.Errorf("failed to obtain initial certificate: %w", err)
			}
			m.logf("failed to renew certificate (will retry tomorrow):", err)
		}
	}

	go m.renewalRoutine(certPath, keyFile)

	return nil
}

func (m *dns01Manager) loadCert(certPath, keyPath string) error {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return err
	}

	// parse leaf
	if len(cert.Certificate) > 0 {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return err
		}
		cert.Leaf = leaf
	} else {
		return errors.New("certificate is empty")
	}

	m.mu.Lock()
	m.cert = &cert
	m.mu.Unlock()
	return nil
}

func (m *dns01Manager) needsRenewal() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil || m.cert.Leaf == nil {
		return true
	}
	return time.Until(m.cert.Leaf.NotAfter) < 30*24*time.Hour
}

func (m *dns01Manager) renew(certPath, keyPath string) error {
	res, err := m.obtain(m.domain)
	if err != nil {
		return err
	}

	if err := os.WriteFile(certPath, res.Certificate, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, res.PrivateKey, 0600); err != nil {
		return err
	}

	return m.loadCert(certPath, keyPath)
}

type legoUser struct {
	key crypto.PrivateKey
	reg *registration.Resource
}

func (u *legoUser) GetEmail() string {
	return ""
}
func (u *legoUser) GetRegistration() *registration.Resource {
	return u.reg
}
func (u *legoUser) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

func (m *dns01Manager) obtainProduction(domain string) (*certificate.Resource, error) {
	keyPath := filepath.Join(certsDir, accountKeyFile)
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read account key: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, errors.New("failed to decode account key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse account key: %w", err)
	}

	user := &legoUser{key: key}
	config := lego.NewConfig(user)
	config.CADirURL = lego.LEDirectoryProduction

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create lego client: %w", err)
	}

	if err := client.Challenge.SetDNS01Provider(m.provider); err != nil {
		return nil, fmt.Errorf("failed to set DNS01 provider: %w", err)
	}

	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, fmt.Errorf("failed to register account: %w", err)
	}
	user.reg = reg

	req := certificate.ObtainRequest{
		Domains: []string{domain},
		Bundle:  true,
	}

	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain certificate: %w", err)
	}

	return res, nil
}

func (m *dns01Manager) renewalRoutine(certPath, keyPath string) {
	ticker := time.NewTicker(24 * time.Hour)
	for range ticker.C {
		if m.needsRenewal() {
			if err := m.renew(certPath, keyPath); err != nil {
				if m.logf != nil {
					m.logf("failed to renew certificate (will retry tomorrow):", err)
				}
			}
		}
	}
}
