package core

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// An agent's fetch_webpage to this computer or a private network waits for
// a person's yes (item 4: SSRF); a person's own try is not asked.
func TestAgentLocalFetchNeedsAPersonsYes(t *testing.T) {
	usePolicy(t, "")
	h, _ := newHub(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("local admin page"))
	}))
	defer srv.Close()
	var asked []string
	answer := false
	h.SetPermissionAsker(func(_ context.Context, r extension.PermissionRequest) extension.PermissionAnswer {
		asked = append(asked, r.Tool+": "+r.Input["description"].(string))
		return extension.PermissionAnswer{Allow: answer, Message: "Not now."}
	})
	args := map[string]any{"url": srv.URL}
	if out, err := h.RunBuiltin(context.Background(), "fetch_webpage", args); err == nil || !strings.Contains(err.Error(), "Not now.") {
		t.Fatalf("a refused local fetch ran: %q %v", out, err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "an address on this computer or a private network") {
		t.Fatalf("asked %v", asked)
	}
	answer = true
	if out, err := h.RunBuiltin(context.Background(), "fetch_webpage", args); err != nil || !strings.Contains(out, "local admin page") {
		t.Fatalf("an allowed local fetch: %q %v", out, err)
	}
	asked = nil
	if _, err := h.RunBuiltin(byPerson(context.Background()), "fetch_webpage", args); err != nil || len(asked) != 0 {
		t.Fatalf("a person's own try was asked (%v): %v", asked, err)
	}
	// cloud metadata is refused even with a yes
	if _, err := h.RunBuiltin(context.Background(), "fetch_webpage", map[string]any{"url": "http://169.254.169.254/latest/meta-data/"}); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata address: %v", err)
	}
}

// Commands do not inherit the app's secrets (item 5); the policy can keep one.
func TestCommandsDoNotInheritSecrets(t *testing.T) {
	usePolicy(t, "")
	h, _ := newHub(t)
	t.Setenv("ACME_API_KEY", "leak-1")
	t.Setenv("NPM_TOKEN", "leak-2")
	t.Setenv("ACME_HOME", "/opt/acme")
	cmd := map[string]any{"command": `echo "[$ACME_API_KEY][$NPM_TOKEN][$ACME_HOME]"`}
	out, err := h.RunBuiltin(byPerson(context.Background()), "run_command", cmd)
	if err != nil || !strings.Contains(out, "[][][/opt/acme]") {
		t.Fatalf("%q %v", out, err)
	}
	usePolicy(t, `{"keepEnvVars":["NPM_TOKEN"]}`)
	if out, _ = h.RunBuiltin(byPerson(context.Background()), "run_command", cmd); !strings.Contains(out, "[][leak-2][/opt/acme]") {
		t.Fatalf("policy keep: %q", out)
	}
}
