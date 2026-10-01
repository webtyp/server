package httpd

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAddr is a net.Addr carrying a fixed CIDR, for stubbing interfaceAddrs.
type fakeAddr struct{ s string }

func (f fakeAddr) Network() string { return "ip+net" }
func (f fakeAddr) String() string  { return f.s }

func stubInterfaceAddrs(t *testing.T, cidrs ...string) {
	t.Helper()
	prev := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		out := make([]net.Addr, 0, len(cidrs))
		for _, c := range cidrs {
			ip, ipnet, err := net.ParseCIDR(c)
			if err != nil {
				t.Fatalf("bad test CIDR %q: %v", c, err)
			}
			ipnet.IP = ip
			out = append(out, ipnet)
		}
		return out, nil
	}
	t.Cleanup(func() { interfaceAddrs = prev })
}

func loadCert(t *testing.T, certFile string) *x509.Certificate {
	t.Helper()
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("reading cert: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("cert file is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing cert: %v", err)
	}
	return cert
}

func hasIP(ips []net.IP, want string) bool {
	w := net.ParseIP(want)
	for _, ip := range ips {
		if ip.Equal(w) {
			return true
		}
	}
	return false
}

func TestLocalCert_CoversLoopbackAndLAN(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "192.168.1.50/24", "10.0.0.7/8", "127.0.0.1/8", "::1/128", "fe80::1/64")

	certFile, _, err := ensureLocalCert(nil)
	if err != nil {
		t.Fatalf("ensureLocalCert: %v", err)
	}
	cert := loadCert(t, certFile)

	foundLocalhost := false
	for _, n := range cert.DNSNames {
		if n == localCertHostname {
			foundLocalhost = true
		}
	}
	if !foundLocalhost {
		t.Errorf("DNSNames %v missing %q", cert.DNSNames, localCertHostname)
	}
	for _, want := range []string{"127.0.0.1", "::1", "192.168.1.50", "10.0.0.7"} {
		if !hasIP(cert.IPAddresses, want) {
			t.Errorf("IPAddresses %v missing %s", cert.IPAddresses, want)
		}
	}
	if hasIP(cert.IPAddresses, "fe80::1") {
		t.Errorf("IPAddresses unexpectedly contains link-local fe80::1")
	}
}

func TestLocalCert_RegeneratesWhenAddressSetChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stubInterfaceAddrs(t, "192.168.1.50/24")
	certFile, _, err := ensureLocalCert(nil)
	if err != nil {
		t.Fatalf("ensureLocalCert #1: %v", err)
	}
	first := loadCert(t, certFile)
	if !hasIP(first.IPAddresses, "192.168.1.50") {
		t.Fatalf("first cert missing 192.168.1.50: %v", first.IPAddresses)
	}

	stubInterfaceAddrs(t, "192.168.9.9/24")
	if _, _, err := ensureLocalCert(nil); err != nil {
		t.Fatalf("ensureLocalCert #2: %v", err)
	}
	second := loadCert(t, certFile)

	if second.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("certificate was not regenerated after the address set changed")
	}
	if hasIP(second.IPAddresses, "192.168.1.50") {
		t.Errorf("regenerated cert still names the old address 192.168.1.50: %v", second.IPAddresses)
	}
	if !hasIP(second.IPAddresses, "192.168.9.9") {
		t.Errorf("regenerated cert missing the new address 192.168.9.9: %v", second.IPAddresses)
	}
}

func TestLocalCert_ReusesCertWhenAddressSetUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "192.168.1.50/24")

	certFile, _, err := ensureLocalCert(nil)
	if err != nil {
		t.Fatalf("ensureLocalCert #1: %v", err)
	}
	first := loadCert(t, certFile)

	if _, _, err := ensureLocalCert(nil); err != nil {
		t.Fatalf("ensureLocalCert #2: %v", err)
	}
	second := loadCert(t, certFile)

	if first.SerialNumber.Cmp(second.SerialNumber) != 0 {
		t.Error("certificate was regenerated even though the address set did not change")
	}
}

