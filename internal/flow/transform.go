package flow

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- templates ----------------------------------------------------------------

// Data is what templates can read while a flow runs (or in a preview).
type Data struct {
	Inputs  map[string]string `json:"inputs"`
	Steps   map[string]string `json:"steps"`
	Context string            `json:"context"`
	Skills  string            `json:"skills"`
	Input   string            `json:"input"` // the current step's input
	// InputFile is the path of a file holding Input ({{input_file}}), for
	// tool fields that expect a file rather than the text itself.
	InputFile string `json:"inputFile,omitempty"`
	Output    string `json:"output"` // an output's content (in actions)
	Flow      string `json:"flow"`
	// Files are the saved output files of the steps that ran, by step id
	// ({{<step>.output_file}}); Names maps tool names and step titles to
	// step ids, so {{html-report-generator.output_file}} works.
	Files map[string]string `json:"files,omitempty"`
	Names map[string]string `json:"names,omitempty"`
}

// Render fills {{…}} variables in tpl. Unknown variables become empty.
func Render(tpl string, d Data) string {
	return varRE.ReplaceAllStringFunc(tpl, func(m string) string {
		v, _ := lookup(strings.TrimSpace(m[2:len(m)-2]), d)
		return v
	})
}

func lookup(v string, d Data) (string, bool) {
	head, rest, _ := strings.Cut(v, ".")
	switch head {
	case "inputs":
		name, path, _ := strings.Cut(rest, ".")
		s, ok := d.Inputs[name]
		if ok && path != "" {
			return jsonAt(s, path)
		}
		return s, ok
	case "steps":
		id, path, _ := strings.Cut(rest, ".")
		s, ok := d.Steps[id]
		if !ok {
			return "", false
		}
		if v, ok := stepField(d, id, path); ok {
			return v, true
		}
		if p, ok := strings.CutPrefix(path, "json"); ok {
			return jsonAt(s, strings.TrimPrefix(p, "."))
		}
		return s, true
	case "input":
		if p, ok := strings.CutPrefix(rest, "json"); ok {
			return jsonAt(d.Input, strings.TrimPrefix(p, "."))
		}
		return d.Input, true
	case "input_file":
		return d.InputFile, true
	case "output":
		return d.Output, true
	case "context":
		return d.Context, true
	case "skills":
		return d.Skills, true
	case "flow":
		return d.Flow, true
	case "date":
		return time.Now().Format("2006-01-02"), true
	case "now":
		return time.Now().Format(time.RFC3339), true
	}
	// {{<step>.output_file}} / {{<step>.buffer_output}}: <step> is a step
	// id, its tool's name or its title
	if IsStepField(rest) {
		id := head
		if _, ok := d.Steps[id]; !ok {
			id = d.Names[head]
		}
		if _, ok := d.Steps[id]; ok {
			return stepField(d, id, rest)
		}
	}
	return "", false
}

// IsStepField reports whether rest (after "<step>.") names a step's output:
// output_file, buffer_output or buffer_output.json.<path>.
func IsStepField(rest string) bool {
	return rest == "output_file" || rest == "buffer_output" || strings.HasPrefix(rest, "buffer_output.json")
}

func stepField(d Data, id, rest string) (string, bool) {
	switch {
	case rest == "output_file":
		return d.Files[id], true
	case rest == "buffer_output":
		return d.Steps[id], true
	case strings.HasPrefix(rest, "buffer_output.json"):
		p := strings.TrimPrefix(strings.TrimPrefix(rest, "buffer_output.json"), ".")
		return jsonAt(d.Steps[id], p)
	}
	return "", false
}

// jsonAt reads a path like items[0].name (or $.items[0].name) from JSON
// text; objects and arrays come back as JSON, strings as text.
func jsonAt(src, path string) (string, bool) {
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		return "", false
	}
	got, err := walkPath(v, path)
	if err != nil {
		return "", false
	}
	return jsonText(got), true
}

func jsonText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

var pathTok = regexp.MustCompile(`\[(\d+|\*)\]|\["([^"]+)"\]|([^.\[\]]+)`)

