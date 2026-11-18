package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/flow"
)

// The error from the Playground: an agent wrote JSON Schema with a type
// list into a flow ("cannot unmarshal array into Go struct field
// Schema.type of type string").
func TestAgentEditedFlowStillLoads(t *testing.T) {
	h, ws := flowHub(t)
	p := flow.Path(ws, "notes")
	b, _ := os.ReadFile(p)
	s := strings.Replace(string(b), `"type": "ai",`, `"type": "ai", "outputSchema": {"type": "object", "properties": {"title": {"type": ["string", "null"]}, "count": {"anyOf": [{"type": "integer"}, {"type": "null"}]}, "kind": {"const": "bug"}}, "required": ["title"]},`, 1)
	if s == string(b) {
		t.Fatal("setup")
	}
	_ = os.WriteFile(p, []byte(s), 0o644)
	f, err := h.LoadFlow("notes")
	if err != nil {
		t.Fatal(err)
	}
	sc := f.Node("write").OutputSchema
	if sc.Prop("title").Type != "string" || sc.Prop("count").Type != "integer" || sc.Prop("kind").Enum[0] != "bug" || len(f.Repairs) != 0 {
		t.Fatalf("%+v %v", sc, f.Repairs)
	}
	if list, _ := h.Flows(); len(list) != 1 {
		t.Fatal("the flow stays in the list")
	}

	// other things that do not fit (a number where text goes): repaired, backed up
	b, _ = os.ReadFile(p)
	_ = os.WriteFile(p, []byte(strings.Replace(string(b), `"v": "fixed {{inputs.version}}"`, `"v": 42`, 1)), 0o644)
	f, err = h.LoadFlow("notes")
	if err != nil || len(f.Repairs) < 2 || f.Node("echo").Args["v"] != "42" {
		t.Fatalf("%v %+v", err, f.Repairs)
	}
	baks, _ := filepath.Glob(filepath.Join(ws, ".codegen", "flows", ".backup", "notes-*.json"))
	if len(baks) != 1 {
		t.Fatal("the file as the agent wrote it is kept")
	}
	if f2, _ := flow.Load(ws, "notes"); len(f2.Repairs) != 0 {
		t.Fatal("the repaired flow is saved: it loads cleanly now")
	}
}

func TestSaveFlowToolMakesAVersion(t *testing.T) {
	h, ws := flowHub(t)
	orig, _ := flow.Load(ws, "notes")
	edited := map[string]any{"name": "Release notes", "nodes": []any{
		map[string]any{"id": "start", "type": "start"},
		map[string]any{"id": "echo", "type": "tool", "tool": "echo-arg", "args": map[string]any{"v": 7}},
		map[string]any{"id": "sum", "type": "ai", "prompt": "Summarise {{echo.buffer_output}}", "outputSchema": map[string]any{"type": "object", "properties": map[string]any{"t": map[string]any{"type": []any{"string", "null"}}}}},
		map[string]any{"id": "md", "type": "output", "format": "md", "actions": []any{map[string]any{"type": "show"}}},
	}, "edges": []any{map[string]any{"from": "start", "to": "echo"}, map[string]any{"from": "echo", "to": "sum"}, map[string]any{"from": "sum", "to": "md"}}}
	out, err := h.RunBuiltin(h.ctx, SaveFlowTool, map[string]any{"flow": edited, "base_flow_id": "notes", "note": "adds a summary"})
	if err != nil || !strings.Contains(out, "version 2 of notes") || !strings.Contains(out, "It checks out") {
		t.Fatalf("%s %v", out, err)
	}
	now, _ := flow.Load(ws, "notes") // it only becomes "version 1" of its family
	if len(now.Nodes) != len(orig.Nodes) || now.Node("echo").Args["v"] != orig.Node("echo").Args["v"] || now.Node("sum") != nil {
		t.Fatal("the original flow's steps must stay as they were")
	}
	v, err := flow.Load(ws, "release-notes-v2")
	if err != nil || v.Node("echo").Args["v"] != "7" || !strings.HasPrefix(v.Designed, savedByAgent) || v.Context.Text != "We ship weekly." {
		t.Fatalf("%v %+v", err, v)
	}
	// problems are reported; the version is updated in place, not another one
	edited["nodes"].([]any)[1].(map[string]any)["tool"] = "no-such-tool"
	out, err = h.RunBuiltin(h.ctx, SaveFlowTool, map[string]any{"flow": edited, "update_version_id": "release-notes-v2"})
	if err != nil || !strings.Contains(out, "PROBLEMS") || !strings.Contains(out, "update_version_id=release-notes-v2") {
		t.Fatalf("%s %v", out, err)
	}
	if list, _ := h.Flows(); len(list) != 2 {
		t.Fatalf("%d flows", len(list))
	}
	// not a flow at all: refused with how to fix it
	if _, err := h.RunBuiltin(h.ctx, SaveFlowTool, map[string]any{"flow": "not json"}); err == nil || !strings.Contains(err.Error(), "Flow Designer format") {
		t.Fatal(err)
	}
	// agents are offered it
	found := false
	for _, b := range h.Builtins() {
		found = found || (b.Name == SaveFlowTool && strings.Contains(b.Description, "FLOW FORMAT"))
	}
	if !found {
		t.Fatal("save_flow must be offered to agents")
	}
}
