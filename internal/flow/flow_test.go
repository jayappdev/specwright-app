package flow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *Flow {
	f := New("jira-to-spec", "Jira ticket to spec")
	f.Inputs = []Input{{Name: "ticket", Label: "Ticket key", Type: "text", Required: true}, {Name: "notes", Label: "Notes", Type: "multiline"}}
	f.Context = Context{Text: "We build Java 21 services.", Files: []string{"docs/arch.md"}}
	f.Skills = []Skill{{Path: ".codegen/skills/project/orders/SKILL.md", Name: "orders"}}
	f.Nodes = append(f.Nodes,
		Node{ID: "jira", Type: "tool", Title: "Fetch ticket", X: 300, Y: 80, Tool: "jira-get", Args: map[string]string{"key": "{{inputs.ticket}}"}},
		Node{ID: "todos", Type: "tool", Title: "Find TODOs", X: 300, Y: 220, Tool: "find-todos", Args: map[string]string{"path": "src"}},
		Node{ID: "merge", Type: "merge", Title: "Combine", X: 560, Y: 150, Mode: "json-object"},
		Node{ID: "pick", Type: "transform", Title: "Summary", X: 760, Y: 150, Op: "json-path", Expr: "Fetch ticket.summary"},
		Node{ID: "spec", Type: "ai", Title: "Write spec", X: 960, Y: 150, Prompt: "Write a spec for {{steps.jira.json.summary}} using {{steps.merge}} and {{context}}"},
		Node{ID: "md", Type: "output", Title: "Spec (md)", X: 1160, Y: 80, Format: "md", Template: "{{steps.spec}}", Actions: []Action{{Type: "save", Path: "specs/{{inputs.ticket}}.md"}}},
		Node{ID: "html", Type: "output", Title: "Spec (html)", X: 1160, Y: 220, Format: "html", Actions: []Action{{Type: "show"}}},
	)
	f.Edges = []Edge{{From: "start", To: "jira"}, {From: "start", To: "todos"}, {From: "jira", To: "merge"}, {From: "todos", To: "merge"}, {From: "merge", To: "pick"}, {From: "pick", To: "spec"}, {From: "spec", To: "md"}, {From: "spec", To: "html"}}
	return f
}

var tools = []ToolInfo{{Name: "jira-get", Params: []ToolParam{{Name: "key", Required: true}}}, {Name: "find-todos", Params: []ToolParam{{Name: "path"}}}}

func errorsOf(is []Issue) []string {
	var out []string
	for _, i := range is {
		if i.Level == "error" {
			out = append(out, i.Node+": "+i.Message)
		}
	}
	return out
}

func TestValidate(t *testing.T) {
	f := sample()
	if errs := errorsOf(Validate(f, tools)); len(errs) != 0 {
		t.Fatal(errs)
	}
	order, err := f.Order()
	if err != nil || order[0] != "start" || order[len(order)-1] != "html" {
		t.Fatal(order, err)
	}
	// problems are found
	bad := sample()
	bad.Node("jira").Args["key"] = ""
	bad.Node("spec").Prompt = "Use {{steps.html}} and {{inputs.nope}}"
	bad.Node("pick").Op = "regex-extract"
	bad.Node("pick").Expr = "(["
	bad.Node("md").Actions[0].Path = "../outside.md"
	bad.Edges = append(bad.Edges, Edge{From: "spec", To: "jira"})
	bad.Nodes = append(bad.Nodes, Node{ID: "x", Type: "tool", Tool: "missing-tool"})
	errs := strings.Join(errorsOf(Validate(bad, tools)), "\n")
	for _, want := range []string{"required parameter \"key\"", "does not run before this step", "no input called \"nope\"", "pattern is not valid", "inside the project", "loop", "\"missing-tool\" is not available"} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in\n%s", want, errs)
		}
	}
}

