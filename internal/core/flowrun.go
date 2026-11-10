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
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
	"codegen/internal/extension"
	"codegen/internal/flow"
	"codegen/internal/store"
	"codegen/internal/workflow"
)

// Flow executor: runs a designed flow step by step.
//
//   - Options: inputs, repeat N times, run once per item of an input (split
//     by lines, a JSON array or CSV rows, kept by a filter pattern), a start
//     time, timeouts, steps to skip, a step to stop after, a model.
//   - Credentials: tools resolve their secrets as usual (session value,
//     environment variable, system keychain); a missing one pauses the run
//     and asks the user (a "secret.request" tagged with the run).
//   - Review: after AI steps, steps marked "pause for review", or every
//     step, the run waits for a decision: continue, stop, refine the output
//     with an AI prompt, run a command on it, edit it, or change the step's
//     settings and run it again. An improvement can be used once or saved
//     into the flow for later runs.
//   - Everything is published on the bus with the run's id ("flow.state"
//     snapshots plus the agent's and tools' events) and recorded in the
//     store and the usage ledger, so History, the Playground and Metrics
//     see flow runs.

// FlowRunOptions tune one run.
type FlowRunOptions struct {
	Inputs             map[string]string `json:"inputs"`
	Review             string            `json:"review"` // none | marked | ai | each (default ai: AI steps and marked steps)
	Repeat             int               `json:"repeat"`
	ForEach            *ForEach          `json:"forEach,omitempty"`
	TimeoutMinutes     int               `json:"timeoutMinutes"`
	StepTimeoutSeconds int               `json:"stepTimeoutSeconds"`
	StartAt            time.Time         `json:"startAt,omitzero"`
	Skip               []string          `json:"skip"`
	StopAfter          string            `json:"stopAfter"`
	Model              string            `json:"model"`
}

// ForEach runs the flow once per item of an input.
type ForEach struct {
	Input  string `json:"input"`  // the input that holds the list
	Split  string `json:"split"`  // lines | json | csv
	Filter string `json:"filter"` // keep items matching this pattern ("" = all)
	Max    int    `json:"max"`    // at most this many items (0 = 100)
}

// FlowStepState is one step's progress.
type FlowStepState struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Type       string      `json:"type"`
	Tool       string      `json:"tool,omitempty"`
	Status     string      `json:"status"` // pending | running | review | done | failed | skipped
	Output     string      `json:"output,omitempty"`
	Truncated  bool        `json:"truncated,omitempty"`
	Error      string      `json:"error,omitempty"`
	Started    time.Time   `json:"started,omitzero"`
	Ended      time.Time   `json:"ended,omitzero"`
	DurationMS int64       `json:"durationMs"`
	Attempts   int         `json:"attempts"`
	Notes      []string    `json:"notes,omitempty"`
	Usage      store.Usage `json:"usage"`
	// Args are the arguments a tool step ran with; Why says how the agent
	// chose them when the step was described in words.
	Args map[string]any `json:"args,omitempty"`
	Why  string         `json:"why,omitempty"`
	// InputFile and OutputFile are the step's saved input and output
	// (workspace paths in the run's results folder).
	InputFile  string `json:"inputFile,omitempty"`
	OutputFile string `json:"outputFile,omitempty"`
}

// FlowOutputState is one produced output.
type FlowOutputState struct {
	Node    string   `json:"node"`
	Title   string   `json:"title"`
	Format  string   `json:"format"`
	Content string   `json:"content"`
	Item    string   `json:"item,omitempty"`
	Saved   []string `json:"saved,omitempty"`
	Copied  bool     `json:"copied,omitempty"`
	Show    bool     `json:"show,omitempty"`
	Tools   []string `json:"tools,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

// FlowReview is what a paused run is waiting on.
type FlowReview struct {
	Step   string `json:"step"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Output string `json:"output"`
}

// FlowRunState is a run as the UI shows it.
type FlowRunState struct {
	ID           string            `json:"id"`
	FlowID       string            `json:"flowId"`
	FlowName     string            `json:"flowName"`
	Status       string            `json:"status"` // scheduled | running | review | done | failed | stopped | interrupted
	Error        string            `json:"error,omitempty"`
	Started      time.Time         `json:"started"`
	Ended        time.Time         `json:"ended,omitzero"`
	Options      FlowRunOptions    `json:"options"`
	Steps        []FlowStepState   `json:"steps"`
	Outputs      []FlowOutputState `json:"outputs"`
	Usage        store.Usage       `json:"usage"`
	Agent        string            `json:"agent"`
	Model        string            `json:"model,omitempty"`
	Pass         int               `json:"pass"`
	Passes       int               `json:"passes"`
	Item         int               `json:"item"`
	Items        int               `json:"items"`
	ItemLabel    string            `json:"itemLabel,omitempty"`
	Review       *FlowReview       `json:"review,omitempty"`
	Changes      []string          `json:"changes,omitempty"` // improvements saved to the flow
	MinutesSaved int               `json:"minutesSaved"`
}

// FlowDecision answers a review.
type FlowDecision struct {
	Action  string     `json:"action"` // continue | stop | refine | command | edit | retry | no-more-reviews
	Prompt  string     `json:"prompt,omitempty"`
	Command string     `json:"command,omitempty"`
	Output  string     `json:"output,omitempty"`
	Node    *flow.Node `json:"node,omitempty"` // retry with these settings
	Save    bool       `json:"save,omitempty"` // keep the improvement in the flow
}

type flowRun struct {
	mu       sync.Mutex
	st       FlowRunState
	flow     *flow.Flow
	cancel   context.CancelFunc
	decide   chan FlowDecision
	done     chan struct{}
	seq      int
	t0       time.Time
	noReview bool
}

// ErrFlowStopped ends a run the user stopped.
var errFlowStopped = errors.New("stopped")

type flowCtxKey struct{}
type flowCtxVal struct{ run, step string }

func withFlowStep(ctx context.Context, run, step string) context.Context {
	return context.WithValue(ctx, flowCtxKey{}, flowCtxVal{run, step})
}

// flowRunFrom returns the flow run and step a context belongs to.
func flowRunFrom(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	if v, ok := ctx.Value(flowCtxKey{}).(flowCtxVal); ok {
		return v.run, v.step
	}
	return "", ""
}

const maxStateText = 24 << 10

