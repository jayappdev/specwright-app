package flow

import (
	"encoding/json"
	"strings"
)

// Shapes: what data every step of a flow gets and gives, worked out before
// the flow runs, so the designer can show the fields, suggest paths and
// check them. A shape comes from (in order of trust):
//
//   - declared: a tool's output shape, an AI step's expected reply, the
//     flow's inputs;
//   - derived: worked out from the steps before, for steps whose result
//     follows from their input (Filter, Aggregate, Decision, Transform's
//     JSON operations, Merge, Loop, Gather);
//   - sample: learned from sample output (the designer's sample data or the
//     flow's last run);
//   - unknown: nothing to go on yet (a tool without a declared shape, an AI
//     step without an expected reply).

// StepShape is what one step gets and gives.
type StepShape struct {
	In     *Schema `json:"in,omitempty"`   // what reaches it from the steps connected into it
	Out    *Schema `json:"out,omitempty"`  // what it gives the steps after it
	Item   *Schema `json:"item,omitempty"` // Loop: one item ({{item}}, {{<loop>.item}})
	Rows   *Schema `json:"rows,omitempty"` // Filter, Aggregate: one row
	Source string  `json:"source"`         // declared | derived | sample | unknown
}

// ShapeHints are the shapes the flow cannot work out itself.
type ShapeHints struct {
	Tools   map[string]*Schema                       // tool name → the shape of its output
	ToolsBy map[string]map[string]map[string]*Schema // tool name → argument → value → shape
	Samples map[string]string                        // step id → a sample of its output
}

// ToolOutput is the shape a tool step gives: the one its arguments select
// (fetch_webpage with format=json), else the tool's declared shape.
func ToolOutput(out *Schema, by map[string]map[string]*Schema, args map[string]string) *Schema {
	for p, m := range by {
		if s := m[strings.TrimSpace(args[p])]; s != nil {
			return s
		}
	}
	return out
}

