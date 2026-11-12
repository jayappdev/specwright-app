package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
)

func TestArchiveTool(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	saved, err := h.SaveExtension(extension.Extension{Name: "say-hi", Desc: "Hi", Lang: "Bash", Scope: "project", Command: "bash say-hi.sh", Script: "echo hi\n"})
	if err != nil || saved.ScriptPath == "" {
		t.Fatalf("%+v %v", saved, err)
	}
	if _, err := os.Stat(filepath.Join(ws, saved.ScriptPath)); err != nil {
		t.Fatal(err)
	}

	r, err := h.ArchiveTool("say-hi")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Path, ".codegen/archive/") || len(r.Moved) != 1 || r.Moved[0] != saved.ScriptPath {
		t.Fatalf("%+v", r)
	}
	arch := filepath.Join(ws, filepath.FromSlash(r.Path))
	for _, f := range []string{"ARCHIVED.md", "tools/say-hi/tool.json", "tools/say-hi/" + filepath.Base(saved.ScriptPath)} {
		if _, err := os.Stat(filepath.Join(arch, filepath.FromSlash(f))); err != nil {
			t.Fatalf("archive is missing %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, saved.ScriptPath)); err == nil {
		t.Fatal("the script must have moved")
	}
	if _, err := h.exts.Get("say-hi"); err == nil {
		t.Fatal("the tool must be gone from extensions.json")
	}
	if _, err := h.ArchiveTool("say-hi"); err == nil {
		t.Fatal("archiving twice must fail")
	}

	// a second delete in the same second gets its own folder
	if _, err := h.SaveExtension(extension.Extension{Name: "say-bye", Desc: "Bye", Lang: "Bash", Scope: "project", Command: "bash say-bye.sh", Script: "echo bye\n"}); err != nil {
		t.Fatal(err)
	}
	r2, err := h.ArchiveTool("say-bye")
	if err != nil || r2.Path == r.Path {
		t.Fatalf("%+v %v", r2, err)
	}
}

func TestArchiveCopilotExtensionTool(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	usePolicy(t, "")
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	src := filepath.Join(home, "extensions", "greeter")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.mjs"), []byte(greeterExt), 0o644); err != nil {
		t.Fatal(err)
	}
	h, ws := newHub(t)
	if _, err := h.LoadCopilotExtension("greeter", false, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".github", "extensions", "greeter", "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := h.ArchiveTool("greet")
	if err != nil || len(r.Tools) != 1 || r.Tools[0] != "greet" {
		t.Fatalf("%+v %v", r, err)
	}
	arch := filepath.Join(ws, filepath.FromSlash(r.Path))
	if _, err := os.Stat(filepath.Join(arch, "copilot-extensions", "greeter", "extension.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(arch, "copilot-extensions", "greeter", "node_modules")); err == nil {
		t.Fatal("installed packages are not archived")
	}
	if _, err := os.Stat(filepath.Join(arch, "copilot-extensions", "greeter.tools.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".github", "extensions", "greeter")); err == nil {
		t.Fatal("the extension must have left .github/extensions")
	}
	if _, err := os.Stat(filepath.Join(src, "extension.mjs")); err != nil {
		t.Fatal("your own copy must stay")
	}
	for _, e := range h.Callable() {
		if e.Name == "greet" {
			t.Fatal("archived tool must not be callable")
		}
	}
}
