package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
	"codegen/internal/loop"
	"codegen/internal/session"
	"codegen/internal/store"
	"codegen/internal/watch"
)

// Runner executes workflow steps. One step runs at a time per runner.
type Runner struct {
	Bus       *bus.Bus
	Store     *store.Store
	Agents    *agent.Registry
	Workspace string
	// MCPConfig, if set, returns a path to an MCP config for the run plus a
	// cleanup func. Env returns extra child environment (session token,
	// bridge address).
	// The bool says whether to offer project/admin tools (extension_managed);
	// SpecWright's built-in tools are always attached.
	MCPConfig func(extensions bool) (string, func(), error)
	Env       func() []string
	// OnStatus is called whenever a step's status changes.
	OnStatus func(stepID, status, runID string)
	// LoopConfig tunes loop detection.
	LoopConfig loop.Config
	// Flush is called when the agent finishes, so file changes it made just
	// before exiting are attributed to this run (the watcher polls).
	Flush func()
	// ToolsReached says whether an agent reached SpecWright's tools since a
	// time (the bridge's last contact): a run that never did gets a
	// tools.unreachable event, as its CLI did not load the MCP server.
	ToolsReached func(since time.Time) bool

	mu     sync.Mutex
	active *Run
}

// Run is a live step run.
type Run struct {
	Session *session.Session
	cancel  context.CancelFunc
	mu      sync.Mutex
	seq     int
	usage   store.Usage
	model   string
	started time.Time
	files   map[string]string
	runner  *Runner
	done    chan struct{}
}

// Usage returns a copy of the run's usage so far.
func (r *Run) Usage() store.Usage { r.mu.Lock(); defer r.mu.Unlock(); return r.usage }

// AddUsage adds usage from outside the agent's own stream, e.g. the AI a
// tool the agent called used itself.
func (r *Run) AddUsage(u store.Usage) { r.mu.Lock(); r.usage.Add(u); r.mu.Unlock() }

// Done is closed when the run reaches a terminal state.
func (r *Run) Done() <-chan struct{} { return r.done }

// Cancel stops the run.
func (r *Run) Cancel() { r.cancel() }

// Active returns the live run, if any.
func (rn *Runner) Active() *Run { rn.mu.Lock(); defer rn.mu.Unlock(); return rn.active }

// StartOptions configure one step run.
type StartOptions struct {
	Agent  string
	Model  string
	Prompt string // composed prompt (Compose)
	// Recompose rebuilds the prompt with reviewer feedback for "request changes".
	Recompose func(feedback string) string
	Budget    Budget
	Retries   int
	Gate      bool
	NoTools   bool // do not offer the extension_managed MCP tool
	// Title and Ask describe the run in history ("Specify", or the user's
	// playground prompt); Parents are earlier runs it continues from.
	Title   string
	Ask     string
	Parents []string
	// Dir is the agent's working directory when it is not the workspace
	// (a Spec Kit project folder inside it); AddDirs are opened to it too.
	Dir     string
	AddDirs []string
	// Interactive: a person answers permission questions in the app while
	// it runs (the Playground); see agent.Request.Interactive.
	Interactive bool
}

// NewRunID returns a unique, sortable run id: the local date and time the
// run started, then a random (version 4) UUID, e.g.
// 20261110-142233-3f2a8c1e-7b3d-4a60-9c1e-0b9d2f5e8a71.
func NewRunID() string {
	return time.Now().Format("20060102-150405") + "-" + NewUUID()
}

// NewUUID returns a random version 4 UUID.
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Start launches step (by id) of workflow wfID asynchronously.
func (rn *Runner) Start(ctx context.Context, wfID, stepID string, o StartOptions) (*Run, error) {
	drv, err := rn.Agents.Get(o.Agent)
	if err != nil {
		return nil, err
	}
	rn.mu.Lock()
	if rn.active != nil && !rn.active.Session.State().Terminal() {
		rn.mu.Unlock()
		return nil, errors.New("another step is still running or waiting for approval")
	}
	ctx, cancel := context.WithCancel(ctx)
	id := NewRunID()
	run := &Run{Session: session.New(id, wfID, stepID, o.Agent), cancel: cancel, started: time.Now(), files: map[string]string{}, runner: rn, done: make(chan struct{})}
	rn.active = run
	rn.mu.Unlock()

	sess := run.Session
	meta := func() store.Run {
		u := run.Usage()
		u.DurationMS = time.Since(run.started).Milliseconds()
		kind := "workflow"
		if wfID == "playground" {
			kind = "playground"
		}
		r := store.Run{ID: id, Workspace: rn.Workspace, Workflow: wfID, Step: stepID, Agent: o.Agent, Model: run.model, State: string(sess.State()), Reason: sess.Reason(), Started: run.started, Usage: u, Events: run.seq,
			Kind: kind, Title: o.Title, Ask: o.Ask, Parents: o.Parents}
		if sess.State().Terminal() {
			r.Ended = time.Now()
		}
		return r
	}
	sess.OnChange(func(t session.Transition) {
		_ = rn.Store.SaveRun(meta())
		if rn.OnStatus != nil {
			rn.OnStatus(stepID, string(t.To), id)
		}
	})
	go rn.execute(ctx, run, drv, wfID, stepID, o, meta)
	return run, nil
}

