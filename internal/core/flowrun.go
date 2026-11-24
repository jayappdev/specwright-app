package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

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
	Inputs map[string]string `json:"inputs"`
	// Trigger: what started the run when it was not a person (a schedule)
	Trigger *RunTrigger `json:"trigger,omitempty"`
	// Files are files attached to an input (workspace paths): uploads for
	// file inputs, and pasted or dropped images, PDFs and documents for
	// clipboard and long-text inputs.
	Files              map[string][]string `json:"files,omitempty"`
	Review             string              `json:"review"` // none | marked | ai | each (default ai: AI steps and marked steps)
	Repeat             int                 `json:"repeat"`
	ForEach            *ForEach            `json:"forEach,omitempty"`
	TimeoutMinutes     int                 `json:"timeoutMinutes"`
	StepTimeoutSeconds int                 `json:"stepTimeoutSeconds"`
	StartAt            time.Time           `json:"startAt,omitzero"`
	Skip               []string            `json:"skip"`
	StopAfter          string              `json:"stopAfter"`
	Model              string              `json:"model"`
	// Reuse: steps that do not run again but give the output they gave in
	// an earlier run (trying a changed step: the steps before it are reused)
	Reuse map[string]ReusedStep `json:"reuse,omitempty"`
	// Secrets: the values of secret inputs. Kept in memory for the run only;
	// Inputs holds flow.SecretMask in their place, so the values never reach
	// the screen, run history or saved results.
	Secrets map[string]string `json:"-"`
	// SaveSecrets: secret inputs whose typed value is also saved in the
	// system keychain (at the input's keychain entry) for later runs.
	SaveSecrets []string `json:"saveSecrets,omitempty"`
	// prompt: secret inputs asked in a dialog once the run has started
	prompt []flow.Input
}

// values: the inputs the steps get, secrets included.
func (o FlowRunOptions) values() map[string]string {
	out := make(map[string]string, len(o.Inputs)+len(o.Secrets))
	for k, v := range o.Inputs {
		out[k] = v
	}
	for k, v := range o.Secrets {
		out[k] = v
	}
	return out
}

