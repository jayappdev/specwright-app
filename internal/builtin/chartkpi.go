package builtin

// chart types for single numbers and trends: kpi (tiles: a value, and its
// change against a second series such as last month or a target), gauge (a
// half dial per label), bullet (a bar per label over its bands, with a
// target mark) and sparklines (one small trend line per series, with its
// last value and change). bands are the thresholds between bad, so-so and
// good (lower_is_better turns them round).

import (
	"fmt"
	"math"
	"strconv"
)

const (
	colGood, colWarn, colBad    = "#1a7f37", "#bf8700", "#cf222e"
	lightGood, lightWarn, light = "#d2f4d3", "#fff1c2", "#ffd8d3"
	colTrack                    = "#eaeef2"
	colInk, colMuted            = "#1f2328", "#57606a"
)

// zone: where v falls among the bands, 0 (bad) … 1 (good); -1 without bands.
func (s ChartSpec) zone(v float64) float64 {
	if len(s.Bands) == 0 {
		return -1
	}
	i := 0
	for _, b := range s.Bands {
		if v >= b {
			i++
		}
	}
	t := float64(i) / float64(len(s.Bands))
	if s.LowerBetter {
		t = 1 - t
	}
	return t
}

func zoneColor(t float64, strong bool) string {
	switch {
	case t < 0:
		if strong {
			return seriesColor(0)
		}
		return colTrack
	case t < 0.34:
		return map[bool]string{true: colBad, false: light}[strong]
	case t < 0.67:
		return map[bool]string{true: colWarn, false: lightWarn}[strong]
	}
	return map[bool]string{true: colGood, false: lightGood}[strong]
}

// fmtUnit writes a value with the spec's unit ($12k, 98.5%, 230 ms).
func (s ChartSpec) fmtUnit(v float64) string {
	n := fmtNum(v)
	if a := math.Abs(v); a >= 1e4 { // one decimal is enough for a headline number
		n = fmtNum(math.Round(v/math.Pow(10, math.Floor(math.Log10(a))-2)) * math.Pow(10, math.Floor(math.Log10(a))-2))
	} else if a >= 100 {
		n = strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
	}
	switch s.Unit {
	case "":
		return n
	case "$", "€", "£", "¥":
		return s.Unit + n
	}
	return n + s.Unit
}

// change: the text and colour of v against p (+12.5% vs target).
func (s ChartSpec) change(v, p float64, against string) (string, string, bool) {
	d := v - p
	txt := ""
	if p != 0 {
		txt = fmt.Sprintf("%+.1f%%", d/math.Abs(p)*100)
	} else {
		txt = (map[bool]string{true: "+", false: ""}[d >= 0]) + s.fmtUnit(d)
	}
	if against != "" {
		txt += " vs " + chartClip(against, 22)
	}
	good := (d >= 0) != s.LowerBetter
	if d == 0 {
		return txt, colMuted, true
	}
	return txt, map[bool]string{true: colGood, false: colBad}[good], d > 0
}

