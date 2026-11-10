package extension

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A tool with dependencies lives in its own folder,
// .codegen/tools/<scope>/<name>/, next to its manifest and its installed
// packages (node_modules, .venv, bin, lib). Downloads go through the
// workspace's PackageSettings and are cached in .codegen/cache/<ecosystem>.

// HasDeps reports whether e needs a dependency install.
func HasDeps(e Extension) bool { return len(e.Deps) > 0 }

// EntryFile is the source file name of a folder-layout tool.
func EntryFile(lang string) string {
	switch lang {
	case "Node.js":
		return "index.js"
	case "Python":
		return "main.py"
	case "Go":
		return "main.go"
	case "Java":
		return "Tool.java"
	}
	return "tool.sh"
}

// ToolDir is the workspace-relative folder of a folder-layout tool.
func ToolDir(scope, name string) string {
	if scope != "global" {
		scope = "project"
	}
	return ".codegen/tools/" + scope + "/" + name
}

// DepsHint shows the dependency spelling the installer expects per language.
var DepsHint = map[string]string{
	"Node.js": "pg@8.13.1",
	"Python":  "psycopg[binary]==3.2.3",
	"Go":      "github.com/jackc/pgx/v5@v5.7.1",
	"Java":    "org.postgresql:postgresql:42.7.4",
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// RunCommand is the command template of a folder-layout tool in dir (as
// the command should name it: relative to the workspace, or absolute),
// with one {{param}} per parameter in declaration order.
func RunCommand(e Extension, dir string) string {
	p := func(parts ...string) string {
		return QuoteArg(filepath.ToSlash(filepath.Join(append([]string{dir}, parts...)...)))
	}
	var cmd string
	switch e.Lang {
	case "Node.js":
		cmd = "node " + p("index.js")
	case "Python":
		if runtime.GOOS == "windows" {
			cmd = p(".venv", "Scripts", "python.exe") + " " + p("main.py")
		} else {
			cmd = p(".venv", "bin", "python") + " " + p("main.py")
		}
	case "Go":
		cmd = p("bin", exe(e.Name))
	case "Java":
		cmd = "java -cp " + p("lib", "*") + " " + p("Tool.java")
	default:
		cmd = "bash " + p("tool.sh")
	}
	for _, pa := range e.Params {
		cmd += " {{" + pa.Name + "}}"
	}
	return cmd
}

// WriteTool writes a folder-layout tool (entry file plus manifest) into
// absDir and returns e with Dir, ScriptPath and Command pointing at cmdDir.
func WriteTool(absDir, cmdDir string, e Extension) (Extension, error) {
	if e.Lang == "Bash" || e.Lang == "" {
		return e, errors.New("Bash tools cannot declare dependencies; use Node.js, Go, Java or Python")
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return e, err
	}
	files := map[string]string{EntryFile(e.Lang): e.Script}
	switch e.Lang {
	case "Node.js":
		deps := map[string]string{}
		for _, d := range e.Deps {
			n, v := splitNpm(d)
			deps[n] = v
		}
		b, _ := json.MarshalIndent(map[string]any{"name": e.Name, "private": true, "version": "0.0.0", "dependencies": deps}, "", "  ")
		files["package.json"] = string(b) + "\n"
	case "Python":
		files["requirements.txt"] = strings.Join(e.Deps, "\n") + "\n"
	case "Go":
		if _, err := os.Stat(filepath.Join(absDir, "go.mod")); err != nil {
			files["go.mod"] = "module codegen.local/tools/" + e.Name + "\n\ngo 1.21\n"
		}
	case "Java":
		var b strings.Builder
		b.WriteString("<project xmlns=\"http://maven.apache.org/POM/4.0.0\">\n  <modelVersion>4.0.0</modelVersion>\n  <groupId>codegen.tools</groupId>\n")
		fmt.Fprintf(&b, "  <artifactId>%s</artifactId>\n  <version>0.0.0</version>\n  <packaging>pom</packaging>\n  <dependencies>\n", xmlEsc(e.Name))
		for _, d := range e.Deps {
			p := strings.Split(d, ":")
			if len(p) < 3 {
				return e, fmt.Errorf("Maven dependency %q: want group:artifact:version", d)
			}
			fmt.Fprintf(&b, "    <dependency><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version></dependency>\n", xmlEsc(p[0]), xmlEsc(p[1]), xmlEsc(p[len(p)-1]))
		}
		b.WriteString("  </dependencies>\n</project>\n")
		files["pom.xml"] = b.String()
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(absDir, n), []byte(c), 0o644); err != nil {
			return e, err
		}
	}
	e.Dir = filepath.ToSlash(cmdDir)
	e.ScriptPath = filepath.ToSlash(filepath.Join(cmdDir, EntryFile(e.Lang)))
	e.Command = RunCommand(e, cmdDir)
	return e, nil
}

