package core

import (
	"strings"
	"testing"

	"codegen/internal/aishim"
)

func TestToolAIUsage(t *testing.T) {
	if _, _, used := toolAIUsage("x", nil); used {
		t.Fatal("no calls, no usage")
	}
	u, ev, used := toolAIUsage("beautify-uml-diagram", []aishim.Record{
		{CLI: "copilot", Model: "gpt-5", InputTokens: 300, OutputTokens: 40, PremiumRequests: 1},
		{CLI: "claude", Model: "claude-sonnet-5-5", InputTokens: 150, OutputTokens: 20, CachedTokens: 50, CostUSD: 0.0123},
	})
	if !used || u.InputTokens != 450 || u.OutputTokens != 60 || u.PremiumRequests != 1 || u.ModelCalls != 2 || u.CostUSD != 0.0123 {
		t.Fatalf("%+v", u)
	}
	if ev.Type != "model.usage" || ev.Data["source"] != "tool" || !strings.Contains(ev.Summary, "beautify-uml-diagram used AI: 2 AI calls via Copilot and Claude · 1 premium request · 450 in / 60 out tokens · $0.0123") {
		t.Fatalf("%q %+v", ev.Summary, ev.Data)
	}
}
