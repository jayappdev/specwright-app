package core

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/workflow"
)

// A project skill can be a folder with more than SKILL.md: docs and
// references in sub-folders that SKILL.md links to (relative paths), and
// its scripts in scripts/. The wizard creates, edits and deletes these
// files; the agent is told which files a selected skill has.
//
//   .codegen/skills/project/<skill>/SKILL.md
//   .codegen/skills/project/<skill>/scripts/gen-dto.sh
//   .codegen/skills/project/<skill>/docs/api.md

// SkillFileSave is a file inside a project skill's folder.
type SkillFileSave struct {
	Skill   string `json:"skill"`   // the skill's name
	Path    string `json:"path"`    // relative to the skill folder, e.g. scripts/gen.sh or docs/api.md
	Content string `json:"content"` // the file's text
	OldPath string `json:"oldPath"` // the file's current relative path when editing (renames)
}

// cleanSkillPath checks a path inside a skill folder.
func cleanSkillPath(p string) (string, error) { return extension.CleanSkillPath(p) }

// skillDir finds a project skill's folder, turning a single-file skill
// (<name>.md) into a folder (<name>/SKILL.md) so it can hold more files.
func (h *Hub) skillDir(root, name string) (rel string, err error) {
	if !extension.LibraryNameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a skill name", name)
	}
	h.mu.RLock()
	p := h.profile
	h.mu.RUnlock()
	base := p.LibraryDir(workflow.ProjectSkillsDir)
	rel = base + "/" + name
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		return rel, nil
	}
	single := abs + ".md"
	if _, err := os.Stat(single); err != nil {
		return "", fmt.Errorf("there is no project skill called %s; create it first", name)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(single, filepath.Join(abs, "SKILL.md")); err != nil {
		return "", err
	}
	return rel, nil
}

// isScriptFile says whether a skill file should be executable.
func isScriptFile(rel, content string) bool {
	return strings.HasPrefix(rel, "scripts/") || strings.HasPrefix(content, "#!")
}

// SaveSkillFile creates, updates or renames a file in a project skill.
func (h *Hub) SaveSkillFile(r SkillFileSave) ([]workflow.Library, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	rel, err := cleanSkillPath(r.Path)
	if err != nil {
		return nil, err
	}
	old := ""
	if strings.TrimSpace(r.OldPath) != "" {
		if old, err = cleanSkillPath(r.OldPath); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(r.Content) == "" {
		return nil, errors.New("the file is empty")
	}
	dir, err := h.skillDir(root, strings.TrimSpace(r.Skill))
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(rel))
	if old != rel {
		if _, err := os.Stat(abs); err == nil {
			return nil, fmt.Errorf("%s already has a file %s", r.Skill, rel)
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if isScriptFile(rel, r.Content) {
		mode = 0o755
	}
	if err := os.WriteFile(abs, []byte(r.Content), mode); err != nil {
		return nil, err
	}
	_ = os.Chmod(abs, mode)
	if old != "" && old != rel {
		oldAbs := filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(old))
		_ = os.Remove(oldAbs)
		pruneEmptyDirs(filepath.Dir(oldAbs), filepath.Join(root, filepath.FromSlash(dir)))
	}
	return h.Libraries()
}

// DeleteSkillFile moves a file of a project skill to SpecWright's trash.
func (h *Hub) DeleteSkillFile(skill, file string) ([]workflow.Library, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	rel, err := cleanSkillPath(file)
	if err != nil {
		return nil, err
	}
	dir, err := h.skillDir(root, strings.TrimSpace(skill))
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(rel))
	if err := h.toTrash(abs); err != nil {
		return nil, fmt.Errorf("%s: %v", path.Join(dir, rel), err)
	}
	pruneEmptyDirs(filepath.Dir(abs), filepath.Join(root, filepath.FromSlash(dir)))
	return h.Libraries()
}

// pruneEmptyDirs removes empty folders from dir up to (not including) stop.
func pruneEmptyDirs(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop+string(filepath.Separator)) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
