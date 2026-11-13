package extension

import (
	"bytes"
	"codegen/internal/flow"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type prov struct {
	root string
	exts []Extension
	seen []string
}

func (p *prov) Callable() []Extension   { return p.exts }
func (p *prov) Builtins() []BuiltinInfo { return nil }
func (p *prov) RunBuiltin(context.Context, string, map[string]any) (string, error) {
	return "", nil
}
func (p *prov) AskPermission(context.Context, PermissionRequest) PermissionAnswer {
	return PermissionAnswer{Message: "test"}
}

func (p *prov) ResolveSecrets(context.Context, Extension) (map[string]string, error) {
	return map[string]string{"GREETING": "hello"}, nil
}
func (p *prov) Root() string                                    { return p.root }
func (p *prov) Observe(n string, _ map[string]any, _ RunResult) { p.seen = append(p.seen, n) }
func (p *prov) Prepare(context.Context, Extension) error        { return nil }
func (p *prov) Env() []string                                   { return nil }

func TestSaveApproveAndExec(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	e, err := s.Save(Extension{Name: "greet", Desc: "Say hi", Lang: "Bash",
		Params:  []Param{{Name: "who", Type: "string", Required: true}},
		Secrets: []Secret{{Name: "GREETING"}},
		Command: "bash .codegen/tools/greet.sh {{who}}", Script: "#!/usr/bin/env bash\necho \"$GREETING, $1\"\n"})
	if err != nil {
		t.Fatal(err)
	}
	if e.ScriptPath != ".codegen/tools/project/greet.sh" || !strings.Contains(e.Command, e.ScriptPath) {
		t.Fatalf("script path %q command %q", e.ScriptPath, e.Command)
	}
	if err := e.Runnable(); err != nil {
		t.Fatal(err)
	}
	r := Exec(context.Background(), root, e, map[string]any{"who": "world; rm -rf /"}, map[string]string{"GREETING": "hello"}, nil, 0)
	if !r.OK || r.Output != "hello, world; rm -rf /" {
		t.Fatalf("exec %+v", r)
	}
	if r := Exec(context.Background(), root, e, nil, nil, nil, 0); r.Error == "" {
		t.Fatal("missing required arg must fail")
	}
	e.Script += "echo changed\n"
	if e.Runnable() == nil {
		t.Fatal("changed script must need re-approval")
	}
}

func TestNpmScriptsNeedApproval(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"ext:lint":"eslint .","build":"x"}}`), 0o644)
	s := NewStore(root)
	l, _ := s.List()
	if len(l) != 1 || l[0].Name != "lint" || l[0].Status != "needs approval" || l[0].Runnable() == nil {
		t.Fatalf("%+v", l)
	}
	a, err := s.Approve("lint")
	if err != nil || a.Runnable() != nil {
		t.Fatalf("approve: %v %v", err, a.Runnable())
	}
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"ext:lint":"eslint . --fix"}}`), 0o644)
	l, _ = s.List()
	if l[0].Runnable() == nil || l[0].Status != "needs approval" {
		t.Fatalf("edited npm script must lose approval: %+v", l[0])
	}
}

func TestBridgeAndMCP(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	e, _ := s.Save(Extension{Name: "echo-arg", Desc: "Echo", Params: []Param{{Name: "v", Required: true},
		{Name: "opts", Type: "json", Desc: "options", Schema: &flow.Schema{Type: "object", Properties: flow.Props{{Name: "deep", Schema: &flow.Schema{Type: "boolean"}}}}}},
		Command: "echo {{v}}", Output: &flow.Schema{Type: "object", Description: "OUTPUT-SHAPE-NOT-FOR-AI"}})
	p := &prov{root: root, exts: []Extension{e}}
	br, err := StartBridge(p)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	for _, kv := range br.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"extension_managed","arguments":{"name":"echo-arg","arguments":{"v":"pong"}}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"extension_managed","arguments":{"name":"nope"}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := ServeMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 responses, got %d: %s", len(lines), out.String())
	}
	if !strings.Contains(lines[1], "echo-arg") || !strings.Contains(lines[1], ToolName) {
		t.Fatalf("tools/list: %s", lines[1])
	}
	// the agent gets a JSON parameter's shape, never the output's
	if !strings.Contains(lines[1], "Shape: {deep: boolean}") || strings.Contains(lines[1], "OUTPUT-SHAPE-NOT-FOR-AI") {
		t.Fatalf("tools/list shapes: %s", lines[1])
	}
	var call struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(lines[2]), &call)
	if call.Result.IsError || call.Result.Content[0].Text != "pong" {
		t.Fatalf("call: %s", lines[2])
	}
	if !strings.Contains(lines[3], `"isError":true`) {
		t.Fatalf("unknown tool must error: %s", lines[3])
	}
	if len(p.seen) != 1 {
		t.Fatalf("observe %v", p.seen)
	}
}

func TestDecodeStringifiedArgs(t *testing.T) {
	if m := decodeArgs([]byte(`"{\"v\":\"x\"}"`)); m["v"] != "x" {
		t.Fatalf("%v", m)
	}
	if m := decodeArgs([]byte(`{"v":"y"}`)); m["v"] != "y" {
		t.Fatalf("%v", m)
	}
}

func TestQuoteSplitRoundTrip(t *testing.T) {
	cases := []string{"", "plain", "a b", "it's", `say "hi"`, `both ' and "`, `C:\Program Files\x.exe`, `back\slash "q"`, "tab\there", "$HOME & #1 (x) ünï", `'`, `"`, `\`, `\"`}
	if got := SplitWords(JoinWords(cases)); len(got) != len(cases) {
		t.Fatalf("got %d words %q", len(got), got)
	} else {
		for i := range cases {
			if got[i] != cases[i] {
				t.Errorf("word %d: got %q want %q", i, got[i], cases[i])
			}
		}
	}
	if w := SplitWords(`node C:\Tools\x.js {{p}}`); len(w) != 3 || w[1] != `C:\Tools\x.js` {
		t.Fatalf("bare Windows path must stay literal: %q", w)
	}
}

func TestRepointScript(t *testing.T) {
	got := RepointScript(`node ".codegen/tools/my tool.js" --flag {{p}}`, "other", "/tmp/a b/it's.js")
	if w := SplitWords(got); len(w) != 4 || w[1] != "/tmp/a b/it's.js" || w[3] != "{{p}}" {
		t.Fatalf("%s → %q", got, w)
	}
	if got := RepointScript("echo {{v}}", "x", "/y"); got != "echo {{v}}" {
		t.Fatalf("no script word must leave the command alone: %s", got)
	}
}
