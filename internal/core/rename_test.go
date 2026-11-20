package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

func TestRenameFlowMovesFileAndReferences(t *testing.T) {
	h, ws := flowHub(t)
	a := flow.New("nightly", "Nightly")
	a.Nodes = append(a.Nodes, flow.Node{ID: "out", Type: "output", Format: "md", Template: "hi"})
	a.Triggers = []flow.Trigger{{ID: "t1", Kind: "schedule", Name: "Every day", Enabled: false, Schedule: &flow.Schedule{Every: 1, Unit: "day"}}}
	if _, err := h.SaveFlow(*a); err != nil {
		t.Fatal(err)
	}
	b := flow.New("report", "Report")
	b.Parent = "nightly"
	b.Triggers = []flow.Trigger{{ID: "after", Kind: "flow", Name: "After nightly", Enabled: true, After: &flow.AfterFlow{Flow: "nightly"}}}
	b.Nodes = append(b.Nodes, flow.Node{ID: "rf", Type: "tool", Tool: "builtin:run_flow", Args: map[string]string{"flow_id": "nightly"}})
	if _, err := h.SaveFlow(*b); err != nil {
		t.Fatal(err)
	}
	ts := h.triggers(ws)
	ts.mu.Lock()
	ts.get(trigKey("nightly", "t1")).Runs = 3
	ts.mu.Unlock()

	st, err := h.RenameFlow("nightly", "Nightly build")
	if err != nil {
		t.Fatal(err)
	}
	if st.Flow.ID != "nightly-build" || st.Flow.Name != "Nightly build" {
		t.Fatalf("%s %q", st.Flow.ID, st.Flow.Name)
	}
	if _, err := os.Stat(flow.Path(ws, "nightly")); !os.IsNotExist(err) {
		t.Fatal("the old file is still there")
	}
	g, _ := flow.Load(ws, "report")
	if g.Triggers[0].After.Flow != "nightly-build" || g.Parent != "nightly-build" || g.Nodes[len(g.Nodes)-1].Args["flow_id"] != "nightly-build" {
		t.Fatalf("references: %+v %q %+v", g.Triggers[0].After, g.Parent, g.Nodes[len(g.Nodes)-1].Args)
	}
	if s := h.triggers(ws).data[trigKey("nightly-build", "t1")]; s == nil || s.Runs != 3 {
		t.Fatal("the trigger state did not move")
	}
	// a name with the same id only changes the name
	if st, _ := h.RenameFlow("nightly-build", "Nightly Build"); st.Flow.ID != "nightly-build" || st.Flow.Name != "Nightly Build" {
		t.Fatalf("%s %q", st.Flow.ID, st.Flow.Name)
	}
	// a taken id gets a free one
	if st, _ := h.RenameFlow("report", "Nightly build"); st.Flow.ID != "nightly-build-2" {
		t.Fatalf("taken id: %s", st.Flow.ID)
	}
	if _, err := h.RenameFlow("nightly-build", " "); err == nil {
		t.Fatal("an empty name should be refused")
	}
}

func TestRenameToolMovesScriptAndFlows(t *testing.T) {
	h, ws := flowHub(t)
	saved, err := h.SaveExtension(extension.Extension{Name: "count-words", Desc: "Counts", Lang: "Bash", Scope: "project",
		Params: []extension.Param{{Name: "file", Type: "string", Required: true}},
		Script: "#!/bin/bash\nwc -w \"$1\"\n", Command: "bash .codegen/tools/project/count-words.sh {{file}}"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.SetToolTags("count-words", []string{"text"})
	f := flow.New("uses", "Uses it")
	f.Nodes = append(f.Nodes, flow.Node{ID: "c", Type: "tool", Tool: "count-words", Args: map[string]string{"file": "x"}},
		flow.Node{ID: "o", Type: "output", Format: "md", Actions: []flow.Action{{Type: "tool", Tool: "count-words"}}})
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	r, err := h.RenameTool("count-words", "word-count")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "word-count" || !r.Approved || r.Runnable() != nil || !strings.Contains(r.Command, ".codegen/tools/project/word-count.sh") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(saved.ScriptPath))); !os.IsNotExist(err) {
		t.Fatal("the old script is still there")
	}
	if _, err := h.exts.Get("count-words"); err == nil {
		t.Fatal("the old entry is still there")
	}
	if tags := h.ToolTags(); len(tags["word-count"]) != 1 || len(tags["count-words"]) != 0 {
		t.Fatalf("tags: %v", tags)
	}
	g, _ := flow.Load(ws, "uses")
	if g.Node("c").Tool != "word-count" || g.Node("o").Actions[0].Tool != "word-count" {
		t.Fatal("the flow still calls the old name")
	}
	run, err := h.RunToolWith(r, map[string]any{"file": filepath.Join(ws, "uses.txt")}, nil, nil)
	if err != nil || run.Command == "" {
		t.Fatalf("%+v %v", run, err)
	}
	for _, bad := range []string{"echo-arg", "read_file", "Bad Name"} {
		if _, err := h.RenameTool("word-count", bad); err == nil {
			t.Errorf("renaming to %q should be refused", bad)
		}
	}
}

func TestListLayoutHeadings(t *testing.T) {
	h, ws := flowHub(t)
	l, err := h.SaveLayout(ListLayout{
		Tools: ListSection{Headings: []string{"  Reporting ", "reporting", "", "Data   jobs"}, Items: map[string]string{"echo-arg": "Data jobs", "gone": "No such heading"}},
		Flows: ListSection{Headings: []string{"Nightly"}, Items: map[string]string{"notes": "nightly"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Tools.Headings, "|") != "Reporting|Data jobs" || l.Tools.Items["echo-arg"] != "Data jobs" || len(l.Tools.Items) != 1 || l.Flows.Items["notes"] != "Nightly" {
		t.Fatalf("tidied: %+v", l)
	}
	if got, _ := h.Layout(); got.Tools.Items["echo-arg"] != "Data jobs" {
		t.Fatalf("read back: %+v", got)
	}
	// nothing is written into the project
	if _, err := os.Stat(filepath.Join(ws, ".codegen", "lists")); !os.IsNotExist(err) {
		t.Fatal("headings were stored in the project")
	}
	// a renamed flow keeps its heading
	if _, err := h.RenameFlow("notes", "Release notes daily"); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.Layout(); got.Flows.Items["release-notes-daily"] != "Nightly" || got.Flows.Items["notes"] != "" {
		t.Fatalf("after the rename: %+v", got.Flows)
	}
}

func TestSetFlowTags(t *testing.T) {
	h, ws := flowHub(t)
	tags, err := h.SetFlowTags("notes", []string{"Nightly Build", "jira", "jira", " "})
	if err != nil || strings.Join(tags, ",") != "jira,nightly-build" {
		t.Fatalf("%v %v", tags, err)
	}
	f, _ := flow.Load(ws, "notes")
	if strings.Join(f.Tags, ",") != "jira,nightly-build" || len(f.Nodes) == 0 {
		t.Fatalf("saved: %v", f.Tags)
	}
	if tags, _ := h.SetFlowTags("notes", nil); len(tags) != 0 {
		t.Fatalf("cleared: %v", tags)
	}
	if _, err := h.SetFlowTags("nope", []string{"x"}); err == nil {
		t.Fatal("an unknown flow should fail")
	}
}
