package core

import (
	"codegen/internal/bigout"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/store"
)

// patterns compiled once (not on every call)
var (
	runIDDate = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})-`) // the date at the start of a run id
)

// Run results: every run (workflow steps, playground prompts, Files › Ask
// AI, flows) gets an id of its start date and time plus a UUID, and when it
// ends its results are written to a folder in the project:
//
//	.codegen/runs/<YYYY-MM-DD>/<run id>/
//	  summary.md    what ran, when, how it went, usage, links to the rest
//	  run.json      the run's record (agent, model, state, usage, parents)
//	  prompt.md     the prompt as sent (secrets redacted)
//	  answer.md     the agent's answer
//	  tools.md      every tool call with its result
//	  files.md      files the run created, changed or deleted
//	  events.jsonl  the full (redacted) event log
//	  flow runs also: state.json, steps/<step>.txt, outputs/<n>-<title>.<format>
//
// The folder is git-ignored. Later prompts can continue from earlier runs:
// their question, answer and results folder become the new prompt's context.

// ResultsDir is where run results live in a workspace.
const ResultsDir = ".codegen/runs"

// runResultsDir is the workspace-relative folder of a run.
func runResultsDir(id string, started time.Time) string {
	day := started.Format("2006-01-02")
	if m := runIDDate.FindStringSubmatch(id); m != nil {
		day = m[1] + "-" + m[2] + "-" + m[3]
	}
	return ResultsDir + "/" + day + "/" + id
}

// watchRunEnds writes results whenever a run ends (started from New).
func (h *Hub) watchRunEnds() {
	ch, unsub := h.Bus.Subscribe(256, func(e bus.Event) bool { return e.Type == "run.end" })
	go func() {
		defer unsub()
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				id := e.RunID
				h.mu.Lock()
				if h.closing {
					h.mu.Unlock()
					continue
				}
				h.resultsWG.Add(1)
				h.mu.Unlock()
				go func() {
					defer h.resultsWG.Done()
					time.Sleep(150 * time.Millisecond) // let the run record its final state
					if err := h.WriteRunResults(id); err != nil {
						h.Bus.Publish(bus.Event{Type: "results.error", RunID: id, Summary: "could not save the run's results: " + err.Error()})
					}
				}()
			case <-h.ctx.Done():
				return
			}
		}
	}()
}

// RunDigest is a run as later prompts and the UI see it.
type RunDigest struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Title    string            `json:"title"`
	Ask      string            `json:"ask"`
	Answer   string            `json:"answer"`
	State    string            `json:"state"`
	Started  time.Time         `json:"started"`
	Ended    time.Time         `json:"ended,omitzero"`
	Agent    string            `json:"agent"`
	Model    string            `json:"model,omitempty"`
	Usage    store.Usage       `json:"usage"`
	Parents  []string          `json:"parents,omitempty"`
	Results  string            `json:"results,omitempty"`
	Files    []string          `json:"files,omitempty"` // files the run changed
	Outputs  []FlowOutputState `json:"outputs,omitempty"`
	Missing  bool              `json:"missing,omitempty"` // the run's record is gone
	Workflow string            `json:"workflow,omitempty"`
}

func kindOf(r store.Run) string {
	switch {
	case r.Kind != "":
		return r.Kind
	case r.Step == "flow":
		return "flow"
	case r.Workflow == PlaygroundStep:
		return "playground"
	}
	return "workflow"
}

// Digest summarizes one run from its record and events.
func (h *Hub) Digest(id string) (RunDigest, error) {
	r, err := h.Store.GetRun(id)
	if err != nil {
		return RunDigest{ID: id, Missing: true}, fmt.Errorf("no run %s", id)
	}
	d := RunDigest{ID: id, Kind: kindOf(r), Title: r.Title, Ask: r.Ask, State: r.State, Started: r.Started, Ended: r.Ended, Agent: r.Agent, Model: r.Model,
		Usage: r.Usage, Parents: r.Parents, Results: r.Results, Workflow: r.Workflow}
	evs, _ := h.Store.Events(id)
	var texts []string
	seen := map[string]bool{}
	for _, e := range evs {
		switch e.Type {
		case "model.text":
			if t, _ := e.Data["text"].(string); strings.TrimSpace(t) != "" {
				texts = append(texts, strings.TrimSpace(t))
			}
		case "prompt.compose":
			if d.Ask == "" {
				if p, _ := e.Data["prompt"].(string); p != "" {
					d.Ask = p
				}
			}
		case "fs.write", "fs.edit", "fs.delete":
			if p, _ := e.Data["path"].(string); p != "" && !seen[p] {
				seen[p] = true
				d.Files = append(d.Files, p)
			}
		case "flow.state":
			b, _ := json.Marshal(e.Data["state"])
			var st FlowRunState
			if json.Unmarshal(b, &st) == nil {
				d.Outputs = st.Outputs
			}
		}
	}
	switch d.Kind {
	case "flow":
		var parts []string
		for _, o := range d.Outputs {
			parts = append(parts, fmt.Sprintf("### %s (%s)\n%s", o.Title, o.Format, strings.TrimSpace(o.Content)))
		}
		if len(parts) == 0 && len(texts) > 0 {
			parts = texts
		}
		d.Answer = strings.Join(parts, "\n\n")
	default:
		d.Answer = strings.Join(texts, "\n\n")
	}
	if d.Title == "" {
		d.Title = firstLine(firstNonEmpty(d.Ask, r.Step))
	}
	return d, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

// Thread returns the runs id continues from, oldest first, ending with id
// itself (at most 12 runs).
func (h *Hub) Thread(id string) ([]RunDigest, error) {
	var out []RunDigest
	seen := map[string]bool{}
	var walk func(string, int)
	walk = func(rid string, depth int) {
		if seen[rid] || depth > 12 || len(out) >= 12 {
			return
		}
		seen[rid] = true
		d, err := h.Digest(rid)
		if err != nil {
			d.Title = "A run that is no longer recorded"
		}
		for _, p := range d.Parents {
			walk(p, depth+1)
		}
		out = append(out, d)
	}
	walk(id, 0)
	if len(out) == 0 {
		return nil, fmt.Errorf("no run %s", id)
	}
	return out, nil
}

// continuationContext is the context a new prompt gets from the runs it
// continues: each run's question and answer (shortened) and its results
// folder, oldest first.
func (h *Hub) continuationContext(parents []string) (string, error) {
	var all []RunDigest
	seen := map[string]bool{}
	for _, p := range parents {
		if _, err := h.Store.GetRun(p); err != nil {
			return "", fmt.Errorf("the earlier run %s is no longer recorded", p)
		}
		th, err := h.Thread(p)
		if err != nil {
			return "", err
		}
		for _, d := range th {
			if !seen[d.ID] {
				seen[d.ID] = true
				all = append(all, d)
			}
		}
	}
	if len(all) == 0 {
		return "", nil
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Started.Before(all[j].Started) })
	if len(all) > 10 {
		all = all[len(all)-10:]
	}
	cut := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if len(s) > n {
			return s[:n] + "\n…(shortened; the full text is in the results folder)"
		}
		return s
	}
	var b strings.Builder
	b.WriteString("This continues earlier work in this project. The earlier runs are below, oldest first. Their complete results (prompt, answer, tool calls, changed files, outputs) are in the folders named; read those files when you need more detail.\n")
	for i, d := range all {
		label := map[string]string{"playground": "Prompt", "workflow": "Workflow step", "flow": "Flow run"}[d.Kind]
		fmt.Fprintf(&b, "\n### %d. %s: %s (%s, %s)\n", i+1, firstNonEmpty(label, "Run"), d.Title, d.Started.Format("2006-01-02 15:04"), d.State)
		if d.Results != "" {
			fmt.Fprintf(&b, "Results folder: %s/\n", d.Results)
		}
		if d.Kind != "playground" || d.Ask != d.Title {
			fmt.Fprintf(&b, "Request:\n%s\n", cut(d.Ask, 2500))
		} else {
			fmt.Fprintf(&b, "Request: %s\n", d.Ask)
		}
		fmt.Fprintf(&b, "Answer:\n%s\n", cut(firstNonEmpty(d.Answer, "(no text answer)"), 6000))
		if len(d.Files) > 0 {
			files := d.Files
			if len(files) > 20 {
				files = files[:20]
			}
			fmt.Fprintf(&b, "Files it changed: %s\n", strings.Join(files, ", "))
		}
	}
	// flows: where they are, and how to change them so they keep fitting the
	// Flow Designer's format (an agent editing the JSON by hand broke them)
	var flows []string
	seenFlow := map[string]bool{}
	for _, d := range all {
		if d.Kind != "flow" {
			continue
		}
		if r, err := h.Store.GetRun(d.ID); err == nil && r.Workflow != "" && !seenFlow[r.Workflow] && !strings.HasPrefix(r.Workflow, "draft-") {
			seenFlow[r.Workflow] = true
			flows = append(flows, fmt.Sprintf("- %s: .codegen/flows/%s.json (id %s)", firstNonEmpty(d.Title, r.Workflow), r.Workflow, r.Workflow))
		}
	}
	if len(flows) > 0 {
		b.WriteString("\n### Changing a flow\nThe flow(s) above:\n" + strings.Join(flows, "\n") +
			"\nRead the flow's file to see it. To change it, call SpecWright's save_flow tool with the complete flow JSON and base_flow_id set to its id: it is checked against the Flow Designer format and saved as a new version (the original stays), and its answer lists any problems to fix (fix them and call save_flow again with update_version_id). Do NOT edit .codegen/flows/*.json directly: a file that does not fit the format breaks the flow.\n" +
			"save_flow is on the agentbridge MCP server: in GitHub Copilot it is named agentbridge-save_flow, in Claude Code mcp__agentbridge__save_flow. If you see only its name, load it with your tool search (search for save_flow); do not look for it in files, and do not search outside this project (that is refused). It needs no approval.\n" +
			"To run a flow by itself (at a time or every so often, after another flow, or when files change), use SpecWright's schedule_flow tool (agentbridge-schedule_flow in Copilot, mcp__agentbridge__schedule_flow in Claude Code): action list shows its triggers, set adds or changes one, remove deletes one. It saves the trigger into the flow in place (no new version).\n")
	}
	b.WriteString("\n---\n\nNow, continuing from the above:\n")
	return b.String(), nil
}

// RunResults describes a run's results folder.
type RunResults struct {
	ID    string   `json:"id"`
	Dir   string   `json:"dir"`   // workspace-relative
	Abs   string   `json:"abs"`   // absolute
	Files []string `json:"files"` // relative to Dir
	Ready bool     `json:"ready"`
}

// Results returns where a run's results are (writing them first if the
// run ended and they are missing).
func (h *Hub) Results(id string) (RunResults, error) {
	r, err := h.Store.GetRun(id)
	if err != nil {
		return RunResults{}, fmt.Errorf("no run %s", id)
	}
	root := r.Workspace
	if root == "" {
		root = h.Root()
	}
	dir := firstNonEmpty(r.Results, runResultsDir(id, r.Started))
	abs := filepath.Join(root, filepath.FromSlash(dir))
	if _, err := os.Stat(abs); err != nil && !r.Ended.IsZero() {
		if err := h.WriteRunResults(id); err != nil {
			return RunResults{}, err
		}
	}
	res := RunResults{ID: id, Dir: dir, Abs: abs}
	_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(abs, p)
			res.Files = append(res.Files, filepath.ToSlash(rel))
		}
		return nil
	})
	res.Ready = len(res.Files) > 0
	return res, nil
}

var unsafeFile = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// WriteRunResults writes (or rewrites) a run's results folder.
func (h *Hub) WriteRunResults(id string) error {
	r, err := h.Store.GetRun(id)
	if err != nil {
		return err
	}
	root := firstNonEmpty(r.Workspace, h.Root())
	if root == "" {
		return errors.New("no workspace")
	}
	dir := runResultsDir(id, r.Started)
	abs := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	// keep run results out of git (they can be large and personal)
	gi := filepath.Join(root, filepath.FromSlash(ResultsDir), ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		_ = os.WriteFile(gi, []byte("# SpecWright run results (prompts, answers, outputs); delete freely\n*\n"), 0o644)
	}
	d, _ := h.Digest(id)
	evs, _ := h.Store.Events(id)
	red := h.Store.Redactor()
	var werr error // the first file that could not be written; the others are still tried
	write := func(name, content string) {
		p := filepath.Join(abs, name)
		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err == nil {
			err = os.WriteFile(p, []byte(red.String(content)), 0o644)
		}
		if err != nil && werr == nil {
			werr = fmt.Errorf("write %s: %w", name, err)
		}
	}
	// events and the composed prompt
	var ev strings.Builder
	var prompt string
	type call struct {
		tool, args, out string
		ok              bool
		ms              int64
		done            bool
	}
	var calls []*call
	byID := map[string]*call{}
	for _, e := range evs {
		b, _ := json.Marshal(e)
		ev.Write(b)
		ev.WriteByte('\n')
		switch e.Type {
		case "prompt.compose":
			if p, _ := e.Data["prompt"].(string); p != "" {
				prompt = p
			}
		case "tool.call":
			c := &call{tool: str(e.Data["tool"])}
			if a, ok := e.Data["args"]; ok {
				ab, _ := json.Marshal(a)
				c.args = string(ab)
			}
			calls = append(calls, c)
			if k := str(e.Data["id"]); k != "" {
				byID[k] = c
			}
		case "tool.result":
			c := byID[str(e.Data["id"])]
			if c == nil {
				for i := len(calls) - 1; i >= 0; i-- {
					if !calls[i].done && calls[i].tool == str(e.Data["tool"]) {
						c = calls[i]
						break
					}
				}
			}
			if c != nil {
				c.done, c.ok, c.out, c.ms = true, e.Data["ok"] != false, str(e.Data["output"]), int64(toInt(e.Data["duration_ms"]))
			}
		}
	}
	write("events.jsonl", ev.String())
	if prompt == "" {
		prompt = d.Ask
	}
	write("prompt.md", prompt+"\n")
	if strings.TrimSpace(d.Answer) != "" {
		write("answer.md", d.Answer+"\n")
	}
	if len(calls) > 0 {
		var t strings.Builder
		fmt.Fprintf(&t, "# Tool calls (%d)\n\n", len(calls))
		for i, c := range calls {
			state := "running"
			if c.done {
				state = map[bool]string{true: "ok", false: "failed"}[c.ok]
			}
			fmt.Fprintf(&t, "## %d. %s · %s", i+1, strings.TrimPrefix(c.tool, "mcp__agentbridge__"), state)
			if c.ms > 0 {
				fmt.Fprintf(&t, " · %d ms", c.ms)
			}
			t.WriteString("\n\n")
			if c.args != "" && c.args != "null" && c.args != "{}" {
				fmt.Fprintf(&t, "Arguments:\n\n```json\n%s\n```\n\n", c.args)
			}
			if out := strings.TrimSpace(c.out); out != "" {
				if len(out) > 4000 {
					out = bigout.SafeCut(out, 4000) + fmt.Sprintf("\n…(%s in all; the whole output is in this call's output file)", bigout.Human(len(out)))
				}
				fmt.Fprintf(&t, "Result:\n\n```\n%s\n```\n\n", out)
			}
		}
		write("tools.md", t.String())
		// each call's input and output as files: tools/<nn>-<tool>/input.json, output.<ext>
		for i, c := range calls {
			dir := fmt.Sprintf("tools/%02d-%s", i+1, strings.Trim(unsafeFile.ReplaceAllString(strings.TrimPrefix(strings.TrimPrefix(c.tool, "mcp__agentbridge__"), "agentbridge-"), "_"), "_"))
			if c.args != "" && c.args != "null" {
				var v any
				if json.Unmarshal([]byte(c.args), &v) == nil {
					b, _ := json.MarshalIndent(v, "", "  ")
					write(dir+"/input.json", string(b)+"\n")
				}
			}
			if c.done {
				write(dir+"/output."+contentExt(c.out), c.out)
			}
		}
	}
	if len(d.Files) > 0 {
		var f strings.Builder
		f.WriteString("# Files changed\n\n")
		for _, p := range d.Files {
			fmt.Fprintf(&f, "- %s\n", p)
		}
		write("files.md", f.String())
	}
	// flows: the final state, every step's output and each output
	if d.Kind == "flow" {
		if st, err := h.FlowRun(id); err == nil {
			b, _ := json.MarshalIndent(st, "", "  ")
			write("state.json", string(b))
			for _, s := range st.Steps {
				if s.Output != "" {
					write("steps/"+unsafeFile.ReplaceAllString(s.ID, "_")+"/output."+contentExt(s.Output), s.Output)
				}
			}
			for i, o := range st.Outputs {
				name := fmt.Sprintf("outputs/%02d-%s.%s", i+1, strings.Trim(unsafeFile.ReplaceAllString(strings.ToLower(o.Title), "-"), "-"), firstNonEmpty(o.Format, "txt"))
				write(name, o.Content)
			}
		}
	}
	// the record, now with its folder
	r.Results = dir
	b, _ := json.MarshalIndent(r, "", "  ")
	write("run.json", string(b))
	write("summary.md", h.summaryMD(r, d, len(calls)))
	// the run record says where its results are: save it last, so a reader
	// that sees Results set finds every file written
	_ = h.Store.SaveRun(r)
	h.Bus.Publish(bus.Event{Type: "results.saved", RunID: id, Summary: dir, Data: map[string]any{"dir": dir}})
	return werr
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (h *Hub) summaryMD(r store.Run, d RunDigest, calls int) string {
	var b strings.Builder
	kind := map[string]string{"playground": "Playground prompt", "workflow": "Workflow step", "flow": "Flow run"}[d.Kind]
	fmt.Fprintf(&b, "# %s: %s\n\n", firstNonEmpty(kind, "Run"), d.Title)
	fmt.Fprintf(&b, "- **Run id:** `%s`\n", r.ID)
	fmt.Fprintf(&b, "- **Started:** %s", r.Started.Format("2006-01-02 15:04:05"))
	if !r.Ended.IsZero() {
		fmt.Fprintf(&b, " · **ended** %s (%s)", r.Ended.Format("15:04:05"), r.Ended.Sub(r.Started).Round(time.Second))
	}
	fmt.Fprintf(&b, "\n- **State:** %s", r.State)
	if r.Reason != "" {
		fmt.Fprintf(&b, " (%s)", r.Reason)
	}
	fmt.Fprintf(&b, "\n- **Agent:** %s%s\n", r.Agent, map[bool]string{true: " · " + r.Model, false: ""}[r.Model != ""])
	u := r.Usage
	fmt.Fprintf(&b, "- **Usage:** %d tokens in · %d out · %d AI calls · %d tool calls", u.InputTokens, u.OutputTokens, u.ModelCalls, max(u.ToolCalls, calls))
	if u.CostUSD > 0 {
		fmt.Fprintf(&b, " · $%.4f", u.CostUSD)
	}
	if u.PremiumRequests > 0 {
		fmt.Fprintf(&b, " · %d premium requests", u.PremiumRequests)
	}
	b.WriteString("\n")
	if len(r.Parents) > 0 {
		fmt.Fprintf(&b, "- **Continues:** %s\n", strings.Join(r.Parents, ", "))
	}
	b.WriteString("\n## Files here\n\n- `prompt.md`: the prompt as sent\n")
	if strings.TrimSpace(d.Answer) != "" {
		b.WriteString("- `answer.md`: the answer\n")
	}
	if calls > 0 {
		b.WriteString("- `tools.md`: tool calls and results · `tools/<n>-<tool>/`: each call's input and output\n")
	}
	if len(d.Files) > 0 {
		b.WriteString("- `files.md`: files the run changed\n")
	}
	if d.Kind == "flow" {
		b.WriteString("- `outputs/`: the flow's outputs · `steps/`: each step's output · `state.json`: the final state\n")
	}
	b.WriteString("- `events.jsonl`: the full event log · `run.json`: the run's record\n")
	if a := strings.TrimSpace(d.Answer); a != "" {
		if len(a) > 1500 {
			a = bigout.SafeCut(a, 1500) + "\n\n…(see answer.md)"
		}
		b.WriteString("\n## Answer\n\n" + a + "\n")
	}
	return b.String()
}

// RevealPath shows a workspace path in Finder / Explorer / the file manager.
func (h *Hub) RevealPath(rel string) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return errors.New("only paths inside the project can be shown")
	}
	abs := filepath.Join(root, clean)
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", abs)
	case "windows":
		cmd = exec.Command("explorer", "/select,", abs)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(abs))
	}
	return cmd.Start()
}

// RecentRuns lists runs of every kind in this workspace for "continue
// from", newest first.
func (h *Hub) RecentRuns(limit int) ([]RunDigest, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	runs, err := h.Store.Runs(root, max(limit, 1))
	if err != nil {
		return nil, err
	}
	out := make([]RunDigest, 0, len(runs))
	for _, r := range runs {
		if r.Archived {
			continue
		}
		out = append(out, RunDigest{ID: r.ID, Kind: kindOf(r), Title: firstNonEmpty(r.Title, firstLine(r.Ask), r.Step), Ask: r.Ask, State: r.State, Started: r.Started, Ended: r.Ended,
			Agent: r.Agent, Model: r.Model, Usage: r.Usage, Parents: r.Parents, Results: r.Results, Workflow: r.Workflow})
	}
	return out, nil
}

var (
	reHTML = regexp.MustCompile(`(?i)^\s*(<!doctype|<html|<(div|p|h[1-6]|table|ul|ol|section|article|body|svg)\b)`)
	reMD   = regexp.MustCompile(`(?m)^(#{1,6}\s|\s*[-*+]\s+\S|\s*\d+\.\s+\S|\|.+\|\s*$|` + "```" + `)`)
)

// contentExt guesses a file extension for tool output: html, json, csv,
// md or txt.
func contentExt(s string) string {
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return "txt"
	case reHTML.MatchString(t):
		return "html"
	case (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)):
		return "json"
	case looksCSVText(t):
		return "csv"
	case reMD.MatchString(t):
		return "md"
	}
	return "txt"
}

func looksCSVText(t string) bool {
	lines := strings.Split(t, "\n")
	if len(lines) < 2 {
		return false
	}
	n := strings.Count(lines[0], ",")
	if n == 0 {
		return false
	}
	for _, l := range lines[1:min(len(lines), 20)] {
		if strings.TrimSpace(l) != "" && strings.Count(l, ",") != n {
			return false
		}
	}
	return true
}

// stepFilesDir is where a flow step's input and output files live.
func stepFilesDir(runID, step string) string {
	return runResultsDir(runID, time.Now()) + "/steps/" + unsafeFile.ReplaceAllString(step, "_")
}

// writeRunFile writes one file into a run's results folder and returns its
// workspace-relative path.
func (h *Hub) writeRunFile(rel, content string) (string, error) {
	root := h.Root()
	if root == "" {
		return "", errors.New("no workspace")
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, []byte(h.Store.Redactor().String(content)), 0o644)
}
