package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"codegen/internal/flow"
)

// paramNameRE is a parameter name (as JSON Schema property names go).
var paramNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

// ToolTest is a saved test case: arguments to run the tool with, and what
// to expect.
type ToolTest struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args"`
	Expect ToolExpect     `json:"expect"`
}

// ToolExpect is what a test case expects. A test passes when the tool
// succeeds (or fails, with Fail), its output contains every Contains text,
// every Fields path exists in its JSON output, and the output fits the
// declared shape.
type ToolExpect struct {
	Fail     bool     `json:"fail,omitempty"`
	Contains []string `json:"contains,omitempty"`
	Fields   []string `json:"fields,omitempty"`
}

// ToolMeta is the metadata of a tool that can be refined without changing
// what runs (so without approving it again): its parameters, output shape
// and test cases.
type ToolMeta struct {
	Params []Param      `json:"params"`
	Output *flow.Schema `json:"output,omitempty"`
	Tests  []ToolTest   `json:"tests,omitempty"`
}

// UpdateMetadata saves a tool's parameters, output shape and tests. It is
// allowed for every tool the project keeps (scripts, npm scripts, Copilot
// extension tools — those are saved here even before they are approved),
// not for admin-managed tools, whose catalog entry is signed.
func (s *Store) UpdateMetadata(name string, m ToolMeta) (Extension, error) {
	if err := checkMeta(m); err != nil {
		return Extension{}, err
	}
	all, err := s.List() // includes npm scripts and Copilot tools not saved yet
	if err != nil {
		return Extension{}, err
	}
	var cur *Extension
	for i := range all {
		if all[i].Name == name {
			cur = &all[i]
		}
	}
	if cur == nil {
		return Extension{}, fmt.Errorf("no tool named %s", name)
	}
	if IsManagedScope(cur.Scope) || cur.Managed != nil {
		return Extension{}, errors.New("admin-managed tools are changed in the catalog")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return Extension{}, err
	}
	i := -1
	for k := range exts {
		if exts[k].Name == name {
			i = k
		}
	}
	if i < 0 { // listed, not saved yet: keep it as it is listed
		exts = append(exts, *cur)
		i = len(exts) - 1
	}
	exts[i].Params, exts[i].Output, exts[i].Tests, exts[i].MetaAt = m.Params, m.Output, m.Tests, time.Now()
	if exts[i].Params == nil {
		exts[i].Params = []Param{}
	}
	return exts[i], s.write(exts)
}

func checkMeta(m ToolMeta) error {
	seen := map[string]bool{}
	for _, p := range m.Params {
		if !paramNameRE.MatchString(p.Name) {
			return fmt.Errorf("%q is not a parameter name", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("two parameters are called %s", p.Name)
		}
		seen[p.Name] = true
		if p.Type != "" && !contains(ParamTypes, p.Type) {
			return fmt.Errorf("%s: unknown type %q", p.Name, p.Type)
		}
		if !contains(ParamFormats, p.Format) {
			return fmt.Errorf("%s: unknown format %q", p.Name, p.Format)
		}
	}
	for _, t := range m.Tests {
		if strings.TrimSpace(t.Name) == "" {
			return errors.New("a test case has no name")
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestCheck is one check of a test run.
type TestCheck struct {
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	Note  string `json:"note,omitempty"`
}

// JudgeRun checks a run's result against what a test case expects (and the
// tool's declared output shape).
func JudgeRun(e Extension, args map[string]any, res RunResult, x ToolExpect) []TestCheck {
	var out []TestCheck
	if x.Fail {
		out = append(out, TestCheck{Label: "It fails, as expected", OK: !res.OK, Note: firstNonBlank(res.Error, "")})
	} else {
		out = append(out, TestCheck{Label: "It runs without an error", OK: res.OK, Note: res.Error})
	}
	for _, c := range x.Contains {
		out = append(out, TestCheck{Label: fmt.Sprintf("The output contains %q", c), OK: strings.Contains(res.Output, c)})
	}
	if len(x.Fields) > 0 {
		var v any
		jsonErr := json.Unmarshal([]byte(strings.TrimSpace(res.Output)), &v)
		for _, f := range x.Fields {
			ok := jsonErr == nil && fieldExists(v, f)
			note := ""
			if jsonErr != nil {
				note = "the output is not JSON"
			}
			out = append(out, TestCheck{Label: fmt.Sprintf("The output has the field %s", f), OK: ok, Note: note})
		}
	}
	if res.OK && !x.Fail {
		if probs := CheckOutput(e, args, res.Output); len(probs) > 0 {
			out = append(out, TestCheck{Label: "The output fits the declared shape", OK: false, Note: strings.Join(probs, "; ")})
		} else if e.Output.Known() && e.Output.Type != "string" {
			out = append(out, TestCheck{Label: "The output fits the declared shape", OK: true})
		}
	}
	return out
}

// fieldExists reports whether a path (rows[0].id, items[*].qty, a.b) exists in v.
func fieldExists(v any, path string) bool {
	path = strings.TrimPrefix(strings.TrimSpace(path), "$.")
	if path == "" || path == "$" {
		return true
	}
	cur := []any{v}
	for _, part := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(path, "[", ".["), "..", "."), ".") {
		if part == "" {
			continue
		}
		var next []any
		for _, c := range cur {
			switch {
			case part == "[*]":
				if l, ok := c.([]any); ok {
					next = append(next, l...)
				}
			case strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]"):
				var i int
				if l, ok := c.([]any); ok {
					if _, err := fmt.Sscan(part[1:len(part)-1], &i); err == nil && i >= 0 && i < len(l) {
						next = append(next, l[i])
					}
				}
			default:
				if m, ok := c.(map[string]any); ok {
					if x, ok := m[part]; ok {
						next = append(next, x)
					}
				}
			}
		}
		if len(next) == 0 {
			return false
		}
		cur = next
	}
	return true
}
