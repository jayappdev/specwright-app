package core

import (
	"strings"
	"testing"

	"codegen/internal/flow"
)

// a Chatflow is chatted with: Run flows (and every other way to run a flow) refuses it
func TestChatflowIsNotRun(t *testing.T) {
	h, ws := newHub(t)
	f := flow.New("sales-qa", "Sales Q&A")
	f.Nodes = append(f.Nodes, flow.Node{ID: "chat", Type: "chat", Instructions: "Answer."})
	f.Edges = []flow.Edge{{From: "start", To: "chat"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RunFlow("sales-qa", FlowRunOptions{Review: "none"}); err == nil || !strings.Contains(err.Error(), "is a Chatflow") {
		t.Fatalf("%v", err)
	}
	l, _ := h.Flows()
	if len(l) != 1 || !l[0].Chat {
		t.Fatalf("%+v", l)
	}
}
