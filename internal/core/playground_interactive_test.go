package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"codegen/internal/agent"
)

// recorder is an agent that keeps the requests it gets.
type recorder struct {
	mu   sync.Mutex
	reqs []agent.Request
}

func (r *recorder) Name() string { return "mock" }
func (r *recorder) Status(context.Context) agent.Status {
	return agent.Status{Name: "mock", Available: true}
}
func (r *recorder) Run(_ context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return agent.Result{Text: "ok"}, nil
}

// the Playground's "Ask me" reaches the agent
func TestPlaygroundInteractive(t *testing.T) {
	h, _ := newHub(t)
	rec := &recorder{}
	h.Agents.Add(rec)
	for _, ask := range []bool{true, false} {
		id, err := h.RunPrompt("hello", PromptOptions{Interactive: ask})
		if err != nil {
			t.Fatal(err)
		}
		for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); time.Sleep(20 * time.Millisecond) {
			if r, err := h.Store.GetRun(id); err == nil && r.State != "running" {
				break
			}
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) != 2 || !rec.reqs[0].Interactive || rec.reqs[1].Interactive {
		t.Fatalf("%+v", rec.reqs)
	}
}
