package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

// Deleting a tool from the project archives it: its files and its record
// from .codegen/extensions.json move to .codegen/archive/<datetime>/ in the
// project, with a note on how to bring it back. A Copilot extension tool
// archives its whole extension (.github/extensions/<name>), since its tools
// live in one folder.

// ArchiveDir is the project folder deleted tools are moved to.
const ArchiveDir = ".codegen/archive"

// ArchiveResult says where a deleted tool went.
type ArchiveResult struct {
	Path  string   `json:"path"`  // workspace-relative archive folder
	Tools []string `json:"tools"` // tools that were removed
	Moved []string `json:"moved"` // workspace-relative paths that were moved
}

// ArchiveTool deletes a project tool by moving it to the archive.
func (h *Hub) ArchiveTool(name string) (ArchiveResult, error) {
	root, err := h.need()
	if err != nil {
		return ArchiveResult{}, err
	}
	if h.isManagedName(name) {
		return ArchiveResult{}, managedRefusal(name)
	}
	e, err := h.exts.Get(name)
	if err != nil {
		return ArchiveResult{}, err
	}
	switch {
	case e.Kind == "npm":
		return ArchiveResult{}, fmt.Errorf("%s is the npm script ext:%s in package.json: delete it there and it disappears from Tools", name, name)
	case e.Kind == extension.KindCopilot:
		x, err := extension.CopilotExtOf(root, e)
		if err != nil {
			return ArchiveResult{}, err
		}
		return h.archiveCopilotExt(root, x.Name)
	case e.Scope == "global":
		return ArchiveResult{}, fmt.Errorf("%s is a global (corporate) tool; it is managed in the corporate catalog, not in this project", name)
	}

	dir, rel, err := newArchiveDir(root)
	if err != nil {
		return ArchiveResult{}, err
	}
	res := ArchiveResult{Path: rel, Tools: []string{name}}
	// the tool's files: a folder (tools with dependencies) or one script
	var from []string
	if extension.HasDeps(e) || e.Dir != "" {
		from = append(from, firstNonEmpty(e.Dir, extension.ToolDir(e.Scope, e.Name)))
	}
	if e.ScriptPath != "" {
		from = append(from, e.ScriptPath)
	}
	for _, f := range from {
		src, ok := insideRoot(root, f)
		if !ok {
			continue
		}
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(dir, "tools", name, filepath.Base(src))
		if err := moveInto(src, dst); err != nil {
			return res, fmt.Errorf("archiving %s: %v", f, err)
		}
		res.Moved = append(res.Moved, filepath.ToSlash(f))
	}
	if err := writeArchiveRecord(filepath.Join(dir, "tools", name, "tool.json"), []extension.Extension{e}); err != nil {
		return res, err
	}
	if err := h.exts.Delete(name); err != nil {
		return res, err
	}
	writeArchiveNote(dir, res, fmt.Sprintf("the project tool **%s**", name),
		"To restore it, move its files back to where they were (listed above) and add the record in `tool.json` back to `.codegen/extensions.json`; then approve it again in Tools.")
	h.Bus.Publish(bus.Event{Type: "tool.archived", Summary: "Deleted " + name + " → " + rel, Data: map[string]any{"tools": res.Tools, "path": rel}})
	return res, nil
}

// archiveCopilotExt moves a project Copilot extension and its tools'
// records to the archive (installed packages are not kept: they can be
// installed again).
func (h *Hub) archiveCopilotExt(root, name string) (ArchiveResult, error) {
	if err := extension.ValidCopilotExtName(name); err != nil {
		return ArchiveResult{}, err
	}
	extRel := filepath.ToSlash(filepath.Join(extension.ProjectCopilotExtDir, name))
	src := filepath.Join(root, filepath.FromSlash(extRel))
	if _, err := os.Stat(src); err != nil {
		return ArchiveResult{}, fmt.Errorf("%s is not in this project", extRel)
	}
	var records []extension.Extension
	all, _ := h.exts.List()
	for _, e := range all {
		if e.Kind == extension.KindCopilot && e.Dir == extRel {
			records = append(records, e)
		}
	}
	dir, rel, err := newArchiveDir(root)
	if err != nil {
		return ArchiveResult{}, err
	}
	res := ArchiveResult{Path: rel, Moved: []string{extRel}}
	for _, e := range records {
		res.Tools = append(res.Tools, e.Name)
	}
	dst := filepath.Join(dir, "copilot-extensions", name)
	if err := moveInto(src, dst); err != nil {
		return res, fmt.Errorf("archiving %s: %v", extRel, err)
	}
	_ = os.RemoveAll(filepath.Join(dst, "node_modules"))
	_ = os.Remove(filepath.Join(dst, ".codegen-installed"))
	if err := writeArchiveRecord(filepath.Join(dir, "copilot-extensions", name+".tools.json"), records); err != nil {
		return res, err
	}
	if err := h.exts.PutCopilotTools(extRel, nil); err != nil {
		return res, err
	}
	what := fmt.Sprintf("the Copilot extension **%s**", name)
	if len(res.Tools) > 0 {
		what += " and its tools " + strings.Join(res.Tools, ", ")
	}
	writeArchiveNote(dir, res, what,
		"To restore it, move `copilot-extensions/"+name+"` back to `"+extRel+"`, then approve it again in Tools (its npm packages are installed again on first use). Your own copy in ~/.copilot/extensions was not touched.")
	h.Bus.Publish(bus.Event{Type: "tool.archived", Summary: "Deleted Copilot extension " + name + " → " + rel, Data: map[string]any{"tools": res.Tools, "path": rel, "extension": name}})
	return res, nil
}

// newArchiveDir creates .codegen/archive/<YYYYMMDD-HHMMSS>[-n].
func newArchiveDir(root string) (abs, rel string, err error) {
	base := time.Now().Format("20060102-150405")
	for i := 1; ; i++ {
		n := base
		if i > 1 {
			n = fmt.Sprintf("%s-%d", base, i)
		}
		rel = ArchiveDir + "/" + n
		abs = filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", "", err
		}
		if err := os.Mkdir(abs, 0o755); err == nil {
			return abs, rel, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
	}
}

// insideRoot resolves a workspace-relative path and refuses one that
// leaves the workspace.
func insideRoot(root, rel string) (string, bool) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	r, err := filepath.Rel(root, abs)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return abs, true
}

func moveInto(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func writeArchiveRecord(p string, records []extension.Extension) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var v any = records
	if len(records) == 1 {
		v = records[0]
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

func writeArchiveNote(dir string, res ArchiveResult, what, restore string) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Archived %s\n\nDeleted from the project in SpecWright on %s.\n\n", what, time.Now().Format("2006-01-02 15:04:05"))
	if len(res.Moved) > 0 {
		b.WriteString("Moved here:\n\n")
		for _, m := range res.Moved {
			fmt.Fprintf(&b, "- `%s`\n", m)
		}
		b.WriteString("\n")
	}
	b.WriteString(restore + "\n")
	_ = os.WriteFile(filepath.Join(dir, "ARCHIVED.md"), []byte(b.String()), 0o644)
}
