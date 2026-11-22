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
	Type   string        `json:"type"` // bar | line | stacked_bar
	Title  string        `json:"title,omitempty"`
	XLabel string        `json:"x_label,omitempty"`
	YLabel string        `json:"y_label,omitempty"`
	Labels []string      `json:"labels"`
	Series []ChartSeries `json:"series"`
	Width  int           `json:"width,omitempty"`
	Height int           `json:"height,omitempty"`
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
	s := ChartSpec{Type: strings.ToLower(firstStr(strings.TrimSpace(a.str("type")), "bar")), Title: a.str("title"),
		XLabel: a.str("x_label"), YLabel: a.str("y_label"), Width: a.num("width", 800, 320, 2400), Height: a.num("height", 480, 240, 1600)}
	switch s.Type {
	case "bar", "bars", "column", "columns":
		s.Type = "bar"
	case "line", "lines", "multiline", "multi_line", "multi-line", "linechart":
		s.Type = "line"
	case "stacked", "stacked_bar", "stacked-bar", "stackedbar":
		s.Type = "stacked_bar"
	default:
		return s, fmt.Errorf("type %q: use bar, line (one or more series: a multi-line chart) or stacked_bar", s.Type)
	}
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
		if err := fromRows(&s, list, x, ys); err != nil {
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
	return s, checkChart(&s)
}

