package core

import (
	"strings"
	"testing"
	"time"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// D1: a reviewer's Refine feedback becomes a lesson for the step, and
// later prompts of that step carry it
func TestLessonsFromReviews(t *testing.T) {
	h, ws := flowHub(t)
	st, err := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "3.0"}}) // reviews after AI steps
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "refine", Prompt: "use bullet points, not prose"}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" && strings.Contains(s.Review.Output, "Revised") })
	_ = h.DecideFlow(st.ID, FlowDecision{Action: "continue"})
	waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })

	ls, err := h.FlowLessons("notes")
	if err != nil || len(ls) != 1 || ls[0].Step != "write" || ls[0].Source != "refine" || ls[0].RunID != st.ID {
		t.Fatalf("%v %+v", err, ls)
	}
	f, _ := flow.Load(ws, "notes")
	p := h.aiStepPrompt(f, f.Node("write"), flow.Data{}, 0)
	if !strings.Contains(p, "Lessons from earlier reviews of this step") || !strings.Contains(p, "use bullet points, not prose") {
		t.Fatalf("the prompt carries the lesson:\n%s", p)
	}
	if ls, _ = h.FlowLessons("notes"); ls[0].Used != 1 {
		t.Fatalf("used %d", ls[0].Used)
	}
	// off: kept, not given; a manual one; the same text twice is one lesson
	if err := h.UpdateLesson(ls[0].ID, ls[0].Text, true); err != nil {
		t.Fatal(err)
	}
	if p = h.aiStepPrompt(f, f.Node("write"), flow.Data{}, 0); strings.Contains(p, "bullet points") {
		t.Fatal("a lesson turned off is not in the prompt")
	}
	if _, err := h.AddFlowLesson("notes", "write", "Keep it under 200 words"); err != nil {
		t.Fatal(err)
	}
	_, _ = h.AddFlowLesson("notes", "write", "keep it under 200 words")
	if ls, _ = h.FlowLessons("notes"); len(ls) != 2 {
		t.Fatalf("%+v", ls)
	}
	if p = h.aiStepPrompt(f, f.Node("write"), flow.Data{}, 0); !strings.Contains(p, "Keep it under 200 words") {
		t.Fatal(p)
	}
	for _, l := range ls {
		_ = h.DeleteLesson(l.ID)
	}
	if p = h.aiStepPrompt(f, f.Node("write"), flow.Data{}, 0); strings.Contains(p, "Lessons from") {
		t.Fatal("no lessons: no section")
	}
	// saved into the flow: the prompt says it, so no lesson as well
	st, _ = h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "3.1"}})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	_ = h.DecideFlow(st.ID, FlowDecision{Action: "refine", Prompt: "mention the date", Save: true})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" && strings.Contains(s.Review.Output, "Revised") })
	_ = h.DecideFlow(st.ID, FlowDecision{Action: "stop"})
	if ls, _ = h.FlowLessons("notes"); len(ls) != 0 {
		t.Fatalf("%+v", ls)
	}
}

// saveTool saves a project tool that runs a bash script
func saveTool(t *testing.T, h *Hub, name, script string) {
	t.Helper()
	if _, err := h.SaveExtension(extension.Extension{Name: name, Desc: name, Lang: "Bash", Scope: "project", Approved: true,
		Command: "bash .codegen/tools/" + name + ".sh", Script: "#!/usr/bin/env bash\n" + script + "\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension(name); err != nil {
		t.Fatal(err)
	}
}

// a flow of one tool step
func toolFlow(t *testing.T, h *Hub, id, tool string, trig ...flow.Trigger) {
	t.Helper()
	f := flow.New(id, id)
	f.Nodes = append(f.Nodes, flow.Node{ID: "t", Type: "tool", Tool: tool, X: 200},
		flow.Node{ID: "md", Type: "output", Format: "md", Template: "{{steps.t}}", X: 400, Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "t"}, {From: "t", To: "md"}}
	f.Triggers = trig
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
}

// D2: tool calls are recorded; their numbers, arguments that worked, and a
// note for the AI designer when a tool keeps failing
func TestToolStats(t *testing.T) {
	h, _ := flowHub(t)
	for _, v := range []string{"1.0", "1.1"} {
		st, _ := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": v}, Review: "none"})
		waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })
	}
	saveTool(t, h, "broken-tool", "echo nope >&2; exit 3")
	toolFlow(t, h, "broken", "broken-tool")
	for i := 0; i < 3; i++ {
		st, _ := h.RunFlow("broken", FlowRunOptions{Review: "none"})
		waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "failed" })
	}
	stats, err := h.ToolStats()
	if err != nil {
		t.Fatal(err)
	}
	if s := stats["echo-arg"]; s.Calls != 2 || s.Failed != 0 || len(s.Recent) != 2 || s.LastOK.IsZero() {
		t.Fatalf("%+v", s)
	}
	if s := stats["broken-tool"]; s.Calls != 3 || s.Failed != 3 || !strings.Contains(s.LastError, "exit code 3") {
		t.Fatalf("%+v", s)
	}
	ex, _ := h.ToolArgExamples("echo-arg")
	if len(ex) != 2 || ex[0]["v"] != "fixed 1.1" {
		t.Fatalf("newest first, distinct: %+v", ex)
	}
	var note string
	for _, d := range h.designTools() {
		if d.Name == "broken-tool" {
			note = d.Desc
		}
		if d.Name == "echo-arg" && strings.Contains(d.Desc, "RELIABILITY") {
			t.Fatal("a tool that works gets no note")
		}
	}
	if !strings.Contains(note, "RELIABILITY: failed 3 of its last 3 runs") {
		t.Fatalf("%q", note)
	}
	// the test form's runs count too
	if r, err := h.RunToolWith(extension.Extension{Name: "broken-tool", Lang: "Bash", Scope: "project", Approved: true, Command: "bash -c 'echo fixed'"}, nil, nil, nil); err != nil || !r.OK {
		t.Fatal(err, r)
	}
	if stats, _ = h.ToolStats(); stats["broken-tool"].Calls != 4 || !stats["broken-tool"].Recent[0] {
		t.Fatalf("%+v", stats["broken-tool"])
	}
}

