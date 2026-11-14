package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Loops. A Loop step splits a list (a step's output, or any template) into
// items; the steps after it run once per item, until a Gather step, which
// collects what they produced. Loop and Gather pair up like brackets along
// the run order, so loops can be nested:
//
//	tool → Loop ──→ tool → AI prompt ──→ Gather → output
//	          └──→ output (saved per item)
//
// Inside a loop {{item}} is the current item ({{item.json.<path>}} for
// JSON items), {{item_index}} its position (1-based) and {{item_count}} the
// number of items. The loop step's own output, as seen by the steps after
// it, is the item.

// Loop split modes and gather modes.
var (
	SplitModes  = []string{"auto", "lines", "json", "csv", "regex"}
	GatherModes = []string{"concat", "sections", "json-array", "json-object", "template"}
)

// MaxLoopItems caps how many items one loop runs.
const MaxLoopItems = 1000

// LoopInfo is how a flow's loops pair up.
type LoopInfo struct {
	Gather map[string]string   // loop id → the gather that closes it
	Loop   map[string]string   // gather id → its loop
	Stack  map[string][]string // step id → the loops it runs inside, outermost first
}

// Body lists the steps inside loop (in run order), nested loops included.
func (li LoopInfo) Body(order []string, loop string) []string {
	var out []string
	for _, id := range order {
		if id == loop {
			continue
		}
		for _, l := range li.Stack[id] {
			if l == loop {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

// Loops pairs every Loop with its Gather and finds which steps run inside
// which loops. Problems are returned as issues.
func (f *Flow) Loops() (LoopInfo, []Issue) {
	li := LoopInfo{Gather: map[string]string{}, Loop: map[string]string{}, Stack: map[string][]string{}}
	var is []Issue
	order, err := f.Order()
	if err != nil {
		return li, nil // reported elsewhere
	}
	for _, id := range order {
		n := f.Node(id)
		var stack []string
		first := true
		for _, p := range f.Parents(id) {
			ps := li.Stack[p]
			if pn := f.Node(p); pn != nil && pn.Type == "loop" {
				ps = append(append([]string{}, ps...), p)
			}
			if first {
				stack, first = ps, false
				continue
			}
			if strings.Join(ps, ">") != strings.Join(stack, ">") {
				is = append(is, Issue{Node: id, Level: "error", Message: fmt.Sprintf("%s joins steps from inside and outside a loop. Connect the loop's steps through its Gather first.", label(*n))})
				if len(ps) < len(stack) {
					stack = ps
				}
			}
		}
		if n.Type == "gather" {
			if len(stack) == 0 {
				is = append(is, Issue{Node: id, Level: "error", Message: fmt.Sprintf("%s has no Loop before it: connect it after the steps of a Loop.", label(*n))})
			} else {
				l := stack[len(stack)-1]
				if g, ok := li.Gather[l]; ok {
					is = append(is, Issue{Node: id, Level: "error", Message: fmt.Sprintf("The loop %s already ends at %s; one Gather per loop.", label(*f.Node(l)), label(*f.Node(g)))})
				} else {
					li.Gather[l], li.Loop[id] = id, l
				}
				stack = stack[:len(stack)-1]
			}
		}
		li.Stack[id] = stack
	}
	for _, n := range f.Nodes {
		if n.Type == "loop" {
			if _, ok := li.Gather[n.ID]; !ok {
				is = append(is, Issue{Node: n.ID, Level: "error", Message: fmt.Sprintf("%s needs a Gather step after the steps it repeats, to collect their results.", label(n))})
			}
		}
		if n.Type == "start" && len(li.Stack[n.ID]) > 0 {
			is = append(is, Issue{Node: n.ID, Level: "error", Message: "The Inputs step cannot be inside a loop."})
		}
	}
	return li, is
}

// SplitItems turns a list into loop items. When the list arrives inside a
// JSON object — the Inputs step gives {"stocks": "AMZN\nMSFT"}, a tool
// gives {"data": {"items": [...]}} — the list is taken from the object:
// from the loop's Field (a path like stocks or data.items), else from its
// only field, else from its only list. A list that is text is split as the
// loop says (one per line by default); a JSON array gives its elements.
func SplitItems(src string, n Node) ([]string, error) {
	src = strings.TrimSpace(src)
	mode := n.Split
	if mode == "" {
		mode = "auto"
	}
	if mode != "regex" && (strings.TrimSpace(n.Field) != "" || strings.HasPrefix(src, "{")) {
		inner, isList, err := listIn(src, n.Field, mode)
		if err != nil {
			return nil, err
		}
		if isList || inner != src {
			src = strings.TrimSpace(inner)
			if isList && (mode == "auto" || mode == "lines") {
				mode = "json"
			}
		}
		if mode == "json" && !isList && !strings.HasPrefix(src, "[") {
			return nil, fmt.Errorf("the list is text, not a JSON array: split it “One item per line” (or Detect)")
		}
	}
	if mode == "auto" {
		mode = "lines"
		if strings.HasPrefix(src, "[") {
			var probe []any
			if json.Unmarshal([]byte(src), &probe) == nil {
				mode = "json"
			}
		}
	}
	var raw []string
	switch mode {
	case "json":
		var arr []any
		if err := json.Unmarshal([]byte(src), &arr); err != nil {
			return nil, fmt.Errorf("the list is not a JSON array: %v", err)
		}
		for _, v := range arr {
			if s, ok := v.(string); ok {
				raw = append(raw, s)
			} else {
				b, _ := json.Marshal(v)
				raw = append(raw, string(b))
			}
		}
	case "csv":
		rows, err := Transform(Node{Op: "csv-to-json"}, src, Data{})
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		_ = json.Unmarshal([]byte(rows), &arr)
		for _, r := range arr {
			b, _ := json.Marshal(r)
			raw = append(raw, string(b))
		}
	case "regex":
		re, err := regexp.Compile(n.Expr)
		if err != nil || n.Expr == "" {
			return nil, fmt.Errorf("the pattern is not a valid regular expression")
		}
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if len(m) > 1 {
				raw = append(raw, m[1])
			} else {
				raw = append(raw, m[0])
			}
		}
	case "lines":
		for _, l := range strings.Split(src, "\n") {
			if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
				raw = append(raw, strings.TrimSpace(l))
			}
		}
	default:
		return nil, fmt.Errorf("unknown split %q", n.Split)
	}
	var keep *regexp.Regexp
	if n.Filter != "" {
		re, err := regexp.Compile(n.Filter)
		if err != nil {
			return nil, fmt.Errorf("the filter is not a valid regular expression")
		}
		keep = re
	}
	limit := n.Max
	if limit <= 0 || limit > MaxLoopItems {
		limit = MaxLoopItems
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		if keep != nil && !keep.MatchString(r) {
			continue
		}
		if n.Unique && seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// listIn takes the list out of JSON input: at path (when set), else the
// only field of an object, else its only list. It returns the list as text
// (a JSON array when isList), or src unchanged when there is nothing to take.
func listIn(src, path, mode string) (string, bool, error) {
	v, err := decodeOrdered(src)
	if err != nil {
		if strings.TrimSpace(path) != "" {
			return "", false, fmt.Errorf("the list should be taken from the field %q, but the input is not JSON", path)
		}
		return src, false, nil
	}
	var picked any
	switch {
	case strings.TrimSpace(path) != "":
		got, err := walkPath(v, strings.TrimSpace(path))
		if err != nil {
			msg := fmt.Sprintf("the input has no field %q", strings.TrimSpace(path))
			if o, ok := v.(*Obj); ok {
				msg += " (its fields: " + strings.Join(o.Keys, ", ") + ")"
			}
			return "", false, errors.New(msg)
		}
		picked = got
	default:
		o, ok := v.(*Obj)
		if !ok {
			return src, false, nil
		}
		var lists []string
		for _, k := range o.Keys {
			if _, isArr := o.Vals[k].([]any); isArr {
				lists = append(lists, k)
			}
		}
		switch {
		case len(o.Keys) == 1:
			picked = o.Vals[o.Keys[0]]
		case len(lists) == 1:
			picked = o.Vals[lists[0]]
		default:
			if mode == "json" || mode == "lines" || mode == "auto" {
				return "", false, fmt.Errorf("the input is a JSON object with the fields %s: choose the field that holds the list in “List from”", strings.Join(o.Keys, ", "))
			}
			return src, false, nil
		}
	}
	switch x := picked.(type) {
	case []any:
		return string(rawJSON(x)), true, nil
	case nil:
		return "", false, nil
	case string:
		return x, false, nil
	case *Obj:
		return string(rawJSON(x)), false, nil
	}
	return textOf(picked), false, nil
}

// ItemResult is what one loop item produced.
type ItemResult struct {
	Item   string
	Output string // the Gather's input for this item
	Error  string // set when the item failed (and the loop went on)
}

// Gather collects the loop's results.
func Gather(n Node, results []ItemResult, d Data) (string, error) {
	sep := strings.ReplaceAll(strings.ReplaceAll(n.Separator, `\n`, "\n"), `\t`, "\t")
	ok := func(r ItemResult) string {
		if r.Error != "" {
			return "ERROR: " + r.Error
		}
		return strings.TrimSpace(r.Output)
	}
	switch firstNonEmptyStr(n.Mode, "concat") {
	case "concat":
		if n.Separator == "" {
			sep = "\n\n"
		}
		parts := make([]string, len(results))
		for i, r := range results {
			parts[i] = ok(r)
		}
		return JoinText(parts, sep), nil
	case "sections":
		var b strings.Builder
		for i, r := range results {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "## %s\n\n%s", itemTitle(r.Item), ok(r))
		}
		return b.String(), nil
	case "json-array":
		arr := make([]any, len(results))
		for i, r := range results {
			if r.Error != "" {
				arr[i] = map[string]any{"item": asJSON(r.Item), "error": r.Error}
			} else {
				arr[i] = asJSON(strings.TrimSpace(r.Output))
			}
		}
		b, err := json.MarshalIndent(arr, "", "  ")
		return string(b), err
	case "json-object":
		obj := map[string]any{}
		for _, r := range results {
			if r.Error != "" {
				obj[itemTitle(r.Item)] = map[string]any{"error": r.Error}
			} else {
				obj[itemTitle(r.Item)] = asJSON(strings.TrimSpace(r.Output))
			}
		}
		b, err := json.MarshalIndent(obj, "", "  ")
		return string(b), err
	case "template":
		if n.Separator == "" {
			sep = "\n"
		}
		parts := make([]string, len(results))
		for i, r := range results {
			di := d
			di.Item, di.ItemIndex, di.ItemCount, di.Input = r.Item, i+1, len(results), ok(r)
			parts[i] = Render(n.Template, di)
		}
		return JoinText(parts, sep), nil
	}
	return "", fmt.Errorf("unknown gather mode %q", n.Mode)
}

// itemTitle is a short name for an item: its text, or for a JSON object
// its name / id / title / symbol field.
func itemTitle(item string) string {
	s := strings.TrimSpace(item)
	if strings.HasPrefix(s, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) == nil {
			for _, k := range []string{"name", "id", "title", "symbol", "key", "path", "file"} {
				if v, ok := m[k]; ok {
					s = fmt.Sprint(v)
					break
				}
			}
		}
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

var (
	mdTableRow = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	mdListItem = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+\S`)
)

// JoinText joins parts with sep. Joined with a blank line (the default),
// Markdown would fall apart where one part ends with a table row or a
// list item and the next starts with one — each row of a table gathered
// from a loop would become its own paragraph — so there the parts are
// joined with a single line break, keeping the table or list whole.
func JoinText(parts []string, sep string) string {
	if strings.Trim(sep, " \t") != "\n\n" && sep != "\n" {
		return strings.Join(parts, sep)
	}
	var b strings.Builder
	prev := ""
	for i, p := range parts {
		if sep == "\n\n" {
			p = strings.TrimRight(p, "\n")
		}
		if i > 0 {
			last, first := lastLine(prev), firstLine(p)
			if (mdTableRow.MatchString(last) && mdTableRow.MatchString(first)) || (mdListItem.MatchString(last) && mdListItem.MatchString(first)) {
				b.WriteString("\n")
			} else {
				b.WriteString(sep)
			}
		}
		b.WriteString(p)
		if strings.TrimSpace(p) != "" {
			prev = p
		}
	}
	return b.String()
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n ")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimLeft(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
