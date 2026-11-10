package extension

import (
	"path/filepath"
	"strings"
)

// Command templates are stored as one string ("node .codegen/tools/x.js
// {{path}}") and split into argv before running; no shell is ever involved.
// The quoting rules are a small, predictable subset of POSIX sh, chosen so
// any path round-trips (spaces, quotes, $, &, #, unicode) and Windows
// backslashes need no escaping:
//
//	'…'   literal; may contain anything except '
//	"…"   \" and \\ are escapes; any other backslash is literal
//	bare  backslash is literal (C:\Tools\x.exe works unquoted)

// SplitWords splits a command template into words.
func SplitWords(s string) []string {
	var out []string
	var cur strings.Builder
	in := false // inside a word (even an empty quoted one)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\'':
			in = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
		case r == '"':
			in = true
			for i++; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// QuoteArg quotes s so SplitWords returns it unchanged as one word.
func QuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\r\n'\"") {
		return s
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// JoinWords is the inverse of SplitWords.
func JoinWords(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = QuoteArg(a)
	}
	return strings.Join(q, " ")
}

// isToolScript reports whether a command word names a script under
// .codegen/tools/ (any extension), optionally for one tool name.
func isToolScript(word, name string) bool {
	w := filepath.ToSlash(word)
	if !strings.Contains(w, ".codegen/tools/") {
		return false
	}
	if name == "" {
		return true
	}
	base := strings.TrimSuffix(filepath.Base(w), filepath.Ext(w))
	return base == name || strings.ReplaceAll(base, "_", "-") == name
}

// RepointScript returns cmd with the word naming the tool's script replaced
// by path. It prefers a word that names the tool itself and falls back to
// the first .codegen/tools/ word, then requotes the whole command.
func RepointScript(cmd, name, path string) string {
	words := SplitWords(cmd)
	idx := -1
	for i, w := range words {
		if isToolScript(w, name) {
			idx = i
			break
		}
	}
	if idx < 0 {
		for i, w := range words {
			if isToolScript(w, "") {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return cmd
	}
	words[idx] = path
	return JoinWords(words)
}
