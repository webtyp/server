package httpd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/truststore"
)

const (
	localCertOrg      = "WebTyp Local CA"
	localCertHostname = "localhost"
	localCertIP4Loop  = "127.0.0.1"
	localCertIP6Loop  = "::1"

	localCAFilename    = "ca.crt"
	localCAKeyFilename = "ca.key"
	localCertFilename  = "localhost.crt"
	localKeyFilename   = "localhost.key"
	localSANsFilename  = "localhost.sans" // records the SANs the cert on disk was built with
	pemTypeCert        = "CERTIFICATE"
	pemTypeECPrivate   = "EC PRIVATE KEY"

	localCertDirPerm    os.FileMode = 0o755
	localKeyFilePerm     os.FileMode = 0o600
	localSANsFilePerm    os.FileMode = 0o644
	localCAValidFor                  = 10 * 365 * 24 * time.Hour
	localCertValidFor                = 365 * 24 * time.Hour
	localCertRenewBefore             = 30 * 24 * time.Hour
	localCertRecheck                 = time.Hour

	// EnvSkipTruststore, when set to any non-empty value, stops the local
	// certificate from being installed into the OS trust store. The server
	// still generates and serves the certificate (and CAPath still works); only
	// the root-requiring system-trust step — which shells out to `sudo` on
	// Linux and prompts — is skipped. Test runners and CI set this.
	EnvSkipTruststore = "WEBTYP_LOCALCERT_SKIP_TRUSTSTORE"
)

var errLocalCertDecode = errors.New("httpd: local certificate is not valid PEM")

var (
	now            = time.Now
	hostname       = os.Hostname
	interfaceAddrs = net.InterfaceAddrs
)

// localCert hands the TLS stack the current leaf, regenerating it when it is close to
// expiry or the host's address set changed.
type localCert struct {
	mu       sync.Mutex
	cert     *tls.Certificate
	notAfter time.Time
	checked  time.Time
	logf     func(...any)
}

func (l *localCert) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	currentTime := now()
	needRefresh := l.cert == nil ||
		!currentTime.Before(l.notAfter.Add(-localCertRenewBefore)) ||
		currentTime.Sub(l.checked) > localCertRecheck

	if !needRefresh {
		return l.cert, nil
	}

	certFile, keyFile, err := ensureLocalCert(l.logf)
	if err != nil {
		return nil, err
	}

	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	if len(tlsCert.Certificate) > 0 {
		x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
		if err == nil {
			l.notAfter = x509Cert.NotAfter
		}
	}

	l.cert = &tlsCert
	l.checked = currentTime
	return l.cert, nil
}

// localCertDir is where the local certificate, key and SAN record live.
func localCertDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".webtyp", "httpd", "certs"), nil
}

// lanIPs returns every non-loopback IPv4 address of the host's active
// interfaces. A phone on the LAN opens https://192.168.x.x:<port>, and a
// certificate that does not name that address is rejected outright by Safari on
// iOS.
func lanIPs() []net.IP {
	var out []net.IP
	addrs, err := interfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4)
		}
	}
	return out
}

// desiredSANs returns the DNS name and the IP addresses the local
// certificate must carry, and a stable string form of that set for change
// detection.
func desiredSANs() (dns []string, ips []net.IP, fingerprint string) {
	dns = []string{localCertHostname}
	// Clinic PCs open the server by its Windows computer name (https://servidor:8080), not only by IP.
	if name, err := hostname(); err == nil && name != "" {
		lower := strings.ToLower(name)
		if lower != localCertHostname {
			dns = append(dns, lower)
		}
	}
	ips = []net.IP{net.ParseIP(localCertIP4Loop), net.ParseIP(localCertIP6Loop)}
	ips = append(ips, lanIPs()...)

	parts := append([]string{}, dns...)
	for _, ip := range ips {
		parts = append(parts, ip.String())
	}
	sort.Strings(parts)
	return dns, ips, strings.Join(parts, ",")
}

