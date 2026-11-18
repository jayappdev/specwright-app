package core

// Flow runs: executing a run — passes, items, the node graph, loops and
// per-step timeouts. The run API and state types are in flowrun.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"codegen/internal/flow"
	"codegen/internal/store"
)

func (h *Hub) execFlow(ctx context.Context, root string, fr *flowRun) {
	defer close(fr.done)
	defer fr.cancel()
	o := fr.st.Options
	final, reason := "done", ""
	defer func() {
		fr.update(func(st *FlowRunState) {
			st.Status, st.Error, st.Ended, st.Review = final, reason, time.Now(), nil
			st.Usage.DurationMS = time.Since(st.Started).Milliseconds()
		})
		st := fr.snapshot()
		h.flowPublish(fr, "run.usage", fmt.Sprintf("Usage: %d in · %d out · %d tools · %d AI calls", st.Usage.InputTokens, st.Usage.OutputTokens, st.Usage.ToolCalls, st.Usage.ModelCalls),
			map[string]any{"input_tokens": st.Usage.InputTokens, "output_tokens": st.Usage.OutputTokens, "cost_usd": st.Usage.CostUSD, "premium_requests": st.Usage.PremiumRequests, "tool_calls": st.Usage.ToolCalls})
		h.flowState(fr)
		h.flowPublish(fr, "run.end", "Flow "+final, map[string]any{"state": final, "reason": reason})
		_ = h.Store.SaveRun(store.Run{ID: st.ID, Workspace: root, Workflow: st.FlowID, Step: "flow", Agent: st.Agent, Model: st.Model, State: final, Reason: reason, Started: st.Started, Ended: st.Ended, Events: fr.seq, Usage: st.Usage,
			Kind: "flow", Title: st.FlowName, Ask: flowAsk(st.Options.Inputs), Trigger: triggerTag(st.Options.Trigger), TriggerName: triggerName(st.Options.Trigger)})
		minutes := 0
		if final == "done" {
			minutes = st.MinutesSaved * max(1, st.Passes*max(1, st.Items))
		}
		_ = h.Store.Ledger(store.LedgerEntry{At: time.Now(), RunID: st.ID, Workspace: root, Workflow: "flow:" + st.FlowID, Step: "flow", Agent: st.Agent, Model: st.Model, State: final, Usage: st.Usage, MinutesSaved: minutes})
		h.mu.Lock()
		delete(h.flowRuns, st.ID)
		h.mu.Unlock()
		go h.onFlowFinished(root, st) // flows that start after this one (triggers.go)
	}()

	if !o.StartAt.IsZero() && time.Until(o.StartAt) > 0 {
		fr.update(func(st *FlowRunState) { st.Status = "scheduled" })
		h.flowState(fr)
		h.flowPublish(fr, "flow.scheduled", "Starts at "+o.StartAt.Local().Format("15:04, Jan 2"), nil)
		select {
		case <-time.After(time.Until(o.StartAt)):
		case <-ctx.Done():
			final, reason = "stopped", "stopped before it started"
			return
		case d := <-fr.decide:
			if d.Action == "stop" {
				final, reason = "stopped", "stopped before it started"
				return
			}
		}
		fr.update(func(st *FlowRunState) { st.Status = "running" })
	}
	if err := h.promptSecrets(ctx, fr); err != nil {
		final, reason = "failed", err.Error()
		return
	}
	o = fr.st.Options

	items, err := flowItems(o)
	if err != nil {
		final, reason = "failed", err.Error()
		return
	}
	passes := max(1, min(o.Repeat, 50))
	fr.update(func(st *FlowRunState) { st.Passes, st.Items = passes, len(items) })
	failed := false
	for pass := 1; pass <= passes; pass++ {
		for i, item := range items {
			inputs := o.values()
			label := ""
			if item != nil {
				inputs[o.ForEach.Input] = *item
				label = strings.TrimSpace(*item)
				if len(label) > 60 {
					label = label[:60] + "…"
				}
			}
			fr.update(func(st *FlowRunState) {
				st.Pass, st.Item, st.ItemLabel, st.Status = pass, i+1, label, "running"
				st.Steps = stepStates(fr.flow)
			})
			if passes > 1 || len(items) > 1 {
				h.flowPublish(fr, "flow.pass", passLabel(pass, passes, i+1, len(items), label), nil)
			}
			h.flowState(fr)
			err := h.execPass(ctx, root, fr, inputs, label)
			if errors.Is(err, errFlowStopped) {
				final, reason = "stopped", "stopped by you"
				return
			}
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					final, reason = "failed", fmt.Sprintf("the run took longer than %d minutes", o.TimeoutMinutes)
				} else {
					final, reason = "stopped", "stopped by you"
				}
				return
			}
			if err != nil {
				failed = true
				reason = err.Error()
			}
		}
	}
	if failed {
		final = "failed"
	}
}

