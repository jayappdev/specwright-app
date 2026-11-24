package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"codegen/internal/agent"
)

// waitResults waits until a run's results folder has been written.
func waitResults(t *testing.T, h *Hub, id string) RunResults {
	t.Helper()
	for i := 0; i < 200; i++ {
		if r, err := h.Store.GetRun(id); err == nil && r.Results != "" {
			res, err := h.Results(id)
			if err == nil && res.Ready {
				return res
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("results were not written for", id)
	return RunResults{}
}

func TestRunIDsResultsAndContinuation(t *testing.T) {
	h, ws := newHub(t)
	id, err := h.RunPrompt("What does the order service do?", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("run id %q is not date-time + UUID v4", id)
	}
	res := waitResults(t, h, id)
	wantDir := ResultsDir + "/" + id[0:4] + "-" + id[4:6] + "-" + id[6:8] + "/" + id
	if res.Dir != wantDir {
		t.Fatalf("%s, want %s", res.Dir, wantDir)
	}
	for _, f := range []string{"summary.md", "run.json", "prompt.md", "answer.md", "events.jsonl", "tools/01-list_directory/input.json", "tools/01-list_directory/output.txt"} {
		if !hasFile(res.Files, f) {
			t.Fatalf("%s missing: %v", f, res.Files)
		}
	}
	sum, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(res.Dir), "summary.md"))
	if !strings.Contains(string(sum), id) || !strings.Contains(string(sum), "Playground prompt") {
		t.Fatal(string(sum))
	}
	if _, err := os.Stat(filepath.Join(ws, ResultsDir, ".gitignore")); err != nil {
		t.Fatal("results must be git-ignored")
	}

	// continue the conversation: the AI's own conversation goes on, so the
	// new prompt is only the question (nothing is pasted again)
	id2, err := prompt(t, h, "And what are its risks?", PromptOptions{Parents: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	waitResults(t, h, id2)
	p2, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(runResultsDir(id2, time.Now())), "prompt.md"))
	if !strings.Contains(string(p2), "And what are its risks?") || strings.Contains(string(p2), "This continues earlier work") {
		t.Fatalf("a resumed conversation gets only the question:\n%s", p2)
	}
	// from several earlier runs there is no one conversation: the earlier
	// questions and answers go as text
	idM, err := prompt(t, h, "Compare them", PromptOptions{Parents: []string{id, id2}})
	if err != nil {
		t.Fatal(err)
	}
	waitResults(t, h, idM)
	pM, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(runResultsDir(idM, time.Now())), "prompt.md"))
	for _, want := range []string{"This continues earlier work", "What does the order service do?", "Results folder: " + res.Dir, "Now, continuing from the above:", "Compare them"} {
		if !strings.Contains(string(pM), want) {
			t.Fatalf("prompt.md missing %q:\n%s", want, pM)
		}
	}
	// a third turn sees the whole thread
	id3, _ := prompt(t, h, "Summarize both answers", PromptOptions{Parents: []string{id2}})
	waitResults(t, h, id3)
	th, err := h.Thread(id3)
	if err != nil || len(th) != 3 || th[0].ID != id || th[2].ID != id3 || th[2].Ask != "Summarize both answers" || th[0].Answer == "" {
		t.Fatalf("%v %+v", err, th)
	}
	recent, _ := h.RecentRuns(10)
	if len(recent) != 4 || recent[0].Kind != "playground" || recent[0].Results == "" {
		t.Fatalf("%+v", recent)
	}
	if _, err := prompt(t, h, "x", PromptOptions{Parents: []string{"nope"}}); err == nil {
		t.Fatal("unknown parent accepted")
	}
}

func TestFlowRunResults(t *testing.T) {
	h, ws := flowHub(t)
	st, err := h.RunFlow("notes", FlowRunOptions{Review: "none", Inputs: map[string]string{"version": "9.1"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })
	res := waitResults(t, h, st.ID)
	for _, f := range []string{"state.json", "steps/write/output.txt", "steps/echo/input.txt", "steps/echo/args.json", "steps/echo/output.txt", "outputs/01-md.md", "outputs/02-js.json", "tools.md"} {
		if !hasFile(res.Files, f) {
			t.Fatalf("%s missing: %v", f, res.Files)
		}
	}
	// the step state points at its saved files, and they hold what went in and came out
	fst, _ := h.FlowRun(st.ID)
	var echo FlowStepState
	for _, s := range fst.Steps {
		if s.ID == "echo" {
			echo = s
		}
	}
	if echo.InputFile != res.Dir+"/steps/echo/input.txt" || echo.OutputFile != res.Dir+"/steps/echo/output.txt" {
		t.Fatalf("%+v", echo)
	}
	if out, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(echo.OutputFile))); !strings.Contains(string(out), "fixed 9.1") {
		t.Fatalf("output file: %q", out)
	}
	b, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(res.Dir), "outputs", "01-md.md"))
	if !strings.Contains(string(b), "# 9.1") {
		t.Fatal(string(b))
	}
	// a prompt can continue from a flow run: its AI step's conversation goes
	// on (only the question is sent)
	id, _ := h.RunPrompt("Turn the notes into a tweet", PromptOptions{Parents: []string{st.ID}})
	waitResults(t, h, id)
	p, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(runResultsDir(id, time.Now())), "prompt.md"))
	if strings.TrimSpace(string(p)) == "" || strings.Contains(string(p), "Flow run: Release notes") {
		t.Fatal(string(p))
	}
	// with another agent that conversation cannot go on: the flow's outputs go as text
	h.mu.Lock()
	h.agentName = "mock2"
	h.mu.Unlock()
	m2 := agent.NewMock()
	m2.Delay = time.Millisecond
	h.Agents.Add(renamed{m2, "mock2"})
	id, _ = h.RunPrompt("Turn the notes into a haiku", PromptOptions{Parents: []string{st.ID}})
	waitResults(t, h, id)
	p, _ = os.ReadFile(filepath.Join(ws, filepath.FromSlash(runResultsDir(id, time.Now())), "prompt.md"))
	if !strings.Contains(string(p), "Flow run: Release notes") || !strings.Contains(string(p), "# 9.1") {
		t.Fatal(string(p))
	}
}

// prompt retries while the previous run is still finishing.
func prompt(t *testing.T, h *Hub, text string, o PromptOptions) (string, error) {
	t.Helper()
	for i := 0; i < 200; i++ {
		id, err := h.RunPrompt(text, o)
		if err == nil || !strings.Contains(err.Error(), "still running") {
			return id, err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return "", nil
}

func hasFile(list []string, f string) bool {
	for _, x := range list {
		if x == f {
			return true
		}
	}
	return false
}

// Regression: a playground prompt (no approval gate) must end, so the next
// prompt can start right away.
func TestPlaygroundRunsEnd(t *testing.T) {
	h, _ := newHub(t)
	id, err := h.RunPrompt("one", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := h.Store.GetRun(id); r.State == "done" && !r.Ended.IsZero() {
			if _, err := h.RunPrompt("two", PromptOptions{}); err != nil {
				t.Fatal("second prompt refused after the first ended:", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, _ := h.Store.GetRun(id)
	t.Fatalf("run never ended: %s", r.State)
}

// renamed is a driver under another name
type renamed struct {
	*agent.Mock
	name string
}

func (r renamed) Name() string { return r.name }
