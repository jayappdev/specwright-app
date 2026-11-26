package core

// What a chat answer takes: prompts, model calls, premium requests,
// tokens, cost, tool runs (chat.Usage). The chat's AI is measured from
// its events; its tools' AI from their flow runs.

import (
	"sync"

	"codegen/internal/agent"
	"codegen/internal/chat"
	"codegen/internal/store"
)

// chatMeter totals one agent run from its events. A driver that reports
// a total at the end (run.usage) is trusted over the per-call events.
type chatMeter struct {
	mu                       sync.Mutex
	callIn, callOut, cached  int
	calls, tools             int
	runIn, runOut, runCached int
	premium                  int
	cost                     float64
	model                    string
}

func (m *chatMeter) event(ev agent.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch ev.Type {
	case "model.request":
		m.calls++
		m.callIn += toInt(ev.Data["input_tokens"])
		m.cached += toInt(ev.Data["cached_tokens"])
		if s, _ := ev.Data["model"].(string); s != "" {
			m.model = s
		}
	case "model.response":
		m.callOut += toInt(ev.Data["output_tokens"])
	case "tool.call":
		m.tools++
	case "run.usage":
		m.premium += toInt(ev.Data["premium_requests"])
		if c, ok := ev.Data["cost_usd"].(float64); ok && c > 0 {
			m.cost += c
		}
		m.runIn += toInt(ev.Data["input_tokens"])
		m.runOut += toInt(ev.Data["output_tokens"])
		m.runCached += toInt(ev.Data["cached_tokens"])
		if s, _ := ev.Data["model"].(string); s != "" {
			m.model = s
		}
	}
}

// usage is the run's usage, one prompt.
func (m *chatMeter) usage(res agent.Result) chat.Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := chat.Usage{Prompts: 1, ModelCalls: m.calls, AgentTools: m.tools, PremiumRequests: m.premium, CostUSD: m.cost,
		InputTokens: m.callIn, OutputTokens: m.callOut, CachedTokens: m.cached}
	if m.runIn > 0 || m.runOut > 0 {
		u.InputTokens, u.OutputTokens = m.runIn, m.runOut
		if m.runCached > 0 {
			u.CachedTokens = m.runCached
		}
	}
	if u.PremiumRequests == 0 {
		u.PremiumRequests = res.PremiumRequests
	}
	if u.CostUSD == 0 {
		u.CostUSD = res.CostUSD
	}
	if u.ModelCalls == 0 {
		u.ModelCalls = 1
	}
	return u
}

func (m *chatMeter) modelName(res agent.Result) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return firstNonEmpty(res.Model, m.model)
}

// usageOf is a flow run's AI usage, as the chat counts it (nil when it
// used none).
func usageOf(u store.Usage) *chat.Usage {
	c := chat.Usage{ModelCalls: u.ModelCalls, PremiumRequests: u.PremiumRequests, InputTokens: u.InputTokens,
		OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens, CostUSD: u.CostUSD, AgentTools: u.ToolCalls}
	if c == (chat.Usage{}) {
		return nil
	}
	return &c
}

// soFar is the part of a usage an agent may report as its conversation's
// running total.
func soFar(u chat.Usage) chat.Usage {
	return chat.Usage{PremiumRequests: u.PremiumRequests, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CachedTokens: u.CachedTokens, CostUSD: u.CostUSD}
}

// ownPart turns a run's reported usage into its own: when the agent
// reported the conversation's totals so far, the totals it reported last
// time are taken off (a total that went down started again: kept as is).
// It also gives the totals to remember for the next run.
func ownPart(u chat.Usage, res agent.Result, last chat.Usage) (own, next chat.Usage) {
	next = soFar(u)
	if !res.UsageSoFar {
		return u, next
	}
	minus := func(v, p int) int {
		if v >= p {
			return v - p
		}
		return v
	}
	u.PremiumRequests = minus(u.PremiumRequests, last.PremiumRequests)
	u.InputTokens = minus(u.InputTokens, last.InputTokens)
	u.OutputTokens = minus(u.OutputTokens, last.OutputTokens)
	u.CachedTokens = minus(u.CachedTokens, last.CachedTokens)
	if u.CostUSD >= last.CostUSD {
		u.CostUSD -= last.CostUSD
	}
	return u, next
}
