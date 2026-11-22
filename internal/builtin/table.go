package builtin

// table: a formatted HTML table from rows (a JSON array of objects): chosen
// columns with labels, number / percent / currency / date / bytes formats,
// sorting, totals, colours by thresholds or rules, data bars and sparklines
// (a column holding a list of numbers). The page is self-contained (its CSS
// inline) and saved under out/tables; headers sort when it is opened in a
// browser.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TableMarker is on every table the table tool makes.
const TableMarker = `data-specwright-table="1"`

// TableColumn is one column's look.
type TableColumn struct {
	Field  string `json:"field"`
	Label  string `json:"label,omitempty"`
	Format string `json:"format,omitempty"` // auto | text | number | integer | percent | currency | date | datetime | bytes | duration_ms | bar | sparkline | json
	// Decimals: digits after the point (default by format)
	Decimals *int   `json:"decimals,omitempty"`
	Unit     string `json:"unit,omitempty"` // currency symbol, or a suffix (" ms")
	Align    string `json:"align,omitempty"`
	// Bands colour a number by thresholds (bad < … < good; LowerBetter
	// turns them round); Rules colour cells that match.
	Bands       []float64   `json:"bands,omitempty"`
	LowerBetter bool        `json:"lower_is_better,omitempty"`
	Rules       []TableRule `json:"rules,omitempty"`
	Total       string      `json:"total,omitempty"` // sum | avg | min | max | count
}

// TableRule colours a cell (or its row) when its value matches.
type TableRule struct {
	Op    string `json:"op"` // > >= < <= = != contains empty
	Value any    `json:"value,omitempty"`
	Color string `json:"color"` // good | warn | bad | info | a #hex colour
	Row   bool   `json:"row,omitempty"`
}

// TableSpec is what the table tool draws.
type TableSpec struct {
	Title   string
	Caption string
	Columns []TableColumn
	Rows    []map[string]any
	Total   int // rows before max_rows
	Totals  bool
}

var tableFormats = []string{"auto", "text", "number", "integer", "percent", "currency", "date", "datetime", "bytes", "duration_ms", "bar", "sparkline", "json"}

// parseTable reads the tool's arguments.
func parseTable(a args) (TableSpec, error) {
	s := TableSpec{Title: a.str("title"), Caption: a.str("caption")}
	rows, err := rowsArg(a["rows"])
	if err != nil {
		return s, err
	}
	if len(rows) == 0 {
		return s, errors.New("rows is empty: give a JSON array of objects, e.g. a query's result")
	}
	cols, err := columnsArg(a["columns"])
	if err != nil {
		return s, err
	}
	if len(cols) == 0 { // every field, in the order they first appear
		seen := map[string]bool{}
		for _, r := range rows[:min(len(rows), 200)] {
			for _, k := range orderedKeys(r) {
				if !seen[k] {
					seen[k] = true
					cols = append(cols, TableColumn{Field: k})
				}
			}
		}
	}
	// per-column settings given as maps: {"field": value}
	for k, v := range mapArg(a["formats"]) {
		for i := range cols {
			if cols[i].Field == k {
				cols[i].Format = fmt.Sprint(v)
			}
		}
	}
	for i := range cols {
		c := &cols[i]
		c.Format = strings.ToLower(strings.TrimSpace(c.Format))
		if c.Format == "" {
			c.Format = "auto"
		}
		if !slices.Contains(tableFormats, c.Format) {
			return s, fmt.Errorf("column %s: format %q: use %s", c.Field, c.Format, strings.Join(tableFormats, ", "))
		}
		if c.Format == "auto" {
			c.Format = guessFormat(rows, c.Field)
		}
		if c.Label == "" {
			c.Label = c.Field
		}
	}
	// sort: "field", "-field" or "field desc"
	if by := strings.TrimSpace(a.str("sort")); by != "" {
		desc := strings.HasPrefix(by, "-") || strings.HasSuffix(strings.ToLower(by), " desc")
		by = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(by, "-"), " desc"), " asc"))
		sort.SliceStable(rows, func(i, j int) bool {
			c := compareCells(lookupField(rows[i], by), lookupField(rows[j], by))
			if desc {
				return c > 0
			}
			return c < 0
		})
	}
	// totals: true (sum every number column) or {"field": "sum|avg|…"}
	switch t := a["totals"].(type) {
	case bool:
		s.Totals = t
	case string:
		if t == "true" || t == "yes" {
			s.Totals = true
		} else if m := mapArg(t); len(m) > 0 {
			setTotals(cols, m)
			s.Totals = true
		} else if strings.TrimSpace(t) != "" && t != "false" {
			for _, f := range strings.Split(t, ",") {
				setTotals(cols, map[string]any{strings.TrimSpace(f): "sum"})
			}
			s.Totals = true
		}
	case map[string]any:
		setTotals(cols, t)
		s.Totals = true
	case []any:
		for _, f := range t {
			setTotals(cols, map[string]any{fmt.Sprint(f): "sum"})
		}
		s.Totals = true
	}
	if s.Totals {
		for i := range cols {
			if cols[i].Total == "" && isNumberFormat(cols[i].Format) && cols[i].Format != "percent" {
				cols[i].Total = "sum"
			}
		}
	}
	s.Columns = cols
	s.Total = len(rows)
	if n := a.num("max_rows", 500, 1, 5000); len(rows) > n {
		rows = rows[:n]
	}
	s.Rows = rows
	return s, nil
}

