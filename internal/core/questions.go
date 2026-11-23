package core

// ask_user (builtin/ask.go): the questions an agent or a flow step asks a
// person, shown in a dialog on every page ("question.request"); the call
// waits until they send their answers, take the recommendations, or the
// time runs out (then the tool takes the recommendations, or fails, as
// the caller chose). The answers are the tool's result, so they are
// recorded with the run like any tool result.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"codegen/internal/builtin"
	"codegen/internal/bus"
)

// QuestionRequest is a pending ask_user call, for the dialog.
type QuestionRequest struct {
	ID        string                `json:"id"`
	Title     string                `json:"title,omitempty"`
	Intro     string                `json:"intro,omitempty"`
	Questions []builtin.Question    `json:"questions"`
	RunID     string                `json:"run_id,omitempty"`
	Step      string                `json:"step,omitempty"`
	Asked     time.Time             `json:"asked"`
	Deadline  time.Time             `json:"deadline"`
	reply     chan builtin.AskReply `json:"-"`
}

type questionsState struct {
	mu      sync.Mutex
	pending map[string]*QuestionRequest
}

func (h *Hub) askQuestions(ctx context.Context, r builtin.AskRequest) (builtin.AskReply, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	q := &QuestionRequest{ID: hex.EncodeToString(b), Title: r.Title, Intro: r.Intro, Questions: r.Questions,
		Asked: time.Now(), Deadline: time.Now().Add(r.Timeout), reply: make(chan builtin.AskReply, 1)}
	if fr, step := flowRunFrom(ctx); fr != "" {
		q.RunID, q.Step = fr, step
	} else if a := h.activeRun(); a != nil {
		q.RunID, q.Step = a.Session.ID, a.Session.Step
	}
	h.qs.mu.Lock()
	if h.qs.pending == nil {
		h.qs.pending = map[string]*QuestionRequest{}
	}
	h.qs.pending[q.ID] = q
	h.qs.mu.Unlock()
	defer func() { h.qs.mu.Lock(); delete(h.qs.pending, q.ID); h.qs.mu.Unlock() }()

	title := firstNonEmpty(q.Title, fmt.Sprintf("%d question%s for you", len(q.Questions), map[bool]string{true: "", false: "s"}[len(q.Questions) == 1]))
	h.Bus.Publish(bus.Event{Type: "question.request", RunID: q.RunID, Step: q.Step, Summary: title,
		Data: map[string]any{"id": q.ID, "title": q.Title, "intro": q.Intro, "questions": q.Questions, "deadline": q.Deadline.UTC().Format(time.RFC3339)}})
	_ = h.desktopNotify("SpecWright has questions for you", title, "")
	resolved := func(by string) {
		h.Bus.Publish(bus.Event{Type: "question.resolved", RunID: q.RunID, Step: q.Step, Summary: "Questions answered (" + by + ")", Data: map[string]any{"id": q.ID, "by": by}})
	}
	t := time.NewTimer(time.Until(q.Deadline))
	defer t.Stop()
	select {
	case a := <-q.reply:
		resolved(a.By)
		return a, nil
	case <-t.C:
		resolved("no answer")
		return builtin.AskReply{}, builtin.ErrNoAnswer
	case <-ctx.Done():
		resolved("stopped")
		return builtin.AskReply{}, ctx.Err()
	}
}

// PendingQuestions are the ask_user calls waiting for a person (oldest
// first), for a window opened while they wait.
func (h *Hub) PendingQuestions() []QuestionRequest {
	h.qs.mu.Lock()
	defer h.qs.mu.Unlock()
	out := []QuestionRequest{}
	for _, q := range h.qs.pending {
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Asked.Before(out[j].Asked) })
	return out
}

// AnswerQuestions sends a person's answers to a waiting ask_user call.
// recommended: they went with the recommendations as they were.
func (h *Hub) AnswerQuestions(id string, answers []builtin.Answer, recommended bool) error {
	h.qs.mu.Lock()
	q := h.qs.pending[id]
	h.qs.mu.Unlock()
	if q == nil {
		return errors.New("these questions are no longer waiting (answered, timed out or stopped)")
	}
	by := "person"
	if recommended {
		by = "person (the recommendations)"
	}
	if answers == nil {
		answers = []builtin.Answer{}
	}
	select {
	case q.reply <- builtin.AskReply{Answers: answers, By: by}:
		return nil
	default:
		return errors.New("these questions were answered already")
	}
}
