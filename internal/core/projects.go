package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codegen/internal/speckit"
	"codegen/internal/workflow"
)

// A workspace can hold several Spec Kit projects, each in its own folder
// (orders-service/, billing-service/, …). The Development wizard's skills,
// rules, tools and proxy belong to the workspace and are shared; each
// project has its own Spec workflow: steps, inputs, approvals and history.
// One project is selected at a time (Profile.SpecProject); its workflow is
// the one the screens show and run.

// SpecProject is one Spec Kit project in the workspace.
type SpecProject struct {
	Name         string `json:"name"`  // folder ("" = the workspace itself)
	Label        string `json:"label"` // shown in pickers
	Current      bool   `json:"current"`
	Version      string `json:"version,omitempty"`
	Integration  string `json:"integration,omitempty"`
	Constitution bool   `json:"constitution"`
	Done         int    `json:"done"`  // approved steps
	Steps        int    `json:"steps"` // steps in its workflow
	Next         string `json:"next"`  // the step it is at ("" = all done)
}

// SpecProjects lists the workspace's Spec Kit projects: its own folder
// when it has .specify/, and every top-level folder that has one.
func (h *Hub) SpecProjects() ([]SpecProject, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	cur := h.specRel()
	var names []string
	if speckit.Status(root).Initialized {
		names = append(names, "")
	}
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") || zipSkipDirs[n] || !ProjectNameRE.MatchString(n) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, n, ".specify")); err == nil && fi.IsDir() {
			names = append(names, n)
		}
	}
	// the selected project, even before its Spec Kit files exist
	found := false
	for _, n := range names {
		found = found || n == cur
	}
	if !found && cur != "" {
		names = append(names, cur)
	}
	h.mu.RLock()
	curWf := h.wf
	h.mu.RUnlock()
	out := make([]SpecProject, 0, len(names))
	for _, n := range names {
		dir := root
		if n != "" {
			dir = filepath.Join(root, n)
		}
		st := speckit.Status(dir)
		p := SpecProject{Name: n, Label: firstNonEmpty(n, filepath.Base(root)+" (top folder)"), Current: n == cur,
			Version: st.Version, Integration: st.Integration, Constitution: st.Constitution}
		var w *workflow.Workflow
		if p.Current {
			w = curWf
		} else if _, err := os.Stat(workflow.Path(root, workflow.ProjectID(n))); err == nil {
			w, _ = workflow.Load(root, workflow.ProjectID(n))
		}
		if w != nil {
			p.Steps = len(w.Steps)
			for _, s := range w.Steps {
				if s.Status == "done" {
					p.Done++
				} else if p.Next == "" {
					p.Next = s.Title
				}
			}
		}
		out = append(out, p)
	}
	// the top folder first, then by name
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Name == "") != (out[j].Name == "") {
			return out[i].Name == ""
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// SwitchSpecProject selects another Spec Kit project of the workspace
// ("" = the workspace itself): its workflow, progress and history become
// the current ones.
func (h *Hub) SwitchSpecProject(project string) (WorkspaceState, error) {
	root, err := h.need()
	if err != nil {
		return WorkspaceState{}, err
	}
	project = strings.Trim(strings.TrimSpace(filepath.ToSlash(project)), "/")
	dir := root
	if project != "" {
		if !ProjectNameRE.MatchString(project) {
			return WorkspaceState{}, fmt.Errorf("%q is not a project folder name", project)
		}
		dir = filepath.Join(root, project)
	}
	if !speckit.Status(dir).Initialized {
		return WorkspaceState{}, fmt.Errorf("%s has no Spec Kit project yet: set it up on Choose input", firstNonEmpty(project, "the top folder"))
	}
	if err := h.selectProject(project); err != nil {
		return WorkspaceState{}, err
	}
	return h.Workspace()
}

// selectProject makes project current and loads its workflow (a new one,
// with the current choices, for a project that has none yet).
func (h *Hub) selectProject(project string) error {
	if a := h.activeRun(); a != nil && a.Session.Step != PlaygroundStep {
		return errors.New("finish or cancel the running step before switching project")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.profile == nil || h.wf == nil {
		return errors.New("no workspace is open")
	}
	if h.profile.SpecProject == project && h.wf.ID == workflow.ProjectID(project) {
		return nil
	}
	if workflow.IsSpecKit(h.wf.ID) {
		_ = workflow.Save(h.root, h.wf) // the project being left keeps its progress
	}
	w, err := workflow.LoadProject(h.root, project, h.wf, false)
	if err != nil {
		return err
	}
	interrupt(w)
	h.wf = w
	h.profile.SpecProject = project
	if err := workflow.SaveProfile(h.root, h.profile); err != nil {
		return err
	}
	return workflow.Save(h.root, h.wf)
}

// interrupt marks steps left running or paused by an earlier session.
func interrupt(w *workflow.Workflow) {
	for i := range w.Steps {
		if s := w.Steps[i].Status; s == "running" || s == "paused" {
			w.Steps[i].Status = "interrupted"
		}
	}
}

func (h *Hub) projectsOrNil() []SpecProject {
	ps, _ := h.SpecProjects()
	if ps == nil {
		ps = []SpecProject{}
	}
	return ps
}
