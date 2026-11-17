package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wizUnderstand1 = `{"title": "Quality gate blockers", "requirements": [
  {"id": "R1", "text": "Run the quality gate", "status": "clear"},
  {"id": "R2", "text": "Report the blocking issues", "status": "not-clear", "unclear": "grouped how, and where does the report go?"}],
 "questions": [
  {"id": "Q1", "req": "R2", "topic": "output", "text": "Where should the report go?", "kind": "choice", "options": ["Show it", "Save to a file"]},
  {"id": "Q2", "topic": "secret", "text": "Does the gate need a token?", "kind": "yesno"}],
 "inputs": [], "outputs": [{"name": "report", "type": "md"}], "constraints": [], "secrets": []}`

const wizUnderstand2 = "```json\n" + `{"requirements": [
  {"id": "R1", "text": "Run the quality gate", "status": "clear"},
  {"id": "R2", "text": "Show the blocking issues grouped by file", "status": "clear"}],
 "questions": [],
 "inputs": [{"name": "gate_token", "type": "secret", "required": true}],
 "outputs": [{"name": "report", "type": "json", "destination": "show"}],
 "constraints": ["Only BLOCKER issues"],
 "secrets": [{"name": "gate_token", "usedBy": "gate-scan"}]}` + "\n```"

const wizPlanReply = `{"plan": "## Goal\nShow blockers.\n## Secrets\ngate_token from the Keychain.",
 "tools": [{"name": "gate-scan", "why": "it runs the gate"}, {"name": "made-up-tool", "why": "?"}],
 "inputs": [{"name": "gate_token", "type": "secret", "required": true}],
 "outputs": [{"name": "report", "type": "json"}],
 "secrets": [{"name": "gate_token", "usedBy": "gate-scan", "source": "keychain", "why": "desktop"}],
 "steps": ["scan", "filter", "show"], "risks": ["no blockers: empty report"]}`

// one draft that checks out, with a secret input and a working trial
const wizDraftReply = `{"options": [{"title": "Blockers by file", "summary": "Filters the gate's blockers", "why": "tool + filter", "flow": {"name": "Blockers",
  "inputs": [{"name": "gate_token", "label": "Gate token", "type": "secret"}],
  "nodes": [{"id": "start", "type": "start"}, {"id": "scan", "type": "tool", "tool": "gate-scan"},
    {"id": "keep", "type": "filter", "cond": {"rules": [{"field": "severity", "op": "eq", "value": "BLOCKER"}]}},
    {"id": "out", "type": "output", "format": "json", "actions": [{"type": "show"}]}],
  "edges": [{"from": "start", "to": "scan"}, {"from": "scan", "to": "keep"}, {"from": "keep", "to": "out"}]},
  "test": {"mocks": {"scan": {"issues": [{"severity": "BLOCKER", "file": "pay.go"}, {"severity": "MINOR", "file": "x.go"}]}},
           "expect": [{"step": "out", "contains": ["pay.go"]}]}}]}`

