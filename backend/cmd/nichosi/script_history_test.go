package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScriptHistoryPersistence(t *testing.T) {
	dir := t.TempDir()
	a := &app{historyDir: dir}
	item := &profile{Connection: Connection{Id: "old", Database: "example"}}
	item.scripts.history = []ScriptHistoryEntry{{Id: "execution", Sql: "SELECT 1", ExecutionCount: 5}}
	if err := a.saveScriptHistory(item); err != nil {
		t.Fatal(err)
	}
	restarted := &app{historyDir: dir}
	restored := &profile{Connection: Connection{Id: "new", Database: "example"}}
	if err := restarted.loadScriptHistory(restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.scripts.history) != 1 || restored.scripts.history[0].Sql != "SELECT 1" || restored.scripts.history[0].ExecutionCount != 5 {
		t.Fatal("history did not survive restart and changed connection ID")
	}
	other := &profile{Connection: Connection{Database: "other"}}
	if err := restarted.loadScriptHistory(other); err != nil {
		t.Fatal(err)
	}
	if len(other.scripts.history) != 0 {
		t.Fatal("history leaked across connections")
	}
	info, err := os.Stat(filepath.Join(dir, metadataKey(item.Connection)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("history permissions: %v", info.Mode())
	}
	restored.scripts.history = nil
	if err := restarted.saveScriptHistory(restored); err != nil {
		t.Fatal(err)
	}
	cleared := &profile{Connection: item.Connection}
	if err := restarted.loadScriptHistory(cleared); err != nil {
		t.Fatal(err)
	}
	if len(cleared.scripts.history) != 0 {
		t.Fatal("cleared history returned after restart")
	}
}

func TestScriptHistoryReloadsSharedIdentity(t *testing.T) {
	a := &app{historyDir: t.TempDir()}
	first := &profile{Connection: Connection{Database: "shared"}}
	second := &profile{Connection: first.Connection}
	if err := a.loadScriptHistory(second); err != nil {
		t.Fatal(err)
	}
	first.scripts.history = []ScriptHistoryEntry{{Sql: "SELECT 2"}}
	if err := a.saveScriptHistory(first); err != nil {
		t.Fatal(err)
	}
	if err := a.loadScriptHistory(second); err != nil {
		t.Fatal(err)
	}
	if len(second.scripts.history) != 1 {
		t.Fatal("stale history for shared connection")
	}
	path := filepath.Join(a.historyDir, metadataKey(first.Connection)+".json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.loadScriptHistory(second); err == nil {
		t.Fatal("expected corrupt history error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "broken" {
		t.Fatal("loading modified corrupt history")
	}
}

func TestScriptHistoryLegacyCount(t *testing.T) {
	a := &app{historyDir: t.TempDir()}
	item := &profile{Connection: Connection{Database: "legacy"}}
	path := filepath.Join(a.historyDir, metadataKey(item.Connection)+".json")
	if err := os.WriteFile(path, []byte(`[{"id":"old","sql":"SELECT 1"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.loadScriptHistory(item); err != nil {
		t.Fatal(err)
	}
	if len(item.scripts.history) != 1 || item.scripts.history[0].ExecutionCount != 1 {
		t.Fatal("legacy execution must count as one run")
	}
}
