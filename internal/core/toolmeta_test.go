package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

func TestToolMetaInspectRunAndTestCases(t *testing.T) {
	h, _ := flowHub(t)
	e := extension.Extension{Name: "greet-n", Desc: "Greets", Lang: "Bash", Scope: "project", Approved: true,
		Params:  []extension.Param{{Name: "who", Type: "string", Required: true}, {Name: "count", Type: "string"}},
		Command: "echo {{who}} {{count}} {{mode}}"}
	saved, err := h.SaveExtension(e)
	if err != nil {
		t.Fatal(err)
	}
	// without AI: the placeholder becomes a parameter, count a whole number
	in, err := h.InspectTool(saved)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]extension.Param{}
	for _, p := range in.Params {
		by[p.Name] = p
	}
	if by["count"].Type != "integer" || by["mode"].Required || len(in.Changes) < 2 {
		t.Fatalf("%+v %+v", in.Params, in.Changes)
	}
	// saving metadata keeps the approval (what runs did not change)
	meta := extension.ToolMeta{Params: in.Params, Output: &flow.Schema{Type: "string"},
		Tests: []extension.ToolTest{{Name: "hello", Args: map[string]any{"who": "Ada", "count": "2", "mode": "x"}, Expect: extension.ToolExpect{Contains: []string{"Ada 2 x"}}},
			{Name: "bad count", Args: map[string]any{"who": "Ada", "count": "two", "mode": "x"}, Expect: extension.ToolExpect{Fail: true}}}}
	got, err := h.SaveToolMeta("greet-n", meta)
	if err != nil || len(got.Tests) != 2 || got.MetaAt.IsZero() || got.Runnable() != nil {
		t.Fatalf("%+v %v", got, err)
	}
	// a direct run: checked, converted, run
	run, err := h.RunToolWith(got, map[string]any{"who": "Bo", "count": "3", "mode": "y"}, nil, nil)
	if err != nil || !run.OK || strings.TrimSpace(run.Output) != "Bo 3 y" || run.Args["count"] != int64(3) {
		t.Fatalf("%+v %v", run, err)
	}
	// refused before it runs
	run, _ = h.RunToolWith(got, map[string]any{"who": "Bo", "count": "x"}, nil, nil)
	if len(run.Problems) != 1 || run.OK || run.Output != "" { // count; mode is optional (added from the command)
		t.Fatalf("%+v", run)
	}
	// the saved test cases
	res, err := h.RunToolTests(got, nil, nil)
	if err != nil || len(res) != 2 || !res[0].Pass || !res[1].Pass {
		t.Fatalf("%+v %v", res, err)
	}
	// agents are told too: the bridge refuses arguments that do not fit
	if _, probs := extension.CheckArgs(got, map[string]any{"who": "x", "count": "many", "mode": "m"}); len(probs) != 1 {
		t.Fatal(probs)
	}
}
