package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Pum-purum/nichosi/internal/driver"
)

type scriptPreviewRecord struct {
	digest  [32]byte
	expires time.Time
}
type scriptState struct {
	mu       sync.Mutex
	previews map[string]scriptPreviewRecord
	history  []ScriptHistoryEntry
}

func scriptAtomic(v *bool) bool { return v == nil || *v }
func scriptDigest(sql string, limit int, mode ...bool) [32]byte {
	atomic := true
	if len(mode) > 0 {
		atomic = mode[0]
	}
	b, _ := json.Marshal([]any{sql, limit, atomic})
	return sha256.Sum256(b)
}
func scriptID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func (a *app) scriptConnection(w http.ResponseWriter, r *http.Request) *profile {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, 404, "not_found", "Connection not found")
		return nil
	}
	return item
}
func decodeScript(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		writeError(w, 400, "invalid_request", "Invalid script request")
		return false
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid_request", "Expected one script request")
		return false
	}
	return true
}
func scriptLimit(w http.ResponseWriter, v *int) (int, bool) {
	limit := 200
	if v != nil {
		limit = *v
	}
	if limit < 1 || limit > 1000 {
		writeError(w, 400, "invalid_request", "Row limit must be between 1 and 1000")
		return 0, false
	}
	return limit, true
}
func (a *app) previewScript(w http.ResponseWriter, r *http.Request) {
	item := a.scriptConnection(w, r)
	if item == nil {
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.pool == nil {
		writeError(w, 409, "disconnected", "Connection is closed")
		return
	}
	var input ScriptInput
	if !decodeScript(w, r, &input) {
		return
	}
	limit, ok := scriptLimit(w, input.RowLimit)
	if !ok {
		return
	}
	atomic := scriptAtomic(input.Atomic)
	readonly := item.Readonly || connectionReadonly(item.input.Readonly)
	if !atomic && readonly {
		writeError(w, 403, "read_only", "Non-atomic execution requires a writable connection")
		return
	}
	statements, err := driver.SplitScript(input.Sql, string(item.Driver))
	if err == nil {
		err = driver.ValidateScriptMode(statements, string(item.Driver), readonly, atomic)
	}
	if err != nil {
		writeError(w, 400, "unsupported_script", err.Error())
		return
	}
	token := scriptID()
	state := &item.scripts
	state.mu.Lock()
	if state.previews == nil {
		state.previews = map[string]scriptPreviewRecord{}
	}
	now := time.Now()
	for id, p := range state.previews {
		if !p.expires.After(now) {
			delete(state.previews, id)
		}
	}
	if len(state.previews) >= 20 {
		state.previews = map[string]scriptPreviewRecord{}
	}
	state.previews[token] = scriptPreviewRecord{scriptDigest(input.Sql, limit, atomic), now.Add(5 * time.Minute)}
	state.mu.Unlock()
	preview := ScriptPreview{PreviewToken: token, Statements: []string{}, Warnings: []string{"Each result is bounded by the row limit; all results together are limited to 1000 rows and 4 MiB. History is kept for this server session only."}}
	if atomic && item.Driver == ConnectionDriverMysql {
		preview.Warnings = append(preview.Warnings, "MySQL/MariaDB scripts run in a read-only transaction; writes and DDL are unavailable because arbitrary changes cannot guarantee rollback.")
	}
	if !atomic {
		preview.Warnings = append(preview.Warnings, "Partial application is enabled: commands run sequentially without an enclosing transaction. Earlier changes may remain saved after a later failure. Transaction and session commands run on an isolated connection; unfinished transactions are discarded on disconnect.")
	}
	for _, s := range statements {
		preview.Statements = append(preview.Statements, s.SQL)
	}
	writeJSON(w, 200, preview)
}
func (a *app) executeScript(w http.ResponseWriter, r *http.Request) {
	item := a.scriptConnection(w, r)
	if item == nil {
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.pool == nil {
		writeError(w, 409, "disconnected", "Connection is closed")
		return
	}
	var input ScriptExecuteInput
	if !decodeScript(w, r, &input) {
		return
	}
	limit, ok := scriptLimit(w, input.RowLimit)
	if !ok {
		return
	}
	atomic := scriptAtomic(input.Atomic)
	readonly := item.Readonly || connectionReadonly(item.input.Readonly)
	if !atomic && readonly {
		writeError(w, 403, "read_only", "Non-atomic execution requires a writable connection")
		return
	}
	statements, err := driver.SplitScript(input.Sql, string(item.Driver))
	if err == nil {
		err = driver.ValidateScriptMode(statements, string(item.Driver), readonly, atomic)
	}
	if err != nil {
		writeError(w, 400, "unsupported_script", err.Error())
		return
	}
	state := &item.scripts
	state.mu.Lock()
	preview, found := state.previews[input.PreviewToken]
	delete(state.previews, input.PreviewToken)
	state.mu.Unlock()
	digest := scriptDigest(input.Sql, limit, atomic)
	if !found || !preview.expires.After(time.Now()) || subtle.ConstantTimeCompare(preview.digest[:], digest[:]) != 1 {
		writeError(w, 409, "preview_required", "Preview this exact script, row limit and execution mode again before executing")
		return
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	outputs, err := driver.ExecuteScriptMode(ctx, item.pool, string(item.Driver), readonly, statements, limit, atomic)
	entry := ScriptHistoryEntry{Atomic: atomic, Id: scriptID(), Sql: input.Sql, RowLimit: limit, ExecutedAt: started, DurationMs: time.Since(started).Milliseconds(), Success: err == nil, Error: ""}
	if err != nil {
		entry.Error = driver.ScriptErrorMessageMode(err, atomic)
	}
	state.mu.Lock()
	state.history = append([]ScriptHistoryEntry{entry}, state.history...)
	if len(state.history) > 100 {
		state.history = state.history[:100]
	}
	state.mu.Unlock()
	result := ScriptExecution{Entry: entry, Results: []ScriptResult{}, RolledBack: atomic && err != nil && !errors.Is(err, driver.ErrScriptOutcomeUnknown)}
	if err == nil || !atomic {
		for _, o := range outputs {
			result.Results = append(result.Results, ScriptResult{Statement: o.Statement, Columns: o.Columns, Rows: o.Rows, Truncated: o.Truncated, RowsAffected: o.RowsAffected})
		}
	}
	writeJSON(w, 200, result)
}
func (a *app) listScriptHistory(w http.ResponseWriter, r *http.Request) {
	item := a.scriptConnection(w, r)
	if item == nil {
		return
	}
	item.scripts.mu.Lock()
	defer item.scripts.mu.Unlock()
	entries := append([]ScriptHistoryEntry{}, item.scripts.history...)
	writeJSON(w, 200, entries)
}
func (a *app) clearScriptHistory(w http.ResponseWriter, r *http.Request) {
	item := a.scriptConnection(w, r)
	if item == nil {
		return
	}
	item.scripts.mu.Lock()
	item.scripts.history = nil
	item.scripts.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