// Shapes works out every step's shapes, in run order.
func (f *Flow) Shapes(h ShapeHints) map[string]StepShape {
	out := map[string]StepShape{}
	order, err := f.Order()
	if err != nil {
		return out
	}
	for _, id := range order {
		n := f.Node(id)
		parents := f.Parents(id)
		var in *Schema
		switch len(parents) {
		case 0:
		case 1:
			in = out[parents[0]].Out
		default:
			in = &Schema{Type: "string", Description: "the connected steps' outputs, one after another"}
		}
		if strings.TrimSpace(n.Source) != "" && (n.Type == "transform" || n.Type == "loop" || n.Type == "decision" || n.Type == "filter" || n.Type == "aggregate") {
			in = templateShape(n.Source, out)
		}
		s := StepShape{In: in, Source: "derived"}
		sample := func() {
			if t, ok := h.Samples[id]; ok && strings.TrimSpace(t) != "" {
				s.Out, s.Source = SchemaOfText(t), "sample"
			} else if s.Out == nil {
				s.Source = "unknown"
			}
		}
		switch n.Type {
		case "start":
			o := &Schema{Type: "object", Description: "the flow's inputs (each is text)"}
			for _, i := range f.Inputs {
				p := &Schema{Type: "string", Description: firstNonEmptyStr(i.Label, i.Name)}
				if len(i.Options) > 0 && i.Type == "choice" {
					for _, v := range i.Options {
						p.Enum = append(p.Enum, v)
					}
				}
				o.Properties = append(o.Properties, Prop{i.Name, p})
				if i.Required {
					o.Required = append(o.Required, i.Name)
				}
			}
			s.Out, s.Source = o, "declared"
		case "tool":
			if t := ToolOutput(h.Tools[n.Tool], h.ToolsBy[n.Tool], n.Args); t != nil {
				s.Out, s.Source = t, "declared"
			} else {
				sample()
			}
		case "ai":
			if n.OutputSchema != nil {
				s.Out, s.Source = n.OutputSchema, "declared"
			} else {
				sample()
			}
		case "merge":
			switch n.Mode {
			case "json-object":
				o := &Schema{Type: "object"}
				for _, p := range parents {
					o.Properties = append(o.Properties, Prop{label(*f.Node(p)), orAny(out[p].Out)})
				}
				s.Out = o
			case "json-array":
				a := &Schema{Type: "array"}
				for _, p := range parents {
					a.Items = mergeSchemas(a.Items, orAny(out[p].Out))
				}
				s.Out = a
			case "template":
				s.Out = templateShape(n.Template, out)
			default:
				s.Out = &Schema{Type: "string"}
			}
		case "transform":
			s.Out = transformShape(n, in, out)
			if s.Out == nil {
				sample()
			}
		case "loop":
			var item *Schema
			list := in
			if n.Split != "regex" && list != nil && list.Type == "object" { // the list inside a JSON object
				if f := strings.TrimSpace(n.Field); f != "" {
					list, _ = list.Lookup(f)
				} else if len(list.Properties) == 1 {
					list = list.Properties[0].Schema
				} else {
					var arrs []*Schema
					for _, p := range list.Properties {
						if p.Schema != nil && p.Schema.Type == "array" {
							arrs = append(arrs, p.Schema)
						}
					}
					if len(arrs) == 1 {
						list = arrs[0]
					}
				}
			}
			in := list
			switch {
			case n.Split == "lines" || n.Split == "regex":
				item = &Schema{Type: "string"}
			case in != nil && in.Type == "array":
				item = in.Items
			case in != nil && in.Type == "object" && len(in.Properties) == 1 && in.Properties[0].Schema.Type == "array":
				item = in.Properties[0].Schema.Items
			case in != nil && in.Type == "string":
				item = &Schema{Type: "string", Description: "a line"}
			}
			s.Item, s.Out = item, item
		case "gather":
			var body *Schema
			for _, p := range parents {
				body = mergeSchemas(body, orAny(out[p].Out))
			}
			switch n.Mode {
			case "json-array":
				s.Out = &Schema{Type: "array", Items: body}
			case "json-object":
				s.Out = &Schema{Type: "object", Description: "one key per item"}
			default:
				s.Out = &Schema{Type: "string"}
			}
		case "decision":
			s.Out = in
		case "filter":
			s.Rows = rowShape(in, n.Split)
			s.Out = &Schema{Type: "array", Items: s.Rows}
			if n.Split == "lines" || (s.Rows != nil && s.Rows.Type == "string" && in != nil && in.Type == "string") {
				s.Out = &Schema{Type: "string", Description: "one row per line"}
			}
		case "aggregate":
			s.Rows = rowShape(in, n.Split)
			s.Out = &Schema{Type: "array", Items: aggregateRowShape(n, s.Rows)}
		case "output":
			s.Out = in
		}
		if s.Out == nil && s.Source == "derived" {
			s.Source = "unknown"
		}
		out[id] = s
	}
	return out
}

func orAny(s *Schema) *Schema {
	if s == nil {
		return &Schema{}
	}
	return s
}

// templateShape: a template that is one step's whole output
// ({{x.buffer_output}}, {{steps.x}}, {{input}}) has that step's shape;
// anything else is text.
func templateShape(tpl string, out map[string]StepShape) *Schema {
	t := strings.TrimSpace(tpl)
	if m := wholeVarRE.FindStringSubmatch(t); m != nil {
		v := strings.TrimSpace(m[1])
		head, rest, _ := strings.Cut(v, ".")
		switch {
		case head == "steps":
			id, path, _ := strings.Cut(rest, ".")
			if s, ok := out[id]; ok {
				if p, ok := strings.CutPrefix(path, "json"); ok {
					r, _ := s.Out.Lookup(strings.TrimPrefix(p, "."))
					return r
				}
				if path == "" {
					return s.Out
				}
			}
		case rest == "buffer_output":
			if s, ok := out[head]; ok {
				return s.Out
			}
		case strings.HasPrefix(rest, "buffer_output.json"):
			if s, ok := out[head]; ok {
				r, _ := s.Out.Lookup(strings.TrimPrefix(strings.TrimPrefix(rest, "buffer_output.json"), "."))
				return r
			}
		case rest == "item":
			if s, ok := out[head]; ok {
				return s.Item
			}
		}
	}
	if t == "" {
		return nil
	}
	return &Schema{Type: "string"}
}

