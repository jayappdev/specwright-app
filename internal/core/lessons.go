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

import (
	"fmt"
	"strings"

	"codegen/internal/store"
)

// maxLessons is how many of a step's lessons go into its prompt (the newest).
const maxLessons = 8

// learnFromReview keeps a reviewer's request as a lesson for the step.
func (h *Hub) learnFromReview(fr *flowRun, step, text, source string) {
	if strings.TrimSpace(text) == "" || fr.flow == nil || fr.flow.ID == "" {
		return
	}
	if _, err := h.Store.AddLesson(store.Lesson{Workspace: h.Root(), Flow: fr.flow.ID, Step: step, Text: text, Source: source, RunID: fr.st.ID}); err == nil {
		h.flowPublish(fr, "flow.lesson", "Kept for later runs of this step: "+clipText(text, 160), map[string]any{"_step": step, "text": text})
	}
}

// lessonsNote is the prompt part with a step's lessons ("" when it has none).
func (h *Hub) lessonsNote(flowID, step string) string {
	if flowID == "" || step == "" {
		return ""
	}
	ls, err := h.Store.Lessons(h.Root(), flowID, step)
	if err != nil {
		return ""
	}
	var b strings.Builder
	var used []int64
	for _, l := range ls {
		if l.Off || len(used) == maxLessons {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", strings.ReplaceAll(strings.TrimSpace(l.Text), "\n", " "))
		used = append(used, l.ID)
	}
	if len(used) == 0 {
		return ""
	}
	h.Store.LessonsUsed(used)
	return "\n\n## Lessons from earlier reviews of this step\nPeople who reviewed earlier runs of this step asked for these. Follow them unless this task says otherwise:\n" + b.String()
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
