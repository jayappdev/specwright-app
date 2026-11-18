// Package bigout handles tool results that are too large to hand to an AI
// agent whole.
//
// Cutting a result at a byte count breaks it: half a JSON document, an
// unclosed HTML tag, a split UTF-8 character. Instead, a large result is
// saved whole to a file in the workspace (.codegen/tool-output/) and the
// agent gets a short message that is valid on its own: the size and kind,
// where the full result is, how to read it in parts, and a preview — for
// JSON a description of its structure, for text the first lines, cut at a
// line boundary.
package bigout

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// AgentLimit is the largest result passed to an agent as it is.
const AgentLimit = 100 << 10

// Dir is where full results are kept, relative to the workspace.
const Dir = ".codegen/tool-output"

// keep is how many saved results are kept (the oldest go first).
const keep = 50

// ForAgent returns s when it fits AgentLimit; otherwise it saves s under
// root and returns the message the agent gets instead (and true).
func ForAgent(root, tool, s string) (string, bool) {
	if len(s) <= AgentLimit {
		return s, false
	}
	kind := Kind(s)
	rel, err := Save(root, tool, kind, s)
	var b strings.Builder
	fmt.Fprintf(&b, "The result of %s is %s of %s, too large to return whole", tool, Human(len(s)), kindName(kind))
	if err != nil {
		fmt.Fprintf(&b, " (and it could not be saved: %v).\n", err)
	} else {
		fmt.Fprintf(&b, ". The full result is saved in %s (%d lines).\n", rel, strings.Count(s, "\n")+1)
		fmt.Fprintf(&b, "Read it in parts with read_file (path %q, start_line / end_line)", rel)
		if kind == "json" {
			fmt.Fprintf(&b, ", or extract what you need with run_command, e.g. jq '.items[0:5]' %s", rel)
		} else {
			fmt.Fprintf(&b, ", or search it with run_command (grep -n 'pattern' %s)", rel)
		}
		b.WriteString(". Do not guess the parts you have not read.\n")
	}
	if kind == "json" {
		if d := DescribeJSON(s); d != "" {
			b.WriteString("\nStructure:\n" + d + "\n")
		}
	}
	prev := SafeCut(s, 4<<10)
	b.WriteString("\nPreview (the first " + Human(len(prev)) + "; not complete")
	if kind == "json" || kind == "html" || kind == "xml" {
		b.WriteString(", so not valid " + strings.ToUpper(kind) + " on its own")
	}
	b.WriteString("):\n" + prev + "\n…")
	return b.String(), true
}

// SafeCut returns at most n bytes of s, ending at a line break (or else a
// space) in the last fifth of that range, and never inside a UTF-8 character.
func SafeCut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	head := s[:cut]
	floor := cut * 4 / 5
	if i := strings.LastIndex(head, "\n"); i >= floor {
		return head[:i]
	}
	if i := strings.LastIndexAny(head, " \t"); i >= floor {
		return head[:i]
	}
	return head
}

var (
	htmlStart = regexp.MustCompile(`(?is)^\s*(<!doctype html|<html|<head|<body|<div|<table|<p[ >])`)
	xmlStart  = regexp.MustCompile(`^\s*<\?xml|^\s*<[A-Za-z][\w:.-]*[\s>]`)
	mdStart   = regexp.MustCompile(`(?m)^(#{1,6} |[-*] |\d+\. |\|.*\|$|` + "```" + `)`)
)

// Kind guesses what s is: json, html, xml, csv, markdown or text.
func Kind(s string) string {
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return "text"
	case (t[0] == '{' || t[0] == '[') && json.Valid([]byte(t)):
		return "json"
	case htmlStart.MatchString(t):
		return "html"
	case xmlStart.MatchString(t):
		return "xml"
	case looksCSV(t):
		return "csv"
	case len(mdStart.FindAllStringIndex(t[:min(len(t), 20000)], 4)) >= 3:
		return "markdown"
	}
	return "text"
}

func looksCSV(t string) bool {
	lines := strings.SplitN(t, "\n", 6)
	if len(lines) < 3 {
		return false
	}
	n := strings.Count(lines[0], ",")
	if n == 0 {
		return false
	}
	for _, l := range lines[1 : len(lines)-1] {
		if strings.Count(l, ",") != n {
			return false
		}
	}
	return true
}

var extOf = map[string]string{"json": ".json", "html": ".html", "xml": ".xml", "csv": ".csv", "markdown": ".md", "text": ".txt"}

func kindName(k string) string {
	return map[string]string{"json": "JSON", "html": "HTML", "xml": "XML", "csv": "CSV", "markdown": "Markdown", "text": "text"}[k]
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// Save writes content to <root>/.codegen/tool-output/<tool>-<time><ext>,
// keeps the newest results, and returns the path relative to root.
func Save(root, tool, kind, content string) (string, error) {
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		_ = os.WriteFile(gi, []byte("# full tool results saved for the AI to read in parts; delete freely\n*\n"), 0o644)
	}
	name := strings.Trim(unsafeName.ReplaceAllString(tool, "-"), "-.")
	if name == "" {
		name = "tool"
	}
	file := fmt.Sprintf("%s-%s%s", name, time.Now().Format("20060102-150405.000"), extOf[kind])
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		return "", err
	}
	prune(dir)
	return Dir + "/" + file, nil
}

// prune keeps the newest `keep` saved results.
func prune(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type f struct {
		name string
		at   time.Time
	}
	var fs []f
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if in, err := e.Info(); err == nil {
			fs = append(fs, f{e.Name(), in.ModTime()})
		}
	}
	if len(fs) <= keep {
		return
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].at.After(fs[j].at) })
	for _, x := range fs[keep:] {
		_ = os.Remove(filepath.Join(dir, x.name))
	}
}

// DescribeJSON describes a JSON document's structure in a few lines: the
// top-level type, an array's length and its first item's fields, an
// object's keys with their types (array lengths included), one level deep.
func DescribeJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return ""
	}
	var b strings.Builder
	switch x := v.(type) {
	case []any:
		fmt.Fprintf(&b, "an array of %d item(s)", len(x))
		if len(x) > 0 {
			b.WriteString("; the first item is " + typeOf(x[0]))
			if m, ok := x[0].(map[string]any); ok {
				b.WriteString(":\n" + fields(m, "  "))
			}
		}
	case map[string]any:
		b.WriteString("an object with " + fmt.Sprint(len(x)) + " key(s):\n" + fields(x, "  "))
	default:
		b.WriteString("a single " + typeOf(v))
	}
	return strings.TrimRight(b.String(), "\n")
}

func fields(m map[string]any, indent string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i == 40 {
			fmt.Fprintf(&b, "%s… %d more\n", indent, len(keys)-40)
			break
		}
		fmt.Fprintf(&b, "%s%s: %s\n", indent, k, typeOf(m[k]))
	}
	return b.String()
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		if len(x) > 60 {
			return fmt.Sprintf("string (%d characters)", utf8.RuneCountInString(x))
		}
		return "string"
	case []any:
		return fmt.Sprintf("array of %d", len(x))
	case map[string]any:
		return fmt.Sprintf("object (%d keys)", len(x))
	}
	return "value"
}

// Human formats a byte count.
func Human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