func clip(s string) (string, bool) {
	if len(s) <= maxStateText {
		return s, false
	}
	return s[:maxStateText], true
}

// --- API -------------------------------------------------------------------------

// RunFlow starts a flow run and returns its first state.
func (h *Hub) RunFlow(id string, o FlowRunOptions) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return FlowRunState{}, err
	}
	var errs []string
	for _, is := range h.ValidateFlow(*f) {
		if is.Level == "error" {
			errs = append(errs, is.Message)
		}
	}
	if len(errs) > 0 {
		return FlowRunState{}, fmt.Errorf("fix the flow first: %s", strings.Join(errs, " "))
	}
	if o.Inputs == nil {
		o.Inputs = map[string]string{}
	}
	for _, in := range f.Inputs {
		v, ok := o.Inputs[in.Name]
		if !ok || v == "" {
			v = in.Default
			if v == "" && len(in.Files) > 0 {
				v = strings.Join(in.Files, "\n")
			}
			o.Inputs[in.Name] = v
		}
		if in.Required && strings.TrimSpace(o.Inputs[in.Name]) == "" && (o.ForEach == nil || o.ForEach.Input != in.Name) {
			return FlowRunState{}, fmt.Errorf("%s is required", firstNonEmpty(in.Label, in.Name))
		}
	}
	if o.ForEach != nil && o.ForEach.Filter != "" {
		if _, err := regexp.Compile(o.ForEach.Filter); err != nil {
			return FlowRunState{}, fmt.Errorf("the filter is not a valid pattern: %v", err)
		}
	}
	h.mu.RLock()
	agentName, model := h.agentName, h.model
	h.mu.RUnlock()
	if o.Model != "" {
		model = o.Model
	}
	runID := workflow.NewRunID()
	fr := &flowRun{flow: f, decide: make(chan FlowDecision, 1), done: make(chan struct{}), t0: time.Now()}
	fr.st = FlowRunState{ID: runID, FlowID: f.ID, FlowName: f.Name, Status: "running", Started: time.Now(), Options: o, Agent: agentName, Model: model,
		Outputs: []FlowOutputState{}, MinutesSaved: f.EstimatedMinutes()}
	fr.st.Steps = stepStates(f)
	ctx, cancel := context.WithCancel(h.ctx)
	if o.TimeoutMinutes > 0 {
		ctx, cancel = context.WithTimeout(h.ctx, time.Duration(o.TimeoutMinutes)*time.Minute)
	}
	fr.cancel = cancel
	h.mu.Lock()
	if h.flowRuns == nil {
		h.flowRuns = map[string]*flowRun{}
	}
	h.flowRuns[runID] = fr
	h.mu.Unlock()
	_ = h.Store.SaveRun(store.Run{ID: runID, Workspace: root, Workflow: f.ID, Step: "flow", Agent: agentName, Model: model, State: "running", Started: fr.st.Started, Kind: "flow", Title: f.Name, Ask: flowAsk(o.Inputs)})
	h.flowPublish(fr, "run.start", "Started flow "+f.Name, map[string]any{"flow": f.ID})
	h.flowState(fr)
	go h.execFlow(ctx, root, fr)
	return fr.snapshot(), nil
}

// FlowRun returns a run's current (or final) state.
func (h *Hub) FlowRun(id string) (FlowRunState, error) {
	h.mu.RLock()
	fr := h.flowRuns[id]
	h.mu.RUnlock()
	if fr != nil {
		return fr.snapshot(), nil
	}
	evs, err := h.Store.Events(id)
	if err != nil {
		return FlowRunState{}, err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == "flow.state" {
			b, _ := json.Marshal(evs[i].Data["state"])
			var st FlowRunState
			if json.Unmarshal(b, &st) == nil {
				if st.Status == "running" || st.Status == "review" || st.Status == "scheduled" {
					st.Status = "interrupted"
				}
				return st, nil
			}
		}
	}
	return FlowRunState{}, fmt.Errorf("no flow run %q", id)
}

