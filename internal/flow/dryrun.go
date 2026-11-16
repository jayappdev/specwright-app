package flow

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

// Dry runs: a flow is tried end to end without side effects. Tools and AI
// steps are not called — their output is a mock: one the test gives, else a
// sample made from the tool's output shape or the AI step's expected
// reply. Every other step really runs (transforms, filters, aggregates,
// decisions, loops, merges, outputs — without their actions), so wrong
// wiring, paths that lead nowhere, empty required arguments and empty
// results show up. The test's expectations are then checked against the
// outputs. The AI designer tries every option this way before showing it.

// DryTest is how to try a flow: sample inputs, mock outputs for tool and AI
// steps (by step id), and what the outputs should look like.
type DryTest struct {
	Inputs map[string]string `json:"inputs,omitempty"`
	Mocks  map[string]any    `json:"mocks,omitempty"` // text, or JSON
	Expect []DryExpect       `json:"expect,omitempty"`
}

// DryExpect is what one step's output should be.
type DryExpect struct {
	Step     string   `json:"step"`
	NotEmpty bool     `json:"notEmpty,omitempty"`
	Contains []string `json:"contains,omitempty"`
	Fields   []string `json:"fields,omitempty"` // JSON paths that must exist
}

// DryStep is how one step went.
type DryStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Status string `json:"status"` // done | failed | off (not on the path taken) | blocked | not run
	Mocked bool   `json:"mocked,omitempty"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	Path   string `json:"path,omitempty"` // Decision: the paths taken
	Runs   int    `json:"runs,omitempty"` // times it ran (in loops)
}

// DryCheck is one check of the result.
type DryCheck struct {
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	Note  string `json:"note,omitempty"`
}

// DryResult is a dry run's outcome.
type DryResult struct {
	OK     bool              `json:"ok"`
	Steps  []DryStep         `json:"steps"`
	Checks []DryCheck        `json:"checks"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

// DryTool is what a dry run needs to know about a tool.
type DryTool struct {
	Output   *Schema
	OutputBy map[string]map[string]*Schema
	Required []string
}

const dryMaxItems = 3 // loop items tried

type dryRun struct {
	f        *Flow
	test     DryTest
	tools    map[string]DryTool
	li       LoopInfo
	steps    map[string]*DryStep
	gathered map[string][]ItemResult
}