func walkPath(v any, path string) (any, error) {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	if path == "" {
		return v, nil
	}
	cur := []any{v}
	wild := false
	for _, m := range pathTok.FindAllStringSubmatch(path, -1) {
		var next []any
		for _, c := range cur {
			switch {
			case m[1] == "*":
				wild = true
				switch x := c.(type) {
				case []any:
					next = append(next, x...)
				case map[string]any:
					for _, k := range sortedKeys(x) {
						next = append(next, x[k])
					}
				}
			case m[1] != "":
				i, _ := strconv.Atoi(m[1])
				if a, ok := c.([]any); ok && i < len(a) {
					next = append(next, a[i])
				}
			default:
				key := m[2] + m[3]
				switch x := c.(type) {
				case map[string]any:
					if val, ok := x[key]; ok {
						next = append(next, val)
					}
				case []any: // a key on an array maps over it: items.name
					wild = true
					for _, e := range x {
						if o, ok := e.(map[string]any); ok {
							if val, ok := o[key]; ok {
								next = append(next, val)
							}
						}
					}
				}
			}
		}
		cur = next
		if len(cur) == 0 {
			return nil, fmt.Errorf("%s: nothing at %q", path, m[0])
		}
	}
	if wild {
		return cur, nil
	}
	return cur[0], nil
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// --- merge ----------------------------------------------------------------------

// Merge combines the outputs of a merge step's parents (in edge order).
// titles maps step ids to the names used as JSON keys.
func Merge(n Node, parents []string, d Data, titles map[string]string) (string, error) {
	switch n.Mode {
	case "concat", "":
		sep := n.Separator
		if sep == "" {
			sep = "\n\n"
		}
		sep = strings.NewReplacer(`\n`, "\n", `\t`, "\t").Replace(sep)
		parts := make([]string, 0, len(parents))
		for _, p := range parents {
			parts = append(parts, d.Steps[p])
		}
		return strings.Join(parts, sep), nil
	case "json-object":
		obj := map[string]any{}
		for _, p := range parents {
			key := titles[p]
			if key == "" {
				key = p
			}
			obj[key] = asJSON(d.Steps[p])
		}
		b, err := json.MarshalIndent(obj, "", "  ")
		return string(b), err
	case "json-array":
		arr := make([]any, 0, len(parents))
		for _, p := range parents {
			arr = append(arr, asJSON(d.Steps[p]))
		}
		b, err := json.MarshalIndent(arr, "", "  ")
		return string(b), err
	case "template":
		return Render(n.Template, d), nil
	}
	return "", fmt.Errorf("unknown merge mode %q", n.Mode)
}

// asJSON keeps JSON text as structured data and anything else as a string.
func asJSON(s string) any {
	var v any
	if t := strings.TrimSpace(s); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Unmarshal([]byte(t), &v) == nil {
		return v
	}
	return s
}

// --- transforms -----------------------------------------------------------------

// Op is one transformation.
type Op struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	Help  string `json:"help"`
	Expr  string `json:"expr,omitempty"` // what Expr means for this op ("" = not used)
	Repl  bool   `json:"replace,omitempty"`
	Tpl   bool   `json:"template,omitempty"`
	AI    bool   `json:"ai,omitempty"`   // needs the agent (runs only in a real run)
	Exec  bool   `json:"exec,omitempty"` // runs a command (only in a real run)
	check func(n Node) error
	apply func(in string, n Node, d Data) (string, error)
}

