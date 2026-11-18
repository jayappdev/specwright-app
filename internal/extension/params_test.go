package extension

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestJSONSchemaParamsKeepEverything(t *testing.T) {
	ps := jsonSchemaParams(map[string]any{
		"type": "object", "required": []any{"path"},
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "File to format", "format": "uri-reference"},
			"indent": map[string]any{"type": []any{"integer", "null"}, "default": 2.0},
			"style":  map[string]any{"type": "string", "enum": []any{"compact", "pretty"}, "examples": []any{"pretty"}},
			"since":  map[string]any{"anyOf": []any{map[string]any{"type": "string", "format": "date"}, map[string]any{"type": "null"}}},
			"opts":   map[string]any{"type": "object", "properties": map[string]any{"wrap": map[string]any{"type": "boolean"}}},
		}})
	by := map[string]Param{}
	for _, p := range ps {
		by[p.Name] = p
	}
	if ps[0].Name != "path" || !by["path"].Required || by["path"].Format != "url" {
		t.Fatalf("%+v", ps)
	}
	if by["indent"].Type != "integer" || by["indent"].Default != "2" {
		t.Fatalf("%+v", by["indent"])
	}
	if strings.Join(by["style"].Enum, ",") != "compact,pretty" || by["style"].Example != "pretty" {
		t.Fatalf("%+v", by["style"])
	}
	if by["since"].Format != "date" || by["opts"].Schema == nil || by["opts"].Schema.Prop("wrap") == nil {
		t.Fatalf("%+v %+v", by["since"], by["opts"])
	}
}

func TestInferParams(t *testing.T) {
	e := Extension{Command: "node x.js {{file}} {{port}} {{mode}} {{extra}}", Params: []Param{
		{Name: "file", Type: "string"}, {Name: "port", Type: "string"},
		{Name: "mode", Type: "string", Desc: "Output format, one of: md, html or json (default md)"},
		{Name: "since", Type: "string"}, {Name: "dry_run", Type: "string"}, {Name: "query", Type: "string"},
		{Name: "api_url", Type: "string"}, {Name: "out_dir", Type: "string"},
		{Name: "count", Type: "string", Format: "multiline"}, // declared: kept
	}}
	ps, ch := InferParams(e)
	by := map[string]Param{}
	for _, p := range ps {
		by[p.Name] = p
	}
	want := map[string]string{"file": "string/path", "port": "integer/", "since": "string/date", "dry_run": "boolean/",
		"query": "string/multiline", "api_url": "string/url", "out_dir": "string/directory", "count": "string/multiline"}
	for n, w := range want {
		if got := by[n].Type + "/" + by[n].Format; got != w {
			t.Errorf("%s: %s, want %s", n, got, w)
		}
	}
	if strings.Join(by["mode"].Enum, ",") != "md,html,json" || by["mode"].Default != "md" {
		t.Errorf("mode %+v", by["mode"])
	}
	if p, ok := by["extra"]; !ok || !p.Required {
		t.Errorf("a placeholder that is not a parameter becomes one: %+v", ps)
	}
	if len(ch) < 8 {
		t.Errorf("changes %+v", ch)
	}
}

func TestCheckArgsConvertsAndRefuses(t *testing.T) {
	e := Extension{Params: []Param{
		{Name: "port", Type: "integer", Required: true}, {Name: "ok", Type: "boolean"}, {Name: "mode", Type: "string", Enum: []string{"a", "b"}, Default: "a"},
		{Name: "opts", Type: "object"}, {Name: "day", Type: "string", Format: "date"},
	}}
	got, probs := CheckArgs(e, map[string]any{"port": "5432", "ok": "yes", "opts": `{"x": 1}`, "day": "2026-11-17"})
	if len(probs) != 0 || got["port"] != int64(5432) || got["ok"] != true || got["mode"] != "a" || got["opts"].(map[string]any)["x"] != 1.0 {
		t.Fatalf("%v %v", got, probs)
	}
	_, probs = CheckArgs(e, map[string]any{"port": "x", "mode": "c", "day": "17/11/2026", "opts": "[1]"})
	if len(probs) != 4 {
		t.Fatalf("%v", probs)
	}
	_, probs = CheckArgs(e, map[string]any{})
	if len(probs) != 1 || !strings.Contains(probs[0], "port is required") {
		t.Fatalf("%v", probs)
	}
}

func TestCheckOutput(t *testing.T) {
	e := Extension{Output: &flow.Schema{Type: "object", Required: []string{"rows"}, Properties: flow.Props{{Name: "rows", Schema: &flow.Schema{Type: "array", Items: &flow.Schema{Type: "object", Required: []string{"id"}}}}}}}
	if p := CheckOutput(e, nil, `{"rows": [{"id": 1}]}`); len(p) != 0 {
		t.Fatal(p)
	}
	if p := CheckOutput(e, nil, `{"rows": [{"name": "x"}]}`); len(p) != 1 || !strings.Contains(p[0], `rows[0] has no field "id"`) {
		t.Fatal(p)
	}
	if p := CheckOutput(e, nil, "plain text"); len(p) != 1 {
		t.Fatal(p)
	}
	if p := CheckOutput(Extension{}, nil, "anything"); p != nil {
		t.Fatal("nothing declared: nothing to check")
	}
}
