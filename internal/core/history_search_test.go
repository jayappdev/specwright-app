package core

import (
	"strings"
	"testing"
	"time"
)

func TestHistorySearchTool(t *testing.T) {
	h, _ := flowHub(t)
	st, err := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "9.9.9-zeta"}, Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })
	found := false
	for _, b := range h.Builtins() {
		found = found || b.Name == HistorySearchTool && b.ReadOnly
	}
	if !found {
		t.Fatal("agents get history_search")
	}
	var out string
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(30 * time.Millisecond) {
		if out, err = h.RunBuiltin(h.ctx, HistorySearchTool, map[string]any{"query": "zeta"}); err == nil && strings.Contains(out, st.ID) {
			break
		}
	}
	if !strings.Contains(out, st.ID) || !strings.Contains(out, "Release notes") || !strings.Contains(out, "**") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = h.RunBuiltin(h.ctx, HistorySearchTool, map[string]any{"run_id": st.ID})
	if err != nil || !strings.Contains(out, "fixed 9.9.9-zeta") {
		t.Fatalf("%v\n%s", err, out)
	}
	if hits, err := h.SearchHistory("zeta", 5); err != nil || len(hits) != 1 {
		t.Fatalf("%v %+v", err, hits)
	}
	if _, err := h.RunBuiltin(h.ctx, HistorySearchTool, map[string]any{}); err == nil {
		t.Fatal("query or run_id is needed")
	}
	if out, _ := h.RunBuiltin(h.ctx, HistorySearchTool, map[string]any{"query": "nothing-like-this"}); !strings.Contains(out, "No runs") {
		t.Fatal(out)
	}
}
