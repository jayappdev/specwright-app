package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// A flow can pre-approve what its AI steps do: the bridge matches each call
// to its run by the run's own token, so scheduled runs need nobody to
// answer, and another flow's run is still asked.
func TestFlowPreApprovedAIActions(t *testing.T) {
	h, _ := flowHub(t)
	var mu sync.Mutex
	var asked []string
	h.SetPermissionAsker(func(_ context.Context, r extension.PermissionRequest) extension.PermissionAnswer {
		mu.Lock()
		asked = append(asked, r.Agent+" "+r.Tool)
		mu.Unlock()
		return extension.PermissionAnswer{Message: "nobody here"}
	})
	mk := func(id string) *flow.Flow {
		f := flow.New(id, id)
		f.Nodes = append(f.Nodes, flow.Node{ID: "think", Type: "ai", Prompt: "do it"})
		if _, err := h.SaveFlow(*f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	a, b := mk("nightly"), mk("other")
	h.mu.Lock()
	if h.flowRuns == nil {
		h.flowRuns = map[string]*flowRun{}
	}
	h.flowRuns["run-a"] = &flowRun{flow: a}
	h.flowRuns["run-b"] = &flowRun{flow: b}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.flowRuns, "run-a"); delete(h.flowRuns, "run-b"); h.mu.Unlock() }()

	call := func(token, name string, args map[string]any) (int, string) {
		body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		req, _ := http.NewRequest("POST", h.Bridge.URL()+"/builtin", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ Output, Error string }
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Output + out.Error
	}
	tokA, endA := h.Bridge.NewScope("run-a")
	tokB, endB := h.Bridge.NewScope("run-b")
	defer endB()
	cmd := map[string]any{"command": "echo pre-approved"}

	if st, _ := h.FlowAIActions("nightly"); !st.HasAI || len(st.Allowed) != 0 {
		t.Fatalf("%+v", st)
	}
	if _, out := call(tokA, "run_command", cmd); !strings.Contains(out, "nobody here") || len(asked) != 1 || asked[0] != "flow:run-a run_command" {
		t.Fatalf("not pre-approved yet: %q %v", out, asked)
	}
	if _, err := h.SetFlowAIActions("nightly", []string{"commands", "bogus"}); err == nil {
		t.Fatal("an unknown kind was taken")
	}
	if st, err := h.SetFlowAIActions("nightly", []string{AIRunCommands}); err != nil || len(st.Allowed) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	mu.Lock()
	asked = nil
	mu.Unlock()
	if _, out := call(tokA, "run_command", cmd); !strings.Contains(out, "pre-approved") || len(asked) != 0 {
		t.Fatalf("pre-approved run: %q asked %v", out, asked)
	}
	// only that kind: changing flows is still asked
	if _, out := call(tokA, "save_flow", map[string]any{"flow": map[string]any{"name": "x"}}); !strings.Contains(out, "nobody here") {
		t.Fatalf("save_flow: %q", out)
	}
	// another flow's run is asked
	mu.Lock()
	asked = nil
	mu.Unlock()
	if _, out := call(tokB, "run_command", cmd); !strings.Contains(out, "nobody here") || len(asked) != 1 || asked[0] != "flow:run-b run_command" {
		t.Fatalf("other flow: %q %v", out, asked)
	}
	// the bridge's own token (a chat, the playground) is asked as before
	mu.Lock()
	asked = nil
	mu.Unlock()
	if _, out := call(h.Bridge.Token(), "run_command", cmd); !strings.Contains(out, "nobody here") || len(asked) != 1 {
		t.Fatalf("own token: %q %v", out, asked)
	}
	// a run's token stops working when its step ends
	endA()
	if code, _ := call(tokA, "run_command", cmd); code != http.StatusUnauthorized {
		t.Fatalf("ended scope: %d", code)
	}
	// clearing the approvals
	if st, _ := h.SetFlowAIActions("nightly", nil); len(st.Allowed) != 0 {
		t.Fatalf("%+v", st)
	}
}
