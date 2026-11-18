package extension

import (
	"strings"
	"testing"
)

// Copilot asks about extensions_reload as a custom tool with no arguments:
// the app's question must say what it does.
func TestHookDescribesExtensionReload(t *testing.T) {
	tool, input := hookRequest([]byte(`{"hookName":"permissionRequest","toolName":"extensions_reload","toolInput":{}}`))
	describeCopilotTool(tool, input)
	if tool != "extensions_reload" || !strings.Contains(input["description"].(string), "Reload the Copilot extensions") {
		t.Fatalf("%s %v", tool, input)
	}
	tool, input = hookRequest([]byte(`{"toolName":"extensions_manage","toolInput":{"operation":"scaffold","name":"lint-ext"}}`))
	describeCopilotTool(tool, input)
	if d := input["description"].(string); !strings.Contains(d, "scaffold lint-ext") {
		t.Fatal(d)
	}
	if editInside("extensions_reload", map[string]any{}, []string{"/tmp"}) {
		t.Fatal("a reload is not a file edit: it is asked in the app")
	}
}

// SpecWright's own tools are allowed without asking (as for Claude); other
// tools still go to the app.
func TestHookAllowsOwnTools(t *testing.T) {
	var out strings.Builder
	t.Setenv("CODEGEN_BRIDGE_URL", "http://127.0.0.1:9") // nothing there: anything asked gets no answer
	if err := PermissionHook(strings.NewReader(`{"toolName":"agentbridge/save_flow","toolInput":{"flow":{}}}`), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"allow"`) {
		t.Fatalf("own tool: %q", out.String())
	}
	out.Reset()
	_ = PermissionHook(strings.NewReader(`{"toolName":"bash","toolInput":{"command":"rm -rf x"}}`), &out)
	if strings.Contains(out.String(), "allow") {
		t.Fatalf("other tools are still asked: %q", out.String())
	}
}