// DryRunFlow tries f with test (see the package comment on dry runs).
func DryRunFlow(f *Flow, test DryTest, tools map[string]DryTool) DryResult {
	x := &dryRun{f: f, test: test, tools: tools, steps: map[string]*DryStep{}, gathered: map[string][]ItemResult{}}
	res := DryResult{Inputs: map[string]string{}}
	order, err := f.Order()
	if err != nil {
		res.Checks = append(res.Checks, DryCheck{Label: "The steps run in an order", OK: false, Note: err.Error()})
		return res
	}
	x.li, _ = f.Loops()
	for _, id := range order {
		n := f.Node(id)
		x.steps[id] = &DryStep{ID: id, Title: label(*n), Type: n.Type, Status: "not run"}
	}
	inputs := map[string]string{}
	for _, in := range f.Inputs {
		v := test.Inputs[in.Name]
		if strings.TrimSpace(v) == "" {
			v = firstNonEmptyStr(in.Default, sampleInput(in))
		}
		inputs[in.Name] = v
		res.Inputs[in.Name] = v
		if in.Type == "secret" {
			res.Inputs[in.Name] = SecretMask
		}
	}
	d := Data{Inputs: inputs, Steps: map[string]string{}, Files: map[string]string{}, Branches: map[string][]string{}, Flow: f.Name}
	x.run(order, &d, map[string]string{})
	// checks
	ranAll := true
	for _, id := range order {
		s := x.steps[id]
		if s.Status == "failed" || s.Status == "blocked" {
			ranAll = false
			res.Checks = append(res.Checks, DryCheck{Label: s.Title + " runs", OK: false, Note: s.Error})
		}
	}
	if ranAll {
		res.Checks = append(res.Checks, DryCheck{Label: "Every step on the path runs", OK: true})
	}
	reached := false
	for _, n := range f.Nodes {
		if n.Type != "output" {
			continue
		}
		s := x.steps[n.ID]
		switch s.Status {
		case "done":
			reached = true
			if strings.TrimSpace(s.Output) == "" {
				res.Checks = append(res.Checks, DryCheck{Label: s.Title + " has content", OK: false, Note: "the output is empty"})
			}
		}
	}
	if !reached {
		res.Checks = append(res.Checks, DryCheck{Label: "An output is produced", OK: false, Note: "no output step was reached with the sample data"})
	}
	for _, e := range test.Expect {
		s := x.steps[e.Step]
		if s == nil {
			continue
		}
		if s.Status != "done" {
			res.Checks = append(res.Checks, DryCheck{Label: s.Title + " gives the expected result", OK: false, Note: "it did not run (" + s.Status + ")"})
			continue
		}
		if e.NotEmpty && strings.TrimSpace(s.Output) == "" {
			res.Checks = append(res.Checks, DryCheck{Label: s.Title + " is not empty", OK: false})
		}
		for _, c := range e.Contains {
			ok := strings.Contains(strings.ToLower(s.Output), strings.ToLower(c))
			res.Checks = append(res.Checks, DryCheck{Label: fmt.Sprintf("%s contains %q", s.Title, c), OK: ok})
		}
		if len(e.Fields) > 0 {
			v, err := decodeOrdered(s.Output)
			for _, p := range e.Fields {
				ok := false
				if err == nil {
					_, perr := walkPath(v, p)
					ok = perr == nil
				}
				res.Checks = append(res.Checks, DryCheck{Label: fmt.Sprintf("%s has %s", s.Title, p), OK: ok, Note: map[bool]string{true: "", false: "not in its JSON"}[ok]})
			}
		}
	}
	res.OK = true
	for _, c := range res.Checks {
		res.OK = res.OK && c.OK
	}
	for _, id := range order {
		s := *x.steps[id]
		if len(s.Output) > 4000 {
			s.Output = s.Output[:4000] + "\n…"
		}
		res.Steps = append(res.Steps, s)
	}
	return res
}

func (x *dryRun) run(ids []string, d *Data, status map[string]string) string {
	f := x.f
	handled := map[string]bool{}
	firstErr := ""
	for _, id := range ids {
		if handled[id] {
			continue
		}
		n := f.Node(id)
		st := x.steps[id]
		parents := f.Parents(id)
		var live []string
		if len(parents) > 0 {
			blocked := false
			for _, p := range parents {
				if status[p] == "failed" {
					blocked = true
				}
			}
			if blocked {
				status[id] = "failed"
				if st.Status == "not run" {
					st.Status, st.Error = "blocked", "a step before it failed"
				}
				continue
			}
			live = f.LiveParents(id, func(p string) bool { return status[p] == "off" }, d.Branches)
			if len(live) == 0 {
				status[id] = "off"
				if st.Status == "not run" {
					st.Status = "off"
				}
				continue
			}
		}
		if n.Type == "loop" {
			body, g := x.li.Body(ids, id), x.li.Gather[id]
			for _, b := range body {
				handled[b] = true
			}
			handled[g] = true
			x.loop(n, live, body, g, d, status)
			if status[id] == "failed" && firstErr == "" {
				firstErr = st.Error
			}
			continue
		}
		dd := *d
		dd.Live = live
		out, mocked, err := x.exec(n, dd, live)
		st.Runs++
		if err != nil {
			status[id] = "failed"
			st.Status, st.Error = "failed", err.Error()
			if firstErr == "" {
				firstErr = label(*n) + ": " + err.Error()
			}
			continue
		}
		if n.Type == "decision" {
			bs, _ := Decide(*n, out, dd)
			d.Branches = maps.Clone(d.Branches)
			d.Branches[id] = bs
			var ls []string
			for _, b := range bs {
				ls = append(ls, BranchLabel(*n, b))
			}
			st.Path = strings.Join(ls, ", ")
		}
		status[id] = "done"
		if st.Status != "failed" {
			st.Status = "done"
		}
		st.Output, st.Mocked = out, mocked
		d.Steps[id] = out
		d.Files[id] = ".codegen/runs/dry/steps/" + id + "/output.txt"
	}
	return firstErr
}