// rowShape is one row of a Filter's or Aggregate's input.
func rowShape(in *Schema, format string) *Schema {
	switch {
	case format == "lines":
		return &Schema{Type: "string"}
	case in == nil:
		return nil
	case in.Type == "array":
		return in.Items
	case in.Type == "object":
		var lists []*Schema
		for _, p := range in.Properties {
			if p.Schema != nil && p.Schema.Type == "array" {
				lists = append(lists, p.Schema)
			}
		}
		if len(lists) == 1 {
			return lists[0].Items
		}
		return in
	case in.Type == "string" && format != "json" && format != "csv":
		return &Schema{Type: "string", Description: "a line (or a CSV row)"}
	}
	return nil
}

func aggregateRowShape(n *Node, row *Schema) *Schema {
	o := &Schema{Type: "object"}
	field := func(f string) *Schema {
		if row == nil {
			return &Schema{}
		}
		s, ok := row.Lookup(f)
		if !ok || s == nil {
			return &Schema{}
		}
		return s
	}
	if n.Mode == "window" {
		if row != nil && row.Type == "object" {
			o.Properties = append(o.Properties, row.Properties...)
		} else {
			o.Properties = append(o.Properties, Prop{"value", orAny(row)})
		}
	} else {
		for _, g := range n.GroupBy {
			o.Properties = append(o.Properties, Prop{g, field(g)})
		}
	}
	for _, m := range n.Measures {
		var t *Schema
		switch m.Fn {
		case "count", "count_distinct", "row_number", "rank", "dense_rank", "running_count", "ntile":
			t = &Schema{Type: "integer"}
		case "sum", "avg", "median", "running_sum", "running_avg", "pct_of_total":
			t = &Schema{Type: "number"}
		case "min", "max", "first", "last", "lag", "lead":
			t = elem(field(m.Field))
		case "list", "distinct":
			t = &Schema{Type: "array", Items: elem(field(m.Field))}
		case "concat":
			t = &Schema{Type: "string"}
		default:
			t = &Schema{}
		}
		o.Properties = append(o.Properties, Prop{measureName(m), t})
	}
	return o
}

// elem: a list path (items[*].qty) gives the elements' shape.
func elem(s *Schema) *Schema {
	if s != nil && s.Type == "array" {
		return orAny(s.Items)
	}
	return s
}

func transformShape(n *Node, in *Schema, out map[string]StepShape) *Schema {
	switch n.Op {
	case "json-path":
		if in == nil || !in.Known() {
			return nil
		}
		s, ok := in.Lookup(n.Expr)
		if !ok {
			return nil
		}
		return s
	case "json-pick":
		if in == nil {
			return nil
		}
		pick := func(o *Schema) *Schema {
			if o == nil || o.Type != "object" {
				return o
			}
			r := &Schema{Type: "object"}
			for _, k := range splitList(n.Expr) {
				if p := o.Prop(k); p != nil {
					r.Properties = append(r.Properties, Prop{k, p})
				}
			}
			return r
		}
		if in.Type == "array" {
			return &Schema{Type: "array", Items: pick(in.Items)}
		}
		return pick(in)
	case "json-template":
		t, err := parseJSONTemplate(n.Template)
		if err != nil {
			return nil
		}
		if p := strings.TrimSpace(n.Expr); p != "" {
			list, _ := in.Lookup(p)
			var item *Schema
			if list != nil && list.Type == "array" {
				item = list.Items
			}
			return &Schema{Type: "array", Items: tplShape(t, &shapeScope{this: item, root: in})}
		}
		return tplShape(t, &shapeScope{this: in, root: in})
	case "json-stringify", "json-parse":
		f := strings.TrimSpace(n.Expr)
		repl := &Schema{Type: "string", Description: "JSON as a string"}
		if n.Op == "json-parse" {
			if f == "" {
				return nil // what the string holds: learned from a run or sample data
			}
			repl = &Schema{Description: "parsed from a JSON string"}
		}
		if f == "" {
			return repl
		}
		return schemaWith(in, f, repl)
	case "split-lines":
		return &Schema{Type: "array", Items: &Schema{Type: "string"}}
	case "csv-to-json":
		return &Schema{Type: "array", Items: &Schema{Type: "object"}}
	case "json-pretty", "json-minify":
		return in
	case "template":
		return templateShape(n.Template, out)
	case "ai", "command":
		return nil
	}
	return &Schema{Type: "string"}
}

type shapeScope struct {
	this, root *Schema
	parent     *shapeScope
}

