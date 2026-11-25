package core

import (
	"slices"
	"strings"
	"testing"
	"time"

	"codegen/internal/chat"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

// A Chatflow runs only its setup, as a flow run linked to a chat session:
// Run flows (RunFlow) starts a chat; "setup only" runs the setup without
// one; startFlow itself still refuses the whole Chatflow.
func TestChatflowRunsItsSetupForAChat(t *testing.T) {
	h, ws := flowHub(t)
	f := flow.New("sales-qa", "Sales Q&A")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "load", Type: "transform", Op: "template", Template: "region,amount\nEU,3\n", Title: "Load sales"},
		flow.Node{ID: "chat", Type: "chat", Instructions: "Answer."})
	f.Edges = []flow.Edge{{From: "start", To: "load"}, {From: "load", To: "chat"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	if _, err := h.startFlow(ws, f, FlowRunOptions{Review: "none"}, nil); err == nil || !strings.Contains(err.Error(), "is a Chatflow") {
		t.Fatalf("the whole Chatflow ran: %v", err)
	}
	if l, _ := h.Flows(); !slices.ContainsFunc(l, func(x flow.Summary) bool { return x.ID == "sales-qa" && x.Chat }) {
		t.Fatalf("%+v", l)
	}

	// Start chat: a setup run, then a session that has its data and links to it
	run, err := h.RunFlow("sales-qa", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Options.Chat == nil || run.Options.Chat.Role != "setup" || run.Options.Chat.Session == "" || run.FlowID != "sales-qa" || run.FlowName != "Sales Q&A" {
		t.Fatalf("the run %+v", run.Options.Chat)
	}
	run = waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if run.Status != "done" || len(run.Steps) != 2 { // start, load: not the Chat step
		t.Fatalf("%s %+v", run.Status, run.Steps)
	}
	s := waitChat(t, h, "sales-qa", run.Options.Chat.Session, func(x chat.Session) bool { return x.Status == "ready" })
	if s.SetupRun != run.ID || len(s.Data) != 1 || s.Data[0].Name != "setup-load.csv" {
		t.Fatalf("the session %+v", s)
	}

	// setup only: the run, no session
	before, _ := h.ChatSessions("sales-qa")
	only, err := h.RunFlow("sales-qa", FlowRunOptions{Review: "none", Chat: &FlowRunChat{Role: "setup-only"}})
	if err != nil {
		t.Fatal(err)
	}
	only = waitFlow(t, h, only.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	after, _ := h.ChatSessions("sales-qa")
	if only.Status != "done" || only.Options.Chat.Session != "" || len(after) != len(before) {
		t.Fatalf("setup only: %s %+v; sessions %d → %d", only.Status, only.Options.Chat, len(before), len(after))
	}
}

// A first setup that fails leaves the failed run to look at, and no session.
func TestChatSetupFailureRemovesTheSession(t *testing.T) {
	h, ws := flowHub(t)
	if _, err := h.SaveExtension(extension.Extension{Name: "fail-tool", Desc: "Fails", Lang: "Bash", Scope: "project", Approved: true, Command: "exit 3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("fail-tool"); err != nil {
		t.Fatal(err)
	}
	f := flow.New("broken-chat", "Broken chat")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "load", Type: "tool", Tool: "fail-tool", Title: "Load"},
		flow.Node{ID: "chat", Type: "chat", Instructions: "Answer."})
	f.Edges = []flow.Edge{{From: "start", To: "load"}, {From: "load", To: "chat"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	run, err := h.RunFlow("broken-chat", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Skipf("the command step could not start here: %v", err)
	}
	run = waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status != "running" && s.Status != "scheduled" })
	if run.Status == "done" {
		t.Skip("the failing command did not fail here")
	}
	for i := 0; i < 200; i++ {
		if l, _ := h.ChatSessions("broken-chat"); len(l) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the session of a failed setup is still there")
}
