package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/store"
)

// CredentialSource is one place an agent CLI can find credentials. Only
// presence is checked: the app never reads or stores token values.
type CredentialSource struct {
	ID       string `json:"id"`
	Provider string `json:"provider"` // claude | copilot
	Label    string `json:"label"`
	Where    string `json:"where"`
	Found    bool   `json:"found"`
	Who      string `json:"who,omitempty"`
	Warn     string `json:"warn,omitempty"`
}

// DetectCredentials looks for credentials the CLIs would use, in their
// order of precedence.
func (h *Hub) DetectCredentials() []CredentialSource {
	home, _ := os.UserHomeDir()
	env := func(names ...string) (bool, string) {
		for _, n := range names {
			if os.Getenv(n) != "" {
				return true, n + " is set"
			}
		}
		return false, ""
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	var out []CredentialSource

	f, who := env("COPILOT_GITHUB_TOKEN")
	out = append(out, CredentialSource{ID: "env-copilot", Provider: "copilot", Label: "COPILOT_GITHUB_TOKEN", Where: "environment variable", Found: f, Who: who})
	f, who = env("GH_TOKEN", "GITHUB_TOKEN")
	out = append(out, CredentialSource{ID: "env-gh", Provider: "copilot", Label: "GH_TOKEN / GITHUB_TOKEN", Where: "environment variable", Found: f, Who: who})
	cp := filepath.Join(home, ".copilot", "config.json")
	out = append(out, CredentialSource{ID: "copilot-cli", Provider: "copilot", Label: "Copilot CLI login", Where: "~/.copilot/config.json · keychain", Found: exists(cp), Who: map[bool]string{true: "signed in with `copilot`"}[exists(cp)]})
	gh := CredentialSource{ID: "gh-cli", Provider: "copilot", Label: "GitHub CLI", Where: "gh auth token · github.com"}
	if p, err := exec.LookPath("gh"); err == nil {
		if o, err := exec.CommandContext(ctx, p, "auth", "status", "--hostname", "github.com").CombinedOutput(); err == nil {
			gh.Found = true
			gh.Who = ghAccount(string(o))
			gh.Warn = "OAuth token: works for Copilot only if your org allows the GitHub CLI app"
		}
	}
	out = append(out, gh)

	f, who = env("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN")
	out = append(out, CredentialSource{ID: "env-anthropic", Provider: "claude", Label: "ANTHROPIC_API_KEY / CLAUDE_CODE_OAUTH_TOKEN", Where: "environment variable", Found: f, Who: who})
	cl := CredentialSource{ID: "claude-cli", Provider: "claude", Label: "Claude Code login", Where: "~/.claude · keychain"}
	if exists(filepath.Join(home, ".claude", ".credentials.json")) {
		cl.Found, cl.Who = true, "signed in with `claude`"
	} else if runtime.GOOS == "darwin" {
		// presence check only: without -w the password is not printed
		if exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials").Run() == nil {
			cl.Found, cl.Who = true, "signed in with `claude` (keychain)"
		}
	}
	out = append(out, cl)
	return out
}

func ghAccount(s string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if i := strings.Index(l, "account "); i >= 0 {
			return "signed in as " + strings.Fields(l[i+8:])[0]
		}
	}
	return "signed in"
}

// HoursSaved estimates human hours saved per approved step.
var HoursSaved = map[string]float64{"constitution": 0.5, "specify": 3, "clarify": 1.5, "plan": 4, "tasks": 2, "implement": 14}

// Metrics summarizes the usage ledger.
type Metrics struct {
	Project struct {
		FilesCreated int         `json:"filesCreated"`
		Files        int         `json:"files"`
		Rules        int         `json:"rules"`
		HoursSaved   float64     `json:"hoursSaved"`
		Usage        store.Usage `json:"usage"`
	} `json:"project"`
	AllTime struct {
		TimesUsed  int         `json:"timesUsed"`
		HoursSaved float64     `json:"hoursSaved"`
		Usage      store.Usage `json:"usage"`
	} `json:"allTime"`
	PerStep []StepHours `json:"perStep"`
	Recent  []RecentRun `json:"recent"`
	Flows   FlowMetrics `json:"flows"`
}

