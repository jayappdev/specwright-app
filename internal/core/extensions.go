package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
	"codegen/internal/extension"
)

// --- extension.Provider -------------------------------------------------

// Callable implements extension.Provider: approved, runnable extensions.
func (h *Hub) Callable() []extension.Extension {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.callableLocked()
}

func (h *Hub) callableLocked() []extension.Extension {
	// verified managed tools (global, admin-signed) come first and cannot be
	// shadowed by a workspace tool of the same name
	out := h.managedRunnable()
	managed := map[string]bool{}
	for _, e := range h.managedTools() {
		managed[e.Name] = true
	}
	if h.exts == nil {
		return out
	}
	all, err := h.exts.List()
	if err != nil {
		return out
	}
	for _, e := range all {
		if !managed[e.Name] && e.Runnable() == nil {
			out = append(out, e)
		}
	}
	return out
}

// Root implements extension.Provider.
func (h *Hub) Root() string { h.mu.RLock(); defer h.mu.RUnlock(); return h.root }

// testCall is one bridge call seen while "Test with AI" runs.
type testCall struct {
	name string
	args map[string]any
	res  extension.RunResult
}

// argsMap accepts tool arguments as an object or as a JSON-encoded string.
func argsMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			return m
		}
	}
	return nil
}

// Observe implements extension.Provider: every bridge call becomes an
// ext.result event on the live run, and is captured during a test.
func (h *Hub) Observe(name string, args map[string]any, res extension.RunResult) {
	h.mu.RLock()
	tc := h.testCalls
	h.mu.RUnlock()
	if tc != nil {
		select {
		case tc <- testCall{name: name, args: args, res: res}:
		default:
		}
	}
	e := bus.Event{Type: "ext.result", Summary: fmt.Sprintf("%s → exit %d", name, res.ExitCode),
		Data: map[string]any{"name": name, "args": args, "ok": res.OK, "exit_code": res.ExitCode, "duration_ms": res.DurationMS, "output": trunc(res.Output, 2000)}}
	if a := h.activeRun(); a != nil {
		e.RunID, e.Step = a.Session.ID, a.Session.Step
		_ = h.Store.Append(h.Bus.Publish(e))
		return
	}
	h.Bus.Publish(e)
}

// Prepare implements extension.Provider: install a saved tool's packages.
func (h *Hub) Prepare(ctx context.Context, e extension.Extension) error {
	root := h.Root()
	dir := e.Dir
	if dir == "" {
		dir = extension.ToolDir(e.Scope, e.Name)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, filepath.FromSlash(dir))
	}
	return extension.Install(ctx, root, dir, e, h.packageSettings(), func(l string) {
		h.Bus.Publish(bus.Event{Type: "ext.install.line", Summary: l})
	})
}

// Env implements extension.Provider.
func (h *Hub) Env() []string { return h.packageSettings().Env(h.Root()) }

func (h *Hub) mcpConfig(extensions bool) (string, func(), error) {
	p, err := h.Bridge.WriteMCPConfig(filepath.Join(h.Store.Dir(), "tmp"), extensions)
	if err != nil {
		return "", nil, err
	}
	return p, func() { _ = os.Remove(p) }, nil
}

// --- extensions API ------------------------------------------------------

// Extensions lists the workspace's extensions (saved + ext:* npm scripts).
// Managed (admin) tools are listed too, read-only, in scope "managed".
func (h *Hub) Extensions() ([]extension.Extension, error) {
	if _, err := h.need(); err != nil {
		return nil, err
	}
	mine, err := h.exts.List()
	if err != nil {
		return nil, err
	}
	managed := h.managedTools()
	names := map[string]bool{}
	for _, e := range managed {
		names[e.Name] = true
	}
	out := append([]extension.Extension{}, managed...)
	for _, e := range mine {
		if !names[e.Name] { // a managed tool of the same name wins
			out = append(out, e)
		}
	}
	return out, nil
}

// ManagedTools lists the admin-published tools (no workspace needed).
func (h *Hub) ManagedTools() []extension.Extension {
	out := h.managedTools()
	if out == nil {
		out = []extension.Extension{}
	}
	return out
}

