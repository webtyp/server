package server_test

import (
	"os"
	"path/filepath"
	"testing"

	"webtyp.com/server/httpd"
)

const (
	testDomain   = "app.example.com"
	testToken    = "test-token-not-real"
	testInternet = "8443"
)

// setEnv sets the three variables FromEnv reads, and clears lego's alternative
// credentials so the machine running the tests cannot change the result.
func setEnv(t *testing.T, domain, token, internet string) {
	t.Helper()
	t.Setenv(httpd.EnvAppDomain, domain)
	t.Setenv(httpd.EnvDNSAPIToken, token)
	t.Setenv(httpd.EnvDNSAPIToken+"_FILE", "")
	t.Setenv(httpd.EnvInternetPort, internet)
	for _, k := range []string{"CLOUDFLARE_EMAIL", "CLOUDFLARE_API_KEY", "CF_API_EMAIL", "CF_API_KEY", "CF_DNS_API_TOKEN", "CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN"} {
		t.Setenv(k, "")
	}
}

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name                     string
		domain, token, internet  string
		wantErr                  string
		wantDNS01                bool
		wantDomain, wantInternet string
	}{
		{name: "nothing set: zero config", wantDNS01: false},
		{name: "domain and token: certificate, one door", domain: testDomain, token: testToken, wantDNS01: true, wantDomain: testDomain},
		{name: "domain, token and internet port: both doors", domain: testDomain, token: testToken, internet: testInternet, wantDNS01: true, wantDomain: testDomain, wantInternet: testInternet},
		{name: "domain without token", domain: testDomain, wantErr: httpd.ErrEnvDomainWithoutToken},
		{name: "token without domain", token: testToken, wantErr: httpd.ErrEnvTokenWithoutDomain},
		{name: "internet port without domain", internet: testInternet, wantErr: httpd.ErrEnvInternetWithoutDomain},
		{name: "internet port and token without domain", token: testToken, internet: testInternet, wantErr: httpd.ErrEnvInternetWithoutDomain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setEnv(t, c.domain, c.token, c.internet)
			cfg, err := httpd.FromEnv()
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("FromEnv: %v", err)
			}
			if (cfg.TLS.DNS01 != nil) != c.wantDNS01 {
				t.Fatalf("DNS01 set = %v, want %v", cfg.TLS.DNS01 != nil, c.wantDNS01)
			}
			if cfg.TLS.Domain != c.wantDomain || cfg.InternetPort != c.wantInternet {
				t.Fatalf("got Domain %q InternetPort %q, want %q %q", cfg.TLS.Domain, cfg.InternetPort, c.wantDomain, c.wantInternet)
			}
		})
	}
}

func TestFromEnv_TokenFile(t *testing.T) {
	setEnv(t, testDomain, "", "")

	// Create token file
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(testToken), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(httpd.EnvDNSAPIToken+"_FILE", tokenFile)

	cfg, err := httpd.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.TLS.DNS01 == nil {
		t.Fatalf("DNS01 not set")
	}
	if cfg.TLS.Domain != testDomain {
		t.Fatalf("got Domain %q, want %q", cfg.TLS.Domain, testDomain)
	}
}

func TestFromEnv_TokenFileWithoutDomain(t *testing.T) {
	setEnv(t, "", "", "")

	// Create token file
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(testToken), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(httpd.EnvDNSAPIToken+"_FILE", tokenFile)

	_, err := httpd.FromEnv()
	if err == nil || err.Error() != httpd.ErrEnvTokenWithoutDomain {
		t.Fatalf("got error %v, want %q", err, httpd.ErrEnvTokenWithoutDomain)
	}
}
