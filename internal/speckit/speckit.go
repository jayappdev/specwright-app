// Package speckit sets a project up with GitHub Spec Kit.
//
// SpecWright does not ship Spec Kit. It downloads it on this computer the
// first time it is needed (when proxies are first set up, or at the first
// "Set up Spec Kit"), through the configured proxy, CA and package index,
// by running Spec Kit's own CLI from its GitHub release:
//
//	uvx --from https://github.com/github/spec-kit/archive/refs/tags/v<version>.zip \
//	  specify init proj --integration <claude|copilot> --non-interactive --ignore-agent-tools --script sh
//
// (or pipx, or a Python virtual environment, when uv is not installed). What
// the CLI generates is kept per version in a cache folder, and Init copies it
// into projects: the project's own changes and a written constitution are
// never overwritten.
package speckit

import (
	"codegen/internal/safeenv"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

// DefaultVersion is used when the latest release cannot be looked up.
const DefaultVersion = "1.4.0"

// Repo is where Spec Kit comes from.
const Repo = "github/spec-kit"

// Integrations are the agent integrations SpecWright sets up.
var Integrations = []string{"claude", "copilot"}

// Constitution is the project constitution; Init never overwrites one
// that has been filled in.
const Constitution = ".specify/memory/constitution.md"

// Result says what Init did.
type Result struct {
	Version     string   `json:"version"`
	Integration string   `json:"integration"`
	Created     []string `json:"created"`
	Updated     []string `json:"updated"` // Spec Kit files refreshed to the downloaded version
	Kept        []string `json:"kept"`    // existing files left alone (changed locally, or the constitution)
}

// Info describes a project's Spec Kit setup.
type Info struct {
	Initialized  bool     `json:"initialized"`
	Version      string   `json:"version,omitempty"`
	Integration  string   `json:"integration,omitempty"`  // the default integration
	Integrations []string `json:"integrations,omitempty"` // every installed one
	Constitution bool     `json:"constitution"`           // the constitution has been written (not the blank template)
	Project      string   `json:"project"`                // the project folder in the workspace ("" = the workspace)
	// the download on this computer
	Available   string `json:"available,omitempty"` // the downloaded version ("" = not downloaded yet)
	Downloading bool   `json:"downloading,omitempty"`
	Progress    string `json:"progress,omitempty"` // the last line of the download
	Error       string `json:"error,omitempty"`    // why the last download failed
}

// Status reads a project's Spec Kit setup.
func Status(root string) Info {
	var in Info
	if fi, err := os.Stat(filepath.Join(root, ".specify")); err != nil || !fi.IsDir() {
		return in
	}
	in.Initialized = true
	var ij struct {
		Version     string   `json:"version"`
		Installed   []string `json:"installed_integrations"`
		Integration string   `json:"integration"`
	}
	if b, err := os.ReadFile(filepath.Join(root, ".specify", "integration.json")); err == nil && json.Unmarshal(b, &ij) == nil {
		in.Version, in.Integration, in.Integrations = ij.Version, ij.Integration, ij.Installed
	}
	if in.Integration == "" { // older layouts: guess from the agent folders
		for _, c := range []struct{ name, dir string }{{"claude", ".claude"}, {"copilot", ".github/prompts"}, {"copilot", ".github/agents"}, {"copilot", ".github/skills"}} {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.dir))); err == nil && !slices.Contains(in.Integrations, c.name) {
				in.Integrations = append(in.Integrations, c.name)
			}
		}
		if len(in.Integrations) > 0 {
			in.Integration = in.Integrations[0]
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Constitution))); err == nil {
		in.Constitution = !strings.Contains(string(b), "[PROJECT_NAME]") && !strings.Contains(string(b), "[PRINCIPLE_1_NAME]")
	}
	return in
}

// --- the download ------------------------------------------------------------------------

// Cache is where downloaded Spec Kit versions are kept:
// <dir>/<version>/<integration>/ holds what `specify init` generated.
type Cache struct{ Dir string }

