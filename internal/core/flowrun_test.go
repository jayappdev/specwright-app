package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// waitFlow polls a run until pred holds (or fails the test).
func waitFlow(t *testing.T, h *Hub, id string, pred func(FlowRunState) bool) FlowRunState {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st, err := h.FlowRun(id)
		if err == nil && pred(st) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, _ := h.FlowRun(id)
	t.Fatalf("timed out; state %s %+v", st.Status, st.Steps)
	return st
}

func flowHub(t *testing.T) (*Hub, string) {
	usePolicy(t, "")
	h, ws := newHub(t)
	if _, err := h.SaveExtension(extension.Extension{Name: "echo-arg", Desc: "Echo", Lang: "Bash", Scope: "project", Approved: true,
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "echo {{v}}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("echo-arg"); err != nil {
		t.Fatal(err)
	}
	f := flow.New("notes", "Release notes")
	f.Inputs = []flow.Input{{Name: "version", Label: "Version", Type: "text", Required: true}, {Name: "tickets", Label: "Tickets", Type: "multiline"}}
	f.Context = flow.Context{Text: "We ship weekly."}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "fixed {{inputs.version}}"}, X: 200},
		flow.Node{ID: "up", Type: "transform", Op: "uppercase", X: 400},
		flow.Node{ID: "write", Type: "ai", Title: "Write", Prompt: "Write notes for {{steps.up}}", X: 600},
		flow.Node{ID: "md", Type: "output", Format: "md", Template: "# {{inputs.version}}\n\n{{steps.write}}", X: 800, Actions: []flow.Action{{Type: "save", Path: "out/notes-{{inputs.version}}.md"}, {Type: "show"}}},
		flow.Node{ID: "js", Type: "output", Format: "json", Template: "{{steps.start}}", X: 800, Y: 200})
	f.Edges = []flow.Edge{{From: "start", To: "echo"}, {From: "echo", To: "up"}, {From: "up", To: "write"}, {From: "write", To: "md"}, {From: "start", To: "js"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	return h, ws
}

func TestFlowRunEndToEnd(t *testing.T) {
	h, ws := flowHub(t)
	if _, err := h.RunFlow("notes", FlowRunOptions{Review: "none"}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatal("missing required input must be refused:", err)
	}
	st, err := h.RunFlow("notes", FlowRunOptions{Review: "none", Inputs: map[string]string{"version": "1.4"}})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Steps)
	}
	for _, s := range st.Steps {
		if s.Status != "done" {
			t.Fatalf("%s: %s %s", s.ID, s.Status, s.Error)
		}
	}
	b, _ := os.ReadFile(filepath.Join(ws, "out", "notes-1.4.md"))
	if !strings.Contains(string(b), "# 1.4") || !strings.Contains(string(b), "Summary: Write notes for FIXED 1.4") {
		t.Fatalf("output: %q", b)
	}
	if len(st.Outputs) != 2 || !st.Outputs[0].Show && !st.Outputs[1].Show {
		t.Fatalf("%+v", st.Outputs)
	}
	if st.Usage.ToolCalls < 1 || st.Usage.ModelCalls < 1 {
		t.Fatalf("usage %+v", st.Usage)
	}
	// recorded: history and metrics
	runs, _ := h.FlowRuns(10)
	if len(runs) != 1 || runs[0].State != "done" {
		t.Fatalf("%+v", runs)
	}
	m, _ := h.Metrics()
	if m.Flows.Executed != 1 || m.Flows.Completed != 1 || m.Flows.ToolExecutions < 1 || m.Flows.HoursSaved <= 0 || len(m.Flows.Top) != 1 {
		t.Fatalf("%+v", m.Flows)
	}
	// finished runs are read back from the store
	h.mu.Lock()
	delete(h.flowRuns, st.ID)
	h.mu.Unlock()
	if back, err := h.FlowRun(st.ID); err != nil || back.Status != "done" {
		t.Fatal(back.Status, err)
	}
}

