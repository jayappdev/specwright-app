package flow

import (
	"strings"
	"testing"
)

func loopFlow() *Flow {
	f := New("l", "Loops")
	f.Nodes = append(f.Nodes,
		Node{ID: "list", Type: "tool", Tool: "ls", X: 1},
		Node{ID: "each", Type: "loop", X: 2},
		Node{ID: "doc", Type: "tool", Tool: "doc", X: 3},
		Node{ID: "files", Type: "loop", X: 4}, // nested
		Node{ID: "read", Type: "tool", Tool: "read", X: 5},
		Node{ID: "inner", Type: "gather", X: 6},
		Node{ID: "ai", Type: "ai", Prompt: "Summarise {{item}}", X: 7},
		Node{ID: "save", Type: "output", Format: "md", X: 7, Y: 1}, // per item
		Node{ID: "all", Type: "gather", Mode: "sections", X: 8},
		Node{ID: "out", Type: "output", Format: "md", X: 9})
	f.Edges = []Edge{{From: "start", To: "list"}, {From: "list", To: "each"}, {From: "each", To: "doc"}, {From: "doc", To: "files"}, {From: "files", To: "read"}, {From: "read", To: "inner"},
		{From: "inner", To: "ai"}, {From: "doc", To: "save"}, {From: "ai", To: "all"}, {From: "all", To: "out"}}
	return f
}

func TestLoopsPairAndNest(t *testing.T) {
	f := loopFlow()
	li, is := f.Loops()
	if len(is) != 0 {
		t.Fatal(is)
	}
	if li.Gather["each"] != "all" || li.Gather["files"] != "inner" || li.Loop["all"] != "each" {
		t.Fatalf("%+v", li)
	}
	order, _ := f.Order()
	body := strings.Join(li.Body(order, "each"), ",")
	if body != "doc,files,read,inner,ai,save" && body != "doc,files,read,inner,save,ai" {
		t.Fatal(body)
	}
	if strings.Join(li.Body(order, "files"), ",") != "read" || len(li.Stack["out"]) != 0 || len(li.Stack["ai"]) != 1 {
		t.Fatalf("%v %v", li.Body(order, "files"), li.Stack)
	}

	// a loop without its gather, and a step joining inside and outside
	f.Edges = append(f.Edges, Edge{From: "list", To: "ai"})
	f.Nodes = append(f.Nodes, Node{ID: "lonely", Type: "loop", X: 1, Y: 5})
	f.Edges = append(f.Edges, Edge{From: "start", To: "lonely"})
	_, is = f.Loops()
	msgs := ""
	for _, i := range is {
		msgs += i.Message + "\n"
	}
	if !strings.Contains(msgs, "joins steps from inside and outside") || !strings.Contains(msgs, "needs a Gather") {
		t.Fatal(msgs)
	}
}

func TestSplitItems(t *testing.T) {
	cases := []struct {
		src  string
		n    Node
		want string
	}{
		{"a\n\n b \nc", Node{}, "a|b|c"},
		{`["x", {"name":"Order"}]`, Node{Split: "auto"}, `x|{"name":"Order"}`},
		{`{"symbols": ["A","B"]}`, Node{}, "A|B"},
		{"name\nA\nB\n", Node{Split: "csv"}, `{"name":"A"}|{"name":"B"}`},
		{"class Order {} class Item {}", Node{Split: "regex", Expr: `class (\w+)`}, "Order|Item"},
		{"a\nb\na\nc", Node{Unique: true, Filter: "^[ab]$", Max: 5}, "a|b"},
		{"1\n2\n3", Node{Max: 2}, "1|2"},
	}
	for _, c := range cases {
		got, err := SplitItems(c.src, c.n)
		if err != nil || strings.Join(got, "|") != c.want {
			t.Errorf("%q: %v %v", c.src, got, err)
		}
	}
	if _, err := SplitItems("x", Node{Split: "regex", Expr: "("}); err == nil {
		t.Fatal("bad pattern")
	}
}

