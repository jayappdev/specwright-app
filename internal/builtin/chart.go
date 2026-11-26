package builtin

// chart: bar, line and multi-line charts from data. The chart is laid out
// once (shapes and text in pixels) and drawn either as SVG (what the tool
// returns and the app shows) or as PNG (on demand: the tool's format, or
// Export PNG in the app, which reads the chart's data back from the SVG's
// metadata so the PNG is the same chart).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"context"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// ChartSpec is a chart's data and look.
type ChartSpec struct {
	// bar | hbar | stacked_bar | line | area | stacked_area | scatter | pie | donut | heatmap
	Type   string        `json:"type"`
	Title  string        `json:"title,omitempty"`
	XLabel string        `json:"x_label,omitempty"`
	YLabel string        `json:"y_label,omitempty"`
	Labels []string      `json:"labels"`
	Series []ChartSeries `json:"series"`
	Width  int           `json:"width,omitempty"`
	Height int           `json:"height,omitempty"`
	// ShowValues writes each value on its bar, point, slice or cell.
	ShowValues bool `json:"show_values,omitempty"`
	// kpi, gauge, bullet, sparklines: the scale (default from the data),
	// the thresholds between bad, so-so and good, whether lower is better,
	// and the unit written with the values ($, %, " ms").
	Min         *float64  `json:"min,omitempty"`
	Max         *float64  `json:"max,omitempty"`
	Bands       []float64 `json:"bands,omitempty"`
	LowerBetter bool      `json:"lower_is_better,omitempty"`
	Unit        string    `json:"unit,omitempty"`
}

// chartTypes: each type and the names it is also asked by.
var chartTypes = map[string][]string{
	"bar":          {"bar", "bars", "column", "columns", "vbar"},
	"hbar":         {"hbar", "horizontal_bar", "horizontal-bar", "barh", "horizontal", "horizontal bar"},
	"stacked_bar":  {"stacked", "stacked_bar", "stacked-bar", "stackedbar"},
	"line":         {"line", "lines", "multiline", "multi_line", "multi-line", "linechart"},
	"area":         {"area", "area_chart"},
	"stacked_area": {"stacked_area", "stacked-area", "stackedarea"},
	"scatter":      {"scatter", "points", "xy", "scatterplot"},
	"pie":          {"pie", "pie_chart"},
	"donut":        {"donut", "doughnut", "ring"},
	"heatmap":      {"heatmap", "heat", "heat_map", "matrix"},
	"kpi":          {"kpi", "kpis", "tiles", "stat", "stats", "scorecard", "big_number"},
	"gauge":        {"gauge", "gauges", "dial"},
	"bullet":       {"bullet", "bullets", "bullet_chart", "progress"},
	"sparklines":   {"sparklines", "sparkline", "trends", "small_multiples"},
}

// oneValue: the chart types that show each label's value on its own (a
// second series is the comparison or target, not more data).
var oneValue = map[string]bool{"kpi": true, "gauge": true, "bullet": true}

// ChartTypeNames lists the chart types, for the tool's description.
const ChartTypeNames = "bar, hbar (horizontal bars), stacked_bar, line (or multiline), area, stacked_area, scatter, pie, donut, heatmap, kpi (tiles), gauge, bullet, sparklines"

func chartType(t string) (string, bool) {
	t = strings.ToLower(strings.TrimSpace(t))
	for k, names := range chartTypes {
		for _, n := range names {
			if n == t {
				return k, true
			}
		}
	}
	return "", false
}

// ChartSeries is one named row of values (one bar colour, one line).
type ChartSeries struct {
	Name   string     `json:"name"`
	Values []*float64 `json:"values"` // nil: no value at that label
}

const (
	maxChartPoints = 2000
	maxChartSeries = 12
)

var chartPalette = []string{"#4e79a7", "#f28e2b", "#e15759", "#76b7b2", "#59a14f", "#edc948", "#b07aa1", "#ff9da7", "#9c755f", "#bab0ac", "#1f77b4", "#2ca02c"}

// parseChart reads the tool's arguments: labels and series, or rows
// (a JSON array of objects) with the x field and the y field(s).
func parseChart(a args) (ChartSpec, error) {
	s := ChartSpec{Title: a.str("title"), XLabel: a.str("x_label"), YLabel: a.str("y_label"),
		Width: a.num("width", 800, 320, 2400), Height: a.num("height", 480, 240, 1600), ShowValues: a.flag("show_values")}
	t, ok := chartType(firstStr(a.str("type"), "bar"))
	if !ok {
		return s, fmt.Errorf("type %q: use %s", a.str("type"), ChartTypeNames)
	}
	s.Type = t
	data := any(a)
	if d, ok := a["data"]; ok { // everything inside data, as an object or its JSON text
		switch x := d.(type) {
		case string:
			var v any
			if err := json.Unmarshal([]byte(strings.TrimSpace(x)), &v); err != nil {
				return s, fmt.Errorf("data is not JSON: %v", err)
			}
			data = v
		default:
			data = x
		}
	}
	get := func(k string) any {
		if m, ok := data.(map[string]any); ok {
			return m[k]
		}
		if m, ok := data.(args); ok {
			return m[k]
		}
		return nil
	}
	rows := get("rows")
	if arr, ok := data.([]any); ok { // data is the rows themselves
		rows = arr
	}
	if r, ok := rows.(string); ok {
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(r)), &v); err != nil {
			return s, fmt.Errorf("rows is not JSON: %v", err)
		}
		rows = v
	}
	if list, ok := rows.([]any); ok {
		x := firstStr(strField(get("x")), a.str("x"))
		ys := strList(get("y"))
		if len(ys) == 0 {
			ys = strList(a["y"])
		}
		var err error
		value := firstStr(strField(get("value")), a.str("value"))
		if s.Type == "heatmap" || s.Type == "sparklines" && value != "" {
			xl, yl := s.XLabel, s.YLabel
			err = heatFromRows(&s, list, x, firstStr(strField(get("y")), a.str("y")), value)
			if s.Type == "sparklines" { // a line per row value; no axis captions
				s.XLabel, s.YLabel = xl, yl
			}
		} else {
			err = fromRows(&s, list, x, ys)
		}
		if err != nil {
			return s, err
		}
	} else {
		s.Labels = strList(get("labels"))
		for _, raw := range anyList(get("series")) {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			sr := ChartSeries{Name: strField(m["name"])}
			for _, v := range anyList(m["values"]) {
				sr.Values = append(sr.Values, number(v))
			}
			s.Series = append(s.Series, sr)
		}
		if len(s.Series) == 0 {
			if vals := anyList(get("values")); len(vals) > 0 { // one series, plain
				sr := ChartSeries{Name: firstStr(s.YLabel, "value")}
				for _, v := range vals {
					sr.Values = append(sr.Values, number(v))
				}
				s.Series = []ChartSeries{sr}
			}
		}
	}
	s.Unit = a.str("unit")
	s.LowerBetter = a.flag("lower_is_better")
	s.Min, s.Max = number(a["min"]), number(a["max"])
	for _, v := range floatList(a["bands"]) {
		s.Bands = append(s.Bands, v)
	}
	sort.Float64s(s.Bands)
	if err := checkChart(&s); err != nil {
		return s, err
	}
	if a["height"] == nil { // sized to what it shows
		top := 24.0
		if s.Title != "" {
			top += 24
		}
		n := float64(len(s.Labels))
		switch s.Type {
		case "kpi", "gauge":
			L := &chartLayout{W: float64(s.Width), top: top}
			_, rows, cw, _ := L.grid(len(s.Labels))
			per := 130.0
			if s.Type == "gauge" {
				per = math.Min(cw/2-16, 150) + 76
			}
			s.Height = int(top + 16 + float64(rows)*per + float64(rows-1)*16)
		case "bullet":
			s.Height = int(top + n*40 + 70)
		case "sparklines":
			s.Height = int(top + float64(len(s.Series))*40 + 20)
		}
		s.Height = max(120, min(s.Height, 1600))
	}
	return s, nil
}

