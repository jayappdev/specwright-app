package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codegen/internal/flow"
)

// An approved command step cannot be turned into another command by what
// fills it: an input (or an earlier step's output) is passed as data.
func TestCommandStepValuesCannotInject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	h, ws := flowHub(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	f := flow.New("echo-q", "Echo q")
	f.Inputs = []flow.Input{{Name: "q", Label: "Query", Type: "text"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "say", Type: "transform", Op: "command", Expr: `printf '%s' {{inputs.q}}`, Title: "Say"},
		flow.Node{ID: "o", Type: "output", Format: "txt", Template: "{{steps.say}}", Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "say"}, {From: "say", To: "o"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveFlowCommands("echo-q", []string{"say"}); err != nil {
		t.Fatal(err)
	}
	payload := "hi; touch " + marker
	st, err := h.RunFlow("echo-q", FlowRunOptions{Review: "none", Inputs: map[string]string{"q": payload}})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the input ran as a command: the marker file was created")
	}
	got := ""
	for _, s := range st.Steps {
		if s.ID == "say" {
			got = s.Output
		}
	}
	if st.Status != "done" || strings.TrimSpace(got) != payload {
		t.Fatalf("%s: the step printed %q, want the input as text %q", st.Status, got, payload)
	}
}
