package httpd

// Internal test: it sets the identity through httpContext.userID, as TestRoute_PatchIsGated does.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"webtyp.com/events/inprocess"
	"webtyp.com/model"
	"webtyp.com/router"
	"webtyp.com/securityevent"
)

// TestGate_ReportsASignedInPersonRefused: a signed-in person refused by a permission is an authz_fail security event
// with who and which resource; an anonymous caller refused the same way is not (it is the browser before sign-in).
func TestGate_ReportsASignedInPersonRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		userID string
		want   int
	}{{"signed in", "u1", 1}, {"anonymous", "", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			broker := &inprocess.Broker{}
			var got []securityevent.Event
			if _, err := securityevent.Listen(broker, func(e securityevent.Event) { got = append(got, e) }); err != nil {
				t.Fatal(err)
			}
			report, _ := securityevent.New(broker)
			srv := New(Config{Authorize: func(string, model.Resource, model.Action) bool { return false }, Report: report})
			srv.Router().Handle(http.MethodPatch, "/api/m/patient", func(ctx router.Context) {}).Requires("patient", model.Update)
			userID := tc.userID
			srv.config.Authn = func(next router.HandlerFunc) router.HandlerFunc {
				return func(ctx router.Context) {
					ctx.(*httpContext).userID = userID
					next(ctx)
				}
			}
			h, err := srv.Handler()
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/m/patient", nil))
			if w.Code != http.StatusForbidden || len(got) != tc.want {
				t.Fatalf("status %d, events %+v; want 403 and %d event(s)", w.Code, got, tc.want)
			}
			if tc.want == 1 && (got[0].Kind != securityevent.AccessDenied || got[0].UserID != "u1" || got[0].Resource != "patient") {
				t.Fatalf("event = %+v, want authz_fail by u1 on patient", got[0])
			}
		})
	}
}
