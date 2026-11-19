package core

import (
	"testing"
	"time"

	"codegen/internal/agent"
	"codegen/internal/flow"
)

// a file changed while the project was closed starts the triggers that ask
// for it when the project opens again; the others do not
func TestFilesWhileClosed(t *testing.T) {
	stubKeychain(t)
	old := triggerQuiet
	triggerQuiet = func(flow.FileWatch) time.Duration { return 100 * time.Millisecond }
	t.Cleanup(func() { triggerQuiet = old })
	h, ws := flowHub(t)
	writeFile(t, ws, "docs/old.md", "there before")
	echoFlow(t, h, "docs", flow.Trigger{ID: "w", Kind: "files", Name: "While closed", Enabled: true,
		Files: &flow.FileWatch{Paths: []string{"docs/**"}, WhileClosed: true}, Inputs: map[string]string{"from": "{{trigger.files}}"}})
	echoFlow(t, h, "live", flow.Trigger{ID: "l", Kind: "files", Name: "Live only", Enabled: true,
		Files: &flow.FileWatch{Paths: []string{"docs/**"}}})
	h.onFiles(h.watcher.Poll()) // the scan includes docs/old.md
	h.syncExternalWatches(ws)
	h.saveScans(ws, true)

	// the project closes (another opens): its scans are kept
	other := t.TempDir()
	if _, err := h.OpenWorkspace(other); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if v, ok, _ := h.Store.LoadState(scansNS(ws)); !ok || len(v) != 1 {
		t.Fatalf("scans kept: %v %d", ok, len(v))
	}

	// while it is closed
	writeFile(t, ws, "docs/new.md", "written while closed")

	// the app opens the project again
	h2, err := New(Options{StoreDir: h.Store.Dir(), Agent: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h2.Close)
	m := agent.NewMock()
	m.Delay = 5 * time.Millisecond
	h2.Agents.Add(m)
	if _, err := h2.OpenWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	st := waitTrigger(t, h2, "docs", 1)
	if st.Status != "done" || st.Options.Inputs["from"] != "docs/new.md" {
		t.Fatalf("%s %q", st.Status, st.Options.Inputs["from"])
	}
	time.Sleep(300 * time.Millisecond)
	if v, _ := h2.FlowTriggers("live"); v[0].State.Runs != 0 {
		t.Fatal("a trigger that did not ask for them does not get changes made while closed")
	}
}
