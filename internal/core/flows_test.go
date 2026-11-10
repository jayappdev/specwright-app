package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

func TestFlowDesignerHub(t *testing.T) {
	h, _ := newHub(t)
	if _, err := h.SaveExtension(extension.Extension{Name: "echo-arg", Desc: "Echo", Lang: "Bash", Scope: "project",
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "echo {{v}}"}); err != nil {
		t.Fatal(err)
	}
	f, err := h.NewFlow("Release notes")
	if err != nil || f.ID != "release-notes" {
		t.Fatal(f, err)
	}
	f.Inputs = []flow.Input{{Name: "version", Label: "Version", Type: "text"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "{{inputs.version}}"}, X: 300},
		flow.Node{ID: "up", Type: "transform", Op: "uppercase", X: 500},
		flow.Node{ID: "out", Type: "output", Format: "html", Template: "# Release {{inputs.version}}\n\n{{steps.up}}", X: 700})
	f.Edges = []flow.Edge{{From: "start", To: "echo"}, {From: "echo", To: "up"}, {From: "up", To: "out"}}
	st, err := h.SaveFlow(*f)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range st.Issues {
		if is.Level == "error" {
			t.Fatal(is)
		}
	}
	sample := flow.Data{Inputs: map[string]string{"version": "1.2"}, Steps: map[string]string{"echo": "fixed bug", "up": "FIXED BUG"}}
	if p := h.PreviewFlowStep(*st.Flow, "up", sample); p.Output != "FIXED BUG" || p.Error != "" {
		t.Fatal(p)
	}
	if p := h.PreviewFlowStep(*st.Flow, "out", sample); !strings.Contains(p.Output, "<h1>Release 1.2</h1>") {
		t.Fatal(p)
	}
	data, err := h.ExportFlow("release-notes")
	if err != nil {
		t.Fatal(err)
	}
	imp, err := h.ImportFlow(data)
	if err != nil || imp.Flow.ID != "release-notes-2" {
		t.Fatal(imp, err)
	}
	list, _ := h.Flows()
	if len(list) != 2 {
		t.Fatal(list)
	}
	if err := h.DeleteFlow("release-notes-2"); err != nil {
		t.Fatal(err)
	}
}
