package core

// File changes made while the app was closed: a files trigger that asks
// for them (FileWatch.WhileClosed) gets them when the project opens again.
// For that, the last scan of each folder such a trigger watches (the
// project, or a folder outside it) is kept in the history database —
// saved when files changed, at most once a scheduler tick, and when the
// project closes — and compared with a fresh scan on open. Only those
// folders' scans are kept; without such triggers nothing is.

import (
	"encoding/json"
	"path/filepath"

	"codegen/internal/watch"
)

func scansNS(root string) string { return "filescans:" + workspaceKey(root) }

// scanWatcher is the watcher of a kept folder: the project's own (kept
// with its trigger state, so it is still the right one while the project
// closes and another opens), or one outside it.
func scanWatcher(ts *triggerStore, root, dir string) *watch.Watcher {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if dir == root {
		return ts.projW
	}
	return ts.extW[dir]
}

// saveScans keeps the kept folders' last scans (when files changed, or force).
func (h *Hub) saveScans(root string, force bool) { h.saveScansOf(h.triggers(root), root, force) }

func (h *Hub) saveScansOf(ts *triggerStore, root string, force bool) {
	if !h.Store.HasDB() {
		return
	}
	ts.mu.Lock()
	dirty := ts.snapDirty || force
	var dirs []string
	for d := range ts.snap {
		dirs = append(dirs, d)
	}
	ts.snapDirty = false
	ts.mu.Unlock()
	if !dirty {
		return
	}
	old, _, _ := h.Store.LoadState(scansNS(root))
	vals := map[string]json.RawMessage{}
	for _, d := range dirs {
		if w := scanWatcher(ts, root, d); w != nil {
			if st := w.Stamps(); st != nil {
				if b, err := json.Marshal(st); err == nil {
					vals[d] = b
					continue
				}
			}
		}
		if b, ok := old[d]; ok { // no watcher up for it now: keep what was kept
			vals[d] = b
		}
	}
	_ = h.Store.SaveState(scansNS(root), vals)
}

// catchUpFiles compares the kept scans with the fresh ones (the project
// just opened) and gives the differences to the triggers that ask for them.
func (h *Hub) catchUpFiles(root string) {
	vals, ok, err := h.Store.LoadState(scansNS(root))
	if !ok || err != nil || len(vals) == 0 {
		return
	}
	ts := h.triggers(root)
	for dir, raw := range vals {
		var old map[string]watch.Stamp
		if json.Unmarshal(raw, &old) != nil {
			continue
		}
		w := scanWatcher(ts, root, dir)
		if w == nil {
			continue
		}
		cur := w.Stamps()
		if cur == nil {
			continue
		}
		var list []fileChange
		for _, c := range watch.DiffStamps(old, cur) {
			abs := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(c.Path)))
			rel := ""
			if dir == root {
				rel = c.Path
			}
			list = append(list, fileChange{rel, abs, c.Op})
		}
		h.noteChangesFor(root, list, true)
	}
}