// FlowMetrics summarizes flow runs (all projects, plus this one).
type FlowMetrics struct {
	Executed       int          `json:"executed"`
	Completed      int          `json:"completed"`
	Failed         int          `json:"failed"`
	Stopped        int          `json:"stopped"`
	ToolExecutions int          `json:"toolExecutions"`
	AICalls        int          `json:"aiCalls"`
	HoursSaved     float64      `json:"hoursSaved"`
	Usage          store.Usage  `json:"usage"`
	ProjectRuns    int          `json:"projectRuns"`
	ProjectHours   float64      `json:"projectHours"`
	Top            []FlowTotals `json:"top"`
}

// FlowTotals is one flow's line in Metrics.
type FlowTotals struct {
	Flow       string    `json:"flow"`
	Runs       int       `json:"runs"`
	Completed  int       `json:"completed"`
	Tools      int       `json:"tools"`
	HoursSaved float64   `json:"hoursSaved"`
	CostUSD    float64   `json:"costUsd"`
	Premium    int       `json:"premium"`
	Last       time.Time `json:"last"`
}

// StepHours is one bar of "Hours saved by step".
type StepHours struct {
	Step  string  `json:"step"`
	Hours float64 `json:"hours"`
}

// RecentRun is one row of "Recent runs" (one workspace's workflow).
type RecentRun struct {
	Name     string    `json:"name"`
	At       time.Time `json:"at"`
	Approved int       `json:"approved"`
	Steps    int       `json:"steps"`
	Files    int       `json:"files"`
	Hours    float64   `json:"hours"`
	Status   string    `json:"status"`
}

// Metrics computes project and all-time metrics from the ledger.
func (h *Hub) Metrics() (Metrics, error) {
	var m Metrics
	ents, err := h.Store.LedgerEntries()
	if err != nil {
		return m, err
	}
	h.mu.RLock()
	root := h.root
	if h.profile != nil {
		m.Project.Rules = len(h.profile.Rules)
	}
	h.mu.RUnlock()
	if root != "" {
		m.Project.Files = len(mustList(h.ListFiles()))
	}
	type key struct{ ws, wf string }
	groups := map[key]*RecentRun{}
	approved := map[key]map[string]bool{}
	flowTop := map[string]*FlowTotals{}
	for _, e := range ents {
		m.AllTime.Usage.Add(e.Usage)
		if id, ok := strings.CutPrefix(e.Workflow, "flow:"); ok {
			fm := &m.Flows
			fm.Executed++
			switch e.State {
			case "done":
				fm.Completed++
			case "stopped":
				fm.Stopped++
			default:
				fm.Failed++
			}
			fm.ToolExecutions += e.ToolCalls
			fm.AICalls += e.ModelCalls
			hours := float64(e.MinutesSaved) / 60
			fm.HoursSaved += hours
			m.AllTime.HoursSaved += hours
			fm.Usage.Add(e.Usage)
			if e.Workspace == root {
				fm.ProjectRuns++
				fm.ProjectHours += hours
				m.Project.Usage.Add(e.Usage)
				m.Project.HoursSaved += hours
			}
			t := flowTop[id]
			if t == nil {
				t = &FlowTotals{Flow: id}
				flowTop[id] = t
			}
			t.Runs++
			if e.State == "done" {
				t.Completed++
			}
			t.Tools += e.ToolCalls
			t.HoursSaved += hours
			t.CostUSD += e.CostUSD
			t.Premium += e.PremiumRequests
			if e.At.After(t.Last) {
				t.Last = e.At
			}
			continue
		}
		if e.Workflow == PlaygroundStep { // counts toward usage, not toward workflow progress
			if e.Workspace == root {
				m.Project.Usage.Add(e.Usage)
			}
			continue
		}
		k := key{e.Workspace, e.Workflow}
		g := groups[k]
		if g == nil {
			g = &RecentRun{Name: filepath.Base(e.Workspace), Steps: 6}
			groups[k] = g
			approved[k] = map[string]bool{}
		}
		if e.At.After(g.At) {
			g.At = e.At
			g.Status = e.State
		}
		g.Files += e.FilesCreated
		if e.State == "done" && !approved[k][e.Step] {
			approved[k][e.Step] = true
			g.Approved++
			g.Hours += HoursSaved[e.Step]
			m.AllTime.HoursSaved += HoursSaved[e.Step]
			if e.Step == "implement" {
				m.AllTime.TimesUsed++
			}
		}
		if e.Workspace == root {
			m.Project.Usage.Add(e.Usage)
			m.Project.FilesCreated += e.FilesCreated
		}
	}
	for _, t := range flowTop {
		m.Flows.Top = append(m.Flows.Top, *t)
	}
	sort.Slice(m.Flows.Top, func(i, j int) bool { return m.Flows.Top[i].Runs > m.Flows.Top[j].Runs })
	if len(m.Flows.Top) > 8 {
		m.Flows.Top = m.Flows.Top[:8]
	}
	for k, g := range groups {
		if k.ws == root {
			m.Project.HoursSaved += g.Hours
		}
		if g.Approved == g.Steps {
			g.Status = "done"
		} else if g.Status == "done" {
			g.Status = "paused"
		}
		m.Recent = append(m.Recent, *g)
	}
	sort.Slice(m.Recent, func(i, j int) bool { return m.Recent[i].At.After(m.Recent[j].At) })
	if len(m.Recent) > 10 {
		m.Recent = m.Recent[:10]
	}
	for _, s := range []string{"constitution", "specify", "clarify", "plan", "tasks", "implement"} {
		m.PerStep = append(m.PerStep, StepHours{Step: s, Hours: HoursSaved[s]})
	}
	return m, nil
}

