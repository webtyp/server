package httpd

import (
	"crypto/tls"
	"errors"
	"net/http"

	"github.com/go-acme/lego/v4/challenge"
	"golang.org/x/crypto/acme/autocert"
)

const errMultipleTLSModes = "multiple TLS modes enabled; choose at most one (AutoCert, DNS01, Cert/Key, or PlainHTTP)"

// TLSConfig picks how the server is reached. The zero value serves HTTPS with the
// local certificate authority (see LocalCA): a page served by httpd is always a
// secure context, on localhost and on a LAN. Set at most one of the fields.
type TLSConfig struct {
	AutoCert  bool               // public certificate from Let's Encrypt for Domain
	DNS01     challenge.Provider // a real certificate for Domain by DNS-01; nil = off
	Domain    string
	CertFile  string // certificate and key provided by the operator
	KeyFile   string
	PlainHTTP bool // plain HTTP: only when a proxy in front terminates TLS
}

type tlsMode uint8

const (
	tlsLocal tlsMode = iota // zero value
	tlsAutoCert
	tlsDNS01
	tlsFiles
	tlsPlain
)

func (c TLSConfig) mode() tlsMode {
	if c.DNS01 != nil {
		return tlsDNS01
	}
	if c.AutoCert {
		return tlsAutoCert
	}
	if c.CertFile != "" || c.KeyFile != "" {
		return tlsFiles
	}
	if c.PlainHTTP {
		return tlsPlain
	}
	return tlsLocal
}

func (s *Server) validateTLS() error {
	modes := 0
	if s.config.TLS.AutoCert {
		modes++
		if s.config.TLS.Domain == "" {
			return errors.New("TLS AutoCert requires a Domain")
		}
	}
	if s.config.TLS.DNS01 != nil {
		modes++
		if s.config.TLS.Domain == "" {
			return errors.New("TLS DNS01 requires a Domain")
		}
	}
	if s.config.TLS.CertFile != "" || s.config.TLS.KeyFile != "" {
		modes++
		if s.config.TLS.CertFile == "" || s.config.TLS.KeyFile == "" {
			return errors.New("TLS CertFile and KeyFile must both be provided")
		}
	}
	if s.config.TLS.PlainHTTP {
		modes++
	}

	if modes > 1 {
		return errors.New(errMultipleTLSModes)
	}
	return nil
}

func (s *Server) listenAndServe(srv *http.Server) error {
	switch s.config.TLS.mode() {
	case tlsDNS01:
		d, err := s.dns01Certificates()
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{GetCertificate: d.get}
		return srv.ListenAndServeTLS("", "")
	case tlsAutoCert:
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(s.config.TLS.Domain),
			Cache:      autocert.DirCache("certs"),
		}
		srv.TLSConfig = m.TLSConfig()
		return srv.ListenAndServeTLS("", "")
	case tlsFiles:
		return srv.ListenAndServeTLS(s.config.TLS.CertFile, s.config.TLS.KeyFile)
	case tlsPlain:
		return srv.ListenAndServe()
	}
	// tlsLocal, the zero value: never falls back to plain HTTP.
	srv.TLSConfig = &tls.Config{GetCertificate: s.localCert.get}
	return srv.ListenAndServeTLS("", "")
}

// dns01Certificates builds and starts the DNS-01 manager once per Server: with two
// doors, listenAndServe runs once per listener, and both must share one
// certificate, one order and one renewal routine.
func (s *Server) dns01Certificates() (*dns01Manager, error) {
	s.dns01Once.Do(func() {
		if s.dns01 == nil {
			s.dns01 = &dns01Manager{
				domain:   s.config.TLS.Domain,
				provider: s.config.TLS.DNS01,
				logf:     s.log,
			}
		}
		s.dns01Err = s.dns01.init()
	})
	return s.dns01, s.dns01Err
}
