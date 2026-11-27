package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codegen/internal/bus"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

// a flow step that sends data out (POST) needs its approval, like a command
func TestOutwardNeedsApproval(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.Method+" "+string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()
	h, ws := flowHub(t)
	f := flow.New("post", "Post it")
	f.Nodes = append(f.Nodes, flow.Node{ID: "send", Type: "tool", Tool: "builtin:http_request", X: 200,
		Args: map[string]string{"method": "POST", "url": srv.URL + "/items", "body": "hello"}})
	f.Edges = []flow.Edge{{From: "start", To: "send"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	cmds, _ := h.FlowCommands("post")
	if len(cmds) != 1 || cmds[0].Kind != "sends" || cmds[0].Approved || !strings.Contains(cmds[0].Command, "POST "+srv.URL) {
		t.Fatalf("%+v", cmds)
	}
	if _, err := h.RunFlow("post", FlowRunOptions{Review: "none"}); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("refused before approval: %v", err)
	}
	if _, err := h.ApproveFlowCommands("post", []string{"send"}); err != nil {
		t.Fatal(err)
	}
	st, err := h.RunFlow("post", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	mu.Lock()
	sent := strings.Join(bodies, ",")
	mu.Unlock()
	if st.Status != "done" || sent != "POST hello" {
		t.Fatalf("%s %s %q", st.Status, st.Error, sent)
	}
	// a changed URL is a new approval
	f, _ = flow.Load(ws, "post")
	f.Node("send").Args["url"] = srv.URL + "/other"
	f.Rev = ""
	h.SaveFlow(*f)
	if cmds, _ = h.FlowCommands("post"); cmds[0].Approved {
		t.Fatal("changed: approve again")
	}
	// GET needs nothing
	f.Node("send").Args["method"] = "GET"
	h.SaveFlow(*f)
	if cmds, _ = h.FlowCommands("post"); len(cmds) != 0 {
		t.Fatalf("%+v", cmds)
	}
}

// an agent's call that sends data out is asked in the app
func TestAgentOutwardAsks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	h, _ := flowHub(t)
	var asked []string
	allow := false
	h.SetPermissionAsker(func(_ context.Context, req extension.PermissionRequest) extension.PermissionAnswer {
		asked = append(asked, req.Tool+": "+req.Input["description"].(string))
		return extension.PermissionAnswer{Allow: allow, Message: "no"}
	})
	// a GET sends nothing out, but this server is on this computer: asked (agentNetworkAllowed)
	if _, err := h.RunBuiltin(h.ctx, "http_request", map[string]any{"url": srv.URL}); err == nil || len(asked) != 1 || !strings.Contains(asked[0], "on this computer") {
		t.Fatalf("a local GET is asked once: %v %v", err, asked)
	}
	asked = nil
	if _, err := h.RunBuiltin(h.ctx, "http_request", map[string]any{"method": "POST", "url": srv.URL, "body": "x"}); err == nil || len(asked) != 1 || !strings.Contains(asked[0], "POST "+srv.URL) {
		t.Fatalf("POST is asked and refused: %v %v", err, asked)
	}
	allow = true
	if out, err := h.RunBuiltin(h.ctx, "http_request", map[string]any{"method": "POST", "url": srv.URL, "body": "x"}); err != nil || !strings.Contains(out, "200") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestNotifyDesktop(t *testing.T) {
	h, _ := flowHub(t)
	old := notifyCommand
	var shown []string
	notifyCommand = func(title, msg string) error { shown = append(shown, title+"|"+msg); return nil }
	t.Cleanup(func() { notifyCommand = old })
	sub, stop := h.Bus.Subscribe(16, func(e bus.Event) bool { return e.Type == "notify" })
	defer stop()
	if _, err := h.RunBuiltin(h.ctx, "notify", map[string]any{"title": "Nightly", "message": "All tests passed"}); err != nil {
		t.Fatal(err)
	}
	got := false
	for dl := time.After(2 * time.Second); !got; {
		select {
		case e := <-sub:
			got = e.Type == "notify" && e.Summary == "Nightly"
		case <-dl:
			t.Fatal("the app is told")
		}
	}
	time.Sleep(100 * time.Millisecond)
	if len(shown) != 1 || shown[0] != "Nightly|All tests passed" {
		t.Fatal(shown)
	}
}

// an agent reuses a flow: its outputs come back, the run is tagged
func TestRunFlowTool(t *testing.T) {
	h, _ := flowHub(t)
	allowAgentActions(h) // the person says yes to the AI's actions (agentactions.go)
	out, err := h.RunBuiltin(h.ctx, RunFlowTool, map[string]any{"flow_id": "notes", "inputs": map[string]any{"version": "5.0"}, "timeout_seconds": 60.0})
	if err != nil || !strings.Contains(out, ": done") || !strings.Contains(out, "# 5.0") {
		t.Fatalf("%v\n%s", err, out)
	}
	runs, _ := h.FlowRuns(5)
	if len(runs) == 0 || runs[0].Trigger != "by an agent" {
		t.Fatalf("%+v", runs)
	}
	id := runs[0].ID
	if again, _ := h.RunBuiltin(h.ctx, RunFlowTool, map[string]any{"run_id": id}); !strings.Contains(again, id) || !strings.Contains(again, "# 5.0") {
		t.Fatal(again)
	}
	if _, err := h.RunBuiltin(h.ctx, RunFlowTool, map[string]any{"flow_id": "notes"}); err == nil {
		t.Fatal("a required input is missing: refused")
	}
	if _, err := h.RunBuiltin(h.ctx, RunFlowTool, map[string]any{}); err == nil {
		t.Fatal("flow_id or run_id")
	}
}