// ensureLocalCA returns the CA certificate and private key, loading them from
// disk if valid, or generating and saving them if missing or expired.
func ensureLocalCA(dir string, logf func(...any)) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	caFile := filepath.Join(dir, localCAFilename)
	caKeyFile := filepath.Join(dir, localCAKeyFilename)

	if certPEM, err := os.ReadFile(caFile); err == nil {
		if keyPEM, err := os.ReadFile(caKeyFile); err == nil {
			certBlock, _ := pem.Decode(certPEM)
			keyBlock, _ := pem.Decode(keyPEM)
			if certBlock != nil && keyBlock != nil {
				caCert, err1 := x509.ParseCertificate(certBlock.Bytes)
				caPriv, err2 := x509.ParseECPrivateKey(keyBlock.Bytes)
				if err1 == nil && err2 == nil && now().Before(caCert.NotAfter) {
					return caCert, caPriv, certBlock.Bytes, nil
				}
			}
		}
	}

	caPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, nil, err
	}

	currentTime := now()
	caTemplate := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{localCertOrg}},
		NotBefore:             currentTime,
		NotAfter:              currentTime.Add(localCAValidFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caPriv.PublicKey, caPriv)
	if err != nil {
		return nil, nil, nil, err
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := writePEM(caFile, pemTypeCert, caDER, localSANsFilePerm); err != nil {
		return nil, nil, nil, err
	}

	keyDER, err := x509.MarshalECPrivateKey(caPriv)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := writePEM(caKeyFile, pemTypeECPrivate, keyDER, localKeyFilePerm); err != nil {
		return nil, nil, nil, err
	}

	if os.Getenv(EnvSkipTruststore) == "" {
		if ierr := truststore.Install(caCert); ierr != nil && logf != nil {
			logf("Warning: failed to install local CA certificate in truststore (browsers may show warning):", ierr)
		}
	}

	return caCert, caPriv, caDER, nil
}

// ensureLocalCert returns paths to the local certificate and key, generating
// them on first use and regenerating them whenever the host's address set has
// changed since the cert on disk was written. logf, when non-nil, receives a
// best-effort warning if the CA cannot be installed in the OS truststore.
func ensureLocalCert(logf func(...any)) (certFile, keyFile string, err error) {
	dir, err := localCertDir()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, localCertDirPerm); err != nil {
		return "", "", err
	}

	caCert, caPriv, caDER, err := ensureLocalCA(dir, logf)
	if err != nil {
		return "", "", err
	}

	certFile = filepath.Join(dir, localCertFilename)
	keyFile = filepath.Join(dir, localKeyFilename)
	sansFile := filepath.Join(dir, localSANsFilename)

	dnsNames, ipAddrs, fingerprint := desiredSANs()

	if fresh(certFile, keyFile, sansFile, fingerprint) {
		return certFile, keyFile, nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", err
	}

	currentTime := now()
	leafTemplate := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{localCertOrg}},
		NotBefore:             currentTime,
		NotAfter:              currentTime.Add(localCertValidFor),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddrs,
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, &leafTemplate, caCert, &priv.PublicKey, caPriv)
	if err != nil {
		return "", "", err
	}

	if err := writeChainPEM(certFile, leafDER, caDER); err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	if err := writePEM(keyFile, pemTypeECPrivate, keyDER, localKeyFilePerm); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(sansFile, []byte(fingerprint), localSANsFilePerm); err != nil {
		return "", "", err
	}

	return certFile, keyFile, nil
}

// fresh reports whether the cert, key and SAN record on disk are all present and
// the recorded SAN set still matches the host's current addresses.
func fresh(certFile, keyFile, sansFile, fingerprint string) bool {
	for _, f := range []string{certFile, keyFile} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	recorded, err := os.ReadFile(sansFile)
	if err != nil {
		return false
	}
	if string(recorded) != fingerprint {
		return false
	}
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if now().Add(localCertRenewBefore).After(cert.NotAfter) {
		return false
	}
	return now().Before(cert.NotAfter)
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func writeChainPEM(path string, certDERs ...[]byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, localSANsFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, der := range certDERs {
		if err := pem.Encode(f, &pem.Block{Type: pemTypeCert, Bytes: der}); err != nil {
			return err
		}
	}
	return nil
}

// LocalCertFiles returns paths to the local certificate and key, creating
// and truststore-installing them on first use.
func LocalCertFiles() (certFile, keyFile string, err error) {
	return ensureLocalCert(nil)
}

// LocalCA returns the DER of the local certificate authority — the file a
// device installs to trust this server. It is NOT the certificate the server
// presents; that is the leaf LocalCA signed.
func LocalCA() ([]byte, error) {
	if _, _, err := ensureLocalCert(nil); err != nil {
		return nil, err
	}
	dir, err := localCertDir()
	if err != nil {
		return nil, err
	}
	pemBytes, err := os.ReadFile(filepath.Join(dir, localCAFilename))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errLocalCertDecode
	}
	return block.Bytes, nil
}

// localCertLeafDER returns the DER of the leaf certificate presented by the server.
func localCertLeafDER() ([]byte, error) {
	certFile, _, err := ensureLocalCert(nil)
	if err != nil {
		return nil, err
	}
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errLocalCertDecode
	}
	return block.Bytes, nil
}
