package core

// Flow runs: Improve steps ("Improve until done") — a tool step and the AI
// run as a pair, round after round, until the AI judges the result good
// enough (and, when asked, a person approves it). See flow/improve.go.
//
// Context: every round the AI gets the goal, the current result, the
// check's findings and a short record of the earlier rounds — not their
// full text. Long results are cut (start and end kept) and saved in the
// run's results folder (steps/<step>/rounds/), where the agent can read
// them whole. The flow's context and skills are added as for AI steps.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"codegen/internal/bigout"
	"codegen/internal/flow"
	"codegen/internal/store"
)

// ImproveRound is one round of an Improve step, as the run shows it.
type ImproveRound struct {
	Round   int    `json:"round"`
	By      string `json:"by"` // ai | person
	Done    bool   `json:"done"`
	Reason  string `json:"reason,omitempty"`  // the AI's verdict, or the person's feedback
	Changes string `json:"changes,omitempty"` // what it changed for the next round
	File    string `json:"file,omitempty"`    // the round's result, in the run's results folder
}

// How much of each text goes into a round's prompt (bytes).
const (
	improveResultBudget = 24 << 10
	improveCheckBudget  = 16 << 10
	improveInputBudget  = 8 << 10
	improveArgsBudget   = 4 << 10
	improveHistoryFull  = 8 // the last records described in full; older ones in one line
	improveAITimeout    = 10 * time.Minute
)

// improveVerdict is the AI's reply in a round.
type improveVerdict struct {
	Done   bool           `json:"done"`
	Reason string         `json:"reason"`
	Args   map[string]any `json:"args,omitempty"`
	Input  *string        `json:"input,omitempty"`
	Output *string        `json:"output,omitempty"`
}

// improvePair is the state of the pair between rounds.
type improvePair struct {
	n, t    *flow.Node // the Improve step and its tool step
	after   bool       // the tool checks the AI's result (else the AI fixes the tool's run)
	result  string     // the result being judged: the tool's output (before) or the AI's (after)
	check   string     // after: the tool's findings
	toolErr string     // the tool's error, if its last run failed
	input   string     // before: what the tool gets from the steps before it
	args    map[string]any
	// after: the AI's changes to the check's arguments, kept across rounds
	argChanges map[string]any
	file       string // the result's file in the results folder
	history    []ImproveRound
	feedback   string // a person's feedback, for the next round
	judged     bool   // the judge's reason was kept as a lesson in this run
}

