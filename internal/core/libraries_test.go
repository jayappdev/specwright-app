package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/workflow"
)

func libByID(libs []workflow.Library, id string) *workflow.Library {
	for i := range libs {
		if libs[i].ID == id {
			return &libs[i]
		}
	}
	return nil
}

func TestCorporateLibrariesAreAdminOnlyAndSynced(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	skill := "# ACME Java style\n\nUse records for DTOs.\n"
	if _, err := h.SaveManagedLibrary("acme-java-style", "House Java style", skill); err == nil {
		t.Fatal("publishing a corporate library must need admin mode")
	}
	// a local, unmanaged corporate skill with the same name gets replaced (to the trash)
	local := filepath.Join(ws, ".codegen", "skills", "global", "acme-java-style")
	_ = os.MkdirAll(local, 0o755)
	_ = os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("old local copy\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(ws, ".codegen", "skills", "global", "team-notes"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, ".codegen", "skills", "global", "team-notes", "SKILL.md"), []byte("Team notes\n"), 0o644)

	if _, err := h.CreateAdminKey("Ada Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	libs, err := h.SaveManagedLibrary("acme-java-style", "House Java style", skill)
	if err != nil {
		t.Fatal(err)
	}
	l := libByID(libs, "global:skill:acme-java-style")
	if l == nil || l.Managed == nil || l.Managed.Version != 1 || l.Managed.Signer != "Ada Admin" || l.Note != "House Java style" {
		t.Fatalf("%+v", l)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "SKILL.md")); string(b) != skill {
		t.Fatalf("copy not synced: %q", b)
	}
	if tn := libByID(libs, "global:skill:team-notes"); tn == nil || tn.Managed != nil {
		t.Fatal("local corporate skills stay, unmanaged")
	}
	gi, _ := os.ReadFile(filepath.Join(ws, ".codegen", "skills", "global", ".gitignore"))
	if !strings.Contains(string(gi), "/acme-java-style/") || strings.Contains(string(gi), "team-notes") {
		t.Fatalf("gitignore: %s", gi)
	}
	// a second version
	if libs, err = h.SaveManagedLibrary("acme-java-style", "House Java style", skill+"\nPrefer var.\n"); err != nil || libByID(libs, "global:skill:acme-java-style").Managed.Version != 2 {
		t.Fatal(err)
	}
	h.LockAdmin()

	// a user's edit of the copy is overwritten on the next sync
	_ = os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("hacked\n"), 0o644)
	if _, err := h.Libraries(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "SKILL.md")); !strings.Contains(string(b), "Prefer var.") {
		t.Fatalf("local edit must be replaced: %q", b)
	}
	// users cannot change or delete it through the project API
	if _, err := h.DeleteProjectLibrary(".codegen/skills/global/acme-java-style/SKILL.md"); err == nil || !strings.Contains(err.Error(), "admin") {
		t.Fatal(err)
	}
	if _, err := h.DeleteManagedLibrary("acme-java-style"); err == nil {
		t.Fatal("retiring needs admin mode")
	}

	// tampering with the catalog blocks the library and removes the copy
	cat := filepath.Join(h.Store.Dir(), "managed-libraries.json")
	b, _ := os.ReadFile(cat)
	_ = os.WriteFile(cat, []byte(strings.Replace(string(b), "Prefer var.", "Prefer anything.", 1)), 0o644)
	libs, _ = h.Libraries()
	if l := libByID(libs, "global:skill:acme-java-style"); l != nil {
		t.Fatalf("tampered library must not reach the project: %+v", l)
	}
	for _, m := range h.ManagedLibraries() {
		if m.Name == "acme-java-style" && m.Status != extension.ManagedTampered {
			t.Fatal(m.Status)
		}
	}
	_ = os.WriteFile(cat, b, 0o644)

	// global: another project gets it too; retiring removes it everywhere
	other := filepath.Join(t.TempDir(), "other project")
	_ = os.MkdirAll(other, 0o755)
	st, err := h.OpenWorkspace(other)
	if err != nil || libByID(st.Libraries, "global:skill:acme-java-style") == nil {
		t.Fatalf("%v %+v", err, st.Libraries)
	}
	if _, err := h.UnlockAdmin("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	libs, err = h.DeleteManagedLibrary("acme-java-style")
	if err != nil || libByID(libs, "global:skill:acme-java-style") != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, ".codegen", "skills", "global", "acme-java-style")); err == nil {
		t.Fatal("retired copy must be removed")
	}
	var marker map[string]string
	mb, _ := os.ReadFile(filepath.Join(other, ".codegen", "skills", "global", managedMarker))
	_ = json.Unmarshal(mb, &marker)
	if len(marker) != 0 {
		t.Fatal(marker)
	}
}

func TestProjectLibrariesCRUD(t *testing.T) {
	h, ws := newHub(t)
	libs, err := h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "orders-domain", Content: "# Orders domain\n\nAn order is NEW, PLACED or REJECTED.\n"})
	if err != nil || libByID(libs, "project:skill:orders-domain") == nil {
		t.Fatal(err, libs)
	}
	if _, err := h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "orders-domain", Content: "dup"}); err == nil {
		t.Fatal("creating a duplicate must fail")
	}
	if _, err := h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "Bad Name", Content: "x"}); err == nil {
		t.Fatal("bad name accepted")
	}
	// select it, then rename: the selection follows
	p := *h.profile
	p.Project = []string{"project:skill:orders-domain"}
	_ = h.SaveProfile(p)
	libs, err = h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "orders", Content: "# Orders\n", OldPath: ".codegen/skills/project/orders-domain/SKILL.md"})
	if err != nil || libByID(libs, "project:skill:orders") == nil || libByID(libs, "project:skill:orders-domain") != nil {
		t.Fatal(err, libs)
	}
	if h.profile.Project[0] != "project:skill:orders" {
		t.Fatal(h.profile.Project)
	}
	// a single-file skill is edited in place
	_ = os.WriteFile(filepath.Join(ws, ".codegen", "skills", "project", "api.md"), []byte("APIs\n"), 0o644)
	if _, err := h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "api", Content: "APIs v2\n", OldPath: ".codegen/skills/project/api.md"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, ".codegen", "skills", "project", "api.md")); string(b) != "APIs v2\n" {
		t.Fatal(string(b))
	}
	// scripts are executable
	libs, err = h.SaveProjectLibrary(LibrarySave{Kind: "script", Name: "gen-dto.sh", Content: "#!/bin/sh\necho dto\n"})
	if err != nil || libByID(libs, "project:script:gen-dto.sh") == nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(ws, ".codegen", "scripts", "gen-dto.sh")); fi.Mode()&0o100 == 0 {
		t.Fatal("script not executable")
	}
	// delete goes to the trash; paths outside the compartments are refused
	if libs, err = h.DeleteProjectLibrary(".codegen/skills/project/orders/SKILL.md"); err != nil || libByID(libs, "project:skill:orders") != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".codegen", "skills", "project", "orders")); err == nil {
		t.Fatal("not deleted")
	}
	for _, bad := range []string{"pom.xml", ".codegen/skills/project/../../pom.xml", ".codegen/profile.json"} {
		if _, err := h.DeleteProjectLibrary(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}
