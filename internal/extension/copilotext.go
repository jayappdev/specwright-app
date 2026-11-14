package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Copilot CLI extensions: a folder holding extension.mjs (or .cjs/.js)
// that calls joinSession() from @github/copilot-sdk with the tools it adds.
// Your own live in ~/.copilot/extensions/<name> ($COPILOT_HOME/extensions);
// a project's live in .github/extensions/<name>. SpecWright lists both and
// copies one of yours into the project, so everyone working on it (and the
// app's own Copilot runs) gets the same tools.

// ProjectCopilotExtDir is where a project's Copilot extensions live.
const ProjectCopilotExtDir = ".github/extensions"

var copilotEntries = []string{"extension.mjs", "extension.cjs", "extension.js"}

// limits for copying an extension folder (node_modules is never copied)
const (
	maxCopilotExtFile  = 25 << 20 // room for a bundled driver or library jar
	maxCopilotExtTotal = 50 << 20
	maxCopilotExtFiles = 2000
)

// CopilotExt is one Copilot CLI extension.
type CopilotExt struct {
	Name    string            `json:"name"`
	Source  string            `json:"source"` // user | project
	Dir     string            `json:"dir"`    // absolute folder
	Entry   string            `json:"entry"`  // extension.mjs …
	Desc    string            `json:"desc"`
	Readme  string            `json:"readme,omitempty"`
	Tools   []CopilotExtTool  `json:"tools"`
	Files   []string          `json:"files"` // relative, node_modules left out
	Size    int64             `json:"size"`
	Deps    map[string]string `json:"deps"`
	Modules bool              `json:"modules"` // node_modules present
	// for a user extension: is a copy in this project, and is it the same?
	InProject   bool `json:"inProject"`
	ProjectSame bool `json:"projectSame"`
	// for a project extension: does one of yours have the same name?
	FromUser bool `json:"fromUser"`
}

