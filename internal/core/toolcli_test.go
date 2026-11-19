package core

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"codegen/internal/agent"
	"codegen/internal/extension"
)

// Without MCP, an agent runs SpecWright's tools with its shell:
// "$CODEGEN_TOOL" tool <name> '<json>'.
func TestToolCommandWithoutMCP(t *testing.T) {
	h, _ := flowHub(t)
	for _, kv := range h.Bridge.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := extension.RunToolCLI(args, strings.NewReader(`{"tool":{"name":"from-stdin","command":"echo x"}}`), &out, &errb)
		return code, out.String(), errb.String()
	}
	code, out, _ := run("list")
	for _, want := range []string{"save_flow\t", "save_tool\t", "read_file\t", "echo-arg\t(project tool)"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("list lacks %q (%d):\n%s", want, code, out)
		}
	}
	if code, out, _ := run("save_tool", `{"tool":{"name":"greet","command":"echo hi"}}`); code != 0 || !strings.Contains(out, "Proposed a new tool greet") {
		t.Fatalf("save_tool: %d %s", code, out)
	}
	if code, out, _ := run("agentbridge-save_tool", "-"); code != 0 || !strings.Contains(out, "from-stdin") {
		t.Fatalf("arguments from stdin, with the Copilot prefix: %d %s", code, out)
	}
	if code, out, _ := run("echo-arg", `{"v":"hello"}`); code != 0 || strings.TrimSpace(out) != "hello" {
		t.Fatalf("a project tool: %d %q", code, out)
	}
	if code, _, e := run("save_tool", "not json"); code == 0 || !strings.Contains(e, "not a JSON object") {
		t.Fatalf("bad arguments: %d %s", code, e)
	}
	if code, out, _ := run("save_flow", "--schema"); code != 0 || !strings.Contains(out, "base_flow_id") {
		t.Fatalf("schema: %d %s", code, out)
	}
	if code, _, _ := run("no_such_tool", "{}"); code == 0 {
		t.Fatal("an unknown tool should fail")
	}
}

// noMCP is an agent whose CLI never loads SpecWright's MCP server.
type noMCP struct{}

func (noMCP) Name() string                         { return "nomcp" }
func (noMCP) Status(context.Context) agent.Status { return agent.Status{Name: "nomcp", Available: true} }
func (noMCP) Run(_ context.Context, req agent.Request, emit func(agent.Event)) (agent.Result, error) {
	emit(agent.Event{Type: "model.text", Data: map[string]any{"text": "done"}})
	return agent.Result{Text: "done"}, nil
}

// A run whose agent never reached SpecWright's tools says so, and its
// prompt told the agent about the tool command.
func TestRunWarnsWhenToolsDidNotReachTheAgent(t *testing.T) {
	h, _ := newHub(t)
	h.Agents.Add(noMCP{})
	if err := h.SetAgent("nomcp", "", ""); err != nil {
		t.Fatal(err)
	}
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	id, err := h.RunPrompt("hello", PromptOptions{Tools: true})
	if err != nil {
		t.Fatal(err)
	}
	warned, noted := false, false
	for to := time.After(10 * time.Second); ; {
		select {
		case e := <-ch:
			if e.RunID != id {
				continue
			}
			if e.Type == "prompt.compose" && strings.Contains(e.Str("prompt"), `"$CODEGEN_TOOL" tool list`) {
				noted = true
			}
			if e.Type == "tools.unreachable" {
				warned = e.Seq > 0 && strings.Contains(e.Summary, "agentbridge")
			}
			if e.Type == "run.end" {
				if !noted || !warned {
					t.Fatalf("note in the prompt %v, warning %v", noted, warned)
				}
				return
			}
		case <-to:
			t.Fatal("timed out")
		}
	}
}
