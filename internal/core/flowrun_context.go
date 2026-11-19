package core

// Running a Context step (flow/context.go): gather its sources from the
// project, refine them into a pack within its budget, condense it with the
// AI when the step asks, and record what was kept for the run view. The AI
// and Improve steps after it get the pack in their prompt (contextPacks).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codegen/internal/flow"
	"codegen/internal/store"
	"codegen/internal/watch"
)

const (
	ctxMaxFiles    = 200
	ctxMaxFileSize = 512 << 10
)

// ctxSkipFiles are never context: lock files and minified files (and
// SpecWright's own .codegen folder, which is not even counted).
var ctxSkipFiles = []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum", "Cargo.lock", "poetry.lock", "composer.lock", "Gemfile.lock",
	"*.min.js", "*.min.css", "*.map", "*.lock"}

func (h *Hub) contextStep(ctx context.Context, root string, fr *flowRun, n *flow.Node, d flow.Data, parents []string) (string, store.Usage, error) {
	var u store.Usage
	f := fr.flow
	var pieces []flow.CtxPiece
	for _, p := range parents { // the connected steps, each its own source
		title := p
		if pn := f.Node(p); pn != nil {
			title = firstNonEmpty(pn.Title, p)
		}
		pieces = append(pieces, flow.CtxPiece{Label: title, Kind: "step", Text: d.Steps[p]})
	}
	for _, s := range n.Sources {
		pc := flow.CtxPiece{Label: flow.CtxLabel(s), Kind: s.Kind, Priority: s.Priority, Focus: flow.Render(s.Query, d)}
		switch s.Kind {
		case "files":
			pc.Parts, pc.Note = h.ctxFiles(root, s, d)
		case "text":
			pc.Text = flow.Render(s.Text, d)
		case "inputs":
			b, _ := json.MarshalIndent(f.MaskSecrets(d.Inputs), "", "  ")
			pc.Text = string(b)
		case "history":
			pc.Parts, pc.Note = h.ctxHistory(root, fr.st.ID, flow.Render(s.Query, d))
		case "lessons":
			ls, _ := h.Store.Lessons(root, f.ID, "")
			for _, l := range ls {
				if !l.Off {
					pc.Parts = append(pc.Parts, fmt.Sprintf("- (%s) %s", l.Step, l.Text))
				}
			}
			if len(pc.Parts) == 0 {
				pc.Note = "no lessons for this flow yet"
			}
		case "url":
			addr := flow.Render(s.URL, d)
			out, err := h.tools.Call(ctx, "fetch_webpage", map[string]any{"url": addr, "max_chars": 40000})
			if err != nil {
				pc.Note = "could not read it: " + err.Error()
			} else {
				pc.Text = out
			}
		}
		pieces = append(pieces, pc)
	}
	for i := range pieces {
		pieces[i].ID = fmt.Sprintf("S%d", i+1)
		pieces[i].Text = h.Store.Redactor().String(pieces[i].Text)
		for j := range pieces[i].Parts {
			pieces[i].Parts[j] = h.Store.Redactor().String(pieces[i].Parts[j])
		}
	}
	title := firstNonEmpty(n.Title, n.ID)
	purpose := flow.Render(n.Goal, d)
	pack, rep := flow.BuildContext(title, purpose, flow.CtxBudget(*n), pieces)
	if n.Condense && rep.Used > 0 {
		words := max(150, flow.CtxBudget(*n)/6)
		prompt := "Condense the context below into a brief for the AI steps that use it next.\n" +
			"Purpose: " + firstNonEmpty(purpose, "(not given: keep what an engineer would need)") + "\n\n" +
			"Keep facts, numbers, names, paths, decisions and open questions that serve the purpose; drop the rest. " +
			"Do not add anything that is not in the context. End every point with its source label, like [S2]. " +
			fmt.Sprintf("Reply with only the brief, in Markdown, at most about %d words.\n\n", words) + pack
		out, cu, err := h.flowAgent(withFlowStep(ctx, fr.st.ID, n.ID), root, fr, n.ID, prompt, fr.st.Options.Model)
		u.Add(cu)
		if err != nil {
			return "", u, fmt.Errorf("condensing the context: %v", err)
		}
		head := "# Context: " + title + "\n"
		if purpose != "" {
			head += "Purpose: " + purpose + "\n"
		}
		var srcs []string
		for _, s := range rep.Sources {
			if s.Kept > 0 {
				srcs = append(srcs, fmt.Sprintf("[%s] %s", s.ID, s.Label))
			}
		}
		pack = head + "Sources: " + strings.Join(srcs, "; ") + "\n\n" + strings.TrimSpace(out) + "\n"
		rep.Condensed, rep.Used = true, flow.Tokens(out)
	}
	fr.update(func(st *FlowRunState) {
		if s := fr.step(n.ID); s != nil {
			r := rep
			s.Context = &r
		}
	})
	h.flowPublish(fr, "flow.context", fmt.Sprintf("%s: %d of %d tokens from %d sources%s", title, rep.Used, rep.Budget, len(rep.Sources), map[bool]string{true: ", condensed", false: ""}[rep.Condensed]),
		map[string]any{"_step": n.ID, "report": rep})
	return pack, u, nil
}

