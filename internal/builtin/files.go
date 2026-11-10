package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxReadBytes = 512 << 10 // per read_file call
	maxReadLines = 2000
	maxFileEdit  = 8 << 20
)

func isBinary(b []byte) bool {
	n := min(len(b), 8000)
	return bytes.IndexByte(b[:n], 0) >= 0 || (!utf8.Valid(b[:n]) && !mostlyText(b[:n]))
}

func mostlyText(b []byte) bool {
	bad := 0
	for _, c := range b {
		if c < 9 || (c > 13 && c < 32) {
			bad++
		}
	}
	return bad*20 < len(b)+1
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	ls := strings.Split(s, "\n")
	if ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// --- read_file ---------------------------------------------------------------

func readFile(_ context.Context, b *Box, a args) (string, error) {
	paths := a.strs("paths")
	if p := a.str("path"); p != "" {
		paths = append([]string{p}, paths...)
	}
	if len(paths) == 0 {
		return "", errors.New(`give "path" (or "paths" for several files)`)
	}
	start, end := a.num("start_line", 1, 1, 0), a.num("end_line", 0, 0, 0)
	budget := maxReadBytes
	var out strings.Builder
	for i, path := range paths {
		if i > 0 {
			out.WriteString("\n")
		}
		abs, rel, err := b.resolve(path)
		if err != nil {
			if len(paths) == 1 {
				return "", err
			}
			fmt.Fprintf(&out, "== %s: %v\n", path, err)
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil {
			fmt.Fprintf(&out, "== %s: does not exist\n", rel)
			continue
		}
		if fi.IsDir() {
			fmt.Fprintf(&out, "== %s is a directory; use list_directory\n", rel)
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			fmt.Fprintf(&out, "== %s: %v\n", rel, err)
			continue
		}
		if isBinary(data) {
			fmt.Fprintf(&out, "== %s: binary file (%s), not shown\n", rel, human(fi.Size()))
			continue
		}
		lines := splitLines(string(data))
		s, e := start, end
		if len(paths) > 1 {
			s, e = 1, 0 // ranges apply to a single file
		}
		if e == 0 || e > len(lines) {
			e = len(lines)
		}
		if s > len(lines) && len(lines) > 0 {
			fmt.Fprintf(&out, "== %s has %s; start_line %d is past the end\n", rel, plural(len(lines), "line", "lines"), s)
			continue
		}
		if e-s+1 > maxReadLines {
			e = s + maxReadLines - 1
		}
		fmt.Fprintf(&out, "== %s (lines %d-%d of %d)\n", rel, s, e, len(lines))
		w := len(fmt.Sprint(e))
		truncated := false
		for n := s; n <= e && n <= len(lines); n++ {
			l := fmt.Sprintf("%*d | %s\n", w, n, lines[n-1])
			if budget -= len(l); budget < 0 {
				truncated = true
				e = n - 1
				break
			}
			out.WriteString(l)
		}
		if len(lines) == 0 {
			out.WriteString("(empty file)\n")
		}
		if truncated || e < len(lines) {
			fmt.Fprintf(&out, "… %s more; call read_file with start_line=%d to continue\n", plural(len(lines)-e, "line", "lines"), e+1)
		}
		if budget <= 0 {
			if i < len(paths)-1 {
				fmt.Fprintf(&out, "\n(output limit reached; %d more file(s) not read)\n", len(paths)-i-1)
			}
			break
		}
	}
	return out.String(), nil
}

// --- list_directory ----------------------------------------------------------

func listDirectory(_ context.Context, b *Box, a args) (string, error) {
	abs, rel, err := b.resolve(a.str("path"))
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", rel)
	}
	depth := a.num("depth", 1, 1, 6)
	all := a.flag("include_ignored")
	gi := loadIgnore(b.Root())
	var out strings.Builder
	if rel == "." {
		out.WriteString("./ (workspace root)\n")
	} else {
		fmt.Fprintf(&out, "%s/\n", rel)
	}
	count, limit := 0, 1000
	var visit func(dir, relDir string, level int)
	visit = func(dir, relDir string, level int) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(ents, func(i, j int) bool {
			if ents[i].IsDir() != ents[j].IsDir() {
				return ents[i].IsDir()
			}
			return ents[i].Name() < ents[j].Name()
		})
		for _, e := range ents {
			if count >= limit {
				return
			}
			name := e.Name()
			r := name
			if relDir != "." {
				r = relDir + "/" + name
			}
			if name == ".DS_Store" || (!all && (skipDirs[name] && e.IsDir() || gi.ignored(r, e.IsDir()))) {
				continue
			}
			count++
			ind := strings.Repeat("  ", level)
			if e.IsDir() {
				n := 0
				if sub, err := os.ReadDir(filepath.Join(dir, name)); err == nil {
					n = len(sub)
				}
				more := ""
				if level >= depth && n > 0 {
					more = ", not expanded: raise depth or list it"
				}
				fmt.Fprintf(&out, "%s%s/  (%s%s)\n", ind, name, plural(n, "entry", "entries"), more)
				if level < depth {
					visit(filepath.Join(dir, name), r, level+1)
				}
			} else if info, err := e.Info(); err == nil {
				fmt.Fprintf(&out, "%s%s  %s\n", ind, name, human(info.Size()))
			}
		}
	}
	visit(abs, rel, 1)
	if count >= limit {
		fmt.Fprintf(&out, "… listing stopped at %d entries; list a subdirectory\n", limit)
	}
	if count == 0 {
		out.WriteString("  (empty)\n")
	}
	return out.String(), nil
}