// FlowRuns lists recorded flow runs in this workspace, newest first.
func (h *Hub) FlowRuns(limit int) ([]store.Run, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	runs, err := h.Store.Runs(root, 500)
	if err != nil {
		return nil, err
	}
	out := []store.Run{}
	for _, r := range runs {
		if r.Step == "flow" {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DecideFlow answers a paused run.
func (h *Hub) DecideFlow(runID string, d FlowDecision) error {
	h.mu.RLock()
	fr := h.flowRuns[runID]
	h.mu.RUnlock()
	if fr == nil {
		return fmt.Errorf("run %s is not live", runID)
	}
	fr.mu.Lock()
	waiting := fr.st.Status == "review"
	fr.mu.Unlock()
	if !waiting {
		return errors.New("the run is not waiting for a review")
	}
	select {
	case fr.decide <- d:
		return nil
	default:
		return errors.New("a decision is already being applied")
	}
}

// StopFlowRun stops a run at once.
func (h *Hub) StopFlowRun(runID string) error {
	h.mu.RLock()
	fr := h.flowRuns[runID]
	h.mu.RUnlock()
	if fr == nil {
		return fmt.Errorf("run %s is not live", runID)
	}
	select {
	case fr.decide <- FlowDecision{Action: "stop"}:
	default:
	}
	fr.cancel()
	return nil
}

// FlowCredential is one secret a flow's tools need, and where it would come from.
type FlowCredential struct {
	SecretState
	Desc  string   `json:"desc"`
	Tools []string `json:"tools"`
}

// FlowCredentials lists the secrets the flow's tools need.
func (h *Hub) FlowCredentials(id string) ([]FlowCredential, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, n := range f.Nodes {
		if n.Type == "tool" {
			used[n.Tool] = true
		}
		for _, a := range n.Actions {
			if a.Type == "tool" {
				used[a.Tool] = true
			}
		}
	}
	byName := map[string]*FlowCredential{}
	var order []string
	for _, e := range h.Callable() {
		if !used[e.Name] {
			continue
		}
		for _, s := range e.Secrets {
			c := byName[s.Name]
			if c == nil {
				st := h.SecretInfo([]extension.Secret{s})[0]
				c = &FlowCredential{SecretState: st, Desc: s.Desc}
				byName[s.Name] = c
				order = append(order, s.Name)
			}
			c.Tools = append(c.Tools, e.Name)
		}
	}
	out := make([]FlowCredential, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// --- running ---------------------------------------------------------------------

func stepStates(f *flow.Flow) []FlowStepState {
	order, _ := f.Order()
	out := make([]FlowStepState, 0, len(order))
	for _, id := range order {
		n := f.Node(id)
		out = append(out, FlowStepState{ID: n.ID, Title: firstNonEmpty(n.Title, n.ID), Type: n.Type, Tool: n.Tool, Status: "pending"})
	}
	return out
}

func (fr *flowRun) snapshot() FlowRunState {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	b, _ := json.Marshal(fr.st)
	var c FlowRunState
	_ = json.Unmarshal(b, &c)
	return c
}

func (fr *flowRun) step(id string) *FlowStepState {
	for i := range fr.st.Steps {
		if fr.st.Steps[i].ID == id {
			return &fr.st.Steps[i]
		}
	}
	return nil
}

func (fr *flowRun) update(fn func(st *FlowRunState)) {
	fr.mu.Lock()
	fn(&fr.st)
	fr.mu.Unlock()
}

func (h *Hub) flowPublish(fr *flowRun, typ, summary string, data map[string]any) {
	fr.mu.Lock()
	fr.seq++
	seq := fr.seq
	fr.mu.Unlock()
	if data == nil {
		data = map[string]any{}
	}
	data["dt"] = time.Since(fr.t0).Milliseconds()
	step, _ := data["_step"].(string)
	delete(data, "_step")
	e := h.Bus.Publish(bus.Event{Seq: seq, RunID: fr.st.ID, Step: step, Type: typ, Summary: summary, Data: data})
	_ = h.Store.Append(e)
}

func (h *Hub) flowState(fr *flowRun) {
	st := fr.snapshot()
	h.flowPublish(fr, "flow.state", st.Status, map[string]any{"state": st, "flow": st.FlowID})
}

func (h *Hub) execFlow(ctx context.Context, root string, fr *flowRun) {
	defer close(fr.done)
	defer fr.cancel()
	o := fr.st.Options
	final, reason := "done", ""
	defer func() {
		fr.update(func(st *FlowRunState) {
			st.Status, st.Error, st.Ended, st.Review = final, reason, time.Now(), nil
			st.Usage.DurationMS = time.Since(st.Started).Milliseconds()
		})
		st := fr.snapshot()
		h.flowPublish(fr, "run.usage", fmt.Sprintf("Usage: %d in · %d out · %d tools · %d AI calls", st.Usage.InputTokens, st.Usage.OutputTokens, st.Usage.ToolCalls, st.Usage.ModelCalls),
			map[string]any{"input_tokens": st.Usage.InputTokens, "output_tokens": st.Usage.OutputTokens, "cost_usd": st.Usage.CostUSD, "premium_requests": st.Usage.PremiumRequests, "tool_calls": st.Usage.ToolCalls})
		h.flowState(fr)
		h.flowPublish(fr, "run.end", "Flow "+final, map[string]any{"state": final, "reason": reason})
		_ = h.Store.SaveRun(store.Run{ID: st.ID, Workspace: root, Workflow: st.FlowID, Step: "flow", Agent: st.Agent, Model: st.Model, State: final, Reason: reason, Started: st.Started, Ended: st.Ended, Events: fr.seq, Usage: st.Usage,
			Kind: "flow", Title: st.FlowName, Ask: flowAsk(st.Options.Inputs)})
		minutes := 0
		if final == "done" {
			minutes = st.MinutesSaved * max(1, st.Passes*max(1, st.Items))
		}
		_ = h.Store.Ledger(store.LedgerEntry{At: time.Now(), RunID: st.ID, Workspace: root, Workflow: "flow:" + st.FlowID, Step: "flow", Agent: st.Agent, Model: st.Model, State: final, Usage: st.Usage, MinutesSaved: minutes})
		h.mu.Lock()
		delete(h.flowRuns, st.ID)
		h.mu.Unlock()
	}()

	if !o.StartAt.IsZero() && time.Until(o.StartAt) > 0 {
		fr.update(func(st *FlowRunState) { st.Status = "scheduled" })
		h.flowState(fr)
		h.flowPublish(fr, "flow.scheduled", "Starts at "+o.StartAt.Local().Format("15:04, Jan 2"), nil)
		select {
		case <-time.After(time.Until(o.StartAt)):
		case <-ctx.Done():
			final, reason = "stopped", "stopped before it started"
			return
		case d := <-fr.decide:
			if d.Action == "stop" {
				final, reason = "stopped", "stopped before it started"
				return
			}
		}
		fr.update(func(st *FlowRunState) { st.Status = "running" })
	}

	items, err := flowItems(o)
	if err != nil {
		final, reason = "failed", err.Error()
		return
	}
	passes := max(1, min(o.Repeat, 50))
	fr.update(func(st *FlowRunState) { st.Passes, st.Items = passes, len(items) })
	failed := false
	for pass := 1; pass <= passes; pass++ {
		for i, item := range items {
			inputs := map[string]string{}
			for k, v := range o.Inputs {
				inputs[k] = v
			}
			label := ""
			if item != nil {
				inputs[o.ForEach.Input] = *item
				label = strings.TrimSpace(*item)
				if len(label) > 60 {
					label = label[:60] + "…"
				}
			}
			fr.update(func(st *FlowRunState) {
				st.Pass, st.Item, st.ItemLabel, st.Status = pass, i+1, label, "running"
				st.Steps = stepStates(fr.flow)
			})
			if passes > 1 || len(items) > 1 {
				h.flowPublish(fr, "flow.pass", passLabel(pass, passes, i+1, len(items), label), nil)
			}
			h.flowState(fr)
			err := h.execPass(ctx, root, fr, inputs, label)
			if errors.Is(err, errFlowStopped) {
				final, reason = "stopped", "stopped by you"
				return
			}
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					final, reason = "failed", fmt.Sprintf("the run took longer than %d minutes", o.TimeoutMinutes)
				} else {
					final, reason = "stopped", "stopped by you"
				}
				return
			}
			if err != nil {
				failed = true
				reason = err.Error()
			}
		}
	}
	if failed {
		final = "failed"
	}
}

func passLabel(pass, passes, item, items int, label string) string {
	var parts []string
	if passes > 1 {
		parts = append(parts, fmt.Sprintf("run %d of %d", pass, passes))
	}
	if items > 1 {
		parts = append(parts, fmt.Sprintf("item %d of %d", item, items))
	}
	s := strings.Join(parts, " · ")
	if label != "" {
		s += ": " + label
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// flowItems expands "run once per item"; one nil item means a single run.
func flowItems(o FlowRunOptions) ([]*string, error) {
	fe := o.ForEach
	if fe == nil || fe.Input == "" {
		return []*string{nil}, nil
	}
	src := o.Inputs[fe.Input]
	var raw []string
	switch fe.Split {
	case "json":
		var arr []any
		if err := json.Unmarshal([]byte(strings.TrimSpace(src)), &arr); err != nil {
			return nil, fmt.Errorf("%s is not a JSON array: %v", fe.Input, err)
		}
		for _, v := range arr {
			if s, ok := v.(string); ok {
				raw = append(raw, s)
			} else {
				b, _ := json.Marshal(v)
				raw = append(raw, string(b))
			}
		}
	case "csv":
		rows, err := flow.Transform(flow.Node{Op: "csv-to-json"}, src, flow.Data{})
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		_ = json.Unmarshal([]byte(rows), &arr)
		for _, r := range arr {
			b, _ := json.Marshal(r)
			raw = append(raw, string(b))
		}
	default:
		for _, l := range strings.Split(src, "\n") {
			if strings.TrimSpace(l) != "" {
				raw = append(raw, strings.TrimRight(l, "\r"))
			}
		}
	}
	var re *regexp.Regexp
	if fe.Filter != "" {
		re = regexp.MustCompile(fe.Filter)
	}
	limit := fe.Max
	if limit <= 0 {
		limit = 100
	}
	var out []*string
	for i := range raw {
		if re != nil && !re.MatchString(raw[i]) {
			continue
		}
		out = append(out, &raw[i])
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no items in %s%s", fe.Input, map[bool]string{true: " match the filter", false: ""}[re != nil])
	}
	return out, nil
}

func (h *Hub) execPass(ctx context.Context, root string, fr *flowRun, inputs map[string]string, item string) error {
	f := fr.flow
	o := fr.st.Options
	order, err := f.Order()
	if err != nil {
		return err
	}
	skip := map[string]bool{}
	for _, s := range o.Skip {
		skip[s] = true
	}
	d := flow.Data{Inputs: inputs, Steps: map[string]string{}, Context: h.flowContext(f), Skills: h.flowSkills(f), Flow: f.Name}
	status := map[string]string{}
	var firstErr error
	for _, id := range order {
		n := *f.Node(id) // a copy: retries may change it for this run only
		blocked := ""
		for _, p := range f.Parents(id) {
			if status[p] == "failed" {
				blocked = "a step before it failed"
			}
		}
		if skip[id] || blocked != "" {
			why := firstNonEmpty(blocked, "skipped by you")
			fr.update(func(st *FlowRunState) { s := fr.step(id); s.Status = "skipped"; s.Error = why })
			status[id] = "skipped"
			if blocked != "" {
				status[id] = "failed" // keep blocking the steps after it
			}
			h.flowState(fr)
			continue
		}
		out, err := h.runFlowStep(ctx, root, fr, &n, d, item)
		if errors.Is(err, errFlowStopped) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			status[id] = "failed"
			if firstErr == nil {
				firstErr = fmt.Errorf("%s failed: %v", firstNonEmpty(n.Title, n.ID), err)
			}
			continue
		}
		// review
		if h.wantsReview(fr, &n) {
			out, err = h.reviewStep(ctx, root, fr, &n, d, item, out)
			if err != nil {
				return err
			}
		}
		d.Steps[id] = out
		status[id] = "done"
		if o.StopAfter != "" && o.StopAfter == id {
			// mark the rest as skipped
			fr.update(func(st *FlowRunState) {
				for i := range st.Steps {
					if st.Steps[i].Status == "pending" {
						st.Steps[i].Status, st.Steps[i].Error = "skipped", "the run stops after "+firstNonEmpty(n.Title, n.ID)
					}
				}
			})
			h.flowState(fr)
			break
		}
	}
	return firstErr
}

func (h *Hub) wantsReview(fr *flowRun, n *flow.Node) bool {
	if fr.noReview || n.Type == "start" {
		return false
	}
	switch fr.st.Options.Review {
	case "none":
		return false
	case "each":
		return true
	case "marked":
		return n.Review
	}
	return n.Review || n.Type == "ai" || (n.Type == "transform" && n.Op == "ai")
}

// runFlowStep runs one step and records its state.
func (h *Hub) runFlowStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, item string) (string, error) {
	start := time.Now()
	fr.update(func(st *FlowRunState) {
		s := fr.step(n.ID)
		s.Status, s.Started, s.Error, s.Attempts = "running", start, "", s.Attempts+1
	})
	h.flowState(fr)
	h.flowPublish(fr, "flow.step", "Running "+firstNonEmpty(n.Title, n.ID), map[string]any{"_step": n.ID, "node": n.ID, "kind": n.Type, "status": "running"})
	out, u, err := h.execStep(ctx, root, fr, n, d, item)
	// secret values never travel on: not to AI steps, files, outputs or the screen
	out = h.Store.Redactor().String(out)
	if err != nil {
		err = errors.New(h.Store.Redactor().String(err.Error()))
	}
	fr.update(func(st *FlowRunState) {
		s := fr.step(n.ID)
		s.Ended, s.DurationMS = time.Now(), time.Since(start).Milliseconds()
		s.Usage.Add(u)
		st.Usage.Add(u)
		if err != nil {
			s.Status, s.Error = "failed", err.Error()
		} else {
			s.Status = "done"
		}
		s.Output, s.Truncated = clip(out)
	})
	summary := firstNonEmpty(n.Title, n.ID) + " done"
	if err != nil {
		summary = firstNonEmpty(n.Title, n.ID) + " failed: " + err.Error()
	}
	h.flowPublish(fr, "flow.step", summary, map[string]any{"_step": n.ID, "node": n.ID, "kind": n.Type, "status": map[bool]string{true: "failed", false: "done"}[err != nil], "duration_ms": time.Since(start).Milliseconds()})
	h.flowState(fr)
	return out, err
}

func stepTimeout(o FlowRunOptions, n *flow.Node) time.Duration {
	if o.StepTimeoutSeconds > 0 {
		return time.Duration(o.StepTimeoutSeconds) * time.Second
	}
	if n.Type == "ai" || (n.Type == "transform" && n.Op == "ai") || (n.Type == "tool" && strings.TrimSpace(n.Instruction) != "") {
		return 10 * time.Minute // includes the agent's investigation
	}
	return 2 * time.Minute
}

// toolRunTimeout is how long the tool itself may run.
func toolRunTimeout(o FlowRunOptions) time.Duration {
	if o.StepTimeoutSeconds > 0 {
		return time.Duration(o.StepTimeoutSeconds) * time.Second
	}
	return 2 * time.Minute
}

func (h *Hub) execStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, item string) (string, store.Usage, error) {
	var u store.Usage
	ctx, cancel := context.WithTimeout(withFlowStep(ctx, fr.st.ID, n.ID), stepTimeout(fr.st.Options, n))
	defer cancel()
	f := fr.flow
	parents := f.Parents(n.ID)
	in := ""
	for i, p := range parents {
		if i > 0 {
			in += "\n\n"
		}
		in += d.Steps[p]
	}
	d.Input = in
	switch n.Type {
	case "start":
		b, _ := json.MarshalIndent(d.Inputs, "", "  ")
		return string(b), u, nil
	case "tool":
		// the previous steps' output as a file, for {{input_file}} and $SPECWRIGHT_INPUT_FILE
		if d.Input != "" {
			if p, done := h.flowInputFile(fr, n.ID, d.Input); p != "" {
				defer done()
				d.InputFile = p
			}
		}
		args := map[string]any{}
		for k, v := range n.Args {
			if r := expandInputFile(flow.Render(v, d), d.InputFile); strings.TrimSpace(r) != "" || strings.TrimSpace(n.Instruction) == "" {
				args[k] = r
			}
		}
		why := ""
		if req := strings.TrimSpace(flow.Render(n.Instruction, d)); req != "" {
			// described in words: the agent investigates and builds the rest
			actx, acancel := context.WithTimeout(ctx, 10*time.Minute)
			got, w, au, err := h.aiToolArgs(actx, root, fr, n, req, args, d)
			acancel()
			u.Add(au)
			if err != nil {
				return "", u, err
			}
			for k, v := range got { // the agent may refer to the file as {{input_file}}
				if s, ok := v.(string); ok {
					got[k] = expandInputFile(flow.Render(s, d), d.InputFile)
				}
			}
			args, why = got, w
		}
		fr.update(func(st *FlowRunState) { s := fr.step(n.ID); s.Args, s.Why = args, why })
		out, err := h.flowTool(ctx, root, fr, n.ID, n.Tool, args, toolRunTimeout(fr.st.Options), flowInput{d.Input, d.InputFile})
		u.ToolCalls++
		if err != nil {
			u.ToolErrors++
		}
		return out, u, err
	case "ai":
		prompt := flow.Render(n.Prompt, d)
		// connected steps' output is always part of the prompt, even when
		// the prompt does not mention it
		if in := strings.TrimSpace(d.Input); in != "" && !flow.UsesUpstream(*n, f) {
			if len(in) > 60000 {
				in = in[:60000] + "\n…(shortened)"
			}
			prompt += "\n\n## Input from the previous step" + map[bool]string{true: "s", false: ""}[len(parents) > 1] + "\n```\n" + in + "\n```"
		}
		prompt += h.aiSuffix(f, d)
		return h.flowAgent(ctx, root, fr, n.ID, prompt, firstNonEmpty(n.Model, fr.st.Options.Model))
	case "merge":
		titles := map[string]string{}
		for _, p := range parents {
			if pn := f.Node(p); pn != nil && pn.Title != "" {
				titles[p] = pn.Title
			}
		}
		out, err := flow.Merge(*n, parents, d, titles)
		return out, u, err
	case "transform":
		if n.Source != "" {
			in = flow.Render(n.Source, d)
			d.Input = in
		}
		switch n.Op {
		case "ai":
			instr := flow.Render(n.Template, d)
			prompt := instr + "\n\nData:\n```\n" + in + "\n```\n\nReturn only the transformed data, with no explanation and no code fences." + h.aiSuffix(f, d)
			return h.flowAgent(ctx, root, fr, n.ID, prompt, fr.st.Options.Model)
		case "command":
			out, err := h.flowCommand(ctx, root, fr, n.ID, flow.Render(n.Expr, d), in)
			return out, u, err
		}
		out, err := flow.Transform(*n, in, d)
		return out, u, err
	case "output":
		content := in
		if strings.TrimSpace(n.Template) != "" {
			content = flow.Render(n.Template, d)
		}
		conv, err := flow.Convert(h.Store.Redactor().String(content), n.Format)
		if err != nil {
			return "", u, err
		}
		o := FlowOutputState{Node: n.ID, Title: firstNonEmpty(n.Title, n.ID), Format: n.Format, Item: item}
		o.Content, _ = clip(conv)
		d.Output = conv
		for _, a := range n.Actions {
			switch a.Type {
			case "save":
				p, err := h.saveFlowOutput(root, flow.Render(a.Path, d), conv)
				if err != nil {
					o.Errors = append(o.Errors, err.Error())
				} else {
					o.Saved = append(o.Saved, p)
					h.flowPublish(fr, "fs.write", "Saved "+p, map[string]any{"_step": n.ID, "path": p})
				}
			case "tool":
				od := d
				if p, done := h.flowInputFile(fr, n.ID+"-out", conv); p != "" {
					defer done()
					od.InputFile = p
				}
				args := map[string]any{}
				for k, v := range a.Args {
					args[k] = expandInputFile(flow.Render(v, od), od.InputFile)
				}
				_, err := h.flowTool(ctx, root, fr, n.ID, a.Tool, args, stepTimeout(fr.st.Options, n), flowInput{conv, od.InputFile})
				u.ToolCalls++
				if err != nil {
					u.ToolErrors++
					o.Errors = append(o.Errors, a.Tool+": "+err.Error())
				} else {
					o.Tools = append(o.Tools, a.Tool)
				}
			case "clipboard":
				if h.Clipboard != nil {
					if err := h.Clipboard(conv); err != nil {
						o.Errors = append(o.Errors, "copy: "+err.Error())
					} else {
						o.Copied = true
					}
				}
			case "show":
				o.Show = true
			}
		}
		fr.update(func(st *FlowRunState) { st.Outputs = append(st.Outputs, o) })
		h.flowPublish(fr, "flow.output", fmt.Sprintf("%s (%s)", o.Title, strings.ToUpper(n.Format)), map[string]any{"_step": n.ID, "node": n.ID, "format": n.Format, "saved": o.Saved})
		if len(o.Errors) > 0 {
			return conv, u, errors.New(strings.Join(o.Errors, "; "))
		}
		return conv, u, nil
	}
	return "", u, fmt.Errorf("unknown step type %q", n.Type)
}

// saveFlowOutput writes an output inside the workspace.
func (h *Hub) saveFlowOutput(root, rel, content string) (string, error) {
	rel = strings.TrimSpace(rel)
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is not a path inside the project", rel)
	}
	abs := filepath.Join(root, clean)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(clean), nil
}

func (h *Hub) flowContext(f *flow.Flow) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(f.Context.Text))
	if len(f.Context.Files) > 0 {
		b.WriteString("\n\nContext files (read them): ")
		b.WriteString(strings.Join(f.Context.Files, ", "))
	}
	return strings.TrimSpace(b.String())
}