// SaveExtension stores (and so approves) an extension the user reviewed.
func (h *Hub) SaveExtension(e extension.Extension) (extension.Extension, error) {
	if _, err := h.need(); err != nil {
		return e, err
	}
	if extension.IsManagedScope(e.Scope) || e.Managed != nil || h.isManagedName(e.Name) {
		return e, managedRefusal(e.Name)
	}
	if e.Scope == "global" && (e.LastTest == nil || !e.LastTest.Pass) {
		return e, errors.New("global tools must pass a test before they can be submitted")
	}
	saved, err := h.exts.Save(e)
	if err != nil || !extension.HasDeps(saved) {
		return saved, err
	}
	// install now so the first agent call does not wait for downloads
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Minute)
	defer cancel()
	if err := h.Prepare(ctx, saved); err != nil {
		return saved, fmt.Errorf("%s was saved, but installing its dependencies failed (it will retry on first use): %v", saved.Name, err)
	}
	return saved, nil
}

// ApproveExtension approves an ext:* npm script at its current contents.
func (h *Hub) ApproveExtension(name string) (extension.Extension, error) {
	if _, err := h.need(); err != nil {
		return extension.Extension{}, err
	}
	if h.isManagedName(name) {
		return extension.Extension{}, managedRefusal(name)
	}
	return h.exts.Approve(name)
}

// DeleteExtension removes an extension from extensions.json.
func (h *Hub) DeleteExtension(name string) error {
	if _, err := h.need(); err != nil {
		return err
	}
	if h.isManagedName(name) {
		return managedRefusal(name)
	}
	return h.exts.Delete(name)
}

// SetSecret keeps a secret value in memory for this session.
func (h *Hub) SetSecret(name, value string) {
	h.mu.Lock()
	if value == "" {
		delete(h.secrets, name)
	} else {
		h.secrets[name] = value
	}
	h.mu.Unlock()
	h.Store.Redactor().Add(value)
}

// GenerateRequest asks the agent to write an extension.
type GenerateRequest struct {
	Prompt   string               `json:"prompt"`
	RunsIn   string               `json:"runsIn"`
	Connects []string             `json:"connects"`
	Draft    *extension.Extension `json:"draft,omitempty"` // for refinement
	Change   string               `json:"change,omitempty"`
	// Failure and Question ask the agent to fix a draft that failed "Test with AI".
	Failure  *extension.TestResult `json:"failure,omitempty"`
	Question string                `json:"question,omitempty"`
	// Attachments are the files attached to Question (workspace paths).
	Attachments []string `json:"attachments,omitempty"`
}

const genInstructions = `You write small, safe developer tools that an AI coding agent can call.
Write the tool in the language given under "Runs in". The language priority is
Node.js, then Go, then Java, then Bash: only move down the list when the one
above cannot do the job.
Reply with ONE JSON object and nothing else, with these fields:
{"name": kebab-case (max 40 chars), "desc": one sentence the agent sees,
 "lang": "Node.js"|"Go"|"Java"|"Bash"|"Python", "params": [{"name","type","required","desc"}],
 "secrets": [{"name": ENV_VAR, "desc"}], "perms": ["read files", ...], "deps": [...],
 "command": how to run it, with {{param}} placeholders:
   Node.js "node .codegen/tools/<name>.js {{param}}" (standard library only, Node 18+),
   Go      "go run .codegen/tools/<name>.go {{param}}" (package main, standard library only),
   Java    "java .codegen/tools/<name>.java {{param}}" (one source file, JDK 11+ launcher, no dependencies),
   Bash    "bash .codegen/tools/<name>.sh {{param}}",
 "script": full source text, "sampleTest": a question a developer would ask,
 "call": example arguments object}
Prefer the standard library. When a package is needed (a database driver, an
SDK), list it in "deps" with an exact version; it is installed into the tool's
own folder from the company's registry (proxy and registry URLs are set by the
app, never by the script):
   Node.js "pg@8.13.1" · Go "github.com/jackc/pgx/v5@v5.7.1" ·
   Java "org.postgresql:postgresql:42.7.4" · Python "psycopg[binary]==3.2.3".
Bash tools cannot have deps. With deps, the script is the entry file
(Node.js index.js CommonJS, Go main.go package main, Java Tool.java,
Python main.py) and the app writes the command; arguments arrive in the order
of "params".
Arguments arrive as separate command-line arguments in the order of the
command's {{param}} placeholders; an optional one may be missing.
Arguments, file names and folder names may contain spaces, quotes, $, &, #,
parentheses and non-ASCII letters, and the tool may live in a folder whose
path has them too. Treat every argument as data:
  Bash: quote every expansion ("$1", "$path"), use -- before file arguments,
        never eval or build command strings;
  Node.js: child_process.execFile/spawn with an argument array, never exec;
  Go: exec.Command(name, args...) with separate arguments;
  Python: subprocess.run([...]) with a list, never shell=True;
  Java: ProcessBuilder with a list.
Resolve files relative to the working directory (the workspace) or to the
script's own folder (__dirname, os.Executable, Path(__file__)), not a
hard-coded path. In "command", quote a path that has spaces.
Never put secret values in the script; read them from environment variables.
Print the result to stdout and exit non-zero on failure.
Do not create or modify files; only answer.`

