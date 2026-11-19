package core

import (
	"strings"
	"testing"

	"codegen/internal/extension"
)

func TestFixRequestCarriesInstructionsAndHistory(t *testing.T) {
	r := GenerateRequest{
		Change:  "read the date as YYYY-MM-DD",
		Failure: &extension.TestResult{Checks: []extension.Check{{Label: "Exit code 0"}}},
		History: []GenTurn{{Revision: 2, Ask: "quote the path", Why: "quoted argv[2]", Test: "failed: Exit code 0"}},
	}
	got := fixRequest(r)
	for _, want := range []string{"The developer's instructions", "YYYY-MM-DD", "failed check: Exit code 0", "What was tried before", "revision 2", "quoted argv[2]"} {
		if !strings.Contains(got, want) {
			t.Errorf("fix request lacks %q:\n%s", want, got)
		}
	}
	if historyNote(nil) != "" {
		t.Error("no history should add nothing")
	}
	var many []GenTurn
	for i := 1; i <= 20; i++ {
		many = append(many, GenTurn{Revision: i})
	}
	if n := historyNote(many); strings.Contains(n, "revision 12\n") || !strings.Contains(n, "revision 20") {
		t.Errorf("history should keep the last eight:\n%s", n)
	}
}

func TestToolFromReply(t *testing.T) {
	h := &Hub{}
	draft := extension.Extension{Name: "find-todos", Desc: "Finds TODOs", Lang: "Node.js", Script: "old", Revision: 3, Scope: "project", Prompt: "find todos", TestPrompt: "list todos"}
	answer := "I changed the regex.\n\n```json\n{\"script\": \"new\", \"params\": [{\"name\": \"path\", \"type\": \"string\", \"default\": 5}], \"why\": \"the regex missed lowercase\"}\n```"
	tr, err := h.ToolFromReply(answer, draft)
	if err != nil {
		t.Fatal(err)
	}
	d := tr.Tool
	if d.Name != "find-todos" || d.Script != "new" || d.Revision != 4 || d.Scope != "project" || d.TestPrompt != "list todos" || d.Params[0].Default != "5" {
		t.Errorf("tool = %+v", d)
	}
	if tr.Why != "the regex missed lowercase" {
		t.Errorf("why = %q", tr.Why)
	}
	if _, err := h.ToolFromReply("No JSON here, just talk.", draft); err == nil {
		t.Error("an answer without a tool should be refused")
	}
	if _, err := h.ToolFromReply(`{"answer": 42}`, draft); err == nil {
		t.Error("JSON that is not a tool should be refused")
	}
}