func (x *dryRun) loop(n *Node, live, body []string, g string, d *Data, status map[string]string) {
	st := x.steps[n.ID]
	st.Runs++
	var in []string
	for _, p := range live {
		in = append(in, d.Steps[p])
	}
	src := strings.Join(in, "\n")
	if strings.TrimSpace(n.Source) != "" {
		dd := *d
		dd.Input = src
		src = Render(n.Source, dd)
	}
	items, err := SplitItems(src, *n)
	if err != nil {
		status[n.ID], st.Status, st.Error = "failed", "failed", err.Error()
		return
	}
	b, _ := json.MarshalIndent(items, "", "  ")
	st.Output, st.Status = string(b), "done"
	if len(items) == 0 {
		st.Error = "the list is empty, so the steps inside the loop did not run"
	}
	if len(items) > dryMaxItems {
		items = items[:dryMaxItems]
	}
	gparents := x.f.Parents(g)
	var results []ItemResult
	for i, it := range items {
		di := *d
		di.Steps, di.Files, di.Branches = maps.Clone(d.Steps), maps.Clone(d.Files), maps.Clone(d.Branches)
		di.Steps[n.ID] = it
		di.Item, di.ItemIndex, di.ItemCount = it, i+1, len(items)
		di.Loops = maps.Clone(d.Loops)
		if di.Loops == nil {
			di.Loops = map[string]LoopItem{}
		}
		di.Loops[n.ID] = LoopItem{Item: it, Index: i + 1, Count: len(items)}
		si := maps.Clone(status)
		si[n.ID] = "done"
		errText := x.run(body, &di, si)
		r := ItemResult{Item: it}
		var parts []string
		for _, p := range gparents {
			if si[p] == "done" {
				parts = append(parts, di.Steps[p])
			}
		}
		r.Output = strings.Join(parts, "\n\n")
		if errText != "" {
			r.Error = errText
			if !n.ContinueOnError {
				status[n.ID], st.Status, st.Error = "failed", "failed", fmt.Sprintf("item %d: %s", i+1, errText)
				return
			}
		}
		results = append(results, r)
	}
	status[n.ID] = "done"
	d.Steps[n.ID] = st.Output
	gn := x.f.Node(g)
	gs := x.steps[g]
	out, err := Gather(*gn, results, *d)
	gs.Runs++
	if err != nil {
		status[g], gs.Status, gs.Error = "failed", "failed", err.Error()
		return
	}
	status[g], gs.Status, gs.Output = "done", "done", out
	d.Steps[g] = out
}