// floatList reads numbers given as a list, JSON text or "60, 80".
func floatList(v any) []float64 {
	if t, ok := v.(string); ok {
		t = strings.TrimSpace(t)
		var l []any
		if json.Unmarshal([]byte(t), &l) == nil {
			v = l
		} else {
			for _, f := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
				l = append(l, f)
			}
			v = l
		}
	}
	var out []float64
	for _, x := range anyList(v) {
		if n := number(x); n != nil {
			out = append(out, *n)
		}
	}
	return out
}

func fromRows(s *ChartSpec, rows []any, x string, ys []string) error {
	if len(rows) == 0 {
		return errors.New("rows is empty: there is nothing to chart")
	}
	first, _ := rows[0].(map[string]any)
	if first == nil {
		return errors.New("rows must be objects, e.g. [{\"month\": \"Jan\", \"sales\": 12}]")
	}
	if x == "" { // the first text field (a scatter: the first number field)
		for _, k := range sortedFields(first) {
			if _, isNum := first[k].(float64); isNum == (s.Type == "scatter") {
				x = k
				break
			}
		}
	}
	if len(ys) == 0 { // every number field
		for _, k := range sortedFields(first) {
			if _, isNum := first[k].(float64); isNum && k != x {
				ys = append(ys, k)
			}
		}
	}
	if x == "" || len(ys) == 0 {
		return errors.New("say which field is x (the labels) and which are y (the numbers)")
	}
	for _, y := range ys {
		s.Series = append(s.Series, ChartSeries{Name: y})
	}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		s.Labels = append(s.Labels, cellText(lookupField(m, x)))
		for i, y := range ys {
			s.Series[i].Values = append(s.Series[i].Values, number(lookupField(m, y)))
		}
	}
	return nil
}

// heatFromRows builds a heatmap's grid from rows: x the column field, y
// the row field, value the number (summed where rows repeat a cell).
func heatFromRows(s *ChartSpec, rows []any, x, y, value string) error {
	if len(rows) == 0 {
		return errors.New("rows is empty: there is nothing to chart")
	}
	first, _ := rows[0].(map[string]any)
	if first == nil {
		return errors.New("rows must be objects, e.g. [{\"day\": \"Mon\", \"hour\": \"9\", \"builds\": 4}]")
	}
	var texts, nums []string
	for _, k := range sortedFields(first) {
		if _, isNum := first[k].(float64); isNum {
			nums = append(nums, k)
		} else {
			texts = append(texts, k)
		}
	}
	if x == "" && len(texts) > 0 {
		x = texts[0]
	}
	if y == "" {
		for _, k := range append(texts, nums...) {
			if k != x && k != value {
				y = k
				break
			}
		}
	}
	if value == "" {
		for _, k := range nums {
			if k != x && k != y {
				value = k
				break
			}
		}
	}
	if x == "" || y == "" || value == "" {
		return errors.New("a heatmap from rows needs x (the columns), y (the rows) and value (the number)")
	}
	col, row := map[string]int{}, map[string]int{}
	cells := map[[2]int]float64{}
	seen := map[[2]int]bool{}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		cx, cy := cellText(lookupField(m, x)), cellText(lookupField(m, y))
		if _, ok := col[cx]; !ok {
			col[cx] = len(s.Labels)
			s.Labels = append(s.Labels, cx)
		}
		if _, ok := row[cy]; !ok {
			row[cy] = len(s.Series)
			s.Series = append(s.Series, ChartSeries{Name: cy})
		}
		if v := number(lookupField(m, value)); v != nil {
			k := [2]int{row[cy], col[cx]}
			cells[k] += *v
			seen[k] = true
		}
	}
	for i := range s.Series {
		s.Series[i].Values = make([]*float64, len(s.Labels))
		for j := range s.Labels {
			if k := [2]int{i, j}; seen[k] {
				v := cells[k]
				s.Series[i].Values[j] = &v
			}
		}
	}
	if s.YLabel == "" {
		s.YLabel = y
	}
	if s.XLabel == "" {
		s.XLabel = x
	}
	return nil
}

// lookupField reads a field, or a dotted path (customer.name).
func lookupField(m map[string]any, path string) any {
	var cur any = m
	for _, k := range strings.Split(path, ".") {
		o, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = o[k]
	}
	return cur
}

