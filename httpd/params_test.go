package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/router"
)

func TestParamFromServeMux(t *testing.T) {
	s := New(Config{})
	var gotParam string
	s.Router().Get("/api/items/{id}", func(ctx router.Context) {
		gotParam = ctx.Param("id")
		ctx.WriteStatus(http.StatusOK)
	}).Public()

	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/items/42", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if gotParam != "42" {
		t.Errorf("expected param '42', got %q", gotParam)
	}
}

func TestParamIsNotAContextValue(t *testing.T) {
	s := New(Config{})
	var gotValue string
	s.Router().Get("/api/items/{id}", func(ctx router.Context) {
		gotValue = ctx.Value("id")
		ctx.WriteStatus(http.StatusOK)
	}).Public()

	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/items/42", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if gotValue != "" {
		t.Errorf("expected ctx.Value(\"id\") to be \"\", got %q", gotValue)
	}
}

func TestRegistrationRejectsWildcard(t *testing.T) {
	s := New(Config{})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when registering wildcard pattern, got nil")
		}
	}()

	s.Router().Get("/x/{a...}", func(ctx router.Context) {})
}

// router v0.5.0 contract: Path() returns the path only and RawQuery() returns the query string.
func TestPathAndRawQuery(t *testing.T) {
	s := New(Config{})
	var gotPath, gotQuery, gotCursor string
	s.Router().Get("/api/pull", func(ctx router.Context) {
		gotPath = ctx.Path()
		gotQuery = ctx.RawQuery()
		reader := router.QueryReader(ctx.RawQuery())
		gotCursor, _ = reader.String("cursor")
		ctx.WriteStatus(http.StatusOK)
	}).Public()

	handler, err := s.Handler()
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/pull?cursor=42&limit=5", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if gotPath != "/api/pull" {
		t.Errorf("Path() = %q, want only the path", gotPath)
	}
	if gotQuery != "cursor=42&limit=5" {
		t.Errorf("RawQuery() = %q, want the query string", gotQuery)
	}
	if gotCursor != "42" {
		t.Errorf("QueryReader(cursor) = %q, want %q", gotCursor, "42")
	}
}