// --- create_directory / create_file ---------------------------------------

func createDirectory(_ context.Context, b *Box, a args) (string, error) {
	p, err := a.need("path")
	if err != nil {
		return "", err
	}
	abs, rel, err := b.resolve(p)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(abs); err == nil {
		if fi.IsDir() {
			return fmt.Sprintf("%s/ already exists", rel), nil
		}
		return "", fmt.Errorf("%s exists and is a file", rel)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created %s/", rel), nil
}

func writeAtomic(abs string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".sw-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	_ = tmp.Chmod(mode)
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), abs)
}

func createFile(_ context.Context, b *Box, a args) (string, error) {
	p, err := a.need("path")
	if err != nil {
		return "", err
	}
	abs, rel, err := b.resolve(p)
	if err != nil {
		return "", err
	}
	content := a.str("content")
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		if fi.IsDir() {
			return "", fmt.Errorf("%s is a directory", rel)
		}
		if !a.flag("overwrite") {
			return "", fmt.Errorf("%s already exists; use edit_file to change it, or pass overwrite=true to replace it", rel)
		}
		mode = fi.Mode().Perm()
	}
	if len(content) > maxFileEdit {
		return "", errors.New("content is larger than 8 MB")
	}
	if err := writeAtomic(abs, []byte(content), mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %s (%s, %s)", rel, plural(len(splitLines(content)), "line", "lines"), human(int64(len(content)))), nil
}

// --- edit_file -----------------------------------------------------------------

type edit struct {
	oldText, newText string
	all              bool
	start, end       int // line range mode (1-based, inclusive); 0 = not used
}

func parseEdits(a args) ([]edit, error) {
	var es []edit
	if raw, ok := a["edits"].([]any); ok {
		for i, r := range raw {
			m, ok := r.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("edits[%d] must be an object", i)
			}
			ea := args(m)
			es = append(es, edit{oldText: ea.str("old_text"), newText: ea.str("new_text"), all: ea.flag("replace_all"),
				start: ea.num("start_line", 0, 0, 0), end: ea.num("end_line", 0, 0, 0)})
		}
	}
	if a.str("old_text") != "" || a.num("start_line", 0, 0, 0) > 0 {
		es = append(es, edit{oldText: a.str("old_text"), newText: a.str("new_text"), all: a.flag("replace_all"),
			start: a.num("start_line", 0, 0, 0), end: a.num("end_line", 0, 0, 0)})
	}
	if len(es) == 0 {
		return nil, errors.New(`give "old_text" and "new_text" (or "edits": [{old_text, new_text}, …], or start_line/end_line with new_text)`)
	}
	return es, nil
}

