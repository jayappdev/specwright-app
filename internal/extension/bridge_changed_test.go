package extension

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// changedProv: a provider whose Copilot extension tool changed after it was
// approved (ChangedTools), answering permission questions with allow.
type changedProv struct {
	prov
	changedTools []Extension
	allow        bool
	asked        []PermissionRequest
}

func (p *changedProv) ChangedTools() []Extension { return p.changedTools }
func (p *changedProv) AskPermission(_ context.Context, r PermissionRequest) PermissionAnswer {
	p.asked = append(p.asked, r)
	if p.allow {
		return PermissionAnswer{Allow: true}
	}
	return PermissionAnswer{Message: "not now"}
}

// An agent fixing an extension can run the new version in the same session:
// the changed tool stays listed, and each run asks the user first.
func TestBridgeRunsChangedToolAfterAsking(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("needs Node.js")
	}
	root := t.TempDir()
	// the extension as the agent left it: fixed, not approved again
	dir := filepath.Join(root, ".github", "extensions", "greet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "extension.mjs"), []byte(`import { joinSession } from "@github/copilot-sdk/extension";
await joinSession({ tools: [{ name: "greet_hello", description: "Greets", handler: async () => "hello fixed" }] });
`), 0o644)
	e := Extension{Name: "greet_hello", Desc: "Greets", Kind: KindCopilot, Scope: "project", Tool: "greet_hello", Entry: "extension.mjs",
		Dir: ".github/extensions/greet", Tree: treeHash(dir), Approved: true, Hash: "approved-before-the-change", Params: []Param{}}
	e.Command = copilotCommand(e)
	if e.Runnable() == nil {
		t.Fatal("setup: the tool must look changed")
	}
	p := &changedProv{prov: prov{root: root}, changedTools: []Extension{e}}
	br, err := StartBridge(p)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	for _, kv := range br.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	call := func() string {
		in := strings.Join([]string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"extension_managed","arguments":{"name":"greet_hello","arguments":{}}}}`,
		}, "\n")
		var out bytes.Buffer
		if err := ServeMCP(strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	out := call()
	if !strings.Contains(out, "greet_hello") || !strings.Contains(out, "the user is asked before the new version runs") {
		t.Fatalf("the changed tool must stay listed: %s", out)
	}
	if !strings.Contains(out, "not now") || strings.Contains(out, "hello fixed") || len(p.asked) != 1 {
		t.Fatalf("denied: must not run: %s %+v", out, p.asked)
	}
	if a := p.asked[0]; a.Tool != "greet_hello" || a.Agent == "" || a.Agent == "agent" || !strings.Contains(a.Input["description"].(string), "changed version") {
		t.Fatalf("question: %+v", a)
	}
	p.allow = true
	if out := call(); !strings.Contains(out, "hello fixed") {
		t.Fatalf("allowed: must run the new version: %s", out)
	}
}
