package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
)

func TestToolGuideSaveRunAndShare(t *testing.T) {
	h, _ := flowHub(t)
	e := extension.Extension{Name: "count-lines", Desc: "Counts matching lines", Lang: "Bash", Scope: "project", Approved: true,
		Params:  []extension.Param{{Name: "file", Type: "string", Required: true}, {Name: "label", Type: "string"}},
		Command: "grep -c {{label}} {{file}}"}
	saved, err := h.SaveExtension(e)
	if err != nil || saved.Runnable() != nil {
		t.Fatalf("%+v %v", saved, err)
	}
	g := &extension.ToolGuide{Summary: " Counts lines. ", When: []string{"a", " ", "b"},
		Examples: []extension.ToolExample{{Title: "From the step before", Args: map[string]string{"file": "{{input_file}}", "label": "{{sample.buffer_output.json.name}}"}, Sample: "{\"name\": \"n\"}\n"}}}
	got, err := h.SaveToolGuide("count-lines", g)
	if err != nil || got.Guide == nil || got.Guide.Summary != "Counts lines." || len(got.Guide.When) != 2 || got.Guide.Updated.IsZero() {
		t.Fatalf("%+v %v", got.Guide, err)
	}
	// the guide does not change what runs: still approved
	if x, _ := h.exts.Get("count-lines"); x.Guide == nil || x.Runnable() != nil {
		t.Fatalf("after saving the guide: %+v", x)
	}
	// an example runs as a flow step would: the sample is the previous step's output
	run, err := h.RunToolExample(got, got.Guide.Examples[0], nil)
	if err != nil || !run.OK || strings.TrimSpace(run.Output) != "1" || run.Args["label"] != "n" {
		t.Fatalf("%+v %v", run, err)
	}
	// it travels in tool files
	b, err := extension.Bundle(h.root, got, nil)
	if err != nil || b.Guide == nil || len(b.Guide.Examples) != 1 || b.Extension().Guide == nil {
		t.Fatalf("%+v %v", b.Guide, err)
	}
	// limits
	many := &extension.ToolGuide{}
	for range extension.MaxGuideExamples + 1 {
		many.Examples = append(many.Examples, extension.ToolExample{Title: "x"})
	}
	if _, err := h.SaveToolGuide("count-lines", many); err == nil {
		t.Fatal("too many examples should be refused")
	}
	// an empty guide removes it
	if got, err = h.SaveToolGuide("count-lines", &extension.ToolGuide{}); err != nil || got.Guide != nil {
		t.Fatalf("%+v %v", got.Guide, err)
	}
	if _, err := h.SaveToolGuide("nope", g); err == nil {
		t.Fatal("an unknown tool should be refused")
	}
}

func TestExampleArgs(t *testing.T) {
	a := ExampleArgs(extension.ToolExample{Args: map[string]string{"a": "{{input}}", "b": "{{steps.sample}}", "c": "{{sample.output_file}}", "d": "{{input_file}}", "e": "plain"}, Sample: "S"}, "/tmp/f")
	if a["a"] != "S" || a["b"] != "S" || a["c"] != "/tmp/f" || a["d"] != "/tmp/f" || a["e"] != "plain" {
		t.Fatalf("%v", a)
	}
}
