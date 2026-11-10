package extension

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPackageSettingsCredsStayInMemory(t *testing.T) {
	root := t.TempDir()
	ps := NewPackageStore(root)
	var s PackageSettings
	s.Proxy.HTTPS = "http://alice:s3cret@proxy.corp:8080"
	s.Npm.Registry = "https://nexus.corp/repository/npm/"
	s.Maven.MirrorURL = "https://nexus.corp/repository/maven-public/"
	s.Proxy.NoProxy = "localhost,.corp"
	if err := ps.Save(s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".codegen", "packages.json"))
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "alice") {
		t.Fatalf("credentials written to disk: %s", b)
	}
	got, _ := ps.Load()
	if got.Proxy.HTTPS != "http://alice:s3cret@proxy.corp:8080" {
		t.Fatalf("credentials lost: %s", got.Proxy.HTTPS)
	}
	pub := got.Public()
	if strings.Contains(pub.Proxy.HTTPS, "s3cret") || !strings.Contains(pub.Proxy.HTTPS, "xxxxx") {
		t.Fatalf("not masked: %s", pub.Proxy.HTTPS)
	}
	// saving the masked value back keeps the real password
	if err := ps.Save(pub); err != nil {
		t.Fatal(err)
	}
	if again, _ := ps.Load(); again.Proxy.HTTPS != got.Proxy.HTTPS {
		t.Fatalf("masked save replaced the password: %s", again.Proxy.HTTPS)
	}
	env := strings.Join(got.Env(root), "\n")
	for _, want := range []string{"HTTPS_PROXY=http://alice:s3cret@proxy.corp:8080", "npm_config_registry=https://nexus.corp/repository/npm/", "NO_PROXY=localhost,.corp", "GOMODCACHE=" + CacheDir(root, "go-mod")} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %s:\n%s", want, env)
		}
	}
	xml, err := got.MavenSettings(root)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := os.ReadFile(xml)
	for _, want := range []string{"<url>https://nexus.corp/repository/maven-public/</url>", "<host>proxy.corp</host><port>8080</port>", "<username>alice</username>", "<nonProxyHosts>localhost|.corp</nonProxyHosts>", "<localRepository>"} {
		if !strings.Contains(string(x), want) {
			t.Fatalf("maven settings missing %s:\n%s", want, x)
		}
	}
	if fi, _ := os.Stat(xml); fi.Mode().Perm() != 0o600 {
		t.Fatalf("maven settings with credentials must be private: %v", fi.Mode())
	}
	if err := ps.Save(PackageSettings{Npm: struct {
		Registry string `json:"registry"`
	}{Registry: "nexus.corp"}}); err == nil {
		t.Fatal("a registry without scheme must be rejected")
	}
}

func TestRunCommandLayout(t *testing.T) {
	e := Extension{Name: "q", Params: []Param{{Name: "sql"}, {Name: "limit"}}}
	for lang, want := range map[string]string{
		"Node.js": "node '/a b/q/index.js' {{sql}} {{limit}}",
		"Go":      "'/a b/q/bin/q' {{sql}} {{limit}}",
		"Java":    "java -cp '/a b/q/lib/*' '/a b/q/Tool.java' {{sql}} {{limit}}",
	} {
		e.Lang = lang
		if got := RunCommand(e, "/a b/q"); got != want {
			t.Errorf("%s: got %s want %s", lang, got, want)
		}
	}
	if w := SplitWords(RunCommand(Extension{Lang: "Node.js"}, "/a b/q")); len(w) != 2 || w[1] != "/a b/q/index.js" {
		t.Fatalf("quoted path must stay one word: %q", w)
	}
}

// End to end and offline: a Node.js tool depending on a local package.
func TestNodeDepsInstallAndExec(t *testing.T) {
	if _, err := os.Stat("/usr/bin/true"); err != nil {
		t.Skip()
	}
	if !hasBin("npm") {
		t.Skip("npm not installed")
	}
	root := t.TempDir()
	lib := filepath.Join(t.TempDir(), "mylib")
	_ = os.MkdirAll(lib, 0o755)
	_ = os.WriteFile(filepath.Join(lib, "package.json"), []byte(`{"name":"mylib","version":"1.0.0","main":"index.js"}`), 0o644)
	_ = os.WriteFile(filepath.Join(lib, "index.js"), []byte(`exports.hi = (n) => "hi " + n;`), 0o644)
	e := Extension{Name: "greet", Lang: "Node.js", Kind: "script", Params: []Param{{Name: "who", Required: true}},
		Deps: []string{"mylib@file:" + lib}, Script: `console.log(require("mylib").hi(process.argv[2]));`}
	saved, err := NewStore(root).Save(e)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Dir != ".codegen/tools/project/greet" || !strings.HasPrefix(saved.Command, "node .codegen/tools/project/greet/index.js") {
		t.Fatalf("layout %+v", saved)
	}
	dir := filepath.Join(root, filepath.FromSlash(saved.Dir))
	var log []string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Install(ctx, root, dir, saved, PackageSettings{}, func(l string) { log = append(log, l) }); err != nil {
		t.Fatalf("install: %v\n%s", err, strings.Join(log, "\n"))
	}
	if !Installed(dir, saved, PackageSettings{}) {
		t.Fatal("marker not written")
	}
	r := Exec(ctx, root, saved, map[string]any{"who": "corp"}, nil, nil, 0)
	if !r.OK || r.Output != "hi corp" {
		t.Fatalf("exec %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, ".codegen", ".gitignore")); err != nil {
		t.Fatal("caches must be git-ignored")
	}
}

func hasBin(n string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if fi, err := os.Stat(filepath.Join(d, n)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}
