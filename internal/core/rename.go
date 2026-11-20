package core

// Renaming flows and tools: the name is also what they are stored and
// called by (a flow's file .codegen/flows/<id>.json, a tool's name and its
// script), so a rename moves the files and updates what refers to them:
// other flows (after-flow triggers, versions, run_flow steps, tool steps
// and Output actions), trigger state, lessons and tool tags. Past runs keep
// the name they ran with.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// RenameFlow gives a flow a new name and, with it, a new id (its file name
// follows the name). The id stays when the name gives the same one.
func (h *Hub) RenameFlow(id, name string) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FlowState{}, errors.New("give the flow a name")
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return FlowState{}, err
	}
	if h.flowRunning(id) {
		return FlowState{}, fmt.Errorf("%s is running: rename it when the run has ended", f.Name)
	}
	f.Name = name
	newID := flow.Slug(name)
	if newID == id {
		f.Rev = ""
		return h.SaveFlow(*f)
	}
	newID = flow.UniqueID(root, newID)
	f.ID, f.Rev = newID, ""
	if err := flow.Save(root, f); err != nil {
		return FlowState{}, err
	}
	if err := flow.Delete(root, id); err != nil {
		_ = flow.Delete(root, newID)
		return FlowState{}, fmt.Errorf("renaming %s: %v", id, err)
	}
	// what refers to it by id
	if list, err := flow.List(root); err == nil {
		for _, s := range list {
			if s.ID == newID {
				continue
			}
			if g, err := flow.Load(root, s.ID); err == nil && renameFlowRefs(g, id, newID) {
				_ = flow.Save(root, g)
			}
		}
	}
	ts := h.triggers(root)
	ts.mu.Lock()
	for k, v := range ts.data {
		if rest, ok := strings.CutPrefix(k, id+"/"); ok {
			ts.data[trigKey(newID, rest)] = v
			delete(ts.data, k)
		}
	}
	_ = ts.save()
	ts.mu.Unlock()
	_ = h.Store.RenameFlow(root, id, newID)
	h.moveLayoutItem("flows", id, newID)
	h.kickTriggers(root)
	return FlowState{Flow: f, Issues: flow.Validate(f, h.flowTools()), Notes: []string{fmt.Sprintf("Renamed: its file is now .codegen/flows/%s.json (was %s.json).", newID, id)}}, nil
}

// renameFlowRefs points g's references to flow from at flow to: its
// after-flow triggers, the flow it is a version of, and run_flow steps.
func renameFlowRefs(g *flow.Flow, from, to string) bool {
	changed := false
	for i := range g.Triggers {
		if a := g.Triggers[i].After; a != nil && a.Flow == from {
			a.Flow, changed = to, true
		}
	}
	if g.Parent == from {
		g.Parent, changed = to, true
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if (n.Tool == "builtin:"+RunFlowTool || n.Tool == RunFlowTool) && n.Args["flow_id"] == from {
			n.Args["flow_id"], changed = to, true
		}
	}
	return changed
}

// RenameTool gives a tool written in SpecWright (a project script tool) a
// new name: its script follows, and the flows that call it, its tags and
// its approval go with it.
func (h *Hub) RenameTool(old, name string) (extension.Extension, error) {
	root, err := h.need()
	if err != nil {
		return extension.Extension{}, err
	}
	name = strings.TrimSpace(name)
	e, err := h.exts.Get(old)
	if err != nil {
		return e, err
	}
	switch {
	case e.Managed != nil || extension.IsManagedScope(e.Scope) || h.isManagedName(old):
		return e, managedRefusal(old)
	case e.Kind == extension.KindCopilot:
		return e, errors.New(old + " comes from a Copilot extension: its name is set in the extension's files")
	case e.Kind != "script":
		return e, errors.New(old + " is an npm script (ext:*): rename it in package.json")
	case e.Scope != "project":
		return e, errors.New(old + " is a global tool: its name is part of the corporate catalog")
	case !e.Approved:
		return e, errors.New(old + " is not approved: review and save it first, then rename it")
	case name == old:
		return e, nil
	case !extension.NameRE.MatchString(name):
		return e, errors.New("a tool name starts with a letter and uses lowercase letters, digits, - and _ (at most 40)")
	}
	if _, err := h.exts.Get(name); err == nil || h.isManagedName(name) {
		return e, fmt.Errorf("there is already a tool named %s", name)
	}
	for _, b := range h.BuiltinTools() {
		if b.Name == name {
			return e, fmt.Errorf("%s is the name of a built-in tool", name)
		}
	}
	ne := e
	ne.Name = name
	saved, err := h.exts.Save(ne)
	if err != nil {
		return e, err
	}
	if err := h.exts.Delete(old); err != nil {
		return saved, err
	}
	// the old script (a file, or a folder for a tool with packages)
	tools := filepath.Join(root, ".codegen", "tools") + string(filepath.Separator)
	for _, rel := range []string{e.ScriptPath, e.Dir} {
		if rel == "" || rel == saved.ScriptPath || rel == saved.Dir {
			continue
		}
		if p := filepath.Join(root, filepath.FromSlash(rel)); strings.HasPrefix(p, tools) {
			_ = os.RemoveAll(p)
		}
	}
	h.moveLayoutItem("tools", old, name)
	// its tags
	if tags := h.ToolTags()[old]; len(tags) > 0 {
		_, _ = h.SetToolTags(name, tags)
		_, _ = h.SetToolTags(old, nil)
	}
	// the flows that call it
	if list, err := flow.List(root); err == nil {
		for _, s := range list {
			g, err := flow.Load(root, s.ID)
			if err != nil {
				continue
			}
			changed := false
			for i := range g.Nodes {
				n := &g.Nodes[i]
				if n.Tool == old {
					n.Tool, changed = name, true
				}
				for j := range n.Actions {
					if n.Actions[j].Tool == old {
						n.Actions[j].Tool, changed = name, true
					}
				}
			}
			if changed {
				_ = flow.Save(root, g)
			}
		}
	}
	return saved, nil
}
