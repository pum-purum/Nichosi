package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChangeRowsAccess(t *testing.T) {
	writable := false
	for _, tc := range []struct {
		name    string
		profile *profile
		status  int
	}{
		{"unknown", nil, 404},
		{"readonly default", &profile{Connection: Connection{Id: "test"}}, 403},
		{"readonly explicit", &profile{Connection: Connection{Id: "test", Readonly: true}, input: ConnectionInput{Readonly: &writable}}, 403},
		{"disconnected", &profile{Connection: Connection{Id: "test"}, input: ConnectionInput{Readonly: &writable}}, 409},
	} {
		for _, apply := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				a := &app{}
				if tc.profile != nil {
					a.connections = []*profile{tc.profile}
				}
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"changes":[]}`))
				r.SetPathValue("connId", "test")
				r.SetPathValue("table", "t")
				w := httptest.NewRecorder()
				a.changeRows(w, r, apply)
				if w.Code != tc.status {
					t.Fatalf("got %d: %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
