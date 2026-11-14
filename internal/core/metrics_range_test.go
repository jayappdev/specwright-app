package core

import (
	"testing"
	"time"

	"codegen/internal/store"
)

func TestMetricsInTimeRange(t *testing.T) {
	h, _ := newHub(t)
	now := time.Now().UTC().Truncate(time.Second)
	for i, ago := range []time.Duration{2 * time.Hour, 30 * time.Hour, 10 * 24 * time.Hour} {
		e := store.LedgerEntry{At: now.Add(-ago), Workflow: "flow:f" + string(rune('a'+i)), State: "done", MinutesSaved: 60}
		e.ToolCalls = 1
		if err := h.Store.Ledger(e); err != nil {
			t.Fatal(err)
		}
	}
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	cases := []struct {
		r    MetricsRange
		runs int
	}{
		{MetricsRange{}, 3},
		{MetricsRange{From: at(24 * time.Hour)}, 1},
		{MetricsRange{From: at(7 * 24 * time.Hour)}, 2},
		{MetricsRange{From: at(48 * time.Hour), To: at(24 * time.Hour)}, 1},
		{MetricsRange{To: at(24 * time.Hour)}, 2},
		{MetricsRange{From: at(2 * time.Hour), To: at(time.Hour)}, 1}, // From is inclusive
	}
	for _, c := range cases {
		m, err := h.MetricsIn(c.r)
		if err != nil {
			t.Fatal(err)
		}
		if m.Flows.Executed != c.runs || m.Entries != c.runs || m.Flows.ToolExecutions != c.runs || m.Flows.HoursSaved != float64(c.runs) {
			t.Fatalf("%+v: executed %d entries %d tools %d hours %v, want %d", c.r, m.Flows.Executed, m.Entries, m.Flows.ToolExecutions, m.Flows.HoursSaved, c.runs)
		}
		if m.Range != c.r {
			t.Fatalf("range %+v", m.Range)
		}
	}
	if _, err := h.MetricsIn(MetricsRange{From: at(time.Hour), To: at(2 * time.Hour)}); err == nil {
		t.Fatal("a start after the end should be refused")
	}
	if _, err := h.MetricsIn(MetricsRange{From: "yesterday"}); err == nil {
		t.Fatal("a bad time should be refused")
	}
}
