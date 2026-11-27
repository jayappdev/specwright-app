package store

// The forms a secret is masked in (docs/security/2026-11-26-security-review.md,
// item 6). A tool may print a secret as it is, or encoded: base64 (an
// Authorization: Basic header, a Kubernetes secret, a config dump), URL
// escaped, hex, JSON escaped. Or a terminal or a base64 tool wraps it across
// lines. Each registered value is masked in all of these.

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// minForm: shorter derived forms are not masked (too likely to match
// ordinary text).
const minForm = 8

// secretForms lists every form of the values, longest first, and a regexp
// for those forms with line breaks inside.
func secretForms(vals map[string]struct{}) ([]string, *regexp.Regexp) {
	set := map[string]bool{}
	add := func(f string, min int) {
		if len(f) >= min {
			set[f] = true
		}
	}
	for v := range vals {
		add(v, 4)
		add(url.QueryEscape(v), minForm)
		add(url.PathEscape(v), minForm)
		add(hex.EncodeToString([]byte(v)), minForm)
		add(strings.ToUpper(hex.EncodeToString([]byte(v))), minForm)
		if b, err := json.Marshal(v); err == nil {
			add(string(b[1:len(b)-1]), minForm)
		}
		for _, f := range base64Forms(v) {
			add(f, minForm)
		}
	}
	forms := make([]string, 0, len(set))
	for f := range set {
		forms = append(forms, f)
	}
	sort.Slice(forms, func(i, j int) bool {
		if len(forms[i]) != len(forms[j]) {
			return len(forms[i]) > len(forms[j])
		}
		return forms[i] < forms[j]
	})
	// wrapped across lines: a line break (and the next line's indent or
	// quote mark) may sit between any two characters
	var alts []string
	for _, f := range forms {
		if len(f) < minForm {
			continue
		}
		var b strings.Builder
		for i, c := range f {
			if i > 0 {
				b.WriteString(`(?:\r?\n[ \t>]*)?`)
			}
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
		alts = append(alts, b.String())
	}
	if len(alts) == 0 {
		return forms, nil
	}
	re, err := regexp.Compile(strings.Join(alts, "|"))
	if err != nil {
		return forms, nil
	}
	return forms, re
}

// mayHoldWrapped: a form broken by one line break still has its first or
// its last 4 characters in one piece (forms are 8 or more long), so text
// with neither cannot hold a wrapped form; the regexp is skipped.
func mayHoldWrapped(s string, forms []string) bool {
	for _, f := range forms {
		if len(f) >= minForm && (strings.Contains(s, f[:4]) || strings.Contains(s, f[len(f)-4:])) {
			return true
		}
	}
	return false
}

// base64Forms: the characters that encode v in standard and URL-safe
// base64, wherever v starts in the encoded data. Starting at a byte offset
// of 0, 1 or 2 changes v's encoding, so each is computed, keeping only the
// characters that depend on v's bytes alone; "user:" + secret inside a
// Basic header is caught that way.
func base64Forms(v string) []string {
	var out []string
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		full := enc.EncodeToString([]byte(v))
		out = append(out, full, strings.TrimRight(full, "="))
		for k := 0; k < 3; k++ {
			e := enc.EncodeToString(append(make([]byte, k), v...))
			n := len(v)
			from := (8*k + 5) / 6   // first character made of v's bits only
			to := (8*(k+n) - 6) / 6 // last such character
			if to >= from && to < len(e) {
				out = append(out, e[from:to+1])
			}
		}
	}
	return out
}
