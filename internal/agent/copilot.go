package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Copilot drives the GitHub Copilot CLI in programmatic mode
// (`copilot -p … --allow-all-tools`). CLIs that support it (1.0+) run with
// `--output-format json` (JSONL events) and `--usage-output-file` (tokens,
// premium requests, AI units); older ones print text, which a fallback
// parser reads.
type Copilot struct {
	Bin       string
	DenyTools []string

	helpOnce sync.Once
	help     string // `copilot --help`, to know which flags this CLI has

	mu  sync.Mutex
	web WebAccess
}

// WebAccess says which web pages Copilot may open in a run. In -p mode the
// CLI cannot ask, so a URL that is not allowed is denied.
type WebAccess struct {
	Mode  string   `json:"mode"`  // "none" (default), "sites", "all"
	Sites []string `json:"sites"` // with "sites": domains or https URLs (--allow-url)
}

// SetWeb sets the web access for later runs.
func (c *Copilot) SetWeb(w WebAccess) {
	c.mu.Lock()
	c.web = w
	c.mu.Unlock()
}

// Web returns the web access setting.
func (c *Copilot) Web() WebAccess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.web
}

func (c *Copilot) hasFlag(ctx context.Context, bin, flag string) bool {
	c.helpOnce.Do(func() {
		out, _ := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
		c.help = string(out)
	})
	return strings.Contains(c.help, flag)
}

// structured reports whether this CLI supports --output-format json.
func (c *Copilot) structured(ctx context.Context, bin string) bool {
	return c.hasFlag(ctx, bin, "--output-format") && c.hasFlag(ctx, bin, "--usage-output-file")
}

// tokenVars are the variables the Copilot CLI reads a GitHub token from, in
// its order of precedence.
var tokenVars = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

// baseEnv is this process's environment without classic GitHub tokens
// (ghp_…) in the token variables: the Copilot CLI rejects them, and one
// inherited from the shell would take precedence over the `copilot` login.
// It also reports which variables were dropped.
func baseEnv() (env, dropped []string) {
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if slices.Contains(tokenVars, k) && strings.HasPrefix(strings.TrimSpace(v), "ghp_") {
			dropped = append(dropped, k)
			continue
		}
		env = append(env, kv)
	}
	return env, dropped
}

// extraArgs are the flags that depend on what this CLI supports and on the
// web setting: GitHub tokens hidden from shell commands and MCP servers the
// agent starts, and the URLs it may open.
func (c *Copilot) extraArgs(ctx context.Context, bin string) []string {
	var args []string
	if c.hasFlag(ctx, bin, "--secret-env-vars") {
		args = append(args, "--secret-env-vars="+strings.Join(tokenVars, ","))
	}
	switch w := c.Web(); w.Mode {
	case "all":
		args = append(args, "--allow-all-urls")
	case "sites":
		for _, s := range w.Sites {
			if s = strings.TrimSpace(s); s != "" {
				args = append(args, "--allow-url", s)
			}
		}
	}
	return args
}

// NewCopilot returns the Copilot CLI driver.
func NewCopilot() *Copilot {
	return &Copilot{DenyTools: []string{"shell(git push)", "shell(rm -rf)"}}
}

// Name implements Driver.
func (c *Copilot) Name() string { return "copilot" }

func (c *Copilot) bin() (string, error) {
	if c.Bin != "" {
		return c.Bin, nil
	}
	return findBinary("copilot")
}

// Status implements Driver.
func (c *Copilot) Status(ctx context.Context) Status {
	s := Status{Name: "copilot", Label: "GitHub Copilot CLI", Billing: "premium"}
	p, err := c.bin()
	if err != nil {
		s.Problem = "Install the Copilot CLI (npm i -g @github/copilot) and run `copilot` once to sign in."
		return s
	}
	s.Path, s.Available = p, true
	s.Version = version(ctx, p)
	return s
}

// Args builds the CLI arguments for req.
func (c *Copilot) Args(req Request) []string { return c.argsWith(req, "") }