var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$`)

// Latest returns the newest downloaded version ("" = none).
func (c Cache) Latest() string {
	ents, _ := os.ReadDir(c.Dir)
	var vs []string
	for _, e := range ents {
		if e.IsDir() && versionRE.MatchString(e.Name()) && c.complete(e.Name()) {
			vs = append(vs, e.Name())
		}
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	if len(vs) == 0 {
		return ""
	}
	return vs[len(vs)-1]
}

func (c Cache) complete(v string) bool {
	for _, in := range Integrations {
		if _, err := os.Stat(filepath.Join(c.Dir, v, in, ".specify")); err != nil {
			return false
		}
	}
	return true
}

// Source is the generated project for version and integration.
func (c Cache) Source(version, integration string) string {
	return filepath.Join(c.Dir, version, integration)
}

func versionLess(a, b string) bool {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' })
	}
	pa, pb := split(a), split(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		var x, y int
		_, ex := fmt.Sscan(pa[i], &x)
		_, ey := fmt.Sscan(pb[i], &y)
		switch {
		case ex == nil && ey == nil:
			if x != y {
				return x < y
			}
		case pa[i] != pb[i]:
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// Net is how the download reaches the internet.
type Net struct {
	Env    []string     // proxy, CA and package-index variables for the CLI tools
	Client *http.Client // for looking up the latest release (nil = default)
}

// LatestRelease asks GitHub for the newest Spec Kit release; it falls back
// to DefaultVersion.
func LatestRelease(ctx context.Context, n Net) string {
	cl := n.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "SpecWright")
	resp, err := cl.Do(req)
	if err != nil {
		return DefaultVersion
	}
	defer resp.Body.Close()
	var r struct {
		Tag string `json:"tag_name"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&r) != nil {
		return DefaultVersion
	}
	if v := strings.TrimPrefix(r.Tag, "v"); versionRE.MatchString(v) {
		return v
	}
	return DefaultVersion
}

// ArchiveURL is the release's source archive (no git needed).
func ArchiveURL(version string) string {
	return "https://github.com/" + Repo + "/archive/refs/tags/v" + version + ".zip"
}

// Download runs Spec Kit's CLI for every integration and keeps what it
// generates in the cache. log gets progress lines.
func (c Cache) Download(ctx context.Context, version string, n Net, log func(string)) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("%q is not a Spec Kit version", version)
	}
	if log == nil {
		log = func(string) {}
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(c.Dir, ".download-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	run, err := cliRunner(ctx, c.Dir, version, n, log)
	if err != nil {
		return err
	}
	for _, in := range Integrations {
		log("Running specify init for " + in + "…")
		cmd := run("init", in, "--integration", in, "--non-interactive", "--ignore-agent-tools", "--script", "sh")
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("specify init (%s) failed: %v\n%s", in, err, tail(string(out), 1500))
		}
		if _, err := os.Stat(filepath.Join(work, in, ".specify")); err != nil {
			return fmt.Errorf("specify init (%s) did not create .specify/:\n%s", in, tail(string(out), 1500))
		}
	}
	dest := filepath.Join(c.Dir, version)
	_ = os.RemoveAll(dest)
	if err := os.Rename(work, dest); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dest, "SOURCE.txt"), []byte("GitHub Spec Kit "+version+", generated on this computer with its own CLI from "+ArchiveURL(version)+"\n"), 0o644)
	log("Spec Kit " + version + " is ready.")
	return nil
}

