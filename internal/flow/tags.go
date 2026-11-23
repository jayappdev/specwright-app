package flow

// Tags and a group organise flows: the group is where a flow is listed
// (one per flow, e.g. "Releases" or "Billing / Refunds"), tags are labels
// for finding it (any number, e.g. nightly, jira, report). Both are in the
// flow file, so they go with the flow through git and sharing. The flow
// lists search the name, description, group, tags and the steps' names
// and tools (Summary.Keywords).

import (
	"sort"
	"strings"
	"unicode"
)

// MaxTags and the longest tag and group.
const (
	MaxTags     = 20
	maxTagLen   = 32
	maxGroupLen = 60
)

// NormTag makes a tag: lower case, words joined with -, only letters,
// digits and - _ . / (empty when nothing is left).
func NormTag(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '/':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	t := strings.Trim(b.String(), "-")
	if len(t) > maxTagLen {
		t = strings.Trim(t[:maxTagLen], "-")
	}
	return t
}

// NormTags normalises, removes repeats and sorts tags (at most MaxTags).
func NormTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		for _, part := range strings.Split(t, ",") { // "a, b" typed in one field
			if n := NormTag(part); n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	if len(out) > MaxTags {
		out = out[:MaxTags]
	}
	return out
}

// NormGroup tidies a group name (spaces, its length; "a/b" becomes "a / b").
func NormGroup(s string) string {
	parts := strings.Split(s, "/")
	var keep []string
	for _, p := range parts {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			keep = append(keep, p)
		}
	}
	g := strings.Join(keep, " / ")
	if len(g) > maxGroupLen {
		g = strings.TrimSpace(g[:maxGroupLen])
	}
	return g
}

// keywords are what else a flow is found by: its steps' names and tools.
func (f *Flow) keywords() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(strings.TrimPrefix(s, "builtin:"))
		if s != "" && !seen[strings.ToLower(s)] && len(out) < 60 {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	for _, n := range f.Nodes {
		if n.Type == "start" {
			continue
		}
		add(n.Title)
		add(n.Tool)
		for _, a := range n.Actions {
			add(a.Tool)
		}
	}
	return out
}
