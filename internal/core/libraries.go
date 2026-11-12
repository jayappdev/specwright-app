package core

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
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

// libraryAdmin returns the unlocked, trusted admin key for publishing name.
func (h *Hub) libraryAdmin(name string) (ed25519.PrivateKey, string, error) {
	priv, signer, err := h.adminKey()
	if err != nil {
		return nil, "", err
	}
	if _, ok := h.trustedKeys()[extension.KeyID(priv.Public().(ed25519.PublicKey))]; !ok {
		return nil, "", errors.New("your admin key is not trusted on this computer, so the library would be blocked; trust it on the Admin screen or deploy the policy file")
	}
	if list, _ := extension.NewLibraryStore(MachineLibraryCatalogPath()).List(); len(list) > 0 {
		for _, l := range list {
			if l.Name == name {
				return nil, "", fmt.Errorf("%s comes from the machine catalog (%s); change it there", name, MachineLibraryCatalogPath())
			}
		}
	}
	return priv, signer, nil
}

// SaveManagedLibrary signs and publishes a corporate skill (admin only),
// then refreshes the open project's copy. It keeps the skill's files; a
// local copy being published for the first time brings the files in its
// folder along.
func (h *Hub) SaveManagedLibrary(name, desc, content string) ([]workflow.Library, error) {
	priv, signer, err := h.libraryAdmin(name)
	if err != nil {
		return nil, err
	}
	var files map[string]string
	if cur, ok := h.libs.Get(name); ok {
		files = cur.Files
	} else if files, err = h.localGlobalSkillFiles(name); err != nil {
		return nil, err
	}
	if _, err := h.libs.Put(name, desc, content, files, priv, signer); err != nil {
		return nil, err
	}
	return h.Libraries()
}

// localGlobalSkillFiles reads the files of an unpublished local skill in
// the project's corporate skills folder.
func (h *Hub) localGlobalSkillFiles(name string) (map[string]string, error) {
	h.mu.RLock()
	root, p := h.root, h.profile
	h.mu.RUnlock()
	if root == "" || !extension.LibraryNameRE.MatchString(name) {
		return nil, nil
	}
	base := filepath.Join(root, filepath.FromSlash(p.LibraryDir(workflow.GlobalSkillsDir)))
	var marker map[string]string
	if b, err := os.ReadFile(filepath.Join(base, managedMarker)); err == nil && json.Unmarshal(b, &marker) == nil {
		if _, ours := marker[name]; ours {
			return nil, nil // an earlier managed copy, not the user's local skill
		}
	}
	dir := filepath.Join(base, name)
	files := map[string]string{}
	for _, f := range workflow.SkillFiles(dir) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		files[f] = string(b)
	}
	return files, nil
}

// SaveManagedLibraryFile adds, changes or renames one file of a corporate
// skill and publishes the skill again as a new signed version (admin only).
func (h *Hub) SaveManagedLibraryFile(name, file, content, oldFile string) ([]workflow.Library, error) {
	rel, err := extension.CleanSkillPath(file)
	if err != nil {
		return nil, err
	}
	old := ""
	if strings.TrimSpace(oldFile) != "" {
		if old, err = extension.CleanSkillPath(oldFile); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("the file is empty")
	}
	priv, signer, err := h.libraryAdmin(name)
	if err != nil {
		return nil, err
	}
	cur, ok := h.libs.Get(name)
	if !ok {
		return nil, fmt.Errorf("publish the corporate skill %s first, then add files to it", name)
	}
	files := map[string]string{}
	for k, v := range cur.Files {
		files[k] = v
	}
	if _, exists := files[rel]; exists && old != rel {
		return nil, fmt.Errorf("%s already has a file %s", name, rel)
	}
	if old != "" && old != rel {
		delete(files, old)
	}
	files[rel] = content
	if _, err := h.libs.Put(name, cur.Desc, cur.Content, files, priv, signer); err != nil {
		return nil, err
	}
	return h.Libraries()
}

// DeleteManagedLibraryFile removes one file from a corporate skill and
// publishes it again (admin only).
func (h *Hub) DeleteManagedLibraryFile(name, file string) ([]workflow.Library, error) {
	rel, err := extension.CleanSkillPath(file)
	if err != nil {
		return nil, err
	}
	priv, signer, err := h.libraryAdmin(name)
	if err != nil {
		return nil, err
	}
	cur, ok := h.libs.Get(name)
	if !ok {
		return nil, fmt.Errorf("there is no corporate skill called %s", name)
	}
	if _, ok := cur.Files[rel]; !ok {
		return nil, fmt.Errorf("%s has no file %s", name, rel)
	}
	files := map[string]string{}
	for k, v := range cur.Files {
		if k != rel {
			files[k] = v
		}
	}
	if _, err := h.libs.Put(name, cur.Desc, cur.Content, files, priv, signer); err != nil {
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
		folder := filepath.Join(dir, name)
		if marker[name] == l.Signature && globalCopyMatches(folder, l) {
			continue
		}
		if _, ours := marker[name]; !ours {
			if _, err := os.Stat(folder); err == nil {
				_ = h.toTrash(folder) // a local skill with the same name
			}
		} else {
			_ = os.RemoveAll(folder) // our earlier copy: write it afresh
		}
		if err := writeGlobalCopy(folder, l); err != nil {
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

// globalCopyMatches says whether a project's copy of a corporate skill is
// exactly the catalog entry (SKILL.md and its files, nothing else).
func globalCopyMatches(folder string, l extension.ManagedLibrary) bool {
	if b, err := os.ReadFile(filepath.Join(folder, "SKILL.md")); err != nil || string(b) != l.Content {
		return false
	}
	have := workflow.SkillFiles(folder)
	if len(have) != len(l.Files) {
		return false
	}
	for _, f := range have {
		want, ok := l.Files[f]
		if !ok {
			return false
		}
		if b, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(f))); err != nil || string(b) != want {
			return false
		}
	}
	return true
}

// writeGlobalCopy writes a corporate skill's SKILL.md and files.
func writeGlobalCopy(folder string, l extension.ManagedLibrary) error {
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(l.Content), 0o644); err != nil {
		return err
	}
	for f, c := range l.Files {
		rel, err := extension.CleanSkillPath(f)
		if err != nil || rel != f {
			continue // never write outside the folder
		}
		p := filepath.Join(folder, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if isScriptFile(rel, c) {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(c), mode); err != nil {
			return err
		}
		_ = os.Chmod(p, mode)
	}
	return nil
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
	renamedDir := false
	if r.Kind == "skill" && r.OldPath != "" && r.OldPath != rel && strings.HasSuffix(r.OldPath, "/SKILL.md") && strings.HasSuffix(rel, "/SKILL.md") {
		// a skill folder keeps its docs and scripts: move the whole folder
		from := filepath.Join(root, filepath.FromSlash(path.Dir(r.OldPath)))
		if err := os.Rename(from, filepath.Dir(abs)); err != nil {
			return nil, err
		}
		renamedDir = true
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
	if r.OldPath != "" && r.OldPath != rel && !renamedDir { // renamed: remove the old one
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
