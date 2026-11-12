package core

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

func runFlowTest(t *testing.T, h *Hub, f *flow.Flow, inputs map[string]string) (FlowRunState, map[string]FlowStepState) {
	t.Helper()
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	for _, i := range h.ValidateFlow(*f) {
		if i.Level == "error" {
			t.Fatalf("%+v", i)
		}
	}
	st, err := h.RunFlow(f.ID, FlowRunOptions{Review: "none", Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	steps := map[string]FlowStepState{}
	for _, s := range st.Steps {
		steps[s.ID] = s
	}
	return st, steps
}

// A decision routes to one path; the paths join again in a merge.
func TestFlowDecisionRoutes(t *testing.T) {
	h, _ := flowHub(t)
	f := flow.New("route", "Route")
	f.Inputs = []flow.Input{{Name: "data", Type: "multiline"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "rows", Type: "transform", Op: "template", Template: "{{inputs.data}}", X: 100},
		flow.Node{ID: "open", Type: "filter", Cond: &flow.Cond{Rules: []flow.Rule{{Field: "status", Op: "eq", Value: "open"}}}, X: 200},
		flow.Node{ID: "sum", Type: "aggregate", Measures: []flow.Measure{{Fn: "count", As: "n"}, {Fn: "sum", Field: "total", As: "total"}}, X: 300},
		flow.Node{ID: "check", Type: "decision", X: 400, Branches: []flow.Branch{
			{ID: "big", Label: "Big", When: &flow.Cond{Rules: []flow.Rule{{Field: "[0].total", Op: "gte", Value: "100"}}}},
			{ID: "some", Label: "Some", When: &flow.Cond{Rules: []flow.Rule{{Field: "[0].n", Op: "gt", Value: "0"}}}}}},
		flow.Node{ID: "big", Type: "transform", Op: "template", Template: "BIG {{check.buffer_output.json[0].total}}", X: 500},
		flow.Node{ID: "some", Type: "transform", Op: "template", Template: "SOME", X: 500, Y: 100},
		flow.Node{ID: "none", Type: "transform", Op: "template", Template: "NONE", X: 500, Y: 200},
		flow.Node{ID: "join", Type: "merge", Mode: "concat", X: 600},
		flow.Node{ID: "o", Type: "output", Format: "txt", X: 700})
	f.Edges = []flow.Edge{{From: "start", To: "rows"}, {From: "rows", To: "open"}, {From: "open", To: "sum"}, {From: "sum", To: "check"},
		{From: "check", To: "big", Branch: "big"}, {From: "check", To: "some", Branch: "some"}, {From: "check", To: "none", Branch: flow.ElseBranch},
		{From: "big", To: "join"}, {From: "some", To: "join"}, {From: "none", To: "join"}, {From: "join", To: "o"}}
	for _, c := range []struct{ data, path, out, off string }{
		{`[{"status":"open","total":70},{"status":"open","total":50},{"status":"closed","total":900}]`, "Big", "BIG 120", "some,none"},
		{`[{"status":"open","total":7}]`, "Some", "SOME", "big,none"},
		{`[{"status":"closed","total":7}]`, "Otherwise", "NONE", "big,some"},
	} {
		st, steps := runFlowTest(t, h, f, map[string]string{"data": c.data})
		if st.Status != "done" {
			t.Fatalf("%s: %s %s", c.path, st.Status, st.Error)
		}
		if steps["check"].Path != c.path || steps["o"].Output != c.out {
			t.Fatalf("path %q output %q, want %q %q", steps["check"].Path, steps["o"].Output, c.path, c.out)
		}
		for _, id := range strings.Split(c.off, ",") {
			if s := steps[id]; s.Status != "skipped" || !strings.Contains(s.Error, "not on the path") {
				t.Fatalf("%s must be off the path: %+v", id, s)
			}
		}
	}
}

// A decision inside a loop routes each item; Gather collects what ran.
func TestFlowDecisionInLoop(t *testing.T) {
	h, _ := flowHub(t)
	f := flow.New("per-item", "Per item")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "each", Type: "loop", Source: `[{"n":1},{"n":5},{"n":9}]`, X: 100},
		flow.Node{ID: "d", Type: "decision", X: 200, Branches: []flow.Branch{{ID: "hi", When: &flow.Cond{Rules: []flow.Rule{{Field: "n", Op: "gt", Value: "4"}}}}}},
		flow.Node{ID: "hi", Type: "transform", Op: "template", Template: "hi {{each.item.json.n}}", X: 300},
		flow.Node{ID: "lo", Type: "transform", Op: "template", Template: "lo {{each.item.json.n}}", X: 300, Y: 100},
		flow.Node{ID: "g", Type: "gather", Mode: "concat", Separator: ",", X: 400},
		flow.Node{ID: "o", Type: "output", Format: "txt", X: 500})
	f.Edges = []flow.Edge{{From: "start", To: "each"}, {From: "each", To: "d"}, {From: "d", To: "hi", Branch: "hi"}, {From: "d", To: "lo", Branch: flow.ElseBranch},
		{From: "hi", To: "g"}, {From: "lo", To: "g"}, {From: "g", To: "o"}}
	st, steps := runFlowTest(t, h, f, nil)
	if st.Status != "done" || steps["o"].Output != "lo 1,hi 5,hi 9" {
		t.Fatalf("%s %s %q", st.Status, st.Error, steps["o"].Output)
	}
}

func TestPreviewNewSteps(t *testing.T) {
	h, _ := flowHub(t)
	f := flow.New("p", "P")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "src", Type: "transform", Op: "trim"},
		flow.Node{ID: "d", Type: "decision", Branches: []flow.Branch{{ID: "hi", Label: "High", When: &flow.Cond{Rules: []flow.Rule{{Field: "[*].n", Op: "gt", Value: "5"}}}}}},
		flow.Node{ID: "x", Type: "output", Format: "md", Title: "Alert"},
		flow.Node{ID: "flt", Type: "filter", Cond: &flow.Cond{Rules: []flow.Rule{{Field: "n", Op: "gt", Value: "1"}}}},
		flow.Node{ID: "agg", Type: "aggregate", Measures: []flow.Measure{{Fn: "sum", Field: "n", As: "total"}}})
	f.Edges = []flow.Edge{{From: "start", To: "src"}, {From: "src", To: "d"}, {From: "d", To: "x", Branch: "hi"}, {From: "src", To: "flt"}, {From: "src", To: "agg"}}
	sample := flow.Data{Steps: map[string]string{"src": `[{"n":1},{"n":9}]`}}
	for id, want := range map[string]string{"d": "→ High (runs Alert)", "flt": `"n": 9`, "agg": `"total": 10`} {
		p := h.PreviewFlowStep(*f, id, sample)
		if p.Error != "" || !strings.Contains(p.Output, want) {
			t.Errorf("%s: %q %q", id, p.Output, p.Error)
		}
	}
}
