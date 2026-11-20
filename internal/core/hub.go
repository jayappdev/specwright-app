// Package core wires the internal packages into the Hub, the single object
// the desktop UI (through app.go) and the headless runner talk to.
package core

import (
	"codegen/internal/builtin"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/agent"
	"codegen/internal/aishim"
	"codegen/internal/bus"
	"codegen/internal/extension"
	"codegen/internal/session"
	"codegen/internal/speckit"
	"codegen/internal/store"
	"codegen/internal/watch"
	"codegen/internal/workflow"
)

// Version of the app.
const Version = "0.1.0"

// Hub owns the bus, store, agents, extension bridge and the open workspace.
type Hub struct {
	Bus    *bus.Bus
	Store  *store.Store
	Agents *agent.Registry
	Bridge *extension.Bridge

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.RWMutex
	root      string
	profile   *workflow.Profile
	wf        *workflow.Workflow
	exts      *extension.Store
	pkgs      *extension.PackageStore
	runner    *workflow.Runner
	watcher   *watch.Watcher
	stopWatch context.CancelFunc
	agentName string
	model     string
	token     string              // session-only credential, never persisted
	sec       secretsState        // session secrets, pending prompts, resolved values (secrets.go)
	qs        questionsState      // ask_user questions waiting for a person (questions.go)
	flowRuns  map[string]*flowRun // live flow runs
	resultsWG sync.WaitGroup      // results folders being written
	closing   bool                // Close has started: no new results writes
	// Clipboard copies text for a flow output's "copy" action (set by the
	// desktop app; nil in headless runs).
	Clipboard func(string) error
	// admin mode: the decrypted signing key for this session (nil = locked)
	// and the app-wide catalog of managed tools (global, not per workspace)
	adminPriv ed25519.PrivateKey
	adminName string
	managed   *extension.ManagedStore
	libs      *extension.LibraryStore // corporate (global) libraries
	tools     *builtin.Box            // built-in agent tools (read_file, edit_file, …)
	aiShimDir string                  // claude/copilot launchers put first on tools' PATH ("" = off)
	// Spec Kit download (see speckit.go)
	sk        skDownload      // the Spec Kit download in progress (speckit.go)
	perms     permissionState // permission prompts (permissions.go)
	runs      map[string]*workflow.Run
	testCalls chan testCall
	trig      *triggerStore  // triggers.go: the open project's trigger state
	sched     sync.WaitGroup // the triggers' scheduler goroutines (Close waits for them)

}

// Options configure New.
type Options struct {
	StoreDir string // default store.DefaultDir()
	Agent    string // default "claude"
	// AIShimExe is this program's path, for the claude/copilot launchers
	// that count the AI a tool uses itself (see internal/aishim). Empty
	// leaves tools' PATH alone.
	AIShimExe string
}

// New creates a hub. Call Close when done.
func New(o Options) (*Hub, error) {
	if o.StoreDir == "" {
		o.StoreDir = store.DefaultDir()
	}
	st, err := store.Open(o.StoreDir)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	st.MarkInterrupted()
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{Bus: bus.New(), Store: st, Agents: agent.NewRegistry(), ctx: ctx, cancel: cancel,
		agentName: o.Agent, runs: map[string]*workflow.Run{}}
	h.sec.values = map[string]string{}
	if h.agentName == "" {
		h.agentName = "claude"
	}
	h.managed = extension.NewManagedStore(filepath.Join(st.Dir(), "managed-tools.json"))
	h.libs = extension.NewLibraryStore(filepath.Join(st.Dir(), "managed-libraries.json"))
	h.tools = h.newToolbox()
	if o.AIShimExe != "" {
		dir := filepath.Join(st.Dir(), "aishim")
		if err := aishim.Install(dir, o.AIShimExe); err == nil {
			h.aiShimDir = dir
		}
	}
	h.watchRunEnds()
	h.reportStoreErrors()
	go h.autoArchiveLoop()
	h.Bus.SetFilter(func(e *bus.Event) { st.Redactor().Event(e) })
	br, err := extension.StartBridge(h)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start extension bridge: %w", err)
	}
	h.Bridge = br
	return h, nil
}

