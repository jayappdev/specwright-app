package core

import (
	"context"
	"testing"
	"time"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

// answerPerm answers the next permission.request on the bus.
func answerPerm(t *testing.T, h *Hub, decision, note string) {
	ch, unsub := h.Bus.Subscribe(64, func(e bus.Event) bool { return e.Type == "permission.request" })
	go func() {
		defer unsub()
		select {
		case e := <-ch:
			if err := h.AnswerPermission(e.Str("id"), decision, note); err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("no permission.request")
		}
	}()
}

func TestPermissionPrompt(t *testing.T) {
	h, _ := newHub(t)
	ctx := context.Background()
	req := extension.PermissionRequest{Agent: "a1", Tool: "WebFetch", Input: map[string]any{"url": "https://example.com"}}

	answerPerm(t, h, "deny", "use the local docs")
	if a := h.AskPermission(ctx, req); a.Allow || a.Message == "" {
		t.Fatalf("deny: %+v", a)
	}
	answerPerm(t, h, "once", "")
	if a := h.AskPermission(ctx, req); !a.Allow {
		t.Fatalf("once: %+v", a)
	}
	answerPerm(t, h, "run", "")
	if a := h.AskPermission(ctx, req); !a.Allow {
		t.Fatalf("run: %+v", a)
	}
	// remembered for this agent process without asking again …
	if a := h.AskPermission(ctx, req); !a.Allow {
		t.Fatalf("remembered: %+v", a)
	}
	// … but not for another run
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	other := req
	other.Agent = "a2"
	if a := h.AskPermission(cctx, other); a.Allow {
		t.Fatalf("another run must be asked: %+v", a)
	}
	if err := h.AnswerPermission("nope", "once", ""); err == nil {
		t.Fatal("unknown id must fail")
	}
	if err := h.AnswerPermission("x", "maybe", ""); err == nil {
		t.Fatal("unknown decision must fail")
	}
}
