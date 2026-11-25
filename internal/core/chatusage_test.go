package core

import (
	"testing"

	"codegen/internal/agent"
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