func TestLocalCertSPKI_StableBase64(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "192.168.1.50/24")

	if _, _, err := ensureLocalCert(nil); err != nil {
		t.Fatalf("ensureLocalCert: %v", err)
	}

	first, err := LocalCertSPKI()
	if err != nil {
		t.Fatalf("LocalCertSPKI: %v", err)
	}
	if len(first) != 44 {
		t.Errorf("SPKI hash length = %d, want 44 (%q)", len(first), first)
	}
	second, err := LocalCertSPKI()
	if err != nil {
		t.Fatalf("LocalCertSPKI (again): %v", err)
	}
	if first != second {
		t.Errorf("LocalCertSPKI not stable: %q != %q", first, second)
	}
}

func TestLocalCA_IsParseableDER(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "192.168.1.50/24")

	der, err := LocalCA()
	if err != nil {
		t.Fatalf("LocalCA: %v", err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("LocalCA did not return valid DER: %v", err)
	}
	if !caCert.IsCA {
		t.Error("LocalCA certificate IsCA = false, want true")
	}
	if caCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("LocalCA certificate missing KeyUsageCertSign")
	}
	dir, _ := localCertDir()
	if _, err := os.Stat(filepath.Join(dir, localCAFilename)); err != nil {
		t.Fatalf("expected ca cert on disk: %v", err)
	}
}

func TestLocalCertSPKI_PointsToLeafNotCA(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "192.168.1.50/24")

	spki, err := LocalCertSPKI()
	if err != nil {
		t.Fatalf("LocalCertSPKI: %v", err)
	}

	leafDER, err := localCertLeafDER()
	if err != nil {
		t.Fatalf("localCertLeafDER: %v", err)
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parsing leaf cert: %v", err)
	}
	leafSum := sha256.Sum256(leafCert.RawSubjectPublicKeyInfo)
	expectedLeafSPKI := base64.StdEncoding.EncodeToString(leafSum[:])

	caDER, err := LocalCA()
	if err != nil {
		t.Fatalf("LocalCA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parsing CA cert: %v", err)
	}
	caSum := sha256.Sum256(caCert.RawSubjectPublicKeyInfo)
	caSPKI := base64.StdEncoding.EncodeToString(caSum[:])

	if spki != expectedLeafSPKI {
		t.Errorf("LocalCertSPKI() = %q, want leaf SPKI %q", spki, expectedLeafSPKI)
	}
	if spki == caSPKI {
		t.Errorf("LocalCertSPKI() unexpectedly equal to CA SPKI %q", caSPKI)
	}
}

func TestLocalTLS_CAEndpointConsumerShaped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "127.0.0.1/8")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving port: %v", err)
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

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	url := fmt.Sprintf("https://127.0.0.1:%s%s", port, CAPath)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != CADownloadContentType {
		t.Errorf("Content-Type = %q, want %q", ct, CADownloadContentType)
	}

	caBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	caCert, err := x509.ParseCertificate(caBytes)
	if err != nil {
		t.Fatalf("failed to parse CA certificate from /__webtyp/ca: %v", err)
	}

	if !caCert.IsCA {
		t.Error("CA certificate IsCA = false, want true")
	}
	if caCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA certificate missing KeyUsageCertSign")
	}

	if openssl, err := exec.LookPath("openssl"); err == nil {
		tmpDir := t.TempDir()
		caPath := filepath.Join(tmpDir, "ca.crt")
		leafPath := filepath.Join(tmpDir, "leaf.crt")

		_ = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes}), 0644)
		_ = os.WriteFile(leafPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: resp.TLS.PeerCertificates[0].Raw}), 0644)

		outCA, err1 := exec.Command(openssl, "x509", "-in", caPath, "-noout", "-ext", "basicConstraints").CombinedOutput()
		if err1 != nil || !strings.Contains(string(outCA), "CA:TRUE") {
			t.Errorf("openssl basicConstraints for CA = %q (err %v), want CA:TRUE", string(outCA), err1)
		}

		outLeaf, err2 := exec.Command(openssl, "x509", "-in", leafPath, "-noout", "-ext", "basicConstraints").CombinedOutput()
		if err2 != nil || !strings.Contains(string(outLeaf), "CA:FALSE") {
			t.Errorf("openssl basicConstraints for Leaf = %q (err %v), want CA:FALSE", string(outLeaf), err2)
		}
	}

	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		t.Fatal("no peer certificates presented in TLS handshake")
	}
	leafCert := resp.TLS.PeerCertificates[0]
	if leafCert.IsCA {
		t.Error("presented leaf certificate has IsCA = true, want false")
	}

	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	opts := x509.VerifyOptions{
		Roots:       roots,
		DNSName:     "localhost",
		CurrentTime: time.Now(),
	}
	if _, err := leafCert.Verify(opts); err != nil {
		t.Errorf("leaf certificate failed to verify against CA: %v", err)
	}
}

