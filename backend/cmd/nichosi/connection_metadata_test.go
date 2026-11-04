package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectionMetadataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "metadata.json")
	var original connectionMetadataStore
	if err := original.load(path); err != nil {
		t.Fatal(err)
	}
	size := int64(12345)
	now := time.Now().UTC()
	c := Connection{Id: "old-id", Driver: "mysql", Host: "db", Port: 3306, User: "user", Database: "example", DatabaseSizeBytes: &size, LastConnectedAt: &now, Connected: true}
	if err := original.save(c); err != nil {
		t.Fatal(err)
	}
	var restarted connectionMetadataStore
	if err := restarted.load(path); err != nil {
		t.Fatal(err)
	}
	restored := Connection{Id: "new-id", Driver: c.Driver, Host: c.Host, Port: c.Port, User: c.User, Database: c.Database}
	restarted.restore(&restored)
	if restored.DatabaseSizeBytes == nil || *restored.DatabaseSizeBytes != size || restored.LastConnectedAt == nil || !restored.LastConnectedAt.Equal(now) {
		t.Fatalf("metadata not restored: %+v", restored)
	}
	if restored.Connected {
		t.Fatal("restored connection must remain disconnected")
	}
	for _, change := range []func(*Connection){
		func(c *Connection) { c.Database = "other" },
		func(c *Connection) { c.Host = "other" },
		func(c *Connection) { c.Port++ },
		func(c *Connection) { c.User = "other" },
		func(c *Connection) { c.Driver = "postgres" },
	} {
		other := restored
		change(&other)
		other.DatabaseSizeBytes, other.LastConnectedAt = nil, nil
		restarted.restore(&other)
		if other.DatabaseSizeBytes != nil || other.LastConnectedAt != nil {
			t.Fatal("metadata leaked to another connection")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"old-id", "example", "user", "connected", "password"} {
		if strings.Contains(string(data), value) {
			t.Fatalf("unexpected profile detail %q in metadata", value)
		}
	}
	size = 67890
	c.DatabaseSizeBytes = &size
	if err := restarted.save(c); err != nil {
		t.Fatal(err)
	}
	var updated connectionMetadataStore
	if err := updated.load(path); err != nil {
		t.Fatal(err)
	}
	updated.restore(&restored)
	if *restored.DatabaseSizeBytes != size {
		t.Fatal("updated size not persisted")
	}
}

func TestConnectionMetadataRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	var store connectionMetadataStore
	if err := store.load(path); err == nil {
		t.Fatal("expected corrupt metadata error")
	}
}
