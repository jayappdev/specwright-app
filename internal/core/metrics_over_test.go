package core

import (
	"testing"
	"time"

	"codegen/internal/store"
)

func TestMetricsOverTime(t *testing.T) {
	d := func(y, m, dd, h int) time.Time { return time.Date(y, time.Month(m), dd, h, 0, 0, 0, time.Local) }
	ents := []store.LedgerEntry{
		{At: d(2026, 11, 20, 9), Workflow: "flow:a", State: "done", MinutesSaved: 30, Usage: store.Usage{CostUSD: 0.5, InputTokens: 100, OutputTokens: 20}},
		{At: d(2026, 11, 20, 15), Workflow: "flow:a", State: "failed", Usage: store.Usage{PremiumRequests: 2}},
		{At: d(2026, 11, 22, 11), Workflow: "wf", Step: "plan", State: "done"},
	}
	now := d(2026, 11, 23, 12)
	over, unit := overTime(ents, time.Time{}, time.Time{}, now)
	if unit != "day" || len(over) != 4 { // 20, 21 (empty), 22, 23 (empty)
		t.Fatalf("%s %+v", unit, over)
	}
	p := over[0]
	if p.Start != "2026-11-20" || p.FlowRuns != 2 || p.FlowDone != 1 || p.CostUSD != 0.5 || p.Premium != 2 || p.Tokens != 120 || p.HoursSaved != 0.5 {
		t.Fatalf("day 1 %+v", p)
	}
	if over[1].FlowRuns != 0 || over[2].Steps != 1 || over[2].HoursSaved != HoursSaved["plan"] {
		t.Fatalf("%+v", over)
	}
	// a long window is weekly, Monday first
	over, unit = overTime(ents, d(2026, 8, 1, 0), time.Time{}, now)
	if unit != "week" || over[0].Start != "2026-07-27" {
		t.Fatalf("%s first %s", unit, over[0].Start)
	}
	total := 0
	for _, p := range over {
		total += p.FlowRuns
	}
	if total != 2 {
		t.Fatalf("weekly runs %d", total)
	}
	if over, _ := overTime(nil, time.Time{}, time.Time{}, now); len(over) != 0 {
		t.Fatalf("no entries: %+v", over)
	}
}
