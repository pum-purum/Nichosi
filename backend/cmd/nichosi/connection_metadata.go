package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Only non-secret observations are persisted, independently of connection IDs.
type connectionMetadata struct {
	DatabaseSizeBytes *int64     `json:"databaseSizeBytes"`
	LastConnectedAt   *time.Time `json:"lastConnectedAt"`
}

type connectionMetadataStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]connectionMetadata
}

func metadataKey(c Connection) string {
	identity, _ := json.Marshal([]any{c.Driver, c.Host, c.Port, c.User, c.Database})
	return fmt.Sprintf("%x", sha256.Sum256(identity))
}

func (s *connectionMetadataStore) load(path string) error {
	s.path = path
	s.entries = make(map[string]connectionMetadata)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}
	var entries map[string]connectionMetadata
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("decode metadata: %w", err)
	}
	if entries != nil {
		s.entries = entries
	}
	return nil
}

func (s *connectionMetadataStore) restore(c *Connection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[metadataKey(*c)]; ok {
		c.DatabaseSizeBytes = entry.DatabaseSizeBytes
		c.LastConnectedAt = entry.LastConnectedAt
	}
}

func (s *connectionMetadataStore) save(c Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	if s.entries == nil {
		s.entries = make(map[string]connectionMetadata)
	}
	s.entries[metadataKey(c)] = connectionMetadata{c.DatabaseSizeBytes, c.LastConnectedAt}
	data, err := json.Marshal(s.entries)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create metadata directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".metadata-*")
	if err != nil {
		return fmt.Errorf("create metadata file: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync metadata: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close metadata: %w", err)
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return fmt.Errorf("replace metadata: %w", err)
	}
	return nil
}

func (a *app) persistMetadata(c Connection) {
	if err := a.metadata.save(c); err != nil {
		slog.Error("could not save connection metadata", "error", err)
	}
}
