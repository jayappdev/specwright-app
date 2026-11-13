package flow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaInferLookupPaths(t *testing.T) {
	s := SchemaOfText(`[{"id":"A1","total":12.5,"date":"2026-11-02","customer":{"name":"Ana"},"items":[{"sku":"k","qty":2}]},
	                   {"id":"A2","total":3,"customer":{"name":"Bob","vip":true},"items":[]}]`)
	if s.Type != "array" || s.Items.Type != "object" {
		t.Fatal(s)
	}
	row := s.Items
	if got := strings.Join(row.Fields(), ","); got != "id,total,date,customer,items" {
		t.Fatal("order", got)
	}
	if row.Prop("total").Type != "number" || row.Prop("date").Format != "date" || contains(row.Required, "date") || !contains(row.Required, "id") {
		t.Fatalf("merge: %+v", row)
	}
	for path, want := range map[string]string{"customer.name": "string[]", "[0].customer.name": "string", "items[*].qty": "integer[]", "items.sku": "string[]", "[0].customer.vip": "boolean", "[*].id": "string[]"} {
		got, ok := s.Lookup(path)
		if !ok || typeName(got) != want {
			t.Errorf("%s: %v %s, want %s", path, ok, typeName(got), want)
		}
	}
	if _, ok := row.Lookup("customer.email"); ok {
		t.Fatal("a missing field")
	}
	if _, ok := (&Schema{Type: "object"}).Lookup("anything.at.all"); !ok {
		t.Fatal("an open object accepts any path")
	}
	var paths []string
	for _, p := range s.Paths(50) {
		paths = append(paths, p.Path+":"+p.Type)
	}
	if got := strings.Join(paths, " "); got != "[*].id:string [*].total:number [*].date:date [*].customer:object [*].customer.name:string [*].customer.vip:boolean [*].items:object[] [*].items[*].sku:string [*].items[*].qty:integer" {
		t.Fatal(got)
	}
	if c := row.Compact(); c != "{id: string, total: number, date?: date, customer: {name: string, vip?: boolean}, items: {sku: string, qty: integer}[]}" {
		t.Fatal(c)
	}
	// JSON Schema in, JSON Schema out, order kept
	js := `{"type":"object","properties":{"z":{"type":"string","description":"last name"},"a":{"type":"array","items":{"type":"integer"}}},"required":["z"]}`
	p, err := ParseSchema(js)
	if err != nil || strings.Join(p.Fields(), ",") != "z,a" || p.Prop("z").Description != "last name" {
		t.Fatal(p, err)
	}
	b, _ := json.Marshal(p)
	if string(b) != js {
		t.Fatalf("round trip:\n%s\n%s", b, js)
	}
	if ex, _ := ParseSchema(`{"type":"x","n":1}`); ex.Prop("type") == nil {
		t.Fatal("an example that only looks like a schema is an example")
	}
}

