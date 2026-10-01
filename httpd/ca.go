package httpd

import (
	"net/http"

	"webtyp.com/router"
)

// CAPath is where the local certificate authority is served so a device on
// the LAN can install it and trust the server. httpd serves it automatically
// whenever it serves the local CA (the zero TLSConfig).
//
// iOS needs two steps and hints at neither: install the profile (Settings →
// Profile Downloaded), then enable it under Settings → General → About →
// Certificate Trust Settings. A profile installed but not trusted behaves
// exactly like no profile at all.
//
// Windows: open the downloaded file and install it in "Trusted Root
// Certification Authorities".
const CAPath = "/__webtyp/ca"

// CADownloadContentType is the MIME type iOS and Android expect for a CA
// certificate offered for installation.
const CADownloadContentType = "application/x-x509-ca-cert"

func serveLocalCA(c router.Context) {
	der, err := LocalCA()
	if err != nil {
		c.WriteStatus(http.StatusServiceUnavailable)
		return
	}
	c.SetHeader("Content-Type", CADownloadContentType)
	c.WriteStatus(http.StatusOK)
	c.Write(der)
}
