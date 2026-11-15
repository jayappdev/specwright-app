package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codegen/internal/flow"
)

// A flow with a command step does not run until that command is approved
// for the project; changing the command needs a new approval; approvals live
// in the app's data folder, not in the project.
func TestFlowCommandsNeedApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	h, root := newHub(t)
	f, err := h.NewFlow("Count lines")
	if err != nil {
		t.Fatal(err)
	}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "count", Type: "transform", Op: "command", Expr: "wc -l", X: 300},
		flow.Node{ID: "out", Type: "output", Format: "txt", X: 500})
	f.Edges = []flow.Edge{{From: "start", To: "count"}, {From: "count", To: "out"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}

	cmds, err := h.FlowCommands(f.ID)
	if err != nil || len(cmds) != 1 || cmds[0].Approved || cmds[0].Command != "wc -l" {
		t.Fatalf("before approval: %+v %v", cmds, err)
	}
	if _, err := h.RunFlow(f.ID, FlowRunOptions{Review: "none"}); !errors.Is(err, ErrCommandsNotApproved) || !strings.Contains(err.Error(), "wc -l") {
		t.Fatalf("run before approval: %v", err)
	}

	cmds, err = h.ApproveFlowCommands(f.ID, []string{"count"})
	if err != nil || !cmds[0].Approved {
		t.Fatalf("approve: %+v %v", cmds, err)
	}
	st, err := h.RunFlow(f.ID, FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatalf("run after approval: %v", err)
	}
	if got := waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status != "running" }); got.Status != "done" {
		t.Fatalf("run ended %s: %s", got.Status, got.Error)
	}

	// a changed command is a different command
	f.Nodes[1].Expr = "wc -c"
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RunFlow(f.ID, FlowRunOptions{Review: "none"}); !errors.Is(err, ErrCommandsNotApproved) {
		t.Fatalf("changed command ran without approval: %v", err)
	}

	// nothing about approvals is written into the project
	if _, err := os.Stat(filepath.Join(h.Store.Dir(), "approved-commands.json")); err != nil {
		t.Fatal("approvals not in the app's data folder:", err)
	}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), "approved-commands") {
			t.Errorf("approval file in the project: %s", p)
		}
		return nil
	})
}
