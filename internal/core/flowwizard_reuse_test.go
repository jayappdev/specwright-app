package core

import (
	"strings"
	"testing"
)

const wizPlanReuse = `{"plan": "## Goal\nPull Jira issues.\n## Reusable components\n- fetch: project, status, component",
 "tools": [],
 "inputs": [{"name": "project", "type": "text", "required": true}, {"name": "status", "type": "text"}, {"name": "component", "type": "text"}],
 "outputs": [{"name": "report", "type": "md"}], "secrets": [],
 "steps": ["fetch", "show"],
 "components": [{"name": "Fetch issues", "kind": "new tool", "tool": "pull-jira-issues", "inputs": ["project", "status", "component"], "why": "one general fetch"}],
 "newTools": [
   {"name": "pull-jira-issues", "desc": "Lists Jira issues of a project, filtered by status and component", "params": [
     {"name": "project", "type": "string", "required": true, "desc": "project key", "default": "ABC"},
     {"name": "status", "type": "string", "default": "Done", "enum": ["To Do", "In Progress", "Done"]},
     {"name": "component", "type": "string", "default": "abc"},
     {"name": "max", "type": "integer", "default": 50}], "output": {"type": "array"}},
   {"name": "echo-arg", "desc": "exists already"}],
 "risks": []}`

// The plan breaks the flow into reusable components; a tool the project
// lacks is planned as a general one, driven by parameters (the request's
// values are only defaults).
func TestWizardPlansReusableComponents(t *testing.T) {
	h, s := designHub(t)
	s.replies = []string{wizUnderstand2}
	ws, err := h.WizardStart("Pull Jiras of project ABC with status Done and component abc", "")
	if err != nil {
		t.Fatal(err)
	}
	s.replies = []string{wizPlanReuse}
	ws, err = h.WizardPlan(ws.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	p := s.prompts[len(s.prompts)-1]
	for _, want := range []string{"## Reusable components", "never hard-coded", "NEW TOOL to create as a general, parameterized one", `"newTools"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("the plan prompt lacks %q", want)
		}
	}
	pi := ws.PlanInfo
	if len(pi.Components) != 1 || strings.Join(pi.Components[0].Inputs, ",") != "project,status,component" {
		t.Fatalf("%+v", pi.Components)
	}
	if len(pi.NewTools) != 1 || pi.NewTools[0].Name != "pull-jira-issues" || len(pi.NewTools[0].Params) != 4 {
		t.Fatalf("an existing tool is not planned as new: %+v", pi.NewTools)
	}
	nt := pi.NewTools[0]
	if nt.Params[1].Default != "Done" || len(nt.Params[1].Enum) != 3 || nt.Params[3].Type != "integer" || nt.Params[3].Default != "50" || nt.Output == "" {
		t.Fatalf("%+v", nt)
	}
	if !strings.Contains(strings.Join(ws.Notes, " "), "pull-jira-issues") {
		t.Fatalf("notes %v", ws.Notes)
	}
	// drafts and generated tools are told the same
	if !strings.Contains(designRules, "REUSABLE") || !strings.Contains(genInstructions, "MAKE THE TOOL REUSABLE") {
		t.Fatal("designer and tool generation rules")
	}
}
