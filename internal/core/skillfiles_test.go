package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codegen/internal/workflow"
)

func findLib(libs []workflow.Library, id string) *workflow.Library {
	for i := range libs {
		if libs[i].ID == id {
			return &libs[i]
		}
	}
	return nil
}

func TestSkillFolderFiles(t *testing.T) {
	h, ws := newHub(t)
	if _, err := h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "orders", Content: "# Orders\nSee [API](docs/api.md).\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SaveSkillFile(SkillFileSave{Skill: "orders", Path: "docs/api.md", Content: "# API\n"}); err != nil {
		t.Fatal(err)
	}
	libs, err := h.SaveSkillFile(SkillFileSave{Skill: "orders", Path: "scripts/gen.sh", Content: "#!/bin/sh\necho hi\n"})
	if err != nil {
		t.Fatal(err)
	}
	l := findLib(libs, "project:skill:orders")
	if l == nil || l.Dir != ".codegen/skills/project/orders" || !slices.Equal(l.Files, []string{"docs/api.md", "scripts/gen.sh"}) {
		t.Fatalf("%+v", l)
	}
	fi, err := os.Stat(filepath.Join(ws, ".codegen/skills/project/orders/scripts/gen.sh"))
	if err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("script must be executable: %v %v", fi, err)
	}
	// nested scripts are part of the skill, not separate libraries
	if findLib(libs, "project:script:gen.sh") != nil {
		t.Fatal("skill scripts must not be listed as shared scripts")
	}
	// bad paths
	for _, p := range []string{"../x.md", "SKILL.md", "a b.md", "", "/etc/passwd/../../x"} {
		if _, err := h.SaveSkillFile(SkillFileSave{Skill: "orders", Path: p, Content: "x"}); err == nil {
			t.Errorf("%q must be refused", p)
		}
	}
	if _, err := h.SaveSkillFile(SkillFileSave{Skill: "nope", Path: "a.md", Content: "x"}); err == nil {
		t.Error("unknown skill must be refused")
	}
	// rename a doc into another folder; the empty folder goes away
	libs, err = h.SaveSkillFile(SkillFileSave{Skill: "orders", Path: "references/api.md", OldPath: "docs/api.md", Content: "# API v2\n"})
	if err != nil {
		t.Fatal(err)
	}
	if l := findLib(libs, "project:skill:orders"); !slices.Equal(l.Files, []string{"references/api.md", "scripts/gen.sh"}) {
		t.Fatalf("%v", l.Files)
	}
	if _, err := os.Stat(filepath.Join(ws, ".codegen/skills/project/orders/docs")); !os.IsNotExist(err) {
		t.Fatal("empty docs folder should be removed")
	}
	// renaming the skill keeps its files
	libs, err = h.SaveProjectLibrary(LibrarySave{Kind: "skill", Name: "orders-domain", Content: "# Orders\n", OldPath: ".codegen/skills/project/orders/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if l := findLib(libs, "project:skill:orders-domain"); l == nil || len(l.Files) != 2 {
		t.Fatalf("rename lost files: %+v", l)
	}
	// delete a file
	libs, err = h.DeleteSkillFile("orders-domain", "scripts/gen.sh")
	if err != nil {
		t.Fatal(err)
	}
	if l := findLib(libs, "project:skill:orders-domain"); !slices.Equal(l.Files, []string{"references/api.md"}) {
		t.Fatalf("%v", l.Files)
	}
}

func TestSingleFileSkillBecomesFolder(t *testing.T) {
	h, ws := newHub(t)
	if err := os.MkdirAll(filepath.Join(ws, ".codegen/skills/project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".codegen/skills/project/style.md"), []byte("# Style\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	libs, err := h.SaveSkillFile(SkillFileSave{Skill: "style", Path: "scripts/fmt.sh", Content: "#!/bin/sh\n"})
	if err != nil {
		t.Fatal(err)
	}
	l := findLib(libs, "project:skill:style")
	if l == nil || l.Path != ".codegen/skills/project/style/SKILL.md" || len(l.Files) != 1 {
		t.Fatalf("%+v", l)
	}
}

func TestComposeListsSkillFiles(t *testing.T) {
	wf := &workflow.Workflow{Steps: []workflow.Step{{Cmd: "/x", Options: workflow.Options{Project: true}}}}
	p := &workflow.Profile{Project: []string{"project:skill:orders"}}
	libs := []workflow.Library{{ID: "project:skill:orders", Name: "orders", Kind: "skill", Scope: "project", Path: ".codegen/skills/project/orders/SKILL.md", Dir: ".codegen/skills/project/orders", Files: []string{"docs/api.md", "scripts/gen.sh"}}}
	out := workflow.Compose(wf, 0, p, libs, nil, "")
	for _, want := range []string{"`.codegen/skills/project/orders/SKILL.md`", "`docs/api.md`", "`scripts/gen.sh`", "relative to that folder"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt lacks %s:\n%s", want, out)
		}
	}
}