// ReusedStep is an earlier run's result of a step.
type ReusedStep struct {
	Output   string   `json:"output"`
	Branches []string `json:"branches,omitempty"` // a Decision's paths
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
	// in a loop: the item this step is on (1-based) and the item count; for
	// a Loop step itself, its progress through the list
	Iteration  int `json:"iteration,omitempty"`
	Iterations int `json:"iterations,omitempty"`
	Failed     int `json:"failedItems,omitempty"` // Loop: items that failed (with continue on error)
	// Rounds: an Improve step's rounds (Iteration / Iterations: the round)
	Rounds []ImproveRound `json:"rounds,omitempty"`
	// Args are the arguments a tool step ran with; Why says how the agent
	// chose them when the step was described in words.
	Args map[string]any `json:"args,omitempty"`
	// Session is the AI conversation the step's agent ran in (its last
	// call), with the agent's name: "Keep chatting" continues it, so the
	// context the step gathered is not fetched or sent again.
	Session      string `json:"session,omitempty"`
	SessionAgent string `json:"sessionAgent,omitempty"`
	Why          string `json:"why,omitempty"`
	// Context: what the step gathered and kept (flowrun_context.go)
	Context *flow.CtxReport `json:"context,omitempty"`
	// Confidence: how sure the AI was of a decision (percent; Why is its reason)
	Confidence int `json:"confidence,omitempty"`
	// Decision: the paths it took (branch ids) and their names
	Branches []string `json:"branches,omitempty"`
	Path     string   `json:"path,omitempty"`
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
	// an Improve step: the round, the AI's verdict and the rounds so far
	Round   int            `json:"round,omitempty"`
	Rounds  int            `json:"rounds,omitempty"`
	Verdict string         `json:"verdict,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	History []ImproveRound `json:"history,omitempty"`
	// a Decision the AI decides, unsure: the paths to choose from and the AI's
	// choice (flowrun_decide.go)
	Choices   []FlowChoice `json:"choices,omitempty"`
	Suggested string       `json:"suggested,omitempty"`
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
	// Trial: this run tried changed steps on an unsaved copy of the flow
	Trial *FlowTrial `json:"trial,omitempty"`
}

// FlowDecision answers a review.
type FlowDecision struct {
	Action  string     `json:"action"` // continue | stop | refine | command | edit | retry | no-more-reviews | next (an Improve step: another round, Prompt the feedback) | choose (a Decision: Branch)
	Prompt  string     `json:"prompt,omitempty"`
	Command string     `json:"command,omitempty"`
	Output  string     `json:"output,omitempty"`
	Node    *flow.Node `json:"node,omitempty"`   // retry with these settings
	Save    bool       `json:"save,omitempty"`   // keep the improvement in the flow
	Branch  string     `json:"branch,omitempty"` // choose: the path a person chose (a Decision the AI decides)
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
	gathered map[string][]flow.ItemResult // a Gather's results, for execStep
	// toolArgs: the arguments each tool step last ran with (secrets in
	// place; memory only), for an Improve step that runs it again
	toolArgs map[string]map[string]any
	// paired: an Improve step's tool step's last result, for runNodes
	paired map[string]pairedResult
}

type pairedResult struct {
	out, err string
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
	if err := h.prepareRun(root, f, &o); err != nil {
		return FlowRunState{}, err
	}
	return h.startFlow(root, f, o, nil)
}

// prepareRun checks f and the run's inputs (types, required values,
// secrets) before it starts.
func (h *Hub) prepareRun(root string, f *flow.Flow, o *FlowRunOptions) error {
	var errs []string
	for _, is := range h.ValidateFlow(*f) {
		if is.Level == "error" {
			errs = append(errs, is.Message)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("fix the flow first: %s", strings.Join(errs, " "))
	}
	if o.Inputs == nil {
		o.Inputs = map[string]string{}
	}
	for _, in := range f.Inputs {
		v, err := flowInputValue(root, in, o.Inputs[in.Name], o.Files[in.Name])
		if err != nil {
			return err
		}
		if in.Type == "secret" {
			if v, err = h.secretInput(in, v, o); err != nil {
				return err
			}
		}
		if in.Required && strings.TrimSpace(v) == "" && (o.ForEach == nil || o.ForEach.Input != in.Name) && !(in.Type == "secret" && in.SecretSource() == "prompt") {
			return fmt.Errorf("%s is required", firstNonEmpty(in.Label, in.Name))
		}
		if in.Type == "secret" {
			if o.ForEach != nil && o.ForEach.Input == in.Name {
				return fmt.Errorf("%s is a secret: a run cannot repeat once per line of it", firstNonEmpty(in.Label, in.Name))
			}
			if o.Secrets == nil {
				o.Secrets = map[string]string{}
			}
			o.Secrets[in.Name] = v
			if v != "" {
				v = flow.SecretMask
			}
		}
		o.Inputs[in.Name] = v
	}
	if o.ForEach != nil && o.ForEach.Filter != "" {
		if _, err := regexp.Compile(o.ForEach.Filter); err != nil {
			return fmt.Errorf("the filter is not a valid pattern: %v", err)
		}
	}
	return nil
}

// startFlow starts a run of f (a saved flow, or a trial copy).
func (h *Hub) startFlow(root string, f *flow.Flow, o FlowRunOptions, trial *FlowTrial) (FlowRunState, error) {
	// every way to run a flow (Run flows, trials, the built-in tool, headless) passes here
	if err := h.checkFlowCommands(root, f); err != nil {
		return FlowRunState{}, err
	}
	h.mu.RLock()
	agentName, model := h.agentName, h.model
	h.mu.RUnlock()
	if o.Model != "" {
		model = o.Model
	}
	for _, v := range o.Secrets { // hidden from every output, event and saved file
		h.Store.Redactor().Add(v)
	}
	runID := workflow.NewRunID()
	fr := &flowRun{flow: f, decide: make(chan FlowDecision, 1), done: make(chan struct{}), t0: time.Now()}
	fr.st = FlowRunState{ID: runID, FlowID: f.ID, FlowName: f.Name, Status: "running", Started: time.Now(), Options: o, Agent: agentName, Model: model,
		Outputs: []FlowOutputState{}, MinutesSaved: f.EstimatedMinutes(), Trial: trial}
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
	kind, title := "flow", f.Name
	if trial != nil {
		kind, title = "flow-trial", f.Name+" (trial)"
	}
	_ = h.Store.SaveRun(store.Run{ID: runID, Workspace: root, Workflow: f.ID, Step: "flow", Agent: agentName, Model: model, State: "running", Started: fr.st.Started, Kind: kind, Title: title, Ask: flowAsk(o.Inputs), Trigger: triggerTag(o.Trigger), TriggerName: triggerName(o.Trigger)})
	h.flowPublish(fr, "run.start", "Started flow "+f.Name, map[string]any{"flow": f.ID})
	h.flowState(fr)
	go h.execFlow(ctx, root, fr)
	return fr.snapshot(), nil
}

// triggerTag is how a triggered run is tagged in history ("scheduled").
func triggerTag(t *RunTrigger) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case "schedule":
		return "scheduled"
	case "flow":
		return "after flow"
	case "files":
		return "file change"
	case "agent":
		return "by an agent"
	}
	return t.Kind
}

func triggerName(t *RunTrigger) string {
	if t == nil {
		return ""
	}
	return t.Name
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
		if r.Step == "flow" && !r.Archived {
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
	argsOf := map[string]map[string]any{} // a tool's arguments in every step (for ${DB_SALES} of a DB_* family)
	note := func(tool, at string, args map[string]string) {
		used[tool] = true
		if argsOf[tool] == nil {
			argsOf[tool] = map[string]any{}
		}
		for k, v := range args {
			argsOf[tool][at+"."+k] = v
		}
	}
	for _, n := range f.Nodes {
		if n.Type == "tool" {
			note(n.Tool, n.ID, n.Args)
		}
		for i, a := range n.Actions {
			if a.Type == "tool" {
				note(a.Tool, fmt.Sprintf("%s.action%d", n.ID, i), a.Args)
			}
		}
	}
	byName := map[string]*FlowCredential{}
	var order []string
	for _, e := range h.Callable() {
		if !used[e.Name] {
			continue
		}
		for _, s := range extension.WithArgSecrets(e, argsOf[e.Name]).Secrets {
			if extension.IsSecretFamily(s) { // its members are listed as the steps name them
				continue
			}
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