func TestGather(t *testing.T) {
	rs := []ItemResult{{Item: `{"name":"Order"}`, Output: `{"lines": 3}`}, {Item: "Item", Output: "plain"}, {Item: "Bad", Error: "boom"}}
	for mode, want := range map[string]string{
		"concat":      "{\"lines\": 3}\n\nplain\n\nERROR: boom",
		"sections":    "## Order\n\n{\"lines\": 3}\n\n## Item\n\nplain\n\n## Bad\n\nERROR: boom",
		"json-object": `"Order": {`,
		"json-array":  `"error": "boom"`,
	} {
		got, err := Gather(Node{Mode: mode}, rs, Data{})
		if err != nil || !strings.Contains(got, want) {
			t.Errorf("%s: %q %v", mode, got, err)
		}
	}
	got, _ := Gather(Node{Mode: "template", Template: "{{item_index}}/{{item_count}} {{item}} = {{input}}"}, rs[1:2], Data{})
	if got != "1/1 Item = plain" {
		t.Fatal(got)
	}
}

func TestValidateLoopVars(t *testing.T) {
	f := loopFlow()
	f.Nodes[len(f.Nodes)-1].Template = "{{item}}" // the output after the loop
	found := false
	for _, i := range Validate(f, []ToolInfo{{Name: "ls"}, {Name: "doc"}, {Name: "read"}}) {
		found = found || (i.Node == "out" && strings.Contains(i.Message, "not inside a loop"))
		if i.Level == "error" {
			t.Errorf("unexpected: %+v", i)
		}
	}
	if !found {
		t.Fatal("{{item}} outside a loop must be flagged")
	}
}

func TestMultichoiceNeedsOptions(t *testing.T) {
	f := New("m", "M")
	f.Inputs = []Input{{Name: "langs", Label: "Languages", Type: "multichoice"}}
	bad := false
	for _, i := range Validate(f, nil) {
		bad = bad || strings.Contains(i.Message, "no options")
	}
	if !bad {
		t.Fatal("a multiple choice without options must be flagged")
	}
}

func TestReferencesUseStepIDs(t *testing.T) {
	f := New("u", "U")
	f.Nodes = append(f.Nodes,
		Node{ID: "clean", Type: "transform", Op: "uppercase", Title: "Clean up"},
		Node{ID: "b", Type: "transform", Op: "template", Title: "Clean up", Template: "{{clean-up.buffer_output}} {{clean.buffer_output}}"},
		Node{ID: "o", Type: "output", Format: "md"})
	f.Edges = []Edge{{From: "start", To: "clean"}, {From: "clean", To: "b"}, {From: "b", To: "o"}}
	var byTitle, byID bool
	for _, i := range Validate(f, nil) {
		byTitle = byTitle || strings.Contains(i.Message, "{{clean-up.buffer_output}}, but there is no step with the id")
		byID = byID || strings.Contains(i.Message, "{{clean.buffer_output}}")
		if strings.Contains(i.Message, "already called") {
			t.Fatal("titles need not be unique:", i.Message)
		}
	}
	if !byTitle || byID {
		t.Fatalf("a title is not a reference (%v); an id is (%v)", byTitle, byID)
	}
	// older flows: a unique title or tool name is moved over to the id
	f.Nodes[2].Title = "Tidy"
	f.Nodes = append(f.Nodes, Node{ID: "t", Type: "tool", Tool: "builtin:read_file", Args: map[string]string{"p": "{{tidy.output_file}}"}},
		Node{ID: "u", Type: "ai", Prompt: "{{read_file.buffer_output}} {{each.item}} {{steps.clean}} {{inputs.x}}"})
	f.normalize()
	if got := f.Node("t").Args["p"]; got != "{{b.output_file}}" {
		t.Fatal(got)
	}
	if got := f.Node("u").Prompt; got != "{{t.buffer_output}} {{each.item}} {{steps.clean}} {{inputs.x}}" {
		t.Fatal(got)
	}
	if got := f.Node("b").Template; got != "{{clean.buffer_output}} {{clean.buffer_output}}" {
		t.Fatal("the title is now unique, so it is moved over:", got)
	}
	g := New("s", "S") // a title shared by two steps cannot be moved over
	g.Nodes = append(g.Nodes, Node{ID: "x", Type: "ai", Title: "Same"}, Node{ID: "y", Type: "ai", Title: "Same", Prompt: "{{same.buffer_output}}"})
	g.normalize()
	if got := g.Node("y").Prompt; got != "{{same.buffer_output}}" {
		t.Fatal(got)
	}
}