// Ops lists the transformations by id.
var Ops = map[string]Op{
	"template":      {Name: "Fill a template", Group: "Text", Help: "Write the result with {{input}} and any other variable.", Tpl: true, check: needTemplate, apply: func(in string, n Node, d Data) (string, error) { d.Input = in; return Render(n.Template, d), nil }},
	"trim":          {Name: "Trim whitespace", Group: "Text", apply: func(in string, _ Node, _ Data) (string, error) { return strings.TrimSpace(in), nil }},
	"uppercase":     {Name: "UPPERCASE", Group: "Text", apply: func(in string, _ Node, _ Data) (string, error) { return strings.ToUpper(in), nil }},
	"lowercase":     {Name: "lowercase", Group: "Text", apply: func(in string, _ Node, _ Data) (string, error) { return strings.ToLower(in), nil }},
	"truncate":      {Name: "Keep the first N characters", Group: "Text", Expr: "N", check: needInt, apply: truncate},
	"filter-lines":  {Name: "Keep matching lines", Group: "Text", Expr: "Regular expression", check: needRegex, apply: filterLines},
	"regex-extract": {Name: "Extract with a pattern", Group: "Text", Help: "Every match on its own line (the first group if the pattern has one).", Expr: "Regular expression", check: needRegex, apply: regexExtract},
	"regex-replace": {Name: "Find and replace", Group: "Text", Help: "$1, $2 … refer to groups.", Expr: "Regular expression", Repl: true, check: needRegex, apply: regexReplace},
	"split-lines":   {Name: "Lines → JSON array", Group: "Convert", apply: splitLines},
	"json-path":     {Name: "Pick a JSON value", Group: "JSON", Help: "A path like items[0].name, items[*].id or $.data.total.", Expr: "Path", check: needExpr, apply: jsonPath},
	"json-pick":     {Name: "Keep JSON fields", Group: "JSON", Help: "Comma-separated field names; works on an object or an array of objects.", Expr: "Fields", check: needExpr, apply: jsonPick},
	"json-pretty":   {Name: "Format JSON", Group: "JSON", apply: jsonPretty},
	"json-minify":   {Name: "Minify JSON", Group: "JSON", apply: jsonMinify},
	"csv-to-json":   {Name: "CSV → JSON", Group: "Convert", apply: csvToJSON},
	"json-to-csv":   {Name: "JSON → CSV", Group: "Convert", apply: jsonToCSV},
	"md-to-html":    {Name: "Markdown → HTML", Group: "Convert", apply: func(in string, _ Node, _ Data) (string, error) { return MarkdownToHTML(in), nil }},
	"html-to-text":  {Name: "HTML → text", Group: "Convert", apply: func(in string, _ Node, _ Data) (string, error) { return HTMLToText(in), nil }},
	"command":       {Name: "Run a command", Group: "Command", Help: "A shell command run in the project; the data arrives on stdin and stdout is the result (e.g. jq '.items', sort -u).", Expr: "Command", Exec: true, check: needExpr},
	"ai":            {Name: "Ask AI to transform", Group: "AI", Help: "Describe the change; {{input}} is the data. Runs with the agent when the flow runs.", Tpl: true, AI: true, check: needTemplate},
}

func needTemplate(n Node) error {
	if strings.TrimSpace(n.Template) == "" {
		return errors.New("write the template")
	}
	return nil
}
func needExpr(n Node) error {
	if strings.TrimSpace(n.Expr) == "" {
		return errors.New("fill in the expression")
	}
	return nil
}
func needRegex(n Node) error {
	if n.Expr == "" {
		return errors.New("write the pattern")
	}
	_, err := regexp.Compile(n.Expr)
	if err != nil {
		return fmt.Errorf("the pattern is not valid: %v", err)
	}
	return nil
}
func needInt(n Node) error {
	if i, err := strconv.Atoi(strings.TrimSpace(n.Expr)); err != nil || i < 0 {
		return errors.New("N must be a whole number")
	}
	return nil
}

// Transform applies a transform step to its input. AI transforms need the
// agent and are refused here (the executor runs them).
func Transform(n Node, in string, d Data) (string, error) {
	op, ok := Ops[n.Op]
	if !ok {
		return "", fmt.Errorf("unknown transformation %q", n.Op)
	}
	if op.check != nil {
		if err := op.check(n); err != nil {
			return "", err
		}
	}
	if op.AI {
		return "", errors.New("AI transformations run with the agent when the flow runs")
	}
	if op.Exec {
		return "", errors.New("commands run when the flow runs")
	}
	d.Input = in
	return op.apply(in, n, d)
}

