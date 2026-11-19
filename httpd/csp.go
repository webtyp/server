package httpd

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"webtyp.com/router/security"
	"webtyp.com/securityevent"
)

// CSPReportPath is the framework endpoint browsers post CSP violation reports to.
const CSPReportPath = "/_csp-report"

const (
	cspManifestFile    = "csp.json"
	maxCSPReportBytes  = 64 << 10
	maxCSPFieldLen     = 256
	reportTypeCSPViol  = "csp-violation"
)

type cspManifest struct {
	ScriptHashes []string `json:"script_hashes"`
}

type cspHashCache struct {
	mu      sync.Mutex
	modTime time.Time
	hasFile bool
	hashes  []string
}

func (c *cspHashCache) load(publicDir string) []string {
	if publicDir == "" {
		return nil
	}
	p := filepath.Join(publicDir, cspManifestFile)
	info, err := os.Stat(p)
	if err != nil {
		c.mu.Lock()
		c.hasFile = false
		c.modTime = time.Time{}
		c.hashes = nil
		c.mu.Unlock()
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasFile && info.ModTime().Equal(c.modTime) {
		return c.hashes
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return c.hashes
	}
	var m cspManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return c.hashes
	}
	c.hasFile = true
	c.modTime = info.ModTime()
	c.hashes = m.ScriptHashes
	return c.hashes
}

func (s *Server) effectiveSecurityPolicy() security.Policy {
	p := s.config.SecurityPolicy.ReportEndpoint(CSPReportPath)
	if hashes := s.cspCache.load(s.config.PublicDir); len(hashes) > 0 {
		p = p.ScriptHashes(hashes...)
	}
	if s.hasReporter() {
		p = p.WithReporter(s.config.Report)
	}
	return p
}

func (s *Server) hasReporter() bool {
	return s.config.Report != (securityevent.Reporter{})
}

type reportingAPIV1Item struct {
	Type string `json:"type"`
	Body struct {
		BlockedURL         string `json:"blockedURL"`
		EffectiveDirective string `json:"effectiveDirective"`
	} `json:"body"`
}

type legacyCSPReport struct {
	CSPReport struct {
		BlockedURI        string `json:"blocked-uri"`
		ViolatedDirective string `json:"violated-directive"`
	} `json:"csp-report"`
}

func truncateCSPField(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxCSPFieldLen {
		return s[:maxCSPFieldLen]
	}
	return s
}

func requestIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			fwd = fwd[:i]
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) handleCSPReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxCSPReportBytes))
	if err != nil || len(raw) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ip := requestIP(r)

	var v1 []reportingAPIV1Item
	if err := json.Unmarshal(raw, &v1); err == nil && len(v1) > 0 {
		for _, item := range v1 {
			if item.Type != reportTypeCSPViol {
				continue
			}
			s.reportCSPViolation(ip, item.Body.BlockedURL, item.Body.EffectiveDirective)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var legacy legacyCSPReport
	if err := json.Unmarshal(raw, &legacy); err == nil &&
		(legacy.CSPReport.BlockedURI != "" || legacy.CSPReport.ViolatedDirective != "") {
		s.reportCSPViolation(ip, legacy.CSPReport.BlockedURI, legacy.CSPReport.ViolatedDirective)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reportCSPViolation(ip, blockedURL, directive string) {
	if !s.hasReporter() {
		return
	}
	_ = s.config.Report.Report(securityevent.Event{
		Kind:     securityevent.ContentBlocked,
		IP:       ip,
		Resource: truncateCSPField(blockedURL),
		Reason:   securityevent.Reason(truncateCSPField(directive)),
	})
}