// Close stops the watcher, cancels live runs and closes the bridge.
func (h *Hub) Close() {
	h.tools.Close()
	h.mu.Lock()
	h.closing = true
	if h.stopWatch != nil {
		h.stopWatch()
	}
	live := make([]*workflow.Run, 0, len(h.runs))
	for _, r := range h.runs {
		r.Cancel()
		live = append(live, r)
	}
	h.mu.Unlock()
	h.cancel()
	waitTimeout(&h.sched, 3*time.Second) // the scheduler and its folder watchers stop
	for _, r := range live {             // let runs record their end before we return
		select {
		case <-r.Done():
		case <-time.After(3 * time.Second):
		}
	}
	if h.Bridge != nil {
		_ = h.Bridge.Close()
	}
	h.resultsWG.Wait() // finish writing results folders
}

// Info is the startup snapshot for the UI.
type Info struct {
	Version   string              `json:"version"`
	Workspace string              `json:"workspace"`
	Agent     string              `json:"agent"`
	Model     string              `json:"model"`
	HasToken  bool                `json:"hasToken"`
	Agents    []agent.Status      `json:"agents"`
	StoreDir  string              `json:"storeDir"`
	Node      string              `json:"node"`
	Languages map[string][]string `json:"languages"`
	LangOrder []string            `json:"langOrder"`
	// Runtimes are the tool languages installed here, in priority order
	// (Node.js → Go → Java → Bash); "Auto" uses the first.
	Runtimes []string `json:"runtimes"`
	// OS is the operating system (darwin, windows, linux): where secrets are kept is named after it.
	OS string `json:"os"`
}

// Info reports versions, agents and the open workspace.
func (h *Hub) Info() Info {
	ctx, cancel := context.WithTimeout(h.ctx, 4*time.Second)
	defer cancel()
	var sts []agent.Status
	for _, n := range []string{"claude", "copilot", "mock"} {
		if d, err := h.Agents.Get(n); err == nil {
			sts = append(sts, d.Status(ctx))
		}
	}
	node, _ := exec.LookPath("node")
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Info{Version: Version, Workspace: h.root, Agent: h.agentName, Model: h.model, HasToken: h.token != "", Agents: sts,
		StoreDir: h.Store.Dir(), Node: node, Languages: workflow.Languages, LangOrder: workflow.LanguageOrder, Runtimes: Runtimes(), OS: runtime.GOOS}
}

// SetAgent chooses the agent CLI. A token, if given, is kept in memory for
// this session only and handed to the CLI through its environment.
func (h *Hub) SetAgent(name, token, model string) error {
	if _, err := h.Agents.Get(name); err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if strings.ContainsAny(token, " \t\n") {
		return errors.New("a token cannot contain whitespace")
	}
	if name == "copilot" && strings.HasPrefix(token, "ghp_") {
		return errors.New("the Copilot CLI rejects classic tokens (ghp_…); create a fine-grained token (github_pat_…) with the “Copilot Requests” permission")
	}
	h.mu.Lock()
	h.agentName, h.model = name, strings.TrimSpace(model)
	if token != "" {
		h.token = token
		h.Store.Redactor().Add(token)
	}
	h.mu.Unlock()
	return nil
}

