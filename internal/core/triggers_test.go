package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codegen/internal/agent"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

func TestScheduleTrigger(t *testing.T) {
	stubKeychain(t)
	h, ws := flowHub(t)
	clock := time.Date(2026, 11, 18, 9, 0, 0, 0, time.Local)
	old := triggerNow
	triggerNow = func() time.Time { return clock }
	t.Cleanup(func() { triggerNow = old })

	f, _ := flow.Load(ws, "notes")
	f.Inputs = append(f.Inputs, flow.Input{Name: "token", Label: "Token", Type: "secret"})
	f.Triggers = []flow.Trigger{{ID: "hourly", Kind: "schedule", Name: "Hourly notes", Enabled: true,
		Schedule: &flow.Schedule{Mode: "every", Every: 1, Unit: "hours", Count: 2}, Inputs: map[string]string{"version": "9.9"}}}
	f.Rev = ""
	if st, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	} else {
		for _, is := range st.Issues {
			if is.Level == "error" {
				t.Fatal(is.Message)
			}
		}
	}
	// the secret: encrypted on this computer, never in plain text
	if names, err := h.SaveTriggerSecrets("notes", "hourly", map[string]string{"token": "s3cret-value"}); err != nil || len(names) != 1 {
		t.Fatal(names, err)
	}
	b, _ := os.ReadFile(h.triggers(ws).path)
	if strings.Contains(string(b), "s3cret-value") || !strings.Contains(string(b), `"token"`) {
		t.Fatalf("secrets must be stored encrypted: %s", b)
	}

	// the first check only works out the next run
	h.checkTriggers(ws)
	v, _ := h.FlowTriggers("notes")
	if len(v) != 1 || v[0].State.NextRun.Format("15:04") != "10:00" || v[0].State.Runs != 0 || len(v[0].Upcoming) != 2 || v[0].SecretsSaved[0] != "token" {
		t.Fatalf("%+v", v)
	}
	if v[0].State.Secrets != nil {
		t.Fatal("encrypted values do not leave the backend")
	}
	// due: it runs, tagged scheduled, with the trigger's inputs and secret
	clock = clock.Add(time.Hour + 5*time.Second)
	h.checkTriggers(ws)
	v, _ = h.FlowTriggers("notes")
	runID := v[0].State.LastRunID
	if runID == "" || v[0].State.Runs != 1 || v[0].State.NextRun.Format("15:04") != "11:00" {
		t.Fatalf("%+v", v[0].State)
	}
	st := waitFlow(t, h, runID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" || st.Options.Trigger == nil || st.Options.Trigger.Kind != "schedule" || st.Options.Inputs["version"] != "9.9" || st.Options.Inputs["token"] != flow.SecretMask {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Options)
	}
	var rec string
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
		if r, err := h.Store.GetRun(runID); err == nil && r.State == "done" {
			rec = r.Trigger + "/" + r.TriggerName
			break
		}
	}
	if rec != "scheduled/Hourly notes" {
		t.Fatalf("history tag %q", rec)
	}

	// asleep for three hours, no catch-up: the missed time is skipped
	clock = clock.Add(3 * time.Hour)
	h.checkTriggers(ws)
	v, _ = h.FlowTriggers("notes")
	if v[0].State.Missed != 1 || v[0].State.Runs != 1 || !strings.Contains(v[0].State.LastError, "missed") {
		t.Fatalf("%+v", v[0].State)
	}

	// the app quits and starts again: the state carries on; the second run is the last (Count 2)
	h2, err := New(Options{StoreDir: h.Store.Dir(), Agent: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	h2.Agents.Add(agent.NewMock())
	if _, err := h2.OpenWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.SaveExtension(extension.Extension{Name: "echo-arg", Desc: "Echo", Lang: "Bash", Scope: "project", Approved: true,
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "echo {{v}}"}); err != nil {
		t.Fatal(err)
	}
	h2.ApproveExtension("echo-arg")
	clock = v[0].State.NextRun.Add(10 * time.Second)
	h2.checkTriggers(ws)
	v, _ = h2.FlowTriggers("notes")
	if v[0].State.Runs != 2 || !v[0].State.Done || len(v[0].Upcoming) != 0 {
		t.Fatalf("after restart: %+v", v[0].State)
	}
	waitFlow(t, h2, v[0].State.LastRunID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })

	// catch-up: a time missed while away runs once when the app is back
	f, _ = flow.Load(ws, "notes")
	f.Triggers[0].CatchUp, f.Triggers[0].Schedule.Count = true, 0
	f.Rev = ""
	h2.SaveFlow(*f)
	h2.checkTriggers(ws) // new settings: starts again
	v, _ = h2.FlowTriggers("notes")
	clock = v[0].State.NextRun.Add(5 * time.Hour)
	h2.checkTriggers(ws)
	v, _ = h2.FlowTriggers("notes")
	if v[0].State.Runs != 1 || v[0].State.Missed != 0 {
		t.Fatalf("catch-up: %+v", v[0].State)
	}
	waitFlow(t, h2, v[0].State.LastRunID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	_ = filepath.Join
}
