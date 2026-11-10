package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPoll(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(f, []byte("one\ntwo\n"), 0o644)
	w := New(dir, time.Second)
	if ch := w.Poll(); ch != nil {
		t.Fatalf("baseline should be silent: %v", ch)
	}
	_ = os.WriteFile(f, []byte("one\nthree\nfour\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "skip.js"), []byte("x"), 0o644)
	got := map[string]Change{}
	for _, c := range w.Poll() {
		got[c.Path] = c
	}
	if c := got["a.txt"]; c.Op != "modified" || c.Added != 2 || c.Removed != 1 {
		t.Fatalf("a.txt %+v", c)
	}
	if c := got["b.txt"]; c.Op != "created" || c.Added != 1 {
		t.Fatalf("b.txt %+v", c)
	}
	if _, ok := got["node_modules/skip.js"]; ok || len(got) != 2 {
		t.Fatalf("unexpected %v", got)
	}
	_ = os.Remove(f)
	if ch := w.Poll(); len(ch) != 1 || ch[0].Op != "deleted" {
		t.Fatalf("delete %v", ch)
	}
}
