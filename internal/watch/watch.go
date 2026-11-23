// Package watch polls a workspace for file changes. It uses only the
// standard library (no fsnotify), which is plenty for agent-sized edits:
// a scan every second, with mtime+size to skip unchanged files and a
// line-multiset diff for the +added/−removed counts shown in the UI.
package watch

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Change is one file created, modified or deleted.
type Change struct {
	Path    string `json:"path"` // slash-separated, relative to the root
	Op      string `json:"op"`   // created | modified | deleted
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Skip lists directory names never descended into.
var Skip = map[string]bool{
	".git": true, "node_modules": true, "target": true, "build": true, "dist": true,
	".gradle": true, ".idea": true, ".venv": true, "__pycache__": true, "bin": true, "obj": true,
}

const maxTrack = 1 << 20 // files above 1 MiB are tracked by size only

type entry struct {
	mod   time.Time
	size  int64
	lines map[string]int
}

// Watcher snapshots a tree and reports differences.
type Watcher struct {
	root     string
	interval time.Duration
	mu       sync.Mutex
	snap     map[string]entry
}

// New creates a watcher for root.
func New(root string, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = time.Second
	}
	return &Watcher{root: root, interval: interval}
}

// Root returns the watched directory.
func (w *Watcher) Root() string { return w.root }

// Run scans until ctx ends, calling emit for each batch of changes.
func (w *Watcher) Run(ctx context.Context, emit func([]Change)) {
	w.mu.Lock()
	if w.snap == nil {
		w.snap = w.scan(nil)
	}
	w.mu.Unlock()
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ch := w.Poll(); len(ch) > 0 {
				emit(ch)
			}
		}
	}
}

// Poll rescans now and returns changes since the last scan.
func (w *Watcher) Poll() []Change {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.snap == nil {
		w.snap = w.scan(nil)
		return nil
	}
	next := w.scan(w.snap)
	var out []Change
	for p, n := range next {
		o, ok := w.snap[p]
		switch {
		case !ok:
			out = append(out, Change{Path: p, Op: "created", Added: total(n.lines)})
		case o.mod != n.mod || o.size != n.size:
			a, r := diff(o.lines, n.lines)
			if a == 0 && r == 0 && o.size == n.size {
				continue
			}
			out = append(out, Change{Path: p, Op: "modified", Added: a, Removed: r})
		}
	}
	for p, o := range w.snap {
		if _, ok := next[p]; !ok {
			out = append(out, Change{Path: p, Op: "deleted", Removed: total(o.lines)})
		}
	}
	w.snap = next
	return out
}

func (w *Watcher) scan(prev map[string]entry) map[string]entry {
	out := map[string]entry{}
	_ = filepath.WalkDir(w.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != w.root && (Skip[name] || name == "history" || ((name == "cache" || name == "runs") && filepath.Base(filepath.Dir(path)) == ".codegen")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") && name != ".gitignore" && !strings.Contains(path, ".specify") && !strings.Contains(path, ".codegen") {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(w.root, path)
		rel = filepath.ToSlash(rel)
		if p, ok := prev[rel]; ok && p.mod.Equal(info.ModTime()) && p.size == info.Size() {
			out[rel] = p
			return nil
		}
		e := entry{mod: info.ModTime(), size: info.Size()}
		if info.Size() <= maxTrack {
			if b, err := os.ReadFile(path); err == nil && !bytes.Contains(b[:min(len(b), 4096)], []byte{0}) {
				e.lines = lineSet(b)
			}
		}
		out[rel] = e
		return nil
	})
	return out
}

func lineSet(b []byte) map[string]int {
	m := map[string]int{}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		m[l]++
	}
	if len(b) == 0 {
		return map[string]int{}
	}
	return m
}

func total(m map[string]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

func diff(a, b map[string]int) (added, removed int) {
	for l, c := range b {
		if d := c - a[l]; d > 0 {
			added += d
		}
	}
	for l, c := range a {
		if d := c - b[l]; d > 0 {
			removed += d
		}
	}
	return
}

// ListFiles returns every tracked file path under root (sorted by the walk).
func ListFiles(root string, limit int) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (Skip[d.Name()] || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".DS_Store" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, filepath.ToSlash(rel))
		if limit > 0 && len(out) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

// Stamp is what a scan knows of a file, enough to tell a later scan what
// changed (kept between app starts: file triggers see changes made while
// the app was closed).
type Stamp struct {
	Mod  int64 `json:"m"` // modification time, Unix nanoseconds
	Size int64 `json:"s"`
}

// Stamps is the last scan (nil before the first).
func (w *Watcher) Stamps() map[string]Stamp {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.snap == nil {
		return nil
	}
	out := make(map[string]Stamp, len(w.snap))
	for p, e := range w.snap {
		out[p] = Stamp{Mod: e.mod.UnixNano(), Size: e.size}
	}
	return out
}

// DiffStamps lists what changed from an older scan to a newer one.
func DiffStamps(old, cur map[string]Stamp) []Change {
	var out []Change
	for p, n := range cur {
		o, ok := old[p]
		switch {
		case !ok:
			out = append(out, Change{Path: p, Op: "created"})
		case o != n:
			out = append(out, Change{Path: p, Op: "modified"})
		}
	}
	for p := range old {
		if _, ok := cur[p]; !ok {
			out = append(out, Change{Path: p, Op: "deleted"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
