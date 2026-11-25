package core

import (
	"context"
	"sync"
	"testing"

	"codegen/internal/agent"
	"codegen/internal/chat"
	"codegen/internal/flow"
	"codegen/internal/store"
)

func TestChatMeter(t *testing.T) {
	// Claude: per-call tokens, the cost at the end
	m := &chatMeter{}
	for _, ev := range []agent.Event{
		{Type: "model.request", Data: map[string]any{"input_tokens": 1000, "cached_tokens": 800, "model": "claude-x"}},
		{Type: "tool.call"},
		{Type: "model.response", Data: map[string]any{"output_tokens": 50}},
		{Type: "model.request", Data: map[string]any{"input_tokens": 1200, "cached_tokens": 1000}},
		{Type: "model.response", Data: map[string]any{"output_tokens": 70}},
		{Type: "run.usage", Data: map[string]any{"cost_usd": 0.02}},
	} {
		m.event(ev)
	}
	u := m.usage(agent.Result{})
	if u.Prompts != 1 || u.ModelCalls != 2 || u.InputTokens != 2200 || u.OutputTokens != 120 || u.CachedTokens != 1800 || u.CostUSD != 0.02 || u.AgentTools != 1 || m.modelName(agent.Result{}) != "claude-x" {
		t.Errorf("claude %+v", u)
	}
	// Copilot: totals and premium requests at the end (they win over the per-call zeros)
	m = &chatMeter{}
	for _, ev := range []agent.Event{
		{Type: "model.request", Data: map[string]any{"input_tokens": 0}},
		{Type: "model.response", Data: map[string]any{"output_tokens": 40}},
		{Type: "run.usage", Data: map[string]any{"premium_requests": 1, "input_tokens": 3000, "output_tokens": 40, "cached_tokens": 100}},
	} {
		m.event(ev)
	}
	if u := m.usage(agent.Result{}); u.PremiumRequests != 1 || u.InputTokens != 3000 || u.OutputTokens != 40 || u.CachedTokens != 100 {
		t.Errorf("copilot %+v", u)
	}
	// nothing reported: what the result says
	m = &chatMeter{}
	if u := m.usage(agent.Result{PremiumRequests: 2, CostUSD: 0.5}); u.PremiumRequests != 2 || u.CostUSD != 0.5 || u.ModelCalls != 1 {
		t.Errorf("from the result %+v", u)
	}
	if usageOf(store.Usage{}) != nil || usageOf(store.Usage{InputTokens: 5}).InputTokens != 5 {
		t.Error("usageOf")
	}
}

// soFarAgent reports usage the way a resumed Copilot session does: the
// conversation's totals so far (1 premium request and 20k tokens a prompt).
type soFarAgent struct {
	mu      sync.Mutex
	prompts map[string]int // by conversation
}

func (a *soFarAgent) Name() string { return "mock" } // (the test hub's agent)
func (a *soFarAgent) Status(context.Context) agent.Status {
	return agent.Status{Name: "mock", Available: true}
}
func (a *soFarAgent) Run(_ context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	a.mu.Lock()
	if a.prompts == nil {
		a.prompts = map[string]int{}
	}
	a.prompts[req.SessionID]++
	n := a.prompts[req.SessionID]
	a.mu.Unlock()
	emit(agent.Event{Type: "model.request", Data: map[string]any{"input_tokens": 0}})
	emit(agent.Event{Type: "model.text", Data: map[string]any{"text": "Answer."}})
	emit(agent.Event{Type: "run.usage", Data: map[string]any{"premium_requests": n, "input_tokens": 20000 * n, "output_tokens": 100 * n, "cached_tokens": 10000 * n}})
	return agent.Result{Text: "Answer.", SessionID: req.SessionID, PremiumRequests: n, UsageSoFar: req.Resume}, nil
}

func TestChatCountsEachAnswersOwnUsage(t *testing.T) {
	// the arithmetic: a resumed run's totals less the last ones; a total that went down started again
	last := chat.Usage{PremiumRequests: 13, InputTokens: 294000}
	own, next := ownPart(chat.Usage{Prompts: 1, PremiumRequests: 14, InputTokens: 318000}, agent.Result{UsageSoFar: true}, last)
	if own.PremiumRequests != 1 || own.InputTokens != 24000 || own.Prompts != 1 || next.PremiumRequests != 14 {
		t.Fatalf("%+v %+v", own, next)
	}
	if own, _ := ownPart(chat.Usage{PremiumRequests: 2}, agent.Result{UsageSoFar: true}, last); own.PremiumRequests != 2 {
		t.Fatalf("a restarted total: %+v", own)
	}
	if own, _ := ownPart(chat.Usage{PremiumRequests: 3}, agent.Result{}, last); own.PremiumRequests != 3 {
		t.Fatalf("a run's own usage: %+v", own)
	}

	// in a chat: three questions, each one prompt (1 premium request, 20k tokens)
	h, ws := flowHub(t)
	ag := &soFarAgent{}
	h.Agents.Add(ag)
	f := flow.New("shop-chat", "Shop chat")
	f.Nodes = append(f.Nodes, flow.Node{ID: "chat", Type: "chat", Instructions: "Answer."})
	f.Edges = []flow.Edge{{From: "start", To: "chat"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	s, err := h.StartChatSession("shop-chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitChat(t, h, "shop-chat", s.ID, func(x chat.Session) bool { return x.Status == "ready" })
	for i, q := range []string{"One?", "Two?", "Three?"} {
		if _, err := h.SendChatMessage("shop-chat", s.ID, q, nil); err != nil {
			t.Fatal(err)
		}
		ms := waitAnswer(t, h, "shop-chat", s.ID, 2*(i+1))
		u := ms[len(ms)-1].Usage
		if u == nil || u.PremiumRequests != 1 || u.InputTokens != 20000 || u.OutputTokens != 100 || u.CachedTokens != 10000 {
			t.Fatalf("answer %d: %+v", i+1, u)
		}
	}
	x, _ := h.ChatSession("shop-chat", s.ID)
	if x.Usage.PremiumRequests != 3 || x.Usage.InputTokens != 60000 || x.Tokens != 60300 || x.AgentSoFar.PremiumRequests != 3 {
		t.Fatalf("the session: %+v (tokens %d, so far %+v)", x.Usage, x.Tokens, x.AgentSoFar)
	}
}
