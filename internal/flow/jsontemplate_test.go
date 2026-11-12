package flow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONTemplate(t *testing.T) {
	in := `{"user":{"name":"Ana \"A\""},"order":{"id":7,"total":12.50,"big":12345678901234567890},"labels":["a","b"],"items":[{"sku":"x1","qty":2},{"sku":"y2","qty":5}]}`
	d := Data{Inputs: map[string]string{"region": "eu", "limit": "10"}}
	cases := []struct{ name, expr, tpl, want string }{
		{"typed whole values", "", `{"who":"{{this.user.name}}","total":"{{this.order.total}}","big":"{{this.order.big}}","tags":"{{this.labels}}","missing":"{{this.nope}}"}`,
			`{"who":"Ana \"A\"","total":12.50,"big":12345678901234567890,"tags":["a","b"],"missing":null}`},
		{"text inside a string", "", `{"label":"Order {{this.order.id}} for {{this.user.name}}","tags":"{{this.labels}} x"}`,
			`{"label":"Order 7 for Ana \"A\"","tags":"[\"a\",\"b\"] x"}`},
		{"bare placeholders and flow variables", "", `{"n": {{this.order.id}}, "region": "{{inputs.region}}", "limit": {{inputs.limit}}, "lit": "{not a var}"}`,
			`{"n":7,"region":"eu","limit":10,"lit":"{not a var}"}`},
		{"each element", "items", `{"id":"{{this.sku}}","n":"{{this.qty}}","pos":"{{this_index}}","of":"{{this_index}}/2"}`,
			`[{"id":"x1","n":2,"pos":1,"of":"1/2"},{"id":"y2","n":5,"pos":2,"of":"2/2"}]`},
	}
	for _, c := range cases {
		got, err := Transform(Node{Op: "json-template", Expr: c.expr, Template: c.tpl}, in, d)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if compact(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, compact(got), c.want)
		}
	}
	if _, err := Transform(Node{Op: "json-template", Expr: "user", Template: `{}`}, in, d); err == nil || !strings.Contains(err.Error(), "not a list") {
		t.Fatal("a non-list path must fail:", err)
	}
	if _, err := Transform(Node{Op: "json-template", Template: `{}`}, "not json", d); err == nil {
		t.Fatal("non-JSON input must fail")
	}
	if err := Ops["json-template"].check(Node{Template: `{"a": "{{this.x}}",}`}); err == nil {
		t.Fatal("an invalid template must be reported")
	}
	if err := Ops["json-template"].check(Node{Template: `{"a": {{this.x}}, "b": ["{{this.y}}"]}`}); err != nil {
		t.Fatal(err)
	}
}

func compact(s string) string {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return "INVALID: " + s
	}
	return b.String()
}

func TestJSONTemplateNested(t *testing.T) {
	in := `{"customer":{"name":"Ana","address":{"zip":"69001","city":"Lyon"}},
"orders":[{"id":1,"lines":[{"sku":"a","qty":2,"tags":["x"]},{"sku":"b","qty":1}]},{"id":2,"lines":[{"sku":"c","qty":5}]},{"id":3}]}`
	cases := []struct{ name, expr, tpl, want string }{
		{"nested each with parent, root and index", "", `{
  "customer": "{{this.customer.name}}",
  "orders": { "{{#each this.orders}}": {
    "order": "{{this.id}}",
    "lines": { "{{#each this.lines}}": {
      "sku": "{{this.sku}}", "order": "{{parent.id}}", "who": "{{root.customer.name}}",
      "pos": "{{parent.this_index}}.{{this_index}}", "tags": "{{this.tags}}" } } } } }`,
			`{"customer":"Ana","orders":[{"order":1,"lines":[{"sku":"a","order":1,"who":"Ana","pos":"1.1","tags":["x"]},{"sku":"b","order":1,"who":"Ana","pos":"1.2","tags":null}]},{"order":2,"lines":[{"sku":"c","order":2,"who":"Ana","pos":"2.1","tags":null}]},{"order":3,"lines":[]}]}`},
		{"each spread inside a list, with list path", "orders", `{"id": {{this.id}}, "all": ["first", {"{{#each this.lines}}": "{{this.sku}}"}, "last"]}`,
			`[{"id":1,"all":["first","a","b","last"]},{"id":2,"all":["first","c","last"]},{"id":3,"all":["first","last"]}]`},
		{"flatten lines across orders with the parent", "orders[*].lines[*]", `{"sku":"{{this.sku}}"}`,
			`[{"sku":"a"},{"sku":"b"},{"sku":"c"}]`},
		{"copied objects keep their key order; keys can use placeholders", "", `{"addr":"{{this.customer.address}}","by_sku":{"{{this.orders[0].lines[0].sku}}":"{{this.orders[0].lines[0].qty}}"}}`,
			`{"addr":{"zip":"69001","city":"Lyon"},"by_sku":{"a":2}}`},
		{"parent above the top is the outer list element's parent", "orders", `{"o":"{{this.id}}","c":"{{parent.customer.name}}"}`,
			`[{"o":1,"c":"Ana"},{"o":2,"c":"Ana"},{"o":3,"c":"Ana"}]`},
	}
	for _, c := range cases {
		got, err := Transform(Node{Op: "json-template", Expr: c.expr, Template: c.tpl}, in, Data{})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if g := compact(got); g != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, g, c.want)
		}
	}
	if !strings.Contains(compact(mustT(t, `{"a":"{{this.customer.address}}"}`, in)), `{"zip":"69001","city":"Lyon"}`) {
		t.Fatal("order")
	}
	for tpl, want := range map[string]string{
		`{"x": {"{{#each this.orders}}": {}, "other": 1}}`: "only key",
		`{"x": {"{{#each}}": {}}}`:                         "write an each block",
	} {
		if err := Ops["json-template"].check(Node{Template: tpl}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", tpl, err)
		}
	}
	if _, err := Transform(Node{Op: "json-template", Template: `{"x":{"{{#each this.customer}}":"{{this}}"}}`}, in, Data{}); err == nil || !strings.Contains(err.Error(), "not a list") {
		t.Fatal("each over an object must fail:", err)
	}
}

func mustT(t *testing.T, tpl, in string) string {
	got, err := Transform(Node{Op: "json-template", Template: tpl}, in, Data{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestJSONTemplateVarsAreKnown(t *testing.T) {
	f := New("j", "J")
	f.Nodes = append(f.Nodes,
		Node{ID: "t", Type: "transform", Op: "json-template", Title: "Reshape", Template: `{"o":{"{{#each this.orders}}":{"id":"{{parent.x}}","r":"{{root.y}}","i":"{{this_index}}"}}}`},
		Node{ID: "o", Type: "output", Format: "json"})
	f.Edges = []Edge{{From: "start", To: "t"}, {From: "t", To: "o"}}
	for _, i := range Validate(f, nil) {
		if i.Node == "t" {
			t.Errorf("unexpected: %+v", i)
		}
	}
}
