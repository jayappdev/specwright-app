package flow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Conditions, used by Decision steps (which path to take) and Filter steps
// (which rows to keep). A condition is a group of rules joined by all /
// any / none; a rule can itself be a group, so conditions nest to any depth:
//
//	all of
//	  status = "open"
//	  any of
//	    priority >= 3
//	    labels contains "urgent"
//	  items[*].qty > 0  (every element)
//
// A rule's field is a path into the data — the step's input JSON for a
// decision, the row for a filter (customer.address.city, items[0].sku,
// items[*].qty, items.qty) — or a template ({{inputs.limit}}). Values are
// templates too, and {{this.<path>}} in a value reads the same data, so two
// fields can be compared (total > {{this.limit}}). Numbers compare as
// numbers, everything else as text (ISO dates compare correctly as text).
// When a field gives a list (a [*] or a key on a list), the rule holds when
// any element matches, or every element with Every.

// Cond is a group of rules.
type Cond struct {
	Match string `json:"match,omitempty"` // all (default) | any | none
	Rules []Rule `json:"rules,omitempty"`
}

// Rule compares one field, or is a nested group.
type Rule struct {
	Field      string `json:"field,omitempty"`
	Op         string `json:"op,omitempty"`
	Value      string `json:"value,omitempty"`
	Every      bool   `json:"every,omitempty"`      // a list matches only if every element does
	IgnoreCase bool   `json:"ignoreCase,omitempty"` // text comparisons
	Group      *Cond  `json:"group,omitempty"`
}

// CondOp is a comparison the designer offers.
type CondOp struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Unary bool   `json:"unary,omitempty"` // takes no value
}

// CondOps are the comparisons, in the order the designer lists them.
var CondOps = []CondOp{
	{ID: "eq", Name: "="}, {ID: "ne", Name: "≠"},
	{ID: "gt", Name: ">"}, {ID: "gte", Name: "≥"}, {ID: "lt", Name: "<"}, {ID: "lte", Name: "≤"},
	{ID: "contains", Name: "contains"}, {ID: "not_contains", Name: "does not contain"},
	{ID: "starts", Name: "starts with"}, {ID: "ends", Name: "ends with"},
	{ID: "matches", Name: "matches pattern"},
	{ID: "in", Name: "is one of (a, b, c)"}, {ID: "not_in", Name: "is not one of"},
	{ID: "between", Name: "is between (low, high)"},
	{ID: "empty", Name: "is empty", Unary: true}, {ID: "not_empty", Name: "is not empty", Unary: true},
	{ID: "exists", Name: "exists", Unary: true}, {ID: "not_exists", Name: "does not exist", Unary: true},
	{ID: "true", Name: "is true", Unary: true}, {ID: "false", Name: "is false", Unary: true},
	{ID: "len_eq", Name: "count / length ="}, {ID: "len_gt", Name: "count / length >"}, {ID: "len_lt", Name: "count / length <"},
}

var condOpByID = func() map[string]CondOp {
	m := map[string]CondOp{}
	for _, o := range CondOps {
		m[o.ID] = o
	}
	return m
}()

// CondMatches are the ways a group joins its rules.
var CondMatches = []string{"all", "any", "none"}

// Check reports the first problem with a condition, or nil.
func (c *Cond) Check() error {
	if c == nil || len(c.Rules) == 0 {
		return fmt.Errorf("add at least one condition")
	}
	if c.Match != "" && !contains(CondMatches, c.Match) {
		return fmt.Errorf("unknown match %q", c.Match)
	}
	for i, r := range c.Rules {
		if r.Group != nil {
			if err := r.Group.Check(); err != nil {
				return fmt.Errorf("group %d: %v", i+1, err)
			}
			continue
		}
		_, ok := condOpByID[r.Op]
		switch {
		case !ok:
			return fmt.Errorf("condition %d: choose a comparison", i+1)
		case r.Op == "matches" && !strings.Contains(r.Value, "{{"):
			if _, err := regexp.Compile(r.Value); err != nil {
				return fmt.Errorf("condition %d: the pattern is not a valid regular expression", i+1)
			}
		case strings.HasPrefix(r.Op, "len_") && !strings.Contains(r.Value, "{{"):
			if _, err := strconv.Atoi(strings.TrimSpace(r.Value)); err != nil {
				return fmt.Errorf("condition %d: the count must be a whole number", i+1)
			}
		case r.Op == "between" && !strings.Contains(r.Value, "{{") && len(splitList(r.Value)) != 2:
			return fmt.Errorf("condition %d: write the range as low, high", i+1)
		}
	}
	return nil
}

