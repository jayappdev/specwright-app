package store

import (
	"context"
	"strings"
	"testing"

	"codegen/internal/bus"
)

func TestRedactAndReplay(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Redactor().Add("hunter2-secret")
	evs := []bus.Event{
		{Seq: 1, RunID: "r1", Type: "run.start", Summary: "token sk-ant-api03-abcdefghijklmnop"},
		{Seq: 2, RunID: "r1", Type: "tool.result", Data: map[string]any{"output": "pw=hunter2-secret", "nested": map[string]any{"t": "github_pat_11AAAAAAAAAAAAAAAAAAAAAA"}}},
	}
	for _, e := range evs {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Events("r1")
	if err != nil || len(got) != 2 {
		t.Fatalf("events %v %v", got, err)
	}
	all := got[0].Summary + got[1].Str("output") + got[1].Data["nested"].(map[string]any)["t"].(string)
	for _, leak := range []string{"sk-ant-api03", "hunter2", "github_pat_"} {
		if strings.Contains(all, leak) {
			t.Fatalf("leaked %q in %q", leak, all)
		}
	}
	n := 0
	if err := s.Replay(context.Background(), "r1", 0, func(bus.Event) { n++ }); err != nil || n != 2 {
		t.Fatalf("replay %d %v", n, err)
	}
}

func TestRunsAndInterrupted(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.SaveRun(Run{ID: "a", Workspace: "/w", State: "running"})
	_ = s.SaveRun(Run{ID: "b", Workspace: "/x", State: "done"})
	if n := s.MarkInterrupted(); n != 1 {
		t.Fatalf("interrupted %d", n)
	}
	rs, _ := s.Runs("/w", 0)
	if len(rs) != 1 || rs[0].State != "interrupted" {
		t.Fatalf("%+v", rs)
	}
}
