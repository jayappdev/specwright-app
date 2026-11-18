package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"codegen/internal/flow"
	"codegen/internal/watch"
)

// a small flow that shows its "from" input
func echoFlow(t *testing.T, h *Hub, id string, trig ...flow.Trigger) {
	t.Helper()
	f := flow.New(id, strings.ToUpper(id[:1])+id[1:])
	f.Inputs = []flow.Input{{Name: "from", Label: "From", Type: "multiline"}}
	f.Nodes = append(f.Nodes, flow.Node{ID: "md", Type: "output", Format: "md", Template: "{{inputs.from}}", X: 200, Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "md"}}
	f.Triggers = trig
	st, err := h.SaveFlow(*f)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range st.Issues {
		if is.Level == "error" {
			t.Fatal(is.Message)
		}
	}
}

// waitTrigger waits for a trigger to have started a run, and for that run to end.
func waitTrigger(t *testing.T, h *Hub, flowID string, runs int) FlowRunState {
	t.Helper()
	for dl := time.Now().Add(10 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
		v, _ := h.FlowTriggers(flowID)
		if len(v) > 0 && v[0].State.Runs >= runs && v[0].State.LastRunID != "" {
			return waitFlow(t, h, v[0].State.LastRunID, func(s FlowRunState) bool { return !slicesContains(flowLive, s.Status) })
		}
	}
	t.Fatalf("%s: no run started by its trigger", flowID)
	return FlowRunState{}
}

func TestAfterFlowTrigger(t *testing.T) {
	stubKeychain(t)
	h, ws := flowHub(t)
	echoFlow(t, h, "follow", flow.Trigger{ID: "after-notes", Kind: "flow", Name: "After the notes", Enabled: true,
		After: &flow.AfterFlow{Flow: "notes"}, Inputs: map[string]string{"from": "{{trigger.flow}}/{{trigger.status}}: {{trigger.output}}"}})
	// and back: notes after follow would loop (notes → follow → notes)
	f, _ := flow.Load(ws, "notes")
	f.Triggers = []flow.Trigger{{ID: "back", Kind: "flow", Enabled: true, After: &flow.AfterFlow{Flow: "follow"}, Inputs: map[string]string{"version": "2"}}}
	f.Rev = ""
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.FlowTriggers("follow"); len(v) != 1 || v[0].Describe != "after Release notes completes" {
		t.Fatalf("%+v", v)
	}

	first, err := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "1.0"}, Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	waitFlow(t, h, first.ID, func(s FlowRunState) bool { return s.Status == "done" })
	st := waitTrigger(t, h, "follow", 1)
	if st.Status != "done" || st.Options.Trigger == nil || st.Options.Trigger.Kind != "flow" || !slices.Equal(st.Options.Trigger.Chain, []string{"notes"}) {
		t.Fatalf("%s %+v", st.Status, st.Options.Trigger)
	}
	if !strings.HasPrefix(st.Options.Inputs["from"], "notes/done: # 1.0") {
		t.Fatalf("trigger values: %q", st.Options.Inputs["from"])
	}
	var tag string
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl) && tag == ""; time.Sleep(20 * time.Millisecond) {
		if r, err := h.Store.GetRun(st.ID); err == nil && r.State == "done" {
			tag = r.Trigger
		}
	}
	if tag != "after flow" {
		t.Fatalf("history tag %q", tag)
	}
	// follow ended: notes would start again, which is the loop: refused
	var back []TriggerView
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
		if back, _ = h.FlowTriggers("notes"); len(back) == 1 && back[0].State.LastError != "" {
			break
		}
	}
	if back[0].State.Runs != 0 || !strings.Contains(back[0].State.LastError, "loop (notes → follow → notes)") {
		t.Fatalf("%+v", back[0].State)
	}

	// it starts only on the ends it says
	f, _ = flow.Load(ws, "follow")
	f.Triggers[0].After.On = []string{"failed"}
	f.Rev = ""
	h.SaveFlow(*f)
	second, _ := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "1.1"}, Review: "none"})
	waitFlow(t, h, second.ID, func(s FlowRunState) bool { return s.Status == "done" })
	time.Sleep(300 * time.Millisecond)
	if v, _ := h.FlowTriggers("follow"); v[0].State.Runs != 0 { // new settings: counted again, and not started
		t.Fatalf("%+v", v[0].State)
	}
}

