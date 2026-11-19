package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codegen/internal/bus"
)

// history written by an older build (files only) moves into the database
// on the first open, once
func TestImportFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "runs"), 0o700)
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i, r := range []Run{
		{ID: "old", Workspace: "/w", State: "done", Title: "Release notes", Started: t0},
		{ID: "new", Workspace: "/w", State: "failed", Title: "TODO report", Started: t0.Add(time.Hour)},
		{ID: "other", Workspace: "/x", State: "done", Title: "Elsewhere", Started: t0.Add(2 * time.Hour)},
	} {
		b, _ := json.Marshal(r)
		_ = os.WriteFile(filepath.Join(dir, "runs", r.ID+".json"), b, 0o600)
		ev, _ := json.Marshal(bus.Event{Seq: 1, RunID: r.ID, Type: "flow.step", Summary: []string{"wrote the changelog", "the kafka consumer kept failing", "x"}[i]})
		_ = os.WriteFile(filepath.Join(dir, "runs", r.ID+".jsonl"), append(ev, '\n'), 0o600)
	}
	led, _ := json.Marshal(LedgerEntry{At: t0, RunID: "old", Workspace: "/w", Usage: Usage{InputTokens: 10}})
	_ = os.WriteFile(filepath.Join(dir, "usage.jsonl"), append(led, '\n'), 0o600)

	s, err := Open(dir)
	if err != nil || !s.HasDB() || s.DBError() != nil {
		t.Fatal(err, s.DBError())
	}
	rs, _ := s.Runs("/w", 0)
	if len(rs) != 2 || rs[0].ID != "new" || rs[1].ID != "old" {
		t.Fatalf("newest first, one workspace: %+v", rs)
	}
	if es, _ := s.LedgerEntries(); len(es) != 1 || es[0].InputTokens != 10 {
		t.Fatalf("%+v", es)
	}
	hits, err := s.Search("/w", "kafka consum", 10)
	if err != nil || len(hits) != 1 || hits[0].Run.ID != "new" || !strings.Contains(hits[0].Snippet, HitStart+"kafka"+HitEnd) {
		t.Fatalf("%+v %v", hits, err)
	}
	s.Close()

	// opened again: not imported twice
	s2, _ := Open(dir)
	defer s2.Close()
	if es, _ := s2.LedgerEntries(); len(es) != 1 {
		t.Fatalf("imported twice: %d", len(es))
	}
	if hits, _ := s2.Search("", "changelog", 10); len(hits) != 1 {
		t.Fatalf("%+v", hits)
	}
}

func TestSearchAndState(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Redactor().Add("hunter2-secret")
	_ = s.SaveRun(Run{ID: "r1", Workspace: "/w", State: "done", Title: "Deploy notes", Ask: "version=1.2", Started: time.Now()})
	_ = s.Append(bus.Event{RunID: "r1", Type: "tool.result", Summary: "deploy-tool ok", Data: map[string]any{"output": "password hunter2-secret accepted; rollout to eu-west done"}})
	_ = s.Append(bus.Event{RunID: "r1", Type: "flow.review", Summary: "Waiting for your review", Data: map[string]any{"feedback": "use the canary first"}})

	for q, want := range map[string]int{"rollout eu": 1, "canary": 1, "deploy": 1, "version 1.2": 1, "nothing-like-this": 0, `"; DROP TABLE runs; --`: 0, "": 0} {
		hits, err := s.Search("/w", q, 10)
		if err != nil || len(hits) != want {
			t.Errorf("Search(%q): %d hits, %v (want %d)", q, len(hits), err, want)
		}
	}
	if hits, _ := s.Search("/other", "canary", 10); len(hits) != 0 {
		t.Fatal("another workspace's runs are not found")
	}
	if hits, _ := s.Search("/w", "hunter2", 10); len(hits) != 0 {
		t.Fatal("a secret value is not in the index")
	}
	texts, _ := s.RunTexts("r1", 0)
	if len(texts) != 3 || strings.Contains(strings.Join(texts, "\n"), "hunter2") {
		t.Fatalf("%q", texts)
	}
	// archiving keeps the run findable and its flag
	arch := true
	_ = s.SetRunMeta("r1", &arch, nil)
	if r, _ := s.GetRun("r1"); !r.Archived {
		t.Fatal("archived")
	}
	if hits, _ := s.Search("/w", "canary", 10); len(hits) != 1 || !hits[0].Run.Archived {
		t.Fatal("still found, as archived")
	}

	// named state, replaced as a whole
	_ = s.SaveState("triggers:x", map[string]json.RawMessage{"a/b": json.RawMessage(`{"runs":2}`), "a/c": json.RawMessage(`{}`)})
	_ = s.SaveState("triggers:x", map[string]json.RawMessage{"a/b": json.RawMessage(`{"runs":3}`)})
	vals, ok, err := s.LoadState("triggers:x")
	if !ok || err != nil || len(vals) != 1 || string(vals["a/b"]) != `{"runs":3}` {
		t.Fatalf("%v %v %s", ok, err, vals)
	}
}

