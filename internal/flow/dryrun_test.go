package flow

import (
	"strings"
	"testing"
)

func TestDryRun(t *testing.T) {
	gate := SchemaOfText(`{"issues": [{"severity": "BLOCKER", "file": "a.go", "message": "x"}]}`)
	gate.Prop("issues").Items.Prop("severity").Enum = []any{"BLOCKER", "MINOR"}
	tools := map[string]DryTool{"gate": {Output: gate}, "fetch": {Required: []string{"url"}}}
	f := New("d", "D")
	f.Inputs = []Input{{Name: "team", Label: "Team"}}
	f.Nodes = append(f.Nodes,
		Node{ID: "scan", Type: "tool", Tool: "gate"},
		Node{ID: "blockers", Type: "filter", Cond: &Cond{Rules: []Rule{{Field: "severity", Op: "eq", Value: "BLOCKER"}}}},
		Node{ID: "byFile", Type: "aggregate", GroupBy: []string{"file"}, Measures: []Measure{{Fn: "count", As: "n"}}},
		Node{ID: "any", Type: "decision", Branches: []Branch{{ID: "some", Label: "Some", When: &Cond{Rules: []Rule{{Op: "len_gt", Value: "0"}}}}}},
		Node{ID: "each", Type: "loop", Split: "json"},
		Node{ID: "line", Type: "transform", Op: "template", Template: "{{item.json.file}}: {{item.json.n}} for {{inputs.team}}"},
		Node{ID: "g", Type: "gather", Mode: "concat", Separator: "\n"},
		Node{ID: "report", Type: "output", Format: "md", Title: "Report"},
		Node{ID: "none", Type: "output", Format: "txt", Title: "All clear"})
	f.Edges = []Edge{{From: "start", To: "scan"}, {From: "scan", To: "blockers"}, {From: "blockers", To: "byFile"}, {From: "byFile", To: "any"},
		{From: "any", To: "each", Branch: "some"}, {From: "each", To: "line"}, {From: "line", To: "g"}, {From: "g", To: "report"}, {From: "any", To: "none", Branch: ElseBranch}}
	test := DryTest{Inputs: map[string]string{"team": "orders"}, Expect: []DryExpect{{Step: "report", Contains: []string{"for orders"}}}}
	res := DryRunFlow(f, test, tools)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	st := map[string]DryStep{}
	for _, s := range res.Steps {
		st[s.ID] = s
	}
	if !st["scan"].Mocked || st["any"].Path != "Some" || st["none"].Status != "off" || st["line"].Runs < 1 || !strings.Contains(st["report"].Output, "file 1: 1 for orders") {
		t.Fatalf("%+v", res.Steps)
	}
	// a mock where the filter finds nothing: the Otherwise path; the expectation then fails
	test.Mocks = map[string]any{"scan": map[string]any{"issues": []any{map[string]any{"severity": "MINOR", "file": "b.go"}}}}
	res = DryRunFlow(f, test, tools)
	if res.OK || !strings.Contains(fmtChecks(res), "Report gives the expected result") {
		t.Fatalf("%s", fmtChecks(res))
	}
	// wiring mistakes: an empty required argument, a path that does not exist
	g := New("w", "W")
	g.Nodes = append(g.Nodes, Node{ID: "get", Type: "tool", Tool: "fetch", Args: map[string]string{"url": "{{inputs.nope}}"}},
		Node{ID: "o", Type: "output", Format: "md"})
	g.Edges = []Edge{{From: "start", To: "get"}, {From: "get", To: "o"}}
	res = DryRunFlow(g, DryTest{}, tools)
	if res.OK || !strings.Contains(fmtChecks(res), `the required argument "url" is empty`) || !strings.Contains(fmtChecks(res), "no output step was reached") {
		t.Fatalf("%s", fmtChecks(res))
	}
}

func fmtChecks(r DryResult) string {
	var b strings.Builder
	for _, c := range r.Checks {
		b.WriteString(c.Label + " " + map[bool]string{true: "ok", false: "FAIL"}[c.OK] + " " + c.Note + "\n")
	}
	return b.String()
}
