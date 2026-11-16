package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// ExportTools returns a tool file with the named tools of this project.
func (h *Hub) ExportTools(names []string) (string, error) {
	if _, err := h.need(); err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", errors.New("choose at least one tool to export")
	}
	var out []extension.BundledTool
	for _, n := range names {
		t, err := h.bundleTool(n)
		if err != nil {
			return "", err
		}
		out = append(out, t)
	}
	return extension.MarshalBundle(out)
}

func (h *Hub) bundleTool(name string) (extension.BundledTool, error) {
	if h.isManagedName(name) {
		return extension.BundledTool{}, fmt.Errorf("%s is managed by an admin; share it by publishing it", name)
	}
	e, err := h.exts.Get(name)
	if err != nil {
		return extension.BundledTool{}, fmt.Errorf("no tool named %s in this project", name)
	}
	return extension.Bundle(h.Root(), e, h.ToolTags()[name])
}

// ToolImportItem is one tool of a file being imported, as the review shows it.
type ToolImportItem struct {
	Tool extension.BundledTool `json:"tool"`
	// Exists: this project already has a tool with this name; Managed: the
	// name is an admin-managed tool (it can only be imported under a new name)
	Exists  bool   `json:"exists"`
	Managed bool   `json:"managed"`
	Suggest string `json:"suggest"` // a free name for "import as a copy"
}

// PreviewToolImport reads a tool file and lists its tools with what would
// happen to each, so the user can review the scripts before importing.
func (h *Hub) PreviewToolImport(data string) ([]ToolImportItem, error) {
	if _, err := h.need(); err != nil {
		return nil, err
	}
	tools, err := extension.ParseBundle(data)
	if err != nil {
		return nil, err
	}
	out := make([]ToolImportItem, 0, len(tools))
	for _, t := range tools {
		_, exErr := h.exts.Get(t.Name)
		it := ToolImportItem{Tool: t, Exists: exErr == nil, Managed: h.isManagedName(t.Name)}
		if it.Exists || it.Managed {
			it.Suggest = h.freeToolName(t.Name)
		}
		out = append(out, it)
	}
	return out, nil
}

// freeToolName is name-2, name-3 … (within the 40-character limit) not used yet.
func (h *Hub) freeToolName(name string) string {
	for k := 2; k < 1000; k++ {
		suffix := fmt.Sprintf("-%d", k)
		base := name
		if len(base)+len(suffix) > 40 {
			base = base[:40-len(suffix)]
		}
		n := base + suffix
		if _, err := h.exts.Get(n); err != nil && !h.isManagedName(n) {
			return n
		}
	}
	return name + "-copy"
}

// ToolImportResult says what an import did.
type ToolImportResult struct {
	Imported []string `json:"imported"` // names as saved, all needing approval
	Skipped  []string `json:"skipped"`
	Notes    []string `json:"notes,omitempty"`
}

// ImportTools imports the tools of a tool file into this project. actions
// maps a tool's name to "import" (a new tool), "replace" (overwrite the
// existing one), "copy:<new name>" or "skip"; tools not named are imported
// when new and skipped when the name is taken. Imported tools are saved as
// project tools that need approval: none runs until someone reads it and
// saves it in the tool editor. Dependencies are installed then, not now.
func (h *Hub) ImportTools(data string, actions map[string]string) (ToolImportResult, error) {
	if _, err := h.need(); err != nil {
		return ToolImportResult{}, err
	}
	tools, err := extension.ParseBundle(data)
	if err != nil {
		return ToolImportResult{}, err
	}
	var res ToolImportResult
	for _, t := range tools {
		name, note, err := h.importTool(t, actions[t.Name])
		switch {
		case err != nil:
			return res, err
		case name == "":
			res.Skipped = append(res.Skipped, t.Name)
		default:
			res.Imported = append(res.Imported, name)
		}
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
	}
	return res, nil
}

