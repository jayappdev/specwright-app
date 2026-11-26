package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codegen/internal/agent"
	"codegen/internal/chat"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

// chatScript answers each call with the next reply, remembering the requests
type chatScript struct {
	mu      sync.Mutex
	replies []string
	reqs    []agent.Request
}

func (s *chatScript) Name() string { return "mock" }
func (s *chatScript) Status(context.Context) agent.Status {
	return agent.Status{Name: "mock", Available: true}
}
func (s *chatScript) Run(_ context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, req)
	r := "(no more replies)"
	if len(s.replies) > 0 {
		r, s.replies = s.replies[0], s.replies[1:]
	}
	emit(agent.Event{Type: "model.text", Summary: r, Data: map[string]any{"text": r}})
	return agent.Result{Text: r, SessionID: req.SessionID}, nil
}

func TestChatRunsItsTools(t *testing.T) {
	h, ws := flowHub(t)
	ag := &chatScript{replies: []string{
		"Let me look.\n```specwright-run\n{\"tool\": \"Shout\", \"args\": {\"echo.v\": \"from the AI\"}, \"message\": \"which region\", \"why\": \"to check\"}\n```",
		"```specwright-run\n{\"tool\": \"save\", \"message\": \"keep it\"}\n```",
		"The answer: FROM THE AI, and region rows were there.",
	}}
	h.Agents.Add(ag)
	var asked []string
	h.SetPermissionAsker(func(_ context.Context, r extension.PermissionRequest) extension.PermissionAnswer {
		asked = append(asked, r.Tool)
		return extension.PermissionAnswer{Allow: false, Message: "No saving today."}
	})
	f := flow.New("sales-qa", "Sales Q&A")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "load", Type: "transform", Op: "template", Template: "region rows", Title: "Load sales"},
		flow.Node{ID: "chat", Type: "chat", Instructions: "Answer."},
		// read-only branch: a tool whose empty field the AI fills, then a transform using the setup data
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Title: "Shout", Args: map[string]string{"v": ""}},
		flow.Node{ID: "up", Type: "transform", Op: "template", Template: "{{steps.echo}} / {{session.load}} / {{chat.message}}"},
		// a branch that writes: asked first
		flow.Node{ID: "save", Type: "output", Title: "Save it", Format: "md", Template: "{{input}}", Actions: []flow.Action{{Type: "save", Path: "out/x.md"}}})
	f.Edges = []flow.Edge{{From: "start", To: "load"}, {From: "load", To: "chat"}, {From: "chat", To: "echo"}, {From: "echo", To: "up"}, {From: "chat", To: "save"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	// echo-arg's permissions are not read-only by default: make it so for this test
	e, _ := h.exts.Get("echo-arg")
	e.Perms = []string{"read files"}
	if _, err := h.SaveExtension(e); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("echo-arg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.chatToolsText(f, ""), `tool "echo"`) || !strings.Contains(h.chatToolsText(f, ""), "echo.v") || !strings.Contains(h.chatToolsText(f, ""), "the person is asked") {
		t.Fatal(h.chatToolsText(f, ""))
	}
	s, err := h.StartChatSession("sales-qa", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitChat(t, h, "sales-qa", s.ID, func(x chat.Session) bool { return x.Status == "ready" })
	if _, err := h.SendChatMessage("sales-qa", s.ID, "Which region?", nil); err != nil {
		t.Fatal(err)
	}
	ms := waitAnswer(t, h, "sales-qa", s.ID, 2)
	a := ms[len(ms)-1]
	if a.Text != "Let me look.\n\nThe answer: FROM THE AI, and region rows were there." || strings.Contains(a.Text, "specwright-run") {
		t.Fatalf("answer %q", a.Text)
	}
	if len(a.Steps) != 2 || a.Steps[0].Tool != "echo" || a.Steps[0].Error != "" || a.Steps[0].Data == "" || a.Steps[1].Error != "No saving today." {
		t.Fatalf("steps %+v", a.Steps)
	}
	if len(asked) != 1 || asked[0] != "Save it" {
		t.Fatalf("asked %v", asked)
	}
	if tr, err := h.FlowRun(a.Steps[0].Run); err != nil || tr.Options.Chat == nil || tr.Options.Chat.Role != "tool" || tr.Options.Chat.Session != s.ID || tr.FlowName != "Sales Q&A · Shout" {
		t.Fatalf("the tool's run %q: %+v %v", a.Steps[0].Run, tr.Options.Chat, err)
	}
	out, _ := h.ChatData("sales-qa", s.ID, a.Steps[0].Data)
	if out != "from the AI / region rows / which region" {
		t.Fatalf("the branch's result: %q", out)
	}
	// the results went back into the same conversation
	if len(ag.reqs) != 3 || !ag.reqs[1].Resume || ag.reqs[1].SessionID != ag.reqs[0].SessionID || !strings.Contains(ag.reqs[1].Prompt, "from the AI / region rows") ||
		!strings.Contains(ag.reqs[2].Prompt, "No saving today.") {
		t.Fatalf("%+v", ag.reqs)
	}
	// export: the questions, the steps with their results, the answers
	path, err := h.ExportChatSession("sales-qa", s.ID)
	if err != nil || !strings.HasPrefix(path, "out/chats/which-region-") {
		t.Fatalf("%s %v", path, err)
	}
	md, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(path)))
	for _, want := range []string{"# Which region?", "### 🧑 Which region?", "ran **Shout**", "from the AI / region rows / which region", "Save it did not run: No saving today.", "The answer: FROM THE AI"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("missing %q in\n%s", want, md)
		}
	}
	// archive and restore
	if x, err := h.ArchiveChatSession("sales-qa", s.ID, true); err != nil || !x.Archived {
		t.Fatalf("%+v %v", x, err)
	}
	if x, _ := h.ArchiveChatSession("sales-qa", s.ID, false); x.Archived {
		t.Fatal("restored")
	}
}

func TestParseChatCalls(t *testing.T) {
	calls, rest := parseChatCalls("Sure.\n```specwright-run\n{\"tool\":\"a\",\"args\":{\"x.n\":3}}\n```\nbad:\n```specwright-run\nnot json\n```")
	if len(calls) != 1 || calls[0].args["x.n"] != "3" || rest != "Sure.\n\nbad:" {
		t.Fatalf("%+v %q", calls, rest)
	}
	_, rest = parseChatCalls("Half way\n```specwright-run\n{\"tool\":")
	if rest != "Half way" {
		t.Fatalf("%q", rest)
	}
	_ = time.Second
}
