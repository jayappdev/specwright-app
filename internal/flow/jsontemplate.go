package flow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// JSON → JSON with a template. The template is the JSON to produce, with
// {{…}} placeholders:
//
//	{ "customer": "{{this.customer.name}}",
//	  "orders": { "{{#each this.orders}}": {
//	      "id": "{{this.id}}",
//	      "lines": { "{{#each this.lines}}": {
//	          "sku": "{{this.sku}}", "order": "{{parent.id}}", "n": "{{this_index}}" } } } } }
//
//   - {{this}} is the current JSON ({{this.<path>}} a value in it): the input,
//     or inside an each block the current element.
//   - {{parent.…}} is the level above (parent.parent.… further up), {{root.…}}
//     the whole input, {{this_index}} the element's position (1-based).
//   - An object whose only key is "{{#each <path>}}" becomes a list: its
//     value is the template for each element of that list. Inside a list,
//     the each block's elements are spread into it.
//   - A placeholder that is a whole string value ("{{this.total}}"), or one
//     written without quotes, keeps the value's JSON type; inside a longer
//     string (or an object key) it is inserted as text.
//   - Every other flow variable works too ({{inputs.region}}, {{item}}, …).
//
// With a list path (Node.Expr) the template is applied to each element of
// that list and the result is a JSON array. Objects keep their key order:
// the template's, and the input's for objects copied over.

var (
	wholeVarRE = regexp.MustCompile(`^\{\{\s*([^{}]+?)\s*\}\}$`)
	eachRE     = regexp.MustCompile(`^\{\{\s*#each\s+([^{}]+?)\s*\}\}$`)
)

// Obj is a JSON object that keeps its keys in order.
type Obj struct {
	Keys []string
	Vals map[string]any
}

