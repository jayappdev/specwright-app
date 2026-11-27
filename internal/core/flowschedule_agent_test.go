package core

import (
	"strings"
	"testing"
	"time"

	"codegen/internal/flow"
)

func TestScheduleFlowTool(t *testing.T) {
	stubKeychain(t)
	setTriggerClock(t, func() time.Time { return time.Date(2026, 11, 18, 8, 0, 0, 0, time.Local) })
	h, ws := flowHub(t)
	allowAgentActions(h) // the person says yes to the AI's actions (agentactions.go)
	call := func(args map[string]any) (string, error) { return h.RunBuiltin(h.ctx, ScheduleFlowTool, args) }

	found := false
	for _, b := range h.Builtins() {
		found = found || b.Name == ScheduleFlowTool
	}
	if !found {
		t.Fatal("agents get schedule_flow")
	}
	if out, err := call(map[string]any{"flow_id": "notes"}); err != nil || !strings.Contains(out, "no schedules") {
		t.Fatal(out, err)
	}
	out, err := call(map[string]any{"flow_id": "notes", "action": "set", "name": "Every morning",
		"schedule": map[string]any{"mode": "weekly", "time": "09:30", "days": []any{"Monday", "wed"}, "count": 3},
		"inputs":   map[string]any{"version": 2}})
	if err != nil || !strings.Contains(out, "Added the schedule trigger every-morning") || !strings.Contains(out, "next: Wed 2026-11-18 09:30") {
		t.Fatal(out, err)
	}
	f, _ := flow.Load(ws, "notes")
	if len(f.Triggers) != 1 || f.Revision != 0 && f.Parent != "" {
		t.Fatalf("saved in place: %+v", f.Triggers)
	}
	tr := f.Triggers[0]
	if !tr.Enabled || tr.Kind != "schedule" || tr.Schedule.Days[0] != "mon" || tr.Inputs["version"] != "2" {
		t.Fatalf("%+v %+v", tr, tr.Schedule)
	}
	// change one field: the rest stays
	if out, err = call(map[string]any{"flow_id": "notes", "trigger_id": "every-morning", "enabled": false}); err != nil || !strings.Contains(out, "Changed") || !strings.Contains(out, ", off)") {
		t.Fatal(out, err)
	}
	if f, _ = flow.Load(ws, "notes"); f.Triggers[0].Enabled || f.Triggers[0].Schedule.Time != "09:30" {
		t.Fatalf("%+v", f.Triggers[0])
	}
	// what it refuses
	for _, bad := range []map[string]any{
		{"flow_id": "nope"},
		{"flow_id": "notes", "action": "set", "schedule": map[string]any{"mode": "daily", "time": "25:00"}},
		{"flow_id": "notes", "action": "set", "schedule": map[string]any{"mode": "daily", "time": "09:00"}, "inputs": map[string]any{"bogus": "x"}},
		{"flow_id": "notes", "action": "remove", "trigger_id": "missing"},
	} {
		if _, err := call(bad); err == nil {
			t.Fatalf("expected an error for %v", bad)
		}
	}
	if out, err = call(map[string]any{"flow_id": "notes", "action": "remove", "trigger_id": "every-morning"}); err != nil || !strings.Contains(out, "Removed") {
		t.Fatal(out, err)
	}
	if f, _ = flow.Load(ws, "notes"); len(f.Triggers) != 0 {
		t.Fatal("removed")
	}
	// the other kinds: after another flow, when files change
	echoFlow(t, h, "follow")
	if out, err = call(map[string]any{"flow_id": "follow", "after": map[string]any{"flow": "notes", "on": []any{"done", "failed"}}, "inputs": map[string]any{"from": "{{trigger.output}}"}}); err != nil || !strings.Contains(out, "after Release notes completes or fails") {
		t.Fatal(out, err)
	}
	if out, err = call(map[string]any{"flow_id": "follow", "action": "set", "kind": "files", "name": "On docs", "files": map[string]any{"paths": []any{"docs/**/*.md"}, "ops": []any{"modified"}}}); err != nil || !strings.Contains(out, "on-docs (On docs, files, on): when docs/**/*.md is modified") {
		t.Fatal(out, err)
	}
	if _, err = call(map[string]any{"flow_id": "follow", "after": map[string]any{"flow": "missing"}}); err == nil {
		t.Fatal("an unknown flow is refused")
	}
	if f, _ = flow.Load(ws, "follow"); len(f.Triggers) != 2 || f.Triggers[0].Kind != "flow" || f.Triggers[1].Files.Ops[0] != "modified" {
		t.Fatalf("%+v", f.Triggers)
	}
}