// D3: a run due while the flow is busy waits for its turn; a failed run is
// retried after a wait
func TestTriggerQueueAndRetry(t *testing.T) {
	stubKeychain(t)
	h, ws := flowHub(t)
	saveTool(t, h, "slow-tool", "sleep 1; echo slow")
	tr := flow.Trigger{ID: "go", Kind: "flow", Name: "Go", Enabled: true, After: &flow.AfterFlow{Flow: "notes"}}
	toolFlow(t, h, "slow", "slow-tool", tr)
	f, _ := flow.Load(ws, "slow")

	first, _ := h.RunFlow("slow", FlowRunOptions{Review: "none"})
	h.startTriggered(ws, f, f.Triggers[0], "test", map[string]string{"flow": "x"}, nil, 0)
	if v, _ := h.FlowTriggers("slow"); v[0].State.Queued != 1 || v[0].State.Runs != 0 {
		t.Fatalf("it waits: %+v", v[0].State)
	}
	waitFlow(t, h, first.ID, func(s FlowRunState) bool { return s.Status == "done" })
	st := waitTrigger(t, h, "slow", 1) // the first run ended: the waiting one starts
	if st.Options.Trigger == nil || st.Options.Trigger.Vars["flow"] != "x" {
		t.Fatalf("%+v", st.Options.Trigger)
	}
	if q, _ := h.Store.Queue(ws); len(q) != 0 {
		t.Fatalf("%+v", q)
	}

	// "skip this time"
	f.Triggers[0].IfBusy = "skip"
	f.Rev = ""
	h.SaveFlow(*f)
	busy, _ := h.RunFlow("slow", FlowRunOptions{Review: "none"})
	h.startTriggered(ws, f, f.Triggers[0], "test", nil, nil, 0)
	if v, _ := h.FlowTriggers("slow"); v[0].State.Queued != 0 || !strings.Contains(v[0].State.LastError, "skipped") {
		t.Fatalf("%+v", v[0].State)
	}
	waitFlow(t, h, busy.ID, func(s FlowRunState) bool { return s.Status == "done" })

	// retries: a failed run comes back after the wait, once
	clock := time.Now()
	old := triggerNow
	triggerNow = func() time.Time { return clock }
	t.Cleanup(func() { triggerNow = old })
	saveTool(t, h, "flaky-tool", "exit 1")
	toolFlow(t, h, "flaky", "flaky-tool", flow.Trigger{ID: "r", Kind: "flow", Name: "Retry me", Enabled: true, After: &flow.AfterFlow{Flow: "notes"}, Retries: 1, RetryWait: 10})
	run, err := h.RunTriggerNow("flaky", "r")
	if err != nil {
		t.Fatal(err)
	}
	waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status == "failed" })
	var q []QueuedRunView
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
		if raw, _ := h.Store.Queue(ws); len(raw) == 1 {
			q = []QueuedRunView{{raw[0].Attempt, raw[0].NotBefore}}
			break
		}
	}
	if len(q) != 1 || q[0].attempt != 1 || q[0].notBefore.Sub(clock) < 9*time.Minute {
		t.Fatalf("a retry waits: %+v", q)
	}
	h.processQueue(ws)
	if raw, _ := h.Store.Queue(ws); len(raw) != 1 {
		t.Fatal("not before its time")
	}
	clock = clock.Add(11 * time.Minute)
	h.processQueue(ws)
	retry := waitTrigger(t, h, "flaky", 2)
	if retry.Options.Trigger.Attempt != 1 || retry.Status != "failed" {
		t.Fatalf("%+v %s", retry.Options.Trigger, retry.Status)
	}
	time.Sleep(200 * time.Millisecond)
	if raw, _ := h.Store.Queue(ws); len(raw) != 0 {
		t.Fatal("one retry only")
	}
}

type QueuedRunView struct {
	attempt   int
	notBefore time.Time
}

// Run flows lists the waiting runs; one can be cancelled
func TestTriggerQueueView(t *testing.T) {
	stubKeychain(t)
	h, ws := flowHub(t)
	saveTool(t, h, "slow-tool", "sleep 1; echo slow")
	toolFlow(t, h, "slow", "slow-tool", flow.Trigger{ID: "go", Kind: "flow", Name: "Go", Enabled: true, After: &flow.AfterFlow{Flow: "notes"}})
	f, _ := flow.Load(ws, "slow")
	busy, _ := h.RunFlow("slow", FlowRunOptions{Review: "none"})
	h.startTriggered(ws, f, f.Triggers[0], "test", map[string]string{"output": strings.Repeat("x", 5000)}, nil, 0)
	q, err := h.TriggerQueue()
	if err != nil || len(q) != 1 || q[0].FlowName != "slow" || q[0].TriggerName != "Go" || q[0].Vars != nil {
		t.Fatalf("%v %+v", err, q)
	}
	if err := h.CancelQueued(q[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFlow(t, h, busy.ID, func(s FlowRunState) bool { return s.Status == "done" })
	time.Sleep(300 * time.Millisecond)
	if v, _ := h.FlowTriggers("slow"); v[0].State.Runs != 0 {
		t.Fatal("a cancelled run does not start")
	}
}
