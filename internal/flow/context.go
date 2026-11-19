package flow

// The Context step: gathers what the AI steps after it should know, refines
// it, and hands it over as one "context pack".
//
//   - Gather: the steps connected into it (each its own source), and the
//     sources it lists: files (patterns, their contents), text, the flow's
//     inputs, past runs (history search), lessons from reviews, web pages.
//   - Refine, without the AI: each source is cut into chunks; repeated
//     chunks are dropped; chunks are ranked by the words of the step's
//     purpose (Goal) and the source's own focus; the pack is fitted to a
//     budget of tokens, shared between sources by priority (high 3, normal
//     2, low 1), unused shares going to sources that need more. Kept chunks
//     stay in their original order.
//   - Condense (optional, Condense): one AI pass turns the pack into a
//     short brief for the purpose, keeping the [S1] source labels.
//   - Hand over: the AI and Improve steps after it get the pack in their
//     prompt under its own heading (core: contextPacks), and the run shows
//     each source: how much there was, how much was kept, and why.
//
// This file is the part without the app: sources, checks, chunking,
// ranking and the budget. Gathering (files, history, web) is in core.

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// CtxSource is one source a Context step lists.
type CtxSource struct {
	Kind  string   `json:"kind"`            // files | text | inputs | history | lessons | url
	Label string   `json:"label,omitempty"` // shown in the pack and the run (default by kind)
	Paths []string `json:"paths,omitempty"` // files: patterns relative to the project (templates)
	Text  string   `json:"text,omitempty"`  // text: a template
	Query string   `json:"query,omitempty"` // history: what to search for (template); any kind: words to focus on
	URL   string   `json:"url,omitempty"`   // url: a template
	// Priority: high | normal | low — its share of the budget (default normal)
	Priority string `json:"priority,omitempty"`
}

// CtxKinds, CtxPriorities and the budget's bounds.
var (
	CtxKinds      = []string{"files", "text", "inputs", "history", "lessons", "url"}
	CtxPriorities = []string{"high", "normal", "low"}
)

const (
	DefaultCtxBudget = 8000 // tokens
	MinCtxBudget     = 500
	MaxCtxBudget     = 100000
	ctxChunkChars    = 1200
)

// CtxBudget is a Context step's budget in tokens.
func CtxBudget(n Node) int {
	if n.Budget <= 0 {
		return DefaultCtxBudget
	}
	return min(max(n.Budget, MinCtxBudget), MaxCtxBudget)
}

// CtxLabel is a source's name in the pack.
func CtxLabel(s CtxSource) string {
	if l := strings.TrimSpace(s.Label); l != "" {
		return l
	}
	switch s.Kind {
	case "files":
		return "Files " + strings.Join(s.Paths, ", ")
	case "text":
		return "Notes"
	case "inputs":
		return "The flow's inputs"
	case "history":
		return "Past runs: " + s.Query
	case "lessons":
		return "Lessons from reviews"
	case "url":
		return s.URL
	}
	return s.Kind
}

func (f *Flow) checkContext(n Node, parents []string, add func(node, level, format string, a ...any)) {
	label := firstNonEmptyStr(n.Title, n.ID)
	if len(n.Sources) == 0 && len(parents) == 0 {
		add(n.ID, "error", "%s: add a source, or connect the steps whose results it gathers.", label)
	}
	for i, s := range n.Sources {
		name := fmt.Sprintf("%s › source %d", label, i+1)
		if !contains(CtxKinds, s.Kind) {
			add(n.ID, "error", "%s: %q is not a kind of source.", name, s.Kind)
			continue
		}
		if s.Priority != "" && !contains(CtxPriorities, s.Priority) {
			add(n.ID, "error", "%s: priority is high, normal or low.", name)
		}
		switch {
		case s.Kind == "files" && len(s.Paths) == 0:
			add(n.ID, "error", "%s: say which files (e.g. docs/**/*.md).", name)
		case s.Kind == "text" && strings.TrimSpace(s.Text) == "":
			add(n.ID, "error", "%s: write the text.", name)
		case s.Kind == "history" && strings.TrimSpace(s.Query) == "":
			add(n.ID, "error", "%s: say what to look for in past runs.", name)
		case s.Kind == "url" && strings.TrimSpace(s.URL) == "":
			add(n.ID, "error", "%s: give the web address.", name)
		}
		for _, p := range s.Paths {
			if strings.Contains(p, "..") {
				add(n.ID, "error", "%s: %q leaves the project; use paths inside it.", name, p)
			}
		}
	}
	if n.Budget != 0 && (n.Budget < MinCtxBudget || n.Budget > MaxCtxBudget) {
		add(n.ID, "error", "%s: the budget is %d to %d tokens.", label, MinCtxBudget, MaxCtxBudget)
	}
	if n.Condense && strings.TrimSpace(n.Goal) == "" {
		add(n.ID, "warning", "%s: say what the context is for, so condensing keeps what matters.", label)
	}
}

