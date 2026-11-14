package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleExt = `import { joinSession } from "@github/copilot-sdk/extension";
await joinSession({
  tools: [
    {
      name: "format_html",
      description:
        "Format an existing HTML file " +
        "in place.",
      parameters: { type: "object" },
      handler: async () => "ok",
    },
  ],
});
`

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopilotExtensionsListAndCopy(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	src := filepath.Join(home, "extensions", "html-formatter")
	write(t, filepath.Join(src, "extension.mjs"), sampleExt)
	write(t, filepath.Join(src, "package.json"), `{"dependencies":{"prettier":"3.6.2"}}`)
	write(t, filepath.Join(src, "README.md"), "# HTML formatter\n\nFormats HTML files.\n")
	write(t, filepath.Join(src, "node_modules", "prettier", "index.js"), "x")
	write(t, filepath.Join(home, "extensions", "not-an-ext", "notes.txt"), "no entry file")

	l := ListCopilotExts(root)
	if len(l) != 1 || l[0].Name != "html-formatter" || l[0].InProject || !l[0].Modules {
		t.Fatalf("%+v", l)
	}
	x := l[0]
	if len(x.Tools) != 1 || x.Tools[0].Name != "format_html" || x.Tools[0].Desc != "Format an existing HTML file in place." {
		t.Fatalf("tools: %+v", x.Tools)
	}
	if x.Desc != "Formats HTML files." || x.Deps["prettier"] != "3.6.2" {
		t.Fatalf("desc/deps: %q %v", x.Desc, x.Deps)
	}
	for _, f := range x.Files {
		if strings.HasPrefix(f, "node_modules") {
			t.Fatalf("node_modules listed: %v", x.Files)
		}
	}

	dst, err := CopyCopilotExt(root, "html-formatter", false, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "extension.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "node_modules")); err == nil {
		t.Fatal("node_modules must not be copied")
	}
	l = ListCopilotExts(root)
	if len(l) != 2 || !l[0].InProject || !l[0].ProjectSame || l[1].Source != "project" || !l[1].FromUser {
		t.Fatalf("after copy: %+v", l)
	}
	if !HasProjectCopilotExts(root) {
		t.Fatal("project has an extension now")
	}

	// a second copy needs replace; a change shows as "differs"
	if _, err := CopyCopilotExt(root, "html-formatter", false, nil); err == nil {
		t.Fatal("existing copy must not be overwritten without replace")
	}
	write(t, filepath.Join(src, "extension.mjs"), sampleExt+"// v2\n")
	if l = ListCopilotExts(root); l[0].ProjectSame {
		t.Fatal("changed source must differ from the project copy")
	}
	var trashed string
	if _, err := CopyCopilotExt(root, "html-formatter", true, func(p string) error { trashed = p; return os.Rename(p, p+".old") }); err != nil || trashed == "" {
		t.Fatalf("replace: %v %q", err, trashed)
	}
	if l = ListCopilotExts(root); !l[0].ProjectSame {
		t.Fatal("replaced copy must match")
	}

	for _, bad := range []string{"", "..", "a/b", ".hidden"} {
		if _, err := CopyCopilotExt(root, bad, true, nil); err == nil {
			t.Fatalf("name %q must be rejected", bad)
		}
	}
	if added, err := IgnoreCopilotExtPackages(root); err != nil || !added {
		t.Fatal(added, err)
	}
	if added, _ := IgnoreCopilotExtPackages(root); added {
		t.Fatal("lines must be added once")
	}
}

// A bundled driver jar (ojdbc11.jar is about 7 MB) is copied; a file over
// the per-file limit is still refused.
func TestCopilotExtCopyFileSizes(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("COPILOT_HOME", home)
	src := filepath.Join(home, "extensions", "oracle-db")
	write(t, filepath.Join(src, "extension.mjs"), sampleExt)
	jar := filepath.Join(src, "jdbc", "ojdbc11.jar")
	write(t, jar, strings.Repeat("j", 7<<20))
	dst, err := CopyCopilotExt(root, "oracle-db", false, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "jdbc", "ojdbc11.jar")); err != nil || fi.Size() != 7<<20 {
		t.Fatalf("jar not copied: %v", err)
	}
	if err := os.Truncate(jar, maxCopilotExtFile+1); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyCopilotExt(root, "oracle-db", true, func(p string) error { return os.RemoveAll(p) }); err == nil || !strings.Contains(err.Error(), "larger than 25 MB") {
		t.Fatalf("want a size error, got %v", err)
	}
}
