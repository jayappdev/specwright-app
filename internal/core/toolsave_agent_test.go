package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
)

func TestSaveToolProposesWithoutSaving(t *testing.T) {
	h, _ := flowHub(t)
	listed := false
	for _, b := range h.Builtins() {
		listed = listed || b.Name == SaveToolTool
	}
	if !listed {
		t.Fatal("agents are not offered save_tool")
	}
	if _, err := h.SaveExtension(extension.Extension{Name: "greet", Desc: "Greets", Lang: "Bash", Scope: "project", Command: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (string, error) { return h.RunBuiltin(h.ctx, SaveToolTool, args) }
	out, err := call(map[string]any{"tool": map[string]any{"name": "greet", "desc": "Greets", "command": "echo {{who}}"}, "why": "greets someone"})
	if err != nil || !strings.Contains(out, "Proposed revision 2 of the tool greet") || !strings.Contains(out, "who") {
		t.Fatalf("%q %v", out, err)
	}
	if e, _ := h.exts.Get("greet"); e.Command != "echo hi" {
		t.Fatalf("save_tool changed the saved tool: %q", e.Command)
	}
	if out, err := call(map[string]any{"tool": `{"name": "new-one", "script": "echo x"}`}); err != nil || !strings.Contains(out, "new tool new-one") {
		t.Fatalf("%q %v", out, err)
	}
	for _, bad := range []map[string]any{{}, {"tool": map[string]any{"name": "Bad Name", "command": "x"}}, {"tool": map[string]any{"name": "ok"}}} {
		if _, err := call(bad); err == nil {
			t.Errorf("%v should be refused", bad)
		}
	}
}