func passLabel(pass, passes, item, items int, label string) string {
	var parts []string
	if passes > 1 {
		parts = append(parts, fmt.Sprintf("run %d of %d", pass, passes))
	}
	if items > 1 {
		parts = append(parts, fmt.Sprintf("item %d of %d", item, items))
	}
	s := strings.Join(parts, " · ")
	if label != "" {
		s += ": " + label
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// flowItems expands "run once per item"; one nil item means a single run.
func flowItems(o FlowRunOptions) ([]*string, error) {
	fe := o.ForEach
	if fe == nil || fe.Input == "" {
		return []*string{nil}, nil
	}
	src := o.Inputs[fe.Input]
	var raw []string
	switch fe.Split {
	case "json":
		var arr []any
		if err := json.Unmarshal([]byte(strings.TrimSpace(src)), &arr); err != nil {
			return nil, fmt.Errorf("%s is not a JSON array: %v", fe.Input, err)
		}
		for _, v := range arr {
			if s, ok := v.(string); ok {
				raw = append(raw, s)
			} else {
				b, _ := json.Marshal(v)
				raw = append(raw, string(b))
			}
		}
	case "csv":
		rows, err := flow.Transform(flow.Node{Op: "csv-to-json"}, src, flow.Data{})
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		_ = json.Unmarshal([]byte(rows), &arr)
		for _, r := range arr {
			b, _ := json.Marshal(r)
			raw = append(raw, string(b))
		}
	default:
		for _, l := range strings.Split(src, "\n") {
			if strings.TrimSpace(l) != "" {
				raw = append(raw, strings.TrimRight(l, "\r"))
			}
		}
	}
	var re *regexp.Regexp
	if fe.Filter != "" {
		re = regexp.MustCompile(fe.Filter)
	}
	limit := fe.Max
	if limit <= 0 {
		limit = 100
	}
	var out []*string
	for i := range raw {
		if re != nil && !re.MatchString(raw[i]) {
			continue
		}
		out = append(out, &raw[i])
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no items in %s%s", fe.Input, map[bool]string{true: " match the filter", false: ""}[re != nil])
	}
	return out, nil
}

func (h *Hub) execPass(ctx context.Context, root string, fr *flowRun, inputs map[string]string, item string) error {
	f := fr.flow
	order, err := f.Order()
	if err != nil {
		return err
	}
	li, _ := f.Loops()
	d := flow.Data{Inputs: inputs, Steps: map[string]string{}, Context: h.flowContext(f), Skills: h.flowSkills(f), Flow: f.Name,
		Files: map[string]string{}, Branches: map[string][]string{}}
	stopped := false
	return h.runNodes(ctx, root, fr, order, li, &d, map[string]string{}, item, &stopped)
}

// runNodes runs steps ids (in run order) with data d. A Loop step runs the
// steps inside it once per item (runLoop); they are skipped here.
func (h *Hub) runNodes(ctx context.Context, root string, fr *flowRun, ids []string, li flow.LoopInfo, d *flow.Data, status map[string]string, item string, stopped *bool) error {
	f := fr.flow
	o := fr.st.Options
	skip := map[string]bool{}
	for _, s := range o.Skip {
		skip[s] = true
	}
	handled := map[string]bool{}
	var firstErr error
	for _, id := range ids {
		if *stopped {
			break
		}
		if handled[id] {
			continue
		}
		n := *f.Node(id) // a copy: retries may change it for this run only
		// after a Decision: only the steps on the path it took run
		var live []string
		if ps := f.Parents(id); len(ps) > 0 {
			live = f.LiveParents(id, func(p string) bool { return status[p] == "off" }, d.Branches)
			if len(live) == 0 && !slices.ContainsFunc(ps, func(p string) bool { return status[p] == "failed" }) {
				fr.update(func(st *FlowRunState) {
					s := fr.step(id)
					s.Status, s.Error = "skipped", "not on the path the flow took"
				})
				status[id] = "off"
				h.flowState(fr)
				continue
			}
		}
		blocked := ""
		fixing := n.Type == "improve" && flow.ImprovePair(n) == "before" // it runs a failed tool again
		for _, p := range f.Parents(id) {
			ran := false // the tool ran and failed (not skipped because a step before it failed)
			fr.update(func(st *FlowRunState) { s := fr.step(p); ran = s != nil && s.Status == "failed" })
			if status[p] == "failed" && !(fixing && ran) {
				blocked = "a step before it failed"
			}
		}
		if skip[id] || blocked != "" {
			why := firstNonEmpty(blocked, "skipped by you")
			fr.update(func(st *FlowRunState) { s := fr.step(id); s.Status = "skipped"; s.Error = why })
			status[id] = "skipped"
			if blocked != "" || n.Type == "loop" {
				status[id] = "failed" // keep blocking the steps after it
			}
			if fixing { // the tool's result goes on as it is
				for _, p := range f.Parents(id) {
					d.Steps[id] = d.Steps[p]
					if status[p] == "failed" {
						status[id] = "failed"
						if firstErr == nil {
							firstErr = fmt.Errorf("%s failed, and %s, which runs it again, was skipped", firstNonEmpty(f.Node(p).Title, p), firstNonEmpty(n.Title, id))
						}
					}
				}
			}
			h.flowState(fr)
			continue
		}
		if r, ok := o.Reuse[id]; ok && d.ItemCount == 0 { // an earlier run's result, not run again
			reused := []string{id}
			if n.Type == "loop" { // the whole loop: its steps and its Gather
				body, g := li.Body(ids, id), li.Gather[id]
				for _, b := range append(body, g) {
					handled[b] = true
					status[b] = "done"
					if gr, ok := o.Reuse[b]; ok {
						d.Steps[b] = gr.Output
						reused = append(reused, b)
					}
				}
			}
			d.Steps[id] = r.Output
			if n.Type == "decision" {
				d.Branches = maps.Clone(d.Branches)
				if d.Branches == nil {
					d.Branches = map[string][]string{}
				}
				d.Branches[id] = r.Branches
			}
			for _, x := range reused {
				out := d.Steps[x]
				if p := h.flowOutputFile(fr, x, out); p != "" {
					d.Files[x] = p
				}
				fr.update(func(st *FlowRunState) {
					s := fr.step(x)
					s.Status, s.Output, s.Notes = "done", "", []string{"reused from the earlier run"}
					s.Output, s.Truncated = clip(out)
					if x == id && n.Type == "decision" {
						s.Branches = r.Branches
					}
				})
				status[x] = "done"
			}
			h.flowState(fr)
			continue
		}
		if n.Type == "loop" {
			body, g := li.Body(ids, id), li.Gather[id]
			for _, b := range body {
				handled[b] = true
			}
			handled[g] = true
			err := h.runLoop(ctx, root, fr, &n, body, g, li, d, status, item, stopped)
			if errors.Is(err, errFlowStopped) {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s failed: %v", firstNonEmpty(n.Title, n.ID), err)
			}
			continue
		}
		partner := ""
		if n.Type == "improve" {
			partner, _ = f.ImprovePartner(n)
			if flow.ImprovePair(n) == "after" {
				handled[partner] = true // it runs inside the Improve step
			}
		}
		dd := *d
		dd.Live = live
		out, err := h.runFlowStep(ctx, root, fr, &n, dd, item)
		if partner != "" { // the paired tool's last run is its result
			fr.mu.Lock()
			pr, ok := fr.paired[partner]
			delete(fr.paired, partner)
			fr.mu.Unlock()
			if ok {
				d.Steps[partner] = pr.out
				if p := h.flowOutputFile(fr, partner, pr.out); p != "" {
					d.Files[partner] = p
				}
			}
			status[partner] = map[bool]string{true: "failed", false: "done"}[err != nil]
			if err == nil {
				fr.update(func(st *FlowRunState) {
					if s := fr.step(partner); s != nil && s.Status == "failed" {
						s.Status = "done" // its error was the Improve step's to fix
					}
				})
			} else if flow.ImprovePair(n) == "after" {
				fr.update(func(st *FlowRunState) {
					if s := fr.step(partner); s != nil && s.Status == "pending" {
						s.Status, s.Error = "skipped", firstNonEmpty(n.Title, n.ID)+" failed"
					}
				})
			}
		}
		if errors.Is(err, errFlowStopped) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && n.Type == "decision" { // the paths it took
			var bs []string
			fr.update(func(st *FlowRunState) { bs = fr.step(id).Branches })
			d.Branches = maps.Clone(d.Branches)
			if d.Branches == nil {
				d.Branches = map[string][]string{}
			}
			d.Branches[id] = bs
		}
		if err != nil {
			status[id] = "failed"
			if firstErr == nil && !fixedNext(f, id) {
				firstErr = fmt.Errorf("%s failed: %v", firstNonEmpty(n.Title, n.ID), err)
			}
			continue
		}
		// review
		if h.wantsReview(fr, &n, d.ItemCount > 0) {
			out, err = h.reviewStep(ctx, root, fr, &n, *d, item, out)
			if err != nil {
				return err
			}
		}
		d.Steps[id] = out
		if p := h.flowOutputFile(fr, id, out); p != "" {
			d.Files[id] = p // {{<step>.output_file}}
		}
		if d.ItemCount > 0 { // each item's result is kept too
			_, _ = h.writeRunFile(fmt.Sprintf("%s/items/%03d.%s", stepFilesDir(fr.st.ID, id), d.ItemIndex, contentExt(out)), out)
		}
		status[id] = "done"
		if o.StopAfter != "" && o.StopAfter == id {
			// mark the rest as skipped
			fr.update(func(st *FlowRunState) {
				for i := range st.Steps {
					if st.Steps[i].Status == "pending" {
						st.Steps[i].Status, st.Steps[i].Error = "skipped", "the run stops after "+firstNonEmpty(n.Title, n.ID)
					}
				}
			})
			h.flowState(fr)
			*stopped = true
			break
		}
	}
	return firstErr
}

// runLoop splits the loop's list into items, runs body once per item and
// gathers the results with the Gather step g.
func (h *Hub) runLoop(ctx context.Context, root string, fr *flowRun, n *flow.Node, body []string, g string, li flow.LoopInfo, d *flow.Data, status map[string]string, item string, stopped *bool) error {
	f := fr.flow
	start := time.Now()
	title := firstNonEmpty(n.Title, n.ID)
	setLoop := func(fn func(s *FlowStepState)) {
		fr.update(func(st *FlowRunState) {
			if s := fr.step(n.ID); s != nil {
				fn(s)
			}
		})
		h.flowState(fr)
	}
	fail := func(err error) error {
		err = errors.New(h.Store.Redactor().String(err.Error()))
		setLoop(func(s *FlowStepState) {
			s.Status, s.Error, s.Ended, s.DurationMS = "failed", err.Error(), time.Now(), time.Since(start).Milliseconds()
		})
		status[n.ID] = "failed"
		for _, b := range body {
			if status[b] == "" {
				status[b] = "failed"
			}
		}
		status[g] = "failed"
		fr.update(func(st *FlowRunState) {
			if s := fr.step(g); s != nil && (s.Status == "pending" || s.Status == "") {
				s.Status, s.Error = "skipped", "the loop "+title+" failed"
			}
		})
		h.flowPublish(fr, "flow.step", title+" failed: "+err.Error(), map[string]any{"_step": n.ID, "node": n.ID, "kind": "loop", "status": "failed"})
		return err
	}
	setLoop(func(s *FlowStepState) {
		s.Status, s.Started, s.Error, s.Attempts, s.Iteration, s.Failed = "running", start, "", s.Attempts+1, 0, 0
	})
	h.flowPublish(fr, "flow.step", "Running "+title, map[string]any{"_step": n.ID, "node": n.ID, "kind": "loop", "status": "running"})

	// the list: the connected steps' output, or the loop's own template
	var in []string
	for _, p := range f.LiveParents(n.ID, func(p string) bool { return status[p] == "off" }, d.Branches) {
		in = append(in, d.Steps[p])
	}
	src := strings.Join(in, "\n")
	if strings.TrimSpace(n.Source) != "" {
		dd := *d
		dd.Input = src
		src = flow.Render(n.Source, dd)
	}
	items, err := flow.SplitItems(src, *n)
	if err != nil {
		return fail(err)
	}
	list, _ := json.MarshalIndent(items, "", "  ")
	setLoop(func(s *FlowStepState) { s.Iterations = len(items); s.Output, s.Truncated = clip(string(list)) })
	h.flowPublish(fr, "flow.loop", fmt.Sprintf("%s: %d item%s", title, len(items), map[bool]string{true: "", false: "s"}[len(items) == 1]), map[string]any{"_step": n.ID, "items": len(items)})

	gparents := f.Parents(g)
	results := make([]flow.ItemResult, 0, len(items))
	failed := 0
	for i, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		short := it
		if len(short) > 60 {
			short = short[:60] + "…"
		}
		setLoop(func(s *FlowStepState) { s.Iteration = i + 1 })
		h.flowPublish(fr, "flow.loop", fmt.Sprintf("%s · item %d of %d: %s", title, i+1, len(items), short), map[string]any{"_step": n.ID, "item": i + 1, "items": len(items)})
		di := *d
		di.Steps, di.Files, di.Branches = maps.Clone(d.Steps), maps.Clone(d.Files), maps.Clone(d.Branches)
		di.Steps[n.ID] = it // the loop's output, for the steps it feeds, is the item
		di.Item, di.ItemIndex, di.ItemCount = it, i+1, len(items)
		di.Loops = maps.Clone(d.Loops) // {{<loop>.item}}: outer loops' items stay readable
		if di.Loops == nil {
			di.Loops = map[string]flow.LoopItem{}
		}
		di.Loops[n.ID] = flow.LoopItem{Item: it, Index: i + 1, Count: len(items)}
		si := maps.Clone(status)
		si[n.ID] = "done"
		err := h.runNodes(ctx, root, fr, body, li, &di, si, item, stopped)
		if errors.Is(err, errFlowStopped) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r := flow.ItemResult{Item: it}
		var parts []string
		for _, p := range gparents {
			if si[p] == "done" {
				parts = append(parts, di.Steps[p])
			}
		}
		r.Output = strings.Join(parts, "\n\n")
		if err != nil {
			r.Error = h.Store.Redactor().String(err.Error())
			failed++
			setLoop(func(s *FlowStepState) { s.Failed = failed })
			if !n.ContinueOnError {
				return fail(fmt.Errorf("item %d of %d (%s): %s", i+1, len(items), short, r.Error))
			}
		}
		results = append(results, r)
		if *stopped {
			break
		}
	}
	setLoop(func(s *FlowStepState) {
		s.Status, s.Ended, s.DurationMS = "done", time.Now(), time.Since(start).Milliseconds()
		if failed > 0 {
			s.Notes = append(s.Notes, fmt.Sprintf("%d of %d items failed; the loop went on", failed, len(items)))
		}
	})
	h.flowPublish(fr, "flow.step", fmt.Sprintf("%s done: %d item%s", title, len(items), map[bool]string{true: "", false: "s"}[len(items) == 1]),
		map[string]any{"_step": n.ID, "node": n.ID, "kind": "loop", "status": "done", "duration_ms": time.Since(start).Milliseconds()})
	status[n.ID] = "done"
	for _, b := range body {
		status[b] = "done"
	}
	d.Steps[n.ID] = string(list)

	// gather
	gn := *f.Node(g)
	fr.mu.Lock()
	if fr.gathered == nil {
		fr.gathered = map[string][]flow.ItemResult{}
	}
	fr.gathered[g] = results
	fr.mu.Unlock()
	out, err := h.runFlowStep(ctx, root, fr, &gn, *d, item)
	if err != nil {
		status[g] = "failed"
		return err
	}
	d.Steps[g] = out
	if p := h.flowOutputFile(fr, g, out); p != "" {
		d.Files[g] = p
	}
	status[g] = "done"
	return nil
}

// fixedNext: the step after id is an Improve step that runs it again, so
// its failure is that step's to fix (it fails if it cannot).
func fixedNext(f *flow.Flow, id string) bool {
	cs := f.Children(id)
	if len(cs) != 1 {
		return false
	}
	c := f.Node(cs[0])
	return c != nil && c.Type == "improve" && flow.ImprovePair(*c) == "before"
}

func (h *Hub) wantsReview(fr *flowRun, n *flow.Node, inLoop bool) bool {
	if fr.noReview || n.Type == "start" || n.Type == "gather" || n.Type == "improve" { // an Improve step asks for approval itself
		return false
	}
	if inLoop && fr.st.Options.Review != "each" { // once per item would be too many: only marked steps
		return n.Review && fr.st.Options.Review != "none"
	}
	switch fr.st.Options.Review {
	case "none":
		return false
	case "each":
		return true
	case "marked":
		return n.Review
	}
	return n.Review || n.Type == "ai" || (n.Type == "transform" && n.Op == "ai")
}

// runFlowStep runs one step and records its state.
func (h *Hub) runFlowStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, item string) (string, error) {
	start := time.Now()
	fr.update(func(st *FlowRunState) {
		s := fr.step(n.ID)
		s.Status, s.Started, s.Error, s.Attempts = "running", start, "", s.Attempts+1
		if d.ItemCount > 0 {
			s.Iteration, s.Iterations = d.ItemIndex, d.ItemCount
		}
	})
	h.flowState(fr)
	h.flowPublish(fr, "flow.step", "Running "+firstNonEmpty(n.Title, n.ID), map[string]any{"_step": n.ID, "node": n.ID, "kind": n.Type, "status": "running"})
	out, u, err := h.execStep(ctx, root, fr, n, d, item)
	// secret values never travel on: not to AI steps, files, outputs or the screen
	out = h.Store.Redactor().String(out)
	if err != nil {
		err = errors.New(h.Store.Redactor().String(err.Error()))
	}
	fr.update(func(st *FlowRunState) {
		s := fr.step(n.ID)
		s.Ended, s.DurationMS = time.Now(), time.Since(start).Milliseconds()
		s.Usage.Add(u)
		st.Usage.Add(u)
		if err != nil {
			s.Status, s.Error = "failed", err.Error()
		} else {
			s.Status = "done"
		}
		s.Output, s.Truncated = clip(out)
	})
	summary := firstNonEmpty(n.Title, n.ID) + " done"
	if err != nil {
		summary = firstNonEmpty(n.Title, n.ID) + " failed: " + err.Error()
	}
	h.flowPublish(fr, "flow.step", summary, map[string]any{"_step": n.ID, "node": n.ID, "kind": n.Type, "status": map[bool]string{true: "failed", false: "done"}[err != nil], "duration_ms": time.Since(start).Milliseconds()})
	h.flowState(fr)
	return out, err
}

func stepTimeout(o FlowRunOptions, n *flow.Node) time.Duration {
	if o.StepTimeoutSeconds > 0 {
		return time.Duration(o.StepTimeoutSeconds) * time.Second
	}
	if n.Type == "ai" || (n.Type == "transform" && n.Op == "ai") || (n.Type == "tool" && strings.TrimSpace(n.Instruction) != "") {
		return 10 * time.Minute // includes the agent's investigation
	}
	return 2 * time.Minute
}

// toolRunTimeout is how long the tool itself may run.
func toolRunTimeout(o FlowRunOptions) time.Duration {
	if o.StepTimeoutSeconds > 0 {
		return time.Duration(o.StepTimeoutSeconds) * time.Second
	}
	return 2 * time.Minute
}