// a database that cannot be opened: the files alone, as before
func TestNoDatabase(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "history.db"), 0o700) // a folder where the file should be
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.HasDB() || s.DBError() == nil {
		t.Fatal("expected no database")
	}
	_ = s.SaveRun(Run{ID: "a", Workspace: "/w", State: "done", Started: time.Now()})
	if rs, _ := s.Runs("/w", 0); len(rs) != 1 {
		t.Fatal("runs from the files")
	}
	if _, err := s.Search("/w", "x", 5); err != ErrNoSearch {
		t.Fatal(err)
	}
	if _, ok, _ := s.LoadState("n"); ok {
		t.Fatal("no state without a database")
	}
}

func TestDatabaseFilePrivate(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()
	fi, err := os.Stat(filepath.Join(dir, "history.db"))
	if err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("%v %v", err, fi.Mode())
	}
	var mode string
	db, _ := sql.Open("sqlite", filepath.Join(dir, "history.db"))
	defer db.Close()
	_ = db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Fatalf("journal mode %q", mode)
	}
}

// a database from the first version (schema 1) gets the new tables
func TestMigrateFromSchema1(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.SaveRun(Run{ID: "r", Workspace: "/w", State: "done", Title: "Kept", Started: time.Now()})
	for _, q := range []string{`DROP TABLE lessons`, `DROP TABLE tool_calls`, `DROP TABLE trigger_queue`, `UPDATE meta SET v='1' WHERE k='schema'`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2, err := Open(dir)
	if err != nil || s2.DBError() != nil {
		t.Fatal(err, s2.DBError())
	}
	defer s2.Close()
	if rs, _ := s2.Runs("/w", 0); len(rs) != 1 {
		t.Fatal("the history stays")
	}
	if _, err := s2.AddLesson(Lesson{Workspace: "/w", Flow: "f", Step: "s", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s2.RecordToolCall(ToolCall{Workspace: "/w", Tool: "t", OK: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Enqueue(QueuedRun{Workspace: "/w", Flow: "f", Trigger: "t"}); err != nil {
		t.Fatal(err)
	}
	var v string
	_ = s2.db.QueryRow(`SELECT v FROM meta WHERE k='schema'`).Scan(&v)
	if v != "2" {
		t.Fatal(v)
	}
}

func TestBackups(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()
	_ = s.SaveRun(Run{ID: "r", Workspace: "/w", State: "done", Started: time.Now()})
	if err := s.BackupDaily(); err != nil {
		t.Fatal(err)
	}
	in := s.Info()
	if !in.Open || in.Runs != 1 || len(in.Backups) != 1 || in.Backups[0].Bytes == 0 || in.Bytes == 0 {
		t.Fatalf("%+v", in)
	}
	fi, _ := os.Stat(in.Backups[0].Path)
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatal("copies are private")
	}
	// the copy is a working database with the history in it
	c, err := sql.Open("sqlite", in.Backups[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	_ = c.QueryRow(`SELECT count(*) FROM runs`).Scan(&n)
	c.Close()
	if n != 1 {
		t.Fatalf("runs in the copy: %d", n)
	}
	// older copies beyond the newest 7 go
	for i := 1; i <= 9; i++ {
		_ = os.WriteFile(filepath.Join(dir, "backups", fmt.Sprintf("history-2026-01-%02d.db", i)), []byte("x"), 0o600)
	}
	if _, err := s.Backup(); err != nil {
		t.Fatal(err)
	}
	if b := s.Info().Backups; len(b) != KeepBackups || b[0].Name != "history-"+time.Now().Format("2006-01-02")+".db" {
		t.Fatalf("%d kept, newest %s", len(b), b[0].Name)
	}
}