// splitNpm splits "pg@8.13.1" or "@scope/pkg@^1" into name and version.
func splitNpm(d string) (string, string) {
	at := strings.LastIndex(d, "@")
	if at <= 0 {
		return d, "*"
	}
	return d[:at], d[at+1:]
}

func depsHash(e Extension, s PackageSettings) string {
	k := e.Lang + "\x00" + strings.Join(e.Deps, "\n")
	if e.Lang == "Go" || e.Lang == "Java" {
		k += "\x00" + e.Script // compiled / launched with the jars
	}
	k += "\x00" + s.Npm.Registry + s.Pip.IndexURL + s.Go.Proxy + s.Maven.MirrorURL
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:10])
}

const marker = ".codegen-installed"

// Installed reports whether absDir already holds e's dependencies.
func Installed(absDir string, e Extension, s PackageSettings) bool {
	b, err := os.ReadFile(filepath.Join(absDir, marker))
	return err == nil && strings.TrimSpace(string(b)) == depsHash(e, s)
}

// Install downloads e's dependencies into absDir (workspace root for the
// caches, s for proxy and registries), streaming output lines to log.
func Install(ctx context.Context, root, absDir string, e Extension, s PackageSettings, log func(string)) error {
	if Installed(absDir, e, s) {
		log("Dependencies already installed.")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	env := append(os.Environ(), s.Env(root)...)
	run := func(name string, args ...string) error {
		bin, err := exec.LookPath(name)
		if err != nil && !strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("%s is needed to install %s dependencies but was not found on PATH", name, e.Lang)
		}
		if bin == "" {
			bin = name
		}
		log("$ " + JoinWords(append([]string{name}, args...)))
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Env = absDir, env
		pr, pw, _ := os.Pipe()
		cmd.Stdout, cmd.Stderr = pw, pw
		if err := cmd.Start(); err != nil {
			pw.Close()
			pr.Close()
			return err
		}
		pw.Close()
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			if l := strings.TrimRight(sc.Text(), " "); l != "" {
				log(l)
			}
		}
		pr.Close()
		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%s timed out", name)
			}
			return fmt.Errorf("%s %s failed: %v", name, args[0], err)
		}
		return nil
	}
	var err error
	switch e.Lang {
	case "Node.js":
		err = run("npm", "install", "--no-audit", "--no-fund", "--no-package-lock", "--omit=dev", "--loglevel=error")
	case "Python":
		py := "python3"
		if _, e := exec.LookPath(py); e != nil {
			py = "python"
		}
		vpy := filepath.Join(absDir, ".venv", "bin", "python")
		if runtime.GOOS == "windows" {
			vpy = filepath.Join(absDir, ".venv", "Scripts", "python.exe")
		}
		if err = run(py, "-m", "venv", ".venv"); err == nil {
			err = run(vpy, "-m", "pip", "install", "--disable-pip-version-check", "-q", "-r", "requirements.txt")
		}
	case "Go":
		if len(e.Deps) > 0 {
			err = run("go", append([]string{"get"}, e.Deps...)...)
		}
		if err == nil {
			err = run("go", "mod", "tidy")
		}
		if err == nil {
			err = run("go", "build", "-o", filepath.Join("bin", exe(e.Name)), ".")
		}
	case "Java":
		var settings string
		if settings, err = s.MavenSettings(root); err == nil {
			mvn := "mvn"
			if runtime.GOOS == "windows" {
				mvn = "mvn.cmd"
			}
			err = run(mvn, "-q", "-B", "-s", settings, "dependency:copy-dependencies", "-DoutputDirectory=lib", "-DincludeScope=runtime")
		}
	default:
		err = fmt.Errorf("%s tools cannot declare dependencies", firstNonEmpty(e.Lang, "Bash"))
	}
	if err != nil {
		return err
	}
	log("Dependencies installed.")
	return os.WriteFile(filepath.Join(absDir, marker), []byte(depsHash(e, s)+"\n"), 0o644)
}

// EnsureGitignore keeps the caches and installed packages out of git.
func EnsureGitignore(root string) {
	p := filepath.Join(root, ".codegen", ".gitignore")
	if _, err := os.Stat(p); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("# package caches and installed tool dependencies\ncache/\ntools/**/node_modules/\ntools/**/.venv/\ntools/**/bin/\ntools/**/lib/\ntools/**/"+marker+"\n"), 0o644)
}
