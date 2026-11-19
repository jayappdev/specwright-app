package flow

import (
	"strings"
	"testing"
)

func TestJSONToTables(t *testing.T) {
	in := `[{"name":"Ada","age":36,"tags":["math","code"],"note":"a | b"},{"name":"Bo","city":"Oslo","user":{"email":"bo@x"}}]`
	run := func(op, expr, in string) (string, error) {
		return Transform(Node{Type: "transform", Op: op, Expr: expr}, in, Data{})
	}
	md, err := run("json-md-table", "", in)
	if err != nil {
		t.Fatal(err)
	}
	want := "| name | age | tags | note | city | user |\n| --- | --- | --- | --- | --- | --- |\n" +
		"| Ada | 36 | [\"math\",\"code\"] | a \\| b |  |  |\n" +
		"| Bo |  |  |  | Oslo | {\"email\":\"bo@x\"} |\n"
	if md != want {
		t.Fatalf("markdown, fields in the JSON's order:\n%s\nwant\n%s", md, want)
	}
	md, _ = run("json-md-table", "user.email as Email, name, tags[0] as First tag", in)
	if !strings.HasPrefix(md, "| Email | name | First tag |\n") || !strings.Contains(md, "| bo@x | Bo |  |") || !strings.Contains(md, "|  | Ada | math |") {
		t.Fatalf("chosen columns:\n%s", md)
	}
	h, err := run("json-html-table", "", `{"total":2,"items":[{"q":"<b>&</b>","n":1},{"q":"line1\nline2","n":2}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"<table>", "<th>q</th><th>n</th>", "<td>&lt;b&gt;&amp;&lt;/b&gt;</td>", "<td>line1<br>line2</td>", "</table>"} {
		if !strings.Contains(h, w) {
			t.Fatalf("html lacks %q:\n%s", w, h)
		}
	}
	if strings.Contains(h, "total") {
		t.Fatalf("the object's list is the table, not the object:\n%s", h)
	}
	if md, _ := run("json-md-table", "", `["a","b"]`); md != "| value |\n| --- |\n| a |\n| b |\n" {
		t.Fatalf("plain values:\n%s", md)
	}
	if md, _ := run("json-md-table", "", `{"a":1,"b":"x"}`); md != "| a | b |\n| --- | --- |\n| 1 | x |\n" {
		t.Fatalf("one object:\n%s", md)
	}
	for _, bad := range []string{"not json", `"text"`, `[]`} {
		if _, err := run("json-html-table", "", bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}