// RuntimePriority is the order "Auto" tries tool languages in.
var RuntimePriority = []struct{ Lang, Bin string }{{"Node.js", "node"}, {"Go", "go"}, {"Java", "java"}, {"Bash", "bash"}}

// Runtimes lists the tool languages installed on this machine, in priority order.
func Runtimes() []string {
	var out []string
	for _, r := range RuntimePriority {
		if runtimeInstalled(r.Bin) {
			out = append(out, r.Lang)
		}
	}
	return out
}

func runtimeInstalled(bin string) bool {
	p, err := exec.LookPath(bin)
	if err != nil {
		return false
	}
	if bin == "java" { // macOS ships a /usr/bin/java stub that fails without a JDK
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, p, "-version").Run() == nil
	}
	return true
}

// pickRuntime resolves "Auto" to the first installed language in RuntimePriority.
func pickRuntime(runsIn string) (lang, note string) {
	if runsIn != "" && runsIn != "Auto" {
		return runsIn, ""
	}
	var skipped []string
	for _, r := range RuntimePriority {
		if runtimeInstalled(r.Bin) {
			if len(skipped) > 0 {
				note = fmt.Sprintf(" (%s not installed on this computer)", strings.Join(skipped, ", "))
			}
			return r.Lang, note
		}
		skipped = append(skipped, r.Lang)
	}
	return "Bash", ""
}

// fixRequest describes a failed test so the agent can repair the draft.
func fixRequest(r GenerateRequest) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(r.Change))
	if f := r.Failure; f != nil {
		if b.Len() == 0 {
			b.WriteString("Fix the tool so the test passes.")
		}
		fmt.Fprintf(&b, "\n\nThe test failed.\nDeveloper question: %s\n", r.Question)
		if len(r.Attachments) > 0 {
			fmt.Fprintf(&b, "Files attached to the question (workspace paths; the tool may need to accept a file path): %s\n", strings.Join(r.Attachments, ", "))
		}
		for _, c := range f.Checks {
			if !c.OK {
				fmt.Fprintf(&b, "- failed check: %s\n", c.Label)
			}
		}
		if f.Summary != "" {
			fmt.Fprintf(&b, "Summary: %s\n", f.Summary)
		}
		for _, st := range f.Steps {
			if st.K == "run" || st.K == "out" || st.K == "install" {
				fmt.Fprintf(&b, "%s:\n%s\n", map[string]string{"run": "Command", "out": "Output", "install": "Dependency install log"}[st.K], trunc(st.V, 3000))
			}
		}
		b.WriteString("If an error shows a path cut off at a space or quote (for example \"Cannot find module\" or \"No such file\" with a partial path), the script is splitting or unquoting an argument: quote it or pass arguments as an array. ")
		b.WriteString("If the output shows a missing module or package, add it to deps with an exact version. If the dependency install failed, fix the package name or version (check the log). ")
		b.WriteString("Keep the tool's purpose. Fix the script, command, parameters or description so the agent picks the tool and it exits 0 with a useful answer. A missing secret cannot be fixed in code: never hard-code one.")
	}
	return b.String()
}

