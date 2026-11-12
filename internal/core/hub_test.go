package core

import (
	"bytes"
	"codegen/internal/speckit"
	"codegen/internal/workflow"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
	"codegen/internal/extension"
)

// nastyName has spaces, both quote kinds, $, &, #, parentheses and unicode.
const nastyName = `Application Support (v2) & O'Brien's "draft" $HOME #1 ünï`

func newHub(t *testing.T) (*Hub, string) {
	t.Helper()
	// awkward but legal folder names, like ~/Library/Application Support or
	// a project called "Orders (v2) & O'Brien's \"draft\""
	h, err := New(Options{StoreDir: filepath.Join(t.TempDir(), nastyName), Agent: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	m := agent.NewMock()
	m.Delay = 5 * time.Millisecond
	h.Agents.Add(m)
	ws := filepath.Join(t.TempDir(), nastyName+" ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.OpenWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	return h, ws
}

func setInput(t *testing.T, h *Hub, step, text string) {
	wf, _ := h.Workflow()
	wf.Steps[wf.Index(step)].Input = text
	if err := h.SaveWorkflow(*wf); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, ch <-chan bus.Event, runID, typ string) bus.Event {
	t.Helper()
	to := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.RunID == runID && e.Type == typ {
				return e
			}
		case <-to:
			t.Fatalf("timed out waiting for %s", typ)
		}
	}
}

func TestStepGateChangesApprove(t *testing.T) {
	h, _ := newHub(t)
	specKitReady(t, h)
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	if _, err := h.RunStep("specify"); err == nil {
		t.Fatal("specify must be locked until constitution is approved")
	}
	setInput(t, h, "constitution", "tests everywhere")
	id, err := h.RunStep("constitution")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, id, "gate.wait")
	if err := h.Decide(id, "changes", "add a security principle"); err != nil {
		t.Fatal(err)
	}
	e := waitFor(t, ch, id, "prompt.compose")
	if p := e.Str("prompt"); !contains(p, "add a security principle") {
		t.Fatalf("feedback not in prompt: %s", p)
	}
	waitFor(t, ch, id, "gate.wait")
	if err := h.Decide(id, "approve", ""); err != nil {
		t.Fatal(err)
	}
	end := waitFor(t, ch, id, "run.end")
	if end.Str("state") != "done" {
		t.Fatalf("state %s", end.Str("state"))
	}
	time.Sleep(50 * time.Millisecond)
	wf, _ := h.Workflow()
	if wf.Steps[0].Status != "done" {
		t.Fatalf("status %s", wf.Steps[0].Status)
	}
	evs, _ := h.RunEvents(id)
	sawFile := false
	for _, e := range evs {
		// the template's blank constitution is filled in: an edit (fs.write if it was missing)
		if (e.Type == "fs.write" || e.Type == "fs.edit") && e.Str("path") == ".specify/memory/constitution.md" {
			sawFile = true
		}
	}
	if !sawFile {
		t.Fatal("watcher did not attribute the constitution write to the run")
	}
	m, _ := h.Metrics()
	if m.AllTime.HoursSaved != HoursSaved["constitution"] {
		t.Fatalf("metrics %+v", m.AllTime)
	}
}

func TestLoopPausesRun(t *testing.T) {
	h, _ := newHub(t)
	specKitReady(t, h)
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	setInput(t, h, "constitution", "[mock:loop]")
	id, err := h.RunStep("constitution")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, id, "loop.detected")
	g := waitFor(t, ch, id, "gate.wait")
	if g.Str("gate") != "loop" {
		t.Fatalf("gate %v", g.Data)
	}
	_ = h.Decide(id, "reject", "")
	if e := waitFor(t, ch, id, "run.end"); e.Str("state") != "failed" {
		t.Fatal(e.Data)
	}
}

func TestBudgetStopsRun(t *testing.T) {
	h, _ := newHub(t)
	specKitReady(t, h)
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	setInput(t, h, "constitution", "x")
	wf, _ := h.Workflow()
	wf.Budget.MaxTokens = 100
	_ = h.SaveWorkflow(*wf)
	id, _ := h.RunStep("constitution")
	waitFor(t, ch, id, "budget.exceeded")
	if e := waitFor(t, ch, id, "run.end"); e.Str("state") != "failed" {
		t.Fatal(e.Data)
	}
}