// Emit sends an event of run (a tool's result during the run, say) with the
// run's next sequence number, and records it, as the run's own events are.
func (rn *Runner) Emit(run *Run, typ, summary string, data map[string]any) bus.Event {
	return rn.publish(run, typ, summary, data)
}

// publish sends an event for run through the bus and records it.
func (rn *Runner) publish(run *Run, typ, summary string, data map[string]any) bus.Event {
	run.mu.Lock()
	run.seq++
	seq := run.seq
	run.mu.Unlock()
	if data == nil {
		data = map[string]any{}
	}
	data["dt"] = time.Since(run.started).Milliseconds()
	e := rn.Bus.Publish(bus.Event{Seq: seq, RunID: run.Session.ID, Step: run.Session.Step, Type: typ, Summary: summary, Data: data})
	_ = rn.Store.Append(e)
	return e
}

type stopCause struct{ kind, msg string }

func (c stopCause) Error() string { return c.msg }

func (rn *Runner) execute(ctx context.Context, run *Run, drv agent.Driver, wfID, stepID string, o StartOptions, meta func() store.Run) {
	defer close(run.done)
	defer run.cancel()
	sess := run.Session
	_ = sess.To(session.Running, "started")
	rn.publish(run, "run.start", "Started "+stepID+" with "+drv.Name(), map[string]any{"agent": drv.Name(), "model": o.Model, "cwd": rn.Workspace, "workflow": wfID})
	prompt := o.Prompt
	offered := rn.MCPConfig != nil && drv.Name() != "mock"
	if offered {
		prompt += ToolCommandNote
	}
	finish := func(state session.State, reason string) {
		if offered && rn.ToolsReached != nil && !rn.ToolsReached(run.started) && ctx.Err() == nil {
			rn.publish(run, "tools.unreachable", "SpecWright's tools did not reach "+drv.Name()+" in this run: neither its MCP server (agentbridge) nor its Copilot extension (~/.copilot/extensions/specwright) was loaded. A company policy, or the CLI, may block them. The agent can still run them with its shell (\"$CODEGEN_TOOL\" tool list), as the prompt tells it.", map[string]any{"agent": drv.Name()})
		}
		_ = sess.To(state, reason)
		u := run.Usage()
		u.DurationMS = time.Since(run.started).Milliseconds()
		rn.publish(run, "run.end", fmt.Sprintf("Run %s", state), map[string]any{"state": string(state), "reason": reason})
		_ = rn.Store.SaveRun(meta())
		_ = rn.Store.Ledger(store.LedgerEntry{At: time.Now(), RunID: sess.ID, Workspace: rn.Workspace, Workflow: wfID, Step: stepID, Agent: drv.Name(), Model: run.model, State: string(state), Usage: u})
	}

	for round := 0; ; round++ {
		rn.publish(run, "prompt.compose", fmt.Sprintf("Prompt composed (%d chars)", len(prompt)), map[string]any{"prompt": prompt, "round": round})
		err := rn.attempts(ctx, run, drv, o, prompt)
		if rn.Flush != nil {
			rn.Flush()
		}
		var cause stopCause
		switch {
		case errors.As(err, &cause) && cause.kind == "loop":
			if err := sess.Pause("loop", cause.msg); err == nil {
				rn.publish(run, "gate.wait", "Paused: the agent seems stuck in a loop", map[string]any{"gate": "loop", "reason": cause.msg})
			}
		case errors.As(err, &cause) && cause.kind == "budget":
			rn.publish(run, "budget.exceeded", cause.msg, nil)
			finish(session.Failed, cause.msg)
			return
		case ctx.Err() != nil:
			finish(session.Cancelled, "cancelled by you")
			return
		case err != nil:
			rn.publish(run, "error", err.Error(), map[string]any{"error": err.Error()})
			finish(session.Failed, err.Error())
			return
		default:
			u := run.Usage()
			rn.publish(run, "run.usage", fmt.Sprintf("Usage: %d in · %d out · %d tools · %d files", u.InputTokens, u.OutputTokens, u.ToolCalls, u.FilesCreated+u.FilesModified),
				map[string]any{"input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "cached_tokens": u.CachedTokens, "premium_requests": u.PremiumRequests, "cost_usd": u.CostUSD, "tool_calls": u.ToolCalls, "files_changed": u.FilesCreated + u.FilesModified})
			if !o.Gate {
				finish(session.Done, "completed")
				return
			}
			if err := sess.Pause("approval", "waiting for approval"); err == nil {
				rn.publish(run, "gate.wait", "Paused for human approval", map[string]any{"gate": "approval"})
			}
		}
		// wait at the gate
		var d session.Decision
		select {
		case <-ctx.Done():
			finish(session.Cancelled, "cancelled at the gate")
			return
		case d = <-sess.Decisions():
		}
		switch d.Kind {
		case "approve":
			rn.publish(run, "gate.approved", "Approved by you", nil)
			finish(session.Done, "approved")
			return
		case "reject":
			rn.publish(run, "gate.rejected", "Rejected by you", map[string]any{"feedback": d.Feedback})
			finish(session.Failed, "rejected")
			return
		default: // changes
			rn.publish(run, "gate.changes", "Changes requested", map[string]any{"feedback": d.Feedback})
			if o.Recompose != nil {
				prompt = o.Recompose(d.Feedback)
			}
			_ = sess.To(session.Running, "changes requested")
		}
	}
}

// attempts runs the agent with retries. Loop and budget stops are returned
// as stopCause and are not retried.
func (rn *Runner) attempts(ctx context.Context, run *Run, drv agent.Driver, o StartOptions, prompt string) error {
	var last error
	for attempt := 0; attempt <= o.Retries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * 2 * time.Second
			rn.publish(run, "run.retry", fmt.Sprintf("Retry %d of %d in %s: %s", attempt, o.Retries, wait, last), map[string]any{"attempt": attempt, "error": last.Error()})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		last = rn.once(ctx, run, drv, o, prompt)
		var cause stopCause
		if last == nil || errors.As(last, &cause) || ctx.Err() != nil || errors.Is(last, agent.ErrNotInstalled) {
			return last
		}
	}
	return last
}

func (rn *Runner) once(parent context.Context, run *Run, drv agent.Driver, o StartOptions, prompt string) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	det := loop.New(rn.LoopConfig)
	dir := rn.Workspace
	if o.Dir != "" {
		dir = o.Dir
	}
	req := agent.Request{Purpose: "step", Step: run.Session.Step, Prompt: prompt, Dir: dir, AddDirs: o.AddDirs, Model: o.Model,
		AllowTools: []string{"mcp__agentbridge"}, Interactive: o.Interactive} // every agentbridge tool: built-ins + extension_managed
	if rn.Env != nil {
		req.Env = rn.Env()
	}
	if rn.MCPConfig != nil {
		if p, done, err := rn.MCPConfig(!o.NoTools); err == nil {
			req.MCPConfig = p
			defer done()
		}
	}
	emit := func(ev agent.Event) {
		if ctx.Err() != nil {
			return
		}
		run.mu.Lock()
		u := &run.usage
		switch ev.Type {
		case "model.request":
			u.InputTokens += toInt(ev.Data["input_tokens"])
			u.CachedTokens += toInt(ev.Data["cached_tokens"])
			u.ModelCalls++
			if m, _ := ev.Data["model"].(string); m != "" {
				run.model = m
			}
		case "model.response":
			u.OutputTokens += toInt(ev.Data["output_tokens"])
		case "tool.call":
			u.ToolCalls++
		case "tool.result":
			if ok, _ := ev.Data["ok"].(bool); !ok {
				u.ToolErrors++
			}
		case "run.usage":
			if c, ok := ev.Data["cost_usd"].(float64); ok && c > 0 {
				u.CostUSD += c
			}
			u.PremiumRequests += toInt(ev.Data["premium_requests"])
			if in := toInt(ev.Data["input_tokens"]); in > 0 {
				u.InputTokens += in
				u.OutputTokens += toInt(ev.Data["output_tokens"])
				u.CachedTokens += toInt(ev.Data["cached_tokens"])
			}
			if m, _ := ev.Data["model"].(string); m != "" {
				run.model = m
			}
		}
		snapshot := *u
		run.mu.Unlock()
		typ := ev.Type
		if typ == "run.usage" {
			typ = "model.usage" // the runner emits the step-level run.usage itself
		}
		rn.publish(run, typ, ev.Summary, ev.Data)

		if v := checkLoop(det, ev); v != nil {
			rn.publish(run, "loop.detected", "Loop detected: "+v.Reason, map[string]any{"action": v.Action, "count": v.Count})
			cancel(stopCause{"loop", fmt.Sprintf("%s (%s)", v.Reason, v.Action)})
			return
		}
		if msg := overBudget(o.Budget, snapshot); msg != "" {
			cancel(stopCause{"budget", msg})
		}
	}
	_, err := drv.Run(ctx, req, emit)
	if c := context.Cause(ctx); c != nil {
		var sc stopCause
		if errors.As(c, &sc) {
			return sc
		}
	}
	return err
}

