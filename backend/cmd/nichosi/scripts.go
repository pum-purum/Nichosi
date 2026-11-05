package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pum-purum/nichosi/internal/driver"
)

type scriptState struct {
	mu      sync.Mutex
	history []ScriptHistoryEntry
}

func scriptAtomic(v *bool) bool { return v == nil || *v }
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
func (a *app) executeScript(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
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
	if err == nil && len(statements) != 1 {
		writeError(w, 400, "invalid_script", "Enter exactly one SQL statement.")
		return
	}
	if err == nil {
		err = driver.ValidateScriptMode(statements, string(item.Driver), readonly, atomic)
	}
	if err != nil {
		writeError(w, 400, "unsupported_script", err.Error())
		return
	}
	if input.RowLimit == nil {
		limit = driver.ScriptResultLimit(statements[0])
	}
	state := &item.scripts
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	outputs, err := driver.ExecuteScriptMode(ctx, item.pool, string(item.Driver), readonly, statements, limit, atomic)
	entry := ScriptHistoryEntry{Atomic: atomic, Id: scriptID(), Sql: input.Sql, RowLimit: limit, ExecutedAt: started, DurationMs: time.Since(started).Milliseconds(), Success: err == nil, Error: ""}
	if err != nil {
		entry.Error = redactScriptError(driver.ScriptErrorMessage(err), item.input.Password)
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

var scriptURLDSN = regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mariadb)://[^\s"'<>]+`)
var scriptMySQLDSN = regexp.MustCompile(`[^\s:"'<>]+:[^\r\n]*?@(?:(?:tcp|unix)\([^\r\n)]*\))?/[^\s"'<>]*`)
var scriptPasswordField = regexp.MustCompile(`(?i)\bpassword\s*=\s*(?:'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|[^\s;,]+)`)
var scriptConnectionField = regexp.MustCompile(`(?i)\b(?:host|port|user|password|dbname|database|sslmode)\s*=\s*(?:'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|[^\s;,]+)`)

// Redaction happens before diagnostics enter either response payloads or session history.
func redactScriptError(message string, password *string) string {
	if password != nil && *password != "" {
		type representation struct {
			value          string
			percentEncoded bool
		}
		raw := *password
		slashEscaped := strings.NewReplacer("\\", "\\\\", "\x00", "\\0", "\n", "\\n", "\r", "\\r", "\x1a", "\\Z", "\t", "\\t", "'", "\\'", "\"", "\\\"").Replace(raw)
		variants := []representation{
			{raw, false},
			{strings.ReplaceAll(raw, "'", "''"), false},
			{strings.ReplaceAll(raw, "\"", "\"\""), false},
			{strings.ReplaceAll(raw, "`", "``"), false},
			{slashEscaped, false},
			{strings.ReplaceAll(strings.ReplaceAll(raw, "\\", "\\\\"), "'", "''"), false},
			{url.QueryEscape(raw), true}, {url.PathEscape(raw), true},
		}
		// Prefer complete escaped forms to shorter raw matches inside them.
		sort.SliceStable(variants, func(i, j int) bool { return len(variants[i].value) > len(variants[j].value) })
		patterns := make([]string, 0, len(variants))
		for _, variant := range variants {
			if !variant.percentEncoded {
				patterns = append(patterns, regexp.QuoteMeta(variant.value))
				continue
			}
			var pattern strings.Builder
			for i := 0; i < len(variant.value); i++ {
				if variant.value[i] == '%' && i+2 < len(variant.value) {
					pattern.WriteString("(?i:" + regexp.QuoteMeta(variant.value[i:i+3]) + ")")
					i += 2
				} else {
					pattern.WriteString(regexp.QuoteMeta(variant.value[i : i+1]))
				}
			}
			patterns = append(patterns, pattern.String())
		}
		// One pass prevents shorter variants from rewriting replacement markers.
		message = regexp.MustCompile(strings.Join(patterns, "|")).ReplaceAllString(message, "[redacted]")
	}
	message = scriptURLDSN.ReplaceAllString(message, "[redacted DSN]")
	message = scriptMySQLDSN.ReplaceAllString(message, "[redacted DSN]")
	if len(scriptConnectionField.FindAllString(message, -1)) > 1 {
		message = scriptConnectionField.ReplaceAllString(message, "[redacted DSN]")
	} else {
		message = scriptPasswordField.ReplaceAllString(message, "password=[redacted]")
	}
	return message
}
