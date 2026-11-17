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
