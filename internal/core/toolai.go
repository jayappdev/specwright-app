package core

import (
	"fmt"
	"math"
	"strings"

	"codegen/internal/aishim"
	"codegen/internal/bus"
	"codegen/internal/store"
)

// toolAIUsage sums the AI a tool used itself (its own claude / copilot
// calls, counted by the aishim launchers) and builds the activity-log event
// for it. used is false when the tool made no AI calls.
func toolAIUsage(tool string, recs []aishim.Record) (u store.Usage, ev bus.Event, used bool) {
	if len(recs) == 0 {
		return u, ev, false
	}
	var premium, aiu float64
	var clis, models []string
	add := func(list *[]string, v string) {
		for _, x := range strings.Split(v, ", ") {
			if x != "" && !strings.Contains(strings.Join(*list, "\x00")+"\x00", x+"\x00") {
				*list = append(*list, x)
			}
		}
	}
	for _, r := range recs {
		u.InputTokens += r.InputTokens
		u.OutputTokens += r.OutputTokens
		u.CachedTokens += r.CachedTokens
		u.CostUSD += r.CostUSD
		u.ModelCalls++
		premium += r.PremiumRequests
		aiu += r.AIUnits
		add(&clis, map[string]string{"claude": "Claude", "copilot": "Copilot"}[r.CLI])
		add(&models, r.Model)
	}
	u.PremiumRequests = int(math.Round(premium))
	parts := []string{fmt.Sprintf("%d AI call%s via %s", len(recs), map[bool]string{true: "", false: "s"}[len(recs) == 1], strings.Join(clis, " and "))}
	if premium > 0 {
		parts = append(parts, fmt.Sprintf("%s premium request%s", trimFloat(premium), map[bool]string{true: "", false: "s"}[premium == 1]))
	}
	if u.InputTokens+u.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d in / %d out tokens", u.InputTokens, u.OutputTokens))
	}
	if u.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", u.CostUSD))
	}
	data := map[string]any{"tool": tool, "calls": len(recs), "premium_requests": premium, "input_tokens": u.InputTokens, "output_tokens": u.OutputTokens,
		"cached_tokens": u.CachedTokens, "cost_usd": u.CostUSD, "model": strings.Join(models, ", "), "source": "tool"}
	if aiu > 0 {
		data["ai_units"] = aiu
	}
	return u, bus.Event{Type: "model.usage", Summary: tool + " used AI: " + strings.Join(parts, " · "), Data: data}, true
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}