func setTotals(cols []TableColumn, m map[string]any) {
	for k, v := range m {
		for i := range cols {
			if cols[i].Field == k {
				cols[i].Total = strings.ToLower(fmt.Sprint(v))
			}
		}
	}
}

func isNumberFormat(f string) bool {
	switch f {
	case "number", "integer", "percent", "currency", "bytes", "duration_ms", "bar":
		return true
	}
	return false
}

// rowsArg reads rows: a list of objects, its JSON text, or an object
// holding rows / items / data.
func rowsArg(v any) ([]map[string]any, error) {
	if t, ok := v.(string); ok {
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(t), &v); err != nil {
			return nil, fmt.Errorf("rows is not JSON: %v", err)
		}
	}
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"rows", "items", "data", "results"} {
			if l, ok := m[k].([]any); ok {
				v = l
				break
			}
		}
	}
	list, ok := v.([]any)
	if !ok {
		if v == nil {
			return nil, nil
		}
		return nil, errors.New("rows must be a JSON array of objects")
	}
	var out []map[string]any
	for _, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, errors.New("rows must be objects, e.g. [{\"region\": \"EU\", \"revenue\": 120}]")
		}
		out = append(out, m)
	}
	return out, nil
}

// columnsArg reads columns: names, objects, or their JSON text.
func columnsArg(v any) ([]TableColumn, error) {
	if t, ok := v.(string); ok {
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, nil
		}
		if strings.HasPrefix(t, "[") {
			if err := json.Unmarshal([]byte(t), &v); err != nil {
				return nil, fmt.Errorf("columns is not JSON: %v", err)
			}
		} else { // "region, revenue"
			var l []any
			for _, f := range strings.Split(t, ",") {
				if f = strings.TrimSpace(f); f != "" {
					l = append(l, f)
				}
			}
			v = l
		}
	}
	var out []TableColumn
	for _, x := range anyList(v) {
		switch c := x.(type) {
		case string:
			out = append(out, TableColumn{Field: c})
		case map[string]any:
			b, _ := json.Marshal(c)
			var col TableColumn
			if err := json.Unmarshal(b, &col); err != nil {
				return nil, fmt.Errorf("a column: %v", err)
			}
			if col.Field == "" {
				return nil, errors.New("a column has no field")
			}
			if col.Bands == nil {
				col.Bands = floatList(c["bands"])
			}
			sort.Float64s(col.Bands)
			out = append(out, col)
		}
	}
	return out, nil
}

