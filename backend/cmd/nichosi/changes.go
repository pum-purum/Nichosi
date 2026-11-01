package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Pum-purum/nichosi/internal/driver"
)

func (a *app) previewChanges(w http.ResponseWriter, r *http.Request) { a.changeRows(w, r, false) }
func (a *app) applyChanges(w http.ResponseWriter, r *http.Request)   { a.changeRows(w, r, true) }
func (a *app) changeRows(w http.ResponseWriter, r *http.Request, apply bool) {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, 404, "not_found", "Connection not found")
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.Readonly || connectionReadonly(item.input.Readonly) {
		writeError(w, 403, "read_only", "Connection is read-only")
		return
	}
	if item.pool == nil {
		writeError(w, 409, "disconnected", "Connection is closed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var changes ChangeSet
	if err := decoder.Decode(&changes); err != nil {
		writeError(w, 400, "invalid_request", "Invalid change set")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid_request", "Expected one change set")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	table := r.PathValue("table")
	metadata, err := driver.MySQLEditMetadata(ctx, item.pool, item.Database, table)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	updates := make([]driver.RowChange, 0, len(changes.Changes))
	for _, change := range changes.Changes {
		entry := driver.RowChange{Op: string(change.Op)}
		if change.Key != nil {
			entry.Key = *change.Key
		}
		if change.Values != nil {
			entry.Values = *change.Values
		}
		updates = append(updates, entry)
	}
	statements, err := driver.BuildUpdates(item.Database, table, metadata, updates)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	if !apply {
		preview := ChangePreview{Statements: []string{}, Warnings: []string{}}
		parameters := [][]any{}
		for _, statement := range statements {
			preview.Statements = append(preview.Statements, statement.SQL)
			parameters = append(parameters, statement.Parameters)
		}
		preview.Parameters = &parameters
		writeJSON(w, 200, preview)
		return
	}
	applied, err := driver.MySQLApplyUpdates(ctx, item.pool, item.Database, table, statements)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	writeJSON(w, 200, ApplyResult{Applied: applied, RolledBack: false})
}
func writeChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, driver.ErrRowsObjectNotFound):
		writeError(w, 404, "table_not_found", "Table not found")
	case errors.Is(err, driver.ErrReadOnlyTable):
		writeError(w, 403, "read_only", "Editing requires an InnoDB table with a primary or non-null unique key")
	case errors.Is(err, driver.ErrInvalidChange):
		writeError(w, 400, "invalid_change", "Invalid change operation, columns, values, or row key")
	case errors.Is(err, driver.ErrRowConflict):
		writeError(w, 409, "row_conflict", "The row no longer exists or its key is not unique; changes were rolled back")
	default:
		writeError(w, 502, "change_failed", "Could not confirm changes. Reload the data before retrying.")
	}
}