func TestGenerateAndTestExtension(t *testing.T) {
	h, _ := newHub(t)
	d, err := h.GenerateExtension(GenerateRequest{Prompt: "Find every TODO and FIXME in a package", RunsIn: "Auto"})
	if err != nil || d.Name != "find-todos" {
		t.Fatalf("%+v %v", d, err)
	}
	tr, err := h.TestExtension(d, "List the TODOs")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Pass {
		t.Fatalf("test failed: %+v", tr)
	}
	saved, err := h.SaveExtension(d)
	if err != nil || saved.Runnable() != nil {
		t.Fatalf("save %v %v", err, saved.Runnable())
	}
	if _, err := h.SaveExtension(extension.Extension{Name: "g", Scope: "global", Command: "true"}); err == nil {
		t.Fatal("untested global tool must be refused")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestAutoRuntimePriority(t *testing.T) {
	got := Runtimes()
	order := map[string]int{}
	for i, r := range RuntimePriority {
		order[r.Lang] = i
	}
	for i := 1; i < len(got); i++ {
		if order[got[i-1]] > order[got[i]] {
			t.Fatalf("runtimes out of priority order: %v", got)
		}
	}
	lang, _ := pickRuntime("Auto")
	if len(got) > 0 && lang != got[0] {
		t.Fatalf("Auto picked %s, want first installed %s", lang, got[0])
	}
	if l, _ := pickRuntime("Java"); l != "Java" {
		t.Fatalf("explicit choice must win, got %s", l)
	}
}

func TestFixFailedTool(t *testing.T) {
	h, _ := newHub(t)
	lang, _ := pickRuntime("Auto")
	broken := extension.Extension{Name: "find-todos", Desc: "List TODO and FIXME comments under a path.", Kind: "script", Lang: "Bash",
		Params: []extension.Param{{Name: "path", Type: "string", Required: true}}, Call: map[string]any{"path": "."},
		Command: "bash .codegen/tools/find-todos.sh {{path}}", Script: "#!/usr/bin/env bash\nexit 3\n", Revision: 1}
	tr, err := h.TestExtension(broken, "List the TODOs")
	if err != nil || tr.Pass {
		t.Fatalf("broken tool must fail: %+v %v", tr, err)
	}
	fixed, err := h.GenerateExtension(GenerateRequest{Draft: &broken, Failure: &tr, Question: "List the TODOs"})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Name != "find-todos" || fixed.Revision != 2 {
		t.Fatalf("fix must keep the tool: %+v", fixed)
	}
	tr2, err := h.TestExtension(fixed, "List the TODOs")
	if err != nil || !tr2.Pass {
		t.Fatalf("fixed tool should pass (lang %s): %+v %v", lang, tr2, err)
	}
	if !strings.Contains(fixRequest(GenerateRequest{Failure: &tr, Question: "q"}), "Exit code 0") {
		t.Fatal("fix request must name the failed checks")
	}
}

// quietAgent calls the bridge like a real CLI would but, like Copilot's text
// output, never reports the MCP call as a tool.call event.
type quietAgent struct{ *agent.Mock }

func (quietAgent) Name() string { return "quiet" }
func (q quietAgent) Run(ctx context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	return q.Mock.Run(ctx, req, func(e agent.Event) {
		if e.Type != "tool.call" {
			emit(e)
		}
	})
}

func TestToolTestWithSpacesAndQuietAgent(t *testing.T) {
	h, _ := newHub(t)
	m := agent.NewMock()
	m.Delay = time.Millisecond
	h.Agents.Add(quietAgent{m})
	if err := h.SetAgent("quiet", "", ""); err != nil {
		t.Fatal(err)
	}
	sql := "SELECT table_name FROM information_schema.columns WHERE table_name ILIKE '%user%'"
	d := extension.Extension{Name: "echo-sql", Desc: "Echo a query.", Kind: "script", Lang: "Bash",
		Params:  []extension.Param{{Name: "sql", Type: "string", Required: true}, {Name: "limit", Type: "number"}},
		Call:    map[string]any{"sql": sql, "limit": 200},
		Command: "bash .codegen/tools/echo-sql.sh {{sql}} {{limit}}", Script: "#!/usr/bin/env bash\necho \"$1|$2\"\n"}
	tr, err := h.TestExtension(d, "Which tables mention users?")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Pass || tr.Output != sql+"|200" {
		t.Fatalf("want pass with output %q, got %+v", sql+"|200", tr)
	}
	for _, s := range tr.Steps {
		if s.K == "run" && strings.Contains(s.V, "Application Support") {
			t.Fatalf("the temp path must not be shown: %s", s.V)
		}
	}
}

func TestToolWithDepsTestSaveAndRun(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}
	h, ws := newHub(t)
	lib := filepath.Join(t.TempDir(), "mylib")
	_ = os.MkdirAll(lib, 0o755)
	_ = os.WriteFile(filepath.Join(lib, "package.json"), []byte(`{"name":"mylib","version":"1.0.0","main":"index.js"}`), 0o644)
	_ = os.WriteFile(filepath.Join(lib, "index.js"), []byte(`exports.rows = (q) => "rows for " + q;`), 0o644)
	d := extension.Extension{Name: "db-query", Desc: "Run a read-only query.", Kind: "script", Lang: "Node.js",
		Params: []extension.Param{{Name: "sql", Type: "string", Required: true}}, Call: map[string]any{"sql": "SELECT 1"},
		Deps: []string{"mylib@file:" + lib}, Script: `console.log(require("mylib").rows(process.argv[2]));`}
	tr, err := h.TestExtension(d, "Run SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Pass || tr.Output != "rows for SELECT 1" || tr.Checks[0].Label != "Dependencies installed" {
		t.Fatalf("%+v", tr)
	}
	d.LastTest = &tr
	saved, err := h.SaveExtension(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".codegen/tools/project/db-query/node_modules/mylib")); err != nil {
		t.Fatal("save must install the dependencies into the tool folder")
	}
	// a fresh clone has no node_modules: the bridge installs on first call
	_ = os.RemoveAll(filepath.Join(ws, ".codegen/tools/project/db-query/node_modules"))
	_ = os.Remove(filepath.Join(ws, ".codegen/tools/project/db-query/.codegen-installed"))
	body, _ := json.Marshal(map[string]any{"name": saved.Name, "arguments": map[string]any{"sql": "SELECT 2"}})
	req, _ := http.NewRequest("POST", h.Bridge.URL()+"/call", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.Bridge.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res extension.RunResult
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if !res.OK || res.Output != "rows for SELECT 2" {
		t.Fatalf("bridge call after a fresh clone: %+v", res)
	}
}

func TestToolPathsWithSpecialCharacters(t *testing.T) {
	h, ws := newHub(t)
	// a file the tool must read, itself with an awkward name
	dir := filepath.Join(ws, "my docs", `it's "here"`)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a b.txt"), []byte("TODO: fix me\n"), 0o644)
	arg := "my docs/it's \"here\"/a b.txt"
	for _, lang := range Runtimes() {
		var cmd, script string
		switch lang {
		case "Node.js":
			cmd, script = `node ".codegen/tools/read-file.js" {{path}}`, "const fs=require('node:fs');process.stdout.write(fs.readFileSync(process.argv[2],'utf8'))"
		case "Bash":
			cmd, script = `bash '.codegen/tools/read-file.sh' {{path}}`, "#!/usr/bin/env bash\nset -euo pipefail\ncat -- \"$1\"\n"
		default:
			continue
		}
		d := extension.Extension{Name: "read-file", Desc: "Print a file.", Kind: "script", Lang: lang,
			Params: []extension.Param{{Name: "path", Type: "string", Required: true}}, Call: map[string]any{"path": arg},
			Command: cmd, Script: script}
		tr, err := h.TestExtension(d, "Show the file")
		if err != nil {
			t.Fatal(err)
		}
		if !tr.Pass || !strings.Contains(tr.Output, "TODO: fix me") {
			t.Fatalf("%s: %+v", lang, tr)
		}
		for _, st := range tr.Steps {
			if st.K == "run" && (strings.Contains(st.V, "tmp") || !strings.Contains(st.V, ".codegen/tools/read-file")) {
				t.Fatalf("%s: shown command must name the workspace script: %s", lang, st.V)
			}
		}
		saved, err := h.SaveExtension(d)
		if err != nil {
			t.Fatal(err)
		}
		r := extension.Exec(context.Background(), ws, saved, map[string]any{"path": arg}, nil, nil, 0)
		if !r.OK || !strings.Contains(r.Output, "TODO: fix me") {
			t.Fatalf("%s saved tool: %+v (command %s)", lang, r, saved.Command)
		}
	}
}

func TestPlaygroundPrompt(t *testing.T) {
	h, _ := newHub(t)
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	if _, err := h.RunPrompt("  ", PromptOptions{}); err == nil {
		t.Fatal("empty prompt must be refused")
	}
	id, err := h.RunPrompt("What does this project do?", PromptOptions{Tools: true})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var tools int
	to := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case e := <-ch:
			if e.RunID != id {
				continue
			}
			switch e.Type {
			case "model.text":
				text = e.Str("text")
			case "tool.call":
				tools++
			case "gate.wait":
				t.Fatal("playground runs must not stop at a gate")
			case "run.end":
				if e.Str("state") != "done" {
					t.Fatalf("state %s", e.Str("state"))
				}
				done = true
			}
		case <-to:
			t.Fatal("timed out")
		}
	}
	if !strings.Contains(text, "What does this project do?") || tools != 2 {
		t.Fatalf("text %q tools %d", text, tools)
	}
	wf, _ := h.Workflow()
	for _, s := range wf.Steps {
		if s.LastRun == id {
			t.Fatal("a playground run must not touch workflow steps")
		}
	}
	runs, _ := h.Runs(10)
	if len(runs) == 0 || runs[0].Step != PlaygroundStep {
		t.Fatalf("playground run should be in history: %+v", runs)
	}
}

