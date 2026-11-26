package store

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// a run another SpecWright window is running is not marked interrupted;
// one whose window is gone is
func TestMarkInterruptedSkipsLiveOwners(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// a live process other than this one
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer cmd.Process.Kill()
	gone := exec.Command("true")
	_ = gone.Run()
	now := time.Now()
	for _, r := range []Run{
		{ID: "mine", State: "running", Started: now},
		{ID: "other-window", State: "running", Started: now, Owner: cmd.Process.Pid},
		{ID: "closed-window", State: "running", Started: now, Owner: gone.Process.Pid},
	} {
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if r, _ := s.GetRun("mine"); r.Owner != os.Getpid() {
		t.Fatalf("a live run records its owner: %d", r.Owner)
	}
	if n := s.MarkInterrupted(); n != 2 {
		t.Fatalf("interrupted %d, want 2 (this process's and the closed window's)", n)
	}
	if r, _ := s.GetRun("other-window"); r.State != "running" {
		t.Fatalf("the other window's run: %s", r.State)
	}
	if r, _ := s.GetRun("closed-window"); r.State != "interrupted" {
		t.Fatalf("the closed window's run: %s", r.State)
	}
}
