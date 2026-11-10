package session

import (
	"errors"
	"testing"
)

func TestTransitions(t *testing.T) {
	s := New("r1", "speckit", "plan", "mock")
	if err := s.To(Done, ""); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("idle → done must fail, got %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.To(Running, "start"))
	if err := s.Decide(Decision{Kind: "approve"}); err == nil {
		t.Fatal("decide while running must fail")
	}
	must(s.Pause("approval", "gate"))
	if s.GateKind() != "approval" {
		t.Fatal("gate kind")
	}
	must(s.Decide(Decision{Kind: "changes", Feedback: "more tests"}))
	if d := <-s.Decisions(); d.Feedback != "more tests" {
		t.Fatal("decision lost")
	}
	must(s.To(Running, "changes"))
	must(s.Pause("approval", "gate"))
	must(s.To(Done, "approved"))
	if !s.State().Terminal() || len(s.History()) != 5 {
		t.Fatalf("state %s history %d", s.State(), len(s.History()))
	}
	if err := s.To(Running, ""); err == nil {
		t.Fatal("done is terminal")
	}
	// a run without an approval gate finishes straight from running
	u := New("r2", "playground", "playground", "mock")
	must(u.To(Running, "start"))
	must(u.To(Done, "completed"))
	if !u.State().Terminal() {
		t.Fatal("ungated run must end done")
	}
}