// Templates lists the condition's fields and values, for variable checks.
func (c *Cond) Templates() []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, r := range c.Rules {
		if r.Group != nil {
			out = append(out, r.Group.Templates()...)
			continue
		}
		out = append(out, r.Field, r.Value)
	}
	return out
}

// Eval tells whether data (parsed JSON, or text) satisfies the condition.
func (c *Cond) Eval(data any, d Data) (bool, error) {
	if c == nil || len(c.Rules) == 0 {
		return true, nil
	}
	match := firstNonEmptyStr(c.Match, "all")
	hits := 0
	for _, r := range c.Rules {
		var ok bool
		var err error
		if r.Group != nil {
			ok, err = r.Group.Eval(data, d)
		} else {
			ok, err = r.eval(data, d)
		}
		if err != nil {
			return false, err
		}
		if ok {
			hits++
		}
		switch {
		case match == "all" && !ok:
			return false, nil
		case match == "any" && ok:
			return true, nil
		case match == "none" && ok:
			return false, nil
		}
	}
	return match != "any" || hits > 0, nil
}

// fieldOf reads a rule's field: a path into data, or a template.
func fieldOf(field string, data any, d Data) (any, bool) {
	field = strings.TrimSpace(field)
	if strings.Contains(field, "{{") {
		s := renderWith(field, data, d)
		if v, err := decodeOrdered(s); err == nil && strings.TrimSpace(s) != "" {
			return v, true
		}
		return s, true
	}
	field = strings.TrimPrefix(strings.TrimPrefix(field, "this"), ".")
	if field == "" || field == "$" {
		return data, true
	}
	v, err := walkPath(data, field)
	if err != nil {
		return nil, false
	}
	return v, true
}

// renderWith fills a template where {{this.<path>}} reads data and every
// other variable comes from d.
func renderWith(tpl string, data any, d Data) string {
	return varRE.ReplaceAllStringFunc(tpl, func(m string) string {
		v := strings.TrimSpace(m[2 : len(m)-2])
		if v == "this" || strings.HasPrefix(v, "this.") {
			got, ok := fieldOf(strings.TrimPrefix(v, "this"), data, Data{})
			if !ok {
				return ""
			}
			return textOf(got)
		}
		s, _ := lookup(v, d)
		return s
	})
}

func (r Rule) eval(data any, d Data) (bool, error) {
	v, found := fieldOf(r.Field, data, d)
	want := renderWith(r.Value, data, d)
	switch r.Op {
	case "exists":
		return found && v != nil, nil
	case "not_exists":
		return !found || v == nil, nil
	case "empty":
		return isEmpty(v), nil
	case "not_empty":
		return !isEmpty(v), nil
	case "len_eq", "len_gt", "len_lt":
		n, err := strconv.Atoi(strings.TrimSpace(want))
		if err != nil {
			return false, fmt.Errorf("%s: the count %q is not a whole number", r.Field, want)
		}
		l := lengthOf(v)
		return (r.Op == "len_eq" && l == n) || (r.Op == "len_gt" && l > n) || (r.Op == "len_lt" && l < n), nil
	case "contains", "not_contains":
		// on a list: an element equals the value; on text: a substring
		has := false
		if list, ok := v.([]any); ok {
			for _, e := range list {
				if eq, _ := compare(e, want, r.IgnoreCase); eq == 0 {
					has = true
					break
				}
			}
		} else {
			a, b := textOf(v), want
			if r.IgnoreCase {
				a, b = strings.ToLower(a), strings.ToLower(b)
			}
			has = found && strings.Contains(a, b)
		}
		return has == (r.Op == "contains"), nil
	}
	if !found {
		return r.Op == "ne" || r.Op == "not_in", nil
	}
	var re *regexp.Regexp
	if r.Op == "matches" {
		p := want
		if r.IgnoreCase {
			p = "(?i)" + p
		}
		var err error
		if re, err = regexp.Compile(p); err != nil {
			return false, fmt.Errorf("the pattern %q is not a valid regular expression", want)
		}
	}
	one := func(x any) bool { return r.compareOne(x, want, re) }
	// a list is compared element by element, unless it is compared with a
	// whole JSON list (items = ["a","b"])
	if list, ok := v.([]any); ok && !((r.Op == "eq" || r.Op == "ne") && looksLikeJSONList(want)) {
		if len(list) == 0 {
			return r.Every && r.Op != "true", nil
		}
		for _, e := range list {
			m := one(e)
			if r.Every && !m {
				return false, nil
			}
			if !r.Every && m {
				return true, nil
			}
		}
		return r.Every, nil
	}
	return one(v), nil
}