// SetCopilotWeb sets which web pages Copilot may open in runs ("none",
// "sites" with a list of domains or https URLs, or "all"). Copilot runs
// without a person at the keyboard, so a page it may not open is denied.
func (h *Hub) SetCopilotWeb(w agent.WebAccess) error {
	switch w.Mode {
	case "", "none":
		w = agent.WebAccess{Mode: "none"}
	case "all":
		w.Sites = nil
	case "sites":
		var sites []string
		for _, s := range w.Sites {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if strings.ContainsAny(s, " \t\n,;") {
				return fmt.Errorf("%q: give one domain or URL per line", s)
			}
			if strings.Contains(s, "://") && !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
				return fmt.Errorf("%q: only http and https addresses", s)
			}
			sites = append(sites, s)
		}
		if len(sites) == 0 {
			return errors.New("name at least one site, or choose no web access")
		}
		w.Sites = sites
	default:
		return fmt.Errorf("unknown web access %q", w.Mode)
	}
	d, err := h.Agents.Get("copilot")
	if err != nil {
		return err
	}
	cp, ok := d.(*agent.Copilot)
	if !ok {
		return errors.New("the Copilot driver cannot take web settings")
	}
	cp.SetWeb(w)
	return nil
}

// ClearToken forgets the session token.
func (h *Hub) ClearToken() { h.mu.Lock(); h.token = ""; h.mu.Unlock() }

func (h *Hub) childEnv() []string {
	h.mu.RLock()
	root := h.root
	h.mu.RUnlock()
	return append(h.tokenEnv(), h.agentPackageEnv(root)...)
}

func (h *Hub) tokenEnv() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	env := append([]string{}, h.Bridge.Env()...)
	// a tool call may wait for the user to type a secret: give the CLI's
	// MCP client the same patience as the bridge (Claude Code reads this)
	env = append(env, fmt.Sprintf("MCP_TOOL_TIMEOUT=%d", (AskSecretsTimeout+time.Minute).Milliseconds()))
	if h.token != "" {
		switch h.agentName {
		case "copilot":
			env = append(env, "COPILOT_GITHUB_TOKEN="+h.token)
		case "claude":
			if strings.HasPrefix(h.token, "sk-ant-oat") {
				env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+h.token)
			} else {
				env = append(env, "ANTHROPIC_API_KEY="+h.token)
			}
		}
	}
	return env
}

// WorkspaceState is everything the UI shows about the open workspace.
type WorkspaceState struct {
	Root       string                `json:"root"`
	Name       string                `json:"name"`
	Profile    *workflow.Profile     `json:"profile"`
	Libraries  []workflow.Library    `json:"libraries"`
	Workflow   *workflow.Workflow    `json:"workflow"`
	Extensions []extension.Extension `json:"extensions"`
	FileCount  int                   `json:"fileCount"`
	SpecKit    bool                  `json:"specKit"` // .specify/ exists
	SpecKitSet speckit.Info          `json:"speckit"`
	Projects   []SpecProject         `json:"projects"` // the workspace's Spec Kit projects
}

