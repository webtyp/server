package httpd

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
)

// LocalCertSPKI returns the base64-encoded SHA-256 of the local
// certificate's SubjectPublicKeyInfo — the value Chrome's
// --ignore-certificate-errors-spki-list expects. webtyp.com/devbrowser uses it
// to launch Chrome trusting exactly this certificate and no other.
//
// The output is a stable 44-character base64 string for a given certificate.
func LocalCertSPKI() (string, error) {
	der, err := localCertLeafDER()
	if err != nil {
		return "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}
