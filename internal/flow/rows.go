package flow

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Rows: Filter and Aggregate steps work on a list of rows — a JSON array
// (of objects, or of plain values), a JSON object holding one array, CSV
// with a header line, or text with one row per line. JSON and CSV come out
// as a JSON array; text comes out as text, one row per line.

// RowFormats are how a Filter or Aggregate step reads its input (Node.Split).
var RowFormats = []string{"auto", "json", "csv", "lines"}

// ParseRows reads rows; asText reports that they came from plain lines.
func ParseRows(in, format string) (rows []any, asText bool, err error) {
	s := strings.TrimSpace(in)
	format = firstNonEmptyStr(format, "auto")
	if s == "" {
		return []any{}, format == "lines", nil
	}
	if format == "auto" || format == "json" {
		if v, err := decodeOrdered(s); err == nil {
			switch x := v.(type) {
			case []any:
				return x, false, nil
			case *Obj: // an object holding one list: {"items": [...]}
				var list []any
				n := 0
				for _, k := range x.Keys {
					if l, ok := x.Vals[k].([]any); ok {
						list, n = l, n+1
					}
				}
				if n == 1 {
					return list, false, nil
				}
				return []any{x}, false, nil
			}
			return []any{v}, false, nil
		} else if format == "json" {
			return nil, false, fmt.Errorf("the input is not JSON: %v", err)
		}
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if format == "csv" || (format == "auto" && len(lines) > 1 && strings.Contains(lines[0], ",")) {
		return parseCSVRows(s)
	}
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			rows = append(rows, strings.TrimSpace(l))
		}
	}
	return rows, true, nil
}

func parseCSVRows(s string) ([]any, bool, error) {
	r := csv.NewReader(strings.NewReader(s))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, false, fmt.Errorf("the input is not CSV: %v", err)
	}
	if len(recs) == 0 {
		return []any{}, false, nil
	}
	head := recs[0]
	out := make([]any, 0, len(recs)-1)
	for _, rec := range recs[1:] {
		o := &Obj{Vals: map[string]any{}}
		for i, h := range head {
			v := ""
			if i < len(rec) {
				v = rec[i]
			}
			if _, err := strconv.ParseFloat(v, 64); err == nil && v != "" {
				o.set(h, json.Number(v)) // numbers stay numbers
			} else {
				o.set(h, v)
			}
		}
		out = append(out, o)
	}
	return out, false, nil
}

// writeRows formats rows the way they came in.
func writeRows(rows []any, asText bool) (string, error) {
	if asText {
		parts := make([]string, len(rows))
		for i, r := range rows {
			parts[i] = textOf(r)
		}
		return strings.Join(parts, "\n"), nil
	}
	if rows == nil {
		rows = []any{}
	}
	var b bytes.Buffer
	if err := json.Indent(&b, rawJSON(rows), "", "  "); err != nil {
		return "", err
	}
	return b.String(), nil
}

// --- filter -------------------------------------------------------------------

// FilterModes: keep the rows that match, or drop them.
var FilterModes = []string{"keep", "drop"}

// FilterRows keeps (or drops) the rows that match the step's condition.
// Unique drops repeated rows; Max keeps at most that many.
func FilterRows(n Node, in string, d Data) (string, error) {
	rows, asText, err := ParseRows(in, n.Split)
	if err != nil {
		return "", err
	}
	keep := n.Mode != "drop"
	seen := map[string]bool{}
	out := []any{}
	for _, r := range rows {
		ok, err := n.Cond.Eval(r, d)
		if err != nil {
			return "", err
		}
		if ok != keep {
			continue
		}
		if n.Unique {
			k := string(rawJSON(r))
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, r)
		if n.Max > 0 && len(out) == n.Max && len(n.OrderBy) == 0 {
			break
		}
	}
	if len(n.OrderBy) > 0 { // sort first, then keep the first Max: a top N
		sortRows(out, n.OrderBy, d)
	}
	if n.Max > 0 && len(out) > n.Max {
		out = out[:n.Max]
	}
	return writeRows(out, asText)
}