func TestFlowRunReviewIterateAndSave(t *testing.T) {
	h, ws := flowHub(t)
	st, err := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "2.0"}}) // default review: after AI steps
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	if st.Review == nil || st.Review.Step != "write" {
		t.Fatalf("%+v", st.Review)
	}
	// refine with AI and keep it in the flow
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "refine", Prompt: "mention the date", Save: true}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" && strings.Contains(s.Review.Output, "Revised") })
	f, _ := flow.Load(ws, "notes")
	if !strings.Contains(f.Node("write").Prompt, "Also: mention the date") || len(st.Changes) != 1 {
		t.Fatalf("prompt not saved: %q %v", f.Node("write").Prompt, st.Changes)
	}
	// a one-time command on the output
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "command", Command: "tr a-z A-Z"}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" && strings.Contains(s.Review.Output, "REVISED") })
	if f2, _ := flow.Load(ws, "notes"); len(f2.Nodes) != len(f.Nodes) {
		t.Fatal("a one-time command must not change the flow")
	}
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "continue"}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })
	b, _ := os.ReadFile(filepath.Join(ws, "out", "notes-2.0.md"))
	if !strings.Contains(string(b), "REVISED ANSWER (MENTION THE DATE)") {
		t.Fatalf("%q", b)
	}

	// stop at a review
	st, _ = h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "2.1"}})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	_ = h.DecideFlow(st.ID, FlowDecision{Action: "stop"})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "stopped" })
	if _, err := os.Stat(filepath.Join(ws, "out", "notes-2.1.md")); err == nil {
		t.Fatal("a stopped run must not produce later outputs")
	}
}

func TestFlowRunOptions(t *testing.T) {
	h, ws := flowHub(t)
	// once per ticket, keeping only ORD-*, run twice, skipping the AI step
	st, err := h.RunFlow("notes", FlowRunOptions{Review: "none", Repeat: 2, Skip: []string{"js"},
		Inputs:  map[string]string{"version": "x", "tickets": "ORD-1\nINFRA-9\nORD-2\n"},
		ForEach: &ForEach{Input: "version", Split: "lines", Filter: "^ORD-"}})
	if err == nil {
		_ = st
	}
	// the for-each input must hold the list: use tickets as the varying input
	st, err = h.RunFlow("notes", FlowRunOptions{Review: "none", Repeat: 2, Skip: []string{"js"},
		Inputs:  map[string]string{"version": "ORD-1\nINFRA-9\nORD-2"},
		ForEach: &ForEach{Input: "version", Split: "lines", Filter: "^ORD-"}})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" || st.Items != 2 || st.Passes != 2 || len(st.Outputs) != 4 {
		t.Fatalf("%s items=%d passes=%d outputs=%d %s", st.Status, st.Items, st.Passes, len(st.Outputs), st.Error)
	}
	for _, v := range []string{"ORD-1", "ORD-2"} {
		if _, err := os.Stat(filepath.Join(ws, "out", "notes-"+v+".md")); err != nil {
			t.Fatal(v, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, "out", "notes-INFRA-9.md")); err == nil {
		t.Fatal("filtered item ran")
	}
	// stop after a step; a start time in the future waits
	st, _ = h.RunFlow("notes", FlowRunOptions{Review: "none", StopAfter: "up", Inputs: map[string]string{"version": "3"}})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })
	for _, s := range st.Steps {
		if (s.ID == "write" || s.ID == "md") && s.Status != "skipped" {
			t.Fatalf("%s %s", s.ID, s.Status)
		}
	}
	st, _ = h.RunFlow("notes", FlowRunOptions{Review: "none", StartAt: time.Now().Add(time.Hour), Inputs: map[string]string{"version": "4"}})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "scheduled" })
	_ = h.StopFlowRun(st.ID)
	waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "stopped" })
}

func TestFlowRunCredentials(t *testing.T) {
	h, _ := flowHub(t)
	// a tool that needs a secret
	if _, err := h.SaveExtension(extension.Extension{Name: "needs-token", Desc: "Needs a token", Lang: "Bash", Scope: "project",
		Secrets: []extension.Secret{{Name: "FLOW_TEST_TOKEN", Desc: "API token"}}, Command: "printenv FLOW_TEST_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	_, _ = h.ApproveExtension("needs-token")
	f := flow.New("tok", "Token flow")
	f.Nodes = append(f.Nodes, flow.Node{ID: "t", Type: "tool", Tool: "needs-token", X: 200}, flow.Node{ID: "o", Type: "output", Format: "txt", X: 400, Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "t"}, {From: "t", To: "o"}}
	_, _ = h.SaveFlow(*f)

	// keychain first
	store := stubKeychain(t)
	creds, _ := h.FlowCredentials("tok")
	if len(creds) != 1 || creds[0].Name != "FLOW_TEST_TOKEN" || creds[0].Via != "ask" {
		t.Fatalf("%+v", creds)
	}
	if err := h.SaveSecretToKeychain("FLOW_TEST_TOKEN", "kc-secret-1"); err != nil {
		t.Fatal(err)
	}
	if creds, _ = h.FlowCredentials("tok"); creds[0].Via != "keychain" {
		t.Fatalf("%+v", creds)
	}
	st, _ := h.RunFlow("tok", FlowRunOptions{Review: "none"})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatal(st.Error)
	}
	if strings.Contains(st.Outputs[0].Content, "kc-secret-1") || strings.Contains(st.Steps[1].Output, "kc-secret-1") {
		t.Fatal("secret value must be redacted in recorded state")
	}

	// no keychain entry: the run stops and asks, tagged with the run id
	delete(store, "SpecWright/FLOW_TEST_TOKEN")
	kcForget("FLOW_TEST_TOKEN")
	h.sec.mu.Lock()
	delete(h.sec.values, "FLOW_TEST_TOKEN")
	h.sec.mu.Unlock()
	ch, unsub := h.Bus.Subscribe(256, nil)
	defer unsub()
	st, _ = h.RunFlow("tok", FlowRunOptions{Review: "none"})
	var reqID, runOf string
	for e := range ch {
		if e.Type == "secret.request" {
			reqID, _ = e.Data["id"].(string)
			runOf = e.RunID
			break
		}
	}
	if runOf != st.ID {
		t.Fatalf("secret request not tagged with the run: %q", runOf)
	}
	if err := h.AnswerSecrets(reqID, map[string]string{"FLOW_TEST_TOKEN": "typed-2"}, false); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatal(st.Error)
	}
}

