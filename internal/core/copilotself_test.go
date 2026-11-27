package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
)

// SpecWright's own Copilot extension: written into the user's Copilot
// folder, kept out of SpecWright's own lists, and, run as Copilot would
// (the SDK stubbed), it registers SpecWright's tools and calls the app.
func TestCopilotSelfExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	dir, err := extension.EnsureCopilotSelfExt()
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "extension.mjs")
	st1, _ := os.Stat(entry)
	if _, err := extension.EnsureCopilotSelfExt(); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(entry); !st2.ModTime().Equal(st1.ModTime()) {
		t.Fatal("an up-to-date extension was written again")
	}
	for _, x := range extension.ListCopilotExts("") {
		if x.Name == extension.SelfCopilotExt {
			t.Fatal("SpecWright's own extension is listed with the user's")
		}
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	shim, err := extension.CopilotShimPath()
	if err != nil {
		t.Fatal(err)
	}
	node := func(env []string, args ...string) string {
		cmd := exec.CommandContext(context.Background(), "node", append([]string{shim}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v %v: %s %s", args, err, out.String(), errb.String())
		}
		return out.String()
	}
	// outside SpecWright: no tools
	if out := node([]string{"CODEGEN_BRIDGE_URL=", "CODEGEN_BRIDGE_TOKEN="}, "list", entry); strings.Contains(out, "specwright_") {
		t.Fatalf("tools outside a SpecWright run: %s", out)
	}
	h, _ := flowHub(t)
	// an agent saving a tool is asked first (agentactions.go): yes here
	h.SetPermissionAsker(func(context.Context, extension.PermissionRequest) extension.PermissionAnswer {
		return extension.PermissionAnswer{Allow: true}
	})
	env := h.Bridge.Env()
	out := node(env, "list", entry)
	for _, want := range []string{"specwright_save_flow", "specwright_save_tool", "specwright_run_flow", "specwright_tool", "read_file", "echo-arg"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the extension does not register %s:\n%s", want, out)
		}
	}
	if out := node(env, "call", entry, "specwright_save_tool", `{"tool":{"name":"greet","command":"echo hi"}}`); !strings.Contains(out, "Proposed a new tool greet") {
		t.Fatalf("specwright_save_tool: %s", out)
	}
	if out := node(env, "call", entry, "specwright_tool", `{"name":"echo-arg","arguments":{"v":"from copilot"}}`); !strings.Contains(out, "from copilot") {
		t.Fatalf("a project tool through specwright_tool: %s", out)
	}
	if out := node(env, "call", entry, "specwright_tool", `{"name":"list_directory","arguments":{"path":"."}}`); strings.Contains(out, "Error") {
		t.Fatalf("a built-in through specwright_tool: %s", out)
	}
}
