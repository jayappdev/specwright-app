// Package session is the state machine of a single run (one workflow step
// executed by one agent):
//
//	idle → running → paused (approval gate) → done
//	                 ↘ failed   ↘ running (changes requested)
//	running → cancelled | interrupted | failed
package session

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State of a run.
type State string

const (
	Idle        State = "idle"
	Running     State = "running"
	Paused      State = "paused"
	Done        State = "done"
	Failed      State = "failed"
	Cancelled   State = "cancelled"
	Interrupted State = "interrupted" // app closed mid-run; found on restart
)

// Terminal reports whether no further transitions are possible.
func (s State) Terminal() bool {
	return s == Done || s == Failed || s == Cancelled || s == Interrupted
}

var allowed = map[State][]State{
	Idle:    {Running, Cancelled},
	Running: {Paused, Done, Failed, Cancelled, Interrupted, Running}, // Done: a run without an approval gate
	Paused:  {Done, Failed, Running, Cancelled},
}

// ErrBadTransition is returned for a transition the machine does not allow.
var ErrBadTransition = errors.New("session: transition not allowed")

// Decision is the human answer at an approval gate.
type Decision struct {
	Kind     string `json:"kind"` // approve | changes | reject
	Feedback string `json:"feedback,omitempty"`
}

// Session tracks one run.
type Session struct {
	ID       string    `json:"id"`
	Workflow string    `json:"workflow"`
	Step     string    `json:"step"`
	Agent    string    `json:"agent"`
	Started  time.Time `json:"started"`

	mu       sync.Mutex
	state    State
	reason   string
	gateKind string
	history  []Transition
	decide   chan Decision
	onChange []func(Transition)
}

// Transition is one recorded state change.
type Transition struct {
	From   State     `json:"from"`
	To     State     `json:"to"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// New creates an idle session.
func New(id, workflow, step, agent string) *Session {
	return &Session{ID: id, Workflow: workflow, Step: step, Agent: agent, Started: time.Now(), state: Idle, decide: make(chan Decision, 1)}
}

// State returns the current state.
func (s *Session) State() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state }

// Reason returns why the last transition happened.
func (s *Session) Reason() string { s.mu.Lock(); defer s.mu.Unlock(); return s.reason }

// GateKind is "approval" or "loop" while paused.
func (s *Session) GateKind() string { s.mu.Lock(); defer s.mu.Unlock(); return s.gateKind }

// History returns all transitions so far.
func (s *Session) History() []Transition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Transition(nil), s.history...)
}

// OnChange registers a callback run after every transition.
func (s *Session) OnChange(f func(Transition)) {
	s.mu.Lock()
	s.onChange = append(s.onChange, f)
	s.mu.Unlock()
}

// To moves the session to next, or returns ErrBadTransition.
func (s *Session) To(next State, reason string) error {
	s.mu.Lock()
	cur := s.state
	ok := false
	for _, a := range allowed[cur] {
		if a == next {
			ok = true
			break
		}
	}
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s → %s", ErrBadTransition, cur, next)
	}
	t := Transition{From: cur, To: next, Reason: reason, At: time.Now()}
	s.state, s.reason = next, reason
	if next != Paused {
		s.gateKind = ""
	}
	s.history = append(s.history, t)
	cbs := append([]func(Transition){}, s.onChange...)
	s.mu.Unlock()
	for _, f := range cbs {
		f(t)
	}
	return nil
}

// Pause moves to Paused at a gate of the given kind.
func (s *Session) Pause(kind, reason string) error {
	if err := s.To(Paused, reason); err != nil {
		return err
	}
	s.mu.Lock()
	s.gateKind = kind
	s.mu.Unlock()
	return nil
}

// Decide delivers a human decision to a paused session.
func (s *Session) Decide(d Decision) error {
	if s.State() != Paused {
		return fmt.Errorf("session %s is %s, not waiting for approval", s.ID, s.State())
	}
	select {
	case s.decide <- d:
		return nil
	default:
		return errors.New("a decision is already pending")
	}
}

// Decisions is read by the runner while paused.
func (s *Session) Decisions() <-chan Decision { return s.decide }
