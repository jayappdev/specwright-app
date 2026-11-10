// Command headless runs workflows with no window: in CI, over SSH, or to
// replay a recorded run.
//
//	headless -workspace ./orders -agent claude -input specify="An order service…" -approve auto
//	headless -workspace . -workflow ./ci-workflow.json -steps plan,tasks -json
//	headless -runs                    list recorded runs
//	headless -replay <run-id> -speed 4
//	headless agentbridge              run the agentbridge MCP server on stdio
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/core"
	"codegen/internal/extension"
	"codegen/internal/workflow"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func main() {
	if len(os.Args) > 1 && os.Args[1] == "agentbridge" {
		if err := extension.ServeMCP(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "agentbridge:", err)
			os.Exit(1)
		}
		return
	}
	os.Exit(run())
}

func run() int {
	var (
		ws       = flag.String("workspace", ".", "project folder")
		wfFile   = flag.String("workflow", "", "workflow JSON file (default: the workspace's Spec Kit workflow)")
		agentN   = flag.String("agent", "claude", "agent: claude | copilot | mock")
		model    = flag.String("model", "", "model override passed to the agent CLI")
		steps    = flag.String("steps", "", "comma-separated step ids to run (default: from the current step to the end)")
		approve  = flag.String("approve", "prompt", "at approval gates: auto | prompt | stop")
		asJSON   = flag.Bool("json", false, "print events as JSONL instead of text")
		listRuns = flag.Bool("runs", false, "list recorded runs and exit")
		replay   = flag.String("replay", "", "replay a recorded run by id")
		prompt   = flag.String("prompt", "", "run one free-form prompt (like the Playground) and exit")
		noExt    = flag.Bool("no-project-tools", false, "with -prompt: do not offer project/admin tools (built-ins stay)")
		cont     = flag.String("continue", "", "with -prompt: continue from earlier runs (comma-separated run ids); their questions, answers and results folders become context")
		speed    = flag.Float64("speed", 4, "replay speed multiplier (0 = instant)")
		storeDir = flag.String("store", "", "history directory (default: user config dir)")
		reset    = flag.Bool("reset", false, "set every step back to pending before running")
		inputs   multi
	)
	flag.Var(&inputs, "input", "step input as id=text (repeatable)")
	flag.Parse()

	h, err := core.New(core.Options{StoreDir: *storeDir, Agent: *agentN})
	if err != nil {
		return fail(err)
	}
	defer h.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	printer := newPrinter(*asJSON)

	if *listRuns {
		runs, err := h.Store.Runs("", 50)
		if err != nil {
			return fail(err)
		}
		for _, r := range runs {
			fmt.Printf("%s  %-12s %-10s %-11s %6d tok  $%.3f  %s\n", r.ID, r.Step, r.Agent, r.State, r.Usage.InputTokens+r.Usage.OutputTokens, r.Usage.CostUSD, r.Workspace)
		}
		return 0
	}
	if *replay != "" {
		if err := h.Store.Replay(ctx, *replay, *speed, printer.print); err != nil {
			return fail(err)
		}
		return 0
	}

	// no window to ask in: secrets come from the environment, or are typed
	// (hidden) on the terminal when a tool's source is "ask"
	h.SetSecretAsker(core.TerminalAsker)
	if err := h.SetAgent(*agentN, os.Getenv("CODEGEN_AGENT_TOKEN"), *model); err != nil {
		return fail(err)
	}
	if _, err := h.OpenWorkspace(*ws); err != nil {
		return fail(err)
	}
	if *prompt != "" {
		var parents []string
		for _, p := range strings.Split(*cont, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parents = append(parents, p)
			}
		}
		return runPrompt(ctx, h, *prompt, *model, !*noExt, parents, printer)
	}
	if *wfFile != "" {
		w, err := workflow.LoadFile(*wfFile)
		if err != nil {
			return fail(err)
		}
		if err := h.UseWorkflow(w); err != nil {
			return fail(err)
		}
	}
	if *reset {
		if err := h.ResetWorkflow(); err != nil {
			return fail(err)
		}
	}
	wf, err := h.Workflow()
	if err != nil {
		return fail(err)
	}
	for _, in := range inputs {
		id, text, ok := strings.Cut(in, "=")
		i := wf.Index(id)
		if !ok || i < 0 {
			return fail(fmt.Errorf("-input %q: want <step id>=<text> with a step of %s", in, wf.ID))
		}
		wf.Steps[i].Input = text
	}
	if err := h.SaveWorkflow(*wf); err != nil {
		return fail(err)
	}

	var todo []string
	if *steps != "" {
		todo = strings.Split(*steps, ",")
	} else {
		for _, s := range wf.Steps[wf.Current():] {
			if s.Status != "done" {
				todo = append(todo, s.ID)
			}
		}
	}
	stdin := bufio.NewReader(os.Stdin)
	for _, id := range todo {
		id = strings.TrimSpace(id)
		state, err := runOne(ctx, h, id, *approve, stdin, printer)
		if err != nil {
			return fail(err)
		}
		if state != "done" {
			fmt.Fprintf(os.Stderr, "headless: step %s ended %s; stopping\n", id, state)
			return 2
		}
	}
	return 0
}