func mapArg(v any) map[string]any {
	if t, ok := v.(string); ok {
		var m map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(t)), &m) == nil {
			return m
		}
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// orderedKeys: an object's keys as they were written (JSON decoding loses
// the order, so they are sorted; "id"/"name"-like keys first).
func orderedKeys(m map[string]any) []string {
	ks := sortedFields(m)
	rank := func(k string) int {
		switch strings.ToLower(k) {
		case "id", "name", "title", "key":
			return 0
		}
		if _, isNum := m[k].(float64); isNum {
			return 2
		}
		return 1
	}
	sort.SliceStable(ks, func(i, j int) bool { return rank(ks[i]) < rank(ks[j]) })
	return ks
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T|$)`)

func guessFormat(rows []map[string]any, f string) string {
	nums, texts, lists, dates := 0, 0, 0, 0
	for _, r := range rows[:min(len(rows), 100)] {
		switch v := lookupField(r, f).(type) {
		case float64:
			nums++
		case string:
			if isoDate.MatchString(v) {
				dates++
			} else {
				texts++
			}
		case []any:
			if len(v) > 0 && number(v[0]) != nil {
				lists++
			} else {
				texts++
			}
		case map[string]any:
			texts++
		}
	}
	switch {
	case lists > 0 && lists >= nums+texts:
		return "sparkline"
	case nums > 0 && texts == 0 && dates == 0:
		return "number"
	case dates > 0 && texts == 0 && nums == 0:
		return "date"
	}
	return "text"
}

func compareCells(a, b any) int {
	na, nb := number(a), number(b)
	if na != nil && nb != nil {
		switch {
		case *na < *nb:
			return -1
		case *na > *nb:
			return 1
		}
		return 0
	}
	if a == nil && b != nil {
		return 1 // empty cells last
	}
	if b == nil && a != nil {
		return -1
	}
	return strings.Compare(strings.ToLower(cellText(a)), strings.ToLower(cellText(b)))
}

// fmtCell writes a value in a column's format (as text).
func fmtCell(c TableColumn, v any) string {
	if v == nil {
		return ""
	}
	n := number(v)
	dec := func(d int) int {
		if c.Decimals != nil {
			return *c.Decimals
		}
		return d
	}
	switch c.Format {
	case "number", "bar":
		if n == nil {
			return cellText(v)
		}
		d := 2
		if *n == math.Trunc(*n) {
			d = 0
		}
		return withUnit(c.Unit, group(*n, dec(d)))
	case "integer":
		if n == nil {
			return cellText(v)
		}
		return withUnit(c.Unit, group(math.Round(*n), 0))
	case "percent":
		if n == nil {
			return cellText(v)
		}
		x := *n
		if math.Abs(x) <= 1 && c.Unit != "%" { // a share (0.25), unless the unit says it is already %
			x *= 100
		}
		return group(x, dec(1)) + "%"
	case "currency":
		if n == nil {
			return cellText(v)
		}
		u := c.Unit
		if u == "" {
			u = "$"
		}
		return withUnit(u, group(*n, dec(2)))
	case "bytes":
		if n == nil {
			return cellText(v)
		}
		x, units := *n, []string{"B", "KB", "MB", "GB", "TB", "PB"}
		i := 0
		for math.Abs(x) >= 1024 && i < len(units)-1 {
			x /= 1024
			i++
		}
		return group(x, dec(map[bool]int{true: 0, false: 1}[i == 0])) + " " + units[i]
	case "duration_ms":
		if n == nil {
			return cellText(v)
		}
		return (time.Duration(*n * float64(time.Millisecond))).Round(time.Millisecond).String()
	case "date", "datetime":
		t := cellText(v)
		for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if tm, err := time.Parse(l, t); err == nil {
				if c.Format == "date" {
					return tm.Format("2006-01-02")
				}
				return tm.Format("2006-01-02 15:04")
			}
		}
		return t
	case "json":
		b, _ := json.Marshal(v)
		return string(b)
	}
	return cellText(v)
}

func withUnit(u, n string) string {
	switch u {
	case "":
		return n
	case "$", "€", "£", "¥":
		if strings.HasPrefix(n, "-") {
			return "-" + u + n[1:]
		}
		return u + n
	}
	return n + u
}

// group: digits in thousands (1,234,567.89).
func group(v float64, d int) string {
	s := strconv.FormatFloat(v, 'f', d, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	ip, fp, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range ip {
		if i > 0 && (len(ip)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if fp != "" {
		out += "." + fp
	}
	if neg {
		out = "-" + out
	}
	return out
}

var ruleColors = map[string][2]string{ // background, text
	"good": {"#dafbe1", "#116329"},
	"warn": {"#fff8c5", "#7d4e00"},
	"bad":  {"#ffebe9", "#a40e26"},
	"info": {"#ddf4ff", "#0a3069"},
}

func ruleColor(c string) [2]string {
	if x, ok := ruleColors[strings.ToLower(c)]; ok {
		return x
	}
	switch strings.ToLower(c) {
	case "green":
		return ruleColors["good"]
	case "yellow", "amber", "orange":
		return ruleColors["warn"]
	case "red":
		return ruleColors["bad"]
	case "blue":
		return ruleColors["info"]
	}
	if regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`).MatchString(c) {
		return [2]string{c, "#1f2328"}
	}
	return [2]string{"", ""}
}