// value range of the kpi-like charts: min/max when given, else from the
// values, the second series and the bands.
func (s ChartSpec) span() (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	see := func(v float64) { lo, hi = math.Min(lo, v), math.Max(hi, v) }
	for _, sr := range s.Series[:min(2, len(s.Series))] {
		for _, v := range sr.Values {
			if v != nil {
				see(*v)
			}
		}
	}
	for _, b := range s.Bands {
		see(b)
	}
	lo = math.Min(lo, 0)
	if s.Unit == "%" && hi <= 100 && lo >= 0 {
		hi = 100
	} else {
		hi = niceScale(lo, hi*1.05, true).hi
	}
	if s.Min != nil {
		lo = *s.Min
	}
	if s.Max != nil {
		hi = *s.Max
	}
	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// grid: n cells in up to 4 columns under the header.
func (L *chartLayout) grid(n int) (cols, rows int, cw, ch float64) {
	cols = min(n, 4)
	if n == 5 || n == 6 {
		cols = 3
	}
	rows = (n + cols - 1) / cols
	cw = (L.W - 32 - float64(cols-1)*16) / float64(cols)
	ch = (L.H - L.top - 16 - float64(rows-1)*16) / float64(rows)
	return
}

func (L *chartLayout) triangle(x, y, size float64, up bool, col string) {
	if up {
		L.add(prim{kind: "poly", pts: [][2]float64{{x, y}, {x + size, y}, {x + size/2, y - size*0.9}}, color: col})
	} else {
		L.add(prim{kind: "poly", pts: [][2]float64{{x, y - size*0.9}, {x + size, y - size*0.9}, {x + size/2, y}}, color: col})
	}
}

// kpi: a tile per label with its value, and the change against the second
// series when there is one.
func (L *chartLayout) kpi() {
	s := L.s
	n := len(s.Labels)
	cols, _, cw, ch := L.grid(n)
	for i, label := range s.Labels {
		x := 16 + float64(i%cols)*(cw+16)
		y := L.top + 4 + float64(i/cols)*(ch+16)
		v := s.Series[0].Values[i]
		L.add(prim{kind: "rect", x: x, y: y, w: cw, h: ch, color: "#f6f8fa"})
		accent := seriesColor(0)
		if v != nil && len(s.Bands) > 0 {
			accent = zoneColor(s.zone(*v), true)
		}
		L.add(prim{kind: "rect", x: x, y: y, w: 4, h: ch, color: accent})
		L.text(x+18, y+24, chartClip(label, int(cw/7.5)), 13, "start", false, colMuted)
		size := math.Max(16, math.Min(40, math.Min(ch*0.34, cw/6)))
		val := "–"
		if v != nil {
			val = s.fmtUnit(*v)
		}
		L.text(x+18, y+24+(ch-24)/2+size*0.35, val, size, "start", true, colInk)
		if len(s.Series) > 1 && v != nil && s.Series[1].Values[i] != nil && ch >= 80 {
			txt, col, up := s.change(*v, *s.Series[1].Values[i], s.Series[1].Name)
			L.triangle(x+18, y+ch-14, 9, up, col)
			L.text(x+32, y+ch-14, chartClip(txt, int(cw/7)), 12, "start", true, col)
		}
	}
}

// arcBand: a band of a circle from angle a0 to a1 (radians), radii r0 < r1.
func arcBand(cx, cy, r0, r1, a0, a1 float64) [][2]float64 {
	steps := max(int(math.Abs(a1-a0)/math.Pi*90), 2)
	var pts [][2]float64
	for t := 0; t <= steps; t++ {
		a := a0 + (a1-a0)*float64(t)/float64(steps)
		pts = append(pts, [2]float64{cx + r1*math.Cos(a), cy + r1*math.Sin(a)})
	}
	for t := steps; t >= 0; t-- {
		a := a0 + (a1-a0)*float64(t)/float64(steps)
		pts = append(pts, [2]float64{cx + r0*math.Cos(a), cy + r0*math.Sin(a)})
	}
	return pts
}

// gauge: a half dial per label, its bands as the track, the value as a
// filled arc, the second series as a target mark.
func (L *chartLayout) gauge() {
	s := L.s
	lo, hi := s.span()
	ang := func(v float64) float64 {
		t := math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
		return math.Pi + t*math.Pi
	}
	cols, _, cw, ch := L.grid(len(s.Labels))
	for i, label := range s.Labels {
		x := 16 + float64(i%cols)*(cw+16)
		y := L.top + 4 + float64(i/cols)*(ch+16)
		r := math.Max(20, math.Min(cw/2-16, ch-64))
		r0 := r * 0.7
		cx, cy := x+cw/2, y+12+r
		edges := append(append([]float64{lo}, s.Bands...), hi)
		for k := 0; k+1 < len(edges); k++ {
			a, b := math.Max(edges[k], lo), math.Min(edges[k+1], hi)
			if b <= a {
				continue
			}
			if len(s.Bands) > 0 { // the zones: a thin strip outside
				L.add(prim{kind: "poly", pts: arcBand(cx, cy, r*0.93, r, ang(a), ang(b)), color: zoneColor(s.zone(a), true), opacity: 0.55})
			}
		}
		// the value: a thick arc over a grey track
		L.add(prim{kind: "poly", pts: arcBand(cx, cy, r0, r*0.88, math.Pi, 2*math.Pi), color: colTrack})
		v := s.Series[0].Values[i]
		if v != nil && *v > lo {
			L.add(prim{kind: "poly", pts: arcBand(cx, cy, r0, r*0.88, math.Pi, ang(*v)), color: zoneColor(s.zone(*v), true)})
		}
		if len(s.Series) > 1 && s.Series[1].Values[i] != nil {
			a := ang(*s.Series[1].Values[i])
			L.add(prim{kind: "line", x: cx + (r0-5)*math.Cos(a), y: cy + (r0-5)*math.Sin(a), x2: cx + (r+5)*math.Cos(a), y2: cy + (r+5)*math.Sin(a), color: colInk, width: 3})
		}
		val := "–"
		if v != nil {
			val = s.fmtUnit(*v)
		}
		L.text(cx, cy-6, val, math.Max(14, math.Min(32, r*0.32)), "middle", true, colInk)
		L.text(cx-(r+r0)/2, cy+16, s.fmtUnit(lo), 11, "middle", false, colMuted)
		L.text(cx+(r+r0)/2, cy+16, s.fmtUnit(hi), 11, "middle", false, colMuted)
		L.text(cx, cy+18, chartClip(label, int(cw/7.5)), 13, "middle", true, "#3a3f45")
		if len(s.Series) > 1 && v != nil && s.Series[1].Values[i] != nil {
			txt, col, _ := s.change(*v, *s.Series[1].Values[i], s.Series[1].Name)
			L.text(cx, cy+36, chartClip(txt, int(cw/7)), 11, "middle", false, col)
		}
	}
}

// labelWidth: room for the longest label (up to 24 characters).
func labelWidth(names []string) float64 {
	longest := 0
	for _, n := range names {
		longest = max(longest, len([]rune(chartClip(n, 24))))
	}
	return math.Min(200, math.Max(48, float64(longest)*6.8+20))
}

// bullet: a row per label: the bands as light zones, the value as a bar,
// the second series as a target mark, a scale under them.
func (L *chartLayout) bullet() {
	s := L.s
	lo, hi := s.span()
	left := labelWidth(s.Labels)
	right := 84.0
	pw := L.W - left - right
	n := len(s.Labels)
	rh := math.Min(48, (L.H-L.top-40)/float64(n))
	px := func(v float64) float64 { return left + pw*math.Max(0, math.Min(1, (v-lo)/(hi-lo))) }
	for i, label := range s.Labels {
		y := L.top + 8 + float64(i)*rh
		h := rh * 0.72
		edges := append(append([]float64{lo}, s.Bands...), hi)
		for k := 0; k+1 < len(edges); k++ {
			a, b := math.Max(edges[k], lo), math.Min(edges[k+1], hi)
			if b <= a {
				continue
			}
			c := "#eef1f4"
			if len(s.Bands) > 0 {
				c = zoneColor(s.zone(a), false)
			}
			L.add(prim{kind: "rect", x: px(a), y: y, w: px(b) - px(a), h: h, color: c})
		}
		L.text(left-10, y+h/2+4, chartClip(label, 24), 12, "end", false, "#3a3f45")
		v := s.Series[0].Values[i]
		if v != nil {
			L.add(prim{kind: "rect", x: px(math.Max(lo, 0)), y: y + h*0.3, w: math.Max(1, px(*v)-px(math.Max(lo, 0))), h: h * 0.4, color: map[bool]string{true: colInk, false: zoneColor(s.zone(*v), true)}[len(s.Bands) > 0]})
			L.text(L.W-right+10, y+h/2+4, s.fmtUnit(*v), 12, "start", true, colInk)
		}
		if len(s.Series) > 1 && s.Series[1].Values[i] != nil {
			t := px(*s.Series[1].Values[i])
			L.add(prim{kind: "line", x: t, y: y + h*0.12, x2: t, y2: y + h*0.88, color: colInk, width: 2.5})
		}
	}
	base := L.top + 8 + float64(n)*rh
	sc := scale{lo, hi, niceStep(hi-lo, 5)}
	for _, t := range sc.ticks() {
		if t < lo-1e-9 || t > hi+1e-9 {
			continue
		}
		L.add(prim{kind: "line", x: px(t), y: base, x2: px(t), y2: base + 4, color: "#8c959f", width: 1})
		L.text(px(t), base+17, s.fmtUnit(t), 11, "middle", false, colMuted)
	}
	if len(s.Series) > 1 {
		L.add(prim{kind: "line", x: left, y: base + 32, x2: left, y2: base + 44, color: colInk, width: 2.5})
		L.text(left+8, base+42, chartClip(s.Series[1].Name, 30), 11, "start", false, colMuted)
	}
}

// sparklines: a row per series: its name, a small line over the labels
// (its own scale), the last value and the change since the first.
func (L *chartLayout) sparklines() {
	s := L.s
	var names []string
	for _, sr := range s.Series {
		names = append(names, sr.Name)
	}
	left := labelWidth(names)
	right := 150.0
	pw := L.W - left - right
	n := len(s.Series)
	rh := math.Min(44, (L.H-L.top-12)/float64(n))
	m := len(s.Labels)
	for i, sr := range s.Series {
		y := L.top + 4 + float64(i)*rh
		if i > 0 {
			L.add(prim{kind: "line", x: 16, y: y, x2: L.W - 16, y2: y, color: "#eaeef2", width: 1})
		}
		L.text(left-12, y+rh/2+4, chartClip(sr.Name, 24), 12, "end", false, "#3a3f45")
		lo, hi := math.Inf(1), math.Inf(-1)
		var first, last *float64
		lastI := 0
		for k, v := range sr.Values {
			if v != nil {
				lo, hi = math.Min(lo, *v), math.Max(hi, *v)
				if first == nil {
					first = v
				}
				last, lastI = v, k
			}
		}
		if last == nil {
			continue
		}
		if hi <= lo {
			hi, lo = hi+1, lo-1
		}
		pad := rh * 0.18
		pt := func(k int, v float64) [2]float64 {
			x := left + pw*0.5
			if m > 1 {
				x = left + pw*float64(k)/float64(m-1)
			}
			return [2]float64{x, y + pad + (rh-2*pad)*(1-(v-lo)/(hi-lo))}
		}
		var pts [][2]float64
		for k, v := range sr.Values {
			if v == nil {
				if len(pts) > 1 {
					L.add(prim{kind: "path", pts: pts, color: seriesColor(0), width: 1.6})
				}
				pts = nil
				continue
			}
			pts = append(pts, pt(k, *v))
		}
		if len(pts) > 1 {
			L.add(prim{kind: "path", pts: pts, color: seriesColor(0), width: 1.6})
		}
		e := pt(lastI, *last)
		L.add(prim{kind: "circle", x: e[0], y: e[1], w: 3, color: seriesColor(0)})
		L.text(L.W-right+12, y+rh/2+4, s.fmtUnit(*last), 13, "start", true, colInk)
		if first != last {
			txt, col, _ := s.change(*last, *first, "")
			L.text(L.W-16, y+rh/2+4, txt, 11, "end", false, col)
		}
	}
}