// cliRunner finds a way to run Spec Kit's CLI: uvx, pipx, or a virtual
// environment made with python3 in the cache.
func cliRunner(ctx context.Context, cacheDir, version string, n Net, log func(string)) (func(args ...string) *exec.Cmd, error) {
	env := append(safeenv.Clean(os.Environ()), n.Env...)
	env = append(env, "PYTHONIOENCODING=utf-8", "NO_COLOR=1", "TERM=dumb", "COLUMNS=200")
	src := ArchiveURL(version)
	mk := func(name string, pre ...string) func(args ...string) *exec.Cmd {
		return func(args ...string) *exec.Cmd {
			c := exec.CommandContext(ctx, name, append(append([]string{}, pre...), args...)...)
			c.Env = env
			return c
		}
	}
	if p, err := exec.LookPath("uvx"); err == nil {
		log("Downloading Spec Kit " + version + " with uv from " + src + "…")
		return mk(p, "--from", src, "specify"), nil
	}
	if p, err := exec.LookPath("pipx"); err == nil {
		log("Downloading Spec Kit " + version + " with pipx from " + src + "…")
		return mk(p, "run", "--spec", src, "specify"), nil
	}
	py := ""
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			py = p
			break
		}
	}
	if py == "" {
		return nil, errors.New("Spec Kit's CLI needs uv, pipx or Python 3.11+ on this computer (e.g. brew install uv), and none was found")
	}
	venv := filepath.Join(cacheDir, ".venv-"+version)
	bin := filepath.Join(venv, "bin")
	if runtime.GOOS == "windows" {
		bin = filepath.Join(venv, "Scripts")
	}
	specify := filepath.Join(bin, "specify")
	if _, err := os.Stat(specify); err != nil {
		log("Creating a Python environment for Spec Kit " + version + "…")
		c := exec.CommandContext(ctx, py, "-m", "venv", venv)
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("python -m venv failed: %v\n%s", err, tail(string(out), 800))
		}
		log("Downloading Spec Kit " + version + " with pip from " + src + "…")
		c = exec.CommandContext(ctx, filepath.Join(bin, "python"), "-m", "pip", "install", "--disable-pip-version-check", "-q", src)
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			_ = os.RemoveAll(venv)
			return nil, fmt.Errorf("pip install of Spec Kit failed: %v\n%s", err, tail(string(out), 1500))
		}
	}
	return mk(specify), nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// --- setting a project up -------------------------------------------------------------

// bookkeeping is Spec Kit's per-project state, written fresh by Init.
func bookkeeping(rel string) bool {
	return rel == ".specify/init-options.json" || rel == ".specify/workflows/workflow-registry.json" ||
		(strings.HasPrefix(rel, ".specify/integrations/") && strings.HasSuffix(rel, ".manifest.json"))
}