func (r TableRule) match(v any) bool {
	switch strings.TrimSpace(r.Op) {
	case "empty":
		return v == nil || cellText(v) == ""
	case "contains":
		return strings.Contains(strings.ToLower(cellText(v)), strings.ToLower(cellText(r.Value)))
	}
	if v == nil {
		return false
	}
	c := compareCells(v, r.Value)
	switch strings.TrimSpace(r.Op) {
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case "=", "==":
		return c == 0
	case "!=", "<>":
		return c != 0
	}
	return false
}

// bandColor: a number's colour by the column's thresholds.
func bandColor(c TableColumn, v any) [2]string {
	n := number(v)
	if n == nil || len(c.Bands) == 0 {
		return [2]string{}
	}
	s := ChartSpec{Bands: c.Bands, LowerBetter: c.LowerBetter}
	t := s.zone(*n)
	switch {
	case t < 0.34:
		return ruleColors["bad"]
	case t < 0.67:
		return ruleColors["warn"]
	}
	return ruleColors["good"]
}

// sparkSVG: a small trend line of a list of numbers.
func sparkSVG(v any) string {
	var vals []float64
	switch l := v.(type) {
	case []any:
		for _, x := range l {
			if n := number(x); n != nil {
				vals = append(vals, *n)
			}
		}
	case string:
		var arr []any
		if json.Unmarshal([]byte(l), &arr) == nil {
			return sparkSVG(arr)
		}
	}
	if len(vals) < 2 {
		return ""
	}
	lo, hi := vals[0], vals[0]
	for _, x := range vals {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	const w, h = 96.0, 22.0
	var pts []string
	var lx, ly float64
	for i, x := range vals {
		lx = 2 + (w-4)*float64(i)/float64(len(vals)-1)
		ly = 2 + (h-4)*(1-(x-lo)/(hi-lo))
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", lx, ly))
	}
	return fmt.Sprintf(`<svg class="spark" width="96" height="22" viewBox="0 0 96 22"><polyline points="%s" fill="none" stroke="#4e79a7" stroke-width="1.5" stroke-linejoin="round"/><circle cx="%.1f" cy="%.1f" r="2" fill="#4e79a7"/></svg>`,
		strings.Join(pts, " "), lx, ly)
}

// TableCSS is the look of tables (also used by dashboards).
const TableCSS = `
.sw-table-wrap{overflow:auto;max-width:100%}
table.sw-table{border-collapse:collapse;font:13px/1.45 -apple-system,"Segoe UI",Helvetica,Arial,sans-serif;color:#1f2328;min-width:60%}
.sw-table caption{caption-side:top;text-align:left;font-weight:600;font-size:15px;padding:0 0 8px}
.sw-table caption small{display:block;font-weight:400;color:#57606a;font-size:12px}
.sw-table th,.sw-table td{padding:6px 12px;border-bottom:1px solid #d8dee4;white-space:nowrap}
.sw-table thead th{background:#f6f8fa;text-align:left;font-weight:600;border-bottom:2px solid #d0d7de;cursor:pointer;user-select:none}
.sw-table thead th.num{text-align:right}
.sw-table td.num{text-align:right;font-variant-numeric:tabular-nums}
.sw-table td.c{text-align:center}
.sw-table tbody tr:nth-child(even){background:#fbfcfd}
.sw-table tbody tr:hover{background:#f3f6f9}
.sw-table tfoot td{font-weight:600;border-top:2px solid #d0d7de;border-bottom:none;background:#f6f8fa}
.sw-table .bar{position:relative;min-width:110px}
.sw-table .bar i{position:absolute;left:0;top:5px;bottom:5px;background:#4e79a733;border-right:2px solid #4e79a7}
.sw-table .bar span{position:relative}
.sw-table .spark{vertical-align:middle}
.sw-table .more{color:#57606a;font-size:12px;padding:8px 0}
`

const tableSortJS = `<script>
document.querySelectorAll("table.sw-table").forEach(function(t){t.querySelectorAll("thead th").forEach(function(th,i){th.title="Sort";th.onclick=function(){var b=t.tBodies[0],rows=[].slice.call(b.rows),d=th.dataset.d==="1"?-1:1;th.dataset.d=d===1?"1":"0";rows.sort(function(x,y){var a=x.cells[i].dataset.v,c=y.cells[i].dataset.v,na=parseFloat(a),nc=parseFloat(c);if(a===""&&c!=="")return 1;if(c===""&&a!=="")return -1;return(!isNaN(na)&&!isNaN(nc)?na-nc:a.localeCompare(c))*d});rows.forEach(function(r){b.appendChild(r)})}})});
</script>`

// TableHTML is the table alone (with its title as caption), for a page or
// a dashboard.
func TableHTML(s TableSpec) string {
	var b strings.Builder
	b.WriteString(`<div class="sw-table-wrap"><table class="sw-table">`)
	if s.Title != "" || s.Caption != "" {
		b.WriteString("<caption>" + html.EscapeString(s.Title))
		if s.Caption != "" {
			b.WriteString("<small>" + html.EscapeString(s.Caption) + "</small>")
		}
		b.WriteString("</caption>")
	}
	// the largest value of each bar column
	maxOf := map[string]float64{}
	for _, c := range s.Columns {
		if c.Format == "bar" {
			for _, r := range s.Rows {
				if n := number(lookupField(r, c.Field)); n != nil {
					maxOf[c.Field] = math.Max(maxOf[c.Field], math.Abs(*n))
				}
			}
		}
	}
	b.WriteString("<thead><tr>")
	for _, c := range s.Columns {
		cls := ""
		if isNumberFormat(c.Format) || c.Align == "right" {
			cls = ` class="num"`
		}
		b.WriteString("<th" + cls + ">" + html.EscapeString(c.Label) + "</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, r := range s.Rows {
		rowStyle := ""
		var cells strings.Builder
		for _, c := range s.Columns {
			v := lookupField(r, c.Field)
			col := bandColor(c, v)
			for _, rule := range c.Rules {
				if rule.match(v) {
					if rule.Row {
						if rc := ruleColor(rule.Color); rc[0] != "" {
							rowStyle = fmt.Sprintf(` style="background:%s"`, rc[0])
						}
					} else {
						col = ruleColor(rule.Color)
					}
				}
			}
			var cls []string
			switch {
			case c.Align == "center":
				cls = append(cls, "c")
			case isNumberFormat(c.Format) || c.Align == "right":
				cls = append(cls, "num")
			}
			style := ""
			if col[0] != "" {
				style = fmt.Sprintf(` style="background:%s;color:%s"`, col[0], col[1])
			}
			sortKey := cellText(v)
			if n := number(v); n != nil {
				sortKey = strconv.FormatFloat(*n, 'g', -1, 64)
			}
			body := html.EscapeString(fmtCell(c, v))
			switch c.Format {
			case "sparkline":
				body, sortKey = sparkSVG(v), ""
			case "bar":
				if n := number(v); n != nil && maxOf[c.Field] > 0 {
					cls = append(cls, "bar")
					body = fmt.Sprintf(`<i style="width:%.1f%%"></i><span>%s</span>`, math.Abs(*n)/maxOf[c.Field]*100, body)
				}
			}
			if len(cls) > 0 {
				style = ` class="` + strings.Join(cls, " ") + `"` + style
			}
			fmt.Fprintf(&cells, `<td data-v="%s"%s>%s</td>`, html.EscapeString(sortKey), style, body)
		}
		b.WriteString("<tr" + rowStyle + ">" + cells.String() + "</tr>")
	}
	b.WriteString("</tbody>")
	if s.Totals {
		b.WriteString("<tfoot><tr>")
		for i, c := range s.Columns {
			txt := ""
			if c.Total != "" {
				var xs []float64
				for _, r := range s.Rows {
					if n := number(lookupField(r, c.Field)); n != nil {
						xs = append(xs, *n)
					}
				}
				if t, ok := aggregate(c.Total, xs, len(s.Rows)); ok {
					cc := c
					if c.Total == "count" {
						cc.Format, cc.Unit = "integer", ""
					}
					txt = fmtCell(cc, t)
				}
			} else if i == 0 {
				txt = "Total"
			}
			cls := ""
			if isNumberFormat(c.Format) {
				cls = ` class="num"`
			}
			b.WriteString("<td" + cls + ">" + html.EscapeString(txt) + "</td>")
		}
		b.WriteString("</tr></tfoot>")
	}
	b.WriteString("</table>")
	if s.Total > len(s.Rows) {
		fmt.Fprintf(&b, `<div class="more">The first %d of %d rows (max_rows).</div>`, len(s.Rows), s.Total)
	}
	b.WriteString("</div>")
	return b.String()
}

func aggregate(op string, xs []float64, rows int) (float64, bool) {
	if op == "count" {
		return float64(rows), true
	}
	if len(xs) == 0 {
		return 0, false
	}
	t := 0.0
	lo, hi := xs[0], xs[0]
	for _, x := range xs {
		t += x
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	switch op {
	case "sum":
		return t, true
	case "avg", "mean", "average":
		return t / float64(len(xs)), true
	case "min":
		return lo, true
	case "max":
		return hi, true
	}
	return 0, false
}

// TablePage is a whole HTML page holding the table.
func TablePage(s TableSpec) string {
	return fmt.Sprintf("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>%s</title><style>body{margin:16px;background:#fff}%s</style></head>\n<body %s>\n%s\n%s\n</body></html>\n",
		html.EscapeString(firstStr(s.Title, "Table")), TableCSS, TableMarker, TableHTML(s), tableSortJS)
}

// tableMarkdown: the table as Markdown (formatted values, no colours).
func tableMarkdown(s TableSpec) string {
	var b strings.Builder
	if s.Title != "" {
		b.WriteString("**" + s.Title + "**\n\n")
	}
	esc := func(t string) string { return strings.ReplaceAll(strings.ReplaceAll(t, "|", `\|`), "\n", " ") }
	var head, rule []string
	for _, c := range s.Columns {
		head = append(head, esc(c.Label))
		if isNumberFormat(c.Format) {
			rule = append(rule, "---:")
		} else {
			rule = append(rule, "---")
		}
	}
	b.WriteString("| " + strings.Join(head, " | ") + " |\n| " + strings.Join(rule, " | ") + " |\n")
	for _, r := range s.Rows {
		var cells []string
		for _, c := range s.Columns {
			v := lookupField(r, c.Field)
			if c.Format == "sparkline" {
				cells = append(cells, esc(fmtCell(TableColumn{Format: "json"}, v)))
				continue
			}
			cells = append(cells, esc(fmtCell(c, v)))
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return b.String()
}

func tableTool(_ context.Context, b *Box, a args) (string, error) {
	s, err := parseTable(a)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(a.str("format"), "markdown") {
		return tableMarkdown(s), nil
	}
	page := TablePage(s)
	p := strings.TrimSpace(a.str("path"))
	if p == "" {
		p = "out/tables/" + slugOf(s.Title, "table") + "-" + time.Now().Format("20060102-150405") + ".html"
	}
	if !strings.HasSuffix(strings.ToLower(p), ".html") {
		p += ".html"
	}
	abs, _, err := b.resolve(p)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(page), 0o644); err != nil {
		return "", err
	}
	return page, nil
}

// slugOf: a file name from a title.
func slugOf(title, def string) string {
	slug := strings.Trim(chartSlugRE.ReplaceAllString(strings.ToLower(firstStr(title, def)), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return firstStr(slug, def)
}

// PreviewTable makes the table page without saving it.
func PreviewTable(raw map[string]any) (string, error) {
	a := args(raw)
	s, err := parseTable(a)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(a.str("format"), "markdown") {
		return tableMarkdown(s), nil
	}
	return TablePage(s), nil
}
