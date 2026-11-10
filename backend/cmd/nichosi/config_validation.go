package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Decode fields independently so one malformed profile never hides its neighbors.
// Diagnostics contain field names and fixed messages, never configured values.
func decodeConfiguredConnection(raw json.RawMessage) (connectionConfig, []string) {
	var saved connectionConfig
	problems := []string{}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return saved, []string{"connection: must be a JSON object"}
	}
	for _, field := range []struct {
		name   string
		target any
	}{
		{"name", &saved.Name}, {"driver", &saved.Driver}, {"engine", &saved.Engine},
		{"host", &saved.Host}, {"port", &saved.Port}, {"user", &saved.User},
		{"database", &saved.Database}, {"readonly", &saved.Readonly},
		{"password", &saved.Password}, {"passwordEnv", &saved.PasswordEnv},
	} {
		if value, ok := fields[field.name]; ok {
			if err := json.Unmarshal(value, field.target); err != nil {
				problems = append(problems, field.name+": invalid value type")
			}
		}
	}
	if strings.TrimSpace(saved.Name) == "" {
		problems = append(problems, "name: is required")
	}
	if saved.Driver != "mysql" && saved.Driver != "postgres" && saved.Driver != "sqlite" {
		problems = append(problems, "driver: must be mysql, postgres or sqlite")
	}
	if saved.PasswordEnv != "" {
		var ok bool
		saved.Password, ok = os.LookupEnv(saved.PasswordEnv)
		if !ok {
			problems = append(problems, "passwordEnv: environment variable is not set")
		}
	}
	if saved.Driver == "sqlite" {
		if !filepath.IsAbs(saved.Database) || strings.ContainsRune(saved.Database, 0) {
			problems = append(problems, "database: must be an absolute SQLite file path")
		}
		if saved.Host != "" || saved.Port != 0 || saved.User != "" || saved.Password != "" {
			problems = append(problems, "SQLite: host, port, user and password must be empty")
		}
	} else if saved.Driver == "mysql" || saved.Driver == "postgres" {
		for _, field := range []struct{ name, value string }{{"host", saved.Host}, {"user", saved.User}, {"database", saved.Database}} {
			if strings.TrimSpace(field.value) == "" {
				problems = append(problems, field.name+": is required")
			}
		}
		if saved.Port < 1 || saved.Port > 65535 {
			problems = append(problems, "port: must be between 1 and 65535")
		}
		if saved.Password == "" {
			problems = append(problems, "password: provide a non-empty password through passwordEnv")
		}
	}
	return saved, problems
}