func truncate(in string, n Node, _ Data) (string, error) {
	k, _ := strconv.Atoi(strings.TrimSpace(n.Expr))
	r := []rune(in)
	if len(r) <= k {
		return in, nil
	}
	return string(r[:k]), nil
}

func filterLines(in string, n Node, _ Data) (string, error) {
	re := regexp.MustCompile(n.Expr)
	var out []string
	for _, l := range strings.Split(in, "\n") {
		if re.MatchString(l) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n"), nil
}

func regexExtract(in string, n Node, _ Data) (string, error) {
	re := regexp.MustCompile(n.Expr)
	var out []string
	for _, m := range re.FindAllStringSubmatch(in, -1) {
		if len(m) > 1 {
			out = append(out, m[1])
		} else {
			out = append(out, m[0])
		}
	}
	return strings.Join(out, "\n"), nil
}

func regexReplace(in string, n Node, _ Data) (string, error) {
	return regexp.MustCompile(n.Expr).ReplaceAllString(in, n.Replace), nil
}

func splitLines(in string, _ Node, _ Data) (string, error) {
	lines := []string{}
	for _, l := range strings.Split(strings.TrimRight(in, "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	b, err := json.MarshalIndent(lines, "", "  ")
	return string(b), err
}

func parseJSON(in string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(in)), &v); err != nil {
		return nil, fmt.Errorf("the input is not JSON: %v", err)
	}
	return v, nil
}

func jsonPath(in string, n Node, _ Data) (string, error) {
	v, err := parseJSON(in)
	if err != nil {
		return "", err
	}
	got, err := walkPath(v, n.Expr)
	if err != nil {
		return "", err
	}
	return jsonText(got), nil
}

func jsonPick(in string, n Node, _ Data) (string, error) {
	v, err := parseJSON(in)
	if err != nil {
		return "", err
	}
	var keys []string
	for _, k := range strings.Split(n.Expr, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	pick := func(o map[string]any) map[string]any {
		out := map[string]any{}
		for _, k := range keys {
			if val, ok := o[k]; ok {
				out[k] = val
			}
		}
		return out
	}
	var res any
	switch x := v.(type) {
	case map[string]any:
		res = pick(x)
	case []any:
		arr := make([]any, 0, len(x))
		for _, e := range x {
			if o, ok := e.(map[string]any); ok {
				arr = append(arr, pick(o))
			}
		}
		res = arr
	default:
		return "", errors.New("the input is not a JSON object or array")
	}
	b, err := json.MarshalIndent(res, "", "  ")
	return string(b), err
}

func jsonPretty(in string, _ Node, _ Data) (string, error) {
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(strings.TrimSpace(in)), "", "  "); err != nil {
		return "", fmt.Errorf("the input is not JSON: %v", err)
	}
	return b.String(), nil
}

func jsonMinify(in string, _ Node, _ Data) (string, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(strings.TrimSpace(in))); err != nil {
		return "", fmt.Errorf("the input is not JSON: %v", err)
	}
	return b.String(), nil
}

func csvToJSON(in string, _ Node, _ Data) (string, error) {
	rows, err := csv.NewReader(strings.NewReader(strings.TrimSpace(in))).ReadAll()
	if err != nil {
		return "", fmt.Errorf("the input is not CSV: %v", err)
	}
	if len(rows) == 0 {
		return "[]", nil
	}
	head, out := rows[0], make([]map[string]string, 0, len(rows)-1)
	for _, r := range rows[1:] {
		o := map[string]string{}
		for i, h := range head {
			if i < len(r) {
				o[h] = r[i]
			}
		}
		out = append(out, o)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b), err
}

func jsonToCSV(in string, _ Node, _ Data) (string, error) {
	v, err := parseJSON(in)
	if err != nil {
		return "", err
	}
	return toCSV(v)
}

