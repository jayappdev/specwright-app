package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
)

// An agent's run_command (and flow and tool changes) wait for a person's
// yes; a "no" leaves the command unrun. A person trying the tool is not
// asked.
func TestAgentCommandsNeedAPersonsYes(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	marker := filepath.Join(ws, "ran.txt")
	var asked []string
	answer := false
	h.SetPermissionAsker(func(_ context.Context, r extension.PermissionRequest) extension.PermissionAnswer {
		asked = append(asked, r.Tool+": "+r.Input["description"].(string))
		return extension.PermissionAnswer{Allow: answer, Message: "Not now."}
	})
	args := map[string]any{"command": "touch ran.txt"}
	if _, err := h.RunBuiltin(context.Background(), "run_command", args); err == nil || !strings.Contains(err.Error(), "Not now.") {
		t.Fatalf("a refused command ran: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran without a yes")
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "run_command: The AI wants to run a shell command") {
		t.Fatalf("asked %v", asked)
	}
	answer = true
	if _, err := h.RunBuiltin(context.Background(), "run_command", args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a command the person allowed did not run")
	}
	// reading needs no yes; a person's own try is not asked
	asked = nil
	if _, err := h.RunBuiltin(context.Background(), "list_directory", map[string]any{"path": "."}); err != nil || len(asked) != 0 {
		t.Fatalf("reading was asked (%v) or failed: %v", asked, err)
	}
	if _, err := h.RunBuiltin(byPerson(context.Background()), "run_command", map[string]any{"command": "true"}); err != nil || len(asked) != 0 {
		t.Fatalf("a person's own try was asked (%v): %v", asked, err)
	}
}
