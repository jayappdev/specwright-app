package builtin

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChartTool(t *testing.T) {
	root := t.TempDir()
	b := New(Config{})
	b.SetRoot(root)
	call := func(a map[string]any) (string, error) { return b.Call(context.Background(), "chart", a) }

	// a multi-line chart from rows: the label field and every number field
	rows := `[{"month":"Jan","sales":12,"returns":3},{"month":"Feb","sales":18,"returns":"4"},{"month":"Mar","sales":15,"returns":null}]`
	svg, err := call(map[string]any{"type": "multiline", "title": "Sales & returns", "rows": rows, "format": "both", "path": "out/sales"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<svg", ChartMarker, "<polyline", "Sales &amp; returns", ">Jan<", ">returns<", ">sales<", "out/sales.svg and out/sales.png"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("svg lacks %q:\n%.600s", want, svg)
		}
	}
	for _, f := range []string{"out/sales.svg", "out/sales.png"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("%s not saved: %v", f, err)
		}
	}
	// the PNG from the SVG's data is the same chart, twice the size
	spec, err := ChartFromSVG(svg)
	if err != nil || spec.Type != "line" || len(spec.Series) != 2 || spec.Labels[1] != "Feb" || spec.Series[0].Values[2] != nil && spec.Series[0].Name == "returns" {
		t.Fatalf("%+v %v", spec, err)
	}
	pngData, err := ChartPNG(spec)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil || img.Bounds().Dx() != 1600 || img.Bounds().Dy() != 960 {
		t.Fatalf("png: %v %v", img.Bounds(), err)
	}
	// a bar chart from labels and series, negative values too
	svg, err = call(map[string]any{"type": "bar", "labels": []any{"A", "B", "C"}, "series": []any{
		map[string]any{"name": "2025", "values": []any{3.0, -2.0, 5.0}}, map[string]any{"name": "2026", "values": []any{4.0, 1.0, 6.0}}}})
	if err != nil || strings.Count(svg, "<rect") < 7 || !strings.Contains(svg, ">2026<") {
		t.Fatalf("bar: %v\n%.400s", err, svg)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "charts")); err != nil {
		t.Fatal("the default folder out/charts was not used")
	}
	// stacked, from data as JSON text
	if svg, err := call(map[string]any{"type": "stacked_bar", "data": `{"labels":["x","y"],"series":[{"name":"a","values":[1,2]},{"name":"b","values":[3,4]}]}`}); err != nil || !strings.Contains(svg, "<rect") {
		t.Fatalf("stacked: %v", err)
	}
	for _, bad := range []map[string]any{
		{"type": "radar", "labels": []any{"a"}, "series": []any{map[string]any{"values": []any{1.0}}}},
		{"type": "bar"},
		{"rows": `[]`},
		{"rows": `[{"a":"x"}]`},
		{"labels": []any{"a"}, "series": []any{map[string]any{"values": []any{"n/a"}}}},
		{"labels": []any{"a", "b"}, "series": []any{map[string]any{"values": []any{1.0, 2.0}}}, "format": "gif"},
		{"labels": []any{"a", "b"}, "series": []any{map[string]any{"values": []any{1.0, 2.0}}}, "path": "../outside"},
	} {
		if _, err := call(bad); err == nil {
			t.Errorf("%v should be refused", bad)
		}
	}
	if _, err := ChartFromSVG("<svg></svg>"); err == nil {
		t.Error("an SVG without chart data should be refused")
	}
}

func TestChartKinds(t *testing.T) {
	b := New(Config{})
	b.SetRoot(t.TempDir())
	call := func(a map[string]any) (string, error) {
		a["path"] = "k"
		return b.Call(context.Background(), "chart", a)
	}
	ls := []any{"a", "bb", "c"}
	sr := []any{map[string]any{"name": "s1", "values": []any{3.0, 5.0, 2.0}}, map[string]any{"name": "s2", "values": []any{1.0, 2.0, 4.0}}}
	for typ, want := range map[string][]string{
		"hbar":         {"<rect", ">bb<"},
		"area":         {"<polygon", "fill-opacity", "<polyline"},
		"stacked_area": {"<polygon", "<polyline"},
		"pie":          {"<polygon", "% <", "60%"},
		"donut":        {"<polygon", ">10<"},
		"heatmap":      {"<rect", ">s2<"},
	} {
		svg, err := call(map[string]any{"type": typ, "labels": ls, "series": sr, "show_values": typ == "heatmap"})
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		_ = want
		for _, w := range want {
			if w == "% <" || w == "60%" {
				continue // checked below
			}
			if !strings.Contains(svg, w) {
				t.Fatalf("%s lacks %q:\n%.500s", typ, w, svg)
			}
		}
		if _, err := ChartPNG(mustSpec(t, svg)); err != nil {
			t.Fatalf("%s png: %v", typ, err)
		}
	}
	// pie: the share on each slice, the value and share in the legend
	svg, _ := call(map[string]any{"type": "pie", "labels": []any{"x", "y"}, "series": []any{map[string]any{"values": []any{3.0, 2.0}}}})
	if !strings.Contains(svg, ">60%<") || !strings.Contains(svg, "3 · 60%") {
		t.Fatalf("pie shares:\n%s", svg)
	}
	// value labels on bars
	svg, _ = call(map[string]any{"type": "bar", "labels": ls, "series": sr, "show_values": true})
	if !strings.Contains(svg, ">5<") {
		t.Fatalf("value labels:\n%.400s", svg)
	}
	// scatter: numbers on x
	svg, err := call(map[string]any{"type": "scatter", "rows": `[{"loc":120,"defects":1},{"loc":480,"defects":4}]`, "x": "loc"})
	if err != nil || strings.Count(svg, "<circle") != 2 {
		t.Fatalf("scatter: %v", err)
	}
	if _, err := call(map[string]any{"type": "scatter", "labels": ls, "series": sr}); err == nil {
		t.Fatal("a scatter with text on x should be refused")
	}
	// heatmap from rows: x, y and value, repeated cells summed
	svg, err = call(map[string]any{"type": "heatmap", "show_values": true, "x": "hour", "y": "day", "value": "n",
		"rows": `[{"day":"Mon","hour":"9","n":2},{"day":"Mon","hour":"9","n":3},{"day":"Tue","hour":"12","n":1}]`})
	if err != nil || !strings.Contains(svg, ">5<") || !strings.Contains(svg, ">Tue<") {
		t.Fatalf("heatmap rows: %v\n%.500s", err, svg)
	}
	if _, err := call(map[string]any{"type": "pie", "labels": []any{"a", "b"}, "series": []any{map[string]any{"values": []any{-1.0, 0.0}}}}); err == nil {
		t.Fatal("a pie without positive values should be refused")
	}
}

func mustSpec(t *testing.T, svg string) ChartSpec {
	t.Helper()
	s, err := ChartFromSVG(svg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