func toCSV(v any) (string, error) {
	var rows []map[string]any
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if o, ok := e.(map[string]any); ok {
				rows = append(rows, o)
			} else {
				rows = append(rows, map[string]any{"value": e})
			}
		}
	case map[string]any:
		rows = []map[string]any{x}
	default:
		return "", errors.New("CSV needs a JSON array of objects")
	}
	seen, head := map[string]bool{}, []string{}
	for _, r := range rows {
		for _, k := range sortedKeys(r) {
			if !seen[k] {
				seen[k] = true
				head = append(head, k)
			}
		}
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write(head)
	for _, r := range rows {
		rec := make([]string, len(head))
		for i, k := range head {
			if val, ok := r[k]; ok {
				rec[i] = strings.ReplaceAll(jsonText(val), "\n", " ")
			}
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return b.String(), w.Error()
}

// --- output formats ---------------------------------------------------------

// Convert turns content into an output format, recognizing what it is
// (JSON, CSV, HTML, Markdown or plain text) and converting sensibly.
func Convert(content, format string) (string, error) {
	t := strings.TrimSpace(content)
	isJSON := (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t))
	isHTML := regexp.MustCompile(`(?i)^\s*(<!doctype|<html|<(div|p|h[1-6]|table|ul|ol|section|article|body)\b)`).MatchString(t)
	switch format {
	case "json":
		if isJSON {
			return jsonPretty(t, Node{}, Data{})
		}
		if looksCSV(t) {
			return csvToJSON(t, Node{}, Data{})
		}
		b, _ := json.MarshalIndent(map[string]string{"content": content}, "", "  ")
		return string(b), nil
	case "csv":
		if isJSON {
			v, _ := parseJSON(t)
			return toCSV(v)
		}
		if looksCSV(t) {
			return t + "\n", nil
		}
		var b bytes.Buffer
		w := csv.NewWriter(&b)
		_ = w.Write([]string{"line"})
		for _, l := range strings.Split(t, "\n") {
			_ = w.Write([]string{l})
		}
		w.Flush()
		return b.String(), nil
	case "html":
		switch {
		case isHTML:
			return content, nil
		case isJSON:
			return htmlDoc("<pre><code>" + html.EscapeString(t) + "</code></pre>"), nil
		default:
			return htmlDoc(MarkdownToHTML(content)), nil
		}
	case "txt":
		if isHTML {
			return HTMLToText(content), nil
		}
		return content, nil
	case "md":
		switch {
		case isHTML:
			return HTMLToText(content), nil
		case isJSON:
			if tbl := jsonToMarkdownTable(t); tbl != "" {
				return tbl, nil
			}
			return "```json\n" + t + "\n```\n", nil
		}
		return content, nil
	}
	return "", fmt.Errorf("unknown format %q", format)
}

func looksCSV(t string) bool {
	lines := strings.Split(t, "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], ",") {
		return false
	}
	rows, err := csv.NewReader(strings.NewReader(t)).ReadAll()
	return err == nil && len(rows) > 1 && len(rows[0]) > 1
}

func jsonToMarkdownTable(t string) string {
	c, err := Convert(t, "csv")
	if err != nil {
		return ""
	}
	rows, err := csv.NewReader(strings.NewReader(c)).ReadAll()
	if err != nil || len(rows) < 2 || len(rows) > 500 {
		return ""
	}
	var b strings.Builder
	cell := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	for i, r := range rows {
		cs := make([]string, len(r))
		for j, x := range r {
			cs[j] = cell(x)
		}
		b.WriteString("| " + strings.Join(cs, " | ") + " |\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", len(r)) + "\n")
		}
	}
	return b.String()
}

func htmlDoc(body string) string {
	return "<!doctype html>\n<html><head><meta charset=\"utf-8\"><style>body{font:15px/1.55 -apple-system,Segoe UI,sans-serif;max-width:860px;margin:2rem auto;padding:0 1rem}pre{background:#f6f8fa;padding:12px;overflow:auto}code{font-family:ui-monospace,Menlo,monospace}table{border-collapse:collapse}td,th{border:1px solid #d0d7de;padding:4px 8px}</style></head><body>\n" + body + "\n</body></html>\n"
}