// OpenWorkspace makes root the workspace: loads its profile, workflow and
// extensions and starts the watcher.
func (h *Hub) OpenWorkspace(root string) (WorkspaceState, error) {
	if strings.HasPrefix(root, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, root[2:])
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return WorkspaceState{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return WorkspaceState{}, fmt.Errorf("%s is not a folder", root)
	}
	if a := h.activeRun(); a != nil {
		return WorkspaceState{}, errors.New("finish or cancel the running step before switching workspace")
	}
	prof, err := workflow.LoadProfile(abs)
	if err != nil {
		return WorkspaceState{}, fmt.Errorf("profile: %w", err)
	}
	// the selected Spec Kit project's workflow (each project has its own)
	wf, err := workflow.LoadProject(abs, prof.SpecProject, nil, true)
	if err != nil {
		return WorkspaceState{}, fmt.Errorf("workflow: %w", err)
	}
	// a step left running or paused by a previous app session cannot resume
	interrupt(wf)
	h.mu.Lock()
	if h.stopWatch != nil {
		h.stopWatch()
	}
	h.root, h.profile, h.wf = abs, prof, wf
	h.exts = extension.NewStore(abs)
	h.pkgs = extension.NewPackageStore(abs)
	h.runner = &workflow.Runner{Bus: h.Bus, Store: h.Store, Agents: h.Agents, Workspace: abs,
		Env: h.childEnv, MCPConfig: h.mcpConfig, OnStatus: h.onStepStatus,
		ToolsReached: func(since time.Time) bool { return h.Bridge.LastContact().After(since) }}
	h.tools.SetRoot(abs)
	h.watcher = watch.New(abs, time.Second)
	w0 := h.watcher
	h.runner.Flush = func() {
		if ch := w0.Poll(); len(ch) > 0 {
			h.onFiles(ch)
		}
	}
	wctx, stop := context.WithCancel(h.ctx)
	h.stopWatch = stop
	w := h.watcher
	h.mu.Unlock()
	w.Poll() // take the baseline snapshot now
	go w.Run(wctx, h.onFiles)
	ts := h.triggers(abs)
	ts.mu.Lock()
	ts.projW = w // its scan is kept for files triggers that count changes made while closed
	// this opening's scheduler is the current one from now, not from when
	// its goroutine happens to run: a slower, older one must not take over
	ts.ctx = wctx
	ts.mu.Unlock()
	h.sched.Add(1)
	go func() { // the project's triggers (schedules, files, after flows); triggers.go
		defer h.sched.Done()
		h.runScheduler(wctx, abs)
	}()
	h.Bus.Publish(bus.Event{Type: "workspace.opened", Summary: abs, Data: map[string]any{"root": abs}})
	return h.Workspace()
}

// Workspace returns the current workspace state.
func (h *Hub) Workspace() (WorkspaceState, error) {
	h.mu.RLock()
	root, prof, wf, es := h.root, h.profile, h.wf, h.exts
	h.mu.RUnlock()
	if root == "" {
		return WorkspaceState{}, errors.New("no workspace is open")
	}
	exts, err := es.List()
	if err != nil {
		return WorkspaceState{}, err
	}
	return WorkspaceState{Root: root, Name: filepath.Base(root), Profile: prof, Libraries: h.libsFor(root, prof), Workflow: wf,
		Extensions: exts, FileCount: len(watch.ListFiles(root, 20000)), SpecKit: h.SpecKitInfo().Initialized, SpecKitSet: h.SpecKitInfo(), Projects: h.projectsOrNil()}, nil
}

func (h *Hub) need() (string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.root == "" {
		return "", errors.New("open a workspace first")
	}
	return h.root, nil
}

func (h *Hub) onFiles(chs []watch.Change) {
	h.mu.RLock()
	r := h.runner
	h.mu.RUnlock()
	var free []watch.Change // changes no step run made
	for _, c := range chs {
		if r != nil && r.FileChanged(c) {
			continue
		}
		free = append(free, c)
	}
	h.Bus.Publish(bus.Event{Type: "workspace.files", Summary: fmt.Sprintf("%d file(s) changed", len(chs)), Data: map[string]any{"changes": chs, "unattributed": len(free)}})
	h.mu.RLock()
	root := h.root
	h.mu.RUnlock()
	h.noteFileChanges(root, free) // "files" triggers (triggers.go)
}

// SaveProfile stores the wizard profile.
func (h *Hub) SaveProfile(p workflow.Profile) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	if err := workflow.SaveProfile(root, &p); err != nil {
		return err
	}
	h.mu.Lock()
	h.profile = &p
	h.mu.Unlock()
	return nil
}

