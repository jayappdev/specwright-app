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
		{"type": "pie", "labels": []any{"a"}, "series": []any{map[string]any{"values": []any{1.0}}}},
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
