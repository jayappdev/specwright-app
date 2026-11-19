package flow

// JSON → table transforms: a JSON array (of objects, or of plain values) as
// a Markdown or an HTML table. The columns are the fields in the order the
// JSON has them (first seen first), or the ones the step names, each a
// path into the element (user.email, tags[0]) with an optional header
// ("user.email as Email"). An object that holds the list ({"items": [...]})
// gives the list's table; any other object is a table of one row.

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

type tableCol struct{ head, path string }

var colAsRE = regexp.MustCompile(`(?i)^(.+?)\s+as\s+(.+)$`)

// tableColumns reads the step's column list: "a, b.c as Title, d".
func tableColumns(expr string) []tableCol {
	var out []tableCol
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		c := tableCol{head: part, path: part}
		if m := colAsRE.FindStringSubmatch(part); m != nil {
			c.path, c.head = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		}
		out = append(out, c)
	}
	return out
}

// tableRowsOf finds the rows: the array itself, the one list of an object
// that holds one ({"items": [...]}), or the object as a single row.
func tableRowsOf(v any) ([]any, error) {
	switch x := v.(type) {
	case []any:
		return x, nil
	case *Obj:
		var lists []string
		for _, k := range x.Keys {
			if a, ok := x.Vals[k].([]any); ok && len(a) > 0 {
				if _, isObj := a[0].(*Obj); isObj {
					lists = append(lists, k)
				}
			}
		}
		if len(lists) == 1 {
			return x.Vals[lists[0]].([]any), nil
		}
		return []any{x}, nil
	}
	return nil, errors.New("a table needs a JSON array (or an object holding one)")
}

// tableOf is the header and the cells of the table for in.
func tableOf(in, expr string) ([]string, [][]string, error) {
	v, err := decodeOrdered(in)
	if err != nil {
		return nil, nil, fmt.Errorf("the input is not JSON: %v", err)
	}
	rows, err := tableRowsOf(v)
	if err != nil {
		return nil, nil, err
	}
	cols := tableColumns(expr)
	if len(cols) == 0 { // every field, first seen first
		seen := map[string]bool{}
		plain := false
		for _, r := range rows {
			o, ok := r.(*Obj)
			if !ok {
				plain = true
				continue
			}
			for _, k := range o.Keys {
				if !seen[k] {
					seen[k] = true
					cols = append(cols, tableCol{head: k, path: `["` + k + `"]`})
				}
			}
		}
		if plain { // plain values (or a mix): their own column
			cols = append(cols, tableCol{head: "value", path: ""})
		}
	}
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = c.head
	}
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			if c.path == "" {
				if _, isObj := r.(*Obj); !isObj {
					cells[i] = cellText(r)
				}
				continue
			}
			if _, isObj := r.(*Obj); !isObj {
				continue
			}
			if x, err := walkPath(r, c.path); err == nil {
				cells[i] = cellText(x)
			}
		}
		out = append(out, cells)
	}
	return head, out, nil
}

// cellText is a value as one cell: text as it is, anything else as compact
// JSON.
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func jsonToMDTable(in string, n Node, _ Data) (string, error) {
	head, rows, err := tableOf(in, n.Expr)
	if err != nil {
		return "", err
	}
	if len(head) == 0 {
		return "", errors.New("the array is empty: there is no table to make")
	}
	var b strings.Builder
	line := func(cells []string) {
		cs := make([]string, len(cells))
		for i, c := range cells {
			cs[i] = TableCell(c)
		}
		b.WriteString("| " + strings.Join(cs, " | ") + " |\n")
	}
	line(head)
	b.WriteString("|" + strings.Repeat(" --- |", len(head)) + "\n")
	for _, r := range rows {
		line(r)
	}
	return b.String(), nil
}

func jsonToHTMLTable(in string, n Node, _ Data) (string, error) {
	head, rows, err := tableOf(in, n.Expr)
	if err != nil {
		return "", err
	}
	if len(head) == 0 {
		return "", errors.New("the array is empty: there is no table to make")
	}
	var b strings.Builder
	b.WriteString("<table>\n<thead><tr>")
	for _, h := range head {
		b.WriteString("<th>" + html.EscapeString(h) + "</th>")
	}
	b.WriteString("</tr></thead>\n<tbody>\n")
	for _, r := range rows {
		b.WriteString("<tr>")
		for _, c := range r {
			b.WriteString("<td>" + strings.ReplaceAll(html.EscapeString(c), "\n", "<br>") + "</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody>\n</table>\n")
	return b.String(), nil
}
