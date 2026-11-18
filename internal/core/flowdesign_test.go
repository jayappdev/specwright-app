package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"codegen/internal/agent"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

// scripted answers prompts in order and keeps them.
type scripted struct {
	mu      sync.Mutex
	replies []string
	prompts []string
}

func (s *scripted) Name() string { return "scripted" }
func (s *scripted) Status(context.Context) agent.Status {
	return agent.Status{Name: "scripted", Available: true}
}
func (s *scripted) Run(_ context.Context, r agent.Request, emit func(agent.Event)) (agent.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, r.Prompt)
	out := s.replies[0]
	if len(s.replies) > 1 {
		s.replies = s.replies[1:]
	}
	emit(agent.Event{Type: "run.usage", Data: map[string]any{"input_tokens": 1000, "output_tokens": 200}})
	return agent.Result{Text: out}, nil
}

const designReply = `{"options": [
 {"title": "With AI", "summary": "Asks AI", "why": "AI judges", "flow": {"name": "Gate AI", "nodes": [
   {"id": "start", "type": "start"}, {"id": "scan", "type": "tool", "tool": "gate-scan"},
   {"id": "judge", "type": "ai", "prompt": "Judge {{scan.buffer_output}}"}, {"id": "o", "type": "output", "format": "md", "actions": [{"type": "show"}]}],
   "edges": [{"from": "start", "to": "scan"}, {"from": "scan", "to": "judge"}, {"from": "judge", "to": "o"}]}},
 {"title": "Deterministic", "summary": "Filters blockers", "why": "Uses the tool's issues", "flow": {"name": "Gate", "nodes": [
   {"id": "start", "type": "start"}, {"id": "scan", "type": "tool", "tool": "gate-scan"},
   {"id": "Blockers!", "type": "filter", "cond": {"rules": [{"field": "severity", "op": "eq", "value": "BLOCKER"}]}},
   {"id": "o", "type": "output", "format": "json", "actions": [{"type": "show"}]}],
   "edges": [{"from": "start", "to": "scan"}, {"from": "scan", "to": "Blockers!"}, {"from": "Blockers!", "to": "o"}]}},
 {"title": "Broken", "summary": "Uses a tool that does not exist", "flow": {"name": "Bad", "nodes": [
   {"id": "start", "type": "start"}, {"id": "x", "type": "tool", "tool": "no-such-tool"}, {"id": "o", "type": "output", "format": "md"}],
   "edges": [{"from": "start", "to": "x"}, {"from": "x", "to": "o"}]}}
]}`

const repairReply = "```json\n" + `{"options": [{"title": "Fixed", "summary": "Lists files", "flow": {"name": "Fixed", "nodes": [
   {"id": "start", "type": "start"}, {"id": "ls", "type": "tool", "tool": "list_directory", "args": {"path": "."}}, {"id": "o", "type": "output", "format": "md", "actions": [{"type": "show"}]}],
   "edges": [{"from": "start", "to": "ls"}, {"from": "ls", "to": "o"}]}}]}` + "\n```"