// Init copies the Spec Kit project generated for integration (src, a cache
// folder) into root. It can be run again: to add the other integration, or
// to refresh Spec Kit's files. A Spec Kit file is replaced only when it
// still matches what was installed (its hash in the manifests); local
// changes and a written constitution are kept.
func Init(root, src, integration string) (Result, error) {
	if !slices.Contains(Integrations, integration) {
		return Result{}, fmt.Errorf("unknown integration %q (use %s)", integration, strings.Join(Integrations, " or "))
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return Result{}, fmt.Errorf("%s is not a folder", root)
	}
	if fi, err := os.Stat(filepath.Join(src, ".specify")); err != nil || !fi.IsDir() {
		return Result{}, errors.New("Spec Kit has not been downloaded yet")
	}
	res := Result{Integration: integration}
	var gen struct {
		Version string `json:"version"`
	}
	if b, err := os.ReadFile(filepath.Join(src, ".specify", "integration.json")); err == nil {
		_ = json.Unmarshal(b, &gen)
	}
	res.Version = gen.Version
	installed := installedHashes(root)
	var prev map[string]any
	if b, err := os.ReadFile(filepath.Join(root, ".specify", "integration.json")); err == nil {
		_ = json.Unmarshal(b, &prev)
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".specify/integration.json" || rel == ".specify/feature.json" {
			return nil // merged below / machine-local
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if fi, err := d.Info(); (err == nil && fi.Mode()&0o111 != 0) || strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		action, err := place(root, rel, b, mode, installed, bookkeeping(rel))
		if err != nil {
			return err
		}
		switch action {
		case "created":
			res.Created = append(res.Created, rel)
		case "updated":
			if !bookkeeping(rel) {
				res.Updated = append(res.Updated, rel)
			}
		case "kept":
			res.Kept = append(res.Kept, rel)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if err := mergeIntegration(root, src, integration, prev); err != nil {
		return res, err
	}
	if err := os.MkdirAll(filepath.Join(root, "specs"), 0o755); err != nil {
		return res, err
	}
	sort.Strings(res.Created)
	sort.Strings(res.Updated)
	sort.Strings(res.Kept)
	return res, nil
}

// installedHashes are the hashes of the files Spec Kit installed before.
func installedHashes(root string) map[string]string {
	out := map[string]string{}
	ms, _ := filepath.Glob(filepath.Join(root, ".specify", "integrations", "*.manifest.json"))
	for _, m := range ms {
		var mf struct {
			Files map[string]string `json:"files"`
		}
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &mf) == nil {
			for k, v := range mf.Files {
				out[k] = v
			}
		}
	}
	return out
}

// mergeIntegration writes integration.json with every integration
// installed so far, the newest as the default.
func mergeIntegration(root, src, integration string, prev map[string]any) error {
	var gen map[string]any
	b, err := os.ReadFile(filepath.Join(src, ".specify", "integration.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &gen); err != nil {
		return err
	}
	inst := []string{}
	settings := map[string]any{}
	if prev != nil {
		if l, ok := prev["installed_integrations"].([]any); ok {
			for _, x := range l {
				if s, ok := x.(string); ok && !slices.Contains(inst, s) {
					inst = append(inst, s)
				}
			}
		}
		if s, ok := prev["integration_settings"].(map[string]any); ok {
			for k, v := range s {
				settings[k] = v
			}
		}
	}
	if !slices.Contains(inst, integration) {
		inst = append(inst, integration)
	}
	if s, ok := gen["integration_settings"].(map[string]any); ok {
		for k, v := range s {
			settings[k] = v
		}
	}
	gen["installed_integrations"], gen["integration_settings"] = inst, settings
	gen["integration"], gen["default_integration"] = integration, integration
	out, err := json.MarshalIndent(gen, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ".specify", "integration.json"), append(out, '\n'), 0o644)
}

// place writes one file unless the project has its own version.
func place(root, rel string, b []byte, mode os.FileMode, installed map[string]string, always bool) (string, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	cur, err := os.ReadFile(abs)
	switch {
	case err == nil && string(cur) == string(b):
		_ = os.Chmod(abs, mode)
		return "same", nil
	case err == nil && always:
	case err == nil && rel == Constitution:
		return "kept", nil // the project's constitution
	case err == nil && installed[rel] != hash(cur):
		return "kept", nil // changed locally (or not installed by Spec Kit)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, b, mode); err != nil {
		return "", err
	}
	_ = os.Chmod(abs, mode)
	if cur != nil {
		return "updated", nil
	}
	return "created", nil
}

func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Step is how a workflow step runs a Spec Kit command in this project.
type Step struct {
	Invoke string `json:"invoke"`          // what the agent is given, e.g. /speckit-plan
	Skill  string `json:"skill,omitempty"` // the command's instructions file, relative to root
}

// StepFor finds the installed Spec Kit command for step id (constitution,
// specify, …), preferring integration's folder: skills (v1.x), then the
// older commands/prompts files.
func StepFor(root, integration, id string) Step {
	type cand struct{ invoke, rel string }
	skills := []cand{{"/speckit-" + id, ".claude/skills/speckit-" + id + "/SKILL.md"}, {"/speckit-" + id, ".github/skills/speckit-" + id + "/SKILL.md"}}
	if integration == "copilot" {
		skills[0], skills[1] = skills[1], skills[0]
	}
	legacy := []cand{{"/speckit." + id, ".claude/commands/speckit." + id + ".md"}, {"/speckit." + id, ".github/agents/speckit." + id + ".agent.md"}, {"/speckit." + id, ".github/prompts/speckit." + id + ".prompt.md"}}
	for _, c := range append(skills, legacy...) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.rel))); err == nil {
			return Step{Invoke: c.invoke, Skill: c.rel}
		}
	}
	return Step{Invoke: "/speckit-" + id}
}
