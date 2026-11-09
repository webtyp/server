package httpd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/model"
	"webtyp.com/router"
)

type testDecodable struct {
	Code   string `json:"code"`
	Qty    int    `json:"qty"`
	Active bool   `json:"active"`
}

func (m *testDecodable) IsNil() bool {
	return m == nil
}

func (m *testDecodable) DecodeFields(r model.FieldReader) {
	if v, ok := r.String("code"); ok {
		m.Code = v
	}
	if v, ok := r.Int("qty"); ok {
		m.Qty = int(v)
	}
	if v, ok := r.Bool("active"); ok {
		m.Active = v
	}
}

func newTestContext(method, contentType string, body []byte) *httpContext {
	req := httptest.NewRequest(method, "/", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	return &httpContext{
		w: w,
		r: req,
	}
}

func newTestContextAt(method, target string, body []byte) *httpContext {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	return &httpContext{w: httptest.NewRecorder(), r: req}
}

func TestDecode_GetReadsTheQuery(t *testing.T) {
	ctx := newTestContextAt(http.MethodGet, "/api/m/e?code=abc&qty=3&active=true", nil)
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "abc" || target.Qty != 3 || !target.Active {
		t.Errorf("expected code=abc, qty=3, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
	}
}

func TestDecode_GetIgnoresTheBody(t *testing.T) {
	ctx := newTestContextAt(http.MethodGet, "/x?code=q", []byte(`{"code":"body"}`))
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "q" {
		t.Errorf("expected code=q; got code=%q", target.Code)
	}
}

func TestDecode_GetWithoutQueryLeavesZeroValues(t *testing.T) {
	ctx := newTestContextAt(http.MethodGet, "/x", nil)
	target := testDecodable{Qty: 42}
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Qty != 42 {
		t.Errorf("expected qty=42; got qty=%d", target.Qty)
	}
}

func TestDecode_GetEscapedValues(t *testing.T) {
	ctx := newTestContextAt(http.MethodGet, "/x?code=a%20b%26c", nil)
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "a b&c" {
		t.Errorf("expected code='a b&c'; got code=%q", target.Code)
	}
}

func TestDecode_PatchAndDeleteReadTheBody(t *testing.T) {
	methods := []string{http.MethodPatch, http.MethodDelete}
	for _, m := range methods {
		ctx := newTestContextAt(m, "/x?code=ignored", []byte(`{"code":"abc","qty":3,"active":true}`))
		ctx.r.Header.Set("Content-Type", "application/json")
		var target testDecodable
		if err := ctx.Decode(&target); err != nil {
			t.Fatalf("unexpected error for %s: %v", m, err)
		}
		if target.Code != "abc" || target.Qty != 3 || !target.Active {
			t.Errorf("expected code=abc, qty=3, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
		}
	}
}

type nestedTestDecodable struct {
	Inner struct {
		Name string
	}
}

func (m *nestedTestDecodable) IsNil() bool {
	return m == nil
}

func (m *nestedTestDecodable) DecodeFields(r model.FieldReader) {
	var inner struct{ Name string }
	if ok := r.Object("filter", &innerDecodable{&inner.Name}); ok {
		m.Inner = inner
	}
}

type innerDecodable struct {
	Name *string
}

func (m *innerDecodable) IsNil() bool {
	return m == nil
}

func (m *innerDecodable) DecodeFields(r model.FieldReader) {
	if v, ok := r.String("name"); ok {
		*m.Name = v
	}
}

func TestDecode_GetNestedObjectUsesPrefix(t *testing.T) {
	ctx := newTestContextAt(http.MethodGet, "/x?filter.name=x", nil)
	var target nestedTestDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Inner.Name != "x" {
		t.Errorf("expected Inner.Name=x; got %q", target.Inner.Name)
	}
}

func TestRoute_PatchIsGated(t *testing.T) {
	cases := []struct {
		name       string
		allow      bool
		expectCode int
		expectRun  bool
	}{
		{"Denied", false, http.StatusForbidden, false},
		{"Allowed", true, http.StatusOK, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authorizer := func(userID string, resource model.Resource, action model.Action) bool {
				return tc.allow
			}
			srv := New(Config{
				Authorize: authorizer,
			})

			called := false
			h := func(ctx router.Context) {
				called = true
				ctx.WriteStatus(http.StatusOK)
			}
			srv.Router().Handle(http.MethodPatch, "/api/m/e", h).Requires("e", model.Update)

			req := httptest.NewRequest(http.MethodPatch, "/api/m/e", nil)
			w := httptest.NewRecorder()

			// Simulate identity
			srv.config.Authn = func(next router.HandlerFunc) router.HandlerFunc {
				return func(ctx router.Context) {
					ctx.(*httpContext).userID = "u1"
					next(ctx)
				}
			}

			hdlr, err := srv.Handler()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hdlr.ServeHTTP(w, req)

			if w.Code != tc.expectCode {
				t.Errorf("expected status %d, got %d", tc.expectCode, w.Code)
			}
			if called != tc.expectRun {
				t.Errorf("expected handler run=%v, got %v", tc.expectRun, called)
			}
		})
	}
}

func TestFormDecode_FormUrlEncoded(t *testing.T) {
	// 1. Formulario se decodifica
	ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded", []byte("code=abc&qty=3&active=on"))
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "abc" || target.Qty != 3 || !target.Active {
		t.Errorf("expected code=abc, qty=3, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
	}
}

func TestFormDecode_CharsetHandling(t *testing.T) {
	// 2. El charset no rompe la detección
	ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded; charset=UTF-8", []byte("code=abc&qty=3&active=on"))
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "abc" || target.Qty != 3 || !target.Active {
		t.Errorf("expected code=abc, qty=3, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
	}
}

func TestFormDecode_JSONExplicitContentType(t *testing.T) {
	// 3. JSON sigue funcionando
	ctx := newTestContext(http.MethodPost, "application/json", []byte(`{"code":"abc","qty":3,"active":true}`))
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "abc" || target.Qty != 3 || !target.Active {
		t.Errorf("expected code=abc, qty=3, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
	}
}

func TestFormDecode_JSONMissingContentType(t *testing.T) {
	// 4. Sin Content-Type => JSON
	ctx := newTestContext(http.MethodPost, "", []byte(`{"code":"abc","qty":3,"active":true}`))
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "abc" || target.Qty != 3 || !target.Active {
		t.Errorf("expected code=abc, qty=3, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
	}
}

func TestFormDecode_MissingKeysPreserveZeroValues(t *testing.T) {
	// 5. Clave ausente no pisa
	ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded", []byte("code=abc"))
	target := testDecodable{Qty: 10, Active: true}
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "abc" || target.Qty != 10 || !target.Active {
		t.Errorf("expected code=abc, qty=10, active=true; got code=%q, qty=%d, active=%v", target.Code, target.Qty, target.Active)
	}
}

func TestFormDecode_CheckboxValues(t *testing.T) {
	// 6. Checkbox
	cases := []struct {
		body     string
		expected bool
	}{
		{"active=on", true},
		{"active=", false},
	}

	for _, tc := range cases {
		ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded", []byte(tc.body))
		var target testDecodable
		if err := ctx.Decode(&target); err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.body, err)
		}
		if target.Active != tc.expected {
			t.Errorf("for body %q expected active=%v, got %v", tc.body, tc.expected, target.Active)
		}
	}

	// active=banana -> el segundo retorno es false (clave no valida), no debe cambiar target
	ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded", []byte("active=banana"))
	target := testDecodable{Active: true}
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !target.Active {
		t.Errorf("expected active=true (unchanged for invalid bool value 'banana'), got false")
	}
}

func TestFormDecode_MalformedEscapeIsOpaque(t *testing.T) {
	ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded", []byte("code=%zz"))
	var target testDecodable
	if err := ctx.Decode(&target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Code != "%zz" {
		t.Errorf("expected code=%%zz, got %q", target.Code)
	}
}