func (h *Hub) runImprove(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, parents int) (string, store.Usage, error) {
	var u store.Usage
	f := fr.flow
	pid, err := f.ImprovePartner(*n)
	if err != nil {
		return "", u, err
	}
	t := *f.Node(pid)
	p := &improvePair{n: n, t: &t, after: flow.ImprovePair(*n) == "after"}
	limit := flow.ImproveRounds(*n)
	title := firstNonEmpty(n.Title, n.ID)
	model := firstNonEmpty(n.Model, fr.st.Options.Model)
	ask := func(prompt string) (string, error) {
		actx, cancel := context.WithTimeout(ctx, improveAITimeout)
		defer cancel()
		out, uu, err := h.flowAgent(actx, root, fr, n.ID, prompt, model)
		u.Add(uu)
		fr.update(func(st *FlowRunState) { st.Usage.Add(uu) })
		return out, err
	}
	defer func() { // the tool step's last result goes on as its output
		res := pairedResult{out: p.check, err: p.toolErr}
		if !p.after {
			res.out = p.result
		}
		fr.mu.Lock()
		if fr.paired == nil {
			fr.paired = map[string]pairedResult{}
		}
		fr.paired[pid] = res
		fr.mu.Unlock()
	}()

	if p.after {
		// the AI's first result, then the tool's check of it
		prompt := h.aiStepPrompt(f, n, d, parents) +
			fmt.Sprintf("\n\n%s checks your result next. It is good enough when: %s", toolTitle(&t), flow.Render(n.Goal, d))
		out, err := ask(prompt)
		if err != nil {
			return "", u, err
		}
		p.result = p.shape(out)
		h.improveCheck(ctx, root, fr, p, d, nil)
	} else {
		// the tool already ran: judge its result (or its failure)
		p.result = d.Steps[pid]
		var ins []string
		for _, q := range f.Parents(pid) {
			ins = append(ins, d.Steps[q])
		}
		p.input = strings.Join(ins, "\n\n")
		fr.mu.Lock()
		p.args = maps.Clone(fr.toolArgs[pid])
		fr.mu.Unlock()
		fr.update(func(st *FlowRunState) {
			if s := fr.step(pid); s != nil && s.Status == "failed" {
				p.toolErr = s.Error
			}
		})
	}

	approve := flow.ImproveApproval(*n)
	if fr.noReview || fr.st.Options.Review == "none" {
		approve = "none"
	}
	for round := 1; ; round++ {
		p.file = h.improveFile(fr, n.ID, round, p.result)
		fr.update(func(st *FlowRunState) {
			s := fr.step(n.ID)
			s.Iteration, s.Iterations = round, limit
		})
		h.flowState(fr)
		v, err := h.improveJudge(ask, fr, p, d, round, limit)
		if err != nil {
			return "", u, err
		}
		feedback := p.feedback
		p.feedback = ""
		if feedback != "" && v.Done { // a person asked for changes: not done until they are made
			v.Done = false
		}
		final := p.result
		if v.Done && !p.after && v.Output != nil && strings.TrimSpace(*v.Output) != "" {
			final = *v.Output
		}
		changes := p.changes(v)
		rec := ImproveRound{Round: round, By: "ai", Done: v.Done, Reason: clipText(v.Reason, 600), File: p.file}
		if !v.Done && !p.judged && feedback == "" { // the judge's first "not yet" in this run (lessons.go)
			p.judged = true
			h.learnFromJudge(fr, n.ID, v.Reason)
		}
		if !v.Done {
			rec.Changes = changes
		}
		p.history = append(p.history, rec)
		verdict := map[bool]string{true: "done", false: "not done yet"}[v.Done]
		h.flowPublish(fr, "flow.improve", fmt.Sprintf("%s · round %d of %d: %s — %s", title, round, limit, verdict, clipText(v.Reason, 200)),
			map[string]any{"_step": n.ID, "round": round, "rounds": limit, "done": v.Done, "reason": v.Reason, "changes": rec.Changes})
		h.improveState(fr, p)

		stuck := !v.Done && changes == ""
		last := !v.Done && round >= limit
		if approve == "each" || (approve == "done" && (v.Done || stuck || last)) {
			why := verdict
			switch {
			case stuck:
				why = "stuck: the AI proposed no changes"
			case last:
				why = fmt.Sprintf("not done after %d rounds", round)
			}
			dec, err := h.improveReview(ctx, fr, p, round, limit, why, v.Reason, final)
			if err != nil {
				return "", u, err
			}
			switch dec.Action {
			case "continue", "", "no-more-reviews":
				if dec.Action == "no-more-reviews" {
					fr.noReview, approve = true, "none"
				}
				h.improveNote(fr, n.ID, fmt.Sprintf("Approved by you after round %d", round))
				return final, u, nil
			case "edit":
				h.learnFromEdit(fr, n.ID, final, dec.Output) // a small correction is a lesson (lessons.go)
				h.improveNote(fr, n.ID, fmt.Sprintf("Edited by you after round %d", round))
				return dec.Output, u, nil
			case "stop":
				return "", u, errFlowStopped
			case "next": // another round, with the person's feedback if any
				fb := strings.TrimSpace(dec.Prompt)
				if fb != "" {
					h.learnFromReview(fr, n.ID, fb, "improve") // later runs of this step get it (lessons.go)
					p.history = append(p.history, ImproveRound{Round: round, By: "person", Reason: clipText(fb, 2000)})
					h.flowPublish(fr, "gate.changes", "Another round: "+fb, map[string]any{"_step": n.ID, "feedback": fb})
					h.improveState(fr, p)
				}
				if round >= limit {
					limit = round + 1 // the person asked for one more
				}
				if fb != "" || v.Done || stuck {
					// judge the same result again, with the feedback
					p.feedback = firstNonEmpty(fb, "Not good enough yet. Find what to improve and change it.")
					continue
				}
			default:
				return "", u, fmt.Errorf("unknown decision %q", dec.Action)
			}
		} else {
			if v.Done {
				return final, u, nil
			}
			if stuck || last {
				why := fmt.Sprintf("not done after %d round%s", round, map[bool]string{true: "", false: "s"}[round == 1])
				if stuck {
					why = fmt.Sprintf("stuck after %d round%s: the AI proposed no changes", round, map[bool]string{true: "", false: "s"}[round == 1])
				}
				if n.ContinueOnError {
					h.improveNote(fr, n.ID, why+"; the last result goes on")
					return p.result, u, nil
				}
				return "", u, fmt.Errorf("%s: %s", why, clipText(v.Reason, 300))
			}
		}
		// next round: apply the changes
		if p.after {
			if v.Output != nil && strings.TrimSpace(*v.Output) != "" {
				p.result = p.shape(*v.Output)
			}
			h.improveCheck(ctx, root, fr, p, d, v.Args)
		} else {
			h.improveRerun(ctx, root, fr, p, d, v, round+1)
		}
		if ctx.Err() != nil {
			return "", u, ctx.Err()
		}
	}
}

