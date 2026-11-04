package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetObjectDefinitionValidation(t *testing.T) {
	application := &app{connections: []*profile{{Connection: Connection{Id: "closed"}}}}
	for _, tc := range []struct {
		name, kind, object, connection string
		status                         int
	}{
		{"account SQL forbidden", "user", "root", "closed", http.StatusBadRequest},
		{"role SQL forbidden", "role", "reader", "closed", http.StatusBadRequest},
		{"injected type", "EVENT; SHOW CREATE USER root", "event", "closed", http.StatusBadRequest},
		{"missing type", "", "event", "closed", http.StatusBadRequest},
		{"missing name", "event", "", "closed", http.StatusBadRequest},
		{"unknown connection", "event", "event", "unknown", http.StatusNotFound},
		{"disconnected", "event", "event", "closed", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"type": {tc.kind}, "name": {tc.object}}
			request := httptest.NewRequest(http.MethodGet, "/api/connections/"+tc.connection+"/object-definition?"+query.Encode(), nil)
			request.SetPathValue("connId", tc.connection)
			response := httptest.NewRecorder()
			application.getObjectDefinition(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}

func TestGetObjectDefinitionPreservesDriverError(t *testing.T) {
	source := errors.New("Error 1142: SHOW CREATE denied\n<object> & details")
	pool := sql.OpenDB(tableCheckConnector{err: source})
	defer pool.Close()
	application := &app{connections: []*profile{{Connection: Connection{Id: "test", Database: "demo"}, pool: pool}}}
	request := httptest.NewRequest(http.MethodGet, "/?type=event&name=demo", nil)
	request.SetPathValue("connId", "test")
	response := httptest.NewRecorder()
	application.getObjectDefinition(response, request)
	var body DatabaseObject
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Details == nil || (*body.Details)["Definition status"] != source.Error() {
		t.Fatalf("object status did not preserve source error: %s", response.Body.String())
	}
}

func TestGetObjectDefinitionDriverAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name   string
		driver ConnectionDriver
		kind   string
		status int
	}{
		{"PostgreSQL event forbidden", ConnectionDriverPostgres, "event", http.StatusBadRequest},
		{"MySQL materialized view forbidden", ConnectionDriverMysql, "materialized_view", http.StatusBadRequest},
		{"MySQL cron forbidden", ConnectionDriverMysql, "cron", http.StatusBadRequest},
		{"PostgreSQL materialized view accepted", ConnectionDriverPostgres, "materialized_view", http.StatusBadGateway},
		{"PostgreSQL schema accepted", ConnectionDriverPostgres, "schema", http.StatusBadGateway},
		{"PostgreSQL sequence accepted", ConnectionDriverPostgres, "sequence", http.StatusBadGateway},
		{"PostgreSQL type accepted", ConnectionDriverPostgres, "type", http.StatusBadGateway},
		{"PostgreSQL extension accepted", ConnectionDriverPostgres, "extension", http.StatusBadGateway},
		{"PostgreSQL index accepted", ConnectionDriverPostgres, "index", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := sql.OpenDB(tableCheckConnector{err: errors.New("test query reached")})
			defer pool.Close()
			application := &app{connections: []*profile{{Connection: Connection{Id: "test", Driver: tc.driver}, pool: pool}}}
			request := httptest.NewRequest(http.MethodGet, "/?type="+tc.kind+"&name=object", nil)
			request.SetPathValue("connId", "test")
			response := httptest.NewRecorder()
			application.getObjectDefinition(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}