func (h *Hub) flowSkills(f *flow.Flow) string {
	var parts []string
	for _, s := range f.Skills {
		parts = append(parts, s.Path)
	}
	return strings.Join(parts, "\n")
}

// aiSuffix adds the flow's context and skills to an AI prompt.
func (h *Hub) aiSuffix(f *flow.Flow, d flow.Data) string {
	var b strings.Builder
	if d.Context != "" {
		b.WriteString("\n\n## Context\n" + d.Context)
	}
	if len(f.Skills) > 0 {
		b.WriteString("\n\n## Skills (read these files and follow them)\n")
		for _, s := range f.Skills {
			fmt.Fprintf(&b, "- %s\n", s.Path)
		}
	}
	return b.String()
}

// flowTool calls a project, global or managed tool directly (as the bridge
// does): dependencies, secrets (asking the user if needed), then the run.
// flowInput is what a tool step receives from the steps connected into it.
type flowInput struct {
	text string // on standard input
	file string // the same, as a file ($SPECWRIGHT_INPUT_FILE); written by flowInputFile
}

// flowInputFile saves a step's input in the run's results folder
// (steps/<step>/input.txt) and returns its absolute path. The file is kept,
// so it can be opened after the run; done is a no-op kept for callers.
func (h *Hub) flowInputFile(fr *flowRun, step, text string) (string, func()) {
	name := "input.txt"
	if base, ok := strings.CutSuffix(step, "-out"); ok { // an output's "call a tool" action
		step, name = base, "action-input.txt"
	}
	rel, err := h.writeRunFile(stepFilesDir(fr.st.ID, step)+"/"+name, text)
	if err != nil {
		return "", func() {}
	}
	fr.update(func(st *FlowRunState) {
		if s := fr.step(step); s != nil && name == "input.txt" {
			s.InputFile = rel
		}
	})
	return filepath.Join(h.Root(), filepath.FromSlash(rel)), func() {}
}

