package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// Flow designer: flows are pipelines of tools, AI prompts, merges and
// transforms with inputs, context, skills and outputs. They are stored in
// the workspace (.codegen/flows/). Running them comes with the executor.

// Flows lists the workspace's flows.
func (h *Hub) Flows() ([]flow.Summary, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	return flow.List(root)
}

// LoadFlow reads one flow.
// A flow whose file did not fit the format (an agent edited it) is
// repaired: the file as it was is kept in .codegen/flows/.backup/, the
// repaired flow is saved in its place (same id: runs and links keep
// working), and Repairs says what changed.
func (h *Hub) LoadFlow(id string) (*flow.Flow, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	f, err := flow.Load(root, id)
	if err != nil || len(f.Repairs) == 0 {
		return f, err
	}
	src := flow.Path(root, id)
	if b, rerr := os.ReadFile(src); rerr == nil {
		bak := filepath.Join(filepath.Dir(src), ".backup", id+"-"+time.Now().Format("20060102-150405")+".json")
		if os.MkdirAll(filepath.Dir(bak), 0o755) == nil && os.WriteFile(bak, b, 0o644) == nil {
			rel, _ := filepath.Rel(root, bak)
			f.Repairs = append(f.Repairs, "The file as it was is kept in "+filepath.ToSlash(rel)+".")
		}
	}
	repairs := f.Repairs
	if err := flow.Save(root, f); err != nil {
		return nil, err
	}
	f.Repairs = repairs
	return f, nil
}

// NewFlow creates and saves an empty flow with a free id.
func (h *Hub) NewFlow(name string) (*flow.Flow, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = "New flow"
	}
	f := flow.New(flow.UniqueID(root, flow.Slug(name)), strings.TrimSpace(name))
	return f, flow.Save(root, f)
}

// SaveFlow stores a flow and returns it with its check results.
func (h *Hub) SaveFlow(f flow.Flow) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	if f.ID == "" {
		f.ID = flow.UniqueID(root, flow.Slug(f.Name))
	}
	// the file changed since this copy was loaded (an agent edited it,
	// another window saved it): do not write stale data over it
	if f.Rev != "" {
		if cur := flow.DiskRev(root, f.ID); cur != "" && cur != f.Rev {
			return FlowState{}, fmt.Errorf("%s: %s changed on disk since it was opened here; load the new version, or keep yours to overwrite it", ErrFlowChanged, f.Name)
		}
	}
	if err := flow.Save(root, &f); err != nil {
		return FlowState{}, err
	}
	if len(f.Triggers) > 0 {
		h.kickTriggers(root) // a files trigger may name a folder outside the project
	}
	return FlowState{Flow: &f, Issues: flow.Validate(&f, h.flowTools())}, nil
}

// ErrFlowChanged starts the error of a save refused because the flow's file
// changed on disk (the UI offers to reload or overwrite).
const ErrFlowChanged = "flow changed on disk"

// FlowState is a flow plus its check results.
type FlowState struct {
	Flow   *flow.Flow   `json:"flow"`
	Issues []flow.Issue `json:"issues"`
	Notes  []string     `json:"notes,omitempty"`
}

// DeleteFlow removes a flow.
func (h *Hub) DeleteFlow(id string) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	return flow.Delete(root, id)
}

// ValidateFlow checks a flow (saved or not) against the project's tools.
func (h *Hub) ValidateFlow(f flow.Flow) []flow.Issue {
	issues := flow.Validate(&f, h.flowTools())
	// tools that exist but are not approved cannot run yet
	exts, _ := h.Extensions()
	for _, n := range f.Nodes {
		if n.Type != "tool" {
			continue
		}
		for _, e := range exts {
			if e.Name == n.Tool && !e.Approved {
				issues = append(issues, flow.Issue{Node: n.ID, Level: "warning", Message: n.Tool + " is not approved yet; approve it on the Tools screen before running the flow."})
			}
		}
	}
	return issues
}