func fromRows(s *ChartSpec, rows []any, x string, ys []string) error {
	if len(rows) == 0 {
		return errors.New("rows is empty: there is nothing to chart")
	}
	first, _ := rows[0].(map[string]any)
	if first == nil {
		return errors.New("rows must be objects, e.g. [{\"month\": \"Jan\", \"sales\": 12}]")
	}
	if x == "" { // the first text field
		for _, k := range sortedFields(first) {
			if _, isNum := first[k].(float64); !isNum {
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
	if len(s.Series) > maxChartSeries {
		return fmt.Errorf("at most %d series in one chart", maxChartSeries)
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
	kind   string // rect | line | path | circle | text
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
	switch {
	case a >= 1e9:
		return strconv.FormatFloat(v/1e9, 'f', -1, 64) + "B"
	case a >= 1e6:
		return strconv.FormatFloat(v/1e6, 'f', -1, 64) + "M"
	case a >= 1e4:
		return strconv.FormatFloat(v/1e3, 'f', -1, 64) + "k"
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
	W, H := float64(s.Width), float64(s.Height)
	var ps []prim
	text := func(x, y float64, t string, size float64, anchor string, bold bool, col string) {
		ps = append(ps, prim{kind: "text", x: x, y: y, text: t, size: size, anchor: anchor, bold: bold, color: col})
	}
	top := 20.0
	if s.Title != "" {
		text(W/2, 28, chartClip(s.Title, 90), 18, "middle", true, "#1f2328")
		top = 44
	}
	if len(s.Series) > 1 { // the legend, under the title
		x := 64.0
		y := top + 8
		for i, sr := range s.Series {
			c := chartPalette[i%len(chartPalette)]
			label := chartClip(sr.Name, 24)
			wid := 22 + float64(len([]rune(label)))*7.2 + 18
			if x+wid > W-16 {
				x, y = 64, y+20
			}
			ps = append(ps, prim{kind: "rect", x: x, y: y - 9, w: 12, h: 12, color: c})
			text(x+18, y+1, label, 12, "start", false, "#3a3f45")
			x += wid
		}
		top = y + 20
	}
	if s.YLabel != "" {
		text(16, top+4, chartClip(s.YLabel, 60), 12, "start", true, "#57606a")
		top += 14
	}
	left, right, bottom := 64.0, 24.0, 52.0
	if s.XLabel != "" {
		bottom += 18
	}
	pw, ph := W-left-right, H-top-bottom-8
	py := top + 8
	// the value range (stacked: the sums)
	lo, hi := 0.0, 0.0
	first := true
	n := len(s.Labels)
	for i := 0; i < n; i++ {
		pos, neg := 0.0, 0.0
		for _, sr := range s.Series {
			v := sr.Values[i]
			if v == nil {
				continue
			}
			if s.Type == "stacked_bar" {
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
		if s.Type == "stacked_bar" {
			lo, hi = math.Min(lo, neg), math.Max(hi, pos)
		}
	}
	if s.Type != "line" || lo > 0 { // bars start at zero; lines may float
		lo = math.Min(lo, 0)
	}
	if hi <= lo {
		hi = lo + 1
	}
	step := niceStep(hi-lo, 5)
	lo, hi = math.Floor(lo/step)*step, math.Ceil(hi/step)*step
	ys := func(v float64) float64 { return py + ph - (v-lo)/(hi-lo)*ph }
	// grid and y ticks
	for v := lo; v <= hi+step/2; v += step {
		y := ys(v)
		ps = append(ps, prim{kind: "line", x: left, y: y, x2: left + pw, y2: y, color: "#e6e8eb", width: 1})
		text(left-8, y+4, fmtNum(v), 11, "end", false, "#57606a")
	}
	if lo < 0 {
		ps = append(ps, prim{kind: "line", x: left, y: ys(0), x2: left + pw, y2: ys(0), color: "#8c959f", width: 1})
	}
	ps = append(ps, prim{kind: "line", x: left, y: py + ph, x2: left + pw, y2: py + ph, color: "#8c959f", width: 1})
	band := pw / float64(n)
	xc := func(i int) float64 { return left + band*(float64(i)+0.5) }
	// x labels: as many as fit
	every := int(math.Ceil(float64(n) * 64 / pw))
	every = max(every, 1)
	for i := 0; i < n; i += every {
		text(xc(i), py+ph+18, chartClip(s.Labels[i], max(4, int(band*float64(every)/7))), 11, "middle", false, "#57606a")
	}
	if s.XLabel != "" {
		text(left+pw/2, H-12, chartClip(s.XLabel, 80), 12, "middle", true, "#57606a")
	}
	switch s.Type {
	case "bar":
		k := float64(len(s.Series))
		bw := math.Max(1, band*0.8/k)
		for si, sr := range s.Series {
			c := chartPalette[si%len(chartPalette)]
			for i, v := range sr.Values {
				if v == nil {
					continue
				}
				x := left + band*float64(i) + band*0.1 + bw*float64(si)
				y0, y1 := ys(0), ys(*v)
				ps = append(ps, prim{kind: "rect", x: x, y: math.Min(y0, y1), w: math.Max(bw-1, 1), h: math.Abs(y0 - y1), color: c})
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
				c := chartPalette[si%len(chartPalette)]
				base := pos
				if *v < 0 {
					base = neg
				}
				y0, y1 := ys(base), ys(base+*v)
				ps = append(ps, prim{kind: "rect", x: xc(i) - bw/2, y: math.Min(y0, y1), w: bw, h: math.Abs(y0 - y1), color: c})
				if *v < 0 {
					neg += *v
				} else {
					pos += *v
				}
			}
		}
	case "line":
		for si, sr := range s.Series {
			c := chartPalette[si%len(chartPalette)]
			var seg [][2]float64
			flush := func() {
				if len(seg) > 1 {
					ps = append(ps, prim{kind: "path", pts: seg, color: c, width: 2.2})
				}
				seg = nil
			}
			for i, v := range sr.Values {
				if v == nil {
					flush()
					continue
				}
				seg = append(seg, [2]float64{xc(i), ys(*v)})
			}
			flush()
			if n <= 60 {
				for i, v := range sr.Values {
					if v != nil {
						ps = append(ps, prim{kind: "circle", x: xc(i), y: ys(*v), w: 3, color: c})
					}
				}
			}
		}
	}
	return ps
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
		case "circle":
			fmt.Fprintf(&b, "\n<circle cx=\"%.1f\" cy=\"%.1f\" r=\"%.1f\" fill=\"%s\"/>", p.x, p.y, p.w, p.color)
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
	fill := func(poly [][2]float64, c string) {
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
		r.Draw(img, img.Bounds(), &image.Uniform{hexColor(c)}, image.Point{})
	}
	stroke := func(x1, y1, x2, y2, w float64, c string) {
		dx, dy := x2-x1, y2-y1
		l := math.Hypot(dx, dy)
		if l == 0 {
			return
		}
		nx, ny := -dy/l*w/2, dx/l*w/2
		fill([][2]float64{{x1 + nx, y1 + ny}, {x2 + nx, y2 + ny}, {x2 - nx, y2 - ny}, {x1 - nx, y1 - ny}}, c)
	}
	circle := func(x, y, r float64, c string) {
		var poly [][2]float64
		for i := 0; i < 20; i++ {
			a := float64(i) / 20 * 2 * math.Pi
			poly = append(poly, [2]float64{x + r*math.Cos(a), y + r*math.Sin(a)})
		}
		fill(poly, c)
	}
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
		case "circle":
			circle(p.x, p.y, p.w, p.color)
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