var inputFileVar = regexp.MustCompile(`\$\{?SPECWRIGHT_INPUT_FILE\}?`)

// expandInputFile replaces a literal $SPECWRIGHT_INPUT_FILE typed into a
// field with the file's path: tools run without a shell, so nothing else
// would expand it.
func expandInputFile(s, path string) string {
	if path == "" || !strings.Contains(s, "SPECWRIGHT_INPUT_FILE") {
		return s
	}
	return inputFileVar.ReplaceAllLiteralString(s, path)
}

func (h *Hub) flowTool(ctx context.Context, root string, fr *flowRun, step, name string, args map[string]any, timeout time.Duration, input ...flowInput) (string, error) {
	var e *extension.Extension
	for _, x := range h.Callable() {
		if x.Name == name {
			x := x
			e = &x
		}
	}
	if e == nil {
		return "", fmt.Errorf("the tool %s is not available or not approved", name)
	}
	id := fmt.Sprintf("%s-%d", step, time.Now().UnixNano())
	h.flowPublish(fr, "tool.call", name+" "+argSummary(args), map[string]any{"_step": step, "tool": name, "args": args, "id": id})
	fail := func(err error) (string, error) {
		h.flowPublish(fr, "tool.result", name+" failed", map[string]any{"_step": step, "tool": name, "id": id, "ok": false, "output": err.Error()})
		return "", err
	}
	if err := e.Runnable(); err != nil {
		return fail(err)
	}
	if extension.HasDeps(*e) {
		if err := h.Prepare(ctx, *e); err != nil {
			return fail(fmt.Errorf("installing dependencies failed: %v", err))
		}
	}
	secrets, err := h.ResolveSecrets(ctx, *e)
	if err != nil {
		return fail(fmt.Errorf("credentials: %v", err))
	}
	// the previous steps' output, always handed over: on standard input and
	// as a file named by $SPECWRIGHT_INPUT_FILE (tools can use either)
	env := h.Env()
	var stdin *string
	if len(input) > 0 && input[0].text != "" {
		in := input[0]
		stdin = &in.text
		if in.file == "" {
			if p, done := h.flowInputFile(fr, step, in.text); p != "" {
				defer done()
				in.file = p
			}
		}
		if in.file != "" {
			env = append(env, "SPECWRIGHT_INPUT_FILE="+in.file)
		}
		env = append(env, "SPECWRIGHT_STEP="+step, "SPECWRIGHT_RUN="+fr.st.ID)
	}
	res := extension.ExecInput(ctx, root, *e, args, secrets, env, timeout, stdin)
	// keep what the tool was called with and what it returned
	files := map[string]any{}
	base := stepFilesDir(fr.st.ID, strings.TrimSuffix(step, "-out"))
	prefix := ""
	fr.update(func(st *FlowRunState) {
		if fs := fr.step(step); fs == nil || fs.Type == "output" {
			prefix = unsafeFile.ReplaceAllString(name, "_") + "-" // an output action's tool
		}
	})
	if b, err := json.MarshalIndent(args, "", "  "); err == nil {
		if rel, err := h.writeRunFile(base+"/"+prefix+"args.json", string(b)); err == nil {
			files["args_file"] = rel
		}
	}
	if rel, err := h.writeRunFile(base+"/"+prefix+"output."+contentExt(res.Output), res.Output); err == nil {
		files["output_file"] = rel
		if prefix == "" {
			fr.update(func(st *FlowRunState) {
				if s := fr.step(step); s != nil {
					s.OutputFile = rel
				}
			})
		}
	}
	if len(input) > 0 && input[0].file != "" {
		if rel, err := filepath.Rel(h.Root(), input[0].file); err == nil {
			files["input_file"] = filepath.ToSlash(rel)
		}
	}
	data := map[string]any{"_step": step, "tool": name, "id": id, "ok": res.OK, "output": res.Output, "exit_code": res.ExitCode, "duration_ms": res.DurationMS}
	for k, v := range files {
		data[k] = v
	}
	h.flowPublish(fr, "tool.result", fmt.Sprintf("%s %s · %dms", name, map[bool]string{true: "ok", false: "failed"}[res.OK], res.DurationMS), data)
	if !res.OK {
		msg := firstNonEmpty(res.Error, fmt.Sprintf("exit code %d", res.ExitCode))
		if t := strings.TrimSpace(res.Output); t != "" {
			if len(t) > 600 {
				t = t[len(t)-600:]
			}
			msg += ": " + t
		}
		return res.Output, errors.New(msg)
	}
	return res.Output, nil
}

