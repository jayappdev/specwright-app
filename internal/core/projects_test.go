package core

import (
	"os"
	"strings"
	"testing"
	"time"

	"codegen/internal/workflow"
)

func TestSeveralSpecProjects(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	seedSpecKit(t, h)
	if _, err := h.InitSpecKit("orders-service", "claude"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.wf.Model = "opus"
	h.wf.Steps[0].Options.Tools = true
	h.mu.Unlock()

	// finish Constitution in orders-service
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	setInput(t, h, "constitution", "tests everywhere")
	id, err := h.RunStep("constitution")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, id, "gate.wait")
	_ = h.Decide(id, "approve", "")
	waitFor(t, ch, id, "run.end")
	waitDone := func() {
		for i := 0; i < 100; i++ {
			if w, _ := h.Workflow(); w.Steps[0].Status == "done" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("constitution not done")
	}
	waitDone()

	// a second project starts fresh, with the same choices
	if _, err := h.InitSpecKit("billing-service", "claude"); err != nil {
		t.Fatal(err)
	}
	w, _ := h.Workflow()
	if w.ID != "speckit@billing-service" || w.Steps[0].Status != "pending" || w.Steps[0].Input != "" || w.Model != "opus" || !w.Steps[0].Options.Tools {
		t.Fatalf("%s %s %q %s %+v", w.ID, w.Steps[0].Status, w.Steps[0].Input, w.Model, w.Steps[0].Options)
	}
	ps, err := h.SpecProjects()
	if err != nil || len(ps) != 2 || ps[0].Name != "billing-service" || !ps[0].Current || ps[1].Name != "orders-service" || ps[1].Done != 1 || ps[1].Next != "Specify" {
		t.Fatalf("%+v %v", ps, err)
	}
	if p, _ := h.Compose("constitution", ""); !strings.Contains(p, "`billing-service/`") {
		t.Fatal("steps must run in the selected project")
	}

	// back to orders-service: its progress is still there
	st, err := h.SwitchSpecProject("orders-service")
	if err != nil {
		t.Fatal(err)
	}
	if st.Workflow.ID != "speckit@orders-service" || st.Workflow.Steps[0].Status != "done" || st.SpecKitSet.Project != "orders-service" || len(st.Projects) != 2 {
		t.Fatalf("%+v", st.Workflow.Steps[0])
	}
	if _, err := h.SwitchSpecProject("nope"); err == nil {
		t.Fatal("a folder without Spec Kit cannot be selected")
	}
	// the choice is remembered when the workspace is opened again
	if st, err := h.OpenWorkspace(ws); err != nil || st.Workflow.ID != "speckit@orders-service" || st.Workflow.Steps[0].Status != "done" {
		t.Fatal(err, st.Workflow.ID)
	}
}

// A workspace from before per-project workflows: its speckit.json becomes
// the selected project's.
func TestLegacyWorkflowMovesToProject(t *testing.T) {
	root := t.TempDir()
	w := workflow.SpecKit()
	w.Steps[0].Status = "done"
	if err := workflow.Save(root, w); err != nil {
		t.Fatal(err)
	}
	got, err := workflow.LoadProject(root, "orders-service", nil, true)
	if err != nil || got.ID != "speckit@orders-service" || got.Steps[0].Status != "done" {
		t.Fatal(err, got.ID)
	}
	fresh, _ := workflow.LoadProject(root, "billing", got, false)
	if fresh.Steps[0].Status != "pending" || fresh.ID != "speckit@billing" {
		t.Fatal(fresh.ID)
	}
	_ = os.Remove(workflow.Path(root, "speckit"))
}
