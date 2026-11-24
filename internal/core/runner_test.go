package core

import (
	"slices"
	"strings"
	"testing"
	"time"

	"codegen/internal/flow"
	"codegen/internal/store"
)

func TestRunnerWindowCommand(t *testing.T) {
	h, ws := newHub(t)
	if err := flow.Save(ws, flow.New("report", "Report")); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.token, h.model = "tok-secret", "gpt-x"
	h.mu.Unlock()
	cmd, err := h.runnerCommand("/app/SpecWright", "report")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/app/SpecWright", "runner", "--workspace", ws, "--flow", "report", "--agent", "mock", "--model", "gpt-x"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("%q", cmd.Args)
	}
	// the token goes in the environment, never on the command line
	if strings.Contains(strings.Join(cmd.Args, " "), "tok-secret") || !slices.Contains(cmd.Env, RunnerTokenEnv+"=tok-secret") {
		t.Fatal("token placement")
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("the runner window outlives this one")
	}
	if _, err := h.runnerCommand("/app/SpecWright", "nope"); err == nil {
		t.Fatal("an unknown flow is refused")
	}
}

// a runner window leaves interrupted runs, schedules and archiving to the
// main window
func TestRunnerModeLeavesBackgroundWork(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// a run from a main window that closed while it ran (no owner recorded)
	_ = st.SaveRun(store.Run{ID: "old", State: "running", Started: time.Now(), Owner: -1})
	h, err := New(Options{StoreDir: dir, Agent: "mock", Runner: "report"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if r, _ := h.Store.GetRun("old"); r.State != "running" {
		t.Fatalf("a runner window marked another window's run: %s", r.State)
	}
	ws := t.TempDir()
	if _, err := h.OpenWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	if h.Info().Runner != "report" || h.RunnerFlow() != "report" {
		t.Fatal("Info says it is a runner window")
	}
}