// Tokens estimates the tokens of a text (about 4 characters each).
func Tokens(s string) int { return (len(s) + 3) / 4 }

// CtxPiece is gathered text, before refining.
type CtxPiece struct {
	ID       string // S1, S2, …
	Label    string
	Kind     string // the source's kind, or "step"
	Priority string
	Focus    string // the source's own words to focus on
	Text     string
	// Parts: the text already cut (files: each part starts with its file's
	// name, so a part from the middle of a file still says where it is from)
	Parts []string
	Note  string // what gathering found ("3 files", "not found: …")
}

// CtxReport says what a Context step kept, for the run view.
type CtxReport struct {
	Budget    int            `json:"budget"`
	Used      int            `json:"used"`
	Condensed bool           `json:"condensed,omitempty"`
	Focus     []string       `json:"focus,omitempty"` // the words it ranked by
	Sources   []CtxSourceUse `json:"sources"`
}

// CtxSourceUse is one source in the report.
type CtxSourceUse struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Tokens     int    `json:"tokens"` // all of it
	Kept       int    `json:"kept"`   // in the pack
	Chunks     int    `json:"chunks"`
	KeptChunks int    `json:"keptChunks"`
	Duplicates int    `json:"duplicates,omitempty"`
	Off        int    `json:"offTopic,omitempty"` // parts left out: nothing in them is about the purpose
	Note       string `json:"note,omitempty"`
}

type ctxChunk struct {
	src, idx int
	text     string
	score    float64
	keep     bool
}

var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "what": true, "from": true, "are": true, "was": true, "will": true, "should": true, "about": true, "into": true, "which": true, "when": true, "where": true, "how": true, "why": true, "who": true, "all": true, "any": true, "not": true, "but": true, "can": true, "has": true, "have": true, "its": true, "our": true, "your": true, "their": true, "them": true, "they": true, "then": true, "than": true, "there": true, "these": true, "those": true, "does": true, "did": true, "is": true, "be": true, "to": true, "of": true, "in": true, "on": true, "a": true, "an": true, "or": true, "it": true, "as": true, "at": true, "by": true, "we": true, "if": true, "so": true, "do": true, "us": true}

// FocusWords are the words a purpose ranks by.
func FocusWords(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
		if len(w) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// chunks cuts a text at blank lines, then at lines, into pieces of about
// ctxChunkChars.
func chunks(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
		if cur.Len() > 0 && cur.Len()+len(para) > ctxChunkChars {
			flush()
		}
		if len(para) <= ctxChunkChars {
			if cur.Len() > 0 {
				cur.WriteString("\n\n")
			}
			cur.WriteString(para)
			continue
		}
		for _, line := range strings.Split(para, "\n") { // a long paragraph (code): by lines
			for len(line) > ctxChunkChars {
				flush()
				cur.WriteString(line[:ctxChunkChars])
				flush()
				line = line[ctxChunkChars:]
			}
			if cur.Len()+len(line) > ctxChunkChars {
				flush()
			}
			if cur.Len() > 0 {
				cur.WriteString("\n")
			}
			cur.WriteString(line)
		}
		flush()
	}
	flush()
	return out
}

func score(text string, words []string) float64 {
	if len(words) == 0 {
		return 0
	}
	low := strings.ToLower(text)
	s := 0.0
	for _, w := range words {
		if c := strings.Count(low, w); c > 0 {
			s += 1 + math.Log(float64(c))
		}
	}
	return s / math.Sqrt(float64(len(low)/400+1)) // a little in favour of short, dense chunks
}

func weight(p string) float64 {
	switch p {
	case "high":
		return 3
	case "low":
		return 1
	}
	return 2
}

