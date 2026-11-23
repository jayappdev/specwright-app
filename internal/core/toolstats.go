package core

// Tool reliability (store/learn.go): every tool call is recorded — from a
// flow step, from an agent through the bridge, from the tool test form —
// with whether it worked, how long it took and its error. The Flow
// Designer and Tools show how a tool has done lately ("failed 4 of the
// last 10"); the test form offers arguments that worked before; the AI
// designer is told which tools have been failing, so it can prefer others.

import (
	"fmt"
	"strings"

	"codegen/internal/store"
)

// recordToolCall keeps one call (best effort: history never stops a run).
func (h *Hub) recordToolCall(c store.ToolCall) {
	if c.Workspace == "" {
		c.Workspace = h.Root()
	}
	if c.Workspace == "" {
		return
	}
	_ = h.Store.RecordToolCall(c)
}

// ToolStats is how each tool has done lately in the open project.
func (h *Hub) ToolStats() (map[string]store.ToolStat, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	return h.Store.ToolStats(root, "")
}

// ToolArgExamples are the arguments of a tool's latest successful calls.
func (h *Hub) ToolArgExamples(tool string) ([]map[string]any, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	return h.Store.ToolArgExamples(root, tool, 3)
}

// reliabilityNote says, for the AI designer, that a tool has been failing
// ("" when it has not: most tools need no note).
func reliabilityNote(st store.ToolStat, ok bool) string {
	if !ok || st.Calls < 3 || st.Failed*10 < st.Calls*3 { // under 30% failed: no note
		return ""
	}
	n := fmt.Sprintf(" [RELIABILITY: failed %d of its last %d runs", st.Failed, st.Calls)
	if e := strings.TrimSpace(st.LastError); e != "" {
		n += "; last error: " + clipText(e, 160)
	}
	return n + ". Prefer another tool for the same job if there is one.]"
}
