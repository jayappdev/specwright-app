package core

import (
	"os"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// Multi-step tool chains: a tool that writes a file and prints nothing
// still gives the next step a real {{input_file}}, and any later step can
// read an earlier one by tool name: {{tool.output_file}} / {{tool.buffer_output}}.
func TestFlowToolChainStepOutputs(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	for _, e := range []extension.Extension{
		{Name: "emit", Desc: "Echo", Lang: "Bash", Scope: "project", Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "echo {{v}}"},
		{Name: "readfile", Desc: "Cat", Lang: "Bash", Scope: "project", Params: []extension.Param{{Name: "path", Type: "string", Required: true}}, Command: "cat {{path}}"},
		{Name: "quiet", Desc: "Copies a file, prints nothing", Lang: "Bash", Scope: "project", Params: []extension.Param{{Name: "path", Type: "string", Required: true}}, Command: "cp {{path}} copied.txt"},
		{Name: "both", Desc: "Two args", Lang: "Bash", Scope: "project", Params: []extension.Param{{Name: "f", Type: "string", Required: true}, {Name: "t", Type: "string", Required: true}}, Command: "printf %s|%s {{t}} {{f}}"},
	} {
		if _, err := h.SaveExtension(e); err != nil {
			t.Fatal(err)
		}
	}
	f := flow.New("chain", "Chain")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "a", Type: "tool", Tool: "emit", Args: map[string]string{"v": "hello"}, X: 200},
		flow.Node{ID: "b", Type: "tool", Title: "Read it", Tool: "readfile", Args: map[string]string{"path": "{{input_file}}"}, X: 400},
		flow.Node{ID: "c", Type: "tool", Tool: "quiet", Args: map[string]string{"path": "{{emit.output_file}}"}, X: 600},
		// c printed nothing: {{input_file}} is an empty file, and earlier steps are still reachable
		flow.Node{ID: "d", Type: "tool", Tool: "readfile", Args: map[string]string{"path": "{{input_file}}"}, X: 800},
		flow.Node{ID: "e", Type: "tool", Tool: "both", Args: map[string]string{"f": "{{read-it.output_file}}", "t": "{{emit.buffer_output}}"}, X: 1000},
		flow.Node{ID: "o", Type: "output", Format: "txt", Template: "{{a.buffer_output}} / {{steps.b.buffer_output}}", X: 1200})
	f.Edges = []flow.Edge{{From: "start", To: "a"}, {From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "d"}, {From: "d", To: "e"}, {From: "e", To: "o"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	for _, is := range h.ValidateFlow(*f) {
		if is.Level == "error" {
			t.Fatalf("validation: %+v", is)
		}
	}
	st, err := h.RunFlow("chain", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Steps)
	}
	out := map[string]FlowStepState{}
	for _, s := range st.Steps {
		out[s.ID] = s
	}
	if out["b"].Output != "hello" {
		t.Fatalf("b: %+v", out["b"])
	}
	if b, _ := os.ReadFile(ws + "/copied.txt"); strings.TrimSpace(string(b)) != "hello" {
		t.Fatalf("c must have copied emit's output file: %q (args %v)", b, out["c"].Args)
	}
	if out["d"].Status != "done" || out["d"].Output != "" || out["d"].Args["path"] == "" {
		t.Fatalf("d gets an (empty) input file after a silent step: %+v", out["d"])
	}
	if !strings.HasPrefix(out["e"].Output, "hello|") || !strings.HasSuffix(out["e"].Output, "/steps/b/output.txt") {
		t.Fatalf("e: %q", out["e"].Output)
	}
	if out["c"].OutputFile == "" {
		t.Fatal("a silent step still has an output file")
	}
	if len(st.Outputs) != 1 || st.Outputs[0].Content != "hello / hello" {
		t.Fatalf("outputs: %+v", st.Outputs)
	}
}

func TestFlowStepVariableValidation(t *testing.T) {
	f := flow.New("v", "V")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "a", Type: "tool", Tool: "emit", X: 200},
		flow.Node{ID: "a2", Type: "tool", Tool: "emit", X: 300},
		flow.Node{ID: "b", Type: "ai", Prompt: "{{emit.output_file}} {{nope.output_file}} {{late.buffer_output}} {{b.output_file}} {{a.output_file}}", X: 400},
		flow.Node{ID: "late", Type: "ai", Prompt: "x", X: 600},
		flow.Node{ID: "o", Type: "output", Format: "txt", X: 800})
	f.Edges = []flow.Edge{{From: "start", To: "a"}, {From: "start", To: "a2"}, {From: "a", To: "b"}, {From: "b", To: "late"}, {From: "late", To: "o"}}
	var msgs []string
	for _, is := range flow.Validate(f, nil) {
		if is.Node == "b" && is.Level == "error" {
			msgs = append(msgs, is.Message)
		}
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{`more than one step is called "emit"`, `no step or tool is called "nope"`, `"late" does not run before`, "cannot read its own output"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "{{a.output_file}}") {
		t.Fatalf("{{a.output_file}} is fine:\n%s", all)
	}
}