func looksLikeJSONList(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "[") }

func (r Rule) compareOne(x any, want string, re *regexp.Regexp) bool {
	switch r.Op {
	case "true":
		return truthy(x)
	case "false":
		return !truthy(x)
	case "matches":
		return re.MatchString(textOf(x))
	case "starts", "ends":
		a, b := textOf(x), want
		if r.IgnoreCase {
			a, b = strings.ToLower(a), strings.ToLower(b)
		}
		if r.Op == "starts" {
			return strings.HasPrefix(a, b)
		}
		return strings.HasSuffix(a, b)
	case "in", "not_in":
		hit := false
		for _, w := range splitList(want) {
			if c, _ := compare(x, w, r.IgnoreCase); c == 0 {
				hit = true
				break
			}
		}
		return hit == (r.Op == "in")
	case "between":
		lh := splitList(want)
		if len(lh) != 2 {
			return false
		}
		lo, ok1 := compare(x, lh[0], r.IgnoreCase)
		hi, ok2 := compare(x, lh[1], r.IgnoreCase)
		return ok1 && ok2 && lo >= 0 && hi <= 0
	}
	c, ok := compare(x, want, r.IgnoreCase)
	switch r.Op {
	case "eq":
		return c == 0
	case "ne":
		return c != 0
	case "gt":
		return ok && c > 0
	case "gte":
		return ok && c >= 0
	case "lt":
		return ok && c < 0
	case "lte":
		return ok && c <= 0
	}
	return false
}

// compare orders a value against text: as numbers when both are numbers,
// as true/false, else as text. ok is false when one side is missing.
func compare(x any, want string, fold bool) (int, bool) {
	if x == nil {
		if strings.TrimSpace(want) == "" || want == "null" {
			return 0, true
		}
		return -1, false
	}
	a := textOf(x)
	if fa, ok := num(x); ok {
		if fb, err := strconv.ParseFloat(strings.TrimSpace(want), 64); err == nil {
			switch {
			case fa < fb:
				return -1, true
			case fa > fb:
				return 1, true
			}
			return 0, true
		}
	}
	if b, ok := x.(bool); ok {
		w := strings.ToLower(strings.TrimSpace(want))
		if w == "true" || w == "false" {
			if b == (w == "true") {
				return 0, true
			}
			return 1, true
		}
	}
	if _, isObj := x.(*Obj); isObj || isList(x) { // structures compare as compact JSON
		var b bytes.Buffer
		if json.Compact(&b, []byte(want)) == nil {
			want = b.String()
		}
	}
	if fold {
		a, want = strings.ToLower(a), strings.ToLower(want)
	}
	return strings.Compare(a, strings.TrimSpace(want)), true
}

func isList(x any) bool { _, ok := x.([]any); return ok }

// num reads a number from a JSON value or numeric text.
func num(x any) (float64, bool) {
	switch v := x.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil && strings.TrimSpace(v) != "" && !math.IsNaN(f)
	}
	return 0, false
}

func truthy(x any) bool {
	switch v := x.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s != "" && s != "false" && s != "no" && s != "0" && s != "off"
	}
	if f, ok := num(x); ok {
		return f != 0
	}
	return !isEmpty(x)
}

func isEmpty(x any) bool {
	switch v := x.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		return len(v) == 0
	case *Obj:
		return len(v.Keys) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func lengthOf(x any) int {
	switch v := x.(type) {
	case nil:
		return 0
	case string:
		return len([]rune(v))
	case []any:
		return len(v)
	case *Obj:
		return len(v.Keys)
	case map[string]any:
		return len(v)
	}
	return len(textOf(x))
}

// splitList reads "a, b, c" (or a JSON array) into its parts.
func splitList(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var arr []any
		if json.Unmarshal([]byte(s), &arr) == nil {
			out := make([]string, len(arr))
			for i, v := range arr {
				out[i] = textOf(v)
			}
			return out
		}
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