// flowTools are the tools a flow can call: approved project and global
// tools, ext:* scripts and verified managed tools.
func (h *Hub) flowTools() []flow.ToolInfo {
	exts, err := h.Extensions()
	if err != nil {
		return nil
	}
	var out []flow.ToolInfo
	for _, e := range exts {
		if extension.IsManagedScope(e.Scope) && (e.Managed == nil || e.Managed.Status != extension.ManagedVerified) {
			continue
		}
		t := flow.ToolInfo{Name: e.Name, Output: e.Output, OutputBy: e.OutputBy}
		for _, p := range e.Params {
			t.Params = append(t.Params, flow.ToolParam{Name: p.Name, Required: p.Required})
		}
		out = append(out, t)
	}
	for _, e := range h.FlowBuiltinTools() {
		t := flow.ToolInfo{Name: e.Name, Output: e.Output, OutputBy: e.OutputBy}
		for _, p := range e.Params {
			t.Params = append(t.Params, flow.ToolParam{Name: p.Name, Required: p.Required})
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ExportFlow returns a flow as a portable JSON file with its skill,
// context and default input files embedded.
func (h *Hub) ExportFlow(id string) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return "", err
	}
	b, err := flow.Export(root, f)
	if err != nil {
		return "", err
	}
	// the project tools it uses travel with it (imported as needing approval)
	b, err = h.withFlowTools(f, b)
	return string(b), err
}

// ImportFlow adds an exported flow (or a bare flow JSON) to the project.
func (h *Hub) ImportFlow(data string) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	if strings.TrimSpace(data) == "" {
		return FlowState{}, errors.New("the file is empty")
	}
	f, notes, err := flow.Import(root, []byte(data))
	if err != nil {
		return FlowState{}, err
	}
	notes = append(notes, h.importFlowTools(data)...)
	issues := h.ValidateFlow(*f)
	for _, is := range issues {
		if strings.Contains(is.Message, "is not available") {
			notes = append(notes, "this project is missing a tool the flow uses: "+is.Message)
		}
	}
	return FlowState{Flow: f, Issues: issues, Notes: notes}, nil
}

// FlowPreview is the result of previewing one step with sample data.
type FlowPreview struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// PreviewFlowStep runs a merge, transform or output step on sample data
// (no tools or AI are called), so the designer can show what it produces.
func (h *Hub) PreviewFlowStep(f flow.Flow, nodeID string, sample flow.Data) FlowPreview {
	n := f.Node(nodeID)
	if n == nil {
		return FlowPreview{Error: "no step " + nodeID}
	}
	if sample.Inputs == nil {
		sample.Inputs = map[string]string{}
	}
	if sample.Steps == nil {
		sample.Steps = map[string]string{}
	}
	// {{<step id>.output_file}} in a preview: where the file will be saved
	if sample.Files == nil {
		sample.Files = map[string]string{}
	}
	for id, v := range sample.Steps {
		if sample.Files[id] == "" {
			sample.Files[id] = ".codegen/runs/<date>/<run id>/steps/" + id + "/output." + contentExt(v)
		}
	}
	parents := f.Parents(n.ID)
	in := sample.Input
	if in == "" && len(parents) > 0 {
		var parts []string
		for _, p := range parents {
			parts = append(parts, sample.Steps[p])
		}
		in = strings.Join(parts, "\n\n")
	}
	sample.Input = in
	var out string
	var err error
	switch n.Type {
	case "loop": // the items it would run
		src := strings.Join(func() []string {
			var ps []string
			for _, p := range parents {
				ps = append(ps, sample.Steps[p])
			}
			return ps
		}(), "\n")
		if strings.TrimSpace(n.Source) != "" {
			sample.Input = src
			src = flow.Render(n.Source, sample)
		}
		var items []string
		if items, err = flow.SplitItems(src, *n); err == nil {
			b, _ := json.MarshalIndent(items, "", "  ")
			out = string(b)
		}
	case "gather": // three sample items
		var rs []flow.ItemResult
		for i, it := range []string{"first", "second", "third"} {
			rs = append(rs, flow.ItemResult{Item: it, Output: fmt.Sprintf("(result for item %d)", i+1)})
		}
		out, err = flow.Gather(*n, rs, sample)
	case "decision", "filter", "aggregate":
		if n.Source != "" {
			in = flow.Render(n.Source, sample)
		}
		switch n.Type {
		case "filter":
			out, err = flow.FilterRows(*n, in, sample)
		case "aggregate":
			out, err = flow.Aggregate(*n, in, sample)
		default:
			var bs []string
			if flow.AIDecides(*n) {
				var names []string
				for _, b := range n.Branches {
					names = append(names, flow.BranchLabel(*n, b.ID))
				}
				out = "→ the AI decides when the flow runs: " + strings.Join(append(names, "Otherwise"), ", ")
			} else if bs, err = flow.Decide(*n, in, sample); err == nil {
				var lines []string
				for _, b := range bs {
					to := []string{}
					for _, e := range f.Edges {
						if e.From == n.ID && e.Branch == b {
							to = append(to, firstNonEmpty(f.Node(e.To).Title, e.To))
						}
					}
					l := "→ " + flow.BranchLabel(*n, b)
					if len(to) > 0 {
						l += " (runs " + strings.Join(to, ", ") + ")"
					} else {
						l += " (nothing is connected to it)"
					}
					lines = append(lines, l)
				}
				out = strings.Join(lines, "\n")
			}
		}
	case "context":
		out = flow.ContextPreview(&f, *n, parents, sample)
	case "merge":
		titles := map[string]string{}
		for _, p := range parents {
			if pn := f.Node(p); pn != nil && pn.Title != "" {
				titles[p] = pn.Title
			}
		}
		out, err = flow.Merge(*n, parents, sample, titles)
	case "transform":
		if n.Source != "" {
			in = flow.Render(n.Source, sample)
		}
		out, err = flow.Transform(*n, in, sample)
	case "output":
		content := in
		if strings.TrimSpace(n.Template) != "" {
			content = flow.Render(n.Template, sample)
		}
		out, err = flow.Convert(content, n.Format)
	case "tool", "ai", "improve":
		err = errors.New("tools and AI steps run when the flow runs; enter sample output for this step instead")
	default:
		err = errors.New("nothing to preview for this step")
	}
	if err != nil {
		return FlowPreview{Output: out, Error: err.Error()}
	}
	return FlowPreview{Output: out}
}