// BuildContext refines gathered pieces into a pack within the budget.
func BuildContext(title, purpose string, budget int, pieces []CtxPiece) (string, CtxReport) {
	focus := FocusWords(purpose)
	rep := CtxReport{Budget: budget, Focus: focus, Sources: []CtxSourceUse{}}
	var all [][]*ctxChunk
	seen := map[[32]byte]bool{}
	for si, p := range pieces {
		parts := p.Parts
		if parts == nil {
			parts = chunks(p.Text)
		}
		use := CtxSourceUse{ID: p.ID, Label: p.Label, Kind: p.Kind, Note: p.Note}
		for _, t := range parts {
			use.Tokens += Tokens(t)
		}
		words := append(append([]string{}, focus...), FocusWords(p.Focus)...)
		var cs []*ctxChunk
		for i, t := range parts {
			use.Chunks++
			h := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(t)), " ")))
			if seen[h] {
				use.Duplicates++
				continue
			}
			seen[h] = true
			sc := score(t, words)
			if i == 0 {
				sc += 0.5 // a source's start (its title, its summary) is worth keeping
			}
			cs = append(cs, &ctxChunk{src: si, idx: i, text: t, score: sc})
		}
		// with a purpose, the parts that say nothing about it are left out
		// (a source's start stays); a source with no part about it at all
		// keeps its parts in order, so nothing goes missing without a word
		if len(words) > 0 {
			about := false
			for _, c := range cs {
				about = about || (c.idx > 0 && c.score > 0) || c.score > 0.5
			}
			if about {
				var on []*ctxChunk
				for _, c := range cs {
					if c.idx == 0 || c.score > 0 {
						on = append(on, c)
					} else {
						use.Off++
					}
				}
				cs = on
			}
		}
		all = append(all, cs)
		rep.Sources = append(rep.Sources, use)
	}
	// shares by priority; each source takes its best chunks within its share
	total := 0.0
	for si := range pieces {
		if len(all[si]) > 0 {
			total += weight(pieces[si].Priority)
		}
	}
	left := budget
	rank := func(cs []*ctxChunk) []*ctxChunk {
		r := append([]*ctxChunk{}, cs...)
		sort.SliceStable(r, func(i, j int) bool { return r[i].score > r[j].score })
		return r
	}
	for si := range pieces {
		if total == 0 || len(all[si]) == 0 {
			continue
		}
		share := int(float64(budget) * weight(pieces[si].Priority) / total)
		for _, c := range rank(all[si]) {
			t := Tokens(c.text)
			if t > share {
				continue
			}
			c.keep, share, left = true, share-t, left-t
		}
	}
	// what is left goes to the best chunks not kept yet, whichever source
	var rest []*ctxChunk
	for si := range pieces {
		for _, c := range all[si] {
			if !c.keep {
				rest = append(rest, c)
			}
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		wi, wj := rest[i].score*weight(pieces[rest[i].src].Priority), rest[j].score*weight(pieces[rest[j].src].Priority)
		return wi > wj
	})
	for _, c := range rest {
		if t := Tokens(c.text); t <= left {
			c.keep, left = true, left-t
		}
	}
	// the pack, sources in order, kept chunks in their order
	var b strings.Builder
	fmt.Fprintf(&b, "# Context: %s\n", title)
	if strings.TrimSpace(purpose) != "" {
		fmt.Fprintf(&b, "Purpose: %s\n", strings.TrimSpace(purpose))
	}
	for si, p := range pieces {
		use := &rep.Sources[si]
		var kept []string
		last := -1
		for _, c := range all[si] {
			if !c.keep {
				continue
			}
			if last >= 0 && c.idx != last+1 {
				kept = append(kept, "…")
			}
			kept = append(kept, c.text)
			last = c.idx
			use.Kept += Tokens(c.text)
			use.KeptChunks++
		}
		if len(kept) > 0 && (all[si][0].idx > 0 || !all[si][0].keep) {
			kept = append([]string{"…"}, kept...)
		}
		if use.KeptChunks == 0 {
			if use.Chunks > 0 && use.Note == "" {
				use.Note = "nothing fitted the budget"
			}
			continue
		}
		if use.KeptChunks < use.Chunks-use.Duplicates && use.Note == "" {
			use.Note = fmt.Sprintf("kept the %d most relevant of %d parts", use.KeptChunks, use.Chunks-use.Duplicates)
			if use.Off > 0 {
				use.Note += fmt.Sprintf(" (%d not about the purpose)", use.Off)
			}
		}
		rep.Used += use.Kept
		fmt.Fprintf(&b, "\n## [%s] %s\n%s\n", p.ID, p.Label, strings.Join(kept, "\n\n"))
	}
	return b.String(), rep
}

// FileParts cuts a file into parts that each start with its name.
func FileParts(path, content string) []string {
	var out []string
	for _, c := range chunks(content) {
		out = append(out, "### "+path+"\n"+c)
	}
	return out
}

// ContextPreview is a Context step's pack in the designer: the connected
// steps' sample output and the sources that need nothing from the
// project; files, past runs, lessons and pages are named, and read when
// the flow runs.
func ContextPreview(f *Flow, n Node, parents []string, d Data) string {
	var ps []CtxPiece
	for _, p := range parents {
		title := p
		if pn := f.Node(p); pn != nil {
			title = firstNonEmptyStr(pn.Title, p)
		}
		ps = append(ps, CtxPiece{Label: title, Kind: "step", Text: d.Steps[p]})
	}
	for _, s := range n.Sources {
		pc := CtxPiece{Label: CtxLabel(s), Kind: s.Kind, Priority: s.Priority, Focus: s.Query}
		switch s.Kind {
		case "text":
			pc.Text = Render(s.Text, d)
		case "inputs":
			pc.Text = Render("{{steps.start}}", d)
		default:
			pc.Text = "(" + CtxLabel(s) + ": read when the flow runs)"
		}
		ps = append(ps, pc)
	}
	for i := range ps {
		ps[i].ID = fmt.Sprintf("S%d", i+1)
	}
	pack, _ := BuildContext(firstNonEmptyStr(n.Title, n.ID), Render(n.Goal, d), CtxBudget(n), ps)
	return pack
}