func checkChart(s *ChartSpec) error {
	if len(s.Series) == 0 {
		return errors.New("give the data: labels and series ([{name, values}]), or rows with x and y")
	}
	if len(s.Series) > maxChartSeries && s.Type != "heatmap" && s.Type != "sparklines" {
		return fmt.Errorf("at most %d series in one chart", maxChartSeries)
	}
	if s.Type == "sparklines" && len(s.Series) > 40 {
		return errors.New("at most 40 sparklines in one chart")
	}
	if lim := map[string]int{"kpi": 24, "gauge": 8, "bullet": 40}[s.Type]; lim > 0 {
		if len(s.Series) > 2 {
			s.Series = s.Series[:2] // the value, and what it is compared with
		}
		if n := max(len(s.Labels), len(s.Series[0].Values)); n > lim {
			return fmt.Errorf("at most %d %s in one chart (there are %d)", lim, map[string]string{"kpi": "tiles", "gauge": "gauges", "bullet": "bars"}[s.Type], n)
		}
	}
	if s.Type == "heatmap" && len(s.Series) > 200 {
		return errors.New("at most 200 rows in a heatmap")
	}
	n := len(s.Labels)
	for _, sr := range s.Series {
		n = max(n, len(sr.Values))
	}
	if n == 0 {
		return errors.New("the series have no values")
	}
	if n > maxChartPoints {
		return fmt.Errorf("at most %d points per series", maxChartPoints)
	}
	for len(s.Labels) < n {
		s.Labels = append(s.Labels, strconv.Itoa(len(s.Labels)+1))
	}
	for i := range s.Series {
		if strings.TrimSpace(s.Series[i].Name) == "" {
			s.Series[i].Name = fmt.Sprintf("Series %d", i+1)
		}
		for len(s.Series[i].Values) < n {
			s.Series[i].Values = append(s.Series[i].Values, nil)
		}
	}
	any := false
	for _, sr := range s.Series {
		for _, v := range sr.Values {
			any = any || v != nil
		}
	}
	if !any {
		return errors.New("the series have no numbers")
	}
	switch s.Type {
	case "scatter":
		for _, l := range s.Labels {
			if number(l) == nil {
				return fmt.Errorf("a scatter chart needs numbers on the x axis, not %q: choose a number field as x", l)
			}
		}
	case "pie", "donut":
		sum := 0.0
		for _, v := range s.Series[0].Values {
			if v != nil && *v > 0 {
				sum += *v
			}
		}
		if sum <= 0 {
			return errors.New("a pie needs positive values")
		}
	}
	return nil
}

func number(v any) *float64 {
	switch x := v.(type) {
	case float64:
		return &x
	case int:
		f := float64(x)
		return &f
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return &f
		}
	case string:
		t := strings.TrimSpace(strings.TrimSuffix(strings.ReplaceAll(x, ",", ""), "%"))
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return &f
		}
	}
	return nil
}

func anyList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case string:
		var a []any
		if json.Unmarshal([]byte(strings.TrimSpace(x)), &a) == nil {
			return a
		}
	}
	return nil
}

func strList(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, cellText(e))
		}
	case []string:
		out = x
	case string:
		if a := anyList(x); a != nil {
			return strList(a)
		}
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func strField(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func sortedFields(m map[string]any) []string {
	// JSON objects come unordered: keep a stable order (alphabetical)
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j] < ks[j-1]; j-- {
			ks[j], ks[j-1] = ks[j-1], ks[j]
		}
	}
	return ks
}

// --- layout -----------------------------------------------------------------

type prim struct {
	kind   string // rect | line | path | poly | circle | text
	x, y   float64
	w, h   float64
	x2, y2 float64
	pts    [][2]float64
	color  string
	width  float64
	dash   bool
	text   string
	size   float64
	anchor string // start | middle | end
	bold   bool
	// poly: a filled polygon (pts); opacity 0 is opaque; stroke an outline
	opacity float64
	stroke  string
}

func niceStep(span float64, ticks int) float64 {
	if span <= 0 {
		return 1
	}
	raw := span / float64(ticks)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*mag >= raw {
			return m * mag
		}
	}
	return 10 * mag
}

func fmtNum(v float64) string {
	a := math.Abs(v)
	short := func(x float64) string { return strconv.FormatFloat(math.Round(x*100)/100, 'f', -1, 64) }
	switch {
	case a >= 1e9:
		return short(v/1e9) + "B"
	case a >= 1e6:
		return short(v/1e6) + "M"
	case a >= 1e4:
		return short(v/1e3) + "k"
	}
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}

func chartClip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// layout places everything in pixels.
func layout(s ChartSpec) []prim {
	L := &chartLayout{s: s, W: float64(s.Width), H: float64(s.Height)}
	L.header()
	switch s.Type {
	case "pie", "donut":
		L.pie()
	case "heatmap":
		L.heatmap()
	case "hbar":
		L.hbar()
	case "scatter":
		L.scatter()
	case "kpi":
		L.kpi()
	case "gauge":
		L.gauge()
	case "bullet":
		L.bullet()
	case "sparklines":
		L.sparklines()
	default:
		L.axes()
	}
	return L.ps
}

type chartLayout struct {
	s    ChartSpec
	W, H float64
	top  float64 // where the plot may start, under the title, legend and caption
	ps   []prim
}

func (L *chartLayout) text(x, y float64, t string, size float64, anchor string, bold bool, col string) {
	L.ps = append(L.ps, prim{kind: "text", x: x, y: y, text: t, size: size, anchor: anchor, bold: bold, color: col})
}

func (L *chartLayout) add(p prim) { L.ps = append(L.ps, p) }

func seriesColor(i int) string { return chartPalette[i%len(chartPalette)] }

