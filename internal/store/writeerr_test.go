package store

import (
	"os"
	"path/filepath"
	"testing"

	"codegen/internal/bus"
)

// A failed history write is reported through the hook, not only returned
// (callers carry on after it).
func TestWriteErrorsAreReported(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	s.SetOnWriteError(func(op string, err error) { ops = append(ops, op) })

	// the runs folder becomes a file: run files cannot be written into it
	runs := filepath.Join(s.Dir(), "runs")
	if err := os.RemoveAll(runs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runs, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRun(Run{ID: "r1"}); err == nil {
		t.Fatal("SaveRun: want an error")
	}
	if err := s.Append(bus.Event{RunID: "r1", Type: "x"}); err == nil {
		t.Fatal("Append: want an error")
	}
	if len(ops) != 2 || ops[0] != "save run r1" || ops[1] != "record an event of run r1" {
		t.Fatalf("reported %q", ops)
	}

	// events without a run are not stored, so nothing fails or is reported
	if err := s.Append(bus.Event{Type: "store.error"}); err != nil || len(ops) != 2 {
		t.Fatalf("run-less event: err %v, reported %q", err, ops)
	}
}
