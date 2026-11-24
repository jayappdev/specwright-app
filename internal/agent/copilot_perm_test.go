package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCopilotPermissionPlugin(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp-1.json")
	os.WriteFile(cfg, []byte(`{"mcpServers":{"agentbridge":{"command":"/app/SpecWright","args":["agentbridge"],
		"env":{"CODEGEN_BRIDGE_URL":"http://127.0.0.1:9","CODEGEN_BRIDGE_TOKEN":"tok"}}}}`), 0o600)

	plugin, done := permissionPlugin(cfg)
	if plugin == "" {
		t.Fatal("no plugin written")
	}
	var hooks struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Exec    string            `json:"exec"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Timeout int               `json:"timeoutSec"`
		} `json:"hooks"`
	}
	b, _ := os.ReadFile(filepath.Join(plugin, "hooks.json"))
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatal(err)
	}
	h := hooks.Hooks["permissionRequest"]
	if hooks.Version != 1 || len(h) != 1 || h[0].Exec != "/app/SpecWright" || !slices.Equal(h[0].Args, []string{"copilot-hook", "permission"}) ||
		h[0].Env["CODEGEN_BRIDGE_TOKEN"] != "tok" || !strings.HasPrefix(h[0].Env["CODEGEN_AGENT_ID"], "copilot-") || h[0].Timeout < 300 {
		t.Fatalf("hook: %+v", hooks)
	}
	if _, err := os.Stat(filepath.Join(plugin, "plugin.json")); err != nil {
		t.Fatal("no manifest:", err)
	}

	c := NewCopilot()
	args := c.argsWith(Request{Prompt: "x", MCPConfig: cfg}, plugin)
	if slices.Contains(args, "--allow-all-tools") || !slices.Contains(args, "--plugin-dir") || !slices.Contains(args, "write") {
		t.Fatalf("args with a plugin: %q", args)
	}
	if !slices.Contains(c.Args(Request{Prompt: "x"}), "--allow-all-tools") {
		t.Fatal("without a plugin the run keeps --allow-all-tools")
	}
	done()
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Fatal("plugin not removed")
	}
	if p, _ := permissionPlugin(""); p != "" {
		t.Fatal("no MCP config: no plugin")
	}
}

func TestCopilotTokenEnvAndWeb(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_classic")
	t.Setenv("GITHUB_TOKEN", "github_pat_fine")
	env, dropped := baseEnv()
	if !slices.Equal(dropped, []string{"GH_TOKEN"}) || slices.Contains(env, "GH_TOKEN=ghp_classic") || !slices.Contains(env, "GITHUB_TOKEN=github_pat_fine") {
		t.Fatalf("dropped %v", dropped)
	}

	c := NewCopilot()
	c.help = "--secret-env-vars --allow-url" // as `copilot --help` lists them
	c.helpOnce.Do(func() {})
	ctx := context.Background()
	if a := c.extraArgs(ctx, "copilot"); !slices.Equal(a, []string{"--secret-env-vars=COPILOT_GITHUB_TOKEN,GH_TOKEN,GITHUB_TOKEN"}) {
		t.Fatalf("default: %q", a)
	}
	c.SetWeb(WebAccess{Mode: "sites", Sites: []string{"docs.github.com", " "}})
	if a := c.extraArgs(ctx, "copilot"); !slices.Contains(a, "--allow-url") || !slices.Contains(a, "docs.github.com") || len(a) != 3 {
		t.Fatalf("sites: %q", a)
	}
	c.SetWeb(WebAccess{Mode: "all"})
	if a := c.extraArgs(ctx, "copilot"); !slices.Contains(a, "--allow-all-urls") {
		t.Fatalf("all: %q", a)
	}
}

// With project extensions and the app's bridge, Copilot's reload (always
// denied in -p) is hidden and the agent is told to run a changed extension
// through the bridge.
func TestCopilotReloadNote(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir()) // not the developer's own extensions
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github", "extensions", "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, ".github", "extensions", "greet", "extension.mjs"), []byte("x"), 0o644)
	c := NewCopilot()
	a := c.argsWith(Request{Prompt: "fix it", Dir: dir}, "/plugin")
	if !strings.HasPrefix(a[1], "fix it") || !strings.Contains(a[1], "extension_managed") || !slices.Equal(a[len(a)-2:], []string{"--excluded-tools", "extensions_reload"}) {
		t.Fatalf("%q", a)
	}
	if a := c.argsWith(Request{Prompt: "fix it", Dir: dir}, ""); a[1] != "fix it" || slices.Contains(a, "--excluded-tools") {
		t.Fatalf("no bridge: unchanged: %q", a)
	}
}

// your own extensions (~/.copilot/extensions) load too, and the agent is told
// which copy to change
func TestCopilotUserExtensions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	mk := func(dir string) {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "extension.mjs"), []byte("x"), 0o644)
	}
	mk(filepath.Join(home, "extensions", "html-formatter"))
	mk(filepath.Join(home, "extensions", "mine-only"))
	proj := t.TempDir()
	mk(filepath.Join(proj, ".github", "extensions", "html-formatter"))
	c := NewCopilot()
	a := c.argsWith(Request{Prompt: "improve it", Dir: proj}, "/plugin")
	note := a[1]
	if !slices.Contains(a, "--experimental") || !strings.Contains(note, "html-formatter also exist in the user's own Copilot folder") ||
		!strings.Contains(note, "change the project's copy") || !strings.Contains(note, "mine-only exist only in the user's own Copilot folder") {
		t.Fatalf("%q", note)
	}
	if env := c.env(Request{Dir: t.TempDir()}); !slices.Contains(env, "GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS=true") {
		t.Fatal("your own extensions load in -p runs too")
	}
}

// interactive (the Playground): edits go to the hook, outside paths are not refused
func TestCopilotInteractive(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	c := NewCopilot()
	a := strings.Join(c.argsWith(Request{Prompt: "x", Interactive: true}, "/plugin"), " ")
	if !strings.Contains(a, "--allow-all-paths") || strings.Contains(a, "--allow-tool write") || !strings.Contains(a, "--allow-tool agentbridge") {
		t.Fatal(a)
	}
	b := strings.Join(c.argsWith(Request{Prompt: "x"}, "/plugin"), " ")
	if strings.Contains(b, "--allow-all-paths") || !strings.Contains(b, "--allow-tool write") {
		t.Fatal("not interactive: as before:", b)
	}
	if d := strings.Join(c.argsWith(Request{Prompt: "x", Interactive: true}, ""), " "); strings.Contains(d, "--allow-all-paths") {
		t.Fatal("no hook to ask: no wider paths", d)
	}
}