// nearest reports where text that almost matches might be, to help the
// model fix an old_text that does not match exactly (often whitespace).
func nearest(content, old string) string {
	first := strings.TrimSpace(strings.SplitN(old, "\n", 2)[0])
	if first == "" {
		return ""
	}
	var hits []string
	for i, l := range splitLines(content) {
		if strings.Contains(strings.TrimSpace(l), first) {
			hits = append(hits, fmt.Sprint(i+1))
			if len(hits) == 5 {
				break
			}
		}
	}
	if len(hits) == 0 {
		return " No line contains its first line either; read_file the file and copy the text exactly."
	}
	return fmt.Sprintf(" Its first line appears (ignoring indentation) at line %s; read those lines and copy the text exactly, including whitespace.", strings.Join(hits, ", "))
}

func applyEdits(content string, es []edit) (string, []string, error) {
	var notes []string
	for i, e := range es {
		label := fmt.Sprintf("edit %d", i+1)
		if e.start > 0 {
			lines := splitLines(content)
			end := e.end
			if end == 0 {
				end = e.start
			}
			if e.start > len(lines)+1 || end < e.start-1 || end > len(lines) {
				return "", nil, fmt.Errorf("%s: lines %d-%d are outside the file (%s)", label, e.start, end, plural(len(lines), "line", "lines"))
			}
			repl := splitLines(e.newText)
			next := append(append(append([]string{}, lines[:e.start-1]...), repl...), lines[end:]...)
			trail := ""
			if strings.HasSuffix(content, "\n") || content == "" {
				trail = "\n"
			}
			content = strings.Join(next, "\n") + trail
			notes = append(notes, fmt.Sprintf("%s: replaced lines %d-%d with %s", label, e.start, end, plural(len(repl), "line", "lines")))
			continue
		}
		if e.oldText == "" {
			return "", nil, fmt.Errorf("%s: old_text is empty; to insert, include a neighbouring line in old_text and new_text", label)
		}
		n := strings.Count(content, e.oldText)
		if n == 0 {
			// tolerate CRLF files and models that send LF
			if strings.Contains(content, "\r\n") {
				if crlf := strings.ReplaceAll(e.oldText, "\n", "\r\n"); strings.Count(content, crlf) > 0 {
					e.oldText, e.newText = crlf, strings.ReplaceAll(e.newText, "\n", "\r\n")
					n = strings.Count(content, crlf)
				}
			}
		}
		switch {
		case n == 0:
			return "", nil, fmt.Errorf("%s: old_text was not found.%s", label, nearest(content, e.oldText))
		case n > 1 && !e.all:
			return "", nil, fmt.Errorf("%s: old_text appears %d times; add surrounding lines to make it unique, or pass replace_all=true", label, n)
		}
		if e.all {
			content = strings.ReplaceAll(content, e.oldText, e.newText)
			notes = append(notes, fmt.Sprintf("%s: replaced %s", label, plural(n, "occurrence", "occurrences")))
		} else {
			at := strings.Index(content, e.oldText)
			line := strings.Count(content[:at], "\n") + 1
			content = content[:at] + e.newText + content[at+len(e.oldText):]
			notes = append(notes, fmt.Sprintf("%s: changed at line %d", label, line))
		}
	}
	return content, notes, nil
}

// diffSummary is a compact line diff (changed region with 2 lines of context).
func diffSummary(before, after string) string {
	a, b := splitLines(before), splitLines(after)
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var out strings.Builder
	lo := max(0, p-2)
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", lo+1, len(a)-s-lo+min(2, s), lo+1, len(b)-s-lo+min(2, s))
	for i := lo; i < p; i++ {
		out.WriteString("  " + a[i] + "\n")
	}
	shown := 0
	for i := p; i < len(a)-s; i++ {
		if shown++; shown > 60 {
			out.WriteString("  …\n")
			break
		}
		out.WriteString("- " + a[i] + "\n")
	}
	shown = 0
	for i := p; i < len(b)-s; i++ {
		if shown++; shown > 60 {
			out.WriteString("  …\n")
			break
		}
		out.WriteString("+ " + b[i] + "\n")
	}
	for i := len(a) - s; i < len(a)-s+min(2, s); i++ {
		out.WriteString("  " + a[i] + "\n")
	}
	return out.String()
}