func runOne(ctx context.Context, h *core.Hub, id, approve string, stdin *bufio.Reader, p *printer) (string, error) {
	events, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	runID, err := h.RunStep(id)
	if err != nil {
		return "", err
	}
	for {
		select {
		case <-ctx.Done():
			_ = h.CancelRun(runID)
			return "cancelled", errors.New("interrupted")
		case e, ok := <-events:
			if !ok {
				return "", errors.New("event bus closed")
			}
			if e.RunID != runID {
				continue
			}
			p.print(e)
			switch e.Type {
			case "gate.wait":
				kind, fb := decide(approve, e, stdin)
				if kind == "" {
					return "paused", nil
				}
				if err := h.Decide(runID, kind, fb); err != nil {
					return "", err
				}
			case "run.end":
				return e.Str("state"), nil
			}
		}
	}
}

func runPrompt(ctx context.Context, h *core.Hub, prompt, model string, ext bool, parents []string, p *printer) int {
	events, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	id, err := h.RunPrompt(prompt, core.PromptOptions{Model: model, Tools: ext, Parents: parents})
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(os.Stderr, "run id %s\n", id)
	for {
		select {
		case <-ctx.Done():
			_ = h.CancelRun(id)
			return 1
		case e, ok := <-events:
			if !ok {
				return 1
			}
			if e.RunID != id {
				continue
			}
			p.print(e)
			if e.Type == "run.end" {
				code := 0
				if e.Str("state") != "done" {
					code = 2
				}
				// the results folder is written just after the run ends
				deadline := time.After(5 * time.Second)
				for {
					select {
					case e2 := <-events:
						if e2.RunID == id && e2.Type == "results.saved" {
							fmt.Fprintf(os.Stderr, "results %s\n", e2.Summary)
							return code
						}
					case <-deadline:
						return code
					}
				}
			}
		}
	}
}

func decide(mode string, e bus.Event, stdin *bufio.Reader) (string, string) {
	switch mode {
	case "auto":
		if e.Str("gate") == "loop" {
			return "reject", "loop detected"
		}
		return "approve", ""
	case "stop":
		return "", ""
	}
	for {
		fmt.Fprint(os.Stderr, "\n[a]pprove, [c]hanges <feedback>, [r]eject? ")
		line, err := stdin.ReadString('\n')
		if err != nil {
			return "", ""
		}
		line = strings.TrimSpace(line)
		cmd, rest, _ := strings.Cut(line, " ")
		switch strings.ToLower(cmd) {
		case "a", "approve", "y", "yes":
			return "approve", ""
		case "c", "changes":
			return "changes", rest
		case "r", "reject":
			return "reject", rest
		}
	}
}

type printer struct{ json bool }

func newPrinter(j bool) *printer { return &printer{json: j} }

func (p *printer) print(e bus.Event) {
	if p.json {
		b, _ := json.Marshal(e)
		fmt.Println(string(b))
		return
	}
	switch e.Type {
	case "agent.log", "prompt.compose", "workflow.changed", "model.response":
		return
	case "model.text":
		fmt.Printf("\n%s\n", e.Str("text"))
		return
	}
	ts := e.TS.Local().Format(time.TimeOnly)
	fmt.Printf("%s  %-16s %s\n", ts, e.Type, e.Summary)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "headless:", err)
	return 1
}