// exec runs one step: tools and AI steps are mocked.
func (x *dryRun) exec(n *Node, d Data, live []string) (string, bool, error) {
	parts := make([]string, 0, len(live))
	for _, p := range live {
		parts = append(parts, d.Steps[p])
	}
	in := strings.Join(parts, "\n\n")
	d.Input = in
	d.InputFile = ".codegen/runs/dry/steps/" + n.ID + "/input.txt"
	mock := func(shape *Schema, what string) string {
		if m, ok := x.test.Mocks[n.ID]; ok {
			if s, ok := m.(string); ok {
				return s
			}
			b, _ := json.MarshalIndent(m, "", "  ")
			return string(b)
		}
		if shape != nil && shape.Known() {
			b, _ := json.MarshalIndent(SampleOf(shape, n.ID), "", "  ")
			return string(b)
		}
		return "(sample " + what + " of " + label(*n) + ")"
	}
	switch n.Type {
	case "start":
		b, _ := json.MarshalIndent(x.f.MaskSecrets(d.Inputs), "", "  ")
		return string(b), false, nil
	case "tool":
		t, ok := x.tools[n.Tool]
		if !ok {
			return "", false, fmt.Errorf("the tool %q is not available", n.Tool)
		}
		if strings.TrimSpace(n.Instruction) == "" { // arguments the tool needs must get a value
			for _, p := range t.Required {
				if strings.TrimSpace(Render(n.Args[p], d)) == "" {
					return "", false, fmt.Errorf("the required argument %q is empty with the sample data", p)
				}
			}
		}
		return mock(ToolOutput(t.Output, t.OutputBy, n.Args), "output"), true, nil
	case "ai":
		return mock(n.OutputSchema, "answer"), true, nil
	case "improve":
		if ImprovePair(*n) == "before" {
			return in, false, nil // the tool's result, improved
		}
		return mock(n.OutputSchema, "result"), true, nil
	case "transform":
		if n.Source != "" {
			in = Render(n.Source, d)
		}
		if n.Op == "ai" || n.Op == "command" {
			return mock(nil, "result"), true, nil
		}
		out, err := Transform(*n, in, d)
		return out, false, err
	case "merge":
		titles := map[string]string{}
		for _, p := range live {
			if pn := x.f.Node(p); pn != nil && pn.Title != "" {
				titles[p] = pn.Title
			}
		}
		out, err := Merge(*n, live, d, titles)
		return out, false, err
	case "decision", "filter", "aggregate":
		if n.Source != "" {
			in = Render(n.Source, d)
		}
		switch n.Type {
		case "filter":
			out, err := FilterRows(*n, in, d)
			return out, false, err
		case "aggregate":
			out, err := Aggregate(*n, in, d)
			return out, false, err
		}
		_, err := Decide(*n, in, d)
		return in, false, err
	case "gather":
		out, err := Gather(*n, x.gathered[n.ID], d)
		return out, false, err
	case "output":
		content := in
		if strings.TrimSpace(n.Template) != "" {
			content = Render(n.Template, d)
		}
		out, err := Convert(content, n.Format)
		return out, false, err
	}
	return in, false, nil
}

func sampleInput(in Input) string {
	switch in.Type {
	case "number":
		return "3"
	case "boolean":
		return "true"
	case "choice", "multichoice":
		if len(in.Options) > 0 {
			return in.Options[0]
		}
	case "file", "files":
		return "README.md"
	case "secret":
		return "sample-secret-value"
	}
	return "sample " + firstNonEmptyStr(in.Label, in.Name)
}

// SampleOf makes example data of a shape: a few varied elements per list,
// the first allowed values, dates, numbers, so later steps have data.
func SampleOf(s *Schema, seed string) any {
	return sampleOf(s, seed, 0, 0)
}

func sampleOf(s *Schema, name string, i, depth int) any {
	if s == nil || depth > 8 {
		return "sample"
	}
	switch s.Type {
	case "object":
		o := &Obj{Vals: map[string]any{}}
		for _, p := range s.Properties {
			o.set(p.Name, sampleOf(p.Schema, p.Name, i, depth+1))
		}
		return o
	case "array":
		n := 3
		out := make([]any, n)
		for k := range out {
			out[k] = sampleOf(s.Items, name, k, depth+1)
		}
		return out
	case "integer":
		return json.Number(fmt.Sprint(i + 1))
	case "number":
		return json.Number(fmt.Sprintf("%d.5", (i+1)*10))
	case "boolean":
		return i%2 == 0
	case "null":
		return nil
	case "string":
		if len(s.Enum) > 0 {
			return s.Enum[i%len(s.Enum)]
		}
		switch s.Format {
		case "date":
			return fmt.Sprintf("2026-11-%02d", 10+i)
		case "date-time":
			return fmt.Sprintf("2026-11-%02dT09:00:00Z", 10+i)
		}
		return fmt.Sprintf("%s %d", name, i+1)
	}
	return fmt.Sprintf("%s %d", name, i+1)
}
