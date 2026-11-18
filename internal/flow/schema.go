package flow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Schema describes the shape of JSON data — the subset of JSON Schema the
// designer needs: types, nested objects (properties in order) and lists
// (items), descriptions, required fields and allowed values. Tools declare
// the shape of what they print (Extension.Output) and of JSON parameters;
// the designer works out every step's input and output shape from them
// (Shapes), shows the fields, suggests paths and checks the paths a flow
// uses. Shapes are metadata for the designer: they are not sent to the AI,
// except a compact one where it helps (an AI step's expected reply, a JSON
// parameter the agent fills in).
type Schema struct {
	Type        string   `json:"type,omitempty"` // object | array | string | number | integer | boolean | null; "" = any
	Description string   `json:"description,omitempty"`
	Properties  Props    `json:"properties,omitempty"`
	Required    []string `json:"required,omitempty"`
	Items       *Schema  `json:"items,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"` // date, date-time, … (learned from samples)
}

// Prop is one property of an object, in order.
type Prop struct {
	Name   string
	Schema *Schema
}

// Props are an object's properties, in order; as JSON, an object.
type Props []Prop

// MarshalJSON writes the properties as an object, in order.
func (p Props) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, x := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(rawJSON(x.Name))
		b.WriteByte(':')
		s, err := json.Marshal(x.Schema)
		if err != nil {
			return nil, err
		}
		b.Write(s)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON reads an object of schemas, keeping the order.
func (p *Props) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("properties must be an object")
	}
	*p = nil
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		var s Schema
		if err := dec.Decode(&s); err != nil {
			return err
		}
		*p = append(*p, Prop{Name: fmt.Sprint(kt), Schema: &s})
	}
	_, err := dec.Token()
	return err
}

// Prop returns the property called name.
func (s *Schema) Prop(name string) *Schema {
	if s == nil {
		return nil
	}
	for _, p := range s.Properties {
		if p.Name == name {
			return p.Schema
		}
	}
	return nil
}

// Known reports whether the schema says anything about the data.
func (s *Schema) Known() bool { return s != nil && s.Type != "" }

// ParseSchema reads a schema written as JSON Schema, or — when the text is
// not a schema — learns one from example JSON.
func ParseSchema(text string) (*Schema, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	v, err := decodeOrdered(text)
	if err != nil {
		return nil, fmt.Errorf("not JSON: %v", err)
	}
	if o, ok := v.(*Obj); ok && looksLikeSchema(o) {
		var s Schema
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return nil, err
		}
		return &s, nil
	}
	return InferSchema(v), nil
}