// BuildRules materializes one wizard compartment on disk: the rules file,
// the folder layout, or the selected skills index. Incremental writes only
// what changed; bulk rewrites everything.
func (h *Hub) BuildRules(section, mode string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	h.mu.RLock()
	p := *h.profile
	h.mu.RUnlock()
	bulk := mode == "bulk"
	written, same := 0, 0
	write := func(rel, content string) error {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if old, err := os.ReadFile(abs); err == nil && string(old) == content && !bulk {
			same++
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		written++
		return os.WriteFile(abs, []byte(content), 0o644)
	}
	rulesDir := ".codegen/rules"
	for _, f := range p.Folders {
		if strings.HasSuffix(f.Path, "rules") {
			rulesDir = f.Path
		}
	}
	switch section {
	case "wizard.foldersMode", "folders":
		for _, f := range p.Folders {
			if err := write(f.Path+"/.keep", ""); err != nil {
				return "", err
			}
		}
	case "wizard.globalMode", "wizard.projectMode", "skills":
		libs := h.libsFor(root, &p)
		var b strings.Builder
		b.WriteString("# Selected skills and scripts\n\n")
		for _, l := range libs {
			for _, id := range append(append([]string{}, p.Global...), p.Project...) {
				if id == l.ID {
					fmt.Fprintf(&b, "- **%s** (%s, %s): %s\n", l.Name, l.Kind, l.Scope, l.Path)
					for _, f := range l.Files {
						fmt.Fprintf(&b, "  - %s/%s\n", l.Dir, f)
					}
				}
			}
		}
		if err := write(rulesDir+"/skills.md", b.String()); err != nil {
			return "", err
		}
	}
	if err := write(rulesDir+"/rules.md", workflow.RulesMarkdown(p.Rules)); err != nil {
		return "", err
	}
	if bulk {
		return fmt.Sprintf("Bulk load: rewrote %d file(s) in %s.", written, rulesDir), nil
	}
	return fmt.Sprintf("Incremental: %d file(s) updated, %d unchanged.", written, same), nil
}

// SaveRulesDoc writes the rules document the bulk editor produced into the
// rules compartment: rules.md (Markdown) or rules.html (rich text). The
// profile keeps the rules themselves; this is the readable/shareable copy.
func (h *Hub) SaveRulesDoc(format, content string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	name := map[string]string{"md": "rules.md", "markdown": "rules.md", "html": "rules.html", "rich": "rules.html"}[format]
	if name == "" {
		return "", fmt.Errorf("unknown rules format %q (want md or html)", format)
	}
	if len(content) > 4<<20 {
		return "", errors.New("the rules document is too large")
	}
	h.mu.RLock()
	rulesDir := ".codegen/rules"
	for _, f := range h.profile.Folders {
		if strings.HasSuffix(f.Path, "rules") {
			rulesDir = f.Path
		}
	}
	h.mu.RUnlock()
	rel := strings.TrimSuffix(filepath.ToSlash(rulesDir), "/") + "/" + name
	abs, err := h.inside(root, rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, []byte(content), 0o644)
}

// ListFiles lists workspace files (relative, slash-separated).
func (h *Hub) ListFiles() ([]string, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	fs := watch.ListFiles(root, 5000)
	sort.Strings(fs)
	return fs, nil
}

// ReadFile returns a workspace file's text (max 2 MiB), refusing paths
// that escape the workspace.
func (h *Hub) ReadFile(rel string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	abs, err := h.inside(root, rel)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if fi.Size() > 2<<20 {
		return "", fmt.Errorf("%s is too large to show (%d KB)", rel, fi.Size()/1024)
	}
	b, err := os.ReadFile(abs)
	return string(b), err
}

func (h *Hub) inside(root, rel string) (string, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the workspace", rel)
	}
	return abs, nil
}

// SetDiagram records an input diagram path (or "" to remove).
func (h *Hub) SetDiagram(kind, rel string) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	if rel != "" {
		if _, err := h.inside(root, rel); err != nil {
			return err
		}
	}
	h.mu.Lock()
	p := *h.profile
	p.Diagrams = map[string]string{}
	for k, v := range h.profile.Diagrams {
		p.Diagrams[k] = v
	}
	if rel == "" {
		delete(p.Diagrams, kind)
	} else {
		p.Diagrams[kind] = rel
	}
	h.mu.Unlock()
	return h.SaveProfile(p)
}