// --- decision -----------------------------------------------------------------

// Branch is one path out of a Decision step: the connections marked with
// its id run when its condition holds.
type Branch struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	When  *Cond  `json:"when,omitempty"`
}

// ElseBranch is the path a Decision takes when no branch matches.
const ElseBranch = "else"

// DecisionModes: take the first branch that matches, or every one.
var DecisionModes = []string{"first", "all"}

// Decide picks the branches a Decision step takes for its input: the first
// whose condition holds (or all of them, in "all" mode), else "else". The
// input is read as JSON when it is JSON, else as text.
func Decide(n Node, in string, d Data) ([]string, error) {
	var data any = in
	if v, err := decodeOrdered(in); err == nil && strings.TrimSpace(in) != "" {
		data = v
	}
	var out []string
	for _, b := range n.Branches {
		ok, err := b.When.Eval(data, d)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", firstNonEmptyStr(b.Label, b.ID), err)
		}
		if ok {
			out = append(out, b.ID)
			if n.Mode != "all" {
				break
			}
		}
	}
	if len(out) == 0 {
		out = []string{ElseBranch}
	}
	return out, nil
}

// LiveParents lists the steps connected into id whose output reaches it:
// not those that were off the path taken (off), nor a Decision connected
// through a branch it did not take. A step with parents but no live parent
// is not on the path the flow took.
func (f *Flow) LiveParents(id string, off func(string) bool, branches map[string][]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range f.Edges {
		if e.To != id || seen[e.From] || off(e.From) {
			continue
		}
		if pn := f.Node(e.From); pn != nil && pn.Type == "decision" && e.Branch != "" && !contains(branches[e.From], e.Branch) {
			continue
		}
		seen[e.From] = true
		out = append(out, e.From)
	}
	return out
}

// BranchLabel names a branch of a decision.
func BranchLabel(n Node, id string) string {
	if id == ElseBranch {
		return "Otherwise"
	}
	for _, b := range n.Branches {
		if b.ID == id {
			return firstNonEmptyStr(b.Label, b.ID)
		}
	}
	return id
}

// --- aggregate ----------------------------------------------------------------

// Measure is one aggregated value: Fn over Field, written as As.
type Measure struct {
	As    string `json:"as,omitempty"`
	Fn    string `json:"fn"`
	Field string `json:"field,omitempty"`
	Arg   string `json:"arg,omitempty"` // concat: separator; lag/lead: offset; ntile: buckets
}

// AggFn describes an aggregate function for the designer.
type AggFn struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Window bool   `json:"window,omitempty"`  // only in window mode
	NoArg  bool   `json:"noField,omitempty"` // needs no field
	ArgFor string `json:"arg,omitempty"`     // what Arg means
}

// AggFns are the aggregate and window functions.
var AggFns = []AggFn{
	{ID: "count", Name: "count", NoArg: true},
	{ID: "count_distinct", Name: "distinct count"},
	{ID: "sum", Name: "sum"}, {ID: "avg", Name: "average"},
	{ID: "min", Name: "min"}, {ID: "max", Name: "max"}, {ID: "median", Name: "median"},
	{ID: "first", Name: "first"}, {ID: "last", Name: "last"},
	{ID: "list", Name: "list of values"}, {ID: "distinct", Name: "distinct values"},
	{ID: "concat", Name: "join as text", ArgFor: "Separator (default , )"},
	{ID: "row_number", Name: "row number", Window: true, NoArg: true},
	{ID: "rank", Name: "rank", Window: true, NoArg: true},
	{ID: "dense_rank", Name: "dense rank", Window: true, NoArg: true},
	{ID: "running_sum", Name: "running sum", Window: true},
	{ID: "running_count", Name: "running count", Window: true, NoArg: true},
	{ID: "running_avg", Name: "running average", Window: true},
	{ID: "lag", Name: "previous row's value (lag)", Window: true, ArgFor: "Rows back (default 1)"},
	{ID: "lead", Name: "next row's value (lead)", Window: true, ArgFor: "Rows ahead (default 1)"},
	{ID: "pct_of_total", Name: "% of total", Window: true},
	{ID: "ntile", Name: "bucket (ntile)", Window: true, NoArg: true, ArgFor: "Buckets (default 4)"},
}