// argsWith builds the arguments; with a permission plugin (pluginDir) the
// run asks the user for tool calls instead of allowing every tool: file
// edits and SpecWright's own tools are allowed, read-only commands need no
// approval, and anything else goes to the plugin's permissionRequest hook,
// which asks in the app.
func (c *Copilot) argsWith(req Request, pluginDir string) []string {
	args := []string{"-p", req.Prompt, "--no-color"}
	switch {
	case pluginDir != "" && req.Interactive:
		// a person answers in the app: Copilot does not refuse paths outside
		// the project on its own, and every edit goes to the hook, which
		// allows those inside the project (and the run's folders) and asks
		// about the rest. Reading files needs no permission in Copilot, so
		// reads outside the project are not asked.
		args = append(args, "--plugin-dir", pluginDir, "--allow-tool", "agentbridge", "--allow-all-paths")
	case pluginDir != "":
		args = append(args, "--plugin-dir", pluginDir, "--allow-tool", "write", "--allow-tool", "agentbridge")
	default:
		args = append(args, "--allow-all-tools")
	}
	for _, d := range c.DenyTools {
		args = append(args, "--deny-tool", d)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	for _, d := range req.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if req.MCPConfig != "" {
		args = append(args, "--additional-mcp-config", "@"+req.MCPConfig)
	}
	project, user := copilotExtNames(req.Dir)
	if len(project)+len(user) == 0 && hasSelfExt() {
		args = append(args, "--experimental") // SpecWright's own extension: its tools without MCP
	}
	if len(project)+len(user) > 0 {
		args = append(args, "--experimental") // Copilot extensions are experimental
		if pluginDir != "" {
			// Copilot cannot reload extensions in a -p run (it denies the
			// reload without asking anyone), so it is hidden; the agent runs
			// a changed extension through SpecWright instead, which asks the
			// user. Last: the flag takes a list.
			args[1] += reloadNote(project, user)
			args = append(args, "--excluded-tools", "extensions_reload")
		}
	}
	return args
}

// reloadNote tells the agent how to run an extension it changed: the
// project's extensions (.github/extensions) through SpecWright; your own
// (~/.copilot/extensions) that the project has a copy of: change the
// project's copy, which is the one SpecWright runs; your own that only you
// have: as loaded when the run started.
func reloadNote(project, user []string) string {
	in := map[string]bool{}
	for _, n := range project {
		in[n] = true
	}
	var both, only []string
	for _, n := range user {
		if in[n] {
			both = append(both, n)
		} else {
			only = append(only, n)
		}
	}
	s := "\n\n(Note from SpecWright: Copilot extensions cannot be reloaded in this run."
	if len(project) > 0 {
		s += " The tools of this project's extensions (" + strings.Join(project, ", ") + ", in .github/extensions) are also offered by the agentbridge MCP server (extension_managed), " +
			"which runs each extension's current files: after you change one, call its tool through extension_managed to run the new version in this session. The user is asked before a changed version runs."
	}
	if len(both) > 0 {
		s += " " + strings.Join(both, ", ") + " also exist in the user's own Copilot folder (~/.copilot/extensions): change the project's copy in .github/extensions, which is the one SpecWright runs; change the user's own copy only if the user asks for it."
	}
	if len(only) > 0 {
		s += " " + strings.Join(only, ", ") + " exist only in the user's own Copilot folder (~/.copilot/extensions): they run as they were when this run started, so a change to them takes effect in the next run (or the user loads them into the project under Tools to run a changed version here)."
	}
	return s + ")"
}

// copilotHome is the Copilot CLI's folder ($COPILOT_HOME or ~/.copilot).
func copilotHome() string {
	if h := os.Getenv("COPILOT_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot")
}

// extNames lists the Copilot extensions in dir (folders with an
// extension.mjs, .cjs or .js).
func extNames(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		for _, n := range []string{"extension.mjs", "extension.cjs", "extension.js"} {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), n)); err == nil {
				out = append(out, e.Name())
				break
			}
		}
	}
	return out
}

// copilotExtNames are the project's extensions (.github/extensions in dir)
// and the user's own (~/.copilot/extensions).
func copilotExtNames(dir string) (project, user []string) {
	if dir != "" {
		project = extNames(filepath.Join(dir, ".github", "extensions"))
	}
	for _, n := range extNames(filepath.Join(copilotHome(), "extensions")) {
		if n != "specwright" { // SpecWright's own tools, not the user's (extension.SelfCopilotExt)
			user = append(user, n)
		}
	}
	return project, user
}

