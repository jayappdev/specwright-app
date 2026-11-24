package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"codegen/internal/agent"
)

// chatRecorder is the mock agent, remembering what each call was asked
type chatRecorder struct {
	*agent.Mock
	mu   sync.Mutex
	reqs []agent.Request
}

func (r *chatRecorder) Run(ctx context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.Mock.Run(ctx, req, emit)
}

func (r *chatRecorder) last() agent.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[len(r.reqs)-1]
}

func waitPrompt(t *testing.T, h *Hub, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := h.Store.GetRun(id); err == nil && r.Ended.After(r.Started) && !r.Ended.IsZero() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the playground run did not finish")
}

// a context workflow's AI step keeps its conversation; "Keep chatting"
// continues it with only the question, and each follow-up continues the last
func TestKeepChattingResumesTheConversation(t *testing.T) {
	h, _ := flowHub(t)
	rec := &chatRecorder{Mock: agent.NewMock()}
	rec.Mock.Delay = time.Millisecond
	h.Agents.Add(rec)
	st, err := h.RunFlow("notes", FlowRunOptions{Review: "none", Inputs: map[string]string{"version": "2.0"}})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	var write FlowStepState
	for _, s := range st.Steps {
		if s.ID == "write" {
			write = s
		}
	}
	if write.Session == "" || write.SessionAgent != "mock" || rec.last().SessionID != write.Session || rec.last().Resume {
		t.Fatalf("the AI step's conversation: %+v / %+v", write, rec.last())
	}
	// the first follow-up continues that conversation, with only the question
	id1, err := h.RunPrompt("Which tickets were the riskiest?", PromptOptions{Parents: []string{st.ID}})
	if err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, h, id1)
	q := rec.last()
	if !q.Resume || q.SessionID != write.Session || strings.TrimSpace(q.Prompt) != "Which tickets were the riskiest?" {
		t.Fatalf("follow-up: resume=%v session=%s prompt=%q", q.Resume, q.SessionID, q.Prompt)
	}
	if r, _ := h.Store.GetRun(id1); r.AgentSession != write.Session {
		t.Fatalf("the follow-up's conversation: %q", r.AgentSession)
	}
	// and the next one continues from that answer, in the same conversation
	id2, err := h.RunPrompt("And the safest?", PromptOptions{Parents: []string{id1}})
	if err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, h, id2)
	if q := rec.last(); !q.Resume || q.SessionID != write.Session || strings.Contains(q.Prompt, "riskiest") {
		t.Fatalf("second follow-up: %+v", q)
	}
	// another agent cannot continue it: the earlier answers go as text
	h.mu.Lock()
	h.agentName = "other"
	h.mu.Unlock()
	if s := h.resumableSession(st.ID, ""); s != "" {
		t.Fatal("a conversation of another agent is not resumed")
	}
}

func TestClaudeSessionArgs(t *testing.T) {
	c := agent.NewClaude()
	a := strings.Join(c.Args(agent.Request{SessionID: "abc"}), " ")
	if !strings.Contains(a, "--session-id abc") || strings.Contains(a, "--resume") {
		t.Fatal(a)
	}
	a = strings.Join(c.Args(agent.Request{SessionID: "abc", Resume: true}), " ")
	if !strings.Contains(a, "--resume abc") || strings.Contains(a, "--session-id") {
		t.Fatal(a)
	}
}
