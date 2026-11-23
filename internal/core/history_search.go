package core

// Searching run history (the history database's full-text index): the
// Run History search box, and history_search, the built-in agents use to
// look at what was done before — earlier runs of a flow, how a problem was
// fixed, what a review said.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"codegen/internal/extension"
	"codegen/internal/store"
)

// SearchHistory finds runs of the open project (all projects when none is
// open) whose title, inputs or recorded events contain the words of q.
func (h *Hub) SearchHistory(q string, limit int) ([]store.Hit, error) {
	h.mu.RLock()
	root := h.root
	h.mu.RUnlock()
	return h.Store.Search(root, q, limit)
}

// HistorySearchTool is the built-in agents search run history with.
const HistorySearchTool = "history_search"

func historySearchInfo() extension.BuiltinInfo {
	return extension.BuiltinInfo{
		Name:     HistorySearchTool,
		Title:    "Search run history",
		Group:    "flows",
		ReadOnly: true,
		Description: "Search SpecWright's run history of this project: earlier workflow steps, flow runs and playground prompts, by the words in their titles, inputs, steps, tool output and reviews. " +
			"Use it to see what was done before (how a similar problem was fixed, what a flow produced last time, what a review asked for) before repeating work. " +
			"Give query for a list of matching runs (newest relevant first, with the matching text); give run_id to read one run's recorded text (inputs, steps, outputs). Secrets are never in it.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":  map[string]any{"type": "string", "description": "Words to find (all of them; the last may be the start of a word)"},
				"run_id": map[string]any{"type": "string", "description": "A run id from a search: read that run's recorded text"},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "How many runs (default 10)"},
			},
		},
	}
}

// agentHistorySearch implements history_search.
func (h *Hub) agentHistorySearch(args map[string]any) (string, error) {
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	if id := str("run_id"); id != "" {
		r, err := h.Store.GetRun(id)
		if err != nil {
			return "", fmt.Errorf("no run %s (take run ids from a search)", id)
		}
		texts, err := h.Store.RunTexts(id, 30000)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Run %s: %s (%s, %s, %s)\n", r.ID, firstNonEmpty(r.Title, r.Workflow, r.Step), firstNonEmpty(r.Kind, "workflow"), r.State, r.Started.Local().Format("2006-01-02 15:04"))
		if r.Results != "" {
			fmt.Fprintf(&b, "Results folder: %s\n", r.Results)
		}
		b.WriteString("\n" + strings.Join(texts, "\n"))
		return b.String(), nil
	}
	q := str("query")
	if q == "" {
		return "", errors.New("give query (words to find) or run_id (a run to read)")
	}
	limit := 10
	if n, ok := args["limit"].(float64); ok && n >= 1 {
		limit = min(int(n), 50)
	}
	hits, err := h.SearchHistory(q, limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No runs in this project's history match %q.", q), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d run(s) match %q (call history_search with run_id to read one):", len(hits), q)
	for _, x := range hits {
		r := x.Run
		snip := strings.NewReplacer(store.HitStart, "**", store.HitEnd, "**", "\n", " ").Replace(x.Snippet)
		fmt.Fprintf(&b, "\n- %s · %s · %s · %s · %s (%d match%s, in %s): %s", r.ID, firstNonEmpty(r.Title, r.Workflow, r.Step), firstNonEmpty(r.Kind, "workflow"), r.State,
			localTime(r.Started), x.Matches, map[bool]string{true: "", false: "es"}[x.Matches == 1], x.Field, snip)
	}
	return b.String(), nil
}

func localTime(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.Local().Format("2006-01-02 15:04")
}