// hasSelfExt: SpecWright's own Copilot extension is installed (its tools
// need --experimental, as every extension does).
func hasSelfExt() bool {
	_, err := os.Stat(filepath.Join(copilotHome(), "extensions", "specwright", "extension.mjs"))
	return err == nil
}

// hasProjectExtensions reports whether dir has Copilot extensions in
// .github/extensions, or the user has their own.
func hasProjectExtensions(dir string) bool {
	p, u := copilotExtNames(dir)
	return len(p)+len(u) > 0
}

// env is req.Env plus what makes -p mode load the project's extensions
// (it skips them unless GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS is true).
func (c *Copilot) env(req Request) []string {
	if !hasProjectExtensions(req.Dir) {
		return req.Env
	}
	return append(append([]string{}, req.Env...), "GITHUB_COPILOT_PROMPT_MODE_EXTENSIONS=true")
}

// Run implements Driver.
func (c *Copilot) Run(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	bin, err := c.bin()
	if err != nil {
		return Result{}, err
	}
	if useSDK(req) { // a person is watching: SpecWright hosts the session (copilot_sdk.go)
		res, err := c.runSDK(ctx, bin, req, emit)
		if !errors.Is(err, errSDKUnavailable) {
			return res, err
		}
		emit(Event{Type: "agent.log", Summary: "Copilot could not be hosted by SpecWright; running it as copilot -p, where commands needing approval may be refused",
			Data: map[string]any{"line": err.Error()}})
	}
	if c.structured(ctx, bin) {
		return c.runJSON(ctx, bin, req, emit)
	}
	p := &copilotParser{emit: emit}
	emit(Event{Type: "model.request", Summary: "Request → Copilot", Data: map[string]any{"input_tokens": 0, "model": modelOr(req.Model, "copilot/default")}})
	base, dropped := baseEnv()
	noteDropped(emit, dropped)
	plugin, cleanup := permissionPlugin(req.MCPConfig, req.AddDirs...)
	defer cleanup()
	_, _, err = proc{bin: bin, args: append(c.argsWith(req, plugin), c.extraArgs(ctx, bin)...), dir: req.Dir, env: c.env(req), base: base, onLine: p.line,
		onErr: func(l string) {
			emit(Event{Type: "agent.log", Summary: short(l, 200), Data: map[string]any{"line": l, "stream": "stderr"}})
		},
	}.run(ctx)
	p.flush()
	return p.finish(), err
}

func modelOr(m, d string) string {
	if m != "" {
		return m
	}
	return d
}

var (
	cpToolRE    = regexp.MustCompile(`^\s*([●✓✔✗✘])\s+(.+)$`)
	cpPremiumRE = regexp.MustCompile(`(?i)total usage est:\s*([\d.]+)\s+premium request`)
	cpModelRE   = regexp.MustCompile(`(?i)^\s*([\w.\-/]+)\s+([\d.]+)(k|m)?\s+input,\s*([\d.]+)(k|m)?\s+output(?:,\s*([\d.]+)(k|m)?\s+cache read)?`)
)

type copilotParser struct {
	emit     func(Event)
	buf      []string
	res      Result
	in, out  int
	cached   int
	inUsage  bool
	lastTool string
}

func (p *copilotParser) line(l string) {
	if m := cpPremiumRE.FindStringSubmatch(l); m != nil {
		p.flush()
		f, _ := strconv.ParseFloat(m[1], 64)
		p.res.PremiumRequests = int(f + 0.5)
		p.inUsage = true
		return
	}
	if p.inUsage {
		if m := cpModelRE.FindStringSubmatch(l); m != nil {
			p.res.Model = m[1]
			p.in += num(m[2], m[3])
			p.out += num(m[4], m[5])
			if m[6] != "" {
				p.cached += num(m[6], m[7])
			}
		}
		return
	}
	if m := cpToolRE.FindStringSubmatch(l); m != nil {
		p.flush()
		desc := strings.TrimSpace(m[2])
		name, target, _ := strings.Cut(desc, " ")
		ok := m[1] != "✗" && m[1] != "✘"
		if m[1] == "●" {
			p.lastTool = name
			p.emit(Event{Type: "tool.call", Summary: short(desc, 140), Data: map[string]any{"tool": name, "args": map[string]any{"target": target}}})
			return
		}
		if p.lastTool == "" {
			p.emit(Event{Type: "tool.call", Summary: short(desc, 140), Data: map[string]any{"tool": name, "args": map[string]any{"target": target}}})
		}
		p.emit(Event{Type: "tool.result", Summary: fmt.Sprintf("%s %s", name, map[bool]string{true: "ok", false: "failed"}[ok]), Data: map[string]any{"tool": name, "ok": ok}})
		p.lastTool = ""
		return
	}
	if strings.TrimSpace(l) == "" {
		p.flush()
		return
	}
	p.buf = append(p.buf, l)
}