// CopilotExtTool is a tool an extension adds (read from its source).
type CopilotExtTool struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// CopilotHome is the Copilot CLI's folder ($COPILOT_HOME or ~/.copilot).
func CopilotHome() string {
	if h := os.Getenv("COPILOT_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".copilot")
}

// UserCopilotExtDir is where your own Copilot extensions live.
func UserCopilotExtDir() string {
	if h := CopilotHome(); h != "" {
		return filepath.Join(h, "extensions")
	}
	return ""
}

// ListCopilotExts lists your Copilot extensions and the project's (root
// may be ""), each marked with whether the other side has it.
func ListCopilotExts(root string) []CopilotExt {
	user := scanCopilotExts(UserCopilotExtDir(), "user")
	var proj []CopilotExt
	if root != "" {
		proj = scanCopilotExts(filepath.Join(root, filepath.FromSlash(ProjectCopilotExtDir)), "project")
	}
	byName := map[string]*CopilotExt{}
	for i := range proj {
		byName[proj[i].Name] = &proj[i]
	}
	for i := range user {
		if p := byName[user[i].Name]; p != nil {
			user[i].InProject = true
			user[i].ProjectSame = treeHash(user[i].Dir) == treeHash(p.Dir)
			p.FromUser = true
		}
	}
	return append(user, proj...)
}

func scanCopilotExts(dir, source string) []CopilotExt {
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []CopilotExt
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if x, ok := readCopilotExt(filepath.Join(dir, e.Name()), source); ok {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func readCopilotExt(dir, source string) (CopilotExt, bool) {
	x := CopilotExt{Name: filepath.Base(dir), Source: source, Dir: dir, Deps: map[string]string{}}
	for _, n := range copilotEntries {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.Mode().IsRegular() {
			x.Entry = n
			break
		}
	}
	if x.Entry == "" {
		return x, false
	}
	if src, err := readSmall(filepath.Join(dir, x.Entry), 1<<20); err == nil {
		x.Tools = parseCopilotTools(src)
	}
	var pkg struct {
		Description  string            `json:"description"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if b, err := readSmall(filepath.Join(dir, "package.json"), 1<<20); err == nil && json.Unmarshal([]byte(b), &pkg) == nil {
		x.Desc = pkg.Description
		for k, v := range pkg.Dependencies {
			x.Deps[k] = v
		}
	}
	if r, err := readSmall(filepath.Join(dir, "README.md"), 256<<10); err == nil {
		x.Readme = r
		if x.Desc == "" {
			x.Desc = firstParagraph(r)
		}
	}
	if x.Desc == "" && len(x.Tools) > 0 {
		x.Desc = x.Tools[0].Desc
	}
	if st, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil && st.IsDir() {
		x.Modules = true
	}
	_ = walkCopilotExt(dir, func(rel string, info fs.FileInfo) error {
		x.Files = append(x.Files, rel)
		x.Size += info.Size()
		return nil
	})
	return x, true
}

func readSmall(p string, max int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max))
	return string(b), err
}

func firstParagraph(md string) string {
	var para []string
	for _, l := range strings.Split(md, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "#") && len(para) == 0:
			continue
		case t == "":
			if len(para) > 0 {
				return clipText(strings.Join(para, " "), 300)
			}
		default:
			para = append(para, t)
		}
	}
	return clipText(strings.Join(para, " "), 300)
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var (
	cpExtToolRE = regexp.MustCompile(`name\s*:\s*["'` + "`" + `]([\w.\-]+)["'` + "`" + `]\s*,\s*description\s*:\s*((?:["'` + "`" + `](?:[^"'` + "`" + `\\]|\\.)*["'` + "`" + `]\s*\+?\s*)+)`)
	cpExtStrRE  = regexp.MustCompile(`["'` + "`" + `]((?:[^"'` + "`" + `\\]|\\.)*)["'` + "`" + `]`)
)

// parseCopilotTools finds `name: "…", description: "…" (+ "…")` pairs in an
// extension's source: a best-effort read for display, nothing is run.
func parseCopilotTools(src string) []CopilotExtTool {
	var out []CopilotExtTool
	seen := map[string]bool{}
	for _, m := range cpExtToolRE.FindAllStringSubmatch(src, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		var d strings.Builder
		for _, s := range cpExtStrRE.FindAllStringSubmatch(m[2], -1) {
			d.WriteString(s[1])
		}
		out = append(out, CopilotExtTool{Name: m[1], Desc: strings.TrimSpace(d.String())})
	}
	return out
}

// walkCopilotExt visits the regular files of an extension folder that are
// copied: node_modules, .git and symlinks are left out.
func walkCopilotExt(dir string, f func(rel string, info fs.FileInfo) error) error {
	n := 0
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() || d.Name() == marker {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if n++; n > maxCopilotExtFiles {
			return fmt.Errorf("more than %d files", maxCopilotExtFiles)
		}
		rel, _ := filepath.Rel(dir, p)
		return f(filepath.ToSlash(rel), info)
	})
}

// treeHash fingerprints the copied files of an extension folder.
func treeHash(dir string) string {
	h := sha256.New()
	_ = walkCopilotExt(dir, func(rel string, info fs.FileInfo) error {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// ValidCopilotExtName rejects names that are not a single folder name.
func ValidCopilotExtName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("%q is not a valid extension name", name)
	}
	return nil
}

// CopyCopilotExt copies your extension name into root/.github/extensions,
// without node_modules. The copy is assembled in a temporary folder and
// moved into place; an existing project copy is handed to replaced (which
// moves it to the trash) first, and only when overwrite is set.
func CopyCopilotExt(root, name string, overwrite bool, replaced func(abs string) error) (string, error) {
	if err := ValidCopilotExtName(name); err != nil {
		return "", err
	}
	src := filepath.Join(UserCopilotExtDir(), name)
	if _, ok := readCopilotExt(src, "user"); !ok {
		return "", fmt.Errorf("no Copilot extension named %q in %s", name, UserCopilotExtDir())
	}
	parent := filepath.Join(root, filepath.FromSlash(ProjectCopilotExtDir))
	dst := filepath.Join(parent, name)
	if _, err := os.Lstat(dst); err == nil {
		if !overwrite {
			return "", fmt.Errorf("%s already exists in this project", filepath.ToSlash(filepath.Join(ProjectCopilotExtDir, name)))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, "."+name+"-copy-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	var total int64
	err = walkCopilotExt(src, func(rel string, info fs.FileInfo) error {
		if info.Size() > maxCopilotExtFile {
			return fmt.Errorf("%s is larger than %d MB", rel, maxCopilotExtFile>>20)
		}
		if total += info.Size(); total > maxCopilotExtTotal {
			return fmt.Errorf("the extension is larger than %d MB (node_modules excluded)", maxCopilotExtTotal>>20)
		}
		to := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return copyFile(filepath.Join(src, filepath.FromSlash(rel)), to, info.Mode().Perm())
	})
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := replaced(dst); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// HasProjectCopilotExts reports whether root has any Copilot extensions.
func HasProjectCopilotExts(root string) bool {
	return root != "" && len(scanCopilotExts(filepath.Join(root, filepath.FromSlash(ProjectCopilotExtDir)), "project")) > 0
}

// IgnoreCopilotExtPackages makes sure the project's .gitignore keeps
// installed packages of its Copilot extensions out of git. It reports
// whether it added the lines.
func IgnoreCopilotExtPackages(root string) (bool, error) {
	p := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	have := string(b)
	lines := []string{ProjectCopilotExtDir + "/*/node_modules/", ProjectCopilotExtDir + "/*/" + marker}
	var add []string
	for _, l := range lines {
		if !strings.Contains(have, l) {
			add = append(add, l)
		}
	}
	if len(add) == 0 {
		return false, nil
	}
	if have != "" {
		if !strings.HasSuffix(have, "\n") {
			have += "\n"
		}
		have += "\n"
	}
	have += "# Copilot extension packages (installed by SpecWright)\n" + strings.Join(add, "\n") + "\n"
	return true, os.WriteFile(p, []byte(have), 0o644)
}