func (o *Obj) set(k string, v any) {
	if _, ok := o.Vals[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Vals[k] = v
}

// MarshalJSON writes the keys in order.
func (o *Obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(rawJSON(k))
		b.WriteByte(':')
		b.Write(rawJSON(o.Vals[k]))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// scope is one level of the template: the current JSON and the one above.
type scope struct {
	this   any
	index  int
	parent *scope
	root   any
	d      Data
}

func jsonTemplate(in string, n Node, d Data) (string, error) {
	src, err := decodeOrdered(in)
	if err != nil {
		return "", fmt.Errorf("the input is not JSON: %v", err)
	}
	tpl, err := parseJSONTemplate(n.Template)
	if err != nil {
		return "", err
	}
	d.Input = in
	top := &scope{this: src, index: 1, root: src, d: d}
	var out any
	if p := strings.TrimSpace(n.Expr); p == "" {
		if out, err = evalTpl(tpl, top); err != nil {
			return "", err
		}
	} else {
		got, err := walkPath(src, p)
		if err != nil {
			return "", fmt.Errorf("nothing at %s: %v", p, err)
		}
		list, ok := got.([]any)
		if !ok {
			return "", fmt.Errorf("%s is not a list", p)
		}
		arr := make([]any, 0, len(list))
		for i, el := range list {
			v, err := evalTpl(tpl, &scope{this: el, index: i + 1, parent: top, root: src, d: d})
			if err != nil {
				return "", fmt.Errorf("item %d: %v", i+1, err)
			}
			arr = append(arr, v)
		}
		out = arr
	}
	var b bytes.Buffer
	if err := json.Indent(&b, rawJSON(out), "", "  "); err != nil {
		return "", err
	}
	return b.String(), nil
}

// checkJSONTemplate checks that the template is JSON (placeholders aside)
// and that its each blocks are well formed.
func checkJSONTemplate(n Node) error {
	if err := needTemplate(n); err != nil {
		return err
	}
	_, err := parseJSONTemplate(n.Template)
	return err
}

// parseJSONTemplate reads a template: JSON in which placeholders may also
// stand without quotes.
func parseJSONTemplate(tpl string) (any, error) {
	v, err := decodeOrdered(quoteBareVars(tpl))
	if err != nil {
		return nil, fmt.Errorf("the template is not valid JSON: %v", err)
	}
	if err := checkEach(v); err != nil {
		return nil, err
	}
	return v, nil
}

func checkEach(v any) error {
	switch x := v.(type) {
	case *Obj:
		for _, k := range x.Keys {
			if m := eachRE.FindStringSubmatch(k); m != nil && len(x.Keys) > 1 {
				return fmt.Errorf("%q must be the only key of its object: put the other fields inside its template", k)
			}
			if strings.Contains(k, "#each") && !eachRE.MatchString(k) {
				return fmt.Errorf("%q: write an each block as \"{{#each this.<list>}}\"", k)
			}
			if err := checkEach(x.Vals[k]); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := checkEach(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// quoteBareVars turns placeholders written outside strings ({"n": {{x}}})
// into whole-string ones ({"n": "{{x}}"}), which keep the value's type.
func quoteBareVars(tpl string) string {
	var b strings.Builder
	inStr := false
	for i := 0; i < len(tpl); {
		c := tpl[i]
		switch {
		case inStr && c == '\\' && i+1 < len(tpl):
			b.WriteString(tpl[i : i+2])
			i += 2
			continue
		case c == '"':
			inStr = !inStr
		case !inStr && c == '{' && strings.HasPrefix(tpl[i:], "{{"):
			if m := varRE.FindStringIndex(tpl[i:]); m != nil && m[0] == 0 {
				b.Write(rawJSON(tpl[i : i+m[1]]))
				i += m[1]
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// evalTpl fills one template node.
func evalTpl(t any, sc *scope) (any, error) {
	switch x := t.(type) {
	case string:
		if m := wholeVarRE.FindStringSubmatch(x); m != nil {
			return sc.value(m[1]), nil
		}
		return sc.text(x), nil
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if path, body, ok := eachBlock(e); ok { // spread into the list
				items, err := sc.each(path, body)
				if err != nil {
					return nil, err
				}
				out = append(out, items...)
				continue
			}
			v, err := evalTpl(e, sc)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *Obj:
		if path, body, ok := eachBlock(x); ok {
			items, err := sc.each(path, body)
			if err != nil {
				return nil, err
			}
			return items, nil
		}
		o := &Obj{Vals: map[string]any{}}
		for _, k := range x.Keys {
			v, err := evalTpl(x.Vals[k], sc)
			if err != nil {
				return nil, err
			}
			o.set(sc.text(k), v)
		}
		return o, nil
	}
	return t, nil // numbers, true/false, null
}

func eachBlock(t any) (path string, body any, ok bool) {
	o, isObj := t.(*Obj)
	if !isObj || len(o.Keys) != 1 {
		return "", nil, false
	}
	m := eachRE.FindStringSubmatch(o.Keys[0])
	if m == nil {
		return "", nil, false
	}
	return m[1], o.Vals[o.Keys[0]], true
}

// each fills body once per element of the list at path.
func (sc *scope) each(path string, body any) ([]any, error) {
	got := sc.value(path)
	var list []any
	switch x := got.(type) {
	case nil: // nothing there: an empty list
	case []any:
		list = x
	default:
		return nil, fmt.Errorf("{{#each %s}}: that is not a list", path)
	}
	out := make([]any, 0, len(list))
	for i, el := range list {
		v, err := evalTpl(body, &scope{this: el, index: i + 1, parent: sc, root: sc.root, d: sc.d})
		if err != nil {
			return nil, fmt.Errorf("{{#each %s}} item %d: %v", path, i+1, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// value is a placeholder's value: this / parent / root paths from the
// JSON, any other variable as JSON when its text is JSON, else as a string.
func (sc *scope) value(v string) any {
	v = strings.TrimSpace(v)
	up := false
	for {
		if v == "parent" {
			if sc.parent == nil {
				return nil
			}
			return sc.parent.this
		}
		r, ok := strings.CutPrefix(v, "parent.")
		if !ok {
			break
		}
		if sc.parent == nil {
			return nil
		}
		sc, v, up = sc.parent, r, true
	}
	if h, _, _ := strings.Cut(v, "."); up && h != "this" && h != "root" && h != "this_index" {
		v = "this." + v // {{parent.id}} is the parent's id
	}
	head, rest, _ := strings.Cut(v, ".")
	switch head {
	case "this", "root":
		base := sc.this
		if head == "root" {
			base = sc.root
		}
		got, err := walkPath(base, rest)
		if err != nil {
			return nil
		}
		return got
	case "this_index":
		return sc.index
	}
	s, ok := lookup(v, sc.d)
	if !ok {
		return nil
	}
	if t := strings.TrimSpace(s); t != "" {
		if x, err := decodeOrdered(t); err == nil {
			return x
		}
	}
	return s
}

// text fills the placeholders of a string as text.
func (sc *scope) text(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return varRE.ReplaceAllStringFunc(s, func(m string) string {
		return textOf(sc.value(m[2 : len(m)-2]))
	})
}

func rawJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

func textOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return string(rawJSON(v))
}

// decodeOrdered reads one JSON value with objects as *Obj (keys in order)
// and exact numbers.
func decodeOrdered(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(s)))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value")
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("unexpected text after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &Obj{Vals: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			_, err := dec.Token() // }
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token() // ]
			return arr, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	}
	return tok, nil // string, json.Number, bool, nil
}
