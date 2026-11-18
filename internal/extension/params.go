package extension

// Tool metadata: what a tool takes and gives, as precisely as it can be
// known, so the test form shows the right field for every argument, the
// arguments are checked before the tool runs, and its output is checked
// against what it promises.
//
// Parameters come from the tool's definition (a Copilot extension's JSON
// Schema, an imported or generated tool's params) and are refined without
// AI by InferParams: placeholders in the command that are not declared,
// and types and formats suggested by names and descriptions. Refining with
// AI (core) reads the source and proposes the rest.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"codegen/internal/flow"
)

// ParamTypes and ParamFormats are the types and formats a parameter can have.
var (
	ParamTypes   = []string{"string", "integer", "number", "boolean", "json", "object", "array"}
	ParamFormats = []string{"", "path", "file", "directory", "url", "date", "date-time", "multiline", "email", "password"}
)

// --- JSON Schema (Copilot extensions, MCP-style tools) ---------------------------

// jsonSchemaParams reads a JSON Schema's top-level properties as parameters,
// keeping types, allowed values, formats, defaults, examples and the shape
// of object and array parameters.
func jsonSchemaParams(s map[string]any) []Param {
	props, _ := s["properties"].(map[string]any)
	req := map[string]bool{}
	if l, ok := s["required"].([]any); ok {
		for _, v := range l {
			if n, ok := v.(string); ok {
				req[n] = true
			}
		}
	}
	out := []Param{}
	for name, v := range props {
		m, _ := v.(map[string]any)
		out = append(out, jsonSchemaParam(name, m, req[name]))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func jsonSchemaParam(name string, m map[string]any, required bool) Param {
	p := Param{Name: name, Type: "string", Required: required}
	if m == nil {
		return p
	}
	m = normalizeSchema(m)
	if t, ok := m["type"].(string); ok && t != "" {
		p.Type = t
	}
	if d, ok := m["description"].(string); ok {
		p.Desc = d
	}
	for _, e := range listOf(m["enum"]) {
		p.Enum = append(p.Enum, scalarText(e))
	}
	if f, ok := m["format"].(string); ok {
		p.Format = schemaFormat(f)
	}
	if d, ok := m["default"]; ok && d != nil {
		p.Default = scalarText(d)
	}
	if ex := listOf(m["examples"]); len(ex) > 0 {
		p.Example = scalarText(ex[0])
	} else if ex, ok := m["example"]; ok {
		p.Example = scalarText(ex)
	}
	if p.Type == "object" || p.Type == "array" {
		if b, err := json.Marshal(m); err == nil {
			var sc flow.Schema
			if json.Unmarshal(b, &sc) == nil && sc.Known() {
				p.Schema = &sc
			}
		}
	}
	return p
}

// normalizeSchema reads the common variants: a type list ("string",
// "null"), anyOf/oneOf of one real type, const as a one-value enum.
func normalizeSchema(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	if l := listOf(m["type"]); len(l) > 0 {
		for _, t := range l {
			if s, _ := t.(string); s != "" && s != "null" {
				out["type"] = s
				break
			}
		}
	}
	for _, k := range []string{"anyOf", "oneOf"} {
		if _, has := out["type"]; has {
			break
		}
		for _, alt := range listOf(m[k]) {
			a, _ := alt.(map[string]any)
			if t, _ := a["type"].(string); t != "" && t != "null" {
				for ak, av := range normalizeSchema(a) {
					if _, set := out[ak]; !set {
						out[ak] = av
					}
				}
				break
			}
		}
	}
	if c, ok := m["const"]; ok {
		out["enum"] = []any{c}
	}
	// nested properties and items too
	if props, ok := out["properties"].(map[string]any); ok {
		np := map[string]any{}
		for k, v := range props {
			if pm, ok := v.(map[string]any); ok {
				np[k] = normalizeSchema(pm)
			} else {
				np[k] = v
			}
		}
		out["properties"] = np
	}
	if it, ok := out["items"].(map[string]any); ok {
		out["items"] = normalizeSchema(it)
	}
	return out
}

func listOf(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case string:
		return []any{x}
	}
	return nil
}

func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64, bool, int:
		return fmt.Sprint(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// schemaFormat maps JSON Schema formats to ParamFormats.
func schemaFormat(f string) string {
	switch strings.ToLower(f) {
	case "date":
		return "date"
	case "date-time", "datetime":
		return "date-time"
	case "uri", "url", "uri-reference", "iri":
		return "url"
	case "email", "idn-email":
		return "email"
	case "password":
		return "password"
	case "path", "file", "file-path", "filepath":
		return "path"
	case "directory", "dir":
		return "directory"
	case "textarea", "multiline":
		return "multiline"
	}
	return ""
}

// --- inference without AI ---------------------------------------------------------

// enumInDesc finds a fixed set of values in a description: "one of: a, b, c",
// "(a|b|c)", "md, html or json".
var (
	enumOneOfRE = regexp.MustCompile(`(?i)\b(?:one of|either|choose|allowed(?: values)?|values?)\s*[:=]?\s*([A-Za-z0-9_.\-]+(?:\s*(?:,|\||/|\bor\b)\s*[A-Za-z0-9_.\-]+)+)`)
	enumBarRE   = regexp.MustCompile(`[(\[]\s*([A-Za-z0-9_.\-]+(?:\s*\|\s*[A-Za-z0-9_.\-]+)+)\s*[)\]]`)
	defaultRE   = regexp.MustCompile(`(?i)\bdefault(?:s)?(?: is| to|:)?\s*[:=]?\s*["'\x60]?([A-Za-z0-9_./:\-]+)`)
)

// ParamChange is one change InferParams (or AI refining) makes.
type ParamChange struct {
	Param string `json:"param"`
	What  string `json:"what"`
}

// InferParams refines a tool's parameters without AI: parameters for
// command placeholders that are not declared, and types, formats, allowed
// values and defaults suggested by names and descriptions. What the tool
// already declares precisely (a type other than string, a format, values)
// is kept.
func InferParams(e Extension) ([]Param, []ParamChange) {
	ps := append([]Param(nil), e.Params...)
	var ch []ParamChange
	have := map[string]bool{}
	for _, p := range ps {
		have[p.Name] = true
	}
	for _, m := range placeholderRE.FindAllStringSubmatch(e.Command, -1) {
		n := m[1]
		if have[n] || n == "input_file" || n == "output_file" {
			continue
		}
		have[n] = true
		// optional: calls that leave it out worked before (the word is dropped)
		ps = append(ps, Param{Name: n, Type: "string", Desc: "Used in the command as {{" + n + "}}"})
		ch = append(ch, ParamChange{n, "added: the command uses {{" + n + "}}"})
	}
	for i := range ps {
		p := &ps[i]
		if p.Type == "" {
			p.Type = "string"
		}
		before := *p
		inferOne(p)
		if d := describeChange(before, *p); d != "" {
			ch = append(ch, ParamChange{p.Name, d})
		}
	}
	return ps, ch
}

var (
	intNameRE    = regexp.MustCompile(`(?i)(^|_|-)(port|limit|max|min|count|size|top|depth|retries|retry|timeout|seconds|minutes|hours|days|lines|page|per_page|offset|n|num|number|year|width|height)$`)
	numberNameRE = regexp.MustCompile(`(?i)(^|_|-)(ratio|rate|threshold|percent|percentage|score|amount|price)$`)
	boolNameRE   = regexp.MustCompile(`(?i)^(is|has|enable|enabled|disable|disabled|dry|dry_run|dryrun|verbose|force|recursive|include|exclude|allow|use|skip|strict|debug|quiet|overwrite|fix|all|no)(_|-|$)|(_|-)(only|enabled|flag)$`)
	pathNameRE   = regexp.MustCompile(`(?i)(^|_|-)(file|path|filepath|filename|src|source_file|input_file|output|output_path|dest|destination|config|manifest|spec)$`)
	dirNameRE    = regexp.MustCompile(`(?i)(^|_|-)(dir|directory|folder|root|workspace|cwd)$`)
	urlNameRE    = regexp.MustCompile(`(?i)(^|_|-)(url|uri|endpoint|link|href|webhook)$`)
	dateNameRE   = regexp.MustCompile(`(?i)(^|_|-)(date|day|since|until|from_date|to_date|start_date|end_date|due)$`)
	timeNameRE   = regexp.MustCompile(`(?i)(^|_|-)(time|datetime|timestamp|at|when|start|end|deadline)$`)
	textNameRE   = regexp.MustCompile(`(?i)(^|_|-)(query|sql|body|content|text|template|markdown|md|html|script|code|prompt|message|description|draft|diagram|json|yaml|payload|notes)$`)
	emailNameRE  = regexp.MustCompile(`(?i)(^|_|-)(email|mail)$`)
	secretNameRE = regexp.MustCompile(`(?i)(^|_|-)(password|passwd|pwd|secret|token|api_key|apikey|key)$`)
)

func inferOne(p *Param) {
	desc := strings.ToLower(p.Desc)
	if len(p.Enum) == 0 && p.Type == "string" {
		if m := enumBarRE.FindStringSubmatch(p.Desc); m != nil {
			p.Enum = splitEnum(m[1])
		} else if m := enumOneOfRE.FindStringSubmatch(p.Desc); m != nil {
			p.Enum = splitEnum(m[1])
		}
	}
	if p.Default == "" {
		if m := defaultRE.FindStringSubmatch(p.Desc); m != nil {
			p.Default = strings.TrimRight(m[1], ".,;)")
		}
	}
	if p.Type != "string" || len(p.Enum) > 0 || p.Format != "" {
		return // declared precisely already
	}
	n := p.Name
	switch {
	case boolNameRE.MatchString(n) || strings.Contains(desc, "true or false") || strings.Contains(desc, "yes or no"):
		p.Type = "boolean"
	case intNameRE.MatchString(n) || strings.Contains(desc, "number of") || strings.Contains(desc, "port number"):
		p.Type = "integer"
	case numberNameRE.MatchString(n):
		p.Type = "number"
	case secretNameRE.MatchString(n):
		p.Format = "password"
	case dirNameRE.MatchString(n) || strings.Contains(desc, "folder") || strings.Contains(desc, "directory"):
		p.Format = "directory"
	case urlNameRE.MatchString(n) || strings.Contains(desc, "url"):
		p.Format = "url"
	case emailNameRE.MatchString(n):
		p.Format = "email"
	case timeNameRE.MatchString(n) || strings.Contains(desc, "timestamp") || strings.Contains(desc, "date and time"):
		p.Format = "date-time"
	case dateNameRE.MatchString(n) || strings.Contains(desc, "yyyy-mm-dd") || strings.Contains(desc, "a date"):
		p.Format = "date"
	case pathNameRE.MatchString(n) || strings.Contains(desc, "file path") || strings.Contains(desc, "path to"):
		p.Format = "path"
	case textNameRE.MatchString(n) || strings.Contains(desc, "markdown") || strings.Contains(desc, "multi-line") || strings.Contains(desc, "multiline"):
		p.Format = "multiline"
	}
	if p.Type == "string" && (strings.Contains(desc, "json object") || strings.Contains(desc, "json array")) {
		p.Type = "json"
	}
}

func splitEnum(s string) []string {
	var out []string
	for _, x := range regexp.MustCompile(`\s*(?:,|\||/|\bor\b)\s*`).Split(s, -1) {
		if x = strings.Trim(strings.TrimSpace(x), `"'.`); x != "" {
			out = append(out, x)
		}
	}
	if len(out) < 2 || len(out) > 20 {
		return nil
	}
	return out
}

func describeChange(a, b Param) string {
	var parts []string
	if a.Type != b.Type {
		parts = append(parts, "type "+a.Type+" → "+b.Type)
	}
	if a.Format != b.Format {
		parts = append(parts, "format "+firstNonBlank(b.Format, "none"))
	}
	if strings.Join(a.Enum, "|") != strings.Join(b.Enum, "|") {
		parts = append(parts, "values: "+strings.Join(b.Enum, ", "))
	}
	if a.Default != b.Default {
		parts = append(parts, "default "+b.Default)
	}
	return strings.Join(parts, "; ")
}

// --- checking arguments and output ------------------------------------------------

// CheckArgs checks args against e's parameters and converts them to their
// types: numbers and booleans from text, JSON parameters from text,
// defaults for the arguments left out. It returns the arguments to run with
// and the problems (none: they are fine).
func CheckArgs(e Extension, args map[string]any) (map[string]any, []string) {
	out := map[string]any{}
	for k, v := range args {
		out[k] = v
	}
	var probs []string
	for _, p := range e.Params {
		v, ok := out[p.Name]
		if s, isStr := v.(string); !ok || v == nil || (isStr && strings.TrimSpace(s) == "") {
			if p.Default != "" {
				out[p.Name] = p.Default
				v, ok = p.Default, true
			} else {
				if p.Required {
					probs = append(probs, fmt.Sprintf("%s is required", p.Name))
				}
				continue
			}
		}
		cv, err := convertArg(p, v)
		if err != nil {
			probs = append(probs, fmt.Sprintf("%s: %v", p.Name, err))
			continue
		}
		out[p.Name] = cv
	}
	return out, probs
}

func convertArg(p Param, v any) (any, error) {
	s, isStr := v.(string)
	if isStr {
		s = strings.TrimSpace(s)
	}
	switch p.Type {
	case "integer":
		switch x := v.(type) {
		case float64:
			if x != float64(int64(x)) {
				return nil, fmt.Errorf("%v is not a whole number", x)
			}
			return int64(x), nil
		case int, int64:
			return x, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", clipText(s, 40))
		}
		return n, nil
	case "number":
		if x, ok := v.(float64); ok {
			return x, nil
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", clipText(s, 40))
		}
		return n, nil
	case "boolean":
		if x, ok := v.(bool); ok {
			return x, nil
		}
		switch strings.ToLower(s) {
		case "true", "yes", "y", "1", "on":
			return true, nil
		case "false", "no", "n", "0", "off":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not true or false", clipText(s, 40))
	case "json", "object", "array":
		if !isStr {
			return v, nil
		}
		var x any
		if err := json.Unmarshal([]byte(s), &x); err != nil {
			return nil, fmt.Errorf("not valid JSON: %v", err)
		}
		if p.Type == "object" {
			if _, ok := x.(map[string]any); !ok {
				return nil, fmt.Errorf("must be a JSON object")
			}
		}
		if p.Type == "array" {
			if _, ok := x.([]any); !ok {
				return nil, fmt.Errorf("must be a JSON array")
			}
		}
		return x, nil
	}
	if !isStr {
		s = scalarText(v)
	}
	if len(p.Enum) > 0 {
		found := false
		for _, e := range p.Enum {
			if e == s {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%q is not one of: %s", clipText(s, 40), strings.Join(p.Enum, ", "))
		}
	}
	switch p.Format {
	case "date":
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, fmt.Errorf("%q is not a date (YYYY-MM-DD)", clipText(s, 40))
		}
	case "date-time":
		ok := false
		for _, l := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04"} {
			if _, err := time.Parse(l, s); err == nil {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%q is not a date and time (YYYY-MM-DDTHH:MM)", clipText(s, 40))
		}
	case "url":
		if !strings.Contains(s, "://") {
			return nil, fmt.Errorf("%q is not a URL", clipText(s, 40))
		}
	case "email":
		if !strings.Contains(s, "@") {
			return nil, fmt.Errorf("%q is not an email address", clipText(s, 40))
		}
	}
	return s, nil
}

// CheckOutput checks a tool's output against the shape it declares (none:
// nothing declared, or it fits).
func CheckOutput(e Extension, args map[string]any, out string) []string {
	sc := e.Output
	if by := e.OutputBy; len(by) > 0 { // a shape chosen by an argument (format=json …)
		for p, m := range by {
			if v, ok := args[p]; ok {
				if s, ok := m[scalarText(v)]; ok {
					sc = s
				}
			}
		}
	}
	if !sc.Known() || sc.Type == "string" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return []string{"the output is not JSON, but the tool declares " + sc.Type + " output"}
	}
	return shapeProblems(sc, v, "")
}

func shapeProblems(sc *flow.Schema, v any, at string) []string {
	if !sc.Known() {
		return nil
	}
	name := firstNonBlank(at, "the output")
	switch sc.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return []string{name + " is not an object"}
		}
		var out []string
		for _, r := range sc.Required {
			if _, ok := m[r]; !ok {
				out = append(out, fmt.Sprintf("%s has no field %q", name, r))
			}
		}
		for _, p := range sc.Properties {
			if x, ok := m[p.Name]; ok && x != nil {
				out = append(out, shapeProblems(p.Schema, x, strings.TrimPrefix(at+"."+p.Name, "."))...)
			}
		}
		return out
	case "array":
		l, ok := v.([]any)
		if !ok {
			return []string{name + " is not a list"}
		}
		if len(l) > 0 && sc.Items != nil {
			return shapeProblems(sc.Items, l[0], at+"[0]")
		}
	case "number", "integer":
		if _, ok := v.(float64); !ok {
			return []string{name + " is not a number"}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return []string{name + " is not true or false"}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return []string{name + " is not text"}
		}
	}
	return nil
}
