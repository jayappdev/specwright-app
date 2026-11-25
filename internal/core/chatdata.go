package core

// The chat's data catalog and search (docs/design/2026-11-24-chatflows.md,
// "Data catalog and search"). A session's data is indexed in its own
// SQLite file (chat.Index): tables to query, documents to search. Small
// data still goes to the AI whole; large data goes as a catalog (a
// table's columns, row count and first rows; a document's start and
// passage count), with two built-in tools: data.query (SQL over the
// tables) and data.search (full-text search of the documents). Before
// each question, the passages that best match it are found and sent with
// it, so most document questions need no extra turn.

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"codegen/internal/builtin"
	"codegen/internal/chat"
)

const (
	chatSmallData  = 20000 // data up to this size goes to the AI whole
	chatDataBudget = 80000 // and whole data up to this much in all
	maxDataRuns    = 8     // data.query / data.search runs per question
	chatQueryRows  = 200   // rows a query gives the AI
	chatAutoHits   = 4     // passages sent with each question
	chatSearchHits = 8     // passages a data.search gives
)

// chatData is a session's data, with its index (when it opened).
type chatData struct {
	h   *Hub
	st  *chat.Store
	s   chat.Session
	x   *chat.Index
	cat map[string]chat.Entry
	err error // why there is no index (the chat works without one)
}

// openChatData opens a session's index and brings it up to date.
func (h *Hub) openChatData(st *chat.Store, s chat.Session) *chatData {
	d := &chatData{h: h, st: st, s: s, cat: map[string]chat.Entry{}}
	x, err := st.OpenIndex(s.Flow, s.ID)
	if err != nil {
		d.err = err
		return d
	}
	d.x = x
	d.sync()
	return d
}

func (d *chatData) close() {
	if d.x != nil {
		d.x.Close()
	}
}

// sync indexes new data (a tool's result, an attachment) and reloads the
// catalog.
func (d *chatData) sync() {
	if d.x == nil {
		return
	}
	if s, err := d.st.Get(d.s.Flow, d.s.ID); err == nil {
		d.s = s
	}
	if err := d.x.Sync(d.s.Data, d.load); err != nil {
		d.err = err // partly indexed: what is in, is usable
	}
	cat, err := d.x.Catalog()
	if err != nil {
		d.err = err
		return
	}
	d.cat = map[string]chat.Entry{}
	for _, e := range cat {
		d.cat[e.Name] = e
	}
}

// load reads a data item for the index: a page as its text.
func (d *chatData) load(item chat.Data) (string, error) {
	c, err := d.st.GetData(d.s.Flow, d.s.ID, item.Name)
	if err == nil && contentExt(c) == "html" {
		title, text := builtin.HTMLToText(c)
		c = strings.TrimSpace("# " + title + "\n\n" + text)
	}
	return c, err
}

// item is one data item for the AI: whole when it is small (and the
// budget allows), else its catalog entry.
func (d *chatData) item(it chat.Data, budget *int) string {
	title := firstNonEmpty(it.Title, it.Name)
	e, indexed := d.cat[it.Name]
	if it.Bytes <= chatSmallData && *budget >= it.Bytes {
		c, err := d.st.GetData(d.s.Flow, d.s.ID, it.Name)
		if err != nil {
			return ""
		}
		*budget -= len(c)
		head := fmt.Sprintf("\n### %s (%s)", title, it.Name)
		if indexed && e.Kind == "table" {
			head += fmt.Sprintf(" — also table %s (%d rows) for data.query", chat.QuoteIdent(e.Table), e.Rows)
		}
		return head + "\n```\n" + clipKeep(c, chatSmallData) + "\n```\n"
	}
	if !indexed {
		return fmt.Sprintf("\n### %s\n(left out: %d bytes, too large to send)\n", title, it.Bytes)
	}
	var b strings.Builder
	switch e.Kind {
	case "table":
		fmt.Fprintf(&b, "\n### %s (%s): table %s, %d rows — too large to send; query it with data.query\ncolumns: ", title, it.Name, chat.QuoteIdent(e.Table), e.Rows)
		for i, c := range e.Columns {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(chat.QuoteIdent(c.Name) + " " + c.Type)
		}
		if cols, rows, _, err := d.x.Query(context.Background(), "SELECT * FROM "+chat.QuoteIdent(e.Table)+" LIMIT 5", 5); err == nil {
			b.WriteString("\nfirst rows:\n```csv\n" + clipKeep(csvText(cols, rows), 3000) + "\n```\n")
		}
	default:
		fmt.Fprintf(&b, "\n### %s (%s): a document of %d bytes in %d passages — too large to send; passages that match a question come with it, and data.search finds more\n", title, it.Name, e.Bytes, e.Passages)
		if c, err := d.load(it); err == nil {
			b.WriteString("it starts:\n```\n" + clipKeep(c, 1500) + "\n```\n")
		}
	}
	return b.String()
}

// text is the session's data for the AI (setupOnly: only what the setup
// steps gave, for a refresh).
func (d *chatData) text(setupOnly bool) string {
	var b strings.Builder
	budget := chatDataBudget
	for _, it := range d.s.Data {
		if (setupOnly && !it.Setup) || !chat.Indexed(it) || it.Step == "attachment" {
			continue
		}
		b.WriteString(d.item(it, &budget))
	}
	return b.String()
}

// attachments: what the person attached to a message, for the AI.
func (d *chatData) attachments(names []string) string {
	if len(names) == 0 {
		return ""
	}
	byName := map[string]chat.Data{}
	for _, it := range d.s.Data {
		byName[it.Name] = it
	}
	var b strings.Builder
	b.WriteString("\n\nATTACHED BY THE PERSON:")
	budget := chatDataBudget
	for _, n := range names {
		if it, ok := byName[n]; ok {
			b.WriteString(d.item(it, &budget))
		}
	}
	return b.String()
}

