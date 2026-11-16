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
