package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chartOf(t *testing.T, raw string) (ChartSpec, string) {
	t.Helper()
	var a map[string]any
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	s, err := parseChart(args(a))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChartPNG(s); err != nil {
		t.Fatal(err)
	}
	return s, ChartSVG(s, "")
}

func TestKPIGaugeBulletSparklines(t *testing.T) {
	// kpi: the value, and the change against the second series (green up, red down)
	s, svg := chartOf(t, `{"type":"tiles","unit":"$","rows":[{"m":"Revenue","now":182400,"prev":161000},{"m":"Refunds","now":3100,"prev":4200}],"x":"m","y":["now","prev"]}`)
	if s.Type != "kpi" || !strings.Contains(svg, ">$182k<") || !strings.Contains(svg, "+13.3% vs prev") || !strings.Contains(svg, "-26.2% vs prev") {
		t.Fatal(svg)
	}
	if !strings.Contains(svg, colGood) || !strings.Contains(svg, colBad) || s.Height >= 480 {
		t.Fatalf("colours and a fitted height: %d", s.Height)
	}
	// lower is better: a fall is good
	_, svg = chartOf(t, `{"type":"kpi","lower_is_better":true,"labels":["Refunds"],"series":[{"name":"now","values":[3100]},{"name":"prev","values":[4200]}]}`)
	if !strings.Contains(svg, `fill="`+colGood+`" font-weight="600">-26.2%`) {
		t.Fatal(svg)
	}
	// gauge: bands colour the value; percent scale ends at 100
	s, svg = chartOf(t, `{"type":"gauge","unit":"%","bands":"90, 98","labels":["A","B"],"series":[{"name":"v","values":[99.2,85]},{"name":"target","values":[99,95]}]}`)
	if lo, hi := s.span(); lo != 0 || hi != 100 || len(s.Bands) != 2 || !strings.Contains(svg, ">99.2%<") || !strings.Contains(svg, colBad) {
		t.Fatalf("%v %v %v", lo, hi, s.Bands)
	}
	// bullet: rows, a target mark, lower_is_better zones
	_, svg = chartOf(t, `{"type":"bullet","unit":" ms","bands":[200,400],"lower_is_better":true,"rows":[{"e":"search","p95":180,"budget":250}],"x":"e","y":["p95","budget"]}`)
	if !strings.Contains(svg, ">180 ms<") || !strings.Contains(svg, ">budget<") || !strings.Contains(svg, lightGood) {
		t.Fatal(svg)
	}
	// sparklines from long rows: a line per value of y
	s, svg = chartOf(t, `{"type":"sparklines","rows":[{"d":"1","svc":"api","ms":120},{"d":"2","svc":"api","ms":60},{"d":"1","svc":"db","ms":40},{"d":"2","svc":"db","ms":80}],"x":"d","y":"svc","value":"ms","lower_is_better":true}`)
	if len(s.Series) != 2 || s.XLabel != "" || !strings.Contains(svg, ">-50.0%<") || !strings.Contains(svg, ">+100.0%<") {
		t.Fatal(svg)
	}
	// limits
	var a map[string]any
	_ = json.Unmarshal([]byte(`{"type":"gauge","values":[1,2,3,4,5,6,7,8,9]}`), &a)
	if _, err := parseChart(args(a)); err == nil || !strings.Contains(err.Error(), "at most 8 gauges") {
		t.Fatal(err)
	}
	// the round trip a PNG export uses
	if back, err := ChartFromSVG(svg); err != nil || back.Type != "sparklines" || !back.LowerBetter {
		t.Fatalf("%+v %v", back, err)
	}
}

func TestTableTool(t *testing.T) {
	rows := `[{"region":"EU","revenue":182400.5,"orders":1204,"margin":0.31,"p95":180,"trend":[1,3,2]},{"region":"US","revenue":161000,"orders":1290,"margin":0.18,"p95":420,"trend":[3,2,1]},{"region":"APAC","revenue":-5,"orders":388,"margin":0.24,"p95":260,"trend":[]}]`
	cols := `["region",{"field":"revenue","format":"currency","decimals":0},{"field":"orders","format":"bar","total":"avg"},{"field":"margin","format":"percent","bands":[0.2,0.3]},{"field":"p95","format":"duration_ms","rules":[{"op":">","value":300,"color":"bad","row":true}]},{"field":"trend","format":"sparkline"}]`
	page, err := PreviewTable(map[string]any{"rows": rows, "columns": cols, "sort": "-revenue", "totals": "true", "title": "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{TableMarker, "<caption>Regions", ">$182,400<", ">-$5<", ">31.0%<", ">420ms<", `<tr style="background:#ffebe9">`, "<svg class=\"spark\"", ">$343,396<", ">960.67<", "Total"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s in\n%s", want, page)
		}
	}
	if strings.Index(page, ">EU<") > strings.Index(page, ">US<") || strings.Index(page, ">US<") > strings.Index(page, ">APAC<") {
		t.Fatal("sorted by revenue, largest first")
	}
	// formats guessed; markdown
	md, err := PreviewTable(map[string]any{"rows": `{"rows":[{"name":"a","size":2048,"when":"2026-11-02T10:00:00Z"}]}`, "formats": `{"size":"bytes"}`, "format": "markdown"})
	if err != nil || !strings.Contains(md, "| a | 2026-11-02 | 2.0 KB |") || !strings.Contains(md, "---:") {
		t.Fatalf("%q %v", md, err)
	}
	for _, bad := range []map[string]any{{"rows": "[]"}, {"rows": "[1,2]"}, {"rows": "nope"}, {"rows": `[{"a":1}]`, "columns": `[{"field":"a","format":"weird"}]`}} {
		if _, err := PreviewTable(bad); err == nil {
			t.Fatalf("%v should be refused", bad)
		}
	}
	// the tool saves the page
	b := New(Config{})
	b.SetRoot(t.TempDir())
	out, err := tableTool(context.Background(), b, args{"rows": rows, "path": "out/r"})
	if err != nil || !strings.Contains(out, TableMarker) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.root, "out", "r.html")); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardTool(t *testing.T) {
	kpi, _ := parseChart(args{"type": "kpi", "title": "Now", "labels": []any{"A"}, "values": []any{1.0}})
	table, _ := PreviewTable(map[string]any{"rows": `[{"a":1}]`, "title": "T"})
	page, err := PreviewDashboard(map[string]any{
		"title": "Weekly", "intro": "**hi**", "columns": "3", "wide": "[4]",
		"panel_1": ChartSVG(kpi, ""), "panel_2": table, "panel_3": `[{"x":1,"y":2}]`,
		"panel_4": "<html><body><b>bold</b><script>alert(1)</script><i onclick=\"x()\">i</i></body></html>",
		"panel_6": "# Notes\n- one", "titles": "|||Raw|ignored|Notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{DashboardMarker, "<h1>Weekly</h1>", "<strong>hi</strong>", "--cols:3", `class="panel chart"`, `class="panel table"`, `class="panel rows"`, `class="panel html wide"`, "<h2>Raw</h2>", "<h2>Notes</h2>", "<b>bold</b>", `data-specwright-panel="chart"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, bad := range []string{ChartMarker, "alert(1)", `onclick="x()"`} {
		if strings.Contains(page, bad) {
			t.Fatalf("%s: no chart of its own, no scripts from panels", bad)
		}
	}
	if _, err := PreviewDashboard(map[string]any{"title": "x"}); err == nil {
		t.Fatal("a dashboard needs a panel")
	}
}