// FlowOps describes the transformations, merge modes, formats and input
// types the designer offers.
func (h *Hub) FlowOps() map[string]any {
	ops := map[string]flow.Op{}
	for k, v := range flow.Ops {
		ops[k] = v
	}
	return map[string]any{"ops": ops, "mergeModes": flow.MergeModes, "formats": flow.Formats, "inputTypes": flow.InputTypes, "actionTypes": flow.ActionTypes,
		"condOps": flow.CondOps, "condMatches": flow.CondMatches, "aggFns": flow.AggFns, "aggregateModes": flow.AggregateModes, "rowFormats": flow.RowFormats}
}

// FlowShapes works out what data every step of a flow gets and gives (see
// flow.Shapes): from the tools' declared output shapes, the steps that
// follow from their input, and samples — the designer's sample data, then
// the outputs of the flow's last run. It is metadata for the designer and
// is not sent to the AI.
func (h *Hub) FlowShapes(f flow.Flow, samples map[string]string) map[string]flow.StepShape {
	hints := flow.ShapeHints{Tools: map[string]*flow.Schema{}, ToolsBy: map[string]map[string]map[string]*flow.Schema{}, Samples: map[string]string{}}
	exts, _ := h.Extensions()
	for _, e := range append(exts, h.FlowBuiltinTools()...) {
		if e.Output != nil {
			hints.Tools[e.Name] = e.Output
		}
		if e.OutputBy != nil {
			hints.ToolsBy[e.Name] = e.OutputBy
		}
	}
	for id, s := range h.lastFlowOutputs(f.ID) {
		hints.Samples[id] = s
	}
	for id, s := range samples {
		if strings.TrimSpace(s) != "" {
			hints.Samples[id] = s
		}
	}
	return f.Shapes(hints)
}

// lastFlowOutputs: the step outputs of the flow's latest run (complete
// ones only), to learn shapes from.
func (h *Hub) lastFlowOutputs(flowID string) map[string]string {
	out := map[string]string{}
	if flowID == "" {
		return out
	}
	runs, err := h.FlowRuns(40)
	if err != nil {
		return out
	}
	for _, r := range runs {
		if r.Workflow != flowID {
			continue
		}
		st, err := h.FlowRun(r.ID)
		if err != nil {
			continue
		}
		for _, s := range st.Steps {
			if s.Status == "done" && !s.Truncated && s.Iterations == 0 && strings.TrimSpace(s.Output) != "" {
				out[s.ID] = s.Output
			}
		}
		break
	}
	return out
}
