package core

// run_flow: an agent uses a finished flow as a building block — starts it
// with inputs and gets its outputs back (or reads a recent run's). The run
// is like any other: in Run flows and Run History (tagged "by an agent"),
// its commands and outward calls need their approvals. To keep agents from
// starting runs without end, at most maxAgentRuns go at once, a flow that
// is already running is not started again, and outputs come back cut to a
// size an AI can read (the whole results are in the run's folder).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codegen/internal/extension"
)

// RunFlowTool is the built-in agents start flows with.
const RunFlowTool = "run_flow"

const (
	maxAgentRuns     = 2
	agentOutputChars = 4000  // each output, in the answer
	agentAnswerChars = 12000 // all of them
)

func runFlowInfo() extension.BuiltinInfo {
	return extension.BuiltinInfo{
		Name:  RunFlowTool,
		Title: "Run a flow",
		Group: "flows",
		Description: "Run one of this project's SpecWright flows and get its outputs: give flow_id and its inputs (by input name; secret inputs come from the Keychain or are asked for). " +
			"It waits for the run (up to timeout_seconds) and answers with the status, each output (cut to a readable size) and the results folder. " +
			"Give run_id instead to read a run's result again. Use it to reuse a flow someone built instead of redoing its work. " +
			"The run has no review pauses unless review is \"ai\". It counts like any run: its AI steps use the model, and its commands and outgoing calls need their approvals.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"flow_id":         map[string]any{"type": "string", "description": "The flow's id (its file is .codegen/flows/<id>.json)"},
				"inputs":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Values for the flow's inputs, by name"},
				"review":          map[string]any{"type": "string", "enum": []string{"none", "ai"}, "description": "none (default): no pauses; ai: pause after AI steps for a person"},
				"wait":            map[string]any{"type": "boolean", "description": "Wait for the run to end (default true); false: start it and return its run id"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 10, "maximum": 3600, "description": "How long to wait (default 600)"},
				"run_id":          map[string]any{"type": "string", "description": "Read this run's result instead of starting one"},
			},
		},
	}
}

func (h *Hub) agentRunsLive() int {
	h.mu.RLock()
	var runs []*flowRun
	for _, fr := range h.flowRuns {
		runs = append(runs, fr)
	}
	h.mu.RUnlock()
	n := 0
	for _, fr := range runs {
		if st := fr.snapshot(); st.Options.Trigger != nil && st.Options.Trigger.Kind == "agent" && slicesContains(flowLive, st.Status) {
			n++
		}
	}
	return n
}

// agentRunFlow implements run_flow.
func (h *Hub) agentRunFlow(ctx context.Context, args map[string]any) (string, error) {
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	if id := str("run_id"); id != "" {
		st, err := h.FlowRun(id)
		if err != nil {
			return "", fmt.Errorf("no flow run %s", id)
		}
		return flowRunAnswer(st), nil
	}
	flowID := str("flow_id")
	if flowID == "" {
		return "", errors.New("give flow_id (the flow to run) or run_id (a run to read)")
	}
	if h.flowRunning(flowID) {
		return "", fmt.Errorf("the flow %s is already running; wait for it, or read its run with run_id", flowID)
	}
	if h.agentRunsLive() >= maxAgentRuns {
		return "", fmt.Errorf("%d flows started by agents are already running; wait for one to end", maxAgentRuns)
	}
	o := FlowRunOptions{Inputs: map[string]string{}, Review: "none", Trigger: &RunTrigger{Kind: "agent", ID: RunFlowTool, Name: "an agent (run_flow)"}}
	if in, ok := args["inputs"].(map[string]any); ok {
		for k, v := range in {
			if v != nil {
				o.Inputs[k] = fmt.Sprint(v)
			}
		}
	}
	if str("review") == "ai" {
		o.Review = "ai"
	}
	st, err := h.RunFlow(flowID, o)
	if err != nil {
		return "", err
	}
	if w, ok := args["wait"].(bool); ok && !w {
		return fmt.Sprintf("Started the flow %q: run %s. Read its result with run_flow run_id=%s.", st.FlowName, st.ID, st.ID), nil
	}
	timeout := 600 * time.Second
	if n, ok := args["timeout_seconds"].(float64); ok && n >= 10 {
		timeout = time.Duration(min(n, 3600)) * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		cur, err := h.FlowRun(st.ID)
		if err == nil && !slicesContains(flowLive, cur.Status) {
			return flowRunAnswer(cur), nil
		}
		if err == nil && cur.Status == "review" && o.Review == "none" {
			return flowRunAnswer(cur), nil
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("The flow %q is still running after %s (run %s, %s). Read its result later with run_flow run_id=%s.", st.FlowName, timeout, st.ID, cur.Status, st.ID), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// flowRunAnswer is a run's result, for an agent.
func flowRunAnswer(st FlowRunState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Flow %q, run %s: %s", st.FlowName, st.ID, st.Status)
	if st.Error != "" {
		fmt.Fprintf(&b, " (%s)", clipText(st.Error, 400))
	}
	if st.Status == "review" {
		b.WriteString(". It waits for a person's review in Run flows.")
	}
	used := 0
	for _, o := range st.Outputs {
		content := strings.TrimSpace(o.Content)
		if used >= agentAnswerChars {
			fmt.Fprintf(&b, "\n\n## %s (%s)\n(not shown: the answer is long enough; it is in the results folder)", o.Title, o.Format)
			continue
		}
		n := min(agentOutputChars, agentAnswerChars-used)
		cut := ""
		if len(content) > n {
			content, cut = content[:n], fmt.Sprintf("\n… cut; the whole output is in the run's results (%s)", strings.Join(o.Saved, ", "))
		}
		used += len(content)
		fmt.Fprintf(&b, "\n\n## %s (%s)\n%s%s", o.Title, o.Format, content, cut)
	}
	if len(st.Outputs) == 0 && st.Status == "done" {
		b.WriteString("\n(no outputs)")
	}
	return b.String()
}
