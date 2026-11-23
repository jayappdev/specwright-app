package core

import (
	"fmt"
	"strings"
	"testing"

	"codegen/internal/flow"
	"codegen/internal/store"
)

func TestEditLesson(t *testing.T) {
	got := editLesson("# Notes\n\nFixed the login bug\nTODO: tests\n", "# Notes\n\nFixed the login bug (#42)\n")
	if !strings.Contains(got, `Removed: "Fixed the login bug"; "TODO: tests".`) || !strings.Contains(got, `Added: "Fixed the login bug (#42)".`) {
		t.Fatal(got)
	}
	if editLesson("a\nb", "a\n\nb\n") != "" {
		t.Fatal("blank lines only: nothing to learn")
	}
	var a, b []string
	for i := 0; i < 20; i++ {
		a, b = append(a, fmt.Sprint("old ", i)), append(b, fmt.Sprint("new ", i))
	}
	if editLesson(strings.Join(a, "\n"), strings.Join(b, "\n")) != "" {
		t.Fatal("a rewrite is not a lesson")
	}
	long := strings.Repeat("x", 5000)
	if got := editLesson("keep\n"+long, "keep\n"+long+"y"); len(got) > 620 {
		t.Fatalf("an edit lesson stays short: %d", len(got))
	}
}

// the lessons section stays small, whatever is kept; people's lessons come first
func TestLessonsBudget(t *testing.T) {
	h, ws := flowHub(t)
	for i := 0; i < 20; i++ {
		_, _ = h.Store.AddLesson(store.Lesson{Workspace: ws, Flow: "notes", Step: "write", Source: "judge", Text: fmt.Sprintf("judge %d %s", i, strings.Repeat("z", 900))})
	}
	_, _ = h.AddFlowLesson("notes", "write", "Mention the ticket numbers")
	note := h.lessonsNote("notes", "write")
	if len(note) > lessonsBudget+300 {
		t.Fatalf("the section is %d characters", len(note))
	}
	if i := strings.Index(note, "Mention the ticket numbers"); i < 0 || i > strings.Index(note, "judge ") {
		t.Fatalf("people's lessons first:\n%s", note)
	}
	// learned ones are pruned to the newest few per step
	fr := &flowRun{flow: &flow.Flow{ID: "notes"}}
	h.pruneLearned(fr, "write", "judge")
	ls, _ := h.FlowLessons("notes")
	n := 0
	for _, l := range ls {
		if l.Source == "judge" {
			n++
		}
	}
	if n != keepLearned || len(ls) != keepLearned+1 {
		t.Fatalf("%d judge lessons, %d in all", n, len(ls))
	}
}

// the AI judge's first "not yet" becomes one lesson per run
func TestJudgeLesson(t *testing.T) {
	h, _ := flowHub(t)
	improveTool(t, h, "need-good", `sh -c 'test "$1" = good && echo all good' sh {{v}}`)
	improveFlow(t, h, []flow.Node{
		{ID: "q", Type: "tool", Tool: "need-good", Args: map[string]string{"v": "bad"}, X: 200},
		{ID: "fix", Type: "improve", Pair: "before", Goal: "it says good", Approve: "none", X: 400},
	}, []flow.Edge{{From: "start", To: "q"}, {From: "q", To: "fix"}})
	for i := 0; i < 2; i++ {
		if st := runImp(t, h, FlowRunOptions{Review: "none"}); st.Status != "done" {
			t.Fatal(st.Error)
		}
	}
	ls, _ := h.FlowLessons("imp")
	if len(ls) != 1 || ls[0].Source != "judge" || ls[0].Step != "fix" || !strings.HasPrefix(ls[0].Text, "An earlier run's first try was not good enough: ") {
		t.Fatalf("one lesson (the same reason twice is one): %+v", ls)
	}
	f, _ := flow.Load(h.Root(), "imp")
	if p := h.lessonsNote(f.ID, "fix"); !strings.Contains(p, "first try was not good enough") {
		t.Fatal(p)
	}
}

// edit-then-approve in a review: the small correction is a lesson
func TestEditLessonFromReview(t *testing.T) {
	h, _ := flowHub(t)
	st, _ := h.RunFlow("notes", FlowRunOptions{Inputs: map[string]string{"version": "4.0"}})
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	edited := st.Review.Output + "\nSee the changelog for details."
	if err := h.DecideFlow(st.ID, FlowDecision{Action: "edit", Output: edited}); err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "review" })
	_ = h.DecideFlow(st.ID, FlowDecision{Action: "continue"})
	waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" })
	ls, _ := h.FlowLessons("notes")
	if len(ls) != 1 || ls[0].Source != "edit" || !strings.Contains(ls[0].Text, `Added: "See the changelog for details."`) || strings.Contains(ls[0].Text, "Removed") {
		t.Fatalf("%+v", ls)
	}
}
