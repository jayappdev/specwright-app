package builtin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// patterns compiled once (not on every call)
var (
	identifierRE = regexp.MustCompile(`^[\p{L}_$][\p{L}\p{N}_$]*$`) // a symbol name
)

const maxSearchFile = 4 << 20 // files above this are not searched

// --- file_search ---------------------------------------------------------------

func fileSearch(ctx context.Context, b *Box, a args) (string, error) {
	pat, err := a.need("pattern")
	if err != nil {
		return "", err
	}
	m, err := newMatcher(pat)
	if err != nil {
		return "", fmt.Errorf("bad glob %q: %v", pat, err)
	}
	base, baseRel, err := b.resolve(a.str("path"))
	if err != nil {
		return "", err
	}
	limit := a.num("max_results", 200, 1, 5000)
	var hits []string
	total := 0
	b.walk(base, walkOpts{includeIgnored: a.flag("include_ignored"), hidden: true}, func(_, rel string, d os.DirEntry) bool {
		if ctx.Err() != nil {
			return false
		}
		if d.IsDir() {
			return true
		}
		if m.match(rel) || (baseRel != "." && m.match(strings.TrimPrefix(rel, baseRel+"/"))) {
			total++
			if len(hits) < limit {
				hits = append(hits, rel)
			}
		}
		return true
	})
	if len(hits) == 0 {
		return fmt.Sprintf("No files match %q%s.", pat, where(baseRel)), nil
	}
	sort.Strings(hits)
	out := fmt.Sprintf("%s match %q%s:\n%s\n", plural(total, "file", "files"), pat, where(baseRel), strings.Join(hits, "\n"))
	if total > len(hits) {
		out += fmt.Sprintf("… %d more not shown; narrow the pattern or raise max_results\n", total-len(hits))
	}
	return out, nil
}

func where(rel string) string {
	if rel == "." || rel == "" {
		return ""
	}
	return " under " + rel
}

// --- text_search -----------------------------------------------------------------

type hit struct {
	line int
	text string
	ctx  []ctxLine
}

type ctxLine struct {
	line int
	text string
}

type fileHits struct {
	rel  string
	hits []hit
}

// searchFiles runs re over every candidate file in parallel.
func (b *Box) searchFiles(ctx context.Context, base string, include, exclude []*matcher, all bool, re *regexp.Regexp, context int, maxTotal, maxPerFile int) ([]fileHits, int, int, bool) {
	type job struct{ abs, rel string }
	jobs := make(chan job, 256)
	var mu sync.Mutex
	var results []fileHits
	var total, files atomic.Int64
	var truncated atomic.Bool
	workers := max(2, runtime.NumCPU())
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil || truncated.Load() {
					continue
				}
				fh := scanFile(j.abs, re, context, maxPerFile)
				if len(fh) == 0 {
					continue
				}
				if n := total.Add(int64(len(fh))); n > int64(maxTotal) {
					truncated.Store(true)
				}
				mu.Lock()
				results = append(results, fileHits{rel: j.rel, hits: fh})
				mu.Unlock()
			}
		}()
	}
	b.walk(base, walkOpts{includeIgnored: all}, func(abs, rel string, d os.DirEntry) bool {
		if ctx.Err() != nil || truncated.Load() {
			return false
		}
		if d.IsDir() {
			return true
		}
		if len(include) > 0 && !anyMatch(include, rel) || anyMatch(exclude, rel) {
			return true
		}
		files.Add(1)
		jobs <- job{abs, rel}
		return true
	})
	close(jobs)
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].rel < results[j].rel })
	return results, int(total.Load()), int(files.Load()), truncated.Load()
}

func anyMatch(ms []*matcher, rel string) bool {
	for _, m := range ms {
		if m.match(rel) {
			return true
		}
	}
	return false
}