func TestRulesDocsAndMultilineRules(t *testing.T) {
	h, ws := newHub(t)
	ws2, _ := h.Workspace()
	p := *ws2.Profile
	p.RulesMode = "bulk"
	p.Rules = []workflow.Rule{{ID: "R-001", Text: "Money uses **BigDecimal**:\n\n- round half-even\n- 2 decimals"}, {ID: "R-002", Text: "No `System.out` in services"}}
	if err := h.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.Workspace(); got.Profile.RulesMode != "bulk" {
		t.Fatal("rulesMode must be kept")
	}
	for _, f := range []string{"md", "html"} {
		rel, err := h.SaveRulesDoc(f, "doc "+f)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
		if string(b) != "doc "+f || !strings.HasPrefix(rel, ".codegen/rules/rules.") {
			t.Fatalf("%s → %s %q", f, rel, b)
		}
	}
	if _, err := h.SaveRulesDoc("exe", "x"); err == nil {
		t.Fatal("unknown format must be refused")
	}
	md := workflow.RulesMarkdown(p.Rules)
	if !strings.Contains(md, "## R-001\n\nMoney uses **BigDecimal**:") || !strings.Contains(md, "## R-002") {
		t.Fatalf("%s", md)
	}
	prompt, _ := h.Compose("constitution", "")
	if !strings.Contains(prompt, "- R-001: Money uses **BigDecimal**:\n  \n  - round half-even") {
		t.Fatalf("multi-line rule must stay under its bullet:\n%s", prompt)
	}
}

// specKitReady sets the test project up with the bundled Spec Kit template.
func specKitReady(t *testing.T, h *Hub) {
	t.Helper()
	seedSpecKit(t, h)
	if _, err := h.InitSpecKit("", "claude"); err != nil {
		t.Fatal(err)
	}
}

// seedSpecKit puts a downloaded Spec Kit in the hub's cache (no network).
func seedSpecKit(t *testing.T, h *Hub) {
	t.Helper()
	for _, in := range speckit.Integrations {
		if err := speckit.WriteSample(h.skCache().Source("1.4.0", in), in, "1.4.0"); err != nil {
			t.Fatal(err)
		}
	}
}