func TestLocalCert_IncludesHostname(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prevHostname := hostname
	hostname = func() (string, error) {
		return "Servidor", nil
	}
	t.Cleanup(func() { hostname = prevHostname })

	certFile, _, err := ensureLocalCert(nil)
	if err != nil {
		t.Fatalf("ensureLocalCert: %v", err)
	}
	cert := loadCert(t, certFile)

	foundServidor := false
	for _, name := range cert.DNSNames {
		if name == "servidor" {
			foundServidor = true
			break
		}
	}
	if !foundServidor {
		t.Errorf("DNSNames %v missing %q", cert.DNSNames, "servidor")
	}
}

func TestLocalCert_RenewsBeforeExpiry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "127.0.0.1/8")

	lc := &localCert{}
	tlsCert1, err := lc.get(nil)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	x509Cert1, err := x509.ParseCertificate(tlsCert1.Certificate[0])
	if err != nil {
		t.Fatalf("parsing cert 1: %v", err)
	}

	prevNow := now
	// Stub now to NotAfter − 29 days (which is inside localCertRenewBefore = 30 days window)
	now = func() time.Time {
		return x509Cert1.NotAfter.Add(-29 * 24 * time.Hour)
	}
	t.Cleanup(func() { now = prevNow })

	tlsCert2, err := lc.get(nil)
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	x509Cert2, err := x509.ParseCertificate(tlsCert2.Certificate[0])
	if err != nil {
		t.Fatalf("parsing cert 2: %v", err)
	}

	if !x509Cert2.NotAfter.After(x509Cert1.NotAfter) {
		t.Errorf("expected renewed cert NotAfter (%v) to be after original NotAfter (%v)", x509Cert2.NotAfter, x509Cert1.NotAfter)
	}
}

func TestLocalCert_ReusesFreshLeaf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubInterfaceAddrs(t, "127.0.0.1/8")

	lc := &localCert{}
	tlsCert1, err := lc.get(nil)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	x509Cert1, err := x509.ParseCertificate(tlsCert1.Certificate[0])
	if err != nil {
		t.Fatalf("parsing cert 1: %v", err)
	}

	prevNow := now
	startTime := now()
	now = func() time.Time {
		return startTime.Add(10 * time.Minute)
	}
	t.Cleanup(func() { now = prevNow })

	tlsCert2, err := lc.get(nil)
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	x509Cert2, err := x509.ParseCertificate(tlsCert2.Certificate[0])
	if err != nil {
		t.Fatalf("parsing cert 2: %v", err)
	}

	if x509Cert1.SerialNumber.Cmp(x509Cert2.SerialNumber) != 0 {
		t.Errorf("expected same serial number, got %v and %v", x509Cert1.SerialNumber, x509Cert2.SerialNumber)
	}
}
