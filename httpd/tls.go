package httpd

import (
	"crypto/tls"
	"errors"
	"net/http"

	"golang.org/x/crypto/acme/autocert"
)

const errMultipleTLSModes = "multiple TLS modes enabled; choose at most one (AutoCert, Cert/Key, or PlainHTTP)"

// TLSConfig picks how the server is reached. The zero value serves HTTPS with the
// local certificate authority (see LocalCA): a page served by httpd is always a
// secure context, on localhost and on a LAN. Set at most one of the fields.
type TLSConfig struct {
	AutoCert  bool   // public certificate from Let's Encrypt for Domain
	Domain    string
	CertFile  string // certificate and key provided by the operator
	KeyFile   string
	PlainHTTP bool   // plain HTTP: only when a proxy in front terminates TLS
}

type tlsMode uint8

const (
	tlsLocal tlsMode = iota // zero value
	tlsAutoCert
	tlsFiles
	tlsPlain
)

func (c TLSConfig) mode() tlsMode {
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