func (sc *shapeScope) value(v string) *Schema {
	v = strings.TrimSpace(v)
	up := false
	for {
		if v == "parent" {
			if sc.parent == nil {
				return &Schema{}
			}
			return orAny(sc.parent.this)
		}
		r, ok := strings.CutPrefix(v, "parent.")
		if !ok {
			break
		}
		if sc.parent == nil {
			return &Schema{}
		}
		sc, v, up = sc.parent, r, true
	}
	head, rest, _ := strings.Cut(v, ".")
	if up && head != "this" && head != "root" && head != "this_index" {
		head, rest = "this", v
	}
	switch head {
	case "this", "root":
		base := sc.this
		if head == "root" {
			base = sc.root
		}
		if base == nil {
			return &Schema{}
		}
		s, _ := base.Lookup(rest)
		return orAny(s)
	case "this_index":
		return &Schema{Type: "integer"}
	}
	return &Schema{}
}

func tplShape(t any, sc *shapeScope) *Schema {
	switch x := t.(type) {
	case string:
		if m := wholeVarRE.FindStringSubmatch(x); m != nil {
			return sc.value(m[1])
		}
		return &Schema{Type: "string"}
	case []any:
		a := &Schema{Type: "array"}
		for _, e := range x {
			if path, body, ok := eachBlock(e); ok {
				a.Items = mergeSchemas(a.Items, sc.eachShape(path, body))
				continue
			}
			a.Items = mergeSchemas(a.Items, tplShape(e, sc))
		}
		return a
	case *Obj:
		if path, body, ok := eachBlock(x); ok {
			return &Schema{Type: "array", Items: sc.eachShape(path, body)}
		}
		o := &Schema{Type: "object"}
		for _, k := range x.Keys {
			if strings.Contains(k, "{{") { // keys from the data: an open object
				return &Schema{Type: "object"}
			}
			o.Properties = append(o.Properties, Prop{k, tplShape(x.Vals[k], sc)})
		}
		return o
	}
	return InferSchema(t)
}

func (sc *shapeScope) eachShape(path string, body any) *Schema {
	list := sc.value(path)
	var item *Schema
	if list != nil && list.Type == "array" {
		item = list.Items
	}
	return tplShape(body, &shapeScope{this: item, root: sc.root, parent: sc})
}