// header: the title, the legend (one entry per series; a pie's are its
// slices, a heatmap has its own scale) and the y caption.
func (L *chartLayout) header() {
	s := L.s
	L.top = 20
	if s.Title != "" {
		L.text(L.W/2, 28, chartClip(s.Title, 90), 18, "middle", true, "#1f2328")
		L.top = 44
	}
	if len(s.Series) > 1 && s.Type != "pie" && s.Type != "donut" && s.Type != "heatmap" && s.Type != "sparklines" && !oneValue[s.Type] {
		x, y := 64.0, L.top+8
		for i, sr := range s.Series {
			label := chartClip(sr.Name, 24)
			wid := 22 + float64(len([]rune(label)))*7.2 + 18
			if x+wid > L.W-16 {
				x, y = 64, y+20
			}
			L.add(prim{kind: "rect", x: x, y: y - 9, w: 12, h: 12, color: seriesColor(i)})
			L.text(x+18, y+1, label, 12, "start", false, "#3a3f45")
			x += wid
		}
		L.top = y + 20
	}
	if s.YLabel != "" && s.Type != "pie" && s.Type != "donut" && !oneValue[s.Type] && s.Type != "sparklines" {
		L.text(16, L.top+4, chartClip(s.YLabel, 60), 12, "start", true, "#57606a")
		L.top += 14
	}
}

// scale is a nice value axis over [lo, hi].
type scale struct{ lo, hi, step float64 }

func niceScale(lo, hi float64, fromZero bool) scale {
	if fromZero || lo > 0 && hi > 0 && lo/hi < 0.5 {
		lo = math.Min(lo, 0)
	}
	if hi <= lo {
		hi = lo + 1
	}
	step := niceStep(hi-lo, 5)
	return scale{math.Floor(lo/step) * step, math.Ceil(hi/step) * step, step}
}

func (sc scale) ticks() []float64 {
	var out []float64
	for v := sc.lo; v <= sc.hi+sc.step/2; v += sc.step {
		out = append(out, v)
	}
	return out
}

// valueRange: the smallest and largest value (stacked: the sums).
func valueRange(s ChartSpec, stacked bool) (float64, float64) {
	lo, hi, first := 0.0, 0.0, true
	for i := range s.Labels {
		pos, neg := 0.0, 0.0
		for _, sr := range s.Series {
			v := sr.Values[i]
			if v == nil {
				continue
			}
			if stacked {
				if *v >= 0 {
					pos += *v
				} else {
					neg += *v
				}
				continue
			}
			if first {
				lo, hi, first = *v, *v, false
			}
			lo, hi = math.Min(lo, *v), math.Max(hi, *v)
		}
		if stacked {
			lo, hi = math.Min(lo, neg), math.Max(hi, pos)
		}
	}
	return lo, hi
}

// axes: bar, stacked_bar, line, area and stacked_area, labels along x.
func (L *chartLayout) axes() {
	s := L.s
	left, right, bottom := 64.0, 24.0, 52.0
	if s.XLabel != "" {
		bottom += 18
	}
	pw, ph := L.W-left-right, L.H-L.top-bottom-8
	py := L.top + 8
	stacked := s.Type == "stacked_bar" || s.Type == "stacked_area"
	lo, hi := valueRange(s, stacked)
	sc := niceScale(lo, hi, s.Type != "line")
	ys := func(v float64) float64 { return py + ph - (v-sc.lo)/(sc.hi-sc.lo)*ph }
	for _, v := range sc.ticks() {
		L.add(prim{kind: "line", x: left, y: ys(v), x2: left + pw, y2: ys(v), color: "#e6e8eb", width: 1})
		L.text(left-8, ys(v)+4, fmtNum(v), 11, "end", false, "#57606a")
	}
	if sc.lo < 0 {
		L.add(prim{kind: "line", x: left, y: ys(0), x2: left + pw, y2: ys(0), color: "#8c959f", width: 1})
	}
	L.add(prim{kind: "line", x: left, y: py + ph, x2: left + pw, y2: py + ph, color: "#8c959f", width: 1})
	n := len(s.Labels)
	band := pw / float64(n)
	xc := func(i int) float64 { return left + band*(float64(i)+0.5) }
	every := max(int(math.Ceil(float64(n)*64/pw)), 1)
	for i := 0; i < n; i += every {
		L.text(xc(i), py+ph+18, chartClip(s.Labels[i], max(4, int(band*float64(every)/7))), 11, "middle", false, "#57606a")
	}
	if s.XLabel != "" {
		L.text(left+pw/2, L.H-12, chartClip(s.XLabel, 80), 12, "middle", true, "#57606a")
	}
	valueText := func(x, y float64, v float64) {
		L.text(x, y, fmtNum(v), 10, "middle", false, "#3a3f45")
	}
	base := ys(math.Max(sc.lo, math.Min(0, sc.hi)))
	switch s.Type {
	case "bar":
		k := float64(len(s.Series))
		bw := math.Max(1, band*0.8/k)
		for si, sr := range s.Series {
			for i, v := range sr.Values {
				if v == nil {
					continue
				}
				x := left + band*float64(i) + band*0.1 + bw*float64(si)
				y0, y1 := ys(0), ys(*v)
				L.add(prim{kind: "rect", x: x, y: math.Min(y0, y1), w: math.Max(bw-1, 1), h: math.Abs(y0 - y1), color: seriesColor(si)})
				if s.ShowValues && bw >= 14 {
					if *v >= 0 {
						valueText(x+bw/2, y1-4, *v)
					} else {
						valueText(x+bw/2, y1+12, *v)
					}
				}
			}
		}
	case "stacked_bar":
		bw := math.Max(1, band*0.7)
		for i := 0; i < n; i++ {
			pos, neg := 0.0, 0.0
			for si, sr := range s.Series {
				v := sr.Values[i]
				if v == nil || *v == 0 {
					continue
				}
				from := pos
				if *v < 0 {
					from = neg
				}
				y0, y1 := ys(from), ys(from+*v)
				L.add(prim{kind: "rect", x: xc(i) - bw/2, y: math.Min(y0, y1), w: bw, h: math.Abs(y0 - y1), color: seriesColor(si)})
				if s.ShowValues && math.Abs(y0-y1) >= 14 && bw >= 18 {
					L.text(xc(i), (y0+y1)/2+4, fmtNum(*v), 10, "middle", false, "#ffffff")
				}
				if *v < 0 {
					neg += *v
				} else {
					pos += *v
				}
			}
			if s.ShowValues && bw >= 18 && pos > 0 {
				valueText(xc(i), ys(pos)-4, pos)
			}
		}
	case "line", "area", "stacked_area":
		cum := make([]float64, n) // stacked: the sums so far
		for si, sr := range s.Series {
			c := seriesColor(si)
			// the area under the line (to the axis, or to the series below)
			if s.Type != "line" {
				var seg [][2]float64
				var lower []float64
				flush := func() {
					if len(seg) > 1 {
						poly := append([][2]float64{}, seg...)
						for k := len(seg) - 1; k >= 0; k-- {
							poly = append(poly, [2]float64{seg[k][0], lower[k]})
						}
						L.add(prim{kind: "poly", pts: poly, color: c, opacity: 0.28})
					}
					seg, lower = nil, nil
				}
				for i, v := range sr.Values {
					if v == nil {
						flush()
						continue
					}
					bottomY, topV := base, *v
					if s.Type == "stacked_area" {
						bottomY, topV = ys(cum[i]), cum[i]+*v
					}
					seg = append(seg, [2]float64{xc(i), ys(topV)})
					lower = append(lower, bottomY)
				}
				flush()
			}
			var seg [][2]float64
			flush := func() {
				if len(seg) > 1 {
					L.add(prim{kind: "path", pts: seg, color: c, width: 2.2})
				}
				seg = nil
			}
			for i, v := range sr.Values {
				if v == nil {
					flush()
					continue
				}
				y := *v
				if s.Type == "stacked_area" {
					y = cum[i] + *v
				}
				seg = append(seg, [2]float64{xc(i), ys(y)})
			}
			flush()
			for i, v := range sr.Values {
				if v == nil {
					continue
				}
				y := *v
				if s.Type == "stacked_area" {
					y = cum[i] + *v
					cum[i] = y
				}
				if n <= 60 {
					L.add(prim{kind: "circle", x: xc(i), y: ys(y), w: 3, color: c})
				}
				if s.ShowValues && n <= 40 {
					valueText(xc(i), ys(y)-8, *v)
				}
			}
		}
	}
}