func TestStoreImportExport(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "docs", "arch.md"), []byte("# Architecture\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, ".codegen", "skills", "project", "orders"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".codegen", "skills", "project", "orders", "SKILL.md"), []byte("Orders skill\n"), 0o644)
	f := sample()
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	list, _ := List(root)
	if len(list) != 1 || list[0].Steps != 5 || list[0].Outputs != 2 {
		t.Fatalf("%+v", list)
	}
	data, err := Export(root, f)
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	_ = json.Unmarshal(data, &b)
	if len(b.Files) != 2 || b.Format != "specwright-flow" {
		t.Fatalf("%+v", b.Files)
	}
	// into another project: files land under assets and paths are rewritten
	other := t.TempDir()
	g, notes, err := Import(other, data)
	if err != nil || g.ID != "jira-to-spec" || !strings.HasPrefix(g.Skills[0].Path, AssetsDir+"/jira-to-spec/") {
		t.Fatal(err, g, notes)
	}
	if b, _ := os.ReadFile(filepath.Join(other, filepath.FromSlash(g.Context.Files[0]))); string(b) != "# Architecture\n" {
		t.Fatal("context file not imported")
	}
	// importing again gets a new id; into the same project paths stay as they are
	g2, _, _ := Import(other, data)
	if g2.ID != "jira-to-spec-2" {
		t.Fatal(g2.ID)
	}
	g3, _, _ := Import(root, data)
	if g3.Skills[0].Path != ".codegen/skills/project/orders/SKILL.md" {
		t.Fatal(g3.Skills[0].Path)
	}
	if _, _, err := Import(root, []byte(`{"hello":1}`)); err == nil {
		t.Fatal("garbage accepted")
	}
	if err := Delete(root, "jira-to-spec"); err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesMergeTransforms(t *testing.T) {
	d := Data{Inputs: map[string]string{"ticket": "ORD-1"}, Steps: map[string]string{
		"a": `{"summary":"Place order","items":[{"id":1,"name":"x"},{"id":2,"name":"y"}]}`, "b": "TODO: fix\nok line\nTODO: test"}, Context: "ctx"}
	if got := Render("{{inputs.ticket}} {{steps.a.json.summary}} {{steps.a.json.items[1].name}} {{steps.a.json.items.id}} {{context}} {{nope}}", d); got != "ORD-1 Place order y [\n  1,\n  2\n] ctx " {
		t.Fatalf("%q", got)
	}
	m, _ := Merge(Node{Mode: "json-object"}, []string{"a", "b"}, d, map[string]string{"a": "Ticket"})
	if !strings.Contains(m, `"Ticket": {`) || !strings.Contains(m, `"b": "TODO`) {
		t.Fatal(m)
	}
	if c, _ := Merge(Node{Mode: "concat", Separator: `\n---\n`}, []string{"b", "b"}, d, nil); strings.Count(c, "---") != 1 {
		t.Fatal(c)
	}
	cases := []struct {
		n    Node
		in   string
		want string
	}{
		{Node{Op: "filter-lines", Expr: "^TODO"}, d.Steps["b"], "TODO: fix\nTODO: test"},
		{Node{Op: "regex-extract", Expr: `TODO: (\w+)`}, d.Steps["b"], "fix\ntest"},
		{Node{Op: "regex-replace", Expr: `TODO`, Replace: "DONE"}, "TODO x", "DONE x"},
		{Node{Op: "json-path", Expr: "items[*].name"}, d.Steps["a"], "[\n  \"x\",\n  \"y\"\n]"},
		{Node{Op: "json-pick", Expr: "id"}, `[{"id":1,"n":2}]`, "[\n  {\n    \"id\": 1\n  }\n]"},
		{Node{Op: "csv-to-json"}, "a,b\n1,2\n", "[\n  {\n    \"a\": \"1\",\n    \"b\": \"2\"\n  }\n]"},
		{Node{Op: "json-to-csv"}, `[{"a":1,"b":"x,y"}]`, "a,b\n1,\"x,y\"\n"},
		{Node{Op: "truncate", Expr: "3"}, "abcdef", "abc"},
		{Node{Op: "template", Template: "<{{input}}> for {{inputs.ticket}}"}, "x", "<x> for ORD-1"},
		{Node{Op: "split-lines"}, "a\n\nb\n", "[\n  \"a\",\n  \"b\"\n]"},
	}
	for _, c := range cases {
		got, err := Transform(c.n, c.in, d)
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", c.n.Op, got, err, c.want)
		}
	}
	if _, err := Transform(Node{Op: "ai", Template: "x"}, "y", d); err == nil {
		t.Fatal("AI transform must need the agent")
	}
}

func TestConvert(t *testing.T) {
	md := "# Title\n\nSome **bold** and `code` with [a link](https://x.dev).\n\n- one\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfmt.Println(\"<hi>\")\n```\n"
	h, _ := Convert(md, "html")
	for _, want := range []string{"<h1>Title</h1>", "<strong>bold</strong>", "<code>code</code>", `<a href="https://x.dev">a link</a>`, "<li>two</li>", "<td>2</td>", "&lt;hi&gt;"} {
		if !strings.Contains(h, want) {
			t.Errorf("html missing %q:\n%s", want, h)
		}
	}
	if txt, _ := Convert(h, "txt"); !strings.Contains(txt, "Title") || strings.Contains(txt, "<h1") || !strings.Contains(txt, "1 | 2") {
		t.Fatal(txt)
	}
	js := `[{"id":1,"name":"a"},{"id":2,"name":"b"}]`
	if c, _ := Convert(js, "csv"); c != "id,name\n1,a\n2,b\n" {
		t.Fatalf("%q", c)
	}
	if m, _ := Convert(js, "md"); !strings.Contains(m, "| id | name |") {
		t.Fatal(m)
	}
	if j, _ := Convert("id,name\n1,a\n", "json"); !strings.Contains(j, `"name": "a"`) {
		t.Fatal(j)
	}
	if j, _ := Convert("plain words", "json"); !strings.Contains(j, `"content": "plain words"`) {
		t.Fatal(j)
	}
	if x, _ := Convert("<script>alert(1)</script>[x](javascript:alert(1))", "html"); strings.Contains(x, "<script>") || strings.Contains(x, "javascript:") {
		t.Fatal("unsafe html:", x)
	}
}
