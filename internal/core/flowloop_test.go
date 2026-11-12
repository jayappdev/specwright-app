package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestFlowLoopRunsToolsAndAIPerItem(t *testing.T) {
	h, ws := flowHub(t) // has the echo-arg tool
	f := flow.New("symbols", "Symbols")
	f.Inputs = []flow.Input{{Name: "names", Label: "Names", Type: "multiline"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "list", Type: "transform", Op: "template", Template: "{{inputs.names}}", X: 100},
		flow.Node{ID: "each", Type: "loop", Unique: true, X: 200},
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "sym {{item}} {{item_index}}/{{item_count}}"}, X: 300},
		flow.Node{ID: "up", Type: "transform", Op: "uppercase", X: 400},
		flow.Node{ID: "ask", Type: "ai", Prompt: "Explain {{item}}: {{steps.up}}", X: 400, Y: 200},
		flow.Node{ID: "all", Type: "gather", Mode: "sections", X: 500},
		flow.Node{ID: "md", Type: "output", Format: "md", X: 600, Actions: []flow.Action{{Type: "save", Path: "out/symbols.md"}}})
	f.Edges = []flow.Edge{{From: "start", To: "list"}, {From: "list", To: "each"}, {From: "each", To: "echo"}, {From: "echo", To: "up"},
		{From: "up", To: "ask"}, {From: "up", To: "all"}, {From: "ask", To: "all"}, {From: "all", To: "md"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	for _, i := range h.ValidateFlow(*f) {
		if i.Level == "error" {
			t.Fatalf("%+v", i)
		}
	}
	st, err := h.RunFlow("symbols", FlowRunOptions{Review: "none", Inputs: map[string]string{"names": "Order\nItem\nOrder\nCustomer"}})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Steps)
	}
	b, _ := os.ReadFile(filepath.Join(ws, "out", "symbols.md"))
	got := string(b)
	for _, want := range []string{"## Order", "## Item", "## Customer", "SYM ORDER 1/3", "SYM CUSTOMER 3/3", "Explain Customer"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "## Order") != 1 {
		t.Fatal("unique items")
	}
	steps := map[string]FlowStepState{}
	for _, s := range st.Steps {
		steps[s.ID] = s
	}
	if l := steps["each"]; l.Status != "done" || l.Iterations != 3 || l.Iteration != 3 {
		t.Fatalf("loop %+v", l)
	}
	if e := steps["echo"]; e.Iterations != 3 || e.Status != "done" {
		t.Fatalf("body %+v", e)
	}
	var items []string
	_ = json.Unmarshal([]byte(steps["each"].Output), &items)
	if len(items) != 3 {
		t.Fatal(steps["each"].Output)
	}
	// each item's output is kept
	if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(stepFilesDir(st.ID, "echo")), "items", "002.txt")); err != nil {
		t.Fatal("per-item output:", err)
	}
}

func TestFlowLoopContinueOnError(t *testing.T) {
	h, _ := flowHub(t)
	f := flow.New("errs", "Errors")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "each", Type: "loop", Source: "a\nbad\nc", X: 100},
		flow.Node{ID: "pick", Type: "transform", Op: "json-path", Expr: "x", Source: "{{item}}", X: 200}, // fails on non-JSON
		flow.Node{ID: "all", Type: "gather", Mode: "json-array", X: 300},
		flow.Node{ID: "o", Type: "output", Format: "json", X: 400})
	f.Edges = []flow.Edge{{From: "start", To: "each"}, {From: "each", To: "pick"}, {From: "pick", To: "all"}, {From: "all", To: "o"}}
	f.Nodes[1].Source = `{"x":1}` + "\n" + "bad" + "\n" + `{"x":3}`
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	// stops at the first failing item
	st, _ := h.RunFlow("errs", FlowRunOptions{Review: "none"})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "failed" || !strings.Contains(st.Error, "item 2 of 3") {
		t.Fatalf("%s %q", st.Status, st.Error)
	}
	// goes on when asked to
	f.Nodes[1].ContinueOnError = true
	_, _ = h.SaveFlow(*f)
	st, _ = h.RunFlow("errs", FlowRunOptions{Review: "none"})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" || len(st.Outputs) != 1 || !strings.Contains(st.Outputs[0].Content, `"error"`) || !strings.Contains(st.Outputs[0].Content, "3") {
		t.Fatalf("%s %q %+v", st.Status, st.Error, st.Outputs)
	}
	for _, s := range st.Steps {
		if s.ID == "each" && (s.Failed != 1 || len(s.Notes) == 0) {
			t.Fatalf("%+v", s)
		}
	}
}

// Nested loops: each loop's item is read by the loop step's id.
func TestFlowNestedLoopsByID(t *testing.T) {
	h, _ := flowHub(t)
	f := flow.New("nested", "Nested")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "files", Type: "loop", Title: "Each file", Source: "a.go\nb.go", X: 100},
		flow.Node{ID: "syms", Type: "loop", Title: "Each symbol", Source: "x\ny", X: 200},
		flow.Node{ID: "pair", Type: "transform", Op: "template", Template: "{{files.item}}:{{syms.item}} ({{files.item_index}}.{{syms.item_index}}/{{syms.item_count}})", X: 300},
		flow.Node{ID: "in", Type: "gather", Title: "Symbols", Mode: "concat", Separator: ",", X: 400},
		flow.Node{ID: "out", Type: "gather", Title: "Files", Mode: "concat", Separator: "|", X: 500},
		flow.Node{ID: "md", Type: "output", Format: "txt", X: 600})
	f.Edges = []flow.Edge{{From: "start", To: "files"}, {From: "files", To: "syms"}, {From: "syms", To: "pair"}, {From: "pair", To: "in"}, {From: "in", To: "out"}, {From: "out", To: "md"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	for _, i := range h.ValidateFlow(*f) {
		if i.Level == "error" {
			t.Fatalf("%+v", i)
		}
	}
	st, err := h.RunFlow("nested", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s", st.Status, st.Error)
	}
	var got string
	for _, s := range st.Steps {
		if s.ID == "out" {
			got = s.Output
		}
	}
	if want := "a.go:x (1.1/2),a.go:y (1.2/2)|b.go:x (2.1/2),b.go:y (2.2/2)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
