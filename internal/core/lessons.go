package core

// Lessons from reviews (store/learn.go): when a person reviewing a flow
// step asks for changes — Refine feedback, or "another round" feedback in
// an Improve step — the request is kept as a lesson for that step. Later
// runs of the step get the step's lessons in their prompt, so what a
// reviewer pointed out once is not repeated. People see, edit, turn off,
// add and delete them in the Flow Designer (the step's Lessons).
//
// Refine feedback saved into the flow ("save to the flow") is not kept as a
// lesson as well: the prompt already says it.
//
// Two kinds are learned without anyone typing them, and kept small:
//
//   - edit: a reviewer changed the result by hand and accepted it. The
//     lesson is what they removed and added (a few short lines). A big
//     rewrite says nothing specific, so it is not kept.
//   - judge: an Improve step's AI judge found the first try not good
//     enough. Its reason is kept, once per run, so the next run can avoid
//     the problem from the start.
//
// The prompt never grows much: at most maxLessons lessons, each cut to
// lessonChars, the section at most lessonsBudget characters; lessons from
// people first, the AI judge's last. Each step keeps at most keepLearned
// lessons of each learned kind (the oldest go).

import (
	"fmt"
	"sort"
	"strings"

	"codegen/internal/store"
)

// how much of a prompt lessons may take, and how many learned ones a step keeps
const (
	maxLessons    = 8    // lessons in a prompt
	lessonChars   = 300  // each, in the prompt
	lessonsBudget = 1500 // the whole section
	keepLearned   = 3    // per step, of each learned kind (edit, judge)
	// an edit is a lesson only when it is small: at most this many changed
	// lines, out of results of at most editMaxLines lines
	editMaxChanged = 12
	editMaxLines   = 400
)

// learnFromReview keeps a reviewer's request as a lesson for the step.
func (h *Hub) learnFromReview(fr *flowRun, step, text, source string) {
	if strings.TrimSpace(text) == "" || fr.flow == nil || fr.flow.ID == "" {
		return
	}
	if _, err := h.Store.AddLesson(store.Lesson{Workspace: h.Root(), Flow: fr.flow.ID, Step: step, Text: text, Source: source, RunID: fr.st.ID}); err == nil {
		h.flowPublish(fr, "flow.lesson", "Kept for later runs of this step: "+clipText(text, 160), map[string]any{"_step": step, "text": text})
	}
}

// learnFromEdit keeps what a reviewer changed by hand, when the change is small.
func (h *Hub) learnFromEdit(fr *flowRun, step, before, after string) {
	if s := editLesson(before, after); s != "" {
		h.learnFromReview(fr, step, s, "edit")
		h.pruneLearned(fr, step, "edit")
	}
}

// learnFromJudge keeps the AI judge's reason for finding the first try not
// good enough (call it once per run of the step).
func (h *Hub) learnFromJudge(fr *flowRun, step, reason string) {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) < 12 { // "not done": nothing to learn
		return
	}
	h.learnFromReview(fr, step, "An earlier run's first try was not good enough: "+clipText(reason, 280), "judge")
	h.pruneLearned(fr, step, "judge")
}

func (h *Hub) pruneLearned(fr *flowRun, step, source string) {
	if fr.flow != nil {
		_ = h.Store.PruneLessons(h.Root(), fr.flow.ID, step, source, keepLearned)
	}
}

// editLesson says in a few short lines what changed between the result
// and the reviewer's version ("" when nothing did, or too much did).
func editLesson(before, after string) string {
	a, b := nonBlankLines(before), nonBlankLines(after)
	if len(a) > editMaxLines || len(b) > editMaxLines {
		return ""
	}
	removed, added := lineDiff(a, b)
	if len(removed)+len(added) == 0 || len(removed)+len(added) > editMaxChanged {
		return ""
	}
	quote := func(ls []string) string {
		var q []string
		for i, l := range ls {
			if i == 3 {
				q = append(q, fmt.Sprintf("(and %d more)", len(ls)-3))
				break
			}
			q = append(q, fmt.Sprintf("%q", clipText(l, 120)))
		}
		return strings.Join(q, "; ")
	}
	s := "A reviewer corrected this step's result by hand."
	if len(removed) > 0 {
		s += " Removed: " + quote(removed) + "."
	}
	if len(added) > 0 {
		s += " Added: " + quote(added) + "."
	}
	return clipText(s, 600)
}

func nonBlankLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// lineDiff: the lines only in a (removed) and only in b (added), in order,
// by the longest common subsequence.
func lineDiff(a, b []string) (removed, added []string) {
	n, m := len(a), len(b)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else {
				l[i][j] = max(l[i+1][j], l[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			i, j = i+1, j+1
		case l[i+1][j] >= l[i][j+1]:
			removed, i = append(removed, a[i]), i+1
		default:
			added, j = append(added, b[j]), j+1
		}
	}
	return append(removed, a[i:]...), append(added, b[j:]...)
}

// lessonRank: lessons from people before learned ones, the AI judge's last
// (newest first within each).
var lessonRank = map[string]int{"manual": 0, "refine": 0, "improve": 0, "edit": 1, "judge": 2}

// lessonsNote is the prompt part with a step's lessons ("" when it has
// none), within the budget above.
func (h *Hub) lessonsNote(flowID, step string) string {
	if flowID == "" || step == "" {
		return ""
	}
	ls, err := h.Store.Lessons(h.Root(), flowID, step)
	if err != nil {
		return ""
	}
	sort.SliceStable(ls, func(i, j int) bool { return lessonRank[ls[i].Source] < lessonRank[ls[j].Source] })
	var b strings.Builder
	var used []int64
	for _, l := range ls {
		if l.Off || len(used) == maxLessons {
			continue
		}
		line := "- " + clipText(l.Text, lessonChars) + "\n"
		if b.Len()+len(line) > lessonsBudget {
			break
		}
		b.WriteString(line)
		used = append(used, l.ID)
	}
	if len(used) == 0 {
		return ""
	}
	h.Store.LessonsUsed(used)
	return "\n\n## Lessons from earlier reviews of this step\nWhat reviewers (and the AI checking this step) pointed out in earlier runs. Follow them unless this task says otherwise:\n" + b.String()
}

// FlowLessons lists a flow's lessons (all its steps), newest first.
func (h *Hub) FlowLessons(flowID string) ([]store.Lesson, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	return h.Store.Lessons(root, flowID, "")
}

// AddFlowLesson adds a lesson by hand.
func (h *Hub) AddFlowLesson(flowID, step, text string) (store.Lesson, error) {
	root, err := h.need()
	if err != nil {
		return store.Lesson{}, err
	}
	if flowID == "" || step == "" {
		return store.Lesson{}, fmt.Errorf("a lesson belongs to a flow's step")
	}
	return h.Store.AddLesson(store.Lesson{Workspace: root, Flow: flowID, Step: step, Text: text, Source: "manual"})
}

// UpdateLesson changes a lesson's text, or turns it off (kept, not used) or on.
func (h *Hub) UpdateLesson(id int64, text string, off bool) error {
	return h.Store.UpdateLesson(id, text, off)
}

// DeleteLesson forgets a lesson.
func (h *Hub) DeleteLesson(id int64) error { return h.Store.DeleteLesson(id) }
