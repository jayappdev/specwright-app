package core

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"codegen/internal/extension"
	"codegen/internal/workflow"
)

// Development wizard libraries.
//
//   Corporate libraries (global): skills an admin publishes for the whole
//   app. They live in a signed catalog (like managed tools), are editable
//   only in admin mode, and verified entries are copied into each project's
//   .codegen/skills/global/<name>/SKILL.md, overwriting local edits.
//
//   Project skills and scripts: plain files in the project
//   (.codegen/skills/project/, .codegen/scripts/) that any user can create,
//   edit and delete from the wizard.

// MachineLibraryCatalogPath is the read-only, IT-deployed corporate library catalog.
func MachineLibraryCatalogPath() string {
	return filepath.Join(filepath.Dir(PolicyPath), "managed-libraries.json")
}

// managedMarker records which folders in the global skills folder the app
// wrote, so retired libraries can be removed without touching local ones.
const managedMarker = ".managed.json"

// ManagedLibraries lists every corporate library with its check result
// (machine catalog first; it wins on a name clash).
func (h *Hub) ManagedLibraries() []extension.ManagedLibrary {
	trusted := h.trustedKeys()
	var out []extension.ManagedLibrary
	seen := map[string]bool{}
	for _, src := range []struct {
		name  string
		store *extension.LibraryStore
	}{{"machine", extension.NewLibraryStore(MachineLibraryCatalogPath())}, {"app", h.libs}} {
		list, err := src.store.List()
		if err != nil {
			continue
		}
		for _, l := range list {
			if seen[l.Name] {
				continue
			}
			seen[l.Name] = true
			l.Status, l.Source = l.Verify(trusted), src.name
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SaveManagedLibrary signs and publishes a corporate skill (admin only),
// then refreshes the open project's copy.
func (h *Hub) SaveManagedLibrary(name, desc, content string) ([]workflow.Library, error) {
	priv, signer, err := h.adminKey()
	if err != nil {
		return nil, err
	}
	if _, ok := h.trustedKeys()[extension.KeyID(priv.Public().(ed25519.PublicKey))]; !ok {
		return nil, errors.New("your admin key is not trusted on this computer, so the library would be blocked; trust it on the Admin screen or deploy the policy file")
	}
	if list, _ := extension.NewLibraryStore(MachineLibraryCatalogPath()).List(); len(list) > 0 {
		for _, l := range list {
			if l.Name == name {
				return nil, fmt.Errorf("%s comes from the machine catalog (%s); change it there", name, MachineLibraryCatalogPath())
			}
		}
	}
	if _, err := h.libs.Put(name, desc, content, priv, signer); err != nil {
		return nil, err
	}
	return h.Libraries()
}

// DeleteManagedLibrary retires a corporate skill (admin only).
func (h *Hub) DeleteManagedLibrary(name string) ([]workflow.Library, error) {
	if _, _, err := h.adminKey(); err != nil {
		return nil, err
	}
	if err := h.libs.Remove(name); err != nil {
		return nil, err
	}
	return h.Libraries()
}

// syncGlobalLibraries copies the verified corporate skills into the
// project and removes ones that were retired or no longer verify. Local
// skills in the same folder are left alone, unless a managed one has the
// same name: then the local copy goes to the trash first.
func (h *Hub) syncGlobalLibraries(root string, p *workflow.Profile) error {
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, filepath.FromSlash(p.LibraryDir(workflow.GlobalSkillsDir)))
	var marker map[string]string // name → signature written
	if b, err := os.ReadFile(filepath.Join(dir, managedMarker)); err == nil {
		_ = json.Unmarshal(b, &marker)
	}
	if marker == nil {
		marker = map[string]string{}
	}
	want := map[string]extension.ManagedLibrary{}
	for _, l := range h.ManagedLibraries() {
		if l.Status == extension.ManagedVerified {
			want[l.Name] = l
		}
	}
	if len(want) == 0 && len(marker) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	changed := false
	for name, l := range want {
		target := filepath.Join(dir, name, "SKILL.md")
		cur, err := os.ReadFile(target)
		if err == nil && string(cur) == l.Content && marker[name] == l.Signature {
			continue
		}
		if _, ours := marker[name]; !ours {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				_ = h.toTrash(filepath.Join(dir, name)) // a local skill with the same name
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(l.Content), 0o644); err != nil {
			return err
		}
		marker[name], changed = l.Signature, true
	}
	for name := range marker {
		if _, ok := want[name]; !ok {
			_ = os.RemoveAll(filepath.Join(dir, name))
			delete(marker, name)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	names := make([]string, 0, len(marker))
	for n := range marker {
		names = append(names, n)
	}
	sort.Strings(names)
	b, _ := json.MarshalIndent(marker, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, managedMarker), append(b, '\n'), 0o644)
	// the copies come from the app's catalog; keep them out of git
	var gi strings.Builder
	gi.WriteString("# Corporate skills published by a SpecWright admin. They are copied here\n# from the app's catalog and overwritten when they change: edit them on the\n# Development wizard in admin mode, not here.\n" + managedMarker + "\n")
	for _, n := range names {
		gi.WriteString("/" + n + "/\n")
	}
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gi.String()), 0o644)
}

// Libraries rescans the project's libraries (after syncing the corporate
// ones) and marks which are managed.
func (h *Hub) Libraries() ([]workflow.Library, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	h.mu.RLock()
	p := h.profile
	h.mu.RUnlock()
	if err := h.syncGlobalLibraries(root, p); err != nil {
		return nil, err
	}
	return h.annotate(workflow.Libraries(root, p)), nil
}

func (h *Hub) annotate(libs []workflow.Library) []workflow.Library {
	byName := map[string]extension.ManagedLibrary{}
	for _, l := range h.ManagedLibraries() {
		byName[l.Name] = l
	}
	for i, l := range libs {
		if l.Scope != "global" {
			continue
		}
		if m, ok := byName[l.Name]; ok && m.Status == extension.ManagedVerified {
			libs[i].Managed = &workflow.LibraryManaged{Version: m.Version, Signer: m.Signer, SignedAt: m.SignedAt, Status: m.Status, Source: m.Source}
			if m.Desc != "" {
				libs[i].Note = m.Desc
			}
		}
	}
	return libs
}

// --- project skills and scripts ------------------------------------------------

// LibrarySave is a project skill or script to create or update.
type LibrarySave struct {
	Kind    string `json:"kind"`    // skill | script
	Name    string `json:"name"`    // skill name, or the script's file name
	Content string `json:"content"` // Markdown or script source
	OldPath string `json:"oldPath"` // the library's current path when editing
}

var scriptNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

// SaveProjectLibrary creates or updates a project skill
// (<project skills>/<name>/SKILL.md) or script (<scripts>/<file>).
func (h *Hub) SaveProjectLibrary(r LibrarySave) ([]workflow.Library, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	h.mu.RLock()
	p := h.profile
	h.mu.RUnlock()
	r.Name = strings.TrimSpace(r.Name)
	var rel, oldID, newID string
	switch r.Kind {
	case "skill":
		r.Name = strings.TrimSuffix(r.Name, ".md")
		if !extension.LibraryNameRE.MatchString(r.Name) {
			return nil, errors.New("skill names use lowercase letters, digits, '.', '-' and '_' (they become a folder name)")
		}
		base := p.LibraryDir(workflow.ProjectSkillsDir)
		rel = base + "/" + r.Name + "/SKILL.md"
		// keep a single-file skill (<name>.md) where it is
		if r.OldPath == base+"/"+r.Name+".md" {
			rel = r.OldPath
		}
		newID = "project:skill:" + r.Name
	case "script":
		if !scriptNameRE.MatchString(r.Name) {
			return nil, errors.New("script file names use letters, digits, '.', '-' and '_', e.g. gen-dto.sh")
		}
		rel = p.LibraryDir(workflow.ScriptsDir) + "/" + r.Name
		newID = "project:script:" + r.Name
	default:
		return nil, errors.New("kind must be skill or script")
	}
	if strings.TrimSpace(r.Content) == "" {
		return nil, errors.New("the " + r.Kind + " is empty")
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if r.OldPath != rel {
		if _, err := os.Stat(abs); err == nil {
			return nil, fmt.Errorf("a %s called %s already exists", r.Kind, r.Name)
		}
	}
	if r.OldPath != "" {
		if oldID, err = h.projectLibraryID(r.OldPath); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if r.Kind == "script" {
		mode = 0o755
	}
	if err := os.WriteFile(abs, []byte(r.Content), mode); err != nil {
		return nil, err
	}
	_ = os.Chmod(abs, mode)
	if r.OldPath != "" && r.OldPath != rel { // renamed: remove the old one
		_ = h.removeProjectLibrary(root, r.OldPath)
	}
	// a rename keeps the library selected
	if oldID != "" && oldID != newID {
		h.mu.Lock()
		np := *h.profile
		np.Project = append([]string(nil), np.Project...)
		for i, id := range np.Project {
			if id == oldID {
				np.Project[i] = newID
			}
		}
		h.mu.Unlock()
		_ = h.SaveProfile(np)
	}
	return h.Libraries()
}

// DeleteProjectLibrary moves a project skill or script to SpecWright's trash.
func (h *Hub) DeleteProjectLibrary(path string) ([]workflow.Library, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	if _, err := h.projectLibraryID(path); err != nil {
		return nil, err
	}
	if err := h.removeProjectLibrary(root, path); err != nil {
		return nil, err
	}
	return h.Libraries()
}

// projectLibraryID checks that path is a project skill or script and
// returns its library id.
func (h *Hub) projectLibraryID(path string) (string, error) {
	h.mu.RLock()
	p := h.profile
	h.mu.RUnlock()
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if strings.Contains(path, "..") {
		return "", errors.New("not a project library")
	}
	skills, scripts := p.LibraryDir(workflow.ProjectSkillsDir)+"/", p.LibraryDir(workflow.ScriptsDir)+"/"
	switch {
	case strings.HasPrefix(path, skills):
		rest := strings.TrimPrefix(path, skills)
		if n, ok := strings.CutSuffix(rest, "/SKILL.md"); ok && !strings.Contains(n, "/") {
			return "project:skill:" + n, nil
		}
		if n, ok := strings.CutSuffix(rest, ".md"); ok && !strings.Contains(n, "/") {
			return "project:skill:" + n, nil
		}
	case strings.HasPrefix(path, scripts):
		if n := strings.TrimPrefix(path, scripts); !strings.Contains(n, "/") {
			return "project:script:" + n, nil
		}
	}
	if strings.HasPrefix(path, p.LibraryDir(workflow.GlobalSkillsDir)+"/") {
		return "", errors.New("corporate libraries are managed by an admin; unlock admin mode to change them")
	}
	return "", fmt.Errorf("%s is not a project skill or script", path)
}

func (h *Hub) removeProjectLibrary(root, rel string) error {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if filepath.Base(abs) == "SKILL.md" {
		abs = filepath.Dir(abs)
	}
	return h.toTrash(abs)
}

// toTrash moves a file or folder into SpecWright's trash (recoverable).
func (h *Hub) toTrash(abs string) error {
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	dir := filepath.Join(h.Store.Dir(), "trash", time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.Base(abs))
	for i := 2; ; i++ {
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s-%d", filepath.Base(abs), i))
	}
	return os.Rename(abs, dst)
}

// libsFor syncs the corporate libraries into root and lists them, without
// taking h.mu (callers may hold it).
func (h *Hub) libsFor(root string, p *workflow.Profile) []workflow.Library {
	_ = h.syncGlobalLibraries(root, p)
	return h.annotate(workflow.Libraries(root, p))
}

// prepareLibraries syncs corporate libraries before a run starts and lets
// the watcher see the copies now, so they are never counted as the run's
// file changes.
func (h *Hub) prepareLibraries() {
	h.mu.RLock()
	root, p, w := h.root, h.profile, h.watcher
	h.mu.RUnlock()
	if root == "" {
		return
	}
	_ = h.syncGlobalLibraries(root, p)
	if w != nil {
		if ch := w.Poll(); len(ch) > 0 {
			h.onFiles(ch)
		}
	}
}
