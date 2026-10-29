package httpd

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"webtyp.com/model"
	"webtyp.com/router"
)

func setupNetworkTestServer(t *testing.T) *Server {
	s := New(Config{
		TLS: TLSConfig{PlainHTTP: true},
		Authn: func(next router.HandlerFunc) router.HandlerFunc {
			return func(ctx router.Context) {
				ctx.SetUserID("u1")
				assuranceStr := ctx.GetHeader("X-Test-Assurance")
				if assurance, err := strconv.Atoi(assuranceStr); err == nil {
					ctx.SetAssurance(model.Assurance(assurance))
				}
				next(ctx)
			}
		},
		Authorize: func(userID string, resource model.Resource, action model.Action) bool {
			return true
		},
	})

	s.Router().Get("/pub", func(ctx router.Context) {
		ctx.WriteStatus(http.StatusOK)
		ctx.Write([]byte("ok"))
	}).Public()

	s.Router().Get("/me", func(ctx router.Context) {
		ctx.WriteStatus(http.StatusOK)
		ctx.Write([]byte("ok"))
	}).Authenticated()

	s.Router().Get("/data", func(ctx router.Context) {
		ctx.WriteStatus(http.StatusOK)
		ctx.Write([]byte("ok"))
	}).Requires(model.Resource("data"), model.Read)

	return s
}

func TestLocalDoor_SingleFactorPasses(t *testing.T) {
	s := setupNetworkTestServer(t)
	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data", nil)
	req.Header.Set("X-Test-Assurance", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestInternetDoor_SingleFactorRefused(t *testing.T) {
	s := setupNetworkTestServer(t)
	handler, err := s.InternetHandler()
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data", nil)
	req.Header.Set("X-Test-Assurance", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}
	if challenge := resp.Header.Get(router.HeaderWWWAuthenticate); challenge != router.ChallengeMFARequired {
		t.Errorf("Expected challenge %q, got %q", router.ChallengeMFARequired, challenge)
	}
}

func TestInternetDoor_AuthenticatedSingleFactorRefused(t *testing.T) {
	s := setupNetworkTestServer(t)
	handler, err := s.InternetHandler()
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/me", nil)
	req.Header.Set("X-Test-Assurance", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestInternetDoor_PasskeyPasses(t *testing.T) {
	s := setupNetworkTestServer(t)
	handler, err := s.InternetHandler()
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data", nil)
	req.Header.Set("X-Test-Assurance", "2") // Multi-factor

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestInternetDoor_PublicPasses(t *testing.T) {
	s := setupNetworkTestServer(t)
	handler, err := s.InternetHandler()
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/pub", nil)
	// No assurance set

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestDoors_HandlerSeesNetwork(t *testing.T) {
	s := setupNetworkTestServer(t)
	s.Router().Get("/check-network", func(ctx router.Context) {
		fromInternet := router.FromInternet(ctx)
		ctx.WriteStatus(http.StatusOK)
		if fromInternet {
			ctx.Write([]byte("internet"))
		} else {
			ctx.Write([]byte("local"))
		}
	}).Public()

	// Local door
	localHandler, _ := s.Handler()
	tsLocal := httptest.NewServer(localHandler)
	defer tsLocal.Close()

	respLocal, _ := http.Get(tsLocal.URL + "/check-network")
	defer respLocal.Body.Close()
	bufLocal := make([]byte, 20)
	n, _ := respLocal.Body.Read(bufLocal)
	if string(bufLocal[:n]) != "local" {
		t.Errorf("Expected local door to see network as local, got %s", string(bufLocal[:n]))
	}

	// Internet door
	internetHandler, _ := s.InternetHandler()
	tsInternet := httptest.NewServer(internetHandler)
	defer tsInternet.Close()

	respInternet, _ := http.Get(tsInternet.URL + "/check-network")
	defer respInternet.Body.Close()
	bufInternet := make([]byte, 20)
	n, _ = respInternet.Body.Read(bufInternet)
	if string(bufInternet[:n]) != "internet" {
		t.Errorf("Expected internet door to see network as internet, got %s", string(bufInternet[:n]))
	}
}

func TestInternetHandler_SameRoutes(t *testing.T) {
	s := setupNetworkTestServer(t)
	handler, err := s.InternetHandler()
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/missing", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, resp.StatusCode)
	}
}
