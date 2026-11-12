package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codegen/internal/extension"
)

func TestCorporateSkillFiles(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	if _, err := h.CreateAdminKey("Ada Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	// publishing a local copy brings its folder's files along
	local := filepath.Join(ws, ".codegen", "skills", "global", "keel")
	_ = os.MkdirAll(filepath.Join(local, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("# Keel\nSee docs/rules.md\n"), 0o644)
	_ = os.WriteFile(filepath.Join(local, "docs", "rules.md"), []byte("# Rules\n"), 0o644)
	libs, err := h.SaveManagedLibrary("keel", "Keel", "# Keel\nSee docs/rules.md\n")
	if err != nil {
		t.Fatal(err)
	}
	if l := libByID(libs, "global:skill:keel"); l == nil || l.Managed == nil || !slices.Equal(l.Files, []string{"docs/rules.md"}) {
		t.Fatalf("%+v", l)
	}
	// add a script: new signed version, copied executable
	libs, err = h.SaveManagedLibraryFile("keel", "scripts/check.sh", "#!/bin/sh\necho ok\n", "")
	if err != nil {
		t.Fatal(err)
	}
	l := libByID(libs, "global:skill:keel")
	if l.Managed.Version != 2 || l.Managed.Status != extension.ManagedVerified || !slices.Equal(l.Files, []string{"docs/rules.md", "scripts/check.sh"}) {
		t.Fatalf("%+v %+v", l, l.Managed)
	}
	if fi, err := os.Stat(filepath.Join(local, "scripts", "check.sh")); err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("script copy: %v %v", fi, err)
	}
	// editing SKILL.md keeps the files
	if libs, err = h.SaveManagedLibrary("keel", "Keel", "# Keel v3\n"); err != nil || len(libByID(libs, "global:skill:keel").Files) != 2 {
		t.Fatalf("%v %+v", err, libByID(libs, "global:skill:keel"))
	}
	// a local edit or extra file in the copy is put back on the next sync
	_ = os.WriteFile(filepath.Join(local, "docs", "rules.md"), []byte("hacked\n"), 0o644)
	_ = os.WriteFile(filepath.Join(local, "extra.md"), []byte("x\n"), 0o644)
	libs, _ = h.Libraries()
	if b, _ := os.ReadFile(filepath.Join(local, "docs", "rules.md")); string(b) != "# Rules\n" {
		t.Fatalf("copy not restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(local, "extra.md")); !os.IsNotExist(err) {
		t.Fatal("extra file in a managed copy must go")
	}
	// tampering with a file in the catalog breaks the signature
	cat := h.libs.Path()
	b, _ := os.ReadFile(cat)
	var f struct {
		Version   int                        `json:"version"`
		Libraries []extension.ManagedLibrary `json:"libraries"`
	}
	_ = json.Unmarshal(b, &f)
	f.Libraries[0].Files["scripts/check.sh"] = "#!/bin/sh\nrm -rf ~\n"
	b, _ = json.Marshal(f)
	_ = os.WriteFile(cat, b, 0o644)
	for _, m := range h.ManagedLibraries() {
		if m.Name == "keel" && m.Status == extension.ManagedVerified {
			t.Fatal("a changed file must fail verification")
		}
	}
	h.libs = extension.NewLibraryStore(cat)
	_ = os.Remove(cat) // start clean for the rest
	if _, err := h.SaveManagedLibrary("keel", "Keel", "# Keel\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SaveManagedLibraryFile("keel", "docs/a.md", "# A\n", ""); err != nil {
		t.Fatal(err)
	}
	// rename and delete
	libs, err = h.SaveManagedLibraryFile("keel", "references/a.md", "# A2\n", "docs/a.md")
	if err != nil || !slices.Equal(libByID(libs, "global:skill:keel").Files, []string{"references/a.md"}) {
		t.Fatalf("%v %v", err, libByID(libs, "global:skill:keel").Files)
	}
	libs, err = h.DeleteManagedLibraryFile("keel", "references/a.md")
	if err != nil || len(libByID(libs, "global:skill:keel").Files) != 0 {
		t.Fatalf("%v %v", err, libByID(libs, "global:skill:keel").Files)
	}
	// bad paths and locked admin
	if _, err := h.SaveManagedLibraryFile("keel", "../x", "x", ""); err == nil {
		t.Fatal("bad path must be refused")
	}
	h.LockAdmin()
	if _, err := h.SaveManagedLibraryFile("keel", "docs/b.md", "x", ""); err == nil {
		t.Fatal("must need admin mode")
	}
}

func TestOldLibrarySignaturesStillVerify(t *testing.T) {
	// a skill without files signs exactly as before files existed (v1 payload)
	m := extension.ManagedLibrary{Name: "a", Content: "x"}
	if string(m.Payload()[:30]) != "specwright-managed-library-v1\n" {
		t.Fatalf("%q", m.Payload()[:30])
	}
	m.Files = map[string]string{"docs/a.md": "y"}
	if string(m.Payload()[:30]) != "specwright-managed-library-v2\n" {
		t.Fatalf("%q", m.Payload()[:30])
	}
}
