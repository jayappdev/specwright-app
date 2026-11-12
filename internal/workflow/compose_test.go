package workflow

import (
	"strings"
	"testing"
)

func TestComposeRunContext(t *testing.T) {
	w := &Workflow{Steps: []Step{{ID: "specify", Cmd: "/speckit.specify", Input: "orders", Options: Options{Rules: true, Tools: true}}}}
	p := &Profile{Lang: "Java", Build: "Maven", Rules: []Rule{{ID: "R1", Text: "money is BigDecimal"}, {ID: "R2", Text: "no field injection"}}}
	tools := []ToolRef{{Name: "postgres-query", Desc: "Run SQL"}, {Name: "gen-dto", Desc: "DTOs"}, {Name: "builtin:read_file", Desc: "Read file: read files"}}

	// default: every rule, every project tool, no built-ins listed
	out := Compose(w, 0, p, nil, tools, "")
	if !strings.Contains(out, "R1: money") || !strings.Contains(out, "R2: no field") || !strings.Contains(out, "- postgres-query") || !strings.Contains(out, "- gen-dto") || strings.Contains(out, "read_file") {
		t.Fatalf("default:\n%s", out)
	}

	// chosen: one rule, one project tool, one built-in
	rules, ts := []string{"R2"}, []string{"gen-dto", "builtin:read_file"}
	w.Context = RunContext{Rules: &rules, Tools: &ts}
	out = Compose(w, 0, p, nil, tools, "")
	if strings.Contains(out, "R1") || !strings.Contains(out, "R2: no field") || strings.Contains(out, "postgres-query") || !strings.Contains(out, "- gen-dto") ||
		!strings.Contains(out, "## Built-in tools to use") || !strings.Contains(out, "- read_file: Read file") {
		t.Fatalf("chosen:\n%s", out)
	}

	// none
	none := []string{}
	w.Context = RunContext{Rules: &none, Tools: &none}
	out = Compose(w, 0, p, nil, tools, "")
	if strings.Contains(out, "## Rules") || strings.Contains(out, "## Tools") || strings.Contains(out, "Built-in") {
		t.Fatalf("none:\n%s", out)
	}
}
