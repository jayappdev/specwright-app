package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStore(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	id := NewID(time.Date(2026, 11, 24, 9, 2, 0, 0, time.UTC), "Ab-c9xyz")
	if id != "20261124-090200-abc9xy" {
		t.Fatal(id)
	}
	sess, err := s.Create(Session{ID: id, Flow: "sales-qa", FlowName: "Sales Q&A", Inputs: map[string]string{"year": "2026"}})
	if err != nil || sess.Status != "new" {
		t.Fatalf("%+v %v", sess, err)
	}
	if _, err := s.Create(Session{ID: id, Flow: "sales-qa"}); err == nil {
		t.Fatal("a session id is used once")
	}
	// git-ignored
	if b, _ := os.ReadFile(filepath.Join(root, ".codegen", "chats", ".gitignore")); !strings.Contains(string(b), "*") {
		t.Fatal("chats must be git-ignored")
	}
	// the first question names the session; messages are counted
	if _, err := s.Append("sales-qa", id, Message{Role: "user", Text: "Which region   grew the most\nin 2026, and by how much compared with the year before that?"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("sales-qa", id, Message{Role: "assistant", Text: "EU, by 18%.", Steps: []StepRun{{Tool: "query", Title: "Query sales", DurationMS: 800, Data: "query-1.json"}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("sales-qa", id)
	if got.Messages != 2 || !strings.HasPrefix(got.Title, "Which region grew the most in 2026") || !strings.HasSuffix(got.Title, "…") {
		t.Fatalf("%+v", got)
	}
	ms, err := s.Messages("sales-qa", id)
	if err != nil || len(ms) != 2 || ms[1].Steps[0].Title != "Query sales" {
		t.Fatalf("%+v %v", ms, err)
	}
	// data: saved, replaced by name, read back
	if _, err := s.PutData("sales-qa", id, Data{Name: "setup-orders.json", Step: "orders", Setup: true}, `[1]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutData("sales-qa", id, Data{Name: "setup-orders.json", Step: "orders", Setup: true}, `[1,2]`); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get("sales-qa", id)
	if len(got.Data) != 1 || got.Data[0].Bytes != 5 {
		t.Fatalf("%+v", got.Data)
	}
	if c, _ := s.GetData("sales-qa", id, "setup-orders.json"); c != "[1,2]" {
		t.Fatal(c)
	}
	// names cannot leave the session folder
	for _, bad := range []string{"../x", "a/b", ""} {
		if _, err := s.PutData("sales-qa", id, Data{Name: bad}, "x"); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if _, err := s.Get("../etc", id); err == nil {
		t.Fatal("bad flow id accepted")
	}
	// list, newest first; delete
	id2 := NewID(time.Now(), "zzzzzz")
	if _, err := s.Create(Session{ID: id2, Flow: "sales-qa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Session{ID: id2, Flow: "runbook"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List("sales-qa"); len(l) != 2 || l[0].ID != id2 {
		t.Fatalf("%+v", l)
	}
	if l, _ := s.List(""); len(l) != 3 {
		t.Fatalf("all: %d", len(l))
	}
	if err := s.Delete("sales-qa", id); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List("sales-qa"); len(l) != 1 {
		t.Fatalf("%+v", l)
	}
}