func TestFlowToolStepDescribedInWords(t *testing.T) {
	h, ws := flowHub(t)
	f := flow.New("ask", "Ask in words")
	f.Inputs = []flow.Input{{Name: "who", Label: "Who", Type: "text"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "q", Type: "tool", Title: "Query", Tool: "echo-arg", Args: map[string]string{"v": ""}, Instruction: "greet {{inputs.who}}", X: 200},
		flow.Node{ID: "o", Type: "output", Format: "txt", X: 400, Actions: []flow.Action{{Type: "save", Path: "out/ask.txt"}}})
	f.Edges = []flow.Edge{{From: "start", To: "q"}, {From: "q", To: "o"}}
	st, err := h.SaveFlow(*f)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range st.Issues {
		if is.Level == "error" {
			t.Fatal("a described step needs no exact arguments:", is.Message)
		}
	}
	run, err := h.RunFlow("ask", FlowRunOptions{Review: "none", Inputs: map[string]string{"who": "Ada"}})
	if err != nil {
		t.Fatal(err)
	}
	fs := waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if fs.Status != "done" {
		t.Fatal(fs.Error)
	}
	q := fs.Steps[1]
	if q.Args["v"] != "AI: greet Ada" || q.Why == "" || !strings.Contains(q.Output, "AI: greet Ada") {
		t.Fatalf("%+v", q)
	}
	b, _ := os.ReadFile(filepath.Join(ws, "out", "ask.txt"))
	if !strings.Contains(string(b), "AI: greet Ada") {
		t.Fatalf("%q", b)
	}
	// a value the user filled in stays fixed
	f.Nodes[1].Args["v"] = "exactly this"
	_, _ = h.SaveFlow(*f)
	run, _ = h.RunFlow("ask", FlowRunOptions{Review: "none", Inputs: map[string]string{"who": "Ada"}})
	fs = waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if fs.Steps[1].Args["v"] != "exactly this" {
		t.Fatalf("%+v", fs.Steps[1].Args)
	}
	// without a description, an empty required argument is still an error
	f.Nodes[1].Instruction, f.Nodes[1].Args["v"] = "", ""
	st, _ = h.SaveFlow(*f)
	bad := false
	for _, is := range st.Issues {
		bad = bad || (is.Level == "error" && strings.Contains(is.Message, "required parameter"))
	}
	if !bad {
		t.Fatal("empty required argument must be flagged")
	}
}

func TestParseAIArgs(t *testing.T) {
	for _, in := range []string{
		`{"arguments": {"sql": "select 1"}, "why": "x"}`,
		"```json\n{\"arguments\": {\"sql\": \"select 1\"}}\n```",
		"I looked at migrations/001.sql first.\n{\"arguments\": {\"sql\": \"select {a} from t\"}, \"why\": \"uses {braces}\"}",
	} {
		a, err := parseAIArgs(in)
		if err != nil || a.Arguments["sql"] == nil {
			t.Fatalf("%q: %v %+v", in, err, a)
		}
	}
	if _, err := parseAIArgs("no json here"); err == nil {
		t.Fatal("must fail")
	}
}

