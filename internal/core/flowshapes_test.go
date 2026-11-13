package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// Shapes are designer metadata: the AI gets a compact shape only for an AI
// step's expected reply and for JSON parameters it fills in.
func TestShapesReachTheAIOnlyWhenNeeded(t *testing.T) {
	h, _ := flowHub(t)
	out := &flow.Schema{Type: "array", Items: &flow.Schema{Type: "object", Properties: flow.Props{{Name: "id", Schema: &flow.Schema{Type: "string", Description: "order id"}}}}}
	if _, err := h.SaveExtension(extension.Extension{Name: "orders", Desc: "Orders", Lang: "Bash", Scope: "project", Command: "echo []",
		Params: []extension.Param{{Name: "filter", Type: "json", Desc: "what to fetch", Schema: &flow.Schema{Type: "object", Properties: flow.Props{{Name: "status", Schema: &flow.Schema{Type: "string", Enum: []any{"open", "closed"}}}}}}, {Name: "limit", Type: "number"}},
		Output: out}); err != nil {
		t.Fatal(err)
	}
	f := flow.New("s", "S")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "plain", Type: "ai", Prompt: "Summarise"},
		flow.Node{ID: "typed", Type: "ai", Prompt: "Classify", OutputSchema: &flow.Schema{Type: "object", Properties: flow.Props{{Name: "type", Schema: &flow.Schema{Type: "string", Enum: []any{"bug", "chore"}}}}}})
	d := flow.Data{Input: `[{"id":"A1"}]`, Inputs: map[string]string{}}
	plain := h.aiStepPrompt(f, f.Node("plain"), d, 1)
	if strings.Contains(plain, "shape") || strings.Contains(plain, "order id") {
		t.Fatal("no shapes without an expected reply:\n" + plain)
	}
	typed := h.aiStepPrompt(f, f.Node("typed"), d, 1)
	if !strings.Contains(typed, `Reply with only JSON in this shape, with no explanation and no code fences:
{type: "bug" | "chore"}`) {
		t.Fatal(typed)
	}
	// filling in a tool's arguments: the JSON parameter's shape, never the output's
	exts, _ := h.Extensions()
	var e extension.Extension
	for _, x := range exts {
		if x.Name == "orders" {
			e = x
		}
	}
	if e.Output == nil || e.Params[0].Schema == nil {
		t.Fatal("the shapes are saved with the tool")
	}
	p := aiArgsPrompt(&e, "open orders", nil, d)
	if !strings.Contains(p, `  shape: {status: "open" | "closed"}`) || strings.Contains(p, "order id") || strings.Count(p, "shape:") != 1 {
		t.Fatal(p)
	}
	// the designer gets the declared output shape
	g := flow.New("g", "G")
	g.Nodes = append(g.Nodes, flow.Node{ID: "o", Type: "tool", Tool: "orders"}, flow.Node{ID: "f", Type: "filter", Cond: &flow.Cond{Rules: []flow.Rule{{Field: "id", Op: "not_empty"}}}})
	g.Edges = []flow.Edge{{From: "start", To: "o"}, {From: "o", To: "f"}}
	sh := h.FlowShapes(*g, nil)
	if sh["o"].Source != "declared" || sh["f"].Rows.Prop("id") == nil {
		t.Fatalf("%+v", sh["o"])
	}
	if sh := h.FlowShapes(*g, map[string]string{"o": "not json"}); sh["o"].Source != "declared" {
		t.Fatal("a declared shape wins over a sample", sh["o"])
	}
	g.Nodes[1] = flow.Node{ID: "o", Type: "tool", Tool: "echo-arg"} // no declared shape: learned from the sample
	if sh := h.FlowShapes(*g, map[string]string{"o": `[{"n":1}]`}); sh["o"].Source != "sample" || sh["f"].Rows.Prop("n") == nil {
		t.Fatal("learned from a sample", sh["o"])
	}
}

func TestJSONReply(t *testing.T) {
	for in, want := range map[string]string{
		"```json\n{\"a\":1}\n```":           `{"a":1}`,
		"Here you go: [1,2] — done.":        "[1,2]",
		`{"a":1}`:                           `{"a":1}`,
		"no json here":                      "no json here",
		"```\n[{\"x\":\"y\"}]\n```\nThanks": `[{"x":"y"}]`,
	} {
		if got := jsonReply(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// fetch_webpage with format=json declares its fields to the steps after it.
func TestFetchJSONShapeInFlows(t *testing.T) {
	h, _ := flowHub(t)
	f := flow.New("web", "Web")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "get", Type: "tool", Tool: "builtin:fetch_webpage", Args: map[string]string{"url": "https://api.example.com/q", "format": "json"}},
		flow.Node{ID: "ok", Type: "decision", Branches: []flow.Branch{{ID: "yes", When: &flow.Cond{Rules: []flow.Rule{{Field: "ok", Op: "true"}, {Field: "stauts", Op: "eq", Value: "200"}}}}}},
		flow.Node{ID: "o", Type: "output", Format: "json", Template: "{{get.buffer_output.json.json}}", Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "get"}, {From: "get", To: "ok"}, {From: "ok", To: "o", Branch: "yes"}}
	sh := h.FlowShapes(*f, nil)
	if sh["get"].Source != "declared" || sh["ok"].In.Prop("status") == nil {
		t.Fatalf("%+v", sh["get"])
	}
	var warns []string
	for _, i := range h.ValidateFlow(*f) {
		if strings.Contains(i.Message, "is not in") {
			warns = append(warns, i.Message)
		}
	}
	if len(warns) != 1 || !strings.Contains(warns[0], `"stauts"`) {
		t.Fatal(warns)
	}
	// text (the default): no shape
	f.Nodes[1].Args["format"] = "text"
	if sh := h.FlowShapes(*f, nil); sh["get"].Source == "declared" {
		t.Fatal("text has no declared shape")
	}
}