func argSummary(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprint(args[k])
		if len(v) > 40 {
			v = v[:40] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// flowCommand runs a shell command with data on stdin.
func (h *Hub) flowCommand(ctx context.Context, root string, fr *flowRun, step, command, stdin string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("no command")
	}
	id := fmt.Sprintf("%s-cmd-%d", step, time.Now().UnixNano())
	h.flowPublish(fr, "tool.call", "$ "+command, map[string]any{"_step": step, "tool": "command", "args": map[string]any{"command": command}, "id": id})
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	cmd.Dir = root
	cmd.Env = append(os.Environ(), h.agentPackageEnv(root)...)
	cmd.Stdin = strings.NewReader(stdin)
	start := time.Now()
	out, err := cmd.Output()
	ok := err == nil
	msg := string(out)
	if ee, isExit := err.(*exec.ExitError); isExit && len(ee.Stderr) > 0 {
		msg = strings.TrimSpace(string(ee.Stderr))
	}
	h.flowPublish(fr, "tool.result", fmt.Sprintf("command %s · %dms", map[bool]string{true: "ok", false: "failed"}[ok], time.Since(start).Milliseconds()),
		map[string]any{"_step": step, "tool": "command", "id": id, "ok": ok, "output": msg, "duration_ms": time.Since(start).Milliseconds()})
	if err != nil {
		return "", fmt.Errorf("%s: %v %s", command, err, strings.TrimSpace(msg))
	}
	return string(out), nil
}

// flowAgent runs one AI prompt with the chosen agent, forwarding its
// events into the run and returning its answer.
func (h *Hub) flowAgent(ctx context.Context, root string, fr *flowRun, step, prompt, model string) (string, store.Usage, error) {
	var u store.Usage
	h.mu.RLock()
	name := h.agentName
	if model == "" {
		model = h.model
	}
	h.mu.RUnlock()
	drv, err := h.Agents.Get(name)
	if err != nil {
		return "", u, err
	}
	mcp, done, err := h.mcpConfig(true)
	if err != nil {
		return "", u, err
	}
	defer done()
	var text strings.Builder
	var mu sync.Mutex
	emit := func(ev agent.Event) {
		mu.Lock()
		switch ev.Type {
		case "model.request":
			u.InputTokens += toInt(ev.Data["input_tokens"])
			u.CachedTokens += toInt(ev.Data["cached_tokens"])
			u.ModelCalls++
			if m, _ := ev.Data["model"].(string); m != "" {
				fr.update(func(st *FlowRunState) { st.Model = m })
			}
		case "model.response":
			u.OutputTokens += toInt(ev.Data["output_tokens"])
		case "model.text":
			if t, _ := ev.Data["text"].(string); t != "" {
				if text.Len() > 0 {
					text.WriteString("\n\n")
				}
				text.WriteString(t)
			}
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
		}
		mu.Unlock()
		data := map[string]any{"_step": step}
		for k, v := range ev.Data {
			data[k] = v
		}
		typ := ev.Type
		if typ == "run.usage" {
			typ = "model.usage"
		}
		h.flowPublish(fr, typ, ev.Summary, data)
	}
	res, err := drv.Run(ctx, agent.Request{Purpose: "flow", Step: step, Prompt: prompt, Dir: root, Model: model, Env: h.childEnv(), MCPConfig: mcp,
		AllowTools: []string{"mcp__agentbridge"}}, emit)
	if res.CostUSD > 0 && u.CostUSD == 0 {
		u.CostUSD = res.CostUSD
	}
	if res.PremiumRequests > 0 && u.PremiumRequests == 0 {
		u.PremiumRequests = res.PremiumRequests
	}
	out := strings.TrimSpace(res.Text)
	if out == "" {
		out = strings.TrimSpace(text.String())
	}
	if err != nil {
		return out, u, err
	}
	if out == "" {
		return "", u, errors.New("the agent returned no answer")
	}
	return out, u, nil
}

// --- review and iteration -------------------------------------------------------

func (h *Hub) reviewStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, item, out string) (string, error) {
	for {
		clipped, _ := clip(out)
		fr.update(func(st *FlowRunState) {
			st.Status = "review"
			st.Review = &FlowReview{Step: n.ID, Title: firstNonEmpty(n.Title, n.ID), Type: n.Type, Output: clipped}
			s := fr.step(n.ID)
			s.Status, s.Output = "review", clipped
		})
		h.flowState(fr)
		h.flowPublish(fr, "flow.review", "Waiting for your review of "+firstNonEmpty(n.Title, n.ID), map[string]any{"_step": n.ID, "node": n.ID})
		var dec FlowDecision
		select {
		case dec = <-fr.decide:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		fr.update(func(st *FlowRunState) { st.Status, st.Review = "running", nil })
		note := ""
		switch dec.Action {
		case "continue", "":
			fr.update(func(st *FlowRunState) { fr.step(n.ID).Status = "done" })
			h.flowPublish(fr, "gate.approved", "Accepted "+firstNonEmpty(n.Title, n.ID), map[string]any{"_step": n.ID})
			h.flowState(fr)
			return out, nil
		case "no-more-reviews":
			fr.noReview = true
			fr.update(func(st *FlowRunState) { fr.step(n.ID).Status = "done" })
			h.flowPublish(fr, "gate.approved", "Accepted; no more reviews in this run", map[string]any{"_step": n.ID})
			h.flowState(fr)
			return out, nil
		case "stop":
			fr.update(func(st *FlowRunState) { fr.step(n.ID).Status = "done" })
			h.flowPublish(fr, "gate.rejected", "Stopped at "+firstNonEmpty(n.Title, n.ID), map[string]any{"_step": n.ID})
			return "", errFlowStopped
		case "edit":
			out = dec.Output
			note = "Edited by hand"
		case "refine":
			fb := strings.TrimSpace(dec.Prompt)
			if fb == "" {
				continue
			}
			h.flowPublish(fr, "gate.changes", "Refining with AI: "+fb, map[string]any{"_step": n.ID, "feedback": fb})
			var prompt string
			if n.Type == "ai" {
				prompt = flow.Render(n.Prompt, d) + "\n\nYour previous answer:\n" + out + "\n\nRevise it: " + fb + h.aiSuffix(fr.flow, d)
			} else {
				prompt = fb + "\n\nData:\n```\n" + out + "\n```\n\nReturn only the revised data, with no explanation and no code fences." + h.aiSuffix(fr.flow, d)
			}
			got, u, err := h.flowAgent(withFlowStep(ctx, fr.st.ID, n.ID), root, fr, n.ID, prompt, fr.st.Options.Model)
			fr.update(func(st *FlowRunState) { st.Usage.Add(u); fr.step(n.ID).Usage.Add(u) })
			if err != nil {
				h.flowPublish(fr, "error", "Refining failed: "+err.Error(), map[string]any{"_step": n.ID})
				continue
			}
			out, note = got, "Refined with AI: "+fb
			if dec.Save {
				h.saveImprovement(root, fr, n, "refine", fb)
			}
		case "command":
			cmd := strings.TrimSpace(dec.Command)
			if cmd == "" {
				continue
			}
			got, err := h.flowCommand(ctx, root, fr, n.ID, cmd, out)
			if err != nil {
				h.flowPublish(fr, "error", err.Error(), map[string]any{"_step": n.ID})
				continue
			}
			out, note = got, "Ran: "+cmd
			if dec.Save {
				h.saveImprovement(root, fr, n, "command", cmd)
			}
		case "retry":
			if dec.Node != nil {
				id, typ := n.ID, n.Type
				*n = *dec.Node
				n.ID, n.Type = id, typ
			}
			got, err := h.runFlowStep(ctx, root, fr, n, d, item)
			if err != nil {
				continue // the step shows the error; the user decides again
			}
			out, note = got, "Ran again with changed settings"
			if dec.Save && dec.Node != nil {
				h.saveImprovement(root, fr, n, "settings", "")
			}
		default:
			continue
		}
		if note != "" {
			clipped, _ := clip(out)
			fr.update(func(st *FlowRunState) { s := fr.step(n.ID); s.Notes = append(s.Notes, note); s.Output = clipped })
		}
	}
}

// saveImprovement writes an improvement into the saved flow (the running
// copy already has the new output).
func (h *Hub) saveImprovement(root string, fr *flowRun, n *flow.Node, kind, text string) {
	f, err := flow.Load(root, fr.flow.ID)
	if err != nil {
		return
	}
	cur := f.Node(n.ID)
	if cur == nil {
		return
	}
	var msg string
	switch kind {
	case "refine":
		if cur.Type == "ai" {
			cur.Prompt = strings.TrimRight(cur.Prompt, "\n") + "\n\nAlso: " + text
			msg = fmt.Sprintf("%s: added to the prompt: %q", firstNonEmpty(cur.Title, cur.ID), text)
		} else {
			added := insertAfter(f, cur.ID, flow.Node{Type: "transform", Op: "ai", Title: "Refine " + firstNonEmpty(cur.Title, cur.ID), Template: text})
			msg = fmt.Sprintf("added the AI step %q after %s", added, firstNonEmpty(cur.Title, cur.ID))
		}
	case "command":
		added := insertAfter(f, cur.ID, flow.Node{Type: "transform", Op: "command", Title: "Command after " + firstNonEmpty(cur.Title, cur.ID), Expr: text})
		msg = fmt.Sprintf("added the command step %q after %s", added, firstNonEmpty(cur.Title, cur.ID))
	case "settings":
		x, y, id, typ := cur.X, cur.Y, cur.ID, cur.Type
		*cur = *n
		cur.X, cur.Y, cur.ID, cur.Type = x, y, id, typ
		msg = fmt.Sprintf("saved the new settings of %s", firstNonEmpty(cur.Title, cur.ID))
	}
	if err := flow.Save(root, f); err != nil {
		return
	}
	fr.update(func(st *FlowRunState) { st.Changes = append(st.Changes, msg) })
	h.flowPublish(fr, "flow.saved", "Saved to the flow: "+msg, map[string]any{"_step": n.ID})
}

// insertAfter adds a step between id and the steps it feeds.
func insertAfter(f *flow.Flow, id string, n flow.Node) string {
	base := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(n.Op))
	nid, k := base, 2
	for f.Node(nid) != nil {
		nid = fmt.Sprintf("%s%d", base, k)
		k++
	}
	after := f.Node(id)
	n.ID, n.X, n.Y = nid, after.X+40, after.Y+130
	f.Nodes = append(f.Nodes, n)
	for i := range f.Edges {
		if f.Edges[i].From == id {
			f.Edges[i].From = nid
		}
	}
	f.Edges = append(f.Edges, flow.Edge{From: id, To: nid})
	// templates that read the old step now read the new one
	re := regexp.MustCompile(`\{\{\s*steps\.` + regexp.QuoteMeta(id) + `(\s*\}\}|\.)`)
	for i := range f.Nodes {
		x := &f.Nodes[i]
		if x.ID == nid || x.ID == id || !f.Ancestors(x.ID)[nid] {
			continue
		}
		fix := func(s string) string { return re.ReplaceAllString(s, "{{steps."+nid+"$1") }
		x.Prompt, x.Template, x.Source = fix(x.Prompt), fix(x.Template), fix(x.Source)
		for k, v := range x.Args {
			x.Args[k] = fix(v)
		}
	}
	return nid
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	}
	return 0
}

// flowAsk describes a flow run's inputs for history.
func flowAsk(inputs map[string]string) string {
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.TrimSpace(inputs[k])
		if v == "" {
			continue
		}
		if len(v) > 300 {
			v = v[:300] + "…"
		}
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	return strings.TrimSpace(b.String())
}
