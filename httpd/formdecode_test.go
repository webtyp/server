package httpd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/model"
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

func TestFormDecode_MalformedBody(t *testing.T) {
	// 7. Cuerpo mal formado
	ctx := newTestContext(http.MethodPost, "application/x-www-form-urlencoded", []byte("code=%zz"))
	var target testDecodable
	err := ctx.Decode(&target)
	if err == nil {
		t.Fatalf("expected error for malformed body, got nil")
	}
}
