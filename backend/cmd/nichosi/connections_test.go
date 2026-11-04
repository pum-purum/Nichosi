package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfiguredConnectionsReadonly(t *testing.T) {
	for _, tc := range []struct {
		name, setting string
		want          bool
		invalid       bool
	}{
		{name: "true", setting: `,"readonly":true`, want: true},
		{name: "false", setting: `,"readonly":false`, want: false},
		{name: "missing", want: true},
		{name: "null", setting: `,"readonly":null`, want: true},
		{name: "legacy production", setting: `,"environment":"prod"`, want: true},
		{name: "legacy development", setting: `,"environment":"dev"`, want: true},
		{name: "string is invalid", setting: `,"readonly":"false"`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "connections.json")
			config := `{"connections":[{"name":"Local","driver":"mysql","host":"127.0.0.1","port":3306,"user":"tester","database":"lab","password":"test-password"` + tc.setting + `}]}`
			if err := os.WriteFile(file, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NICHOSI_CONNECTIONS_FILE", file)
			application := &app{}
			application.loadConfiguredConnections()
			if tc.invalid {
				if len(application.connections) != 0 {
					t.Fatal("invalid boolean config must not load profiles")
				}
				return
			}
			if len(application.connections) != 1 {
				t.Fatalf("loaded %d profiles, want 1", len(application.connections))
			}
			item := application.connections[0]
			if item.Readonly != tc.want {
				t.Errorf("readonly=%v, want %v", item.Readonly, tc.want)
			}
			if item.input.Readonly == nil || *item.input.Readonly != tc.want {
				t.Error("reconnect input must preserve the normalized readonly setting")
			}
			if item.Connected || item.pool != nil {
				t.Error("configured profiles must start disconnected")
			}
			response := httptest.NewRecorder()
			application.listConnections(response, httptest.NewRequest("GET", "/api/connections", nil))
			body := response.Body.String()
			for _, forbidden := range []string{`"password"`, "test-password", `"environment"`, `"readOnly"`} {
				if strings.Contains(body, forbidden) {
					t.Error("connection response exposes a secret or obsolete property")
				}
			}
			var returned []Connection
			if err := json.Unmarshal(response.Body.Bytes(), &returned); err != nil {
				t.Fatal(err)
			}
			if len(returned) != 1 || returned[0].Readonly != tc.want {
				t.Fatal("connection response must include the configured readonly setting")
			}
		})
	}
}

func TestConnectionInputReadonlyDefault(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
		invalid    bool
	}{
		{name: "missing", body: `{}`, want: true},
		{name: "null", body: `{"readonly":null}`, want: true},
		{name: "true", body: `{"readonly":true}`, want: true},
		{name: "false", body: `{"readonly":false}`, want: false},
		{name: "invalid string", body: `{"readonly":"false"}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input ConnectionInput
			err := json.Unmarshal([]byte(tc.body), &input)
			if tc.invalid {
				if err == nil {
					t.Fatal("string readonly must be rejected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := connectionReadonly(input.Readonly); got != tc.want {
				t.Errorf("readonly=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPostgresConfiguredPasswordEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
		want int
	}{{"available", true, 1}, {"missing", false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("NICHOSI_TEST_PROFILE_PASSWORD", "env-secret")
			} else {
				t.Setenv("NICHOSI_TEST_PROFILE_PASSWORD", "")
			}
			file := filepath.Join(t.TempDir(), "connections.json")
			config := `{"connections":[{"name":"Postgres","driver":"postgres","host":"localhost","port":5432,"user":"app","database":"app","password":"fallback-secret","passwordEnv":"NICHOSI_TEST_PROFILE_PASSWORD","readonly":false}]}`
			if err := os.WriteFile(file, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NICHOSI_CONNECTIONS_FILE", file)
			application := &app{}
			application.loadConfiguredConnections()
			if len(application.connections) != tc.want {
				t.Fatalf("loaded %d profiles", len(application.connections))
			}
			if tc.want == 0 {
				return
			}
			p := application.connections[0]
			if p.Driver != ConnectionDriverPostgres || !p.Readonly || *p.input.Password != "env-secret" {
				t.Fatal("PostgreSQL must use environment credentials and read-only mode")
			}
			response := httptest.NewRecorder()
			application.listConnections(response, httptest.NewRequest("GET", "/api/connections", nil))
			for _, secret := range []string{"env-secret", "fallback-secret", "passwordEnv"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatal("secret exposed")
				}
			}
		})
	}
}

func TestLoadConfiguredConnectionEngine(t *testing.T) {
	for _, tc := range []struct {
		name, driver, engine, want string
	}{
		{"default MySQL", "mysql", "", "mysql"},
		{"explicit MySQL", "mysql", "mysql", "mysql"},
		{"MariaDB", "mysql", "mariadb", "mariadb"},
		{"default PostgreSQL", "postgres", "", "postgres"},
		{"explicit PostgreSQL", "postgres", "postgres", "postgres"},
		{"incompatible MariaDB", "postgres", "mariadb", ""},
		{"incompatible PostgreSQL", "mysql", "postgres", ""},
		{"unknown engine", "mysql", "other", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "connections.json")
			config := connectionsConfig{Connections: []connectionConfig{{
				Name: "Local", Driver: tc.driver, Engine: tc.engine, Host: "localhost", Port: 3306,
				User: "tester", Database: "lab", Password: "test-password",
			}}}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NICHOSI_CONNECTIONS_FILE", file)
			application := &app{}
			application.loadConfiguredConnections()
			if tc.want == "" {
				if len(application.connections) != 0 {
					t.Fatal("incompatible engine must not load")
				}
				return
			}
			response := httptest.NewRecorder()
			application.listConnections(response, httptest.NewRequest("GET", "/api/connections", nil))
			var returned []Connection
			if err := json.Unmarshal(response.Body.Bytes(), &returned); err != nil {
				t.Fatal(err)
			}
			if len(returned) != 1 || returned[0].Engine == nil || string(*returned[0].Engine) != tc.want {
				t.Fatalf("unexpected engine response: %s", response.Body.String())
			}
			if returned[0].Connected {
				t.Fatal("engine display must not require opening a connection")
			}
		})
	}
}
