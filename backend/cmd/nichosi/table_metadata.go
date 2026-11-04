package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Pum-purum/nichosi/internal/driver"
)

func (a *app) getTableMetadata(w http.ResponseWriter, r *http.Request) {
	a.getObjectMetadata(w, r, false)
}
func (a *app) getViewMetadata(w http.ResponseWriter, r *http.Request) {
	a.getObjectMetadata(w, r, true)
}
func (a *app) getObjectMetadata(w http.ResponseWriter, r *http.Request, view bool) {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.pool == nil {
		writeError(w, http.StatusConflict, "disconnected", "Connection is closed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var metadata driver.TableMetadata
	var err error
	if item.Driver == ConnectionDriverPostgres {
		name := r.PathValue("table")
		if view {
			name = r.PathValue("view")
		}
		metadata, err = driver.PostgresMetadata(ctx, item.pool, name, view)
	} else if view {
		metadata, err = driver.MySQLViewMetadata(ctx, item.pool, item.Database, r.PathValue("view"))
	} else {
		metadata, err = driver.MySQLTableMetadata(ctx, item.pool, item.Database, r.PathValue("table"))
	}
	if errors.Is(err, driver.ErrViewNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "View not found")
		return
	}
	if errors.Is(err, driver.ErrTableNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Table not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "metadata_failed", "Could not read object metadata")
		return
	}
	writeJSON(w, http.StatusOK, TableMetadata{Name: metadata.Name, Details: metadata.Details, Columns: metadata.Columns, Indexes: metadata.Indexes, ForeignKeys: metadata.ForeignKeys, Triggers: metadata.Triggers, Definition: metadata.Definition, Warnings: metadata.Warnings})
}
