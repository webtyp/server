//go:build !wasm

package httpd

import (
	"errors"
	"os"

	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
)

// Environment variables FromEnv reads; the Docker Compose of a self-hosted app sets them.
const (
	// EnvAppDomain is the name the app is reached by, inside and outside, e.g. "app.example.com".
	EnvAppDomain = "APP_DOMAIN"
	// EnvInternetPort is the port of the internet door, reached only by the tunnel.
	EnvInternetPort = "INTERNET_PORT"
	// EnvDNSAPIToken is the Cloudflare token (Zone Read + DNS Edit on that domain) used to obtain its certificate.
	EnvDNSAPIToken = cloudflare.EnvDNSAPIToken

	fileSuffix = "_FILE"
)

// Errors returned by FromEnv; compare with err.Error().
const (
	ErrEnvDomainWithoutToken    = "APP_DOMAIN is set but CLOUDFLARE_DNS_API_TOKEN is not: the certificate for APP_DOMAIN cannot be obtained"
	ErrEnvTokenWithoutDomain    = "CLOUDFLARE_DNS_API_TOKEN is set but APP_DOMAIN is not: there is no name to obtain a certificate for"
	ErrEnvInternetWithoutDomain = "INTERNET_PORT is set but APP_DOMAIN is not: the tunnel verifies the certificate of APP_DOMAIN, so the internet door needs it"
)

// FromEnv returns the doors and the certificate of a self-hosted server, read
// from the environment. With none of the three variables set it returns the
// zero Config (local certificate authority, one door), as in development.
// The token may come as CLOUDFLARE_DNS_API_TOKEN or CLOUDFLARE_DNS_API_TOKEN_FILE.
//
// Example:
//
//	cfg, err := httpd.FromEnv()
//	if err != nil { return err }
//	cfg.Authorize = authorize
//	srv := httpd.New(cfg)
func FromEnv() (Config, error) {
	domain := os.Getenv(EnvAppDomain)
	// The token is present as a value or as a file (Docker secrets); lego reads either.
	token := os.Getenv(EnvDNSAPIToken)
	if token == "" {
		token = os.Getenv(EnvDNSAPIToken + fileSuffix)
	}
	internet := os.Getenv(EnvInternetPort)

	var c Config
	switch {
	case domain == "" && token == "" && internet == "":
		return c, nil
	case domain == "" && internet != "":
		return c, errors.New(ErrEnvInternetWithoutDomain)
	case domain == "":
		return c, errors.New(ErrEnvTokenWithoutDomain)
	case token == "":
		return c, errors.New(ErrEnvDomainWithoutToken)
	}

	provider, err := cloudflare.NewDNSProvider()
	if err != nil {
		return c, err
	}
	c.InternetPort = internet
	c.TLS = TLSConfig{DNS01: provider, Domain: domain}
	return c, nil
}