func checkLoop(d *loop.Detector, ev agent.Event) *loop.Verdict {
	switch ev.Type {
	case "tool.call":
		name, _ := ev.Data["tool"].(string)
		return d.ToolCall(name, ev.Data["args"])
	case "model.text":
		t, _ := ev.Data["text"].(string)
		return d.Reply(t)
	}
	return nil
}

func overBudget(b Budget, u store.Usage) string {
	switch {
	case b.MaxTokens > 0 && u.InputTokens+u.OutputTokens > b.MaxTokens:
		return fmt.Sprintf("Token budget exceeded: %d of %d", u.InputTokens+u.OutputTokens, b.MaxTokens)
	case b.MaxUSD > 0 && u.CostUSD > b.MaxUSD:
		return fmt.Sprintf("Cost budget exceeded: $%.2f of $%.2f", u.CostUSD, b.MaxUSD)
	case b.MaxPremiumRequests > 0 && u.PremiumRequests > b.MaxPremiumRequests:
		return fmt.Sprintf("Premium request budget exceeded: %d of %d", u.PremiumRequests, b.MaxPremiumRequests)
	}
	return ""
}

// FileChanged attributes a watcher change to the active run (if running).
func (rn *Runner) FileChanged(c watch.Change) bool {
	run := rn.Active()
	if run == nil || run.Session.State() != session.Running || appOwned(c.Path) {
		return false
	}
	typ := map[string]string{"created": "fs.write", "modified": "fs.edit", "deleted": "fs.delete"}[c.Op]
	verb := map[string]string{"created": "Created", "modified": "Modified", "deleted": "Deleted"}[c.Op]
	run.mu.Lock()
	prev, seen := run.files[c.Path]
	if !seen {
		run.files[c.Path] = c.Op
		switch c.Op {
		case "created":
			run.usage.FilesCreated++
		case "modified":
			run.usage.FilesModified++
		}
	} else if prev == "deleted" && c.Op == "created" {
		run.files[c.Path] = "modified"
	}
	run.mu.Unlock()
	rn.publish(run, typ, verb+" "+c.Path, map[string]any{"path": c.Path, "added": c.Added, "removed": c.Removed, "op": c.Op})
	return true
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

// Exists reports whether path exists (small helper for callers).
func Exists(path string) bool { _, err := os.Stat(path); return err == nil }

// appOwned reports files the app itself writes, which are never agent work.
func appOwned(p string) bool {
	return strings.HasPrefix(p, ".codegen/workflows/") || p == ".codegen/profile.json" || p == ".codegen/extensions.json"
}

// ToolCommandNote tells the agent how to reach SpecWright's tools when its
// CLI did not load the agentbridge MCP server (extension/toolcli.go).
const ToolCommandNote = "\n\n(SpecWright's tools — save_flow, schedule_flow, run_flow, history_search, save_tool and its built-ins such as read_file or text_search — reach you in one of three ways; use the first one you have: " +
	"1) as agentbridge-<name> (Claude Code: mcp__agentbridge__<name>), from its MCP server; " +
	"2) as specwright_<name> and specwright_tool, from SpecWright's Copilot extension; " +
	"3) with your shell: \"$CODEGEN_TOOL\" tool list shows them, \"$CODEGEN_TOOL\" tool <name> --schema a tool's arguments, and \"$CODEGEN_TOOL\" tool <name> '<arguments as one JSON object>' runs it (exactly that form, nothing chained to it). " +
	"Search your tools for them if they are deferred. Never say a SpecWright tool is unavailable before trying 2 and 3.)"