// GenerateExtension asks the agent for a draft (or a revised draft).
func (h *Hub) GenerateExtension(r GenerateRequest) (extension.Extension, error) {
	root, err := h.need()
	if err != nil {
		return extension.Extension{}, err
	}
	var prompt string
	if r.Draft != nil {
		b, _ := json.MarshalIndent(r.Draft, "", "  ")
		prompt = genInstructions + "\n\nRevise this tool. Runs in: " + firstNonEmpty(r.Draft.Lang, "Node.js") + "\nChange request: " + fixRequest(r) + "\n\nCurrent tool:\n" + string(b)
	} else {
		if strings.TrimSpace(r.Prompt) == "" {
			return extension.Extension{}, errors.New("describe what the tool should do")
		}
		lang, note := pickRuntime(r.RunsIn)
		prompt = fmt.Sprintf("%s\n\nDescription: %s\nRuns in: %s%s\nConnects to: %s", genInstructions, r.Prompt, lang, note, strings.Join(r.Connects, ", "))
	}
	h.mu.RLock()
	name := h.agentName
	model := h.model
	h.mu.RUnlock()
	drv, err := h.Agents.Get(name)
	if err != nil {
		return extension.Extension{}, err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 4*time.Minute)
	defer cancel()
	var text strings.Builder
	res, err := drv.Run(ctx, agent.Request{Purpose: "generate", Prompt: prompt, Dir: root, Model: model, Env: h.childEnv()}, func(ev agent.Event) {
		if ev.Type == "model.text" {
			if t, _ := ev.Data["text"].(string); t != "" {
				text.WriteString(t + "\n")
			}
		}
		h.Bus.Publish(bus.Event{Type: "gen." + ev.Type, Summary: ev.Summary})
	})
	if err != nil {
		return extension.Extension{}, err
	}
	out := res.Text
	if out == "" {
		out = text.String()
	}
	var d extension.Extension
	if err := json.Unmarshal([]byte(firstJSON(out)), &d); err != nil {
		return extension.Extension{}, fmt.Errorf("the agent did not return a tool definition: %v", err)
	}
	if d.Kind == "" {
		d.Kind = "script"
	}
	if d.Params == nil {
		d.Params = []extension.Param{}
	}
	if d.Secrets == nil {
		d.Secrets = []extension.Secret{}
	}
	d.Prompt = r.Prompt
	d.Status = "untested"
	d.Revision = 1
	if r.Draft != nil {
		d.Revision = r.Draft.Revision + 1
		d.Prompt = r.Draft.Prompt
		d.Scope = r.Draft.Scope
	}
	if d.TestPrompt == "" {
		d.TestPrompt = d.SampleTest
	}
	return d, nil
}

var fenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// firstJSON extracts the first JSON object from model text.
func firstJSON(s string) string {
	if m := fenceRE.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	if i := strings.IndexByte(s, '{'); i >= 0 {
		depth, inStr, esc := 0, false, false
		for j := i; j < len(s); j++ {
			c := s[j]
			switch {
			case esc:
				esc = false
			case c == '\\' && inStr:
				esc = true
			case c == '"':
				inStr = !inStr
			case !inStr && c == '{':
				depth++
			case !inStr && c == '}':
				depth--
				if depth == 0 {
					return s[i : j+1]
				}
			}
		}
	}
	return s
}

// TestExtension asks the agent a developer question with only this draft
// available, then checks that it picked the tool, the call succeeded and
// no secret leaked.
func (h *Hub) TestExtension(d extension.Extension, question string) (extension.TestResult, error) {
	return h.TestExtensionWith(d, question, nil)
}