var aggFnByID = func() map[string]AggFn {
	m := map[string]AggFn{}
	for _, f := range AggFns {
		m[f.ID] = f
	}
	return m
}()

// AggregateModes: one row per group, or every row with its group's values
// (a window, like SQL's OVER (PARTITION BY … ORDER BY …)).
var AggregateModes = []string{"group", "window"}

func measureName(m Measure) string {
	if strings.TrimSpace(m.As) != "" {
		return strings.TrimSpace(m.As)
	}
	if m.Field == "" {
		return m.Fn
	}
	return m.Fn + "_" + strings.NewReplacer(".", "_", "[", "_", "]", "", "*", "all").Replace(m.Field)
}

// CheckAggregate reports the first problem with an Aggregate step.
func CheckAggregate(n Node) error {
	mode := firstNonEmptyStr(n.Mode, "group")
	if !contains(AggregateModes, mode) {
		return fmt.Errorf("choose group or window")
	}
	if len(n.Measures) == 0 && len(n.GroupBy) == 0 {
		return fmt.Errorf("add a value to calculate, or a field to group by")
	}
	names := map[string]bool{}
	for i, m := range n.Measures {
		fn, ok := aggFnByID[m.Fn]
		switch {
		case !ok:
			return fmt.Errorf("value %d: choose a function", i+1)
		case fn.Window && mode != "window":
			return fmt.Errorf("value %d: %s works in window mode (every row, with its group's values)", i+1, fn.Name)
		case !fn.NoArg && strings.TrimSpace(m.Field) == "" && m.Fn != "count_distinct" && m.Fn != "list" && m.Fn != "distinct":
			return fmt.Errorf("value %d: choose the field to %s", i+1, fn.Name)
		case (m.Fn == "lag" || m.Fn == "lead" || m.Fn == "ntile") && strings.TrimSpace(m.Arg) != "":
			if k, err := strconv.Atoi(strings.TrimSpace(m.Arg)); err != nil || k < 1 {
				return fmt.Errorf("value %d: %s needs a whole number of at least 1", i+1, fn.Name)
			}
		}
		name := measureName(m)
		if names[name] {
			return fmt.Errorf("two values are called %q: give each its own name", name)
		}
		names[name] = true
	}
	return nil
}

