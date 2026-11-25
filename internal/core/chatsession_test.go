package core

import (
	"context"
	"encoding/base64"
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

// chatRec is the mock agent, remembering each request
type chatRec struct {
	*agent.Mock
	name string
	mu   sync.Mutex
	reqs []agent.Request
}

func (r *chatRec) Name() string { return r.name }
func (r *chatRec) Run(ctx context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.Mock.Run(ctx, req, emit)
}
func (r *chatRec) last() agent.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[len(r.reqs)-1]
}

func waitChat(t *testing.T, h *Hub, f, id string, ok func(chat.Session) bool) chat.Session {
	t.Helper()
	for i := 0; i < 500; i++ {
		s, err := h.ChatSession(f, id)
		if err == nil && ok(s) {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _ := h.ChatSession(f, id)
	t.Fatalf("session did not get there: %+v", s)
	return s
}

func waitAnswer(t *testing.T, h *Hub, f, id string, n int) []chat.Message {
	t.Helper()
	for i := 0; i < 500; i++ {
		ms, _ := h.ChatMessages(f, id)
		if len(ms) >= n && ms[len(ms)-1].Role == "assistant" {
			h.chats.mu.Lock()
			busy := h.chats.answers[chatKey(f, id)] != nil
			h.chats.mu.Unlock()
			if !busy {
				return ms
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no answer")
	return nil
}

func TestChatSessionSetupAndConversation(t *testing.T) {
	h, ws := flowHub(t)
	rec := &chatRec{Mock: agent.NewMock(), name: "mock"}
	rec.Mock.Delay = time.Millisecond
	h.Agents.Add(rec)
	h.SetSecretAsker(func(_ context.Context, r SecretRequest) (map[string]string, error) {
		return map[string]string{"DB_SALES": "s3cret-pw"}, nil
	})
	if _, err := h.SaveExtension(extension.Extension{Name: "env-echo", Desc: "Echo env", Lang: "Bash", Scope: "project", Approved: true,
		Command: `sh -c 'echo region=$REGION pw=$DB_SALES'`}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("env-echo"); err != nil {
		t.Fatal(err)
	}
	f := flow.New("sales-qa", "Sales Q&A")
	f.Inputs = []flow.Input{{Name: "year", Label: "Year", Type: "text", Default: "2026"}}
	f.Vars = []flow.Var{{Name: "REGION", Value: "eu"}}
	f.Secrets = []flow.SecretVar{{Name: "DB_SALES"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "load", Type: "tool", Tool: "env-echo", Title: "Load sales"},
		flow.Node{ID: "chat", Type: "chat", Instructions: "Answer sales questions briefly.", Starters: []string{"Which region?"}},
		flow.Node{ID: "q", Type: "transform", Op: "uppercase", Title: "Query sales"})
	f.Edges = []flow.Edge{{From: "start", To: "load"}, {From: "load", To: "chat"}, {From: "chat", To: "q"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	list, err := h.Chatflows()
	if err != nil || len(list) != 1 || list[0].Setup[0] != "Load sales" || list[0].Tools[0].Title != "Query sales" || list[0].Secrets[0] != "DB_SALES" || len(list[0].Issues) != 0 {
		t.Fatalf("%+v %v", list, err)
	}

	s, err := h.StartChatSession("sales-qa", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	s = waitChat(t, h, "sales-qa", s.ID, func(s chat.Session) bool { return s.Status != "setup" })
	if s.Status != "ready" || len(s.Data) != 1 || s.Inputs["year"] != "2026" {
		t.Fatalf("%+v", s)
	}
	data, _ := h.ChatData("sales-qa", s.ID, s.Data[0].Name)
	if !strings.Contains(data, "region=eu") || strings.Contains(data, "s3cret-pw") {
		t.Fatalf("the variable reaches the step; the secret is redacted: %q", data)
	}

	// the first question: instructions, data and the question, in a new conversation
	if _, err := h.SendChatMessage("sales-qa", s.ID, "Which region is it?", nil); err != nil {
		t.Fatal(err)
	}
	ms := waitAnswer(t, h, "sales-qa", s.ID, 2)
	first := rec.last()
	if first.Resume || !first.ReadOnly || first.SessionID == "" || !strings.Contains(first.Prompt, "Answer sales questions briefly.") ||
		!strings.Contains(first.Prompt, "region=eu") || !strings.Contains(first.Prompt, "Which region is it?") {
		t.Fatalf("first: %+v", first)
	}
	if ms[0].Role != "user" || ms[1].Role != "assistant" || ms[1].Text == "" {
		t.Fatalf("%+v", ms)
	}
	s, _ = h.ChatSession("sales-qa", s.ID)
	if s.AgentSession != first.SessionID || s.Title != "Which region is it?" {
		t.Fatalf("%+v", s)
	}
	// the next: only the question, in the same conversation
	if _, err := h.SendChatMessage("sales-qa", s.ID, "And the year?", nil); err != nil {
		t.Fatal(err)
	}
	waitAnswer(t, h, "sales-qa", s.ID, 4)
	if q := rec.last(); !q.Resume || q.SessionID != first.SessionID || q.Prompt != "And the year?" {
		t.Fatalf("second: %+v", q)
	}
	// another agent: a new conversation with what was said so far
	other := &chatRec{Mock: agent.NewMock(), name: "other"}
	other.Mock.Delay = time.Millisecond
	h.Agents.Add(other)
	h.mu.Lock()
	h.agentName = "other"
	h.mu.Unlock()
	if _, err := h.SendChatMessage("sales-qa", s.ID, "Summarize.", nil); err != nil {
		t.Fatal(err)
	}
	waitAnswer(t, h, "sales-qa", s.ID, 6)
	if q := other.last(); q.Resume || !strings.Contains(q.Prompt, "THE CONVERSATION SO FAR") || !strings.Contains(q.Prompt, "And the year?") {
		t.Fatalf("replay: %+v", q)
	}
	// nothing in the session's files holds the secret
	_ = filepath.Walk(filepath.Join(ws, chat.Dir), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "s3cret-pw") {
				t.Fatalf("%s holds the secret", p)
			}
		}
		return nil
	})
	if _, err := h.RenameChatSession("sales-qa", s.ID, "Regions"); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteChatSession("sales-qa", s.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.ChatSessions("sales-qa"); len(l) != 0 {
		t.Fatal("deleted")
	}
}

func TestSplitFollowups(t *testing.T) {
	a, f := splitFollowups("EU grew most.\n\nFollow-ups: Why EU? | And the US? | `Last year?`")
	if a != "EU grew most." || len(f) != 3 || f[2] != "Last year?" {
		t.Fatalf("%q %q", a, f)
	}
	a, f = splitFollowups("No follow-ups here.")
	if a != "No follow-ups here." || f != nil {
		t.Fatal(a, f)
	}
}

func TestChatAttachmentsAndRefresh(t *testing.T) {
	h, ws := flowHub(t)
	rec := &chatRec{Mock: agent.NewMock(), name: "mock"}
	rec.Mock.Delay = time.Millisecond
	h.Agents.Add(rec)
	f := flow.New("notes-qa", "Notes Q&A")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "load", Type: "transform", Op: "template", Template: "loaded at {{now}}", Title: "Load"},
		flow.Node{ID: "chat", Type: "chat"})
	f.Edges = []flow.Edge{{From: "start", To: "load"}, {From: "load", To: "chat"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	s, err := h.StartChatSession("notes-qa", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitChat(t, h, "notes-qa", s.ID, func(x chat.Session) bool { return x.Status == "ready" })
	// a text attachment is data the AI reads; a binary one is named by its path
	d, err := h.AttachChatFile("notes-qa", s.ID, "my notes.txt", base64.StdEncoding.EncodeToString([]byte("remember the milk")))
	if err != nil || d.Name != "file-my-notes.txt" {
		t.Fatalf("%+v %v", d, err)
	}
	bin, err := h.AttachChatFile("notes-qa", s.ID, "pic.png", base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0, 0xff}))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := h.ChatData("notes-qa", s.ID, bin.Name); !strings.Contains(c, "not text") || !strings.Contains(c, "/files/pic.png") {
		t.Fatal(c)
	}
	if _, err := h.SendChatMessage("notes-qa", s.ID, "What did I attach?", []string{d.Name}); err != nil {
		t.Fatal(err)
	}
	ms := waitAnswer(t, h, "notes-qa", s.ID, 2)
	if !strings.Contains(rec.last().Prompt, "remember the milk") || len(ms[0].Files) != 1 {
		t.Fatalf("%q %+v", rec.last().Prompt, ms[0])
	}
	// refresh: the setup runs again; the next message gives the AI the new data
	if _, err := h.RefreshChatSession("notes-qa", s.ID); err != nil {
		t.Fatal(err)
	}
	waitChat(t, h, "notes-qa", s.ID, func(x chat.Session) bool { return x.Status == "ready" && x.Refreshed })
	if _, err := h.SendChatMessage("notes-qa", s.ID, "Anything new?", nil); err != nil {
		t.Fatal(err)
	}
	waitAnswer(t, h, "notes-qa", s.ID, 4)
	if q := rec.last(); !q.Resume || !strings.Contains(q.Prompt, "THE SESSION DATA WAS REFRESHED") || !strings.Contains(q.Prompt, "loaded at") {
		t.Fatalf("%+v", q)
	}
	if x, _ := h.ChatSession("notes-qa", s.ID); x.Refreshed {
		t.Fatal("told once")
	}
}
