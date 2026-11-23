package store

// The history database: <store dir>/history.db, SQLite (modernc.org/sqlite,
// pure Go, no cgo). It holds
//
//   - runs: every run's summary (the Run row, as JSON, with the columns the
//     lists sort and filter on), so History and Run flows do not read one
//     file per run;
//   - usage: the usage ledger (Metrics);
//   - state: small named state other parts keep (trigger state: next run,
//     counts, encrypted secrets);
//   - search: a full-text index (FTS5) of run titles, inputs and the
//     events' text, for searching history and for agents' memory
//     (history_search).
//
// The files stay: each run's events are still written to runs/<id>.jsonl
// (Replay reads them), and summaries and the ledger are written to their
// files too for now, so an older build reads the same history. The first
// open imports what the files already hold. If the database cannot be
// opened, the store works from the files alone, as before.
//
// Everything indexed has been redacted first (Redactor), like everything
// else written to disk.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"codegen/internal/bus"

	_ "modernc.org/sqlite" // the "sqlite" database/sql driver
)

// schemaVersion: bump with a migration in migrate.
const schemaVersion = 2

func openDB(dir string) (*sql.DB, error) {
	p := filepath.Join(dir, "history.db")
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(p, 0o600)
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		return err
	}
	v := 0
	_ = db.QueryRow(`SELECT CAST(v AS INTEGER) FROM meta WHERE k='schema'`).Scan(&v)
	if v >= schemaVersion {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v < 1 {
		if err := migrate1(tx); err != nil {
			return err
		}
	}
	if v < 2 {
		if err := migrate2(tx); err != nil { // learn.go
			return fmt.Errorf("history database: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES('schema', ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, fmt.Sprint(schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrate1: runs, usage, named state and the search index.
func migrate1(tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS runs(
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL DEFAULT '',
			started INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			workflow TEXT NOT NULL DEFAULT '',
			archived INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS runs_by_workspace ON runs(workspace, started DESC)`,
		`CREATE INDEX IF NOT EXISTS runs_by_started ON runs(started DESC)`,
		`CREATE TABLE IF NOT EXISTS usage(
			n INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			run_id TEXT NOT NULL DEFAULT '',
			workspace TEXT NOT NULL DEFAULT '',
			data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS usage_by_at ON usage(at)`,
		`CREATE TABLE IF NOT EXISTS state(ns TEXT NOT NULL, k TEXT NOT NULL, v TEXT NOT NULL, PRIMARY KEY(ns, k))`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS search USING fts5(run_id UNINDEXED, field UNINDEXED, text, tokenize='unicode61 remove_diacritics 2')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("history database: %w", err)
		}
	}
	return nil
}

// --- runs ---------------------------------------------------------------------------

func (s *Store) dbPutRun(r Run) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO runs(id, workspace, started, state, kind, workflow, archived, data) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET workspace=excluded.workspace, started=excluded.started, state=excluded.state,
		kind=excluded.kind, workflow=excluded.workflow, archived=excluded.archived, data=excluded.data`,
		r.ID, r.Workspace, r.Started.UnixNano(), r.State, r.Kind, r.Workflow, boolInt(r.Archived), string(b)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM search WHERE run_id=? AND field='run'`, r.ID); err != nil {
		return err
	}
	if t := s.red.String(runText(r)); strings.TrimSpace(t) != "" {
		if _, err := tx.Exec(`INSERT INTO search(run_id, field, text) VALUES(?, 'run', ?)`, r.ID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// runText is what search finds a run by (besides its events).
func runText(r Run) string {
	return strings.Join(nonEmpty(r.Title, r.Ask, r.Workflow, r.Step, r.Group, r.Trigger, r.TriggerName, r.Agent, r.Model), "\n")
}

func (s *Store) dbGetRun(id string) (Run, bool) {
	if s.db == nil {
		return Run{}, false
	}
	var data string
	if err := s.db.QueryRow(`SELECT data FROM runs WHERE id=?`, id).Scan(&data); err != nil {
		return Run{}, false
	}
	var r Run
	return r, json.Unmarshal([]byte(data), &r) == nil
}

func (s *Store) dbRuns(workspace string, limit int) ([]Run, error) {
	q := `SELECT data FROM runs`
	var args []any
	if workspace != "" {
		q += ` WHERE workspace=?`
		args = append(args, workspace)
	}
	q += ` ORDER BY started DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var data string
		if rows.Scan(&data) != nil {
			continue
		}
		var r Run
		if json.Unmarshal([]byte(data), &r) == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// --- events: indexed for search (their full record stays in the .jsonl) ------------

// eventTextKeys are the event fields whose text is worth finding.
var eventTextKeys = []string{"text", "output", "content", "prompt", "message", "error", "reason", "answer", "summary", "feedback", "verdict", "command", "path"}

// eventText is an event's searchable text (already redacted), at most 8 KB.
func eventText(e bus.Event) string {
	parts := nonEmpty(e.Summary)
	for _, k := range eventTextKeys {
		if v, ok := e.Data[k].(string); ok && strings.TrimSpace(v) != "" && v != e.Summary {
			parts = append(parts, clip(v, 4000))
		}
	}
	return clip(strings.Join(parts, "\n"), 8000)
}

func (s *Store) dbIndexEvent(e bus.Event) error {
	if s.db == nil || e.RunID == "" {
		return nil
	}
	t := eventText(e)
	if strings.TrimSpace(t) == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO search(run_id, field, text) VALUES(?, ?, ?)`, e.RunID, clip(e.Type, 60), t)
	return err
}

// --- usage --------------------------------------------------------------------------

func (s *Store) dbLedger(e LedgerEntry) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO usage(at, run_id, workspace, data) VALUES(?,?,?,?)`, e.At.UnixNano(), e.RunID, e.Workspace, string(b))
	return err
}

func (s *Store) dbLedgerEntries() ([]LedgerEntry, error) {
	rows, err := s.db.Query(`SELECT data FROM usage ORDER BY n`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var data string
		if rows.Scan(&data) != nil {
			continue
		}
		var e LedgerEntry
		if json.Unmarshal([]byte(data), &e) == nil {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

// --- named state --------------------------------------------------------------------

// LoadState reads the values kept under a namespace (key → JSON); ok is
// false when there is no database (the caller keeps its own file then).
func (s *Store) LoadState(ns string) (map[string]json.RawMessage, bool, error) {
	if s.db == nil {
		return nil, false, nil
	}
	rows, err := s.db.Query(`SELECT k, v FROM state WHERE ns=?`, ns)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = json.RawMessage(v)
		}
	}
	return out, true, rows.Err()
}

// SaveState replaces the values kept under a namespace, in one transaction.
func (s *Store) SaveState(ns string, vals map[string]json.RawMessage) error {
	if s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM state WHERE ns=?`, ns); err != nil {
		return err
	}
	for k, v := range vals {
		if _, err := tx.Exec(`INSERT INTO state(ns, k, v) VALUES(?,?,?)`, ns, k, string(v)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// HasDB says whether the history database is open.
func (s *Store) HasDB() bool { return s.db != nil }

// --- search -------------------------------------------------------------------------

// Hit is a run that a search found, with the best matching text.
type Hit struct {
	Run     Run    `json:"run"`
	Field   string `json:"field"`   // run (its title, inputs) or the event type
	Snippet string `json:"snippet"` // the matching text; matches between \x01 and \x02
	Matches int    `json:"matches"` // how many of its records match
}

// HitStart and HitEnd mark the matched words in Hit.Snippet.
const (
	HitStart = "\x01"
	HitEnd   = "\x02"
)

// ErrNoSearch: the history database is not open (search needs it).
var ErrNoSearch = errors.New("history search is not available (the history database could not be opened)")

// Search finds runs whose title, inputs or events contain all the words of
// q (the last word may be the start of one), best matches first, for one
// workspace ("" for all).
func (s *Store) Search(workspace, q string, limit int) ([]Hit, error) {
	if s.db == nil {
		return nil, ErrNoSearch
	}
	m := ftsQuery(q)
	if m == "" {
		return []Hit{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT search.run_id, search.field, snippet(search, 2, ?, ?, '…', 16), runs.data
		FROM search JOIN runs ON runs.id = search.run_id
		WHERE search MATCH ? AND (? = '' OR runs.workspace = ?)
		ORDER BY search.rank LIMIT 2000`, HitStart, HitEnd, m, workspace, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byRun := map[string]*Hit{}
	var order []string
	for rows.Next() {
		var id, field, snip, data string
		if rows.Scan(&id, &field, &snip, &data) != nil {
			continue
		}
		if h := byRun[id]; h != nil {
			h.Matches++
			continue
		}
		var r Run
		if json.Unmarshal([]byte(data), &r) != nil {
			continue
		}
		byRun[id] = &Hit{Run: r, Field: field, Snippet: snip, Matches: 1}
		order = append(order, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(order))
	for _, id := range order {
		out = append(out, *byRun[id])
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RunTexts is what the index holds for one run, oldest first (an agent
// reading a past run: its inputs, steps and outputs as text).
func (s *Store) RunTexts(id string, max int) ([]string, error) {
	if s.db == nil {
		return nil, ErrNoSearch
	}
	rows, err := s.db.Query(`SELECT field, text FROM search WHERE run_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	n := 0
	for rows.Next() {
		var f, t string
		if rows.Scan(&f, &t) != nil {
			continue
		}
		line := f + ": " + t
		if max > 0 && n+len(line) > max {
			out = append(out, "… (more in the run's history)")
			break
		}
		n += len(line)
		out = append(out, line)
	}
	return out, rows.Err()
}

// ftsQuery makes an FTS5 query of plain words: each word quoted (no query
// syntax gets through), all required, the last one as a prefix.
func ftsQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	var out []string
	for _, w := range words {
		if len(out) == 12 {
			break
		}
		out = append(out, `"`+w+`"`)
	}
	if len(out) == 0 {
		return ""
	}
	out[len(out)-1] += "*"
	return strings.Join(out, " ")
}

// --- import -------------------------------------------------------------------------

// importFiles fills a new database with the history the files hold (once).
func (s *Store) importFiles() error {
	var done string
	if s.db.QueryRow(`SELECT v FROM meta WHERE k='imported'`).Scan(&done) == nil {
		return nil
	}
	ents, _ := os.ReadDir(filepath.Join(s.dir, "runs"))
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, "runs", name))
		if err != nil {
			continue
		}
		var r Run
		if json.Unmarshal(b, &r) != nil || r.ID == "" {
			continue
		}
		if err := s.dbPutRun(r); err != nil {
			return err
		}
		evs, _ := s.Events(r.ID)
		if err := s.indexEvents(evs); err != nil {
			return err
		}
	}
	if es, err := s.fileLedger(); err == nil {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, e := range es {
			b, _ := json.Marshal(e)
			if _, err := tx.Exec(`INSERT INTO usage(at, run_id, workspace, data) VALUES(?,?,?,?)`, e.At.UnixNano(), e.RunID, e.Workspace, string(b)); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT INTO meta(k, v) VALUES('imported', ?)`, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) indexEvents(evs []bus.Event) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range evs {
		if t := eventText(e); strings.TrimSpace(t) != "" && e.RunID != "" {
			if _, err := tx.Exec(`INSERT INTO search(run_id, field, text) VALUES(?, ?, ?)`, e.RunID, clip(e.Type, 60), t); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Close closes the history database.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