// hbar: horizontal bars, the labels down the left (long names fit).
func (L *chartLayout) hbar() {
	s := L.s
	longest := 0
	for _, l := range s.Labels {
		longest = max(longest, len([]rune(chartClip(l, 32))))
	}
	left := math.Min(260, math.Max(60, float64(longest)*6.6+16))
	right, bottom := 40.0, 36.0
	if s.XLabel != "" {
		bottom += 18
	}
	pw, ph := L.W-left-right, L.H-L.top-bottom-8
	py := L.top + 8
	lo, hi := valueRange(s, false)
	sc := niceScale(lo, hi, true)
	xs := func(v float64) float64 { return left + (v-sc.lo)/(sc.hi-sc.lo)*pw }
	for _, v := range sc.ticks() {
		L.add(prim{kind: "line", x: xs(v), y: py, x2: xs(v), y2: py + ph, color: "#e6e8eb", width: 1})
		L.text(xs(v), py+ph+16, fmtNum(v), 11, "middle", false, "#57606a")
	}
	L.add(prim{kind: "line", x: xs(0), y: py, x2: xs(0), y2: py + ph, color: "#8c959f", width: 1})
	n := len(s.Labels)
	band := ph / float64(n)
	k := float64(len(s.Series))
	bh := math.Max(1, band*0.8/k)
	every := max(int(math.Ceil(float64(n)*14/ph)), 1)
	for i := 0; i < n; i += every {
		L.text(left-8, py+band*(float64(i)+0.5)+4, chartClip(s.Labels[i], 32), 11, "end", false, "#3a3f45")
	}
	for si, sr := range s.Series {
		for i, v := range sr.Values {
			if v == nil {
				continue
			}
			y := py + band*float64(i) + band*0.1 + bh*float64(si)
			x0, x1 := xs(0), xs(*v)
			L.add(prim{kind: "rect", x: math.Min(x0, x1), y: y, w: math.Abs(x1 - x0), h: math.Max(bh-1, 1), color: seriesColor(si)})
			if s.ShowValues && bh >= 10 {
				if *v >= 0 {
					L.text(x1+4, y+bh/2+4, fmtNum(*v), 10, "start", false, "#3a3f45")
				} else {
					L.text(x1-4, y+bh/2+4, fmtNum(*v), 10, "end", false, "#3a3f45")
				}
			}
		}
	}
	if s.XLabel != "" {
		L.text(left+pw/2, L.H-12, chartClip(s.XLabel, 80), 12, "middle", true, "#57606a")
	}
}

// scatter: numbers on both axes; each series a colour of points.
func (L *chartLayout) scatter() {
	s := L.s
	left, right, bottom := 64.0, 24.0, 44.0
	if s.XLabel != "" {
		bottom += 18
	}
	pw, ph := L.W-left-right, L.H-L.top-bottom-8
	py := L.top + 8
	xlo, xhi, first := 0.0, 0.0, true
	for _, l := range s.Labels {
		if v := number(l); v != nil {
			if first {
				xlo, xhi, first = *v, *v, false
			}
			xlo, xhi = math.Min(xlo, *v), math.Max(xhi, *v)
		}
	}
	xsc := niceScale(xlo, xhi, false)
	lo, hi := valueRange(s, false)
	ysc := niceScale(lo, hi, false)
	xs := func(v float64) float64 { return left + (v-xsc.lo)/(xsc.hi-xsc.lo)*pw }
	ys := func(v float64) float64 { return py + ph - (v-ysc.lo)/(ysc.hi-ysc.lo)*ph }
	for _, v := range ysc.ticks() {
		L.add(prim{kind: "line", x: left, y: ys(v), x2: left + pw, y2: ys(v), color: "#e6e8eb", width: 1})
		L.text(left-8, ys(v)+4, fmtNum(v), 11, "end", false, "#57606a")
	}
	for _, v := range xsc.ticks() {
		L.add(prim{kind: "line", x: xs(v), y: py, x2: xs(v), y2: py + ph, color: "#f0f1f3", width: 1})
		L.text(xs(v), py+ph+18, fmtNum(v), 11, "middle", false, "#57606a")
	}
	L.add(prim{kind: "line", x: left, y: py + ph, x2: left + pw, y2: py + ph, color: "#8c959f", width: 1})
	L.add(prim{kind: "line", x: left, y: py, x2: left, y2: py + ph, color: "#8c959f", width: 1})
	for si, sr := range s.Series {
		for i, v := range sr.Values {
			x := number(s.Labels[i])
			if v == nil || x == nil {
				continue
			}
			L.add(prim{kind: "circle", x: xs(*x), y: ys(*v), w: 4, color: seriesColor(si), opacity: 0.8})
			if s.ShowValues && len(s.Labels) <= 40 {
				L.text(xs(*x)+6, ys(*v)-6, fmtNum(*v), 10, "start", false, "#3a3f45")
			}
		}
	}
	if s.XLabel != "" {
		L.text(left+pw/2, L.H-12, chartClip(s.XLabel, 80), 12, "middle", true, "#57606a")
	}
}

