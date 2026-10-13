package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/server/httpd"
	"webtyp.com/model"
	"webtyp.com/router"
)

func TestStreamRequiresMFA(t *testing.T) {
	streamRun := false

	h := httpd.New(httpd.Config{
		TLS: httpd.TLSConfig{PlainHTTP: true},
		Authn: func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				ctx.SetUserID("u1")
				ctx.SetAssurance(model.AssuranceSingleFactor)
				next(ctx)
			}
		},
	})

	h.Router().Stream("/stream", func(s router.Streamer) {
		streamRun = true
		s.Write([]byte("ok"))
	}).Authenticated().RequiresMFA()

	handler, err := h.Handler()
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}

	req := httptest.NewRequest("GET", "/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", res.StatusCode)
	}

	if streamRun {
		t.Errorf("stream handler was executed despite MFA rejection")
	}

	challenge := res.Header.Get(router.HeaderWWWAuthenticate)
	if challenge != router.ChallengeMFARequired {
		t.Errorf("expected challenge %q, got %q", router.ChallengeMFARequired, challenge)
	}
}

func TestAnonymousAssuranceIsNone(t *testing.T) {
	h := httpd.New(httpd.Config{
		TLS: httpd.TLSConfig{PlainHTTP: true},
		Authn: func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				// Set assurance high, but user is anonymous (no SetUserID)
				ctx.SetAssurance(model.AssuranceMultiFactor)
				next(ctx)
			}
		},
	})

	h.Router().Get("/route", func(ctx router.Context) {
		ctx.WriteStatus(http.StatusOK)
	}).Authenticated().RequiresMFA()

	handler, err := h.Handler()
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}

	req := httptest.NewRequest("GET", "/route", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	res := rec.Result()
	// Anonymous identity must be rejected before MFA check.
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", res.StatusCode)
	}
}
