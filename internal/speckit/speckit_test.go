package speckit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitFromDownload(t *testing.T) {
	cache := Cache{Dir: t.TempDir()}
	for _, in := range Integrations {
		if err := WriteSample(cache.Source("1.4.0", in), in, "1.4.0"); err != nil {
			t.Fatal(err)
		}
	}
	if cache.Latest() != "1.4.0" {
		t.Fatal(cache.Latest())
	}
	root := t.TempDir()
	if _, err := Init(root, filepath.Join(t.TempDir(), "none"), "claude"); err == nil {
		t.Fatal("nothing downloaded: must fail")
	}
	res, err := Init(root, cache.Source("1.4.0", "claude"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "1.4.0" || !slices.Contains(res.Created, ".claude/skills/speckit-plan/SKILL.md") || !slices.Contains(res.Created, Constitution) {
		t.Fatalf("%+v", res)
	}
	if fi, _ := os.Stat(filepath.Join(root, ".specify/scripts/bash/common.sh")); fi.Mode()&0o111 == 0 {
		t.Fatal("scripts must be executable")
	}
	st := Status(root)
	if !st.Initialized || st.Integration != "claude" || st.Constitution {
		t.Fatalf("%+v", st)
	}
	// local changes survive; the other integration is added
	_ = os.WriteFile(filepath.Join(root, Constitution), []byte("# Orders Constitution\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, ".specify/templates/spec-template.md"), []byte("# ours\n"), 0o644)
	_ = os.Remove(filepath.Join(root, ".specify/templates/plan-template.md"))
	res, err = Init(root, cache.Source("1.4.0", "copilot"), "copilot")
	if err != nil {
		t.Fatal(err)
	}
	if read(t, root, Constitution) != "# Orders Constitution\n" || read(t, root, ".specify/templates/spec-template.md") != "# ours\n" ||
		!slices.Contains(res.Created, ".specify/templates/plan-template.md") || !slices.Contains(res.Created, ".github/skills/speckit-tasks/SKILL.md") {
		t.Fatalf("%+v", res)
	}
	st = Status(root)
	if st.Integration != "copilot" || !slices.Equal(st.Integrations, []string{"claude", "copilot"}) || !st.Constitution {
		t.Fatalf("%+v", st)
	}
	if s := StepFor(root, "copilot", "plan"); s.Invoke != "/speckit-plan" || s.Skill != ".github/skills/speckit-plan/SKILL.md" {
		t.Fatalf("%+v", s)
	}
}

// Download runs Spec Kit's CLI (here a fake uvx) with the proxy settings.
func TestDownloadRunsTheCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	bin := t.TempDir()
	self, _ := os.Executable()
	// the fake uvx records its arguments and environment, then makes the project
	script := "#!/bin/sh\necho \"$@\" >> \"" + filepath.Join(bin, "calls") + "\"\necho \"$HTTPS_PROXY\" > \"" + filepath.Join(bin, "proxy") + "\"\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in init) dir=\"$2\";; --integration) in=\"$2\";; esac; shift; done\n" +
		"SPECKIT_SAMPLE=\"$PWD/$dir|$in\" \"" + self + "\" -test.run=TestHelperSample >/dev/null\n"
	_ = os.WriteFile(filepath.Join(bin, "uvx"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cache := Cache{Dir: t.TempDir()}
	var lines []string
	err := cache.Download(context.Background(), "1.4.0", Net{Env: []string{"HTTPS_PROXY=http://proxy.corp:8080"}}, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	calls := read(t, bin, "calls")
	if !strings.Contains(calls, "--from "+ArchiveURL("1.4.0")+" specify init claude --integration claude") || !strings.Contains(calls, "init copilot --integration copilot") {
		t.Fatal(calls)
	}
	if strings.TrimSpace(read(t, bin, "proxy")) != "http://proxy.corp:8080" {
		t.Fatal("the proxy must reach the CLI")
	}
	if cache.Latest() != "1.4.0" || !strings.Contains(strings.Join(lines, "\n"), "ready") {
		t.Fatal(cache.Latest(), lines)
	}
	if err := cache.Download(context.Background(), "not-a-version", Net{}, nil); err == nil {
		t.Fatal("bad version")
	}
}

// TestHelperSample is the fake CLI's body (run through the test binary).
func TestHelperSample(t *testing.T) {
	v := os.Getenv("SPECKIT_SAMPLE")
	if v == "" {
		t.Skip("helper")
	}
	dir, in, _ := strings.Cut(v, "|")
	if err := WriteSample(dir, in, "1.4.0"); err != nil {
		t.Fatal(err)
	}
}

func TestVersionOrder(t *testing.T) {
	if !versionLess("1.4.0", "1.10.0") || versionLess("2.0.0", "1.9.9") || !versionLess("1.4.0", "1.4.1") {
		t.Fatal("version order")
	}
}