// pie and donut: the first series, one slice per label; the legend on the
// right with each slice's value and share.
func (L *chartLayout) pie() {
	s := L.s
	vals := s.Series[0].Values
	total := 0.0
	type slice struct {
		label string
		v     float64
		i     int
	}
	var sl []slice
	for i, v := range vals {
		if v != nil && *v > 0 {
			sl = append(sl, slice{s.Labels[i], *v, i})
			total += *v
		}
	}
	legendW := math.Min(300, L.W*0.38)
	cx := (L.W - legendW) / 2
	cy := L.top + (L.H-L.top-16)/2
	r := math.Min((L.W-legendW)/2-24, (L.H-L.top-24)/2)
	inner := 0.0
	if s.Type == "donut" {
		inner = r * 0.56
	}
	a0 := -math.Pi / 2
	for k, x := range sl {
		a1 := a0 + x.v/total*2*math.Pi
		steps := max(int((a1-a0)/(2*math.Pi)*120), 2)
		var poly [][2]float64
		for t := 0; t <= steps; t++ {
			a := a0 + (a1-a0)*float64(t)/float64(steps)
			poly = append(poly, [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)})
		}
		if inner > 0 {
			for t := steps; t >= 0; t-- {
				a := a0 + (a1-a0)*float64(t)/float64(steps)
				poly = append(poly, [2]float64{cx + inner*math.Cos(a), cy + inner*math.Sin(a)})
			}
		} else {
			poly = append(poly, [2]float64{cx, cy})
		}
		L.add(prim{kind: "poly", pts: poly, color: seriesColor(k), stroke: "#ffffff"})
		// the share on the slice, when it has room
		if a1-a0 > 0.28 {
			mid := (a0 + a1) / 2
			rr := r * 0.66
			if inner > 0 {
				rr = (r + inner) / 2
			}
			label := fmt.Sprintf("%.0f%%", x.v/total*100)
			if s.ShowValues {
				label = fmtNum(x.v)
			}
			L.text(cx+rr*math.Cos(mid), cy+rr*math.Sin(mid)+4, label, 11, "middle", true, "#ffffff")
		}
		a0 = a1
	}
	if inner > 0 {
		L.text(cx, cy+2, fmtNum(total), 20, "middle", true, "#1f2328")
		L.text(cx, cy+20, firstStr(s.YLabel, "total"), 11, "middle", false, "#57606a")
	}
	// the legend
	lx := L.W - legendW + 8
	ly := cy - float64(len(sl))*11
	ly = math.Max(ly, L.top+12)
	for k, x := range sl {
		if ly > L.H-12 {
			L.text(lx, ly, fmt.Sprintf("… %d more", len(sl)-k), 11, "start", false, "#57606a")
			break
		}
		L.add(prim{kind: "rect", x: lx, y: ly - 9, w: 12, h: 12, color: seriesColor(k)})
		L.text(lx+18, ly+1, chartClip(x.label, 24), 12, "start", false, "#3a3f45")
		L.text(L.W-12, ly+1, fmt.Sprintf("%s · %.0f%%", fmtNum(x.v), x.v/total*100), 11, "end", false, "#57606a")
		ly += 22
	}
}

// heatmap: a grid, the labels across and the series down, each cell's
// colour by its value (a scale under the grid).
func (L *chartLayout) heatmap() {
	s := L.s
	longest := 0
	for _, sr := range s.Series {
		longest = max(longest, len([]rune(chartClip(sr.Name, 24))))
	}
	left := math.Min(200, math.Max(48, float64(longest)*6.6+16))
	right, bottom := 16.0, 64.0
	if s.XLabel != "" {
		bottom += 16
	}
	pw, ph := L.W-left-right, L.H-L.top-bottom
	py := L.top + 4
	cols, rows := len(s.Labels), len(s.Series)
	cw, rh := pw/float64(cols), ph/float64(rows)
	lo, hi := valueRange(s, false)
	if hi <= lo {
		hi = lo + 1
	}
	for ri, sr := range s.Series {
		if rh >= 10 || ri%max(int(math.Ceil(12/rh)), 1) == 0 {
			L.text(left-8, py+rh*(float64(ri)+0.5)+4, chartClip(sr.Name, 24), 11, "end", false, "#3a3f45")
		}
		for ci, v := range sr.Values {
			x, y := left+cw*float64(ci), py+rh*float64(ri)
			if v == nil {
				L.add(prim{kind: "rect", x: x + 0.5, y: y + 0.5, w: cw - 1, h: rh - 1, color: "#f6f8fa"})
				continue
			}
			t := (*v - lo) / (hi - lo)
			c := heatColor(t)
			L.add(prim{kind: "rect", x: x + 0.5, y: y + 0.5, w: math.Max(cw-1, 0.5), h: math.Max(rh-1, 0.5), color: c})
			if s.ShowValues && cw >= 26 && rh >= 14 {
				fg := "#1f2328"
				if t > 0.55 {
					fg = "#ffffff"
				}
				L.text(x+cw/2, y+rh/2+4, fmtNum(*v), 10, "middle", false, fg)
			}
		}
	}
	every := max(int(math.Ceil(float64(cols)*44/pw)), 1)
	for ci := 0; ci < cols; ci += every {
		L.text(left+cw*(float64(ci)+0.5), py+ph+16, chartClip(s.Labels[ci], max(3, int(cw*float64(every)/7))), 11, "middle", false, "#57606a")
	}
	// the scale
	sy := py + ph + 30
	sx, sw := left, math.Min(240, pw)
	for k := 0; k < 40; k++ {
		L.add(prim{kind: "rect", x: sx + sw*float64(k)/40, y: sy, w: sw/40 + 0.5, h: 8, color: heatColor(float64(k) / 39)})
	}
	L.text(sx, sy+20, fmtNum(lo), 10, "start", false, "#57606a")
	L.text(sx+sw, sy+20, fmtNum(hi), 10, "end", false, "#57606a")
	if s.XLabel != "" {
		L.text(left+pw/2, L.H-8, chartClip(s.XLabel, 80), 12, "middle", true, "#57606a")
	}
}