// Aggregate groups rows by GroupBy and calculates the measures: one row per
// group (group mode), or every row with its group's values added (window
// mode; OrderBy orders the rows within each group for ranks, running values
// and lag/lead). In group mode OrderBy sorts the result. Max keeps at most
// that many rows.
func Aggregate(n Node, in string, d Data) (string, error) {
	if err := CheckAggregate(n); err != nil {
		return "", err
	}
	rows, _, err := ParseRows(in, n.Split)
	if err != nil {
		return "", err
	}
	type group struct {
		key  []any
		rows []int // indexes into rows
	}
	var groups []*group
	byKey := map[string]*group{}
	for i, r := range rows {
		key := make([]any, len(n.GroupBy))
		for j, g := range n.GroupBy {
			key[j], _ = fieldOf(g, r, d)
		}
		k := string(rawJSON(key))
		g := byKey[k]
		if g == nil {
			g = &group{key: key}
			byKey[k] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, i)
	}
	if len(rows) == 0 && len(n.GroupBy) == 0 { // totals of nothing
		groups = []*group{{}}
	}
	values := func(idx []int, field string) []any {
		var out []any
		for _, i := range idx {
			if strings.TrimSpace(field) == "" {
				out = append(out, rows[i])
				continue
			}
			v, ok := fieldOf(field, rows[i], d)
			if !ok {
				continue
			}
			if l, isList := v.([]any); isList { // items[*].qty: every value counts
				out = append(out, l...)
			} else {
				out = append(out, v)
			}
		}
		return out
	}
	var out []any
	if firstNonEmptyStr(n.Mode, "group") == "group" {
		for _, g := range groups {
			o := &Obj{Vals: map[string]any{}}
			for j, f := range n.GroupBy {
				o.set(f, g.key[j])
			}
			for _, m := range n.Measures {
				o.set(measureName(m), aggregate(m, values(g.rows, m.Field), len(g.rows)))
			}
			out = append(out, o)
		}
		if len(n.OrderBy) > 0 {
			sortRows(out, n.OrderBy, d)
		}
	} else {
		extra := make([]map[string]any, len(rows))
		for i := range extra {
			extra[i] = map[string]any{}
		}
		for _, g := range groups {
			ordered := append([]int(nil), g.rows...)
			if len(n.OrderBy) > 0 {
				sort.SliceStable(ordered, func(a, b int) bool { return rowLess(rows[ordered[a]], rows[ordered[b]], n.OrderBy, d) })
			}
			for _, m := range n.Measures {
				name := measureName(m)
				if fn := aggFnByID[m.Fn]; !fn.Window {
					v := aggregate(m, values(g.rows, m.Field), len(g.rows))
					for _, i := range g.rows {
						extra[i][name] = v
					}
					continue
				}
				windowValues(m, name, ordered, rows, extra, n.OrderBy, d)
			}
		}
		for i, r := range rows {
			o := &Obj{Vals: map[string]any{}}
			if src, ok := r.(*Obj); ok {
				for _, k := range src.Keys {
					o.set(k, src.Vals[k])
				}
			} else {
				o.set("value", r)
			}
			for _, m := range n.Measures {
				o.set(measureName(m), extra[i][measureName(m)])
			}
			out = append(out, o)
		}
	}
	if n.Max > 0 && len(out) > n.Max {
		out = out[:n.Max]
	}
	return writeRows(out, false)
}

func aggregate(m Measure, vals []any, nrows int) any {
	switch m.Fn {
	case "count":
		if strings.TrimSpace(m.Field) == "" {
			return nrows
		}
		c := 0
		for _, v := range vals {
			if !isEmpty(v) {
				c++
			}
		}
		return c
	case "count_distinct":
		return len(distinct(vals))
	case "sum", "avg", "median":
		var ns []float64
		for _, v := range vals {
			if f, ok := num(v); ok {
				ns = append(ns, f)
			}
		}
		if len(ns) == 0 {
			if m.Fn == "sum" {
				return 0
			}
			return nil
		}
		sum := 0.0
		for _, f := range ns {
			sum += f
		}
		switch m.Fn {
		case "sum":
			return round(sum)
		case "avg":
			return round(sum / float64(len(ns)))
		}
		sort.Float64s(ns)
		if len(ns)%2 == 1 {
			return round(ns[len(ns)/2])
		}
		return round((ns[len(ns)/2-1] + ns[len(ns)/2]) / 2)
	case "min", "max":
		var best any
		for _, v := range vals {
			if v == nil {
				continue
			}
			if best == nil {
				best = v
				continue
			}
			c := compareVals(v, best)
			if (m.Fn == "min" && c < 0) || (m.Fn == "max" && c > 0) {
				best = v
			}
		}
		return best
	case "first", "last":
		if len(vals) == 0 {
			return nil
		}
		if m.Fn == "first" {
			return vals[0]
		}
		return vals[len(vals)-1]
	case "list":
		if vals == nil {
			return []any{}
		}
		return vals
	case "distinct":
		return distinct(vals)
	case "concat":
		sep := m.Arg
		if sep == "" {
			sep = ", "
		}
		parts := make([]string, 0, len(vals))
		for _, v := range vals {
			if !isEmpty(v) {
				parts = append(parts, textOf(v))
			}
		}
		return strings.Join(parts, strings.ReplaceAll(sep, `\n`, "\n"))
	}
	return nil
}