func TestLoopItemByID(t *testing.T) {
	f := New("n", "N")
	f.Nodes = append(f.Nodes,
		Node{ID: "l", Type: "loop", Title: "Each file", Source: "a"},
		Node{ID: "t", Type: "transform", Op: "template", Template: "{{l.item}} {{l.item.json.x}}"},
		Node{ID: "g", Type: "gather"},
		Node{ID: "after", Type: "transform", Op: "template", Template: "{{l.item}}"},
		Node{ID: "o", Type: "output", Format: "md"})
	f.Edges = []Edge{{From: "start", To: "l"}, {From: "l", To: "t"}, {From: "t", To: "g"}, {From: "g", To: "after"}, {From: "after", To: "o"}}
	var inside, outside bool
	for _, i := range Validate(f, nil) {
		inside = inside || (i.Node == "t" && i.Level == "error")
		outside = outside || (i.Node == "after" && strings.Contains(i.Message, "does not run inside the loop"))
	}
	if inside || !outside {
		t.Fatalf("inside error %v, outside error %v", inside, outside)
	}
	d := Data{Loops: map[string]LoopItem{"l": {Item: `{"x":"X"}`, Index: 2, Count: 5}}}
	if got := Render("{{l.item.json.x}} {{l.item_index}}/{{l.item_count}} [{{each-file.item}}]", d); got != "X 2/5 []" {
		t.Fatal(got)
	}
}

func TestSplitItemsFromJSONInput(t *testing.T) {
	cases := []struct {
		src, split, field string
		want              string // items joined by |, or "ERR:<part of the message>"
	}{
		{`{"stocks": "AMZN"}`, "auto", "", "AMZN"},
		{`{"stocks": "AMZN"}`, "lines", "", "AMZN"},
		{"{\"stocks\": \"AMZN\\nMSFT\\n\"}", "lines", "", "AMZN|MSFT"},
		{`{"stocks": "AMZN"}`, "json", "", "ERR:not a JSON array"},
		{`{"stocks": "[\"AMZN\",\"MSFT\"]"}`, "json", "", "AMZN|MSFT"},
		{"{\"stocks\": \"AMZN\\nMSFT\", \"note\": \"x\"}", "auto", "stocks", "AMZN|MSFT"},
		{"{\"stocks\": \"AMZN\", \"note\": \"x\"}", "auto", "", "ERR:the fields stocks, note"},
		{`{"stocks": "A", "picks": ["x", "y"]}`, "auto", "", "x|y"},
		{`{"data": {"items": [{"sku": "k"}, {"sku": "m"}]}}`, "auto", "data.items", `{"sku":"k"}|{"sku":"m"}`},
		{`{"data": {}}`, "auto", "rows", "ERR:no field \"rows\" (its fields: data)"},
		{"a\nb", "auto", "", "a|b"},
		{`{"x": "a1 b2"}`, "regex", "", "ERR:"},
	}
	for _, c := range cases {
		n := Node{Split: c.split, Field: c.field}
		if c.split == "regex" {
			n.Expr = `[a-z]\d`
		}
		items, err := SplitItems(c.src, n)
		got := strings.Join(items, "|")
		if err != nil {
			got = "ERR:" + err.Error()
		}
		if c.split == "regex" { // a regex sees the raw text
			if err != nil || got != "a1|b2" {
				t.Errorf("regex: %q", got)
			}
			continue
		}
		if strings.HasPrefix(c.want, "ERR:") && strings.HasPrefix(got, "ERR:") && strings.Contains(got, c.want[4:]) || got == c.want {
			continue
		}
		t.Errorf("%s (%s, field %q): got %q, want %q", c.src, c.split, c.field, got, c.want)
	}
}