// heatColor runs from a pale to a deep blue (t in 0..1).
func heatColor(t float64) string {
	t = math.Max(0, math.Min(1, t))
	from, to := [3]float64{0xef, 0xf3, 0xff}, [3]float64{0x08, 0x30, 0x6b}
	var c [3]int
	for i := range c {
		c[i] = int(math.Round(from[i] + (to[i]-from[i])*t))
	}
	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

// --- SVG ----------------------------------------------------------------------

// ChartMarker is on every chart's SVG: the app offers Export PNG for it.
const ChartMarker = `data-specwright-chart="1"`

// ChartSVG draws the chart as SVG, with its spec in <metadata> (so a PNG of
// the same chart can be made from it later).
func ChartSVG(s ChartSpec, note string) string {
	var b strings.Builder
	spec, _ := json.Marshal(s)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" %s width="%d" height="%d" viewBox="0 0 %d %d" font-family="-apple-system, Segoe UI, Helvetica, Arial, sans-serif">`, ChartMarker, s.Width, s.Height, s.Width, s.Height)
	fmt.Fprintf(&b, "\n<title>%s</title>", html.EscapeString(firstStr(s.Title, "Chart")))
	if note != "" {
		fmt.Fprintf(&b, "\n<desc>%s</desc>", html.EscapeString(note))
	}
	fmt.Fprintf(&b, "\n<metadata id=\"specwright-chart\">%s</metadata>", html.EscapeString(string(spec)))
	fmt.Fprintf(&b, "\n<rect width=\"%d\" height=\"%d\" fill=\"#ffffff\"/>", s.Width, s.Height)
	for _, p := range layout(s) {
		switch p.kind {
		case "rect":
			fmt.Fprintf(&b, "\n<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" fill=\"%s\"/>", p.x, p.y, p.w, p.h, p.color)
		case "line":
			fmt.Fprintf(&b, "\n<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"%s\" stroke-width=\"%.1f\"/>", p.x, p.y, p.x2, p.y2, p.color, p.width)
		case "path":
			var pts []string
			for _, q := range p.pts {
				pts = append(pts, fmt.Sprintf("%.1f,%.1f", q[0], q[1]))
			}
			fmt.Fprintf(&b, "\n<polyline points=\"%s\" fill=\"none\" stroke=\"%s\" stroke-width=\"%.1f\" stroke-linejoin=\"round\" stroke-linecap=\"round\"/>", strings.Join(pts, " "), p.color, p.width)
		case "poly":
			var pts []string
			for _, q := range p.pts {
				pts = append(pts, fmt.Sprintf("%.1f,%.1f", q[0], q[1]))
			}
			extra := ""
			if p.opacity > 0 {
				extra += fmt.Sprintf(` fill-opacity="%.2f"`, p.opacity)
			}
			if p.stroke != "" {
				extra += fmt.Sprintf(` stroke="%s" stroke-width="1.5" stroke-linejoin="round"`, p.stroke)
			}
			fmt.Fprintf(&b, "\n<polygon points=\"%s\" fill=\"%s\"%s/>", strings.Join(pts, " "), p.color, extra)
		case "circle":
			op := ""
			if p.opacity > 0 {
				op = fmt.Sprintf(` fill-opacity="%.2f"`, p.opacity)
			}
			fmt.Fprintf(&b, "\n<circle cx=\"%.1f\" cy=\"%.1f\" r=\"%.1f\" fill=\"%s\"%s/>", p.x, p.y, p.w, p.color, op)
		case "text":
			weight := ""
			if p.bold {
				weight = ` font-weight="600"`
			}
			fmt.Fprintf(&b, "\n<text x=\"%.1f\" y=\"%.1f\" font-size=\"%.0f\" text-anchor=\"%s\" fill=\"%s\"%s>%s</text>", p.x, p.y, p.size, p.anchor, p.color, weight, html.EscapeString(p.text))
		}
	}
	b.WriteString("\n</svg>\n")
	return b.String()
}

var chartMetaRE = regexp.MustCompile(`(?s)<metadata id="specwright-chart">(.*?)</metadata>`)

// ChartFromSVG reads a chart's spec back from its SVG.
func ChartFromSVG(svg string) (ChartSpec, error) {
	var s ChartSpec
	m := chartMetaRE.FindStringSubmatch(svg)
	if m == nil {
		return s, errors.New("this is not a SpecWright chart (no chart data in it)")
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &s); err != nil {
		return s, fmt.Errorf("the chart's data cannot be read: %v", err)
	}
	if s.Width == 0 {
		s.Width = 800
	}
	if s.Height == 0 {
		s.Height = 480
	}
	return s, checkChart(&s)
}

// --- PNG ----------------------------------------------------------------------

var (
	fontOnce        sync.Once
	fontReg, fontBd *opentype.Font
)

func loadFonts() {
	fontOnce.Do(func() {
		fontReg, _ = opentype.Parse(goregular.TTF)
		fontBd, _ = opentype.Parse(gobold.TTF)
	})
}

func hexColor(h string) color.RGBA {
	var r, g, b uint8
	_, _ = fmt.Sscanf(strings.TrimPrefix(h, "#"), "%02x%02x%02x", &r, &g, &b)
	return color.RGBA{r, g, b, 255}
}

// ChartPNG draws the chart as PNG (twice the size, for sharp text).
func ChartPNG(s ChartSpec) ([]byte, error) {
	loadFonts()
	const k = 2.0
	W, H := int(float64(s.Width)*k), int(float64(s.Height)*k)
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	fillA := func(poly [][2]float64, c string, opacity float64) {
		col := color.NRGBA(hexColor(c))
		if opacity > 0 {
			col.A = uint8(math.Round(opacity * 255))
		}
		r := vector.NewRasterizer(W, H)
		r.DrawOp = draw.Over
		for i, p := range poly {
			if i == 0 {
				r.MoveTo(float32(p[0]*k), float32(p[1]*k))
			} else {
				r.LineTo(float32(p[0]*k), float32(p[1]*k))
			}
		}
		r.ClosePath()
		r.Draw(img, img.Bounds(), &image.Uniform{col}, image.Point{})
	}
	fill := func(poly [][2]float64, c string) { fillA(poly, c, 0) }
	stroke := func(x1, y1, x2, y2, w float64, c string) {
		dx, dy := x2-x1, y2-y1
		l := math.Hypot(dx, dy)
		if l == 0 {
			return
		}
		nx, ny := -dy/l*w/2, dx/l*w/2
		fill([][2]float64{{x1 + nx, y1 + ny}, {x2 + nx, y2 + ny}, {x2 - nx, y2 - ny}, {x1 - nx, y1 - ny}}, c)
	}
	circleA := func(x, y, r float64, c string, opacity float64) {
		var poly [][2]float64
		for i := 0; i < 20; i++ {
			a := float64(i) / 20 * 2 * math.Pi
			poly = append(poly, [2]float64{x + r*math.Cos(a), y + r*math.Sin(a)})
		}
		fillA(poly, c, opacity)
	}
	circle := func(x, y, r float64, c string) { circleA(x, y, r, c, 0) }
	faces := map[string]font.Face{}
	face := func(size float64, bold bool) font.Face {
		key := fmt.Sprint(size, bold)
		if f := faces[key]; f != nil {
			return f
		}
		src := fontReg
		if bold {
			src = fontBd
		}
		f, err := opentype.NewFace(src, &opentype.FaceOptions{Size: size * k, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil
		}
		faces[key] = f
		return f
	}
	for _, p := range layout(s) {
		switch p.kind {
		case "rect":
			if p.w > 0 && p.h > 0 {
				fill([][2]float64{{p.x, p.y}, {p.x + p.w, p.y}, {p.x + p.w, p.y + p.h}, {p.x, p.y + p.h}}, p.color)
			}
		case "line":
			stroke(p.x, p.y, p.x2, p.y2, p.width, p.color)
		case "path":
			for i := 1; i < len(p.pts); i++ {
				stroke(p.pts[i-1][0], p.pts[i-1][1], p.pts[i][0], p.pts[i][1], p.width, p.color)
				circle(p.pts[i][0], p.pts[i][1], p.width/2, p.color) // round joins
			}
		case "poly":
			fillA(p.pts, p.color, p.opacity)
			if p.stroke != "" {
				for i := range p.pts {
					q := p.pts[(i+1)%len(p.pts)]
					stroke(p.pts[i][0], p.pts[i][1], q[0], q[1], 1.5, p.stroke)
				}
			}
		case "circle":
			circleA(p.x, p.y, p.w, p.color, p.opacity)
		case "text":
			f := face(p.size, p.bold)
			if f == nil {
				continue
			}
			d := &font.Drawer{Dst: img, Src: &image.Uniform{hexColor(p.color)}, Face: f}
			w := d.MeasureString(p.text)
			x := fixed.I(int(p.x * k))
			switch p.anchor {
			case "middle":
				x -= w / 2
			case "end":
				x -= w
			}
			d.Dot = fixed.Point26_6{X: x, Y: fixed.I(int(p.y * k))}
			d.DrawString(p.text)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- the tool -------------------------------------------------------------------

var chartSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

func chartTool(_ context.Context, b *Box, a args) (string, error) {
	s, err := parseChart(a)
	if err != nil {
		return "", err
	}
	format := strings.ToLower(firstStr(a.str("format"), "svg"))
	if format != "svg" && format != "png" && format != "both" {
		return "", fmt.Errorf("format %q: svg, png or both", format)
	}
	base := strings.TrimSpace(a.str("path"))
	if base == "" {
		slug := strings.Trim(chartSlugRE.ReplaceAllString(strings.ToLower(firstStr(s.Title, "chart")), "-"), "-")
		if len(slug) > 40 {
			slug = strings.Trim(slug[:40], "-")
		}
		base = "out/charts/" + firstStr(slug, "chart") + "-" + time.Now().Format("20060102-150405")
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".svg"), ".png")
	var saved []string
	write := func(ext string, data []byte) error {
		abs, rel, err := b.resolve(base + ext)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return err
		}
		saved = append(saved, filepath.ToSlash(rel))
		return nil
	}
	if format == "png" || format == "both" {
		pngData, err := ChartPNG(s)
		if err != nil {
			return "", err
		}
		if err := write(".png", pngData); err != nil {
			return "", err
		}
	}
	note := ""
	if format != "png" {
		// the SVG notes where it is saved (and that a PNG can be made)
		note = "Saved as " + base + ".svg"
		if len(saved) > 0 {
			note += " and " + saved[0]
		}
		svg := ChartSVG(s, note)
		if err := write(".svg", []byte(svg)); err != nil {
			return "", err
		}
		return svg, nil
	}
	return ChartSVG(s, "Saved as "+strings.Join(saved, ", ")), nil
}

// PreviewChart draws the chart for the arguments without saving anything
// (the Flow designer's and the guide's previews).
func PreviewChart(raw map[string]any) (string, error) {
	s, err := parseChart(args(raw))
	if err != nil {
		return "", err
	}
	return ChartSVG(s, "Preview: the chart step saves it when the flow runs"), nil
}
