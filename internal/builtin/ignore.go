package builtin

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Directories never walked into: version control, dependencies, builds.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, "target": true, "build": true, "dist": true,
	".gradle": true, ".idea": true, ".venv": true, "venv": true, "__pycache__": true, ".mypy_cache": true,
	".pytest_cache": true, "bin": true, "obj": true, ".next": true, ".nuxt": true, "vendor": true, "coverage": true,
}

// globRE turns a glob into an anchored regexp over slash paths:
// ** any number of directories, * within one segment, ? one character,
// {a,b} alternatives, [..] classes.
func globRE(g string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth > 0 {
				depth--
				b.WriteString(")")
			} else {
				b.WriteString(`\}`)
			}
		case ',':
			if depth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		case '[':
			j := strings.IndexByte(g[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			cls := g[i+1 : i+j]
			if strings.HasPrefix(cls, "!") {
				cls = "^" + cls[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(cls, `\`, `\\`) + "]")
			i += j
		case '\\':
			if i+1 < len(g) {
				i++
				b.WriteString(regexp.QuoteMeta(string(g[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// matcher matches slash paths against a glob; a glob without "/" matches
// the base name anywhere, like editors do ("*.java").
type matcher struct {
	re       *regexp.Regexp
	baseOnly bool
}

func newMatcher(g string) (*matcher, error) {
	g = strings.TrimPrefix(strings.TrimSpace(g), "./")
	re, err := globRE(g)
	if err != nil {
		return nil, err
	}
	return &matcher{re: re, baseOnly: !strings.Contains(g, "/")}, nil
}

func (m *matcher) match(rel string) bool {
	if m.baseOnly {
		return m.re.MatchString(rel[strings.LastIndexByte(rel, '/')+1:])
	}
	return m.re.MatchString(rel)
}

// gitignore holds the root .gitignore rules (a practical subset: globs,
// trailing "/" for directories, leading "/" to anchor, "!" to re-include).
type gitignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	m      *regexp.Regexp
	dir    bool
	negate bool
	anchor bool
}

var ignoreCache sync.Map // root → *gitignore (refreshed when .gitignore changes)

type cachedIgnore struct {
	mod int64
	gi  *gitignore
}

func loadIgnore(root string) *gitignore {
	p := filepath.Join(root, ".gitignore")
	fi, err := os.Stat(p)
	if err != nil {
		return &gitignore{}
	}
	if c, ok := ignoreCache.Load(root); ok && c.(cachedIgnore).mod == fi.ModTime().UnixNano() {
		return c.(cachedIgnore).gi
	}
	f, err := os.Open(p)
	if err != nil {
		return &gitignore{}
	}
	defer f.Close()
	gi := &gitignore{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(l, "!") {
			r.negate, l = true, l[1:]
		}
		if strings.HasSuffix(l, "/") {
			r.dir, l = true, strings.TrimSuffix(l, "/")
		}
		if strings.HasPrefix(l, "/") {
			r.anchor, l = true, l[1:]
		} else if strings.Contains(l, "/") {
			r.anchor = true
		}
		if !r.anchor {
			l = "**/" + l
		}
		re, err := globRE(l)
		if err != nil {
			continue
		}
		r.m = re
		gi.rules = append(gi.rules, r)
	}
	ignoreCache.Store(root, cachedIgnore{mod: fi.ModTime().UnixNano(), gi: gi})
	return gi
}

// ignored reports whether rel (slash path) is ignored; isDir says whether
// it is a directory.
func (g *gitignore) ignored(rel string, isDir bool) bool {
	out := false
	for _, r := range g.rules {
		if r.dir && !isDir {
			continue
		}
		if r.m.MatchString(rel) {
			out = !r.negate
		}
	}
	return out
}

// walkOpts tune a workspace walk.
type walkOpts struct {
	includeIgnored bool
	hidden         bool
}

// walk visits files under dir (absolute) in lexical order, skipping
// dependency/build folders and .gitignore'd paths. fn gets the workspace
// relative slash path; returning false stops the walk.
func (b *Box) walk(dir string, o walkOpts, fn func(abs, rel string, d os.DirEntry) bool) {
	root := b.Root()
	gi := loadIgnore(root)
	stop := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if stop {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if path == dir {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] && !o.includeIgnored {
				return filepath.SkipDir
			}
			if !o.hidden && strings.HasPrefix(name, ".") && name != ".codegen" && name != ".specify" && name != ".github" {
				return filepath.SkipDir
			}
			if !o.includeIgnored && gi.ignored(rel, true) {
				return filepath.SkipDir
			}
			if !fn(path, rel, d) {
				stop = true
				return filepath.SkipAll
			}
			return nil
		}
		if name == ".DS_Store" || (!o.includeIgnored && gi.ignored(rel, false)) {
			return nil
		}
		if !fn(path, rel, d) {
			stop = true
			return filepath.SkipAll
		}
		return nil
	})
}
