package core

// Flow runs: review pauses — continue, refine, run a command, edit, retry —
// and saving an improvement back into the flow.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"codegen/internal/flow"
)

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
			h.learnFromEdit(fr, n.ID, out, dec.Output) // a small correction is a lesson (lessons.go)
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
			} else {
				h.learnFromReview(fr, n.ID, fb, "refine") // later runs of this step get it (lessons.go)
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
		// the person typed this command in the review panel: it is approved
		if err := h.approveCommandTexts(root, []string{text}); err != nil {
			h.flowPublish(fr, "error", "could not approve the saved command: "+err.Error(), map[string]any{"_step": n.ID})
		}
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
