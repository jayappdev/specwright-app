package core

import (
	"runtime"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// A tool step's whole output (here ~250 KB of JSON, past the old 64 KB cut)
// reaches the next step, so a JSON step can read its last item.
func TestFlowPassesLargeToolOutputWhole(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses bash")
	}
	usePolicy(t, "")
	h, _ := newHub(t)
	script := "#!/usr/bin/env bash\necho '{\"items\": ['\nfor i in $(seq 1 5999); do echo \"{\\\"id\\\": $i, \\\"name\\\": \\\"item number $i\\\"},\"; done\necho '{\"id\": 6000}]}'\n"
	if _, err := h.SaveExtension(extension.Extension{Name: "big-list", Desc: "Lists a lot", Lang: "Bash", Scope: "project", Command: "bash .codegen/tools/big-list.sh", Script: script}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("big-list"); err != nil {
		t.Fatal(err)
	}
	f := flow.New("big", "Big output")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "list", Type: "tool", Tool: "big-list", X: 200},
		flow.Node{ID: "last", Type: "transform", Op: "json-path", Expr: "items[5999].id", X: 400},
		flow.Node{ID: "out", Type: "output", Format: "txt", Template: "{{steps.last}}", X: 600})
	f.Edges = []flow.Edge{{From: "start", To: "list"}, {From: "list", To: "last"}, {From: "last", To: "out"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	st, err := h.RunFlow("big", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	got := waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status != "running" })
	if got.Status != "done" {
		t.Fatalf("run %s: %s", got.Status, got.Error)
	}
	for _, s := range got.Steps {
		if s.ID == "last" && strings.TrimSpace(s.Output) != "6000" {
			t.Fatalf("last item id: %q", s.Output)
		}
		if s.ID == "list" && !s.Truncated {
			t.Fatal("the shown list output should be marked as shortened (24 KB display), the data is whole")
		}
	}
}