// windowValues fills a window function for one group, rows in order.
func windowValues(m Measure, name string, ordered []int, rows []any, extra []map[string]any, orderBy []string, d Data) {
	val := func(i int) any {
		if strings.TrimSpace(m.Field) == "" {
			return rows[i]
		}
		v, _ := fieldOf(m.Field, rows[i], d)
		return v
	}
	k := 1
	if a, err := strconv.Atoi(strings.TrimSpace(m.Arg)); err == nil && a > 0 {
		k = a
	}
	switch m.Fn {
	case "row_number", "running_count":
		for pos, i := range ordered {
			extra[i][name] = pos + 1
		}
	case "rank", "dense_rank":
		rank, dense := 0, 0
		for pos, i := range ordered {
			if pos == 0 || len(orderBy) == 0 || rowLess(rows[ordered[pos-1]], rows[i], orderBy, d) || rowLess(rows[i], rows[ordered[pos-1]], orderBy, d) {
				rank, dense = pos+1, dense+1
			}
			if m.Fn == "rank" {
				extra[i][name] = rank
			} else {
				extra[i][name] = dense
			}
		}
	case "running_sum", "running_avg":
		sum, c := 0.0, 0
		for _, i := range ordered {
			if f, ok := num(val(i)); ok {
				sum += f
				c++
			}
			if m.Fn == "running_sum" {
				extra[i][name] = round(sum)
			} else if c > 0 {
				extra[i][name] = round(sum / float64(c))
			} else {
				extra[i][name] = nil
			}
		}
	case "lag", "lead":
		for pos, i := range ordered {
			j := pos - k
			if m.Fn == "lead" {
				j = pos + k
			}
			if j >= 0 && j < len(ordered) {
				extra[i][name] = val(ordered[j])
			} else {
				extra[i][name] = nil
			}
		}
	case "pct_of_total":
		total := 0.0
		for _, i := range ordered {
			if f, ok := num(val(i)); ok {
				total += f
			}
		}
		for _, i := range ordered {
			f, ok := num(val(i))
			if !ok || total == 0 {
				extra[i][name] = nil
				continue
			}
			extra[i][name] = math.Round(f/total*10000) / 100
		}
	case "ntile":
		b := 4
		if a, err := strconv.Atoi(strings.TrimSpace(m.Arg)); err == nil && a > 0 {
			b = a
		}
		for pos, i := range ordered {
			extra[i][name] = pos*b/len(ordered) + 1
		}
	}
}

func distinct(vals []any) []any {
	seen := map[string]bool{}
	out := []any{}
	for _, v := range vals {
		k := string(rawJSON(v))
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}

func round(f float64) any {
	r := math.Round(f*1e9) / 1e9
	if r == math.Trunc(r) && math.Abs(r) < 1e15 {
		return int64(r)
	}
	return r
}

// sortRows orders rows by fields ("field" ascending, "-field" descending).
func sortRows(rows []any, by []string, d Data) {
	sort.SliceStable(rows, func(a, b int) bool { return rowLess(rows[a], rows[b], by, d) })
}

func rowLess(a, b any, by []string, d Data) bool {
	for _, f := range by {
		f = strings.TrimSpace(f)
		desc := strings.HasPrefix(f, "-")
		f = strings.TrimPrefix(strings.TrimPrefix(f, "-"), "+")
		va, _ := fieldOf(f, a, d)
		vb, _ := fieldOf(f, b, d)
		c := compareVals(va, vb)
		if c == 0 {
			continue
		}
		if desc {
			return c > 0
		}
		return c < 0
	}
	return false
}

// compareVals orders two values: numbers as numbers, else as text; missing
// values come last.
func compareVals(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	fa, ok1 := num(a)
	fb, ok2 := num(b)
	if ok1 && ok2 {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(textOf(a), textOf(b))
}