// shape tidies an AI result that is to be JSON.
func (p *improvePair) shape(out string) string {
	if p.n.OutputSchema != nil {
		return jsonReply(out)
	}
	return strings.TrimSpace(out)
}

// changes describes what a verdict changes ("" when nothing).
func (p *improvePair) changes(v improveVerdict) string {
	var parts []string
	if len(v.Args) > 0 {
		parts = append(parts, "arguments: "+strings.Join(sortedKeysAny(v.Args), ", "))
	}
	if v.Input != nil && !p.after {
		parts = append(parts, "the tool's input")
	}
	if v.Output != nil && p.after && strings.TrimSpace(*v.Output) != "" && strings.TrimSpace(*v.Output) != p.result {
		parts = append(parts, "the result")
	}
	return strings.Join(parts, "; ")
}

func sortedKeysAny(m map[string]any) []string {
	out := slices.Collect(maps.Keys(m))
	slices.Sort(out)
	return out
}

// mergeArgs puts the AI's argument changes over args. Values that hold a
// secret stay as they are (the AI only saw them masked).
func (h *Hub) mergeArgs(args, changes map[string]any) map[string]any {
	out := maps.Clone(args)
	if out == nil {
		out = map[string]any{}
	}
	red := h.Store.Redactor()
	for k, v := range changes {
		if old, ok := out[k].(string); ok && red.String(old) != old {
			continue
		}
		out[k] = v
	}
	return out
}

// improveCheck runs the checking tool (pair after) on the current result.
func (h *Hub) improveCheck(ctx context.Context, root string, fr *flowRun, p *improvePair, d flow.Data, changes map[string]any) {
	dd := d
	dd.Steps = maps.Clone(d.Steps)
	dd.Steps[p.n.ID] = p.result // {{steps.<improve>}} in the tool's arguments
	dd.Input, dd.Live = p.result, nil
	if ip, _ := h.flowInputFile(fr, p.t.ID, p.result); ip != "" {
		dd.InputFile = ip
	}
	// the step's own arguments, filled from the current result, with the
	// AI's changes so far on top
	args := map[string]any{}
	for k, v := range p.t.Args {
		args[k] = expandInputFile(flow.Render(v, dd), dd.InputFile)
	}
	p.argChanges = h.mergeArgs(p.argChanges, changes)
	p.args = h.mergeArgs(args, p.argChanges)
	out, err := h.improveToolRun(ctx, root, fr, p, dd)
	p.check, p.toolErr = out, ""
	if err != nil {
		p.toolErr = err.Error()
	}
}