func TestWizardFromRequestToFlow(t *testing.T) {
	h, s := designHub(t)
	s.replies = []string{wizUnderstand1}
	ws, err := h.WizardStart("Tell me the blocking issues of the quality gate", "")
	if err != nil {
		t.Fatal(err)
	}
	// requirements: one NOT-CLEAR, with questions about it
	if ws.Phase != "clarify" || ws.Title != "Quality gate blockers" || len(ws.Requirements) != 2 || ws.Requirements[1].Status != ReqNotClear {
		t.Fatalf("%+v", ws)
	}
	if len(ws.Questions) != 2 || ws.Questions[1].Kind != "yesno" || ws.Questions[1].Options[0] != "Yes" {
		t.Fatalf("questions %+v", ws.Questions)
	}
	if !strings.Contains(s.prompts[0], "gate-scan") || !strings.Contains(s.prompts[0], `"not-clear"`) {
		t.Fatal("the AI sees the tools and how to flag unclear requirements")
	}
	// answers go back to the AI
	s.replies = []string{wizUnderstand2}
	ws, err = h.WizardClarify(ws.ID, WizardEdit{Answers: map[string]string{"Q1": "Show it", "Q2": "Yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.prompts[1], "Answer: Show it") || !strings.Contains(s.prompts[1], "Answer: Yes") {
		t.Fatal("answers reach the AI:", s.prompts[1])
	}
	if ws.unclear() != 0 || len(ws.Spec.Secrets) != 1 || ws.Spec.Constraints[0] != "Only BLOCKER issues" || len(ws.Questions) != 2 {
		t.Fatalf("%+v", ws)
	}
	// the plan: unknown tools dropped (with a note), secret sources kept
	s.replies = []string{wizPlanReply}
	ws, err = h.WizardPlan(ws.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Phase != "plan" || !strings.Contains(ws.Plan, "## Secrets") || len(ws.PlanInfo.Tools) != 1 || ws.Spec.Secrets[0].Source != "keychain" || len(ws.Notes) != 1 {
		t.Fatalf("%+v %+v", ws.PlanInfo, ws.Notes)
	}
	if !strings.Contains(s.prompts[2], "## Secrets") || !strings.Contains(s.prompts[2], "TOOLS (name:") {
		t.Fatal("the plan prompt asks how secrets are handled and gives the tool contracts")
	}
	// the user edits the plan by hand
	plan := ws.Plan + "\n## Note\nKeep it short."
	ws, err = h.WizardUpdate(ws.ID, WizardEdit{Plan: &plan})
	if err != nil || !strings.Contains(ws.Plan, "Keep it short") {
		t.Fatal(err)
	}
	// drafts from the plan: tried with sample data; the secret takes the plan's source
	s.replies = []string{wizDraftReply}
	ws, err = h.WizardDraft(ws.ID, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.prompts[3], "Follow this plan") || !strings.Contains(s.prompts[3], "Keep it short") {
		t.Fatal("drafts are made from the plan the user edited")
	}
	d := ws.Drafts[0]
	if ws.Phase != "drafts" || d.ID != "d1" || d.Errors != 0 || d.Trial == nil || !d.Trial.OK || d.Flow.Inputs[0].Source != "keychain" {
		t.Fatalf("%+v", d)
	}
	// a revision is a new draft; the old one stays
	s.replies = []string{wizDraftReply}
	ws, err = h.WizardRevise(ws.ID, "d1", "name the output Blockers")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Drafts) != 2 || ws.Drafts[1].Parent != "d1" || ws.Drafts[1].Version != 2 || !strings.Contains(s.prompts[4], "CURRENT FLOW") {
		t.Fatalf("%+v", ws.Drafts)
	}
	// every AI call is in the log, with what was asked
	ai := 0
	for _, e := range ws.Log {
		if e.Prompt != "" {
			ai++
		}
	}
	if ai != 5 || ws.Usage.InputTokens == 0 {
		t.Fatalf("log %d %+v", ai, ws.Usage)
	}
	// apply: a flow in the Flow designer, with the requirements as context
	st, err := h.WizardApply(ws.ID, "d2", "Gate blockers")
	if err != nil {
		t.Fatal(err)
	}
	if st.Flow.Name != "Gate blockers" || !strings.Contains(st.Flow.Context.Text, "grouped by file") || !strings.Contains(st.Flow.Designed, "flow wizard") {
		t.Fatalf("%+v", st.Flow)
	}
	ws, _ = h.WizardLoad(ws.ID)
	if ws.Phase != "applied" || ws.Drafts[1].Applied != st.Flow.ID {
		t.Fatalf("%+v", ws)
	}
	// sessions are listed and saved in the project
	list, _ := h.WizardList()
	if len(list) != 1 || list[0].Drafts != 2 {
		t.Fatalf("%+v", list)
	}
	if _, err := os.Stat(filepath.Join(h.Root(), ".codegen", "flow-wizard", ws.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestWizardRunsADraftForReal(t *testing.T) {
	h, s := designHub(t)
	s.replies = []string{wizUnderstand2}
	ws, err := h.WizardStart("blockers", "")
	if err != nil {
		t.Fatal(err)
	}
	// skip to drafts: straight from the request
	s.replies = []string{`{"options": [{"title": "Echo", "summary": "s", "flow": {"name": "Echo", "nodes": [
	  {"id": "start", "type": "start"}, {"id": "e", "type": "tool", "tool": "echo-arg", "args": {"v": "hi"}},
	  {"id": "out", "type": "output", "format": "txt", "actions": [{"type": "show"}]}],
	  "edges": [{"from": "start", "to": "e"}, {"from": "e", "to": "out"}]}, "test": {"mocks": {"e": "hi"}}}]}`}
	ws, err = h.WizardDraft(ws.ID, 1, "")
	if err != nil || len(ws.Drafts) != 1 {
		t.Fatal(err)
	}
	if strings.Contains(s.prompts[1], "Follow this plan") {
		t.Fatal("no plan yet: drafts come from the request and requirements")
	}
	st, err := h.WizardRunDraft(ws.ID, "d1", FlowRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(x FlowRunState) bool { return x.Status == "done" || x.Status == "failed" })
	if st.Status != "done" || st.Trial == nil || st.Trial.Draft != ws.ID+"/d1" || !strings.Contains(st.Outputs[0].Content, "hi") {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Outputs)
	}
	// a draft run is not a flow
	if fs, _ := h.Flows(); len(fs) != 1 { // only flowHub's own "notes"
		t.Fatalf("%+v", fs)
	}
	ws, _ = h.WizardLoad(ws.ID)
	if len(ws.Drafts[0].Runs) != 1 {
		t.Fatalf("%+v", ws.Drafts[0])
	}
}
