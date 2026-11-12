package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

const greeterExt = `import { joinSession } from "@github/copilot-sdk/extension";
await joinSession({ tools: [{
  name: "greet", description: "Say hello to someone.",
  parameters: { type: "object", properties: { who: { type: "string" } }, required: ["who"] },
  handler: async ({ who }) => "Hello, " + who,
}] });
`

// A Copilot extension loaded into the project behaves like a project tool:
// agents can call it and a flow can use it as a step.
func TestLoadedCopilotExtensionIsAProjectTool(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	usePolicy(t, "")
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	src := filepath.Join(home, "extensions", "greeter")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.mjs"), []byte(greeterExt), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _ := newHub(t)
	r, err := h.LoadCopilotExtension("greeter", false, false)
	if err != nil || r.Note != "" || len(r.Tools) != 1 || r.Tools[0] != "greet" {
		t.Fatalf("load: %+v %v", r, err)
	}
	found := false
	for _, e := range h.Callable() {
		found = found || (e.Name == "greet" && e.Kind == extension.KindCopilot)
	}
	if !found {
		t.Fatal("greet must be callable by agents")
	}
	if _, err := h.SaveExtension(extension.Extension{Name: "greet", Kind: extension.KindCopilot}); err == nil {
		t.Fatal("copilot tools change through their files, not SaveExtension")
	}

	f := flow.New("hello", "Hello")
	f.Inputs = []flow.Input{{Name: "who", Label: "Who", Type: "text", Required: true}}
	f.Nodes = append(f.Nodes, flow.Node{ID: "g", Type: "tool", Tool: "greet", Args: map[string]string{"who": "{{inputs.who}}"}, X: 200})
	f.Edges = []flow.Edge{{From: "start", To: "g"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	st, err := h.RunFlow("hello", FlowRunOptions{Review: "none", Inputs: map[string]string{"who": "Ada"}})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Steps)
	}
	var out string
	for _, s := range st.Steps {
		if s.ID == "g" {
			out = s.Output
		}
	}
	if !strings.Contains(out, "Hello, Ada") {
		t.Fatalf("step output %q (%+v)", out, st.Steps)
	}

	if _, err := h.RemoveCopilotExtension("greeter"); err != nil {
		t.Fatal(err)
	}
	for _, e := range h.Callable() {
		if e.Name == "greet" {
			t.Fatal("removed extension must not stay callable")
		}
	}
}