// MarkdownToHTML renders the common Markdown subset: headings, paragraphs,
// lists, code blocks, block quotes, tables, rules, bold, italics, inline
// code and links.
func MarkdownToHTML(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var b strings.Builder
	var para []string
	list := ""
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "```"):
			flush()
			closeList()
			lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			cls := ""
			if lang != "" {
				cls = ` class="language-` + html.EscapeString(lang) + `"`
			}
			b.WriteString("<pre><code" + cls + ">" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
		case t == "":
			flush()
			closeList()
		case regexp.MustCompile(`^#{1,6}\s`).MatchString(t):
			flush()
			closeList()
			lv := len(t) - len(strings.TrimLeft(t, "#"))
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", lv, inline(strings.TrimSpace(t[lv:])), lv)
		case t == "---" || t == "***":
			flush()
			closeList()
			b.WriteString("<hr>\n")
		case strings.HasPrefix(t, ">"):
			flush()
			closeList()
			b.WriteString("<blockquote>" + inline(strings.TrimSpace(strings.TrimPrefix(t, ">"))) + "</blockquote>\n")
		case strings.HasPrefix(t, "|") && i+1 < len(lines) && regexp.MustCompile(`^\|?\s*:?-{3,}`).MatchString(strings.TrimSpace(lines[i+1])):
			flush()
			closeList()
			cells := func(s string) []string {
				s = strings.Trim(strings.TrimSpace(s), "|")
				parts := strings.Split(s, "|")
				for k := range parts {
					parts[k] = strings.TrimSpace(parts[k])
				}
				return parts
			}
			b.WriteString("<table><thead><tr>")
			for _, c := range cells(t) {
				b.WriteString("<th>" + inline(c) + "</th>")
			}
			b.WriteString("</tr></thead><tbody>\n")
			for i += 2; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				b.WriteString("<tr>")
				for _, c := range cells(lines[i]) {
					b.WriteString("<td>" + inline(c) + "</td>")
				}
				b.WriteString("</tr>\n")
			}
			i--
			b.WriteString("</tbody></table>\n")
		case regexp.MustCompile(`^[-*+]\s`).MatchString(t), regexp.MustCompile(`^\d+[.)]\s`).MatchString(t):
			flush()
			kind := "ul"
			if regexp.MustCompile(`^\d`).MatchString(t) {
				kind = "ol"
			}
			if list != kind {
				closeList()
				b.WriteString("<" + kind + ">\n")
				list = kind
			}
			item := regexp.MustCompile(`^([-*+]|\d+[.)])\s+`).ReplaceAllString(t, "")
			b.WriteString("<li>" + inline(item) + "</li>\n")
		default:
			closeList()
			para = append(para, t)
		}
	}
	flush()
	closeList()
	return b.String()
}

var (
	reCode   = regexp.MustCompile("`([^`]+)`")
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	reItalic = regexp.MustCompile(`\*([^*\s][^*]*)\*|\b_([^_]+)_\b`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

func inline(s string) string {
	// protect inline code first
	var codes []string
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	s = html.EscapeString(s)
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reLink.FindStringSubmatch(m)
		href := p[2]
		if !regexp.MustCompile(`^(https?:|mailto:|#|/|\./)`).MatchString(href) {
			href = "#"
		}
		return `<a href="` + href + `">` + p[1] + `</a>`
	})
	s = reBold.ReplaceAllString(s, "<strong>$1$2</strong>")
	s = reItalic.ReplaceAllString(s, "<em>$1$2</em>")
	for i, c := range codes {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), c, 1)
	}
	return s
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reBlock  = regexp.MustCompile(`(?i)</?(p|div|h[1-6]|li|tr|br|pre|blockquote|section|article|table|ul|ol)[^>]*>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reBlank  = regexp.MustCompile(`\n{3,}`)
)

// HTMLToText strips tags, keeping block breaks.
func HTMLToText(h string) string {
	s := reScript.ReplaceAllString(h, "")
	s = regexp.MustCompile(`(?i)<li[^>]*>`).ReplaceAllString(s, "\n- ")
	s = regexp.MustCompile(`(?i)</t[dh]>\s*<t[dh][^>]*>`).ReplaceAllString(s, " | ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(strings.Join(strings.Fields(lines[i]), " "), " ")
	}
	return strings.TrimSpace(reBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")) + "\n"
}
