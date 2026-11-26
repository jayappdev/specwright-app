package core

// save_flow: how an agent changes a flow (the Playground continuing a flow
// run, for example). It takes the complete flow, reads it as the Flow
// Designer does (leniently, with the same repairs as the AI designer),
// checks it, and saves it as a new version of the flow it changes — the
// original stays as it was. The agent is told what does not fit, so it can
// fix the flow and save again (update_version_id updates the version it
// saved instead of making another).

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// SaveFlowTool is the built-in agents change flows with.
const SaveFlowTool = "save_flow"

// savedByAgent starts Flow.Designed of a flow (version) saved with save_flow.
const savedByAgent = "Saved by the AI with save_flow"

func saveFlowInfo() extension.BuiltinInfo {
	var ops []string
	for k := range flow.Ops {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	rules := fmt.Sprintf(designRules, strings.Join(ops, ", "))
	return extension.BuiltinInfo{
		Name:  SaveFlowTool,
		Title: "Save a flow (Flow Designer format)",
		Group: "flows",
		Description: "Change a SpecWright flow: give the complete flow JSON (name, inputs, nodes, edges) and the id of the flow it changes (base_flow_id). " +
			"It is checked against the Flow Designer format and saved as a NEW VERSION of that flow (the original stays); the answer lists problems to fix. " +
			"To fix the version you saved, call it again with update_version_id. Never edit .codegen/flows/*.json directly.\n\nFLOW FORMAT:\n" + rules,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"flow":              map[string]any{"description": "The complete flow: {\"name\", \"description\", \"inputs\": [...], \"nodes\": [...], \"edges\": [...]} (an object, or its JSON text)"},
				"base_flow_id":      map[string]any{"type": "string", "description": "The flow this changes (its id, e.g. todo-report): the result is saved as its new version. Empty: a new flow."},
				"update_version_id": map[string]any{"type": "string", "description": "A version you saved with save_flow before: update it instead of saving another version."},
				"note":              map[string]any{"type": "string", "description": "What you changed, in one sentence (shown with the version)."},
			},
			"required": []string{"flow"},
		},
	}
}

// agentSaveFlow implements save_flow.
func (h *Hub) agentSaveFlow(args map[string]any) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	var raw []byte
	switch v := args["flow"].(type) {
	case string:
		raw = []byte(strings.TrimSpace(jsonReply(v)))
	case map[string]any:
		raw, _ = json.Marshal(v)
	default:
		return "", errors.New(`give the complete flow as "flow": an object with name, inputs, nodes and edges`)
	}
	f, notes, err := flow.ParseDesigned(raw)
	if err != nil {
		return "", fmt.Errorf("the flow does not fit the Flow Designer format: %v. Fix it and call save_flow again (the format is in this tool's description)", err)
	}
	known := map[string]bool{}
	for _, t := range h.designTools() {
		known[t.Name] = true
	}
	fixDesign(f, known) // step ids, tool names with or without builtin:, edges to missing steps
	f.Rev, f.Repairs = "", nil
	placed := false
	for _, n := range f.Nodes {
		if n.X != 0 || n.Y != 0 {
			placed = true
		}
	}
	if !placed {
		f.Layout()
	}
	note := strings.TrimSpace(fmt.Sprint(firstNonNil(args["note"], "")))
	designed := savedByAgent
	if note != "" {
		designed += ": " + note
	}
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	var st FlowState
	switch upd, base := str("update_version_id"), str("base_flow_id"); {
	case upd != "":
		cur, err := flow.Load(root, upd)
		if err != nil {
			return "", fmt.Errorf("no flow %s to update; call save_flow with base_flow_id to save a new version", upd)
		}
		if !strings.HasPrefix(cur.Designed, savedByAgent) {
			return "", fmt.Errorf("%s was not saved with save_flow; save a new version instead (base_flow_id=%s)", upd, upd)
		}
		f.ID, f.Parent, f.Revision = cur.ID, cur.Parent, cur.Revision
		f.Name = firstNonEmpty(strings.TrimSpace(f.Name), cur.Name)
		if versionSuffix.FindString(cur.Name) != "" && versionSuffix.FindString(f.Name) == "" { // keep "(vN)"
			f.Name = strings.TrimSpace(f.Name) + versionSuffix.FindString(cur.Name)
		}
		f.Context, f.Skills = keepContext(f, cur)
		keepListing(f, cur)
		f.Designed = designed
		if st, err = h.SaveFlow(*f); err != nil {
			return "", err
		}
	case base != "":
		cur, err := flow.Load(root, base)
		if err != nil {
			return "", fmt.Errorf("no flow %s: give base_flow_id as the id of the flow you change (its file is .codegen/flows/<id>.json)", base)
		}
		f.Context, f.Skills = keepContext(f, cur)
		f.Designed = designed
		if st, err = h.SaveFlowVersion(base, *f); err != nil {
			return "", err
		}
	default:
		f.Name = firstNonEmpty(strings.TrimSpace(f.Name), "Flow from the AI")
		f.Designed = designed
		if st, err = h.SaveFlow(*f); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Saved the flow %q (id %s, file .codegen/flows/%s.json).", st.Flow.Name, st.Flow.ID, st.Flow.ID)
	if st.Flow.Parent != "" {
		fmt.Fprintf(&b, " It is version %d of %s; the original is unchanged. The user opens it in the Flow Designer.", st.Flow.Revision, st.Flow.Parent)
	}
	if len(notes) > 0 {
		b.WriteString("\nRead with these adjustments:\n- " + strings.Join(notes, "\n- "))
	}
	var errs, warns []string
	for _, is := range st.Issues {
		if is.Level == "error" {
			errs = append(errs, is.Message)
		} else {
			warns = append(warns, is.Message)
		}
	}
	if len(errs) > 0 {
		fmt.Fprintf(&b, "\nPROBLEMS (the flow cannot run until they are fixed; fix them and call save_flow again with update_version_id=%s):\n- %s", st.Flow.ID, strings.Join(errs, "\n- "))
	}
	if len(warns) > 0 {
		b.WriteString("\nWarnings:\n- " + strings.Join(warns, "\n- "))
	}
	if len(errs) == 0 {
		b.WriteString("\nIt checks out.")
	}
	return b.String(), nil
}

// keepContext: a changed flow keeps the original's context and skills unless
// it gives its own.
func keepContext(f, cur *flow.Flow) (flow.Context, []flow.Skill) {
	c, s := f.Context, f.Skills
	if strings.TrimSpace(c.Text) == "" && len(c.Files) == 0 {
		c = cur.Context
	}
	if len(s) == 0 {
		s = cur.Skills
	}
	return c, s
}

func firstNonNil(a ...any) any {
	for _, x := range a {
		if x != nil {
			return x
		}
	}
	return nil
}
