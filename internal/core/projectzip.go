package core

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The finished project can be downloaded as one zip: the Spec Kit project
// folder (constitution, specs, plans, tasks and the code), with build
// output, dependency folders and version control left out. With context,
// the SpecWright skills, rules and diagrams the project uses go in too, at
// the same place relative to the project, so its links keep working after
// unzipping.

// ZipResult describes a written zip.
type ZipResult struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"` // suggested file name
	Files   int      `json:"files"`
	Bytes   int64    `json:"bytes"`   // uncompressed
	Skipped []string `json:"skipped"` // files left out for size
}

// zipSkipDirs are folders never zipped.
var zipSkipDirs = map[string]bool{".git": true, "node_modules": true, "target": true, "build": true, "dist": true, "out": true, ".gradle": true,
	".idea": true, "__pycache__": true, ".venv": true, "venv": true, ".pytest_cache": true, ".mypy_cache": true, "bin": true, "obj": true, ".next": true, "coverage": true}

// zipSkipCodegen are SpecWright's own working folders (history, caches).
var zipSkipCodegen = []string{".codegen/runs", ".codegen/cache", ".codegen/archive", ".codegen/attachments", ".codegen/tools/project", ".codegen/tools/global"}

const (
	zipMaxFile  = 50 << 20
	zipMaxTotal = 1 << 30
)

// ProjectZipName is the suggested file name for the project's zip.
func (h *Hub) ProjectZipName() string {
	name := h.specRel()
	if name == "" {
		name = filepath.Base(h.Root())
	}
	return name + "-" + time.Now().Format("20060102-1504") + ".zip"
}

// WriteProjectZip writes the project as a zip to w.
func (h *Hub) WriteProjectZip(w io.Writer, withContext bool) (ZipResult, error) {
	root, err := h.need()
	if err != nil {
		return ZipResult{}, err
	}
	project := h.specRel()
	res := ZipResult{Name: h.ProjectZipName()}
	zw := zip.NewWriter(w)
	seen := map[string]bool{}
	add := func(rel string) error {
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		abs := filepath.Join(root, filepath.FromSlash(rel))
		fi, err := os.Lstat(abs)
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if fi.Size() > zipMaxFile || res.Bytes+fi.Size() > zipMaxTotal {
			res.Skipped = append(res.Skipped, rel)
			return nil
		}
		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		hdr.Name, hdr.Method = rel, zip.Deflate
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer f.Close()
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		n, err := io.Copy(dst, f)
		res.Files++
		res.Bytes += n
		return err
	}
	// walk adds a folder (workspace-relative) with the usual exclusions
	walk := func(dir string) error {
		return filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if p != filepath.Join(root, filepath.FromSlash(dir)) && zipSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				for _, s := range zipSkipCodegen {
					if rel == s {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if d.Name() == ".DS_Store" || strings.HasSuffix(rel, ".specify/feature.json") {
				return nil
			}
			return add(rel)
		})
	}
	if project == "" {
		err = walk(".")
	} else {
		err = walk(project)
		if err == nil && withContext {
			err = h.zipContext(walk, add)
		}
	}
	if err != nil {
		zw.Close()
		return res, err
	}
	sort.Strings(res.Skipped)
	return res, zw.Close()
}

// zipContext adds the selected libraries, the rules and the diagrams.
func (h *Hub) zipContext(walk func(string) error, add func(string) error) error {
	root := h.Root()
	h.mu.RLock()
	prof := h.profile
	h.mu.RUnlock()
	if prof == nil {
		return nil
	}
	sel := map[string]bool{}
	for _, id := range append(append([]string{}, prof.Global...), prof.Project...) {
		sel[id] = true
	}
	for _, l := range h.libsFor(root, prof) {
		if !sel[l.ID] {
			continue
		}
		var err error
		if l.Dir != "" {
			err = walk(l.Dir)
		} else {
			err = add(l.Path)
		}
		if err != nil {
			return err
		}
	}
	if err := walk(".codegen/rules"); err != nil {
		return err
	}
	for _, d := range prof.Diagrams {
		if d != "" && !strings.HasPrefix(path.Clean(d), "..") {
			if err := add(d); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExportProjectZip writes the project's zip to dest.
func (h *Hub) ExportProjectZip(dest string, withContext bool) (ZipResult, error) {
	if dest == "" {
		return ZipResult{}, errors.New("no file chosen")
	}
	if !strings.HasSuffix(strings.ToLower(dest), ".zip") {
		dest += ".zip"
	}
	root := h.Root()
	if root != "" {
		if rel, err := filepath.Rel(root, dest); err == nil && !strings.HasPrefix(rel, "..") {
			return ZipResult{}, fmt.Errorf("save the zip outside the project folder")
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".specwright-zip-*")
	if err != nil {
		return ZipResult{}, err
	}
	res, err := h.WriteProjectZip(tmp, withContext)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return res, err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return res, err
	}
	res.Path = dest
	return res, nil
}
