package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRowFilterHTTPValidation(t *testing.T) {
	for _, tc := range []struct {
		name, filter string
		status       int
	}{
		{"scalar", `{"column":"id","op":"eq","value":9007199254740993}`, 200},
		{"range", `{"column":"id","op":"between","value":[1,9]}`, 200},
		{"null check", `{"column":"id","op":"isNull"}`, 200},
		{"missing column", `{"column":"other","op":"eq","value":1}`, 400},
		{"operator injection", `{"column":"id","op":"= 1 OR 1=1","value":1}`, 400},
		{"connector injection", `{"column":"id","op":"eq","value":1,"connector":"OR 1=1"}`, 400},
		{"missing value", `{"column":"id","op":"eq"}`, 400},
		{"bad range", `{"column":"id","op":"between","value":[1]}`, 400},
		{"bad member", `{"column":"id","op":"in","value":[{}]}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &viewRowsConnector{}
			pool := sql.OpenDB(connector)
			defer pool.Close()
			application := &app{connections: []*profile{{Connection: Connection{Id: "test", Database: "demo"}, pool: pool}}}
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"filters":[`+tc.filter+`]}`))
			request.SetPathValue("connId", "test")
			request.SetPathValue("view", "v")
			response := httptest.NewRecorder()
			application.queryViewRows(response, request)
			if response.Code != tc.status {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
			if tc.status == 400 && !strings.Contains(response.Body.String(), `"invalid_filter"`) {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestFilteredRowsErrorsDoNotExposeValues(t *testing.T) {
	pool := sql.OpenDB(tableCheckConnector{err: errors.New("secret filter value in database error")})
	defer pool.Close()
	application := &app{connections: []*profile{{Connection: Connection{Id: "test", Database: "demo"}, pool: pool}}}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"filters":[{"column":"id","op":"eq","value":"secret"}]}`))
	request.SetPathValue("connId", "test")
	request.SetPathValue("table", "t")
	response := httptest.NewRecorder()
	application.queryRows(response, request)
	if response.Code != 502 || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("unsafe error: %d %s", response.Code, response.Body.String())
	}
}
