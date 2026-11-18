package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
)

// A tool with its own folder takes its other files along; a Copilot
// extension folder can be exported and imported; a command using a file
// outside the tool gets a warning.
func TestToolFolderAndExtensionExport(t *testing.T) {
	src, srcRoot := newHub(t)
	saved, err := src.SaveExtension(extension.Extension{Name: "report", Desc: "Makes a report", Lang: "Python", Scope: "project",
		Deps: []string{"jinja2==3.1.4"}, Script: "import helpers\nprint(helpers.title())\n"})
	if err != nil && !strings.Contains(err.Error(), "installing its dependencies failed") {
		t.Fatal(err)
	}
	if saved.Dir == "" {
		saved, _ = src.exts.Get("report")
	}
	dir := filepath.Join(srcRoot, filepath.FromSlash(saved.Dir))
	os.WriteFile(filepath.Join(dir, "helpers.py"), []byte("def title():\n    return 'Report'\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "templates"), 0o755)
	os.WriteFile(filepath.Join(dir, "templates", "page.html"), []byte("<h1>{{ title }}</h1>"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".venv", "lib"), 0o755)
	os.WriteFile(filepath.Join(dir, ".venv", "lib", "big.so"), []byte("installed"), 0o644)

	// a script tool whose command uses a file elsewhere in the project
	os.MkdirAll(filepath.Join(srcRoot, "scripts"), 0o755)
	os.WriteFile(filepath.Join(srcRoot, "scripts", "count.py"), []byte("print(1)"), 0o644)
	if _, err := src.SaveExtension(extension.Extension{Name: "count", Desc: "Counts", Lang: "Python", Scope: "project", Command: "python3 scripts/count.py {{path}}",
		Params: []extension.Param{{Name: "path", Type: "string"}}}); err != nil {
		t.Fatal(err)
	}

	// a project Copilot extension
	ext := filepath.Join(srcRoot, ".github", "extensions", "lint-ext")
	os.MkdirAll(filepath.Join(ext, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(ext, "extension.mjs"), []byte("export default {}\n"), 0o644)
	os.WriteFile(filepath.Join(ext, "package.json"), []byte(`{"name":"lint-ext"}`), 0o644)
	os.WriteFile(filepath.Join(ext, "node_modules", "x", "index.js"), []byte("installed"), 0o644)

	exp, err := src.ExportTools([]string{"report", "count"}, []string{"project/lint-ext"})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Warnings) != 1 || !strings.Contains(exp.Warnings[0], "scripts/count.py") {
		t.Fatalf("warnings: %q", exp.Warnings)
	}
	if strings.Contains(exp.File, "aW5zdGFsbGVk") { // base64 of "installed": .venv and node_modules stay behind
		t.Fatal("installed packages were exported")
	}

	dst, dstRoot := newHub(t)
	pv, err := dst.PreviewToolImport(exp.File)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, it := range pv.Tools {
		if it.Tool.Name == "report" {
			files = it.Files
		}
	}
	if strings.Join(files, ",") != "helpers.py,requirements.txt,templates/page.html" {
		t.Fatalf("report's files: %q", files)
	}
	if len(pv.Extensions) != 1 || pv.Extensions[0].Name != "lint-ext" || !strings.Contains(pv.Extensions[0].Entry, "export default") ||
		strings.Join(pv.Extensions[0].Files, ",") != "extension.mjs,package.json" {
		t.Fatalf("extensions: %+v", pv.Extensions)
	}
	res, err := dst.ImportTools(exp.File, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Imported, ",") != "extension lint-ext,report,count" {
		t.Fatalf("imported %q", res.Imported)
	}
	got, _ := dst.exts.Get("report")
	if b, err := os.ReadFile(filepath.Join(dstRoot, filepath.FromSlash(got.Dir), "templates", "page.html")); err != nil || string(b) != "<h1>{{ title }}</h1>" {
		t.Fatalf("template not restored: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dstRoot, filepath.FromSlash(got.Dir), ".venv")); err == nil {
		t.Fatal("installed packages were restored")
	}
	if b, err := os.ReadFile(filepath.Join(dstRoot, ".github", "extensions", "lint-ext", "extension.mjs")); err != nil || !strings.Contains(string(b), "export default") {
		t.Fatalf("extension not written: %v", err)
	}
	// importing again: the extension folder exists, so it is skipped unless replaced
	pv, _ = dst.PreviewToolImport(exp.File)
	if !pv.Extensions[0].Exists {
		t.Fatal("existing extension not noticed")
	}
	res, _ = dst.ImportTools(exp.File, map[string]string{"report": "skip", "count": "skip"})
	if len(res.Imported) != 0 {
		t.Fatalf("second import: %+v", res)
	}
}

func TestBundlePathsAreChecked(t *testing.T) {
	dst, _ := newHub(t)
	for _, bad := range []string{
		`{"format":"specwright-tools","version":1,"tools":[{"name":"x","command":"echo","files":{"../evil.sh":"ZWNobw=="}}]}`,
		`{"format":"specwright-tools","version":1,"tools":[{"name":"x","command":"echo","files":{"/etc/x":"ZWNobw=="}}]}`,
		`{"format":"specwright-tools","version":1,"extensions":[{"name":"../x","files":{"extension.mjs":"eA=="}}]}`,
		`{"format":"specwright-tools","version":1,"extensions":[{"name":"ok","files":{".git/config":"eA=="}}]}`,
	} {
		if _, err := dst.PreviewToolImport(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
