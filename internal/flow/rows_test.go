package flow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const orders = `[
 {"id":"A1","region":"EU","customer":{"name":"Ana","tier":"gold"},"total":120.5,"status":"open","items":[{"sku":"k","qty":2},{"sku":"m","qty":1}],"date":"2026-11-02"},
 {"id":"A2","region":"US","customer":{"name":"Bob","tier":"silver"},"total":80,"status":"closed","items":[{"sku":"k","qty":5}],"date":"2026-11-03"},
 {"id":"A3","region":"EU","customer":{"name":"Cy","tier":"gold"},"total":40,"status":"open","items":[],"date":"2026-11-05"},
 {"id":"A4","region":"EU","customer":{"name":"Ana","tier":"gold"},"total":200,"status":"open","items":[{"sku":"z","qty":0}],"date":"2026-11-09"}
]`

func cmpJSON(t *testing.T, name, got, want string) {
	t.Helper()
	var a, b bytes.Buffer
	if err := json.Compact(&a, []byte(got)); err != nil {
		t.Fatalf("%s: not JSON: %s", name, got)
	}
	_ = json.Compact(&b, []byte(want))
	if a.String() != b.String() {
		t.Errorf("%s:\n got %s\nwant %s", name, a.String(), b.String())
	}
}

func ids(t *testing.T, out string) string {
	t.Helper()
	rows, _, err := ParseRows(out, "json")
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, r := range rows {
		v, _ := fieldOf("id", r, Data{})
		s = append(s, textOf(v))
	}
	return strings.Join(s, ",")
}