func mustList(s []string, _ error) []string { return s }

// BuildResult is the outcome of Compile.
type BuildResult struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

// BuildCommand is the compile command for the profile's build tool.
func BuildCommand(build, root string) []string {
	wrap := func(w, fallback string, args ...string) []string {
		if _, err := os.Stat(filepath.Join(root, w)); err == nil {
			if runtime.GOOS == "windows" {
				return append([]string{w + ".cmd"}, args...)
			}
			return append([]string{"./" + w}, args...)
		}
		return append([]string{fallback}, args...)
	}
	switch build {
	case "Maven":
		return wrap("mvnw", "mvn", "-q", "-B", "compile")
	case "Gradle":
		return wrap("gradlew", "gradle", "build", "-x", "test")
	case "npm":
		return []string{"npm", "run", "build", "--if-present"}
	case "pnpm":
		return []string{"pnpm", "run", "--if-present", "build"}
	case "uv":
		return []string{"uv", "run", "python", "-m", "compileall", "-q", "."}
	case "pip":
		return []string{"python3", "-m", "compileall", "-q", "."}
	case "dotnet":
		return []string{"dotnet", "build", "--nologo"}
	case "go":
		return []string{"go", "build", "./..."}
	}
	return nil
}

// Compile runs the profile's build command in the workspace, streaming
// each output line as a build.line event.
func (h *Hub) Compile() (BuildResult, error) {
	root, err := h.need()
	if err != nil {
		return BuildResult{}, err
	}
	h.mu.RLock()
	argv := BuildCommand(h.profile.Build, root)
	h.mu.RUnlock()
	if argv == nil {
		return BuildResult{}, errors.New("no build command for this build tool")
	}
	res := BuildResult{Command: strings.Join(argv, " ")}
	if _, err := exec.LookPath(argv[0]); err != nil && !strings.HasPrefix(argv[0], "./") {
		return res, fmt.Errorf("%s is not installed or not on PATH", argv[0])
	}
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), h.agentPackageEnv(root)...)
	pr, pw, _ := os.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	h.Bus.Publish(bus.Event{Type: "build.start", Summary: res.Command})
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return res, err
	}
	pw.Close()
	var b strings.Builder
	sc := bufio.NewScanner(pr)
	for sc.Scan() {
		l := sc.Text()
		b.WriteString(l + "\n")
		h.Bus.Publish(bus.Event{Type: "build.line", Summary: l})
	}
	pr.Close()
	err = cmd.Wait()
	res.OK = err == nil
	res.Output = strings.TrimRight(b.String(), "\n")
	if res.OK && res.Output == "" {
		res.Output = "BUILD SUCCESS (no output)"
	}
	h.Bus.Publish(bus.Event{Type: "build.end", Summary: map[bool]string{true: "ok", false: "failed"}[res.OK], Data: map[string]any{"ok": res.OK}})
	return res, nil
}