func TestFlowConnectedOutputReachesNextStep(t *testing.T) {
	h, _ := flowHub(t)
	for _, e := range []extension.Extension{
		{Name: "upper-stdin", Desc: "Upper-cases its standard input", Lang: "Bash", Scope: "project", Command: "tr a-z A-Z"},
		{Name: "read-input-file", Desc: "Prints the input file", Lang: "Bash", Scope: "project", Command: `bash -c "cat \"$SPECWRIGHT_INPUT_FILE\""`},
	} {
		if _, err := h.SaveExtension(e); err != nil {
			t.Fatal(err)
		}
		if _, err := h.ApproveExtension(e.Name); err != nil {
			t.Fatal(err)
		}
	}
	f := flow.New("pipe", "Pipe")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "a", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "hello pipes"}, X: 200},
		flow.Node{ID: "b", Type: "tool", Tool: "upper-stdin", X: 400},     // uses no argument: gets "hello pipes" on stdin
		flow.Node{ID: "c", Type: "tool", Tool: "read-input-file", X: 600}, // reads $SPECWRIGHT_INPUT_FILE
		flow.Node{ID: "d", Type: "ai", Prompt: "Summarize it", X: 800},    // does not mention the previous step
		flow.Node{ID: "o", Type: "output", Format: "txt", X: 1000, Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "a"}, {From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "d"}, {From: "d", To: "o"}}
	st, err := h.SaveFlow(*f)
	if err != nil {
		t.Fatal(err)
	}
	warned := 0
	for _, is := range st.Issues {
		if is.Level == "warning" && strings.Contains(is.Message, "piped in") {
			warned++
		}
	}
	if warned != 2 { // b and c do not reference their input in an argument
		t.Fatalf("want 2 warnings, got %d: %+v", warned, st.Issues)
	}
	run, _ := h.RunFlow("pipe", FlowRunOptions{Review: "none"})
	fs := waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if fs.Status != "done" {
		t.Fatal(fs.Error)
	}
	out := map[string]string{}
	for _, s := range fs.Steps {
		out[s.ID] = strings.TrimSpace(s.Output)
	}
	if out["b"] != "HELLO PIPES" || out["c"] != "HELLO PIPES" || out["d"] != "Saw input: HELLO PIPES" {
		t.Fatalf("%+v", out)
	}
	// referencing the output in an argument clears the warning
	f.Nodes[2].Args = map[string]string{"x": "{{steps.a}}"}
	st, _ = h.SaveFlow(*f)
	for _, is := range st.Issues {
		if is.Node == "b" && strings.Contains(is.Message, "piped in") {
			t.Fatal("warning should be gone:", is.Message)
		}
	}
}

// The user's case: a tool with a file field. Typing $SPECWRIGHT_INPUT_FILE,
// or using {{input_file}}, gives the tool the path of a file holding the
// previous step's output (tools run without a shell).
func TestFlowInputFileField(t *testing.T) {
	h, _ := flowHub(t)
	if _, err := h.SaveExtension(extension.Extension{Name: "report-from-file", Desc: "Builds a report from a file", Lang: "Bash", Scope: "project",
		Params:  []extension.Param{{Name: "content_file", Type: "string", Required: true, Desc: "File with the content"}, {Name: "title", Type: "string"}},
		Command: "cat {{content_file}}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("report-from-file"); err != nil {
		t.Fatal(err)
	}
	f := flow.New("files", "Files")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "q", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "row 1 | row 2"}, X: 200},
		flow.Node{ID: "typed", Type: "tool", Tool: "report-from-file", Args: map[string]string{"content_file": "$SPECWRIGHT_INPUT_FILE", "title": ""}, X: 400, Y: 0},
		flow.Node{ID: "var", Type: "tool", Tool: "report-from-file", Args: map[string]string{"content_file": "{{input_file}}"}, X: 400, Y: 200})
	f.Edges = []flow.Edge{{From: "start", To: "q"}, {From: "q", To: "typed"}, {From: "q", To: "var"}}
	st, err := h.SaveFlow(*f)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range st.Issues {
		if is.Node == "var" || is.Level == "error" {
			t.Fatalf("unexpected issue: %+v", is)
		}
	}
	run, _ := h.RunFlow("files", FlowRunOptions{Review: "none"})
	fs := waitFlow(t, h, run.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if fs.Status != "done" {
		t.Fatal(fs.Error)
	}
	for _, s := range fs.Steps {
		if (s.ID == "typed" || s.ID == "var") && strings.TrimSpace(s.Output) != "row 1 | row 2" {
			t.Fatalf("%s: %q (args %v)", s.ID, s.Output, s.Args)
		}
	}
}