// ImportDiagram copies a file the user picked into <workspace>/inputs/.
func (h *Hub) ImportDiagram(kind, src string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	rel := "inputs/" + kind + "-diagram" + filepath.Ext(src)
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, b, 0o644); err != nil {
		return "", err
	}
	return rel, h.SetDiagram(kind, rel)
}

// Workflow returns the open workflow.
func (h *Hub) Workflow() (*workflow.Workflow, error) {
	if _, err := h.need(); err != nil {
		return nil, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	cp := *h.wf
	cp.Steps = append([]workflow.Step(nil), h.wf.Steps...)
	return &cp, nil
}

// SaveWorkflow stores the user-editable parts of each step (inputs,
// instructions, options, feedback, gate, retries) and the budget. Status
// stays under the runner's control.
func (h *Hub) SaveWorkflow(in workflow.Workflow) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range in.Steps {
		i := h.wf.Index(s.ID)
		if i < 0 {
			continue
		}
		d := &h.wf.Steps[i]
		d.Input, d.Instructions, d.Options, d.Feedback = s.Input, s.Instructions, s.Options, s.Feedback
		d.Gate, d.Retries = s.Gate, max(0, min(s.Retries, 5))
	}
	h.wf.Budget = in.Budget
	h.wf.Model = strings.TrimSpace(in.Model) // "" = the app-wide model
	h.wf.Context = in.Context

	return workflow.Save(root, h.wf)
}

