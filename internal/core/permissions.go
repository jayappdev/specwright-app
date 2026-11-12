package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

// Permission prompts: when an agent wants a tool it has no standing
// permission for (Claude's WebFetch, a shell tool, an MCP tool that is not
// pre-allowed), the CLI calls agentbridge's permission_prompt tool, which
// lands here. The user answers in the app: allow once, allow that tool for
// the rest of the run, or deny (with an optional note for the model).
// Before this, -p mode denied such tools silently and setups went wrong.

// AskPermissionTimeout is how long a permission prompt waits for the user.
var AskPermissionTimeout = 10 * time.Minute

// PermissionDecision is how the user answered a "permission.request".
type PermissionDecision string

const (
	AllowOnce   PermissionDecision = "once" // this call only
	AllowForRun PermissionDecision = "run"  // this tool, for the rest of this run
	Deny        PermissionDecision = "deny"
)

type permAnswer struct {
	decision PermissionDecision
	note     string
}

// SetPermissionAsker replaces the UI prompt (the headless runner asks on
// the terminal).
func (h *Hub) SetPermissionAsker(f func(context.Context, extension.PermissionRequest) extension.PermissionAnswer) {
	h.mu.Lock()
	h.askPerm = f
	h.mu.Unlock()
}

// AskPermission implements extension.Provider.
func (h *Hub) AskPermission(ctx context.Context, req extension.PermissionRequest) extension.PermissionAnswer {
	h.mu.Lock()
	if h.permAllowed[req.Agent][req.Tool] {
		h.mu.Unlock()
		return extension.PermissionAnswer{Allow: true}
	}
	asker := h.askPerm
	h.mu.Unlock()
	if asker != nil {
		return asker(ctx, req)
	}
	return h.askPermissionUI(ctx, req)
}

func (h *Hub) askPermissionUI(ctx context.Context, req extension.PermissionRequest) extension.PermissionAnswer {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	ch := make(chan permAnswer, 1)
	h.mu.Lock()
	if h.pendingPerms == nil {
		h.pendingPerms = map[string]chan permAnswer{}
	}
	h.pendingPerms[id] = ch
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.pendingPerms, id); h.mu.Unlock() }()

	// the input may carry a secret a tool printed earlier: redact before it
	// reaches the UI, the bus or history
	input := redactAny(h, req.Input)
	ev := bus.Event{Type: "permission.request", Summary: "Allow " + req.Tool + "? " + clipLine(h.Store.Redactor().String(permTarget(req.Input)), 100),
		Data: map[string]any{"id": id, "tool": req.Tool, "input": input, "target": h.Store.Redactor().String(permTarget(req.Input))}}
	if a := h.activeRun(); a != nil {
		ev.RunID, ev.Step = a.Session.ID, a.Session.Step
	} else if fr := h.onlyFlowRun(); fr != "" {
		ev.RunID = fr
	}
	h.Bus.Publish(ev)
	resolved := func(d PermissionDecision) {
		h.Bus.Publish(bus.Event{Type: "permission.resolved", RunID: ev.RunID, Step: ev.Step, Summary: req.Tool + ": " + permWords[d],
			Data: map[string]any{"id": id, "tool": req.Tool, "decision": string(d)}})
	}
	t := time.NewTimer(AskPermissionTimeout)
	defer t.Stop()
	select {
	case a := <-ch:
		resolved(a.decision)
		switch a.decision {
		case AllowForRun:
			h.mu.Lock()
			if h.permAllowed == nil {
				h.permAllowed = map[string]map[string]bool{}
			}
			if h.permAllowed[req.Agent] == nil {
				h.permAllowed[req.Agent] = map[string]bool{}
			}
			h.permAllowed[req.Agent][req.Tool] = true
			h.mu.Unlock()
			return extension.PermissionAnswer{Allow: true}
		case AllowOnce:
			return extension.PermissionAnswer{Allow: true}
		}
		msg := "The user denied permission to use " + req.Tool + "."
		if n := strings.TrimSpace(a.note); n != "" {
			msg += " They said: " + n
		}
		return extension.PermissionAnswer{Message: msg}
	case <-t.C:
		resolved(Deny)
		return extension.PermissionAnswer{Message: "No answer from the user within " + AskPermissionTimeout.String() + "; " + req.Tool + " was not run."}
	case <-ctx.Done():
		resolved(Deny)
		return extension.PermissionAnswer{Message: "The request was cancelled."}
	}
}

var permWords = map[PermissionDecision]string{AllowOnce: "allowed once", AllowForRun: "allowed for this run", Deny: "denied"}

// AnswerPermission delivers the user's decision for a pending
// "permission.request"; note (optional) is passed to the model on deny.
func (h *Hub) AnswerPermission(id, decision, note string) error {
	d := PermissionDecision(decision)
	if _, ok := permWords[d]; !ok {
		return fmt.Errorf("unknown decision %q", decision)
	}
	h.mu.RLock()
	ch := h.pendingPerms[id]
	h.mu.RUnlock()
	if ch == nil {
		return errors.New("that request is no longer waiting (it may have timed out or the run stopped)")
	}
	select {
	case ch <- permAnswer{decision: d, note: note}:
		return nil
	default:
		return errors.New("already answered")
	}
}

func (h *Hub) onlyFlowRun() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.flowRuns) != 1 {
		return ""
	}
	for id := range h.flowRuns {
		return id
	}
	return ""
}

// permTarget is the one-line gist of what a tool wants to do.
func permTarget(in map[string]any) string {
	for _, k := range []string{"command", "url", "file_path", "path", "pattern", "query", "prompt", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	if len(in) == 0 {
		return ""
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func redactAny(h *Hub, v map[string]any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal([]byte(h.Store.Redactor().String(string(b))), &out) != nil {
		return map[string]any{"input": h.Store.Redactor().String(string(b))}
	}
	return out
}

// TerminalPermissionAsker asks on the controlling terminal (headless):
// y = allow once, a = allow for this run, anything else denies.
func TerminalPermissionAsker(h *Hub) func(context.Context, extension.PermissionRequest) extension.PermissionAnswer {
	return func(ctx context.Context, req extension.PermissionRequest) extension.PermissionAnswer {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return extension.PermissionAnswer{Message: req.Tool + " needs permission and there is no terminal to ask on."}
		}
		defer tty.Close()
		fmt.Fprintf(tty, "\nThe agent wants to use %s: %s\nAllow? [y]es once / [a]ll this run / [N]o: ", req.Tool, clipLine(h.Store.Redactor().String(permTarget(req.Input)), 300))
		line, _ := bufio.NewReader(tty).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return extension.PermissionAnswer{Allow: true}
		case "a", "all":
			h.mu.Lock()
			if h.permAllowed == nil {
				h.permAllowed = map[string]map[string]bool{}
			}
			if h.permAllowed[req.Agent] == nil {
				h.permAllowed[req.Agent] = map[string]bool{}
			}
			h.permAllowed[req.Agent][req.Tool] = true
			h.mu.Unlock()
			return extension.PermissionAnswer{Allow: true}
		}
		return extension.PermissionAnswer{Message: "The user denied permission to use " + req.Tool + "."}
	}
}

func clipLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