func TestFilesTrigger(t *testing.T) {
	stubKeychain(t)
	old := triggerQuiet
	triggerQuiet = func(flow.FileWatch) time.Duration { return 150 * time.Millisecond }
	t.Cleanup(func() { triggerQuiet = old })
	h, ws := flowHub(t) // after the swap: the watcher goroutine reads it
	echoFlow(t, h, "docs", flow.Trigger{ID: "on-docs", Kind: "files", Name: "Docs changed", Enabled: true,
		Files:  &flow.FileWatch{Paths: []string{"docs/**/*.md"}, Ignore: []string{"draft-*"}, Ops: []string{"created", "modified"}},
		Inputs: map[string]string{"from": "{{trigger.files}}"}})

	// a burst of changes: one run with the ones that count
	h.noteFileChanges(ws, []watch.Change{{Path: "docs/a.md", Op: "modified"}, {Path: "docs/sub/draft-x.md", Op: "created"},
		{Path: ".codegen/runs/x.md", Op: "created"}, {Path: "src/x.go", Op: "modified"}, {Path: "docs/old.md", Op: "deleted"}})
	time.Sleep(50 * time.Millisecond)
	h.noteFileChanges(ws, []watch.Change{{Path: "docs/sub/b.md", Op: "created"}})
	st := waitTrigger(t, h, "docs", 1)
	if st.Status != "done" || st.Options.Trigger.Kind != "files" || st.Options.Inputs["from"] != "docs/a.md\ndocs/sub/b.md" {
		t.Fatalf("%s %q", st.Status, st.Options.Inputs["from"])
	}
	time.Sleep(300 * time.Millisecond)
	if v, _ := h.FlowTriggers("docs"); v[0].State.Runs != 1 || v[0].Describe != "when docs/**/*.md is created or modified (not draft-*)" {
		t.Fatalf("one run for the burst: %+v %q", v[0].State, v[0].Describe)
	}
	// nothing that counts: no run
	h.noteFileChanges(ws, []watch.Change{{Path: "README.md", Op: "modified"}})
	time.Sleep(300 * time.Millisecond)
	if v, _ := h.FlowTriggers("docs"); v[0].State.Runs != 1 {
		t.Fatalf("%+v", v[0].State)
	}
	// a real change through the watcher
	writeFile(t, ws, "docs/c.md", "hello")
	h.onFiles(h.watcher.Poll())
	if st := waitTrigger(t, h, "docs", 2); st.Options.Inputs["from"] != "docs/c.md" {
		t.Fatalf("%q", st.Options.Inputs["from"])
	}
}

func writeFile(t *testing.T, root, rel, s string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// a folder outside the project, given by its absolute path
func TestFilesTriggerOutsideProject(t *testing.T) {
	stubKeychain(t)
	old := triggerQuiet
	triggerQuiet = func(flow.FileWatch) time.Duration { return 150 * time.Millisecond }
	t.Cleanup(func() { triggerQuiet = old })
	h, ws := flowHub(t)
	out := t.TempDir() // not in the project
	echoFlow(t, h, "inbox", flow.Trigger{ID: "in", Kind: "files", Name: "Inbox", Enabled: true,
		Files: &flow.FileWatch{Paths: []string{filepath.ToSlash(out) + "/**"}}, Inputs: map[string]string{"from": "{{trigger.files}}"}})
	var v []TriggerView
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
		if v, _ = h.FlowTriggers("inbox"); len(v) == 1 && len(v[0].State.Watching) == 1 {
			break
		}
	}
	if len(v[0].State.Watching) != 1 || v[0].State.WatchError != "" {
		t.Fatalf("watching: %+v", v[0].State)
	}
	writeFile(t, out, "new/report.txt", "hi")
	st := waitTrigger(t, h, "inbox", 1) // the folder's own watcher scans every second
	want := filepath.ToSlash(filepath.Join(out, "new", "report.txt"))
	if st.Status != "done" || st.Options.Inputs["from"] != want {
		t.Fatalf("%s %q, want %q", st.Status, st.Options.Inputs["from"], want)
	}

	// too broad, or not there: said on the trigger, nothing watched
	home, _ := os.UserHomeDir()
	f, _ := flow.Load(ws, "inbox")
	f.Triggers[0].Files.Paths = []string{filepath.ToSlash(home) + "/**"}
	f.Rev = ""
	h.SaveFlow(*f)
	h.syncExternalWatches(ws)
	if v, _ = h.FlowTriggers("inbox"); len(v[0].State.Watching) != 0 || !strings.Contains(v[0].State.WatchError, "too much") {
		t.Fatalf("%+v", v[0].State)
	}
	f.Triggers[0].Files.Paths = []string{"/no/such/folder/**"}
	h.SaveFlow(*f)
	h.syncExternalWatches(ws)
	if v, _ = h.FlowTriggers("inbox"); !strings.Contains(v[0].State.WatchError, "no such folder") {
		t.Fatalf("%+v", v[0].State)
	}
	h.triggers(ws).mu.Lock()
	n := len(h.triggers(ws).ext)
	h.triggers(ws).mu.Unlock()
	if n != 0 {
		t.Fatal("the old folder's watcher stops")
	}
}
