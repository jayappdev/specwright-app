package core

import (
	"time"

	"codegen/internal/flow"
	"context"
	"strings"
	"testing"

	"codegen/internal/builtin"
)

// ask_user from an agent or a flow: the questions reach the app, the
// person's answers come back as the tool's result.
func TestAskUserThroughTheApp(t *testing.T) {
	h, _ := newHub(t)
	ch, unsub := h.Bus.Subscribe(64, nil)
	defer unsub()
	go func() {
		for e := range ch {
			if e.Type == "question.request" {
				id := e.Str("id")
				if p := h.PendingQuestions(); len(p) != 1 || p[0].ID != id || len(p[0].Questions) != 1 {
					t.Errorf("pending: %+v", p)
				}
				_ = h.AnswerQuestions(id, []builtin.Answer{{ID: "go", Selected: []string{"No"}}}, false)
				return
			}
		}
	}()
	out, err := h.RunBuiltin(context.Background(), "ask_user", map[string]any{"questions": []any{
		map[string]any{"id": "go", "question": "Deploy now?", "options": []any{"Yes", "No"}, "recommended": "Yes"}}})
	if err != nil || !strings.Contains(out, `"selected": [`+"\n"+`        "No"`) || !strings.Contains(out, `"followed_recommended": false`) {
		t.Fatalf("%s %v", out, err)
	}
	if len(h.PendingQuestions()) != 0 {
		t.Fatal("still pending after the answer")
	}
	if err := h.AnswerQuestions("gone", nil, false); err == nil {
		t.Fatal("answering questions that are not waiting should fail")
	}
}

// a flow can ask too: ask_user is a built-in step, and the step's usual
// two minutes do not cut a person's answer short
func TestAskUserInFlows(t *testing.T) {
	h, _ := newHub(t)
	found := false
	for _, e := range h.FlowBuiltinTools() {
		found = found || e.Name == "builtin:ask_user"
	}
	if !found {
		t.Fatal("flows do not offer builtin:ask_user")
	}
	if d := stepTimeout(FlowRunOptions{}, &flow.Node{Type: "tool", Tool: "builtin:ask_user"}); d < 24*time.Hour {
		t.Fatalf("ask_user step timeout %s", d)
	}
}