// improveRerun runs the tool again (pair before) with the AI's changes.
func (h *Hub) improveRerun(ctx context.Context, root string, fr *flowRun, p *improvePair, d flow.Data, v improveVerdict, round int) {
	if v.Input != nil {
		p.input = *v.Input
	}
	if p.args == nil { // the tool's first run failed before its arguments were set
		p.args = map[string]any{}
		for k, val := range p.t.Args {
			p.args[k] = flow.Render(val, d)
		}
	}
	p.args = h.mergeArgs(p.args, v.Args)
	dd := d
	dd.Input, dd.Live = p.input, nil
	if ip, _ := h.flowInputFile(fr, p.t.ID, p.input); ip != "" {
		dd.InputFile = ip
	}
	h.improveNote(fr, p.t.ID, fmt.Sprintf("Run again by %s (round %d)", firstNonEmpty(p.n.Title, p.n.ID), round))
	out, err := h.improveToolRun(ctx, root, fr, p, dd)
	p.result, p.toolErr = out, ""
	if err != nil {
		p.toolErr = err.Error()
	}
}

// improveToolRun runs the paired tool step and records it as that step's run.
func (h *Hub) improveToolRun(ctx context.Context, root string, fr *flowRun, p *improvePair, d flow.Data) (string, error) {
	start := time.Now()
	id := p.t.ID
	fr.update(func(st *FlowRunState) {
		s := fr.step(id)
		s.Status, s.Started, s.Error, s.Attempts = "running", start, "", s.Attempts+1
	})
	h.flowState(fr)
	out, err := h.runToolArgs(ctx, root, fr, p.t, p.args, "", d)
	out = h.Store.Redactor().String(out)
	if err != nil {
		err = errors.New(h.Store.Redactor().String(err.Error()))
	}
	fr.update(func(st *FlowRunState) {
		s := fr.step(id)
		s.Ended, s.DurationMS = time.Now(), time.Since(start).Milliseconds()
		s.Usage.ToolCalls++
		st.Usage.ToolCalls++
		s.Status = "done"
		if err != nil {
			s.Status, s.Error = "failed", err.Error()
			s.Usage.ToolErrors++
			st.Usage.ToolErrors++
		}
		s.Output, s.Truncated = clip(out)
	})
	h.flowState(fr)
	return out, err
}

// improveJudge asks the AI whether the result is good enough and what to
// change; a reply that is not the JSON asked for is asked for once more.
func (h *Hub) improveJudge(ask func(string) (string, error), fr *flowRun, p *improvePair, d flow.Data, round, limit int) (improveVerdict, error) {
	prompt := h.improvePrompt(fr, p, d, round, limit)
	ans, err := ask(prompt)
	if err != nil {
		return improveVerdict{}, fmt.Errorf("the AI could not judge round %d: %v", round, err)
	}
	v, perr := parseVerdict(ans)
	if perr != nil {
		ans, err = ask(prompt + "\n\nYour previous reply could not be used (" + perr.Error() + "). Reply again with ONLY the JSON object.")
		if err != nil {
			return improveVerdict{}, err
		}
		if v, perr = parseVerdict(ans); perr != nil {
			return improveVerdict{}, fmt.Errorf("the AI's verdict in round %d was not the JSON asked for: %v", round, perr)
		}
	}
	return v, nil
}

func parseVerdict(s string) (improveVerdict, error) {
	var v improveVerdict
	t := jsonReply(s)
	if err := json.Unmarshal([]byte(t), &v); err != nil {
		return v, err
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(t), &raw)
	if _, ok := raw["done"]; !ok {
		return v, errors.New(`it has no "done" field`)
	}
	return v, nil
}