// ResetWorkflow sets every step back to pending (keeps inputs).
func (h *Hub) ResetWorkflow() error {
	root, err := h.need()
	if err != nil {
		return err
	}
	if h.activeRun() != nil {
		return errors.New("a step is still running")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.wf.Steps {
		h.wf.Steps[i].Status = "pending"
	}
	h.Bus.Publish(bus.Event{Type: "workflow.changed"})
	return workflow.Save(root, h.wf)
}

func (h *Hub) onStepStatus(stepID, status, runID string) {
	if stepID == PlaygroundStep {
		h.Bus.Publish(bus.Event{Type: "playground.changed", RunID: runID, Step: stepID, Summary: status, Data: map[string]any{"status": status}})
		return
	}
	h.mu.Lock()
	root := h.root
	if i := h.wf.Index(stepID); i >= 0 {
		h.wf.Steps[i].Status = status
		h.wf.Steps[i].LastRun = runID
		_ = workflow.Save(root, h.wf)
	}
	h.mu.Unlock()
	h.Bus.Publish(bus.Event{Type: "workflow.changed", RunID: runID, Step: stepID, Summary: status, Data: map[string]any{"status": status}})
}

// emitRun records e as an event of the run a, with the run's next sequence
// number: the live view keeps a run's events by number, so an event without
// one (0) was dropped once the run had another.
func (h *Hub) emitRun(a *workflow.Run, e bus.Event) {
	h.mu.RLock()
	r := h.runner
	h.mu.RUnlock()
	if r == nil {
		e.RunID, e.Step = a.Session.ID, a.Session.Step
		_ = h.Store.Append(h.Bus.Publish(e))
		return
	}
	r.Emit(a, e.Type, e.Summary, e.Data)
}

func (h *Hub) activeRun() *workflow.Run {
	h.mu.RLock()
	r := h.runner
	h.mu.RUnlock()
	if r == nil {
		return nil
	}
	if a := r.Active(); a != nil && !a.Session.State().Terminal() {
		return a
	}
	return nil
}

// Compose returns the prompt step stepID would be sent with.
func (h *Hub) Compose(stepID, feedback string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	i := h.wf.Index(stepID)
	if i < 0 {
		return "", fmt.Errorf("no step %q", stepID)
	}
	// Spec Kit steps run the project's installed command and its guide
	w := *h.wf
	w.Steps = slices.Clone(h.wf.Steps)
	project := h.profile.SpecProject
	dir := root
	if project != "" {
		dir = filepath.Join(root, filepath.FromSlash(project))
	}
	specKitStep(dir, speckit.Status(dir).Integration, project, &w.Steps[i])
	libs, prof := rebased(project, h.libsFor(root, h.profile), h.profile)
	return workflow.Compose(&w, i, prof, libs, h.toolRefs(), feedback), nil
}

func (h *Hub) toolRefs() []workflow.ToolRef {
	var out []workflow.ToolRef
	for _, e := range h.callableLocked() {
		var sec []string
		for _, s := range e.Secrets {
			sec = append(sec, s.Name)
		}
		out = append(out, workflow.ToolRef{Name: e.Name, Desc: e.Desc, Secrets: sec})
	}
	// built-ins: Compose lists the ones chosen on Choose input
	for _, t := range h.FlowBuiltinTools() {
		_, desc, _ := strings.Cut(t.Desc, ": ") // drop the "Title: " the designer shows
		out = append(out, workflow.ToolRef{Name: t.Name, Desc: desc})
	}
	return out
}

// RunStep starts step stepID with the chosen agent and returns the run id.
func (h *Hub) RunStep(stepID string) (string, error) {
	if _, err := h.need(); err != nil {
		return "", err
	}
	h.prepareLibraries()
	h.mu.RLock()
	i := h.wf.Index(stepID)
	if i < 0 {
		h.mu.RUnlock()
		return "", fmt.Errorf("no step %q", stepID)
	}
	if why := h.wf.CanRun(i); why != "" {
		h.mu.RUnlock()
		return "", fmt.Errorf("cannot run %s: %s", h.wf.Steps[i].Title, why)
	}
	specRun := strings.HasPrefix(h.wf.Steps[i].Cmd, "/speckit")
	h.mu.RUnlock()
	dir := h.specDir()
	if specRun && !speckit.Status(dir).Initialized {
		return "", errors.New("this project is not set up for Spec Kit yet: use Set up Spec Kit on Choose input first")
	}
	h.mu.RLock()
	st := h.wf.Steps[i]
	o := workflow.StartOptions{Agent: h.agentName, Model: firstNonEmpty(h.wf.Model, h.model), Budget: h.wf.Budget, Retries: st.Retries, Gate: st.Gate,
		Title: st.Title, Ask: strings.TrimSpace(st.Input)}
	if specRun && dir != h.root { // the agent works in the project folder and may read the workspace
		o.Dir, o.AddDirs = dir, []string{h.root}
	}
	runner := h.runner
	h.mu.RUnlock()
	_, _ = h.writeAgentContext() // the agent's instruction file follows Choose input
	prompt, err := h.Compose(stepID, "")
	if err != nil {
		return "", err
	}
	o.Prompt = prompt
	o.Recompose = func(fb string) string {
		p, _ := h.Compose(stepID, fb)
		return p
	}
	run, err := runner.Start(h.ctx, h.wf.ID, stepID, o)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	h.runs[run.Session.ID] = run
	h.mu.Unlock()
	go func() {
		<-run.Done()
		h.mu.Lock()
		delete(h.runs, run.Session.ID)
		h.mu.Unlock()
	}()
	return run.Session.ID, nil
}

// PlaygroundStep is the step id (and workflow id) of playground runs.
const PlaygroundStep = "playground"

// PromptOptions tune one playground run.
type PromptOptions struct {
	Model string `json:"model,omitempty"` // overrides the agent's model for this run
	Tools bool   `json:"tools"`           // offer the project's approved tools (extension_managed)
	// Parents are earlier runs (any kind) this prompt continues from: their
	// questions, answers and results folders are added as context.
	Parents []string `json:"parents,omitempty"`
	// Title and Ask describe the run in history when the prompt is composed
	// by the app (Files › Ask AI); default: the prompt itself.
	Title string `json:"title,omitempty"`
	Ask   string `json:"ask,omitempty"`
	// Interactive: you answer the agent's permission questions in the app
	// (on by default in the Playground): Copilot asks before editing files
	// outside the project or running commands, instead of refusing them.
	Interactive bool `json:"interactive,omitempty"`
}

// RunPrompt sends a free-form prompt to the chosen agent in the workspace
// and returns the run id. It goes through the same runner as workflow
// steps (streamed events, budgets, loop detection, history) without an
// approval gate.
func (h *Hub) RunPrompt(prompt string, o PromptOptions) (string, error) {
	if _, err := h.need(); err != nil {
		return "", err
	}
	h.prepareLibraries()
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("type a prompt first")
	}
	ask := firstNonEmpty(o.Ask, strings.TrimSpace(prompt))
	if len(o.Parents) > 0 {
		ctx, err := h.continuationContext(o.Parents)
		if err != nil {
			return "", err
		}
		prompt = ctx + prompt
	}
	h.mu.RLock()
	so := workflow.StartOptions{Agent: h.agentName, Model: firstNonEmpty(o.Model, h.model, h.wf.Model), Budget: h.wf.Budget, Prompt: prompt, NoTools: !o.Tools,
		Title: firstNonEmpty(o.Title, firstLine(ask)), Ask: ask, Parents: o.Parents, Interactive: o.Interactive}
	runner := h.runner
	h.mu.RUnlock()
	run, err := runner.Start(h.ctx, PlaygroundStep, PlaygroundStep, so)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	h.runs[run.Session.ID] = run
	h.mu.Unlock()
	go func() {
		<-run.Done()
		h.mu.Lock()
		delete(h.runs, run.Session.ID)
		h.mu.Unlock()
	}()
	return run.Session.ID, nil
}