func editFile(_ context.Context, b *Box, a args) (string, error) {
	p, err := a.need("path")
	if err != nil {
		return "", err
	}
	abs, rel, err := b.resolve(p)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s does not exist; use create_file to make it", rel)
	}
	if fi.IsDir() || fi.Size() > maxFileEdit {
		return "", fmt.Errorf("%s cannot be edited (directory or larger than 8 MB)", rel)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s is a binary file", rel)
	}
	es, err := parseEdits(a)
	if err != nil {
		return "", err
	}
	after, notes, err := applyEdits(string(data), es)
	if err != nil {
		return "", fmt.Errorf("%s: %v (no changes were written)", rel, err)
	}
	if after == string(data) {
		return fmt.Sprintf("%s: no change (new_text equals old_text)", rel), nil
	}
	if !a.flag("dry_run") {
		if err := writeAtomic(abs, []byte(after), fi.Mode().Perm()); err != nil {
			return "", err
		}
	}
	verb := "Edited"
	if a.flag("dry_run") {
		verb = "Dry run, not written:"
	}
	return fmt.Sprintf("%s %s (%s)\n%s\n%s", verb, rel, strings.Join(notes, "; "), strings.Repeat("-", 3), diffSummary(string(data), after)), nil
}

// --- move_path / delete_path ------------------------------------------------

func movePath(_ context.Context, b *Box, a args) (string, error) {
	from, err := a.need("from")
	if err != nil {
		return "", err
	}
	to, err := a.need("to")
	if err != nil {
		return "", err
	}
	fa, fr, err := b.resolve(from)
	if err != nil {
		return "", err
	}
	ta, tr, err := b.resolve(to)
	if err != nil {
		return "", err
	}
	if fr == "." {
		return "", errors.New("cannot move the workspace root")
	}
	if _, err := os.Stat(fa); err != nil {
		return "", fmt.Errorf("%s does not exist", fr)
	}
	if fi, err := os.Stat(ta); err == nil {
		if fi.IsDir() {
			ta, tr = filepath.Join(ta, filepath.Base(fa)), tr+"/"+filepath.Base(fa)
		}
		if _, err := os.Stat(ta); err == nil && !a.flag("overwrite") {
			return "", fmt.Errorf("%s already exists; pass overwrite=true to replace it", tr)
		}
	}
	if err := os.MkdirAll(filepath.Dir(ta), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(fa, ta); err != nil {
		return "", err
	}
	return fmt.Sprintf("Moved %s → %s", fr, strings.TrimPrefix(tr, "./")), nil
}

func deletePath(_ context.Context, b *Box, a args) (string, error) {
	p, err := a.need("path")
	if err != nil {
		return "", err
	}
	abs, rel, err := b.resolve(p)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return "", fmt.Errorf("refusing to delete %s", rel)
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", rel)
	}
	if fi.IsDir() && !a.flag("recursive") {
		if ents, _ := os.ReadDir(abs); len(ents) > 0 {
			return "", fmt.Errorf("%s is a non-empty directory (%s); pass recursive=true to delete it", rel, plural(len(ents), "entry", "entries"))
		}
	}
	// deletes are recoverable: move into the app's trash
	dest := filepath.Join(b.trash, time.Now().Format("20060102-150405.000"), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err == nil {
		if err := os.Rename(abs, dest); err == nil {
			return fmt.Sprintf("Deleted %s (moved to the SpecWright trash: %s)", rel, dest), nil
		}
	}
	if err := os.RemoveAll(abs); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted %s", rel), nil
}