func (p *copilotParser) flush() {
	if len(p.buf) == 0 {
		return
	}
	t := strings.TrimSpace(strings.Join(p.buf, "\n"))
	p.buf = nil
	if t == "" {
		return
	}
	p.res.Text = t
	p.emit(Event{Type: "model.text", Summary: short(t, 160), Data: map[string]any{"text": t}})
}

func (p *copilotParser) finish() Result {
	p.emit(Event{Type: "model.response", Summary: "Copilot finished", Data: map[string]any{"output_tokens": p.out, "stop_reason": "end_turn"}})
	p.emit(Event{Type: "run.usage", Summary: fmt.Sprintf("Copilot: %d premium request(s)", p.res.PremiumRequests),
		Data: map[string]any{"premium_requests": p.res.PremiumRequests, "input_tokens": p.in, "output_tokens": p.out, "cached_tokens": p.cached, "model": p.res.Model}})
	return p.res
}

func num(s, unit string) int {
	f, _ := strconv.ParseFloat(s, 64)
	switch strings.ToLower(unit) {
	case "k":
		f *= 1e3
	case "m":
		f *= 1e6
	}
	return int(f + 0.5)
}

// noteDropped records in the run's activity that classic tokens were left out.
func noteDropped(emit func(Event), dropped []string) {
	if len(dropped) == 0 {
		return
	}
	emit(Event{Type: "agent.log", Summary: "Left out " + strings.Join(dropped, ", ") + ": a classic GitHub token (ghp_…), which the Copilot CLI rejects. Copilot uses your `copilot` login or the token pasted in SpecWright instead.",
		Data: map[string]any{"stream": "app"}})
}

// permissionPlugin writes a Copilot CLI plugin for one run whose
// permissionRequest hook is this executable's "copilot-hook permission",
// with the bridge address and token from the run's MCP config (a private
// file the app wrote for this run) and an id for "allow for this run". It
// returns the plugin directory and a cleanup func, or "" when the run has
// no MCP config (then the run keeps --allow-all-tools, as before).
func permissionPlugin(mcpConfig string, addDirs ...string) (string, func()) {
	if mcpConfig == "" {
		return "", func() {}
	}
	b, err := os.ReadFile(mcpConfig)
	if err != nil {
		return "", func() {}
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return "", func() {}
	}
	srv, ok := cfg.MCPServers["agentbridge"]
	if !ok || srv.Command == "" {
		return "", func() {}
	}
	dir, err := os.MkdirTemp(filepath.Dir(mcpConfig), "copilot-plugin-")
	if err != nil {
		return "", func() {}
	}
	done := func() { os.RemoveAll(dir) }
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	env := map[string]string{"CODEGEN_AGENT_ID": "copilot-" + hex.EncodeToString(id),
		"CODEGEN_EDIT_DIRS": strings.Join(addDirs, string(os.PathListSeparator))}
	for _, k := range []string{"CODEGEN_BRIDGE_URL", "CODEGEN_BRIDGE_TOKEN"} {
		env[k] = srv.Env[k]
	}
	manifest := map[string]any{"name": "specwright-permissions", "version": "1.0.0",
		"description": "Asks the SpecWright user before Copilot uses a tool it has no permission for."}
	hooks := map[string]any{"version": 1, "hooks": map[string]any{
		"permissionRequest": []map[string]any{{
			"type": "command", "exec": srv.Command, "args": []string{"copilot-hook", "permission"},
			"env": env, "timeoutSec": 900, // the user may take a while to answer
		}},
	}}
	for name, v := range map[string]any{"plugin.json": manifest, "hooks.json": hooks} {
		bs, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name), bs, 0o600); err != nil {
			done()
			return "", func() {}
		}
	}
	return dir, done
}