func TestShapesThroughAFlow(t *testing.T) {
	orders := SchemaOfText(`{"orders":[{"id":"A1","region":"EU","total":10,"customer":{"tier":"gold"},"items":[{"sku":"k","qty":1}]}]}`)
	f := New("s", "S")
	f.Inputs = []Input{{Name: "region", Label: "Region", Required: true}}
	f.Nodes = append(f.Nodes,
		Node{ID: "get", Type: "tool", Tool: "orders-api"},
		Node{ID: "open", Type: "filter", Cond: &Cond{Rules: []Rule{{Field: "region", Op: "eq", Value: "{{inputs.region}}"}, {Field: "customer.tier", Op: "eq", Value: "gold"}}}},
		Node{ID: "agg", Type: "aggregate", GroupBy: []string{"customer.tier"}, Measures: []Measure{{Fn: "count", As: "n"}, {Fn: "sum", Field: "items[*].qty", As: "units"}, {Fn: "max", Field: "total"}, {Fn: "distinct", Field: "items.sku", As: "skus"}}},
		Node{ID: "top", Type: "transform", Op: "json-path", Expr: "[0].units"},
		Node{ID: "shape", Type: "transform", Op: "json-template", Expr: "orders", Template: `{"order":"{{this.id}}","lines":{"{{#each this.items}}":{"sku":"{{this.sku}}","of":"{{parent.id}}"}}}`},
		Node{ID: "each", Type: "loop", Split: "json"},
		Node{ID: "g", Type: "gather", Mode: "json-array"},
		Node{ID: "ask", Type: "ai", Prompt: "x", OutputSchema: &Schema{Type: "object", Properties: Props{{"ok", &Schema{Type: "boolean"}}}}},
		Node{ID: "d", Type: "decision"})
	f.Edges = []Edge{{From: "start", To: "get"}, {From: "get", To: "open"}, {From: "open", To: "agg"}, {From: "agg", To: "top"},
		{From: "get", To: "shape"}, {From: "shape", To: "each"}, {From: "each", To: "g"}, {From: "g", To: "ask"}, {From: "ask", To: "d"}}
	sh := f.Shapes(ShapeHints{Tools: map[string]*Schema{"orders-api": orders}})
	if sh["start"].Out.Prop("region") == nil || sh["get"].Source != "declared" {
		t.Fatalf("%+v", sh["get"])
	}
	if sh["open"].Rows.Prop("customer") == nil || sh["open"].Out.Type != "array" {
		t.Fatalf("filter rows %+v", sh["open"])
	}
	if c := sh["agg"].Out.Compact(); c != "{customer.tier: string, n: integer, units: number, max_total: integer, skus: string[]}[]" {
		t.Fatal(c)
	}
	if typeName(sh["top"].Out) != "number" {
		t.Fatal(typeName(sh["top"].Out))
	}
	if c := sh["shape"].Out.Compact(); c != "{order: string, lines: {sku: string, of: string}[]}[]" {
		t.Fatal(c)
	}
	if sh["each"].Item.Prop("lines") == nil || sh["g"].Out.Items.Prop("order") == nil {
		t.Fatal("loop item / gather")
	}
	if sh["d"].In.Prop("ok") == nil || sh["ask"].Source != "declared" {
		t.Fatal("ai expected reply")
	}
	// unknown without a declared shape; learned from a sample
	sh = f.Shapes(ShapeHints{Samples: map[string]string{"get": `[{"region":"EU"}]`}})
	if sh["get"].Source != "sample" || sh["open"].Rows.Prop("region") == nil {
		t.Fatalf("%+v", sh["get"])
	}
	if sh = f.Shapes(ShapeHints{}); sh["get"].Source != "unknown" || sh["open"].Rows != nil {
		t.Fatalf("%+v", sh["get"])
	}
}

func TestValidateChecksPaths(t *testing.T) {
	orders := SchemaOfText(`[{"id":"A1","total":10,"customer":{"tier":"gold"}}]`)
	f := New("p", "P")
	f.Nodes = append(f.Nodes,
		Node{ID: "get", Type: "tool", Tool: "orders-api"},
		Node{ID: "flt", Type: "filter", Cond: &Cond{Rules: []Rule{{Field: "customer.tier", Op: "eq", Value: "gold"}, {Group: &Cond{Rules: []Rule{{Field: "customer.tire", Op: "eq", Value: "x"}}}}}}},
		Node{ID: "agg", Type: "aggregate", GroupBy: []string{"regoin"}, Measures: []Measure{{Fn: "sum", Field: "total", As: "t"}}, OrderBy: []string{"-t"}},
		Node{ID: "ai", Type: "ai", Prompt: "{{get.buffer_output.json[0].customer.tier}} {{get.buffer_output.json[0].cust}} {{steps.agg.json[0].t}}"},
		Node{ID: "o", Type: "output", Format: "md"})
	f.Edges = []Edge{{From: "start", To: "get"}, {From: "get", To: "flt"}, {From: "flt", To: "agg"}, {From: "agg", To: "ai"}, {From: "ai", To: "o"}}
	tools := []ToolInfo{{Name: "orders-api", Output: orders}}
	var warns []string
	for _, i := range Validate(f, tools) {
		if i.Level == "warning" && strings.Contains(i.Message, "is not in") {
			warns = append(warns, i.Message)
		}
	}
	got := strings.Join(warns, "\n")
	for _, want := range []string{`flt: the field "customer.tire" is not in its rows`, `agg: the group field "regoin" is not in its rows`, `"[0].cust" is not in what get gives. Its fields: id, total, customer`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if len(warns) != 3 {
		t.Errorf("only the three misses:\n%s", got)
	}
	// without a declared shape nothing is flagged
	for _, i := range Validate(f, []ToolInfo{{Name: "orders-api"}}) {
		if strings.Contains(i.Message, "is not in") {
			t.Fatal("unknown shapes accept any path:", i.Message)
		}
	}
}