func (d *chatData) has(kind string) bool {
	for _, e := range d.cat {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// largeText reports whether a document was too large to send whole.
func (d *chatData) largeText() bool {
	for _, it := range d.s.Data {
		if e, ok := d.cat[it.Name]; ok && e.Kind == "text" && it.Bytes > chatSmallData {
			return true
		}
	}
	return false
}

// toolsText lists the built-in tools (empty when there is nothing to
// query or search).
func (d *chatData) toolsText() string {
	var b strings.Builder
	if d.has("table") {
		b.WriteString("\n- tool \"data.query\": run one read-only SQL query (SQLite: strftime() for dates, || to join text, LIKE ignores case) over the session's tables (named above), at once and locally. args: {\"sql\": \"SELECT …\"}. You get up to " + fmt.Sprint(chatQueryRows) + " rows: aggregate (GROUP BY, sum, count) rather than reading every row. Quote names with spaces in double quotes.")
	}
	if d.has("text") {
		b.WriteString("\n- tool \"data.search\": full-text search of the session's documents, at once and locally. args: {\"query\": \"a few key words\"}. You get the " + fmt.Sprint(chatSearchHits) + " best-matching passages.")
	}
	return b.String()
}

// passages: the passages that best match a question, sent with it (when
// a document was too large to send whole).
func (d *chatData) passages(question string) string {
	if d.x == nil || !d.largeText() {
		return ""
	}
	ps, err := d.x.Search(clipKeep(question, 600), chatAutoHits)
	if err != nil || len(ps) == 0 {
		return ""
	}
	return "\n\nPASSAGES OF THE SESSION'S DOCUMENTS THAT MATCH THE QUESTION (found by search; use data.search for others):" + passagesText(ps)
}

func passagesText(ps []chat.Passage) string {
	var b strings.Builder
	for _, p := range ps {
		where := p.Title
		if p.Heading != "" {
			where += " › " + p.Heading
		}
		fmt.Fprintf(&b, "\n### %s\n%s\n", where, p.Text)
	}
	return b.String()
}

// isDataTool reports whether a call is one of the built-in tools.
func isDataTool(name string) bool { return name == "data.query" || name == "data.search" }

// runDataTool runs data.query or data.search and saves its result as the
// message's data (not indexed again: it comes from the index).
func (d *chatData) runDataTool(ctx context.Context, c chatCall, n int) (chat.StepRun, string) {
	run := chat.StepRun{Tool: c.Tool, Args: c.Args}
	pub := func(status string) { d.h.Bus.Publish(chatStepEvent(d.s, run, status)) }
	t0 := time.Now()
	var out, ext string
	var err error
	switch c.Tool {
	case "data.query":
		run.Title = "Query the session data"
		q := firstNonEmpty(c.args["sql"], c.args["query"])
		pub("running")
		if d.x == nil {
			err = fmt.Errorf("the session's data cannot be queried: %v", d.err)
			break
		}
		var cols []string
		var rows [][]string
		var more bool
		if cols, rows, more, err = d.x.Query(ctx, q, chatQueryRows); err == nil {
			out, ext = csvText(cols, rows), "csv"
			if more {
				out += fmt.Sprintf("(only the first %d rows: aggregate or filter for the rest)\n", chatQueryRows)
			} else if len(rows) == 0 {
				out += "(no rows)\n"
			}
		}
	default:
		run.Title = "Search the documents"
		q := firstNonEmpty(c.args["query"], c.args["q"], c.Message)
		pub("running")
		if d.x == nil {
			err = fmt.Errorf("the session's documents cannot be searched: %v", d.err)
			break
		}
		var ps []chat.Passage
		if ps, err = d.x.Search(q, chatSearchHits); err == nil {
			out, ext = strings.TrimSpace(passagesText(ps)), "md"
			if len(ps) == 0 {
				out = "Nothing matches " + fmt.Sprintf("%q", q) + ": try other words."
			}
		}
	}
	run.DurationMS = time.Since(t0).Milliseconds()
	if err != nil {
		run.Error = err.Error()
		pub("failed")
		return run, run.Error
	}
	name := fmt.Sprintf("m%03d-%s.%s", n, c.Tool, ext)
	if _, err := d.st.PutData(d.s.Flow, d.s.ID, chat.Data{Name: name, Step: c.Tool, Title: run.Title}, out); err == nil {
		run.Data = name
	}
	pub("done")
	return run, out
}

// tableNote: after a branch ran, where its whole result is when it was
// too long for the AI to get all of it.
func (d *chatData) tableNote(run chat.StepRun, out string, shown int) string {
	if run.Data == "" || len(out) <= shown {
		return ""
	}
	d.sync()
	switch e, ok := d.cat[run.Data]; {
	case ok && e.Kind == "table":
		return fmt.Sprintf("\n(that was the start: the whole result is table %s, %d rows; query it with data.query)\n", chat.QuoteIdent(e.Table), e.Rows)
	case ok:
		return "\n(that was the start: search the rest with data.search)\n"
	}
	return ""
}

func csvText(cols []string, rows [][]string) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(cols)
	_ = w.WriteAll(rows)
	return b.String()
}

// clipKeep shortens s to n bytes, keeping its lines (clipText joins them).
func clipKeep(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], "\n")
	if cut < n/2 {
		cut = n
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
	}
	return s[:cut] + "\n…"
}
