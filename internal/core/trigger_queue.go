package core

// The trigger queue (store/learn.go): a trigger run that is due while its
// flow is still running, or while maxTriggeredRuns triggered runs are
// going, waits for its turn instead of being skipped (unless the trigger
// says to skip). A failed run is tried again when the trigger has retries,
// after a wait. The queue is in the history database, so waiting runs and
// retries carry on after the app starts again. Without the database a
// busy trigger skips, as before.

import (
	"fmt"

	"codegen/internal/bus"
	"codegen/internal/flow"
	"codegen/internal/store"
)

// triggeredLive is how many runs triggers have going (or waiting to start).
func (h *Hub) triggeredLive() int {
	h.mu.RLock()
	var runs []*flowRun
	for _, fr := range h.flowRuns {
		runs = append(runs, fr)
	}
	h.mu.RUnlock()
	n := 0
	for _, fr := range runs {
		if st := fr.snapshot(); st.Options.Trigger != nil && slicesContains(flowLive, st.Status) {
			n++
		}
	}
	return n
}

// queuedCounts is how many runs each trigger has waiting ("<flow>/<trigger>").
func (h *Hub) queuedCounts(root string) map[string]int {
	out := map[string]int{}
	q, _ := h.Store.Queue(root)
	for _, x := range q {
		out[trigKey(x.Flow, x.Trigger)]++
	}
	return out
}

// startTriggered starts a trigger's run now, or puts it in the queue when
// the flow is busy (or skips it, when the trigger says so or the queue
// cannot be used).
func (h *Hub) startTriggered(root string, f *flow.Flow, t flow.Trigger, why string, vars map[string]string, chain []string, attempt int) {
	busy := ""
	switch {
	case h.flowRunning(f.ID):
		busy = "a run of this flow was still going"
	case h.triggeredLive() >= maxTriggeredRuns:
		busy = fmt.Sprintf("%d triggered runs were already going", maxTriggeredRuns)
	}
	if busy == "" {
		_, _ = h.fireTrigger(root, f, t, why, vars, chain, attempt)
		return
	}
	key, now := trigKey(f.ID, t.ID), triggerNow()
	skip := ""
	switch {
	case t.Skips():
		skip = fmt.Sprintf("skipped at %s: %s", now.Format("Jan 2 15:04"), busy)
	case !h.Store.HasDB():
		skip = fmt.Sprintf("skipped at %s: %s (runs cannot wait without the history database)", now.Format("Jan 2 15:04"), busy)
	case h.queuedCounts(root)[key] >= maxQueued:
		skip = fmt.Sprintf("skipped at %s: %s, and %d runs are already waiting", now.Format("Jan 2 15:04"), busy, maxQueued)
	}
	ts := h.triggers(root)
	if skip != "" {
		ts.mu.Lock()
		ts.current(key, t, now).LastError = skip
		_ = ts.save()
		ts.mu.Unlock()
		return
	}
	if _, err := h.Store.Enqueue(store.QueuedRun{Workspace: root, Flow: f.ID, Trigger: t.ID, Why: why, Vars: vars, Chain: chain, Attempt: attempt, Queued: now}); err != nil {
		ts.mu.Lock()
		ts.current(key, t, now).LastError = "could not queue the run: " + err.Error()
		_ = ts.save()
		ts.mu.Unlock()
		return
	}
	h.Bus.Publish(bus.Event{Type: "trigger.queued", Summary: fmt.Sprintf("%s: %s waits (%s)", f.Name, firstNonEmpty(t.Name, t.ID), busy),
		Data: map[string]any{"flow": f.ID, "trigger": t.ID}})
}

// processQueue starts the waiting runs whose turn it is, oldest first.
func (h *Hub) processQueue(root string) {
	if !h.Store.HasDB() || !h.rootIs(root) {
		return
	}
	ts := h.triggers(root)
	if !ts.qmu.TryLock() { // another pass is starting runs
		return
	}
	defer ts.qmu.Unlock()
	q, err := h.Store.Queue(root)
	if err != nil {
		return
	}
	now := triggerNow()
	for _, x := range q {
		if x.NotBefore.After(now) {
			continue // a retry, later
		}
		f, err := flow.Load(root, x.Flow)
		var t *flow.Trigger
		if err == nil {
			for i := range f.Triggers {
				if f.Triggers[i].ID == x.Trigger {
					t = &f.Triggers[i]
				}
			}
		}
		if t == nil || !t.Enabled { // removed or turned off while it waited
			_ = h.Store.Dequeue(x.ID)
			continue
		}
		if h.triggeredLive() >= maxTriggeredRuns {
			return // no room: the rest wait
		}
		if h.flowRunning(f.ID) {
			continue // its turn after the run that is going
		}
		_ = h.Store.Dequeue(x.ID)
		why := fmt.Sprintf("%s; waited since %s", x.Why, x.Queued.Local().Format("15:04"))
		if x.Attempt > 0 {
			why = fmt.Sprintf("retry %d of %d (%s)", x.Attempt, t.Retries, x.Why)
		}
		_, _ = h.fireTrigger(root, f, *t, why, x.Vars, x.Chain, x.Attempt)
	}
}

// retryLater queues another try of a failed triggered run, when its
// trigger has retries left.
func (h *Hub) retryLater(root string, run FlowRunState) {
	tr := run.Options.Trigger
	if tr == nil || run.Status != "failed" || !h.Store.HasDB() {
		return
	}
	f, err := flow.Load(root, run.FlowID)
	if err != nil {
		return
	}
	for _, t := range f.Triggers {
		if t.ID != tr.ID || !t.Enabled || tr.Attempt >= t.Retries {
			continue
		}
		at := triggerNow().Add(t.RetryAfter())
		why := firstNonEmpty(tr.Name, tr.ID) + " failed"
		if _, err := h.Store.Enqueue(store.QueuedRun{Workspace: root, Flow: f.ID, Trigger: t.ID, Why: why, Vars: tr.Vars, Chain: tr.Chain, Attempt: tr.Attempt + 1, NotBefore: at, Queued: triggerNow()}); err == nil {
			ts := h.triggers(root)
			ts.mu.Lock()
			ts.current(trigKey(f.ID, t.ID), t, triggerNow()).LastError = fmt.Sprintf("the run failed; retry %d of %d at %s", tr.Attempt+1, t.Retries, at.Local().Format("15:04"))
			_ = ts.save()
			ts.mu.Unlock()
		}
	}
}