// checkPaths warns about fields a flow uses that the shapes say are not
// there: a condition's field, a group or sort field, a JSON path, or
// {{<step>.buffer_output.json.<path>}} in a template. Only definite misses
// are reported: where a shape is unknown, any path is accepted.
func (f *Flow) checkPaths(sh map[string]StepShape) []Issue {
	var is []Issue
	li, _ := f.Loops()
	warn := func(n Node, what, path string, s *Schema, of string) {
		if strings.Contains(path, "{{") || s == nil || !s.Known() {
			return
		}
		p := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "this"), ".")
		if p == "" {
			return
		}
		if _, ok := s.Lookup(p); ok {
			return
		}
		msg := label(n) + ": " + what + " " + quote(p) + " is not in " + of + "."
		if fs := s.Fields(); len(fs) > 0 {
			if len(fs) > 12 {
				fs = append(fs[:12], "…")
			}
			msg += " Its fields: " + strings.Join(fs, ", ") + "."
		}
		is = append(is, Issue{Node: n.ID, Level: "warning", Message: msg})
	}
	var condFields func(c *Cond) []string
	condFields = func(c *Cond) []string {
		if c == nil {
			return nil
		}
		var out []string
		for _, r := range c.Rules {
			if r.Group != nil {
				out = append(out, condFields(r.Group)...)
			} else {
				out = append(out, r.Field)
			}
		}
		return out
	}
	for _, n := range f.Nodes {
		s := sh[n.ID]
		from := "the data it gets"
		if ps := f.Parents(n.ID); len(ps) == 1 {
			from = "what " + label(*f.Node(ps[0])) + " gives"
		}
		switch n.Type {
		case "decision":
			for _, b := range n.Branches {
				for _, fl := range condFields(b.When) {
					warn(n, "the field", fl, s.In, from)
				}
			}
		case "filter":
			for _, fl := range condFields(n.Cond) {
				warn(n, "the field", fl, s.Rows, "its rows ("+from+")")
			}
			for _, o := range n.OrderBy {
				warn(n, "the sort field", strings.TrimLeft(o, "+-"), s.Rows, "its rows")
			}
		case "aggregate":
			for _, g := range n.GroupBy {
				warn(n, "the group field", g, s.Rows, "its rows ("+from+")")
			}
			for _, m := range n.Measures {
				warn(n, "the field", m.Field, s.Rows, "its rows")
			}
			for _, o := range n.OrderBy {
				target := s.Rows
				if n.Mode != "window" && s.Out != nil {
					target = s.Out.Items
				}
				warn(n, "the sort field", strings.TrimLeft(o, "+-"), target, "its result")
			}
		case "loop":
			if strings.TrimSpace(n.Field) != "" {
				warn(n, "the list field", n.Field, s.In, from)
			}
		case "transform":
			if n.Op == "json-path" {
				warn(n, "the path", n.Expr, s.In, from)
			}
			if n.Op == "json-template" && strings.TrimSpace(n.Expr) != "" {
				warn(n, "the list path", n.Expr, s.In, from)
			}
		}
		// {{<step>.buffer_output.json.<path>}} and friends in templates
		texts := []string{n.Prompt, n.Template, n.Source, n.Instruction}
		for _, v := range n.Args {
			texts = append(texts, v)
		}
		for _, a := range n.Actions {
			texts = append(texts, a.Path)
			for _, v := range a.Args {
				texts = append(texts, v)
			}
		}
		for _, t := range texts {
			for _, v := range Vars(t) {
				head, rest, _ := strings.Cut(v, ".")
				var id, path string
				var base *Schema
				switch {
				case head == "steps":
					var p string
					id, p, _ = strings.Cut(rest, ".")
					path, _ = strings.CutPrefix(p, "json")
					if !strings.HasPrefix(p, "json") {
						continue
					}
					base = sh[id].Out
				case strings.HasPrefix(rest, "buffer_output.json"):
					id, path = head, strings.TrimPrefix(rest, "buffer_output.json")
					base = sh[id].Out
				case strings.HasPrefix(rest, "item.json"):
					id, path = head, strings.TrimPrefix(rest, "item.json")
					base = sh[id].Item
				case head == "item" && strings.HasPrefix(rest, "json"):
					st := li.Stack[n.ID]
					if len(st) == 0 {
						continue
					}
					id, path = st[len(st)-1], strings.TrimPrefix(rest, "json")
					base = sh[id].Item
				default:
					continue
				}
				if pn := f.Node(id); pn != nil {
					warn(n, "{{"+v+"}}: the path", strings.TrimPrefix(path, "."), base, "what "+label(*pn)+" gives")
				}
			}
		}
	}
	return is
}

func quote(s string) string { return `"` + s + `"` }

// Layout places the steps left to right by how far they are from the
// Inputs step, one column per level (for flows made by the AI designer).
func (f *Flow) Layout() {
	order, err := f.Order()
	if err != nil {
		return
	}
	depth := map[string]int{}
	for _, id := range order {
		for _, p := range f.Parents(id) {
			if depth[p]+1 > depth[id] {
				depth[id] = depth[p] + 1
			}
		}
	}
	row := map[int]int{}
	for _, id := range order {
		n := f.Node(id)
		d := depth[id]
		n.X, n.Y = float64(40+d*260), float64(80+row[d]*140)
		row[d]++
	}
}

// schemaWith is a copy of s with the shape at path replaced (a key on a
// list, or [*], for every element).
func schemaWith(s *Schema, path string, repl *Schema) *Schema {
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	var c Schema
	_ = json.Unmarshal(b, &c)
	toks := pathTok.FindAllStringSubmatch(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), "."), -1)
	cur := &c
	for i, m := range toks {
		last := i == len(toks)-1
		if cur.Type == "array" && cur.Items != nil {
			if m[1] != "" { // [n] or [*]
				if last {
					cur.Items = repl
					return &c
				}
				cur = cur.Items
				continue
			}
			cur = cur.Items
		}
		key := m[2] + m[3]
		if cur.Type != "object" || key == "" {
			return &c
		}
		found := false
		for k := range cur.Properties {
			if cur.Properties[k].Name == key {
				found = true
				if last {
					cur.Properties[k].Schema = repl
					return &c
				}
				cur = cur.Properties[k].Schema
				break
			}
		}
		if !found || cur == nil {
			return &c
		}
	}
	return &c
}
