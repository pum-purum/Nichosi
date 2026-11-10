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
			file := filepath.Join(t.TempDir(), "config.json")
			config := `{"connections":[{"name":"Local","driver":"mysql","host":"127.0.0.1","port":3306,"user":"tester","database":"lab","password":"test-password"` + tc.setting + `}]}`
			if err := os.WriteFile(file, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NICHOSI_CONNECTIONS_FILE", file)
			application := &app{}
			application.loadConfiguredConnections()
			if tc.invalid {
				if len(application.connections) != 1 || application.connections[0].ConfigurationErrors == nil {
					t.Fatal("invalid boolean config must remain visible with errors")
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
			file := filepath.Join(t.TempDir(), "config.json")
			config := struct {
				Connections []connectionConfig `json:"connections"`
			}{Connections: []connectionConfig{{
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
				if len(application.connections) != 1 || application.connections[0].ConfigurationErrors == nil {
					t.Fatal("incompatible engine must remain visible with errors")
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

func TestInvalidConfiguredConnectionsRemainVisible(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	data := `{"connections":[{}, {"name":"Bad types","driver":"mysql","port":"secret-port","readonly":"false","password":{"secret":"hidden"}}, {"name":"Valid SQLite","driver":"sqlite","database":"/tmp/example.sqlite"}, null]}`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NICHOSI_CONNECTIONS_FILE", file)
	a := &app{}
	a.loadConfiguredConnections()
	if len(a.connections) != 4 {
		t.Fatalf("loaded %d profiles", len(a.connections))
	}
	for _, index := range []int{0, 1, 3} {
		item := a.connections[index]
		if item.ConfigurationErrors == nil || len(*item.ConfigurationErrors) == 0 {
			t.Fatalf("profile %d has no errors", index)
		}
		req := httptest.NewRequest("POST", "/api/connections/invalid/connect", nil)
		req.SetPathValue("connId", item.Id)
		response := httptest.NewRecorder()
		a.connectConnection(response, req)
		if response.Code != 400 {
			t.Fatalf("connect status: %d", response.Code)
		}
	}
	if a.connections[0].Name != "Connection #1" || a.connections[0].Driver != ConnectionDriverUnknown {
		t.Fatal("missing safe fallback metadata")
	}
	if a.connections[2].ConfigurationErrors != nil {
		t.Fatal("valid neighbor must remain usable")
	}
	response := httptest.NewRecorder()
	a.listConnections(response, httptest.NewRequest("GET", "/api/connections", nil))
	for _, secret := range []string{"secret-port", "hidden"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("configuration value leaked")
		}
	}
}
