package flow

import (
	"slices"
	"strings"
	"testing"
)

// a Chatflow: Inputs → load → Chat → (query → chart) and (fetch)
func sampleChatflow() *Flow {
	f := New("sales-qa", "Sales Q&A")
	f.Vars = []Var{{Name: "REGION", Value: "eu"}}
	f.Secrets = []SecretVar{{Name: "DB_SALES"}}
	f.Nodes = append(f.Nodes,
		Node{ID: "load", Type: "transform", Op: "template", Template: "rows"},
		Node{ID: "chat", Type: "chat", Instructions: "Answer sales questions.", Starters: []string{"Which region grew most?"}},
		Node{ID: "query", Type: "tool", Tool: "duckdb_data_query", Title: "Query sales", Note: "SQL over the sales data"},
		Node{ID: "chart", Type: "tool", Tool: "builtin:chart"},
		Node{ID: "fetch", Type: "tool", Tool: "builtin:fetch_webpage", Title: "Fetch a page"})
	f.Edges = []Edge{{From: "start", To: "load"}, {From: "load", To: "chat"}, {From: "chat", To: "query"}, {From: "query", To: "chart"}, {From: "chat", To: "fetch"}}
	return f
}

func messages(is []Issue, level string) string {
	var out []string
	for _, i := range is {
		if i.Level == level {
			out = append(out, i.Node+": "+i.Message)
		}
	}
	return strings.Join(out, "\n")
}

func TestChatflowSetupAndTools(t *testing.T) {
	f := sampleChatflow()
	if !f.IsChatflow() || f.ChatNode().ID != "chat" {
		t.Fatal("a flow with a Chat step is a Chatflow")
	}
	if s := f.ChatSetup(); !slices.Equal(s, []string{"start", "load"}) {
		t.Fatalf("setup %v", s)
	}
	tools := f.ChatTools()
	if len(tools) != 2 || tools[0].Title != "Query sales" || tools[0].Desc != "SQL over the sales data" || !slices.Equal(tools[0].Steps, []string{"query", "chart"}) ||
		tools[1].ID != "fetch" || !slices.Equal(tools[1].Steps, []string{"fetch"}) {
		t.Fatalf("%+v", tools)
	}
	// no output needed; a clean Chatflow has no errors
	if e := messages(Validate(f, nil), "error"); strings.Contains(e, "chat") || strings.Contains(e, "Variable") {
		t.Fatal(e)
	}
	if w := messages(Validate(f, nil), "warning"); strings.Contains(w, "Add an output") {
		t.Fatal(w)
	}
}

func TestChatflowChecks(t *testing.T) {
	f := sampleChatflow()
	f.Vars = append(f.Vars, Var{Name: "bad name"}, Var{Name: "DB_SALES"})
	f.Nodes = append(f.Nodes, Node{ID: "chat2", Type: "chat"}, Node{ID: "lonely", Type: "transform", Op: "uppercase"})
	f.Edges = append(f.Edges, Edge{From: "chart", To: "chat2"})
	f.Triggers = []Trigger{{Kind: "schedule", Name: "Nightly"}}
	is := Validate(f, nil)
	errs, warns := messages(is, "error"), messages(is, "warning")
	for _, want := range []string{"Variable \"bad name\"", "Two variables or secrets are called DB_SALES", "A Chatflow has one Chat step", "A Chat step cannot come after the Chat step"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("missing %q in\n%s", want, errs)
		}
	}
	for _, want := range []string{"lonely is not connected to the Chat step", "its triggers never start it"} {
		if !strings.Contains(warns, want) {
			t.Fatalf("missing %q in\n%s", want, warns)
		}
	}
	// a Chat step not after the Inputs step
	g := New("x", "X")
	g.Nodes = append(g.Nodes, Node{ID: "chat", Type: "chat"})
	if e := messages(Validate(g, nil), "error"); !strings.Contains(e, "Connect the Chat step after the Inputs step") {
		t.Fatal(e)
	}
}

func TestSummaryMarksChatflows(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, sampleChatflow()); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, New("plain", "Plain")); err != nil {
		t.Fatal(err)
	}
	l, err := List(root)
	if err != nil || len(l) != 2 {
		t.Fatalf("%v %v", l, err)
	}
	for _, s := range l {
		if s.Chat != (s.ID == "sales-qa") {
			t.Fatalf("%+v", s)
		}
	}
}
