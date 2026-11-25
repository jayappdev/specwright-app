package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseTable(t *testing.T) {
	cases := []struct {
		name, in string
		cols     string
		rows     int
	}{
		{"csv", "region,amount\nUS,100\nEU,\"1,5\"\n", "region|amount", 2},
		{"tsv", "a\tb\tc\n1\t2\t3\n", "a|b|c", 1},
		{"json array", `[{"region":"US","amount":1},{"amount":2,"region":"EU","extra":true}]`, "region|amount|extra", 2},
		{"json rows inside", `{"count":2,"rows":[{"x":1},{"x":2}]}`, "x", 2},
		{"json lines", "{\"a\":1}\n{\"a\":2,\"b\":\"z\"}\n", "a|b", 2},
		{"markdown", "| name | n |\n|---|--:|\n| a \\| b | 1 |\n| c | 2 |\n\nSome text after.", "name|n", 2},
		{"repeated and empty names", "x,x,\n1,2,3\n", "x|x_2|col3", 1},
	}
	for _, c := range cases {
		cols, rows, ok := ParseTable(c.in)
		if !ok {
			t.Errorf("%s: not read as a table", c.name)
			continue
		}
		if got := strings.Join(cols, "|"); got != c.cols || len(rows) != c.rows {
			t.Errorf("%s: columns %q rows %d, want %q %d", c.name, got, len(rows), c.cols, c.rows)
		}
	}
	_, rows, _ := ParseTable("| name | n |\n|---|--:|\n| a \\| b | 1 |\n")
	if rows[0][0] != "a | b" {
		t.Errorf("markdown cell %q", rows[0][0])
	}
	for _, text := range []string{
		"# Notes\n\nWe met on Monday, and it went well.\nThen we left, finally.",
		`{"title": "not rows", "n": 3}`,
		"just one line, with a comma",
		"",
	} {
		if cols, _, ok := ParseTable(text); ok {
			t.Errorf("%q read as a table: %v", text, cols)
		}
	}
}

func TestSplitPassages(t *testing.T) {
	long := strings.Repeat("This sentence fills the paragraph. ", 80) // ~2800 chars
	text := "# Guide\n\nIntro text.\n\n## Install\n\nRun the installer.\n\n## Use\n\n" + long + "\n\n# Other\n\nEnd."
	ps := SplitPassages(text)
	heads := map[string]bool{}
	for _, p := range ps {
		heads[p.Heading] = true
		if len(p.Text) > passageSize+1 {
			t.Errorf("a passage of %d chars", len(p.Text))
		}
	}
	for _, h := range []string{"Guide", "Guide › Install", "Guide › Use", "Other"} {
		if !heads[h] {
			t.Errorf("no passage under %q (have %v)", h, heads)
		}
	}
	if len(ps) < 6 {
		t.Errorf("%d passages: the long paragraph was not cut", len(ps))
	}
}

func TestIndexQueryAndSearch(t *testing.T) {
	root := t.TempDir()
	st := New(root)
	s, err := st.Create(Session{Flow: "sales-chat", ID: NewID(time.Now(), "abcd")})
	if err != nil {
		t.Fatal(err)
	}
	var csv strings.Builder
	csv.WriteString("region,amount,\"order date\"\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&csv, "%s,%d.5,2026-0%d-01\n", []string{"US", "EU", "APAC"}[i%3], i, i%9+1)
	}
	doc := "# Returns policy\n\nA sale can be returned within 30 days.\n\n# Shipping\n\nWe ship to Europe and the US in 5 days."
	at := time.Now()
	items := []Data{
		{Name: "setup-query.csv", Title: "Sales rows", Setup: true, Bytes: csv.Len(), At: at},
		{Name: "setup-docs.md", Title: "Policies", Setup: true, Bytes: len(doc), At: at},
		{Name: "m002-data.query.csv", Step: "data.query", Bytes: 3, At: at}, // the chat's own result: not indexed
	}
	content := map[string]string{"setup-query.csv": csv.String(), "setup-docs.md": doc}
	loads := 0
	load := func(d Data) (string, error) { loads++; return content[d.Name], nil }

	x, err := st.OpenIndex(s.Flow, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if err := x.Sync(items, load); err != nil {
		t.Fatal(err)
	}
	cat, _ := x.Catalog()
	if len(cat) != 2 {
		t.Fatalf("catalog %+v", cat)
	}
	var sales Entry
	for _, e := range cat {
		if e.Kind == "table" {
			sales = e
		}
	}
	if sales.Table != "setup_query" || sales.Rows != 500 || len(sales.Columns) != 3 || sales.Columns[1].Type != "REAL" || sales.Columns[2].Name != "order date" {
		t.Errorf("table entry %+v", sales)
	}

	cols, rows, more, err := x.Query(context.Background(), `SELECT region, round(sum(amount), 1) AS total FROM setup_query GROUP BY region ORDER BY region;`, 10)
	if err != nil || len(rows) != 3 || cols[1] != "total" || more || rows[0][0] != "APAC" {
		t.Errorf("query: %v %v %v %v", cols, rows, more, err)
	}
	if _, rows, more, _ := x.Query(context.Background(), `SELECT * FROM setup_query`, 50); len(rows) != 50 || !more {
		t.Errorf("limit: %d rows, more %v", len(rows), more)
	}
	for _, bad := range []string{
		`DELETE FROM setup_query`,
		`SELECT 1; DROP TABLE setup_query`,
		`ATTACH DATABASE '/tmp/x.db' AS x`,
		`WITH x AS (SELECT 1) DELETE FROM setup_query`,
	} {
		if _, _, _, err := x.Query(context.Background(), bad, 10); err == nil {
			t.Errorf("%q ran", bad)
		}
	}
	if _, rows, _, err := x.Query(context.Background(), `SELECT count(*) FROM setup_query WHERE region = 'a;b'`, 10); err != nil || rows[0][0] != "0" {
		t.Errorf("a ; inside a string: %v %v", rows, err)
	}

	ps, err := x.Search("Can I return my sales?", 3)
	if err != nil || len(ps) == 0 || ps[0].Heading != "Returns policy" || ps[0].Title != "Policies" {
		t.Errorf("search: %+v %v", ps, err)
	}
	if ps, _ := x.Search("the and of", 3); len(ps) != 0 {
		t.Errorf("stop words found %+v", ps)
	}

	// unchanged data is not read again; changed data is; removed data goes
	loads = 0
	if err := x.Sync(items, load); err != nil || loads != 0 {
		t.Errorf("second sync read %d items (%v)", loads, err)
	}
	content["setup-query.csv"] = "region,amount\nUS,1\n"
	items[0].Bytes, items[0].At = len(content["setup-query.csv"]), at.Add(time.Second)
	if err := x.Sync(items[:1], load); err != nil || loads != 1 {
		t.Errorf("sync after a change read %d (%v)", loads, err)
	}
	cat, _ = x.Catalog()
	if len(cat) != 1 || cat[0].Rows != 1 {
		t.Errorf("after the change %+v", cat)
	}
	if ps, _ := x.Search("return", 3); len(ps) != 0 {
		t.Errorf("a removed document is still found: %+v", ps)
	}
}