func scanFile(abs string, re *regexp.Regexp, context, maxPerFile int) []hit {
	fi, err := os.Stat(abs)
	if err != nil || fi.Size() > maxSearchFile || fi.Size() == 0 {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil || isBinary(data) {
		return nil
	}
	if !re.Match(data) { // fast reject before splitting into lines
		return nil
	}
	var lines []string
	if context > 0 {
		lines = splitLines(string(data))
	}
	var out []hit
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	n := 0
	for sc.Scan() {
		n++
		l := sc.Text()
		if !re.MatchString(l) {
			continue
		}
		h := hit{line: n, text: clip(l, 300)}
		if context > 0 {
			for k := max(1, n-context); k <= min(len(lines), n+context); k++ {
				if k != n {
					h.ctx = append(h.ctx, ctxLine{k, clip(lines[k-1], 300)})
				}
			}
		}
		out = append(out, h)
		if len(out) >= maxPerFile {
			break
		}
	}
	return out
}

func clip(s string, n int) string {
	s = strings.TrimRight(s, "\r")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func compileQuery(a args, q string) (*regexp.Regexp, error) {
	expr := q
	if !a.flag("is_regex") {
		expr = regexp.QuoteMeta(q)
	}
	if a.flag("whole_word") {
		expr = `\b(?:` + expr + `)\b`
	}
	if !a.flag("case_sensitive") && !hasUpper(q) || a.flag("ignore_case") {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("bad regular expression: %v (RE2 syntax; set is_regex=false for a literal search)", err)
	}
	return re, nil
}

// smart case: a query with an upper-case letter is case sensitive
func hasUpper(s string) bool { return strings.ToLower(s) != s }

func globs(a args, k string) ([]*matcher, error) {
	var out []*matcher
	for _, g := range a.strs(k) {
		m, err := newMatcher(g)
		if err != nil {
			return nil, fmt.Errorf("bad glob %q in %s", g, k)
		}
		out = append(out, m)
	}
	return out, nil
}

func textSearch(ctx context.Context, b *Box, a args) (string, error) {
	q, err := a.need("query")
	if err != nil {
		return "", err
	}
	re, err := compileQuery(a, q)
	if err != nil {
		return "", err
	}
	base, baseRel, err := b.resolve(a.str("path"))
	if err != nil {
		return "", err
	}
	inc, err := globs(a, "include")
	if err != nil {
		return "", err
	}
	exc, err := globs(a, "exclude")
	if err != nil {
		return "", err
	}
	res, total, files, trunc := b.searchFiles(ctx, base, inc, exc, a.flag("include_ignored"), re, a.num("context_lines", 0, 0, 5),
		a.num("max_results", 200, 1, 2000), a.num("max_per_file", 50, 1, 500))
	if total == 0 {
		return fmt.Sprintf("No matches for %q%s (%s searched).", q, where(baseRel), plural(files, "file", "files")), nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s in %s for %q%s (%s searched):\n", plural(total, "match", "matches"), plural(len(res), "file", "files"), q, where(baseRel), plural(files, "file", "files"))
	for _, f := range res {
		fmt.Fprintf(&out, "\n%s\n", f.rel)
		for _, h := range f.hits {
			for _, c := range h.ctx {
				if c.line < h.line {
					fmt.Fprintf(&out, "  %d- %s\n", c.line, c.text)
				}
			}
			fmt.Fprintf(&out, "  %d: %s\n", h.line, h.text)
			for _, c := range h.ctx {
				if c.line > h.line {
					fmt.Fprintf(&out, "  %d- %s\n", c.line, c.text)
				}
			}
		}
	}
	if trunc {
		out.WriteString("\n… results truncated; narrow the query, add include globs, or raise max_results\n")
	}
	return out.String(), nil
}

// --- find_usages ------------------------------------------------------------------

// definition patterns across common languages (Go, Java/Kotlin/C#, TS/JS, Python, Rust…)
var defRE = `(?:\b(?:func|function|def|class|interface|type|struct|enum|record|trait|impl|object|fun|fn|module|namespace|const|let|var|val)\b[^\n]*\b%[1]s\b|\b%[1]s\s*(?::=|=\s*(?:function|\(|async))|\b(?:public|private|protected|internal|static|final|abstract|override)\b[^\n=;]*\b%[1]s\s*\()`

func findUsages(ctx context.Context, b *Box, a args) (string, error) {
	sym, err := a.need("symbol")
	if err != nil {
		return "", err
	}
	if !identifierRE.MatchString(sym) {
		return "", errors.New("symbol must be a single identifier, e.g. PlaceOrderService or placeOrder")
	}
	q := regexp.QuoteMeta(sym)
	re := regexp.MustCompile(`\b` + q + `\b`)
	def := regexp.MustCompile(fmt.Sprintf(defRE, q))
	base, baseRel, err := b.resolve(a.str("path"))
	if err != nil {
		return "", err
	}
	inc, err := globs(a, "include")
	if err != nil {
		return "", err
	}
	res, total, files, trunc := b.searchFiles(ctx, base, inc, nil, false, re, 0, a.num("max_results", 300, 1, 3000), 100)
	if total == 0 {
		return fmt.Sprintf("No usages of %s%s (%s searched).", sym, where(baseRel), plural(files, "file", "files")), nil
	}
	var defs, refs strings.Builder
	nd, nr := 0, 0
	for _, f := range res {
		for _, h := range f.hits {
			l := fmt.Sprintf("  %s:%d: %s\n", f.rel, h.line, strings.TrimSpace(h.text))
			trim := strings.TrimSpace(h.text)
			if strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "from ") || strings.HasPrefix(trim, "#include") {
				refs.WriteString(l)
				nr++
			} else if def.MatchString(h.text) {
				defs.WriteString(l)
				nd++
			} else {
				refs.WriteString(l)
				nr++
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s: %s, %s in %s\n", sym, plural(nd, "likely definition", "likely definitions"), plural(nr, "reference", "references"), plural(len(res), "file", "files"))
	if nd > 0 {
		out.WriteString("\nDefinitions\n" + defs.String())
	}
	if nr > 0 {
		out.WriteString("\nReferences\n" + refs.String())
	}
	if trunc {
		out.WriteString("\n… truncated; narrow with path or include\n")
	}
	out.WriteString("\n(Found by whole-word text search with definition heuristics, not a compiler; check before large refactors.)\n")
	return out.String(), nil
}