func TestFilterNestedConditions(t *testing.T) {
	d := Data{Inputs: map[string]string{"min": "50"}}
	cases := []struct {
		name string
		n    Node
		want string
	}{
		{"all of, with a nested any group", Node{Cond: &Cond{Rules: []Rule{
			{Field: "region", Op: "eq", Value: "EU"},
			{Group: &Cond{Match: "any", Rules: []Rule{{Field: "total", Op: "gt", Value: "{{inputs.min}}"}, {Field: "customer.tier", Op: "eq", Value: "GOLD", IgnoreCase: true}}}},
			{Field: "items", Op: "not_empty"}}}}, "A1,A4"},
		{"any element of a nested list", Node{Cond: &Cond{Rules: []Rule{{Field: "items[*].qty", Op: "gte", Value: "5"}}}}, "A2"},
		{"every element", Node{Cond: &Cond{Rules: []Rule{{Field: "items.qty", Op: "gt", Value: "0", Every: true}, {Field: "items", Op: "len_gt", Value: "0"}}}}, "A1,A2"},
		{"drop, none of", Node{Mode: "drop", Cond: &Cond{Match: "none", Rules: []Rule{{Field: "status", Op: "eq", Value: "closed"}}}}, "A2"},
		{"compare two fields, dates as text, between", Node{Cond: &Cond{Rules: []Rule{{Field: "date", Op: "between", Value: "2026-11-03, 2026-11-06"}, {Field: "total", Op: "gt", Value: "{{this.items[0].qty}}0"}}}}, "A2,A3"},
		{"in list, contains on list, regex, sorted and limited", Node{Max: 2, OrderBy: []string{"-total"}, Cond: &Cond{Rules: []Rule{{Field: "customer.name", Op: "in", Value: "Ana, Cy"}, {Field: "items.sku", Op: "not_contains", Value: "m"}, {Field: "id", Op: "matches", Value: "^A[0-9]$"}}}}, "A4,A3"},
	}
	for _, c := range cases {
		if err := c.n.Cond.Check(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		out, err := FilterRows(c.n, orders, d)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := ids(t, out); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	// sort before limiting: the top N
	top, _ := FilterRows(Node{Max: 2, OrderBy: []string{"-date"}, Cond: &Cond{Rules: []Rule{{Field: "items", Op: "not_empty"}}}}, orders, Data{})
	if got := ids(t, top); got != "A4,A2" {
		t.Fatal("top 2 newest:", got)
	}
	// plain lines in, lines out; key order kept for JSON rows
	out, _ := FilterRows(Node{Cond: &Cond{Rules: []Rule{{Op: "starts", Value: "b"}}}, Unique: true}, "apple\nbanana\nberry\nbanana", Data{})
	if out != "banana\nberry" {
		t.Fatalf("%q", out)
	}
	out, _ = FilterRows(Node{Cond: &Cond{Rules: []Rule{{Field: "z", Op: "gt", Value: "1"}}}}, `[{"z":2,"a":1}]`, Data{})
	if !strings.Contains(out, `"z": 2,`) {
		t.Fatal("key order:", out)
	}
	// CSV in
	out, _ = FilterRows(Node{Cond: &Cond{Rules: []Rule{{Field: "qty", Op: "gt", Value: "3"}}}}, "sku,qty\nk,2\nm,10", Data{})
	cmpJSON(t, "csv", out, `[{"sku":"m","qty":10}]`)
	if err := (&Cond{Rules: []Rule{{Field: "x", Op: "matches", Value: "("}}}).Check(); err == nil {
		t.Fatal("bad regex")
	}
}

func TestAggregate(t *testing.T) {
	group := Node{GroupBy: []string{"region", "customer.tier"}, OrderBy: []string{"-revenue"}, Measures: []Measure{
		{Fn: "count", As: "orders"}, {Fn: "sum", Field: "total", As: "revenue"}, {Fn: "avg", Field: "total", As: "avg"},
		{Fn: "min", Field: "date", As: "first_date"}, {Fn: "max", Field: "total"}, {Fn: "count_distinct", Field: "customer.name", As: "customers"},
		{Fn: "sum", Field: "items[*].qty", As: "units"}, {Fn: "distinct", Field: "items.sku", As: "skus"}, {Fn: "concat", Field: "id", Arg: "|", As: "ids"}}}
	out, err := Aggregate(group, orders, Data{})
	if err != nil {
		t.Fatal(err)
	}
	cmpJSON(t, "group", out, `[
	 {"region":"EU","customer.tier":"gold","orders":3,"revenue":360.5,"avg":120.166666667,"first_date":"2026-11-02","max_total":200,"customers":2,"units":3,"skus":["k","m","z"],"ids":"A1|A3|A4"},
	 {"region":"US","customer.tier":"silver","orders":1,"revenue":80,"avg":80,"first_date":"2026-11-03","max_total":80,"customers":1,"units":5,"skus":["k"],"ids":"A2"}]`)

	total, _ := Aggregate(Node{Measures: []Measure{{Fn: "count"}, {Fn: "sum", Field: "total"}, {Fn: "median", Field: "total"}}}, orders, Data{})
	cmpJSON(t, "totals", total, `[{"count":4,"sum_total":440.5,"median_total":100.25}]`)

	win := Node{Mode: "window", GroupBy: []string{"region"}, OrderBy: []string{"-total"}, Max: 3, Measures: []Measure{
		{Fn: "row_number", As: "n"}, {Fn: "rank", As: "r"}, {Fn: "running_sum", Field: "total", As: "run"},
		{Fn: "lag", Field: "id", As: "prev"}, {Fn: "pct_of_total", Field: "total", As: "pct"}, {Fn: "sum", Field: "total", As: "region_total"}}}
	out, err = Aggregate(win, orders, Data{})
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	_ = json.Unmarshal([]byte(out), &rows)
	if len(rows) != 3 {
		t.Fatal(out)
	}
	got := func(i int, k string) string { b, _ := json.Marshal(rows[i][k]); return string(b) }
	// input order is kept: A1 (EU, 2nd biggest), A2 (US), A3 (EU, smallest)
	for _, c := range [][3]string{{"0", "n", "2"}, {"0", "run", "320.5"}, {"0", "prev", `"A4"`}, {"0", "pct", "33.43"}, {"0", "region_total", "360.5"},
		{"1", "n", "1"}, {"1", "prev", "null"}, {"1", "pct", "100"}, {"2", "r", "3"}, {"2", "run", "360.5"}} {
		i := int(c[0][0] - '0')
		if g := got(i, c[1]); g != c[2] {
			t.Errorf("row %d %s = %s, want %s", i, c[1], g, c[2])
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[\n  {\n    \"id\": \"A1\"") {
		t.Fatal("rows keep their fields first:", out[:60])
	}
	for _, bad := range []Node{{}, {Measures: []Measure{{Fn: "rank"}}}, {Measures: []Measure{{Fn: "sum"}}}, {Measures: []Measure{{Fn: "count"}, {Fn: "count"}}}} {
		if CheckAggregate(bad) == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
}

func TestDecide(t *testing.T) {
	n := Node{Type: "decision", Branches: []Branch{
		{ID: "big", Label: "Big EU", When: &Cond{Rules: []Rule{{Field: "summary.total", Op: "gt", Value: "100"}, {Field: "summary.regions", Op: "contains", Value: "EU"}}}},
		{ID: "any", Label: "Has errors", When: &Cond{Rules: []Rule{{Field: "errors", Op: "len_gt", Value: "0"}}}}}}
	in := `{"summary":{"total":150,"regions":["EU","US"]},"errors":["x"]}`
	if bs, _ := Decide(n, in, Data{}); strings.Join(bs, ",") != "big" {
		t.Fatal(bs)
	}
	n.Mode = "all"
	if bs, _ := Decide(n, in, Data{}); strings.Join(bs, ",") != "big,any" {
		t.Fatal(bs)
	}
	if bs, _ := Decide(n, `{"summary":{"total":5},"errors":[]}`, Data{}); strings.Join(bs, ",") != ElseBranch {
		t.Fatal(bs)
	}
	if bs, _ := Decide(Node{Branches: []Branch{{ID: "y", When: &Cond{Rules: []Rule{{Op: "contains", Value: "FAIL", IgnoreCase: true}}}}}}, "build failed", Data{}); bs[0] != "y" {
		t.Fatal("text input", bs)
	}
}

func TestValidateDecision(t *testing.T) {
	f := New("d", "D")
	f.Nodes = append(f.Nodes,
		Node{ID: "d", Type: "decision", Branches: []Branch{{ID: "a", Label: "A", When: &Cond{Rules: []Rule{{Field: "x", Op: "gt", Value: "{{this.y}}"}}}}, {ID: "b", Label: "B", When: &Cond{}}}},
		Node{ID: "o1", Type: "output", Format: "md"}, Node{ID: "o2", Type: "output", Format: "md"}, Node{ID: "o3", Type: "output", Format: "md"},
		Node{ID: "flt", Type: "filter", Cond: &Cond{Rules: []Rule{{Field: "x", Op: "nope"}}}},
		Node{ID: "agg", Type: "aggregate"})
	f.Edges = []Edge{{From: "start", To: "d"}, {From: "d", To: "o1", Branch: "a"}, {From: "d", To: "o2", Branch: "gone"}, {From: "d", To: "o3"},
		{From: "start", To: "flt"}, {From: "start", To: "agg"}}
	var all []string
	for _, i := range Validate(f, nil) {
		all = append(all, i.Level+": "+i.Message)
	}
	got := strings.Join(all, "\n")
	for _, want := range []string{
		"error: d › B: add at least one condition",
		"no longer exists (gone)",
		"warning: d: the connection to o3 is not on a path",
		`warning: d: nothing is connected to the path "B"`,
		"error: flt: condition 1: choose a comparison",
		"error: agg: add a value to calculate",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "this.y") {
		t.Errorf("{{this.y}} is fine in a decision:\n%s", got)
	}
}