// ctxFiles reads a files source: the project's files matching its patterns.
func (h *Hub) ctxFiles(root string, s flow.CtxSource, d flow.Data) ([]string, string) {
	var pats []string
	for _, p := range s.Paths {
		if p = strings.TrimSpace(flow.Render(p, d)); p != "" && !strings.Contains(p, "..") {
			pats = append(pats, p)
		}
	}
	var parts []string
	n, skipped := 0, 0
	for _, rel := range watch.ListFiles(root, 50000) {
		hit := false
		for _, p := range pats {
			hit = hit || flow.GlobMatch(p, rel)
		}
		if !hit || rel == ".codegen" || strings.HasPrefix(rel, ".codegen/") { // SpecWright's own files: never context
			continue
		}
		skip := false
		for _, g := range ctxSkipFiles {
			skip = skip || flow.GlobMatch(g, rel)
		}
		if skip {
			skipped++
			continue
		}
		if n == ctxMaxFiles {
			skipped++
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		fi, err := os.Stat(abs)
		if err != nil || fi.Size() > ctxMaxFileSize {
			skipped++
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil || isBinaryData(b) {
			skipped++
			continue
		}
		n++
		parts = append(parts, flow.FileParts(rel, string(b))...)
	}
	note := fmt.Sprintf("%d file%s", n, map[bool]string{true: "", false: "s"}[n == 1])
	if skipped > 0 {
		note += fmt.Sprintf(" (%d left out: lock, minified, binary, over 512 KB, or past %d files)", skipped, ctxMaxFiles)
	}
	if n == 0 {
		note = "no files match " + strings.Join(pats, ", ")
	}
	return parts, note
}

// ctxHistory finds past runs about the query (not this one).
func (h *Hub) ctxHistory(root, self, q string) ([]string, string) {
	hits, err := h.Store.Search(root, q, 6)
	if err != nil {
		return nil, "history search is not available"
	}
	var parts []string
	for _, x := range hits {
		if x.Run.ID == self || len(parts) == 4 {
			continue
		}
		snip := strings.NewReplacer(store.HitStart, "", store.HitEnd, "").Replace(x.Snippet)
		text := fmt.Sprintf("Run %q (%s, %s, %s): %s", firstNonEmpty(x.Run.Title, x.Run.Workflow), x.Run.Kind, x.Run.State, x.Run.Started.Local().Format("2006-01-02"), snip)
		if more, err := h.Store.RunTexts(x.Run.ID, 3000); err == nil && len(more) > 0 {
			text += "\n" + strings.Join(more, "\n")
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return nil, "no past runs match " + q
	}
	return parts, fmt.Sprintf("%d past runs", len(parts))
}

func isBinaryData(b []byte) bool {
	n := min(len(b), 8000)
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

// contextPacks are the packs of the Context steps before n (not those
// connected straight into it: their pack is already its input, nor those
// its prompt names), for its prompt.
func (h *Hub) contextPacks(f *flow.Flow, n *flow.Node, d flow.Data, prompt string) string {
	direct := map[string]bool{}
	for _, e := range f.Edges {
		if e.To == n.ID {
			direct[e.From] = true
		}
	}
	// the steps before n
	before := map[string]bool{}
	todo := []string{n.ID}
	for len(todo) > 0 {
		cur := todo[0]
		todo = todo[1:]
		for _, e := range f.Edges {
			if e.To == cur && !before[e.From] {
				before[e.From] = true
				todo = append(todo, e.From)
			}
		}
	}
	var b strings.Builder
	for _, c := range f.Nodes {
		if c.Type != "context" || !before[c.ID] || direct[c.ID] || strings.Contains(prompt, "steps."+c.ID) {
			continue
		}
		if pack := strings.TrimSpace(d.Steps[c.ID]); pack != "" {
			b.WriteString("\n\n" + pack)
		}
	}
	return b.String()
}
