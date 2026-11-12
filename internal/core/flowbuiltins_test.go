package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestFlowBuiltinTools(t *testing.T) {
	usePolicy(t, `{"disabledBuiltinTools": ["fetch_webpage"]}`)
	h, ws := newHub(t)
	_ = os.MkdirAll(filepath.Join(ws, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "src", "a.txt"), []byte("alpha\nTODO fix beta\ngamma\n"), 0o644)

	names := map[string]bool{}
	for _, e := range h.FlowBuiltinTools() {
		names[e.Name] = true
	}
	if !names["builtin:read_file"] || !names["builtin:text_search"] || names["builtin:fetch_webpage"] || names["builtin:todo_list"] {
		t.Fatalf("offered: %v", names)
	}

	f := flow.New("bt", "Built-ins")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "find", Type: "tool", Tool: "builtin:text_search", Args: map[string]string{"query": "TODO", "include": "**/*.txt", "context_lines": "0"}},
		flow.Node{ID: "read", Type: "tool", Tool: "builtin:read_file", Args: map[string]string{"path": "src/a.txt", "start_line": "2", "end_line": "2"}},
		flow.Node{ID: "out", Type: "output", Format: "md", Template: "{{text_search.buffer_output}}\n---\n{{read_file.buffer_output}}",
			Actions: []flow.Action{{Type: "tool", Tool: "builtin:create_file", Args: map[string]string{"path": "out/report.md", "content": "{{output}}", "overwrite": "true"}}}})
	f.Edges = []flow.Edge{{From: "start", To: "find"}, {From: "find", To: "read"}, {From: "read", To: "out"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	for _, i := range h.ValidateFlow(*f) {
		if i.Level == "error" {
			t.Fatalf("issue: %+v", i)
		}
	}
	st, err := h.RunFlow("bt", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Steps)
	}
	b, _ := os.ReadFile(filepath.Join(ws, "out", "report.md"))
	if !strings.Contains(string(b), "TODO fix beta") || !strings.Contains(string(b), "src/a.txt") || strings.Contains(string(b), "gamma") {
		t.Fatalf("report: %q", b)
	}

	// a policy-disabled tool is refused when checked and when run
	g := *f
	g.Nodes = append([]flow.Node{}, f.Nodes...)
	g.Nodes[1] = flow.Node{ID: "find", Type: "tool", Tool: "builtin:fetch_webpage", Args: map[string]string{"url": "https://example.com"}}
	bad := false
	for _, i := range h.ValidateFlow(g) {
		bad = bad || (i.Level == "error" && strings.Contains(i.Message, "fetch_webpage"))
	}
	if !bad {
		t.Fatal("a disabled built-in must be flagged")
	}
}

func TestBuiltinArgsTyping(t *testing.T) {
	h, _ := newHub(t)
	e, err := h.flowBuiltin("builtin:text_search")
	if err != nil {
		t.Fatal(err)
	}
	got, err := builtinArgs(e, map[string]any{"query": " a b ", "is_regex": "yes", "max_results": "5", "include": "**/*.go\n**/*.js", "exclude": `["x/**"]`, "path": ""})
	if err != nil {
		t.Fatal(err)
	}
	if got["query"] != " a b " || got["is_regex"] != true || got["max_results"] != float64(5) || len(got["include"].([]any)) != 2 || len(got["exclude"].([]any)) != 1 {
		t.Fatalf("%#v", got)
	}
	if _, ok := got["path"]; ok {
		t.Fatal("empty fields are left out")
	}
	if _, err := builtinArgs(e, map[string]any{"max_results": "many"}); err == nil {
		t.Fatal("a bad number must fail")
	}
}