// importTool saves one tool (or skips it) and returns the name it was saved under.
func (h *Hub) importTool(t extension.BundledTool, action string) (string, string, error) {
	_, exErr := h.exts.Get(t.Name)
	exists, managed := exErr == nil, h.isManagedName(t.Name)
	if action == "" {
		action = map[bool]string{true: "skip", false: "import"}[exists || managed]
	}
	switch {
	case action == "skip":
		return "", "", nil
	case action == "replace":
		if managed {
			return "", "", fmt.Errorf("%s is managed by an admin and cannot be replaced; import it under another name", t.Name)
		}
	case strings.HasPrefix(action, "copy:"):
		n := strings.TrimSpace(strings.TrimPrefix(action, "copy:"))
		if !extension.NameRE.MatchString(n) {
			return "", "", fmt.Errorf("%q is not a valid tool name", n)
		}
		if _, err := h.exts.Get(n); err == nil || h.isManagedName(n) {
			return "", "", fmt.Errorf("a tool named %s already exists", n)
		}
		t.Name = n
	case action == "import":
		if exists || managed {
			return "", "", fmt.Errorf("a tool named %s already exists: replace it, import it as a copy or skip it", t.Name)
		}
	default:
		return "", "", fmt.Errorf("unknown import action %q for %s", action, t.Name)
	}
	e := t.Extension()
	if strings.TrimSpace(t.Script) != "" {
		// the command runs the file Save writes; point it at that file
		e.Command = extension.RepointScript(e.Command, t.Name, ".codegen/tools/project/"+t.Name+extension.ScriptExt(e.Lang))
	}
	if _, err := h.exts.Save(e); err != nil {
		return "", "", fmt.Errorf("%s: %v", t.Name, err)
	}
	if _, err := h.exts.MarkUnapproved(t.Name); err != nil {
		return "", "", err
	}
	if len(t.Tags) > 0 {
		_, _ = h.SetToolTags(t.Name, t.Tags) // tags are a convenience: an error does not stop the import
	}
	note := ""
	if len(t.Secrets) > 0 {
		var names []string
		for _, s := range t.Secrets {
			names = append(names, s.Name)
		}
		note = fmt.Sprintf("%s needs secret(s) %s: no values come with the file", t.Name, strings.Join(names, ", "))
	}
	return t.Name, note, nil
}

// flowToolNames are the project tools a flow uses (tool steps and output
// actions that call a tool), without built-ins.
func flowToolNames(f *flow.Flow) []string {
	var out []string
	add := func(n string) {
		if n != "" && !strings.HasPrefix(n, "builtin:") && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, n := range f.Nodes {
		if n.Type == "tool" {
			add(n.Tool)
		}
		for _, a := range n.Actions {
			add(a.Tool)
		}
	}
	return out
}

// withFlowTools adds the flow's project tools to an exported flow file
// ("tools", in the tool file's form). Tools that cannot be exported are
// listed under "toolsMissing".
func (h *Hub) withFlowTools(f *flow.Flow, data []byte) ([]byte, error) {
	var tools []extension.BundledTool
	var missing []string
	for _, n := range flowToolNames(f) {
		t, err := h.bundleTool(n)
		if err != nil {
			missing = append(missing, n)
			continue
		}
		tools = append(tools, t)
	}
	if len(tools) == 0 && len(missing) == 0 {
		return data, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return data, nil
	}
	if len(tools) > 0 {
		b, _ := json.Marshal(tools)
		m["tools"] = b
	}
	if len(missing) > 0 {
		b, _ := json.Marshal(missing)
		m["toolsMissing"] = b
	}
	return json.MarshalIndent(m, "", "  ")
}

// importFlowTools imports the tools carried by a flow file that this project
// does not have yet (needing approval); tools it has are left as they are.
func (h *Hub) importFlowTools(data string) []string {
	var m struct {
		Tools []extension.BundledTool `json:"tools"`
	}
	if json.Unmarshal([]byte(data), &m) != nil || len(m.Tools) == 0 {
		return nil
	}
	b, _ := extension.MarshalBundle(m.Tools)
	tools, err := extension.ParseBundle(b)
	if err != nil {
		return []string{"the tools in the file could not be read: " + err.Error()}
	}
	var added, kept []string
	var notes []string
	for _, t := range tools {
		name, note, err := h.importTool(t, "")
		switch {
		case err != nil:
			notes = append(notes, err.Error())
		case name == "":
			kept = append(kept, t.Name)
		default:
			added = append(added, name)
		}
		if note != "" {
			notes = append(notes, note)
		}
	}
	if len(added) > 0 {
		notes = append([]string{"imported tool(s) " + strings.Join(added, ", ") + ": open each on the Tools screen, read it and save it to approve it"}, notes...)
	}
	if len(kept) > 0 {
		notes = append(notes, "the flow uses this project's own "+strings.Join(kept, ", ")+" (the file's copies were not imported)")
	}
	return notes
}