// TestExtensionWith is TestExtension with files attached to the question
// (workspace paths returned by SaveAttachment).
func (h *Hub) TestExtensionWith(d extension.Extension, question string, files []string) (extension.TestResult, error) {
	root, err := h.need()
	if err != nil {
		return extension.TestResult{}, err
	}
	if strings.TrimSpace(question) == "" {
		return extension.TestResult{}, errors.New("write what you would ask the agent")
	}
	tmp := filepath.Join(h.Store.Dir(), "tmp", "test-"+d.Name)
	td := d
	var sp, shown string // temp path, and the path to show instead
	var installLog []string
	tr := extension.TestResult{At: time.Now()}
	switch {
	case d.Kind != "npm" && extension.HasDeps(d):
		// folder layout: write the tool and its manifest, then install its
		// packages through the workspace's proxy and registries
		_ = os.RemoveAll(tmp)
		defer os.RemoveAll(tmp)
		if td, err = extension.WriteTool(tmp, tmp, d); err != nil {
			return extension.TestResult{}, err
		}
		sp, shown = filepath.ToSlash(tmp), extension.ToolDir(d.Scope, d.Name)
		h.Bus.Publish(bus.Event{Type: "ext.install", Summary: "Installing dependencies for " + d.Name})
		ierr := extension.Install(h.ctx, root, tmp, td, h.packageSettings(), func(l string) {
			installLog = append(installLog, strings.ReplaceAll(l, sp, shown))
			h.Bus.Publish(bus.Event{Type: "ext.install.line", Summary: l})
		})
		if len(installLog) > 60 {
			installLog = append([]string{"…"}, installLog[len(installLog)-60:]...)
		}
		tr.Steps = append(tr.Steps, extension.TestStep{K: "install", V: strings.Join(installLog, "\n")})
		if ierr != nil {
			tr.Steps = append(tr.Steps, extension.TestStep{K: "out", V: "error: " + strings.ReplaceAll(ierr.Error(), sp, shown)})
			tr.Output = ierr.Error()
			tr.Checks = []extension.Check{{Label: "Dependencies installed", OK: false}}
			tr.Summary = "Failed: installing " + strings.Join(d.Deps, ", ") + " did not work. Check the package names and versions, or the proxy and registry under Packages & proxy."
			return tr, nil
		}
	case d.Kind != "npm" && d.Script != "":
		ext := extension.ScriptExt(d.Lang)
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return extension.TestResult{}, err
		}
		sp = filepath.Join(tmp, d.Name+ext)
		shown = ".codegen/tools/" + d.Name + ext
		if err := os.WriteFile(sp, []byte(d.Script), 0o700); err != nil {
			return extension.TestResult{}, err
		}
		defer os.RemoveAll(tmp)
		td.Command = extension.RepointScript(d.Command, d.Name, sp)
	}
	release := h.Bridge.AllowTemporary(td)
	defer release()
	calls := make(chan testCall, 8)
	h.mu.Lock()
	h.testCalls = calls
	name, model := h.agentName, h.model
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.testCalls = nil; h.mu.Unlock() }()

	drv, err := h.Agents.Get(name)
	if err != nil {
		return extension.TestResult{}, err
	}
	mcp, done, err := h.mcpConfig(true)
	if err != nil {
		return extension.TestResult{}, err
	}
	defer done()
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Minute)
	defer cancel()

	atts := h.checkAttachments(files)
	if len(atts) > 0 {
		var names []string
		for _, a := range atts {
			names = append(names, a.Path)
		}
		tr.Steps = append(tr.Steps, extension.TestStep{K: "attach", V: strings.Join(names, "\n")})
	}
	tr.Steps = append(tr.Steps, extension.TestStep{K: "say", V: fmt.Sprintf("Read the tool schema for %s (%d parameter%s).", d.Name, len(d.Params), plural(len(d.Params)))})
	var picked bool
	var callArgs map[string]any
	var agentText strings.Builder
	start := time.Now()
	res, runErr := drv.Run(ctx, agent.Request{Purpose: "test", Prompt: question + attachmentPrompt(atts) + "\n\nAnswer using the extension_managed tool from the agentbridge MCP server (tool name: " + d.Name + ").",
		Dir: root, Model: model, Env: h.childEnv(), MCPConfig: mcp, AllowTools: []string{"mcp__agentbridge"},
		Hints: []agent.ToolHint{{Name: d.Name, Args: d.Call}}}, func(ev agent.Event) {
		switch ev.Type {
		case "tool.call":
			if t, _ := ev.Data["tool"].(string); strings.HasSuffix(t, extension.ToolName) {
				if a, ok := ev.Data["args"].(map[string]any); ok {
					if n, _ := a["name"].(string); n == d.Name {
						picked = true
						callArgs, _ = a["arguments"].(map[string]any)
						b, _ := json.MarshalIndent(map[string]any{"tool": d.Name, "arguments": callArgs}, "", "  ")
						tr.Steps = append(tr.Steps, extension.TestStep{K: "call", V: string(b)})
					}
				}
			}
		case "model.text":
			if t, _ := ev.Data["text"].(string); t != "" {
				agentText.WriteString(t + "\n")
			}
		}
	})
	elapsed := time.Since(start)
	// The bridge is the ground truth for what the agent called: CLIs differ
	// in how (or whether) they report MCP tool calls in their output.
	var call extension.RunResult
	got := false
