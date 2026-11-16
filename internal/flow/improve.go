package flow

import (
	"errors"
	"fmt"
	"strings"
)

// Improve steps ("Improve until done") pair up with a tool step and run the
// pair again and again until the result is good enough:
//
//	pair "before":  tool → Improve      the tool runs; the AI judges its result
//	                                    and changes the tool's arguments (or the
//	                                    input it gets) and runs it again
//	pair "after":   Improve → tool      the AI produces a result (Prompt); the
//	                                    tool checks it (tests, a linter, a
//	                                    validator) and its findings go back to
//	                                    the AI, which revises the result
//
// Each round the AI judges against Goal and says whether it is done. With
// Approve "done" a person confirms once the AI thinks it is done; with
// "each" a person sees every round. A person's feedback starts another
// round. Rounds caps the rounds (DefaultImproveRounds when 0).
//
// The step's output is the final result: the tool's output (before) or the
// AI's (after); the paired tool step's output is its last run.

// ImprovePairs and ImproveApprovals are the choices of an Improve step.
var (
	ImprovePairs     = []string{"before", "after"}
	ImproveApprovals = []string{"none", "done", "each"}
)

// Round limits of an Improve step.
const (
	DefaultImproveRounds = 5
	MaxImproveRounds     = 20
)

// ImproveRounds is how many rounds n may run.
func ImproveRounds(n Node) int {
	switch {
	case n.Rounds <= 0:
		return DefaultImproveRounds
	case n.Rounds > MaxImproveRounds:
		return MaxImproveRounds
	}
	return n.Rounds
}

// ImprovePair is n's pairing ("before" when not set).
func ImprovePair(n Node) string {
	if n.Pair == "after" {
		return "after"
	}
	return "before"
}

// ImproveApproval is who confirms n is done ("done" when not set: a person
// confirms when the AI thinks it is done).
func ImproveApproval(n Node) string {
	if contains(ImproveApprovals, n.Approve) {
		return n.Approve
	}
	return "done"
}

// Children returns the ids of the steps id feeds, in edge order.
func (f *Flow) Children(id string) []string {
	var out []string
	for _, e := range f.Edges {
		if e.From == id {
			out = append(out, e.To)
		}
	}
	return out
}

// ImprovePartner is the tool step an Improve step runs with: the step
// connected into it (pair before) or the step it is connected to (after).
func (f *Flow) ImprovePartner(n Node) (string, error) {
	if ImprovePair(n) == "before" {
		ps := nonStart(f, f.Parents(n.ID))
		if len(ps) != 1 || len(f.Parents(n.ID)) != 1 {
			return "", errors.New("connect exactly one tool step into it: the tool it runs again")
		}
		t := f.Node(ps[0])
		if t == nil || t.Type != "tool" {
			return "", fmt.Errorf("the step connected into it (%s) must be a tool step", titles(f, ps)[0])
		}
		if cs := f.Children(t.ID); len(cs) != 1 {
			return "", fmt.Errorf("%s is also connected to other steps, which would get its first result. Connect them after %s instead", label(*t), label(n))
		}
		return t.ID, nil
	}
	cs := f.Children(n.ID)
	if len(cs) == 0 {
		return "", errors.New("connect it to the tool step that checks its result")
	}
	if len(cs) != 1 {
		return "", errors.New("connect it to one step only: the tool that checks its result (connect the rest after that tool)")
	}
	t := f.Node(cs[0])
	if t == nil || t.Type != "tool" {
		return "", fmt.Errorf("the step it is connected to (%s) must be a tool step that checks its result", titles(f, cs)[0])
	}
	if ps := f.Parents(t.ID); len(ps) != 1 {
		return "", fmt.Errorf("%s gets data from other steps too; only %s may be connected into it", label(*t), label(n))
	}
	return t.ID, nil
}

// checkImprove validates an Improve step.
func (f *Flow) checkImprove(n Node, add func(node, level, format string, a ...any)) {
	if n.Pair != "" && !contains(ImprovePairs, n.Pair) {
		add(n.ID, "error", "%s: choose whether it pairs with the step before or the step after.", label(n))
	}
	if n.Approve != "" && !contains(ImproveApprovals, n.Approve) {
		add(n.ID, "error", "%s: unknown approval %q.", label(n), n.Approve)
	}
	if n.Rounds < 0 || n.Rounds > MaxImproveRounds {
		add(n.ID, "error", "%s: the rounds must be between 1 and %d.", label(n), MaxImproveRounds)
	}
	if strings.TrimSpace(n.Goal) == "" {
		add(n.ID, "error", "%s: say when the result is good enough (Done when).", label(n))
	}
	if ImprovePair(n) == "after" && strings.TrimSpace(n.Prompt) == "" {
		add(n.ID, "error", "%s: say what the AI should produce (the tool after it checks it).", label(n))
	}
	if _, err := f.ImprovePartner(n); err != nil {
		add(n.ID, "error", "%s: %v.", label(n), strings.TrimSuffix(err.Error(), "."))
	}
}
