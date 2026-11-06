package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectedProfileMetadata(t *testing.T) {
	writable := false
	for _, tc := range []struct {
		name                      string
		driver                    ConnectionInputDriver
		database, label, wantName string
		readonly                  *bool
		wantReadonly              bool
	}{
		{"MySQL default", Mysql, "lab", "", "lab", nil, true},
		{"PostgreSQL label", Postgres, "lab", "  Reports  ", "Reports", nil, true},
		{"SQLite filename", Sqlite, "/tmp/demo.db", "", "demo.db", &writable, false},
		{"SQLite label", Sqlite, "/tmp/demo.db", "  Local  ", "Local", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			password := "factory-test-secret"
			input := ConnectionInput{Driver: tc.driver, Database: tc.database, Name: tc.label, Readonly: tc.readonly, Password: &password}
			item := newConnectedProfile(input, nil, 42, nil)
			if item.Name != tc.wantName || item.Driver != ConnectionDriver(tc.driver) || item.Database != tc.database {
				t.Fatalf("unexpected public metadata: %+v", item.Connection)
			}
			if item.Readonly != tc.wantReadonly || item.input.Readonly == nil || *item.input.Readonly != tc.wantReadonly {
				t.Fatal("public and reconnect readonly settings must agree")
			}
			if !item.Connected || item.Id == "" || item.LastConnectedAt == nil || item.DatabaseSizeBytes == nil || *item.DatabaseSizeBytes != 42 {
				t.Fatal("missing connected metadata")
			}
			data, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), password) || strings.Contains(string(data), "password") {
				t.Fatal("profile serialization exposes credentials")
			}
		})
	}
}