// improvePrompt is one round's prompt: what to reach, the current result
// and findings, a short record of the earlier rounds, and the reply format.
// Long texts are cut to their budgets, so rounds stay the same size.
func (h *Hub) improvePrompt(fr *flowRun, p *improvePair, d flow.Data, round, limit int) string {
	f := fr.flow
	n, t := p.n, p.t
	var b strings.Builder
	if p.after {
		fmt.Fprintf(&b, "You are improving a result until it is good enough. You produced it; %s checked it. This is round %d of at most %d.\n", toolTitle(t), round, limit)
		b.WriteString("\n## The task\n" + flow.Render(n.Prompt, d) + "\n")
	} else {
		fmt.Fprintf(&b, "You are improving the result of the tool %s until it is good enough: judge its result and, when it is not good enough, change how the tool is run. This is round %d of at most %d.\n", toolTitle(t), round, limit)
		if s := strings.TrimSpace(flow.Render(n.Prompt, d)); s != "" {
			b.WriteString("\n## Instructions\n" + s + "\n")
		}
	}
	b.WriteString("\n## Done when\n" + flow.Render(n.Goal, d) + "\n")
	if e, err := h.toolForFlow(t.Tool); err == nil {
		fmt.Fprintf(&b, "\n## The tool %s\n%s\n", e.Name, e.Desc)
		for _, prm := range e.Params {
			fmt.Fprintf(&b, "- %s (%s%s): %s\n", prm.Name, firstNonEmpty(prm.Type, "string"), map[bool]string{true: ", required", false: ""}[prm.Required], prm.Desc)
		}
	}
	if len(p.args) > 0 {
		a, _ := json.MarshalIndent(redactAny(h, p.args), "", "  ")
		b.WriteString("\nIt ran with these arguments (secrets show as masked and cannot be changed):\n```json\n" + fitText(string(a), improveArgsBudget, "") + "\n```\n")
	}
	if p.after {
		b.WriteString("\n## Your current result\n```\n" + fitText(p.result, improveResultBudget, p.file) + "\n```\n")
		b.WriteString("\n## What the check found\n")
		if p.toolErr != "" {
			b.WriteString("The check failed: " + clipText(p.toolErr, 2000) + "\n")
		}
		b.WriteString("```\n" + fitText(p.check, improveCheckBudget, "") + "\n```\n")
	} else {
		if strings.TrimSpace(p.input) != "" {
			b.WriteString("\n## The tool's input (from the steps before it)\n```\n" + fitText(p.input, improveInputBudget, "") + "\n```\n")
		}
		b.WriteString("\n## The tool's result\n")
		if p.toolErr != "" {
			b.WriteString("The tool failed: " + clipText(p.toolErr, 2000) + "\n")
		}
		b.WriteString("```\n" + fitText(p.result, improveResultBudget, p.file) + "\n```\n")
	}
	if len(p.history) > 0 {
		b.WriteString("\n## Earlier rounds\n")
		for i, r := range p.history {
			older := len(p.history)-i > improveHistoryFull
			switch {
			case r.By == "person":
				fmt.Fprintf(&b, "- A person after round %d: %s\n", r.Round, clipText(r.Reason, map[bool]int{true: 200, false: 2000}[older]))
			case older:
				fmt.Fprintf(&b, "- Round %d: %s\n", r.Round, clipText(map[bool]string{true: "done", false: "not done"}[r.Done]+": "+r.Reason, 160))
			default:
				fmt.Fprintf(&b, "- Round %d: %s. %s", r.Round, map[bool]string{true: "Done", false: "Not done"}[r.Done], clipText(r.Reason, 600))
				if r.Changes != "" {
					b.WriteString(" Changed: " + r.Changes + ".")
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("Do not repeat a change that did not help.\n")
	}
	if p.feedback != "" {
		b.WriteString("\n## A person reviewed this result and asked for changes\n" + clipText(p.feedback, 4000) + "\nIt is not done until that is addressed: reply with done false and the changes.\n")
	}
	b.WriteString("\n## Your reply\nReply with ONLY a JSON object, no explanation and no code fences:\n")
	if p.after {
		b.WriteString(`{"done": true or false,
 "reason": "<one or two sentences: why it is or is not good enough>",
 "output": "<when not done: your complete revised result (not a diff)>",
 "args": {"<parameter>": "<new value>"}}
"args" is optional: only when the check itself was run wrongly. Leave out what you do not change.`)
		if n.OutputSchema != nil && n.OutputSchema.Known() {
			b.WriteString("\nThe result (output) is JSON in this shape, as a string: " + n.OutputSchema.Compact())
		}
	} else {
		b.WriteString(`{"done": true or false,
 "reason": "<one or two sentences: why it is or is not good enough>",
 "args": {"<parameter>": "<new value>"},
 "input": "<a new input for the tool>",
 "output": "<only when done and the result needs a small fix the tool cannot make: the complete fixed result>"}
When not done, give the arguments to change for the next run (only the ones that change) and, only if the input itself must change, the new input. Leave out what you do not change.`)
	}
	b.WriteString(h.lessonsNote(f.ID, n.ID))
	b.WriteString(h.aiSuffix(f, d))
	return b.String()
}

// fitText cuts s to about n bytes, keeping its start and its end, and says
// where the whole text is.
func fitText(s string, n int, file string) string {
	if len(s) <= n {
		return s
	}
	head := bigout.SafeCut(s, n*2/3)
	i := len(s) - n/4
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 && nl < 400 {
		i += nl + 1
	}
	tail := s[i:]
	where := ""
	if file != "" {
		where = "; the whole text is in " + file
	}
	return fmt.Sprintf("%s\n… [%s left out%s] …\n%s", head, bigout.Human(len(s)-len(head)-len(tail)), where, tail)
}

// improveFile saves a round's result in the run's results folder.
func (h *Hub) improveFile(fr *flowRun, step string, round int, s string) string {
	rel, err := h.writeRunFile(fmt.Sprintf("%s/rounds/%02d.%s", stepFilesDir(fr.st.ID, step), round, contentExt(s)), s)
	if err != nil {
		return ""
	}
	return rel
}

// improveReview pauses the run for a person to approve the result or ask
// for another round.
func (h *Hub) improveReview(ctx context.Context, fr *flowRun, p *improvePair, round, limit int, verdict, reason, out string) (FlowDecision, error) {
	n := p.n
	clipped, _ := clip(out)
	fr.update(func(st *FlowRunState) {
		st.Status = "review"
		st.Review = &FlowReview{Step: n.ID, Title: firstNonEmpty(n.Title, n.ID), Type: n.Type, Output: clipped,
			Round: round, Rounds: limit, Verdict: verdict, Reason: reason, History: slices.Clone(p.history)}
		fr.step(n.ID).Status = "review"
	})
	h.flowState(fr)
	h.flowPublish(fr, "flow.review", fmt.Sprintf("Waiting for you: %s, round %d (%s)", firstNonEmpty(n.Title, n.ID), round, verdict), map[string]any{"_step": n.ID, "node": n.ID})
	var dec FlowDecision
	select {
	case dec = <-fr.decide:
	case <-ctx.Done():
		return dec, ctx.Err()
	}
	fr.update(func(st *FlowRunState) {
		st.Status, st.Review = "running", nil
		fr.step(n.ID).Status = "running"
	})
	h.flowState(fr)
	return dec, nil
}

func (h *Hub) improveState(fr *flowRun, p *improvePair) {
	fr.update(func(st *FlowRunState) { fr.step(p.n.ID).Rounds = slices.Clone(p.history) })
	h.flowState(fr)
}

func (h *Hub) improveNote(fr *flowRun, step, note string) {
	fr.update(func(st *FlowRunState) {
		if s := fr.step(step); s != nil {
			s.Notes = append(s.Notes, note)
		}
	})
}

func toolTitle(t *flow.Node) string {
	if t.Title != "" && t.Title != t.Tool {
		return fmt.Sprintf("%s (%s)", t.Title, t.Tool)
	}
	return t.Tool
}
