package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/flow"
)

// An AI step's own instructions and skills go into its prompt, with the
// flow's (or without the flow's skills, when the step says so).
func TestAIStepInstructionsAndSkills(t *testing.T) {
	h, ws := flowHub(t)
	f := &flow.Flow{Skills: []flow.Skill{{Path: "skills/house-style.md"}}}
	n := &flow.Node{ID: "ai1", Type: "ai", Instructions: "Write for {{inputs.who}}; at most 5 bullets.",
		Skills: []flow.Skill{{Path: "skills/release-notes.md"}, {Path: "skills/house-style.md"}}}
	d := flow.Data{Inputs: map[string]string{"who": "the support team"}}
	got := h.aiSuffix(f, n, d)
	for _, want := range []string{"skills/house-style.md", "skills/release-notes.md", "## Instructions for this step", "Write for the support team; at most 5 bullets."} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "skills/house-style.md") != 1 {
		t.Fatalf("a skill listed twice:\n%s", got)
	}
	n.FlowSkillsOff = true
	if got := h.aiSuffix(f, n, d); strings.Contains(got, "house-style") == false || strings.Contains(got, "release-notes") == false {
		// house-style is also the step's own skill, so it stays
		t.Fatalf("step skills with the flow's off:\n%s", got)
	}
	n.Skills = n.Skills[:1]
	if got := h.aiSuffix(f, n, d); strings.Contains(got, "house-style") {
		t.Fatalf("the flow's skills should be left out:\n%s", got)
	}
	if got := h.aiSuffix(f, nil, d); !strings.Contains(got, "house-style") || strings.Contains(got, "Instructions for this step") {
		t.Fatalf("no step: only the flow's\n%s", got)
	}

	// a step's skill file travels with an exported flow
	_ = os.MkdirAll(filepath.Join(ws, "skills"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "skills", "release-notes.md"), []byte("# Release notes\n"), 0o644)
	g := flow.New("withskill", "With a step skill")
	g.Nodes = append(g.Nodes, flow.Node{ID: "ai1", Type: "ai", Prompt: "x", Skills: []flow.Skill{{Path: "skills/release-notes.md"}}})
	if _, err := h.SaveFlow(*g); err != nil {
		t.Fatal(err)
	}
	b, err := h.ExportFlow("withskill")
	if err != nil || !strings.Contains(b, "skills/release-notes.md") || !strings.Contains(b, "IyBSZWxlYXNlIG5vdGVz") { // base64 of the file
		t.Fatalf("export: %v\n%.400s", err, b)
	}
}

// a chart step is previewed (drawn, not saved); other tools are not
func TestPreviewChartStep(t *testing.T) {
	h, ws := flowHub(t)
	f := flow.New("ch", "Chart")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "rows", Type: "transform", Op: "template", Template: "x"},
		flow.Node{ID: "plot", Type: "tool", Tool: "builtin:chart", Args: map[string]string{"type": "bar", "rows": "{{rows.buffer_output}}", "x": "suite", "title": "Tests"}},
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "x"}})
	f.Edges = append(f.Edges, flow.Edge{From: "start", To: "rows"}, flow.Edge{From: "rows", To: "plot"})
	p := h.PreviewFlowStep(*f, "plot", flow.Data{Steps: map[string]string{"rows": `[{"suite":"api","passed":10,"failed":1}]`}})
	if p.Error != "" || !strings.Contains(p.Output, "data-specwright-chart") || !strings.Contains(p.Output, ">failed<") {
		t.Fatalf("%+v", p)
	}
	if _, err := os.Stat(filepath.Join(ws, "out", "charts")); !os.IsNotExist(err) {
		t.Fatal("a preview saved a chart")
	}
	if p := h.PreviewFlowStep(*f, "echo", flow.Data{}); p.Error == "" {
		t.Fatal("other tools are not previewed")
	}
}