func TestJoinTextKeepsMarkdownWhole(t *testing.T) {
	rows := []ItemResult{{Item: "a", Output: "| AMZN | 265.58 |\n"}, {Item: "b", Output: "| MSFT | 401.10 |"}}
	head := "| Ticker | Price |\n|---|---|"
	got, _ := Gather(Node{Mode: "concat"}, append([]ItemResult{{Item: "h", Output: head}}, rows...), Data{})
	if got != head+"\n| AMZN | 265.58 |\n| MSFT | 401.10 |" {
		t.Fatalf("%q", got)
	}
	got, _ = Gather(Node{Mode: "concat"}, []ItemResult{{Output: "- one"}, {Output: "- two"}, {Output: "Some text"}, {Output: "More text"}}, Data{})
	if got != "- one\n- two\n\nSome text\n\nMore text" {
		t.Fatalf("%q", got)
	}
	if got, _ := Gather(Node{Mode: "concat", Separator: ", "}, rows, Data{}); !strings.Contains(got, "|, |") {
		t.Fatalf("a chosen separator is kept: %q", got)
	}
	m, _ := Merge(Node{Mode: "concat"}, []string{"a", "b"}, Data{Steps: map[string]string{"a": head + "\n| X | 1 |\n", "b": "| Y | 2 |"}}, nil)
	if m != head+"\n| X | 1 |\n| Y | 2 |" {
		t.Fatalf("%q", m)
	}
	// already joined with blank lines: the HTML still has one table
	h := MarkdownToHTML(head + "\n| X | 1 |\n\n| Y | 2 |\n\nAfter")
	if strings.Count(h, "<table>") != 1 || strings.Count(h, "<tr>") != 3 || !strings.Contains(h, "<p>After</p>") {
		t.Fatal(h)
	}
}

func TestTableRowsStayOneLine(t *testing.T) {
	d := Data{Item: "AMZN", ItemCount: 1, Input: "265.58\n", Steps: map[string]string{"q": "price 265.58\nchange -0.7% | flat\n"}}
	if got := Render("| {{item}} | {{input}} |", d); got != "| AMZN | 265.58 |" {
		t.Fatalf("%q", got)
	}
	if got := Render("| {{item}} | {{q.buffer_output}} |", d); got != `| AMZN | price 265.58<br>change -0.7% \| flat |` {
		t.Fatalf("%q", got)
	}
	// not a table row: values as they are
	if got := Render("Price: {{input}}", d); got != "Price: 265.58\n" {
		t.Fatalf("%q", got)
	}
	if got := Render("{{q.buffer_output}}\n| a | b |", d); got != "price 265.58\nchange -0.7% | flat\n\n| a | b |" {
		t.Fatalf("%q", got)
	}
	g, _ := Gather(Node{Mode: "template", Template: "| {{item}} | {{input}} |"}, []ItemResult{{Item: "AMZN", Output: "a\nb"}, {Item: "MSFT", Output: "c"}}, Data{})
	if g != "| AMZN | a<br>b |\n| MSFT | c |" {
		t.Fatalf("%q", g)
	}
	h := MarkdownToHTML("| T | V |\n|---|---|\n" + g)
	if strings.Count(h, "<tr>") != 3 {
		t.Fatal(h)
	}
}

func TestMarkdownTableCells(t *testing.T) {
	h := MarkdownToHTML("| a | b |\n|---|---|\n| x<br>y | z \\| w |")
	if !strings.Contains(h, "<td>x<br>y</td>") || !strings.Contains(h, "<td>z | w</td>") || strings.Count(h, "<td>") != 2 {
		t.Fatal(h)
	}
	if h := MarkdownToHTML("text <script>x</script>"); strings.Contains(h, "<script>") {
		t.Fatal("still escaped:", h)
	}
}
