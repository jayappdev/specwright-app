package core

// A Decision step the AI decides (flow/decide_ai.go): the AI gets the
// question, the paths with their hints, the data that reached the step and
// the context packs before it, and answers with a path, a reason and how
// sure it is. An answer that is not one of the paths is asked for again,
// once. When it is less sure than the step's threshold, the step does what
// it says: a person chooses (the run pauses, the AI's choice suggested), it
// takes Otherwise, or it accepts the AI's choice. A person who chooses
// another path than the AI teaches it: that becomes a lesson for the step,
// so later decisions see it.

import (
	"context"
	"fmt"
	"strings"

	"codegen/internal/flow"
	"codegen/internal/store"
)

// FlowChoice is a path a person can choose in a decision's review.
type FlowChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

func (h *Hub) aiDecision(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, in string) ([]string, store.Usage, error) {
	var u store.Usage
	f := fr.flow
	var paths strings.Builder
	for _, b := range n.Branches {
		fmt.Fprintf(&paths, "- %s: %s", b.ID, firstNonEmpty(b.Label, b.ID))
		if hint := strings.TrimSpace(flow.Render(b.Hint, d)); hint != "" {
			paths.WriteString(" — take it when " + hint)
		}
		paths.WriteString("\n")
	}
	paths.WriteString("- else: Otherwise (none of the paths above fits)\n")
	data := in
	if len(data) > 30000 {
		data = data[:30000] + "\n…(cut)"
	}
	question := flow.Render(n.Prompt, d)
	prompt := "You decide which path a flow takes next.\n\n## Question\n" + question +
		"\n\n## Paths (choose exactly one)\n" + paths.String() +
		"\n## The data that reached this step\n```\n" + data + "\n```" +
		h.contextPacks(f, n, d, n.Prompt) + h.lessonsNote(f.ID, n.ID) + h.aiSuffix(f, n, d) +
		"\n\n## Your reply\nReply with ONLY a JSON object, no explanation and no code fences:\n" +
		`{"path": "<the id of one path above, or else>", "reason": "<one or two sentences; cite the context's [S1] labels when it has them>", "confidence": <how sure you are, as a whole number of percent from 0 to 100, e.g. 85>}` +
		"\nGive confidence as a whole number (85), not a fraction (0.85) or a decimal (85.5)."
	var dec flow.AIDecision
	var err error
	for try := 0; try < 2; try++ {
		var out string
		var cu store.Usage
		out, cu, err = h.flowAgent(withFlowStep(ctx, fr.st.ID, n.ID), root, fr, n.ID, prompt, firstNonEmpty(n.Model, fr.st.Options.Model))
		u.Add(cu)
		if err != nil {
			return nil, u, err
		}
		if dec, err = flow.ParseAIDecision(out, *n); err == nil {
			break
		}
		prompt += "\n\nYour last reply could not be used (" + err.Error() + "). Reply again with only the JSON object, and a path from the list."
	}
	if err != nil {
		return nil, u, fmt.Errorf("the AI did not choose a path: %v", err)
	}
	label := flow.BranchLabel(*n, dec.Path)
	fr.update(func(st *FlowRunState) {
		s := fr.step(n.ID)
		s.Why, s.Confidence = dec.Reason, dec.Confidence
	})
	chosen := dec.Path
	threshold := flow.ConfidenceOf(*n)
	if dec.Confidence < threshold {
		unsure := firstNonEmpty(n.Unsure, "ask")
		nobody := fr.noReview || fr.st.Options.Review == "none"
		if unsure == "ask" && nobody { // nobody to ask: Otherwise when it leads somewhere
			unsure = "accept"
			for _, e := range f.Edges {
				if e.From == n.ID && e.Branch == flow.ElseBranch {
					unsure = "else"
				}
			}
			h.improveNote(fr, n.ID, fmt.Sprintf("Only %d%% sure and nobody to ask in this run", dec.Confidence))
		}
		switch unsure {
		case "else":
			chosen = flow.ElseBranch
			h.improveNote(fr, n.ID, fmt.Sprintf("The AI chose %q but only %d%% sure (below %d%%): took Otherwise", label, dec.Confidence, threshold))
		case "accept":
			h.improveNote(fr, n.ID, fmt.Sprintf("Only %d%% sure (below %d%%): took the AI's choice %q", dec.Confidence, threshold, label))
		default: // ask a person
			picked, err := h.decisionReview(ctx, fr, n, in, dec, threshold)
			if err != nil {
				return nil, u, err
			}
			if picked != dec.Path {
				h.learnFromReview(fr, n.ID, fmt.Sprintf("For %q a person chose %q, not %q (the AI's reason: %s).",
					clipText(question, 160), flow.BranchLabel(*n, picked), label, clipText(dec.Reason, 200)), "decision")
				h.improveNote(fr, n.ID, fmt.Sprintf("You chose %q (the AI suggested %q)", flow.BranchLabel(*n, picked), label))
			} else {
				h.improveNote(fr, n.ID, fmt.Sprintf("You confirmed %q", label))
			}
			chosen = picked
		}
	}
	return []string{chosen}, u, nil
}

// decisionReview pauses the run for a person to choose the path.
func (h *Hub) decisionReview(ctx context.Context, fr *flowRun, n *flow.Node, in string, dec flow.AIDecision, threshold int) (string, error) {
	clipped, _ := clip(in)
	choices := []FlowChoice{}
	for _, b := range n.Branches {
		choices = append(choices, FlowChoice{ID: b.ID, Label: firstNonEmpty(b.Label, b.ID), Hint: b.Hint})
	}
	choices = append(choices, FlowChoice{ID: flow.ElseBranch, Label: "Otherwise"})
	for {
		fr.update(func(st *FlowRunState) {
			st.Status = "review"
			st.Review = &FlowReview{Step: n.ID, Title: firstNonEmpty(n.Title, n.ID), Type: n.Type, Output: clipped,
				Verdict: fmt.Sprintf("The AI suggests %q, %d%% sure (below %d%%)", flow.BranchLabel(*n, dec.Path), dec.Confidence, threshold),
				Reason:  dec.Reason, Choices: choices, Suggested: dec.Path}
			fr.step(n.ID).Status = "review"
		})
		h.flowState(fr)
		h.flowPublish(fr, "flow.review", "Waiting for you: choose the path at "+firstNonEmpty(n.Title, n.ID), map[string]any{"_step": n.ID, "node": n.ID})
		var d FlowDecision
		select {
		case d = <-fr.decide:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		fr.update(func(st *FlowRunState) {
			st.Status, st.Review = "running", nil
			fr.step(n.ID).Status = "running"
		})
		h.flowState(fr)
		switch d.Action {
		case "stop":
			return "", errFlowStopped
		case "continue", "":
			return dec.Path, nil // the AI's choice
		case "choose":
			for _, c := range choices {
				if c.ID == d.Branch {
					return c.ID, nil
				}
			}
		}
		// anything else: ask again
	}
}