drain:
	for {
		select {
		case c := <-calls:
			if c.name != d.Name {
				continue
			}
			call, got = c.res, true
			if !picked {
				picked, callArgs = true, c.args
				b, _ := json.MarshalIndent(map[string]any{"tool": d.Name, "arguments": c.args}, "", "  ")
				tr.Steps = append(tr.Steps, extension.TestStep{K: "call", V: string(b)})
			}
			if callArgs == nil {
				callArgs = c.args
			}
		default:
			break drain
		}
	}
	if got {
		// show the workspace path instead of the temp copy, argument by argument
		cmd := call.Command
		if sp != "" && len(call.Argv) > 0 {
			argv := make([]string, len(call.Argv))
			for i, a := range call.Argv {
				argv[i] = strings.ReplaceAll(a, sp, shown)
			}
			cmd = extension.JoinWords(argv)
		}
		tr.Steps = append(tr.Steps, extension.TestStep{K: "run", V: "$ " + cmd})
		out := call.Output
		if sp != "" {
			out = strings.ReplaceAll(out, sp, shown)
		}
		if call.Error != "" {
			out = "error: " + call.Error + "\n" + out
		}
		tr.Steps = append(tr.Steps, extension.TestStep{K: "out", V: out})
		tr.Output = out
	} else if runErr != nil {
		tr.Steps = append(tr.Steps, extension.TestStep{K: "out", V: "error: " + runErr.Error()})
	}
	argsOK := picked
	for _, p := range d.Params {
		if p.Required {
			if v, ok := callArgs[p.Name]; !ok || fmt.Sprint(v) == "" {
				argsOK = false
			}
		}
	}
	leak := false
	all := agentText.String() + res.Text + tr.Output
	for _, s := range d.Secrets {
		h.mu.RLock()
		v := h.resolved[s.Name]
		h.mu.RUnlock()
		if v != "" && strings.Contains(all, v) {
			leak = true
		}
	}
	// the agent's own answer (what it concluded, including what it saw in
	// attached images and PDFs), with any secret value masked
	if ans := strings.TrimSpace(res.Text); ans != "" || agentText.Len() > 0 {
		if ans == "" {
			ans = strings.TrimSpace(agentText.String())
		}
		for _, s := range d.Secrets {
			h.mu.RLock()
			v := h.resolved[s.Name]
			h.mu.RUnlock()
			if v != "" {
				ans = strings.ReplaceAll(ans, v, "[REDACTED]")
			}
		}
		tr.Steps = append(tr.Steps, extension.TestStep{K: "answer", V: trunc(ans, 4000)})
	}
	tr.Checks = []extension.Check{}
	if extension.HasDeps(d) {
		tr.Checks = append(tr.Checks, extension.Check{Label: "Dependencies installed", OK: true})
	}
	tr.Checks = append(tr.Checks, []extension.Check{
		{Label: "Agent picked this tool for the request", OK: picked},
		{Label: "Arguments match the parameter schema", OK: argsOK},
		{Label: "Exit code 0", OK: got && call.OK && call.Error == ""},
		{Label: "Output answers the request", OK: got && strings.TrimSpace(call.Output) != "" && call.OK},
		{Label: "No secret values in prompt or output", OK: !leak},
		{Label: "Finished within 30 s", OK: got && call.DurationMS < 30_000},
	}...)
	tr.Pass = true
	var failed []string
	for _, c := range tr.Checks {
		if !c.OK {
			tr.Pass = false
			failed = append(failed, strings.ToLower(c.Label))
		}
	}
	if tr.Pass {
		tr.Summary = fmt.Sprintf("The tool answered “%s” correctly in %s.", strings.TrimSpace(question), elapsed.Round(100*time.Millisecond))
	} else {
		var missing []string
		for _, st := range h.SecretInfo(d.Secrets) {
			if st.Via == "missing" {
				missing = append(missing, st.Name)
			}
		}
		tr.Summary = "Failed: " + strings.Join(failed, "; ") + "."
		if len(missing) > 0 {
			tr.Summary += " " + strings.Join(missing, ", ") + " has no value: set the environment variable, enter it under Secrets, or switch its source to “Ask me”."
		}
		if runErr != nil {
			tr.Summary += " Agent error: " + trunc(runErr.Error(), 200)
		}
	}
	if saved, err := h.exts.Get(d.Name); err == nil && saved.Kind == d.Kind {
		_ = h.exts.SetTest(d.Name, tr)
	}
	return tr, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