func looksLikeSchema(o *Obj) bool {
	t, ok := o.Vals["type"].(string)
	if !ok {
		return false
	}
	switch t {
	case "object", "array", "string", "number", "integer", "boolean", "null":
	default:
		return false
	}
	for _, k := range o.Keys {
		switch k {
		case "type", "description", "properties", "required", "items", "enum", "format", "$schema", "title", "additionalProperties":
		default:
			return false
		}
	}
	return true
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var dateTimeRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}`)

// InferSchema learns a schema from a JSON value (decoded with decodeOrdered).
func InferSchema(v any) *Schema {
	switch x := v.(type) {
	case nil:
		return &Schema{Type: "null"}
	case bool:
		return &Schema{Type: "boolean"}
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return &Schema{Type: "integer"}
		}
		return &Schema{Type: "number"}
	case float64:
		if x == float64(int64(x)) {
			return &Schema{Type: "integer"}
		}
		return &Schema{Type: "number"}
	case int, int64:
		return &Schema{Type: "integer"}
	case string:
		s := &Schema{Type: "string"}
		switch {
		case dateRE.MatchString(x):
			s.Format = "date"
		case dateTimeRE.MatchString(x):
			s.Format = "date-time"
		}
		return s
	case *Obj:
		s := &Schema{Type: "object"}
		for _, k := range x.Keys {
			s.Properties = append(s.Properties, Prop{k, InferSchema(x.Vals[k])})
			s.Required = append(s.Required, k)
		}
		return s
	case map[string]any:
		s := &Schema{Type: "object"}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s.Properties = append(s.Properties, Prop{k, InferSchema(x[k])})
			s.Required = append(s.Required, k)
		}
		return s
	case []any:
		s := &Schema{Type: "array"}
		for i, e := range x {
			if i == 200 { // enough to learn the shape
				break
			}
			s.Items = mergeSchemas(s.Items, InferSchema(e))
		}
		return s
	}
	return &Schema{}
}

// SchemaOfText learns the shape of a step's output: JSON, or text.
func SchemaOfText(s string) *Schema {
	if t := strings.TrimSpace(s); t != "" && (t[0] == '{' || t[0] == '[') {
		if v, err := decodeOrdered(t); err == nil {
			return InferSchema(v)
		}
	}
	return &Schema{Type: "string"}
}

// mergeSchemas combines two shapes seen for the same data (list elements).
func mergeSchemas(a, b *Schema) *Schema {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.Type == "null":
		return b
	case b.Type == "null":
		return a
	case a.Type != b.Type:
		if (a.Type == "integer" && b.Type == "number") || (a.Type == "number" && b.Type == "integer") {
			return &Schema{Type: "number", Description: firstNonEmptyStr(a.Description, b.Description)}
		}
		return &Schema{Description: firstNonEmptyStr(a.Description, b.Description)}
	}
	out := &Schema{Type: a.Type, Description: firstNonEmptyStr(a.Description, b.Description)}
	if a.Format == b.Format {
		out.Format = a.Format
	}
	switch a.Type {
	case "array":
		out.Items = mergeSchemas(a.Items, b.Items)
	case "object":
		for _, p := range a.Properties {
			out.Properties = append(out.Properties, Prop{p.Name, mergeSchemas(p.Schema, b.Prop(p.Name))})
		}
		for _, p := range b.Properties {
			if a.Prop(p.Name) == nil {
				out.Properties = append(out.Properties, p)
			}
		}
		for _, r := range a.Required { // required: present in both
			if contains(b.Required, r) {
				out.Required = append(out.Required, r)
			}
		}
	}
	return out
}

// Lookup finds the shape at a path (customer.name, items[0].sku,
// items[*].qty, items.qty — a key on a list collects it from every
// element, giving a list). ok is false only when the schema says the path
// cannot exist; an unknown (any) part gives an unknown result.
func (s *Schema) Lookup(path string) (*Schema, bool) {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	if s == nil {
		return nil, true
	}
	if path == "" {
		return s, true
	}
	cur, wild := s, false
	for _, m := range pathTok.FindAllStringSubmatch(path, -1) {
		if cur == nil || !cur.Known() {
			return &Schema{}, true
		}
		switch {
		case m[1] != "": // [n] or [*]
			switch cur.Type {
			case "array":
				if m[1] == "*" {
					wild = true
				}
				cur = cur.Items
			case "object":
				if m[1] != "*" {
					return nil, false
				}
				wild, cur = true, nil
			default:
				return nil, false
			}
		default:
			key := m[2] + m[3]
			if cur.Type == "array" { // a key on a list maps over it
				wild = true
				cur = cur.Items
				if cur == nil || !cur.Known() {
					return &Schema{}, true
				}
			}
			if cur.Type != "object" {
				return nil, false
			}
			p := cur.Prop(key)
			if p == nil {
				if len(cur.Properties) == 0 {
					return &Schema{}, true // an object with fields we do not know
				}
				return nil, false
			}
			cur = p
		}
	}
	if wild {
		return &Schema{Type: "array", Items: cur}, true
	}
	if cur == nil {
		return &Schema{}, true
	}
	return cur, true
}

// SchemaPath is one field of a shape, for the designer.
type SchemaPath struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Depth       int    `json:"depth"`
}

// Paths lists the fields of a shape: customer, customer.name, items,
// items[*].sku, … (at most max, 6 levels deep).
func (s *Schema) Paths(max int) []SchemaPath {
	var out []SchemaPath
	var walk func(s *Schema, prefix string, depth int)
	walk = func(s *Schema, prefix string, depth int) {
		if s == nil || depth > 6 || len(out) >= max {
			return
		}
		switch s.Type {
		case "object":
			for _, p := range s.Properties {
				if len(out) >= max {
					return
				}
				path := p.Name
				if prefix != "" {
					path = prefix + "." + p.Name
				}
				out = append(out, SchemaPath{Path: path, Type: typeName(p.Schema), Description: descOf(p.Schema), Depth: depth})
				walk(p.Schema, path, depth+1)
			}
		case "array":
			if s.Items != nil && (s.Items.Type == "object" || s.Items.Type == "array") {
				walk(s.Items, prefix+"[*]", depth)
			}
		}
	}
	walk(s, "", 0)
	return out
}

func descOf(s *Schema) string {
	if s == nil {
		return ""
	}
	return s.Description
}

// typeName is a short type: string, integer, date, string[], object, any.
func typeName(s *Schema) string {
	switch {
	case s == nil || s.Type == "":
		return "any"
	case s.Type == "array":
		return typeName(s.Items) + "[]"
	case s.Format != "" && s.Type == "string":
		return s.Format
	}
	return s.Type
}

// Fields names the top-level fields of an object (or of a list's
// elements), for messages.
func (s *Schema) Fields() []string {
	if s != nil && s.Type == "array" {
		s = s.Items
	}
	if s == nil {
		return nil
	}
	var out []string
	for _, p := range s.Properties {
		out = append(out, p.Name)
	}
	return out
}

// Compact writes a shape in a short, token-cheap form for the AI:
// {id: string, total: number, items: {sku: string, qty: integer}[]}.
func (s *Schema) Compact() string {
	var b strings.Builder
	var w func(s *Schema, depth int)
	w = func(s *Schema, depth int) {
		switch {
		case s == nil || s.Type == "":
			b.WriteString("any")
		case depth > 8:
			b.WriteString("…")
		case s.Type == "object":
			if len(s.Properties) == 0 {
				b.WriteString("object")
				return
			}
			b.WriteString("{")
			for i, p := range s.Properties {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(p.Name)
				if !contains(s.Required, p.Name) && len(s.Required) > 0 {
					b.WriteString("?")
				}
				b.WriteString(": ")
				w(p.Schema, depth+1)
				if p.Schema != nil && p.Schema.Description != "" && len(p.Schema.Description) <= 60 {
					b.WriteString(" /* " + p.Schema.Description + " */")
				}
			}
			b.WriteString("}")
		case s.Type == "array":
			w(s.Items, depth+1)
			b.WriteString("[]")
		case len(s.Enum) > 0:
			parts := make([]string, len(s.Enum))
			for i, e := range s.Enum {
				parts[i] = string(rawJSON(e))
			}
			b.WriteString(strings.Join(parts, " | "))
		default:
			b.WriteString(typeName(s))
		}
	}
	w(s, 0)
	return b.String()
}

// UnmarshalJSON reads a schema as people and AIs write JSON Schema, not
// only as the designer writes it: a type list ("type": ["string", "null"]),
// anyOf / oneOf with one real type, const (a one-value enum), a boolean
// schema (true: anything), items as a list (its first), required as a
// boolean (left out). Without this, one such schema made the whole flow
// unreadable. Properties keep their order (they are read as they are).
func (s *Schema) UnmarshalJSON(b []byte) error {
	t := strings.TrimSpace(string(b))
	if t == "true" || t == "false" || t == "null" || t == "{}" {
		*s = Schema{}
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	normalizeSchemaRaw(raw)
	*s = Schema{}
	str := func(k string) string {
		var v string
		if json.Unmarshal(raw[k], &v) != nil {
			return ""
		}
		return v
	}
	s.Type, s.Description, s.Format = str("type"), str("description"), str("format")
	if p, ok := raw["properties"]; ok {
		if err := json.Unmarshal(p, &s.Properties); err != nil {
			return err
		}
	}
	if r, ok := raw["required"]; ok {
		_ = json.Unmarshal(r, &s.Required) // a boolean required (old drafts) is left out
	}
	if it, ok := raw["items"]; ok {
		var one Schema
		if err := json.Unmarshal(it, &one); err == nil && (one.Known() || len(one.Properties) > 0 || one.Items != nil) {
			s.Items = &one
		}
	}
	if e, ok := raw["enum"]; ok {
		_ = json.Unmarshal(e, &s.Enum)
	}
	return nil
}

// normalizeSchemaRaw rewrites the variants (type lists, anyOf, const, item
// lists) into the fields Schema has.
func normalizeSchemaRaw(m map[string]json.RawMessage) {
	if t, ok := m["type"]; ok {
		var list []any
		if json.Unmarshal(t, &list) == nil { // ["string", "null"]: the real one
			delete(m, "type")
			for _, x := range list {
				if s, _ := x.(string); s != "" && s != "null" {
					m["type"], _ = json.Marshal(s)
					break
				}
			}
		}
	}
	if _, has := m["type"]; !has {
		for _, k := range []string{"anyOf", "oneOf", "allOf"} {
			var alts []map[string]json.RawMessage
			if json.Unmarshal(m[k], &alts) != nil {
				continue
			}
			for _, am := range alts {
				normalizeSchemaRaw(am)
				var ty string
				if json.Unmarshal(am["type"], &ty) == nil && ty != "" && ty != "null" {
					for ak, av := range am {
						if _, set := m[ak]; !set {
							m[ak] = av
						}
					}
					break
				}
			}
			if _, has := m["type"]; has {
				break
			}
		}
	}
	if c, ok := m["const"]; ok {
		m["enum"] = json.RawMessage("[" + string(c) + "]")
	}
	if it, ok := m["items"]; ok { // tuple form: its first item
		var list []json.RawMessage
		if json.Unmarshal(it, &list) == nil {
			if len(list) > 0 {
				m["items"] = list[0]
			} else {
				delete(m, "items")
			}
		}
	}
}
