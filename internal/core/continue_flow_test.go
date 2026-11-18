package core

import (
	"strings"
	"testing"
	"time"
)

// Continuing a flow run in the Playground: the agent is told where the flow
// is and how to change it (save_flow, by its name in Copilot and Claude).
func TestContinuingAFlowRunExplainsSaveFlow(t *testing.T) {
	h, _ := flowHub(t)
	st, err := h.RunFlow("notes", FlowRunOptions{Review: "none", Inputs: map[string]string{"version": "1.0"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	var ctx string
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
		if ctx, err = h.continuationContext([]string{st.ID}); err == nil && strings.Contains(ctx, "Changing a flow") {
			break
		}
	}
	for _, want := range []string{".codegen/flows/notes.json", "base_flow_id", "agentbridge-save_flow", "mcp__agentbridge__save_flow", "tool search", "Do NOT edit"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("missing %q in:\n%s", want, ctx)
		}
	}
}
