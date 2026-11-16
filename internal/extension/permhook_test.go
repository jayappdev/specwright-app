package extension

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHookRequestShapes(t *testing.T) {
	tool, in := hookRequest([]byte(`{"toolName":"bash","toolArgs":"{\"command\":\"npm install\"}","kind":"shell"}`))
	if tool != "bash" || in["command"] != "npm install" || in["permission"] != "shell" {
		t.Fatalf("camelCase: %s %v", tool, in)
	}
	tool, in = hookRequest([]byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if tool != "Bash" || in["command"] != "ls" {
		t.Fatalf("snake_case: %s %v", tool, in)
	}
	if tool, _ = hookRequest([]byte(`not json`)); tool != "tool" {
		t.Fatalf("bad input: %s", tool)
	}
}

func TestPermissionHookAsksTheApp(t *testing.T) {
	var got PermissionRequest
	allow := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permission" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(PermissionAnswer{Allow: allow, Message: "not now"})
	}))
	defer srv.Close()
	t.Setenv("CODEGEN_BRIDGE_URL", srv.URL)
	t.Setenv("CODEGEN_BRIDGE_TOKEN", "tok")
	t.Setenv("CODEGEN_AGENT_ID", "copilot-run1")

	var out bytes.Buffer
	if err := PermissionHook(strings.NewReader(`{"toolName":"bash","toolArgs":{"command":"go build ./..."}}`), &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != `{"behavior":"allow"}` || got.Agent != "copilot-run1" || got.Tool != "bash" || got.Input["command"] != "go build ./..." {
		t.Fatalf("allow: %s, sent %+v", out.String(), got)
	}
	allow = false
	out.Reset()
	PermissionHook(strings.NewReader(`{"toolName":"bash"}`), &out)
	if !strings.Contains(out.String(), `"behavior":"deny"`) || !strings.Contains(out.String(), "not now") {
		t.Fatalf("deny: %s", out.String())
	}

	// the app cannot be reached: no output, so Copilot's default applies
	t.Setenv("CODEGEN_BRIDGE_URL", "http://127.0.0.1:1")
	out.Reset()
	if err := PermissionHook(strings.NewReader(`{}`), &out); err != nil || out.Len() != 0 {
		t.Fatalf("unreachable: %v %q", err, out.String())
	}
}

// Edits inside the project or an added folder are allowed without asking;
// edits elsewhere and other tools still go to the app.
func TestPermissionHookAllowsEditsInsideProject(t *testing.T) {
	proj, extra, outside := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CODEGEN_EDIT_DIRS", extra)
	t.Setenv("CODEGEN_BRIDGE_URL", "http://127.0.0.1:1") // the app is not reachable: anything asked gets no answer
	hook := func(payload string) string {
		var out bytes.Buffer
		PermissionHook(strings.NewReader(payload), &out)
		return strings.TrimSpace(out.String())
	}
	in := func(tool, path string) string {
		b, _ := json.Marshal(map[string]any{"cwd": proj, "toolName": tool, "toolInput": map[string]any{"file_path": path}})
		return string(b)
	}
	if got := hook(in("edit", proj+"/src/a.go")); got != `{"behavior":"allow"}` {
		t.Fatalf("edit inside the project: %q", got)
	}
	if got := hook(in("create", extra+"/notes.md")); got != `{"behavior":"allow"}` {
		t.Fatalf("create in an added folder: %q", got)
	}
	for _, p := range []string{outside + "/x.txt", proj + "/../escape.txt", "relative.txt"} {
		if got := hook(in("edit", p)); got != "" {
			t.Fatalf("edit of %s was not asked: %q", p, got)
		}
	}
	if got := hook(in("bash", proj+"/a.sh")); got != "" {
		t.Fatalf("a shell command was not asked: %q", got)
	}
}