// Decide answers the approval gate of a paused run: approve, changes or reject.
func (h *Hub) Decide(runID, kind, feedback string) error {
	switch kind {
	case "approve", "changes", "reject":
	default:
		return fmt.Errorf("unknown decision %q", kind)
	}
	h.mu.RLock()
	run := h.runs[runID]
	h.mu.RUnlock()
	if run == nil {
		return fmt.Errorf("run %s is not live (it may have been interrupted)", runID)
	}
	return run.Session.Decide(session.Decision{Kind: kind, Feedback: feedback})
}

// CancelRun stops a live run.
func (h *Hub) CancelRun(runID string) error {
	h.mu.RLock()
	run := h.runs[runID]
	h.mu.RUnlock()
	if run == nil {
		return fmt.Errorf("run %s is not live", runID)
	}
	run.Cancel()
	return nil
}

// LiveRun describes the run in progress, if any.
type LiveRun struct {
	ID    string      `json:"id"`
	Step  string      `json:"step"`
	State string      `json:"state"`
	Gate  string      `json:"gate,omitempty"`
	Usage store.Usage `json:"usage"`
}

// Live returns the current run or nil.
func (h *Hub) Live() *LiveRun {
	a := h.activeRun()
	if a == nil {
		return nil
	}
	return &LiveRun{ID: a.Session.ID, Step: a.Session.Step, State: string(a.Session.State()), Gate: a.Session.GateKind(), Usage: a.Usage()}
}

// Runs lists run history for the open workspace (all when none is open).
func (h *Hub) Runs(limit int) ([]store.Run, error) {
	h.mu.RLock()
	root := h.root
	h.mu.RUnlock()
	return h.Store.Runs(root, limit)
}

// RunEvents returns the recorded events of a run (for replay and debug).
func (h *Hub) RunEvents(runID string) ([]bus.Event, error) { return h.Store.Events(runID) }

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// UseWorkflow replaces the open workflow (e.g. one loaded from a file by
// the headless runner) and saves it into the workspace.
func (h *Hub) UseWorkflow(w *workflow.Workflow) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	if h.activeRun() != nil {
		return errors.New("a step is still running")
	}
	h.mu.Lock()
	h.wf = w
	h.mu.Unlock()
	return workflow.Save(root, w)
}

// waitTimeout waits for wg, at most d.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}