func designHub(t *testing.T) (*Hub, *scripted) {
	h, _ := flowHub(t)
	s := &scripted{replies: []string{designReply, repairReply}}
	h.Agents.Add(s)
	h.mu.Lock()
	h.agentName = "scripted"
	h.mu.Unlock()
	out := flow.SchemaOfText(`{"issues": [{"severity": "BLOCKER", "file": "a.go"}]}`)
	if _, err := h.SaveExtension(extension.Extension{Name: "gate-scan", Desc: "Runs the quality gate and prints its issues as JSON.", Lang: "Bash", Scope: "project", Command: "echo {}", Output: out}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("gate-scan"); err != nil {
		t.Fatal(err)
	}
	return h, s
}

func TestDesignFlows(t *testing.T) {
	h, s := designHub(t)
	res, err := h.DesignFlows(DesignRequest{Prompt: "Tell me which blocking issues the quality gate found"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Options) != 3 || !res.Repaired {
		t.Fatalf("%+v", res)
	}
	// deterministic first, then AI; the repaired one checks out too
	o := res.Options
	if o[0].Title != "Deterministic" || !o[0].Deterministic || !o[0].Recommended || o[0].Errors != 0 {
		t.Fatalf("first: %+v", o[0])
	}
	if o[1].Title != "Fixed" || o[1].Errors != 0 || o[1].Tools[0] != "builtin:list_directory" {
		t.Fatalf("repaired, with builtin: added: %+v", o[1])
	}
	if o[2].AISteps != 1 || o[2].Deterministic {
		t.Fatalf("ai last: %+v", o[2])
	}
	// ids fixed, connections and references follow, steps laid out
	f := o[0].Flow
	if f.Node("Blockers") == nil || len(f.Edges) != 3 || f.Node("o").X <= f.Node("Blockers").X {
		t.Fatalf("%+v %+v", f.Nodes, f.Edges)
	}
	// the prompt: rules, the tools' contracts (output shapes included: the designer needs them)
	p := s.prompts[0]
	for _, want := range []string{"Use the project's existing TOOLS first", "- gate-scan: Runs the quality gate", "output: {issues: {severity: string, file: string}[]}", "builtin:read_file", "WHAT THE USER WANTS:\nTell me which blocking issues"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Index(p, "gate-scan") > strings.Index(p, "builtin:read_file") {
		t.Error("project tools come before built-in ones")
	}
	if !strings.Contains(s.prompts[1], "no-such-tool") || !strings.Contains(s.prompts[1], "PROBLEMS") {
		t.Fatal("repair prompt:", s.prompts[1])
	}
	if res.Usage.InputTokens != 2000 || res.Usage.ModelCalls != 2 {
		t.Fatalf("usage %+v", res.Usage)
	}
}

func TestImproveAndSaveVersion(t *testing.T) {
	h, s := designHub(t)
	s.replies = []string{`{"options": [{"title": "Safer", "summary": "Adds a check", "flow": {"name": "Release notes", "inputs": [{"name": "version", "type": "text", "required": true}],
	  "nodes": [{"id": "start", "type": "start"}, {"id": "echo", "type": "tool", "tool": "echo-arg", "args": {"v": "fixed {{inputs.version}}"}},
	  {"id": "up", "type": "transform", "op": "uppercase"}, {"id": "md", "type": "output", "format": "md", "actions": [{"type": "show"}]}],
	  "edges": [{"from": "start", "to": "echo"}, {"from": "echo", "to": "up"}, {"from": "up", "to": "md"}]}}]}`}
	res, err := h.DesignFlows(DesignRequest{FlowID: "notes", Prompt: "no AI please"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.prompts[0], "CURRENT FLOW:") || !strings.Contains(s.prompts[0], `"id":"write"`) {
		t.Fatal("the current flow is in the prompt")
	}
	o := res.Options[0]
	got := strings.Join(o.Changes, "; ")
	for _, want := range []string{"Changes md", "Removes Write", "Removes js"} {
		if !strings.Contains(got, want) {
			t.Errorf("changes %q: missing %q", got, want)
		}
	}
	if o.Flow.Context.Text != "We ship weekly." {
		t.Fatal("the context is kept")
	}
	st, err := h.SaveFlowVersion("notes", o.Flow)
	if err != nil {
		t.Fatal(err)
	}
	if st.Flow.ID != "release-notes-v2" || st.Flow.Name != "Release notes (v2)" || st.Flow.Parent != "notes" || st.Flow.Revision != 2 {
		t.Fatalf("%+v", st.Flow)
	}
	st3, _ := h.SaveFlowVersion(st.Flow.ID, o.Flow) // a version of a version: same family
	if st3.Flow.Name != "Release notes (v3)" || st3.Flow.Parent != "notes" || st3.Flow.Revision != 3 {
		t.Fatalf("%+v", st3.Flow)
	}
	orig, _ := h.LoadFlow("notes")
	if orig.Revision != 1 || orig.Node("write") == nil {
		t.Fatal("the original is kept, as v1")
	}
}

// Improving fixes the flow it is given: its problems are in the prompt, a
// fix that keeps the flow ranks before a rewrite, and a rewrite is flagged.
func TestImproveFixesTheFlow(t *testing.T) {
	h, s := designHub(t)
	f := flow.New("broken", "Broken")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "scan", Type: "tool", Tool: "old-scanner"},
		flow.Node{ID: "keep", Type: "filter", Cond: &flow.Cond{Rules: []flow.Rule{{Field: "severity", Op: "eq", Value: "BLOCKER"}}}},
		flow.Node{ID: "o", Type: "output", Format: "json", Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "scan"}, {From: "scan", To: "keep"}, {From: "keep", To: "o"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	s.replies = []string{`{"options": [
	 {"title": "Start over", "summary": "A different flow", "flow": {"name": "Other", "nodes": [{"id": "start", "type": "start"}, {"id": "ask", "type": "ai", "prompt": "do it"}, {"id": "out", "type": "output", "format": "md", "actions": [{"type": "show"}]}],
	   "edges": [{"from": "start", "to": "ask"}, {"from": "ask", "to": "out"}]}},
	 {"title": "Use gate-scan", "summary": "Replaces the missing tool", "flow": {"name": "Broken", "nodes": [{"id": "start", "type": "start"}, {"id": "scan", "type": "tool", "tool": "gate-scan"},
	   {"id": "keep", "type": "filter", "cond": {"rules": [{"field": "severity", "op": "eq", "value": "BLOCKER"}]}}, {"id": "o", "type": "output", "format": "json", "actions": [{"type": "show"}]}],
	   "edges": [{"from": "start", "to": "scan"}, {"from": "scan", "to": "keep"}, {"from": "keep", "to": "o"}]}}]}`}
	res, err := h.DesignFlows(DesignRequest{FlowID: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	p := s.prompts[0]
	for _, want := range []string{"PROBLEMS IN THE CURRENT FLOW (fix these first):", `The tool "old-scanner" is not available`, "This is a FIX of the current flow, not a new design"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	o := res.Options
	if o[0].Title != "Use gate-scan" || o[0].Fixes != 1 || o[0].Kept != 3 || o[0].Rewrite || !o[0].Recommended {
		t.Fatalf("the fix first: %+v", o[0])
	}
	if !o[1].Rewrite || o[1].Recommended || !strings.Contains(o[1].Issues[len(o[1].Issues)-1].Message, "replaces most of the current flow") {
		t.Fatalf("the rewrite is flagged: %+v", o[1])
	}
}

// Every option is tried with mocked tools before it is shown; one whose
// trial fails is repaired, and the trial is kept with the option.
func TestDesignTrialRuns(t *testing.T) {
	h, s := designHub(t)
	opt := func(sev string) string {
		return `{"options": [{"title": "Blockers by file", "summary": "s", "flow": {"name": "Blockers", "nodes": [
	  {"id": "start", "type": "start"}, {"id": "scan", "type": "tool", "tool": "gate-scan"},
	  {"id": "keep", "type": "filter", "cond": {"rules": [{"field": "severity", "op": "eq", "value": "` + sev + `"}]}},
	  {"id": "out", "type": "output", "format": "json", "actions": [{"type": "show"}]}],
	  "edges": [{"from": "start", "to": "scan"}, {"from": "scan", "to": "keep"}, {"from": "keep", "to": "out"}]},
	  "test": {"mocks": {"scan": {"issues": [{"severity": "BLOCKER", "file": "pay.go"}, {"severity": "MINOR", "file": "x.go"}]}},
	           "expect": [{"step": "out", "contains": ["pay.go"], "fields": ["[0].file"]}]}}]}`
	}
	s.replies = []string{opt("CRITICAL"), opt("BLOCKER")}
	res, err := h.DesignFlows(DesignRequest{Prompt: "blocking issues"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.prompts[0], `Each option is TRIED before the user sees it`) || !strings.Contains(s.prompts[1], "Trial run with sample data failed") || !strings.Contains(s.prompts[1], `"mocks"`) {
		t.Fatal("trial instructions and failures reach the agent")
	}
	o := res.Options[0]
	if !res.Repaired || o.Errors != 0 || o.Trial == nil || !o.Trial.OK {
		t.Fatalf("%+v", o)
	}
	out := ""
	for _, st := range o.Trial.Steps {
		if st.ID == "out" {
			out = st.Output
		}
	}
	if !strings.Contains(out, "pay.go") || strings.Contains(out, "x.go") {
		t.Fatal(out)
	}
}

// An answer that cannot be read is asked for again, not lost.
func TestDesignAsksAgainWhenTheAnswerCannotBeRead(t *testing.T) {
	h, s := designHub(t)
	s.replies = []string{"Here are some ideas, no JSON.", designReply, repairReply}
	res, err := h.DesignFlows(DesignRequest{Prompt: "blockers"})
	if err != nil || len(res.Options) == 0 {
		t.Fatal(err)
	}
	if !strings.Contains(s.prompts[1], "Your previous reply could not be used") {
		t.Fatal("the second call says what went wrong")
	}
}
