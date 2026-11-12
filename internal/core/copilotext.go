package core

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

// CopilotExtensions lists your Copilot CLI extensions
// (~/.copilot/extensions) and the open project's (.github/extensions).
func (h *Hub) CopilotExtensions() []extension.CopilotExt {
	return extension.ListCopilotExts(h.Root())
}

// CopilotLoadResult says where an extension was copied and how its
// packages were installed.
type CopilotLoadResult struct {
	Path  string   `json:"path"` // workspace-relative folder
	Log   []string `json:"log"`
	Tools []string `json:"tools"`          // the project tools it became
	Note  string   `json:"note,omitempty"` // why it could not be made into tools yet
}

// LoadCopilotExtension copies one of your Copilot extensions into the
// project's .github/extensions/<name> (node_modules left out), so it goes
// with the project. replace swaps an existing project copy (the old one
// goes to SpecWright's trash); install runs npm install there for its
// packages, through the proxy and registries set under Packages & proxy.
func (h *Hub) LoadCopilotExtension(name string, replace, install bool) (CopilotLoadResult, error) {
	root, err := h.need()
	if err != nil {
		return CopilotLoadResult{}, err
	}
	dst, err := extension.CopyCopilotExt(root, name, replace, h.toTrash)
	if err != nil {
		return CopilotLoadResult{}, err
	}
	rel := filepath.ToSlash(filepath.Join(extension.ProjectCopilotExtDir, name))
	res := CopilotLoadResult{Path: rel}
	h.Bus.Publish(bus.Event{Type: "copilot.ext.loaded", Summary: "Loaded Copilot extension " + name + " into " + rel, Data: map[string]any{"name": name, "path": rel}})
	var deps []string
	for _, x := range extension.ListCopilotExts(root) {
		if x.Source == "project" && x.Name == name {
			for k, v := range x.Deps {
				deps = append(deps, k+"@"+v)
			}
		}
	}
	sort.Strings(deps)
	if !install || len(deps) == 0 {
		h.adoptCopilot(root, name, &res)
		return res, nil
	}
	h.Bus.Publish(bus.Event{Type: "ext.install", Summary: "Installing packages for " + name})
	ierr := extension.Install(h.ctx, root, dst, extension.Extension{Name: name, Lang: "Node.js", Deps: deps}, h.packageSettings(), func(l string) {
		res.Log = append(res.Log, l)
		h.Bus.Publish(bus.Event{Type: "ext.install.line", Summary: l})
	})
	if len(res.Log) > 80 {
		res.Log = append([]string{"…"}, res.Log[len(res.Log)-80:]...)
	}
	if added, err := extension.IgnoreCopilotExtPackages(root); err == nil && added {
		res.Log = append(res.Log, "Added "+extension.ProjectCopilotExtDir+"/*/node_modules/ to .gitignore.")
	}
	if ierr != nil {
		return res, fmt.Errorf("copied to %s, but installing its packages failed: %v", rel, ierr)
	}
	h.adoptCopilot(root, name, &res)
	return res, nil
}

// adoptCopilot turns a just-loaded extension into approved project tools
// (you chose to load it); when that fails, it stays listed as needing
// approval and res.Note says why.
func (h *Hub) adoptCopilot(root, name string, res *CopilotLoadResult) {
	tools, err := h.approveCopilotExt(root, name)
	if err != nil {
		res.Note = err.Error()
		return
	}
	for _, e := range tools {
		res.Tools = append(res.Tools, e.Name)
	}
}

// approveCopilotExt reads the tools a project extension registers (its
// module runs, with the Copilot SDK stubbed) and saves them as approved
// project tools, replacing earlier ones.
func (h *Hub) approveCopilotExt(root, name string) ([]extension.Extension, error) {
	var x *extension.CopilotExt
	for _, c := range extension.ListCopilotExts(root) {
		if c.Source == "project" && c.Name == name {
			c := c
			x = &c
		}
	}
	if x == nil {
		return nil, fmt.Errorf("no Copilot extension %s in %s", name, extension.ProjectCopilotExtDir)
	}
	defs, hooks, err := extension.DiscoverCopilotTools(h.ctx, root, *x, h.Env())
	if err != nil {
		return nil, err
	}
	if len(defs) == 0 {
		return nil, fmt.Errorf("%s registers no tools (only hooks?); it can only be used inside Copilot", name)
	}
	rel := filepath.ToSlash(filepath.Join(extension.ProjectCopilotExtDir, name))
	taken := map[string]bool{}
	all, _ := h.exts.List()
	for _, e := range all {
		if !(e.Kind == extension.KindCopilot && e.Dir == rel) {
			taken[e.Name] = true
		}
	}
	for _, e := range h.managedTools() {
		taken[e.Name] = true
	}
	tools := extension.CopilotToolExtensions(root, *x, defs, hooks, taken)
	if err := h.exts.PutCopilotTools(rel, tools); err != nil {
		return nil, err
	}
	var names []string
	for _, e := range tools {
		names = append(names, e.Name)
	}
	h.Bus.Publish(bus.Event{Type: "copilot.ext.approved", Summary: name + " → tools " + strings.Join(names, ", "), Data: map[string]any{"name": name, "tools": names}})
	return tools, nil
}

// RemoveCopilotExtension deletes the project's copy of an extension by
// moving it, with its tools' records, to .codegen/archive/<datetime>/ (your
// own copy in ~/.copilot is never touched).
func (h *Hub) RemoveCopilotExtension(name string) (ArchiveResult, error) {
	root, err := h.need()
	if err != nil {
		return ArchiveResult{}, err
	}
	return h.archiveCopilotExt(root, name)
}
