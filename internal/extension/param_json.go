package extension

// Reading a parameter leniently: an AI writing a tool definition often
// gives a default or an example as a number or true/false ("default": 10),
// allowed values as numbers ("enum": [1, 2, 3]), or required as text
// ("required": "true"). They are taken as the text they stand for, instead
// of failing the whole definition ("cannot unmarshal number into Go struct
// field Extension.params.default of type string"). A JSON default (an
// object or a list, for a json parameter) is kept as compact JSON text.

import (
	"bytes"
	"encoding/json"
	"strings"
)

// UnmarshalJSON reads a parameter, taking scalars for its text fields.
func (p *Param) UnmarshalJSON(b []byte) error {
	type plain Param
	var raw struct {
		plain
		Default  json.RawMessage   `json:"default,omitempty"`
		Example  json.RawMessage   `json:"example,omitempty"`
		Enum     []json.RawMessage `json:"enum,omitempty"`
		Required json.RawMessage   `json:"required,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Param(raw.plain)
	p.Default = rawText(raw.Default)
	p.Example = rawText(raw.Example)
	p.Enum = nil
	for _, e := range raw.Enum {
		if s := rawText(e); s != "" {
			p.Enum = append(p.Enum, s)
		}
	}
	switch r := strings.Trim(strings.ToLower(string(bytes.TrimSpace(raw.Required))), `"`); r {
	case "true", "yes", "1":
		p.Required = true
	default:
		p.Required = false
	}
	return nil
}

// rawText is a JSON value as text: a string as it is, a number or
// true/false as written, null as "", an object or list as compact JSON.
func rawText(v json.RawMessage) string {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || string(v) == "null" {
		return ""
	}
	if v[0] == '"' {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	if v[0] == '{' || v[0] == '[' {
		var buf bytes.Buffer
		if json.Compact(&buf, v) == nil {
			return buf.String()
		}
	}
	return string(v)
}
