package flow

// Filling a "Run a command" step's command safely
// (docs/security/2026-11-26-security-review.md, item 1). The command is
// approved as written, with its {{…}} placeholders; the values that fill
// them (inputs, earlier steps' output, a fetched page, an AI's answer) are
// data, so they must never become shell syntax.
//
// POSIX (/bin/sh -c): each value is quoted for where its placeholder sits
// in the command: outside quotes it becomes a single-quoted word;
// inside '…' its ' is closed, escaped and reopened; inside "…" its \ " $ `
// are escaped. So `grep {{inputs.q}} f`, `grep '{{inputs.q}}' f` and
// `grep "{{inputs.q}}" f` all pass the value as one literal argument.
//
// Windows (cmd /C) has no reliable quoting: a value with characters cmd
// treats specially is refused, with a message, rather than run.

import (
	"fmt"
	"strings"
)

// shell quoting context of a position in a POSIX command
const (
	qNone = iota
	qSingle
	qDouble
)

// RenderShell fills a command template with d's values, quoted so that no
// value can change the command's shell syntax. windows: the command runs
// under cmd.exe.
func RenderShell(tpl string, d Data, windows bool) (string, error) {
	var b strings.Builder
	state, last := qNone, 0
	scan := func(s string) { // the quoting state after s, in the template's own text
		for i := 0; i < len(s); i++ {
			c := s[i]
			switch state {
			case qNone:
				switch c {
				case '\\':
					i++ // an escaped character
				case '\'':
					state = qSingle
				case '"':
					state = qDouble
				}
			case qSingle:
				if c == '\'' {
					state = qNone
				}
			case qDouble:
				switch c {
				case '\\':
					i++
				case '"':
					state = qNone
				}
			}
		}
	}
	for _, m := range varRE.FindAllStringSubmatchIndex(tpl, -1) {
		text := tpl[last:m[0]]
		b.WriteString(text)
		scan(text)
		v, _ := lookup(strings.TrimSpace(tpl[m[2]:m[3]]), d)
		if windows {
			if bad := strings.IndexAny(v, "\"%!^&|<>()\r\n`"); bad >= 0 {
				return "", fmt.Errorf("{{%s}} has the character %q, which a Windows command cannot take safely: pass it on standard input (the step's input) or through a file instead",
					strings.TrimSpace(tpl[m[2]:m[3]]), v[bad])
			}
			b.WriteString(v)
		} else {
			b.WriteString(quoteFor(v, state))
		}
		last = m[1]
	}
	b.WriteString(tpl[last:])
	return b.String(), nil
}

// quoteFor quotes v for a POSIX shell at a position in the given state.
func quoteFor(v string, state int) string {
	switch state {
	case qSingle: // inside '…': close, an escaped ', reopen
		return strings.ReplaceAll(v, "'", `'\''`)
	case qDouble: // inside "…": only \ " $ ` are special
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
		return r.Replace(v)
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
