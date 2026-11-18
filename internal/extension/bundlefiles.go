package extension

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Files that travel in a tool file: a tool folder's own files (helpers,
// templates, data, lock files) and Copilot extension folders. Installed
// packages, caches and build output stay behind: they are installed again
// from the packages list when the tool is approved.

const (
	maxBundleFile  = 10 << 20
	maxBundleTotal = 40 << 20
	maxBundleFiles = 1000
)

// skipInFolder are folders and files never exported from a tool folder.
var skipInFolder = map[string]bool{"node_modules": true, ".venv": true, "venv": true, "__pycache__": true, "bin": true, "lib": true,
	"target": true, "build": true, "dist": true, ".git": true, ".gradle": true, ".DS_Store": true, marker: true}

// BundledExt is a Copilot extension folder in a tool file.
type BundledExt struct {
	Name  string            `json:"name"`
	Desc  string            `json:"desc,omitempty"`
	Tools []string          `json:"tools,omitempty"` // the tools it adds (for the review)
	Files map[string]string `json:"files"`           // relative path → base64
}

// folderFiles reads the files of dir (relative path → base64), leaving out
// skipInFolder and the files named in except.
func folderFiles(dir string, except ...string) (map[string]string, error) {
	out := map[string]string{}
	total, n := 0, 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != dir && skipInFolder[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil // symlinks and devices are not exported
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		for _, x := range except {
			if rel == x {
				return nil
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if len(b) > maxBundleFile {
			return fmt.Errorf("%s is larger than %d MB", rel, maxBundleFile>>20)
		}
		if n++; n > maxBundleFiles {
			return fmt.Errorf("more than %d files", maxBundleFiles)
		}
		if total += len(b); total > maxBundleTotal {
			return fmt.Errorf("the files are larger than %d MB in all", maxBundleTotal>>20)
		}
		out[rel] = base64.StdEncoding.EncodeToString(b)
		return nil
	})
	return out, err
}

// safeRel checks a relative path from a tool file: no absolute paths, no
// "..", no backslashes, nothing inside .git.
func safeRel(rel string) error {
	switch {
	case rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.Contains(rel, `\`) || strings.Contains(rel, ":"):
		return fmt.Errorf("%q is not a relative path", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || part == ".git" {
			return fmt.Errorf("%q is not allowed", rel)
		}
	}
	return nil
}

// writeFiles writes base64 files under dir, after checking every path.
func writeFiles(dir string, files map[string]string) error {
	total := 0
	for rel, enc := range files {
		if err := safeRel(rel); err != nil {
			return err
		}
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return fmt.Errorf("%s: %v", rel, err)
		}
		if total += len(b); len(b) > maxBundleFile || total > maxBundleTotal {
			return errors.New("the files are too large")
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, b, mode); err != nil {
			return err
		}
	}
	return nil
}

// checkFiles validates the paths and sizes of files from a tool file.
func checkFiles(files map[string]string) error {
	if len(files) > maxBundleFiles {
		return fmt.Errorf("more than %d files", maxBundleFiles)
	}
	total := 0
	for rel, enc := range files {
		if err := safeRel(rel); err != nil {
			return err
		}
		total += base64.StdEncoding.DecodedLen(len(enc))
	}
	if total > maxBundleTotal+maxBundleTotal/10 {
		return fmt.Errorf("the files are larger than %d MB in all", maxBundleTotal>>20)
	}
	return nil
}

// RestoreToolFiles writes a bundled tool's own files into its folder (dir,
// relative to root) after the tool was saved; the entry script is not
// overwritten (Save wrote it from the script).
func RestoreToolFiles(root, dir string, t BundledTool) error {
	if dir == "" || len(t.Files) == 0 {
		return nil
	}
	files := map[string]string{}
	for rel, b := range t.Files {
		if rel != EntryFile(t.Lang) {
			files[rel] = b
		}
	}
	return writeFiles(filepath.Join(root, filepath.FromSlash(dir)), files)
}

// OutsideFiles lists files of the project that e's command uses but that are
// not part of the tool (not its script or folder): they do not go into an
// export, so the tool may fail where they are missing.
func OutsideFiles(root string, e Extension) []string {
	var out []string
	own := func(p string) bool {
		if e.ScriptPath != "" && p == e.ScriptPath {
			return true
		}
		return e.Dir != "" && (p == e.Dir || strings.HasPrefix(p, e.Dir+"/"))
	}
	for _, w := range SplitWords(e.Command) {
		if strings.Contains(w, "{{") || strings.HasPrefix(w, "-") || !strings.ContainsAny(w, "/.") {
			continue
		}
		p := filepath.ToSlash(filepath.Clean(w))
		if filepath.IsAbs(p) || strings.HasPrefix(p, "..") || own(p) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// BundleCopilotExt packs a Copilot extension folder (node_modules left out).
func BundleCopilotExt(x CopilotExt) (BundledExt, error) {
	files := map[string]string{}
	total := 0
	err := walkCopilotExt(x.Dir, func(rel string, info fs.FileInfo) error {
		if info.Size() > maxBundleFile {
			return fmt.Errorf("%s is larger than %d MB", rel, maxBundleFile>>20)
		}
		b, err := os.ReadFile(filepath.Join(x.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil
		}
		if total += len(b); total > maxBundleTotal {
			return fmt.Errorf("the extension is larger than %d MB", maxBundleTotal>>20)
		}
		files[rel] = base64.StdEncoding.EncodeToString(b)
		return nil
	})
	if err != nil {
		return BundledExt{}, fmt.Errorf("%s: %v", x.Name, err)
	}
	var tools []string
	for _, t := range x.Tools {
		tools = append(tools, t.Name)
	}
	return BundledExt{Name: x.Name, Desc: x.Desc, Tools: tools, Files: files}, nil
}

// WriteCopilotExt writes a bundled extension to root/.github/extensions/<name>:
// assembled in a temporary folder and moved into place. An existing folder is
// handed to replaced (which moves it to the trash) when replace is set.
func WriteCopilotExt(root string, x BundledExt, replace bool, replaced func(abs string) error) (string, error) {
	if err := ValidCopilotExtName(x.Name); err != nil {
		return "", err
	}
	hasEntry := false
	for _, e := range copilotEntries {
		if _, ok := x.Files[e]; ok {
			hasEntry = true
		}
	}
	if !hasEntry {
		return "", fmt.Errorf("%s has no extension.mjs, .cjs or .js", x.Name)
	}
	parent := filepath.Join(root, filepath.FromSlash(ProjectCopilotExtDir))
	dst := filepath.Join(parent, x.Name)
	if _, err := os.Lstat(dst); err == nil {
		if !replace {
			return "", fmt.Errorf("%s/%s already exists in this project", ProjectCopilotExtDir, x.Name)
		}
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, "."+x.Name+"-import-")
	if err != nil {
		return "", err
	}
	if err := writeFiles(tmp, x.Files); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("%s: %v", x.Name, err)
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := replaced(dst); err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return ProjectCopilotExtDir + "/" + x.Name, nil
}
