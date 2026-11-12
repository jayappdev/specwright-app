// Package extension holds the tools ("extensions") an agent may call during
// a workflow: scripts the user generated or wrote and reviewed, plus the
// workspace's approved `ext:*` npm scripts. They are saved in
// <workspace>/.codegen/extensions.json, exposed to agents through a loopback
// HTTP bridge and a small MCP server with one tool, extension_managed.
package extension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/aishim"
)

// Param is one argument the agent fills in.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Desc     string `json:"desc"`
}

// Secret names an environment variable injected at run time. Only the name
// is stored; values live in memory for the session.
type Secret struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
	// Source: "auto" (or empty) = session value → environment variable →
	// ask the user; "env" = environment only; "ask" = ask on every run;
	// "session" = only a value entered in the app.
	Source string `json:"source,omitempty"`
	// Env is the environment variable to read (default: Name).
	Env string `json:"env,omitempty"`
}

// Extension is one tool.
type Extension struct {
	Name       string         `json:"name"`
	Desc       string         `json:"desc"`
	Kind       string         `json:"kind"`   // script | npm
	Scope      string         `json:"scope"`  // project | global
	Status     string         `json:"status"` // untested | tested | in review | needs approval
	Lang       string         `json:"lang,omitempty"`
	Params     []Param        `json:"params"`
	Secrets    []Secret       `json:"secrets"`
	Perms      []string       `json:"perms"`
	Command    string         `json:"command"`
	Script     string         `json:"script,omitempty"`
	ScriptPath string         `json:"scriptPath,omitempty"`
	Prompt     string         `json:"prompt,omitempty"`
	TestPrompt string         `json:"testPrompt,omitempty"`
	TestFiles  []string       `json:"testFiles,omitempty"` // files attached to TestPrompt (workspace paths)
	SampleTest string         `json:"sampleTest,omitempty"`
	Call       map[string]any `json:"call,omitempty"`
	Revision   int            `json:"revision"`
	Approved   bool           `json:"approved"`
	ApprovedAt time.Time      `json:"approvedAt,omitzero"`
	Hash       string         `json:"hash,omitempty"`
	LastTest   *TestResult    `json:"lastTest,omitempty"`
	// Deps are packages installed into the tool's own folder (Dir) from the
	// configured registries: "pg@8.13.1", "requests==2.32.3",
	// "github.com/jackc/pgx/v5@v5.7.1", "org.postgresql:postgresql:42.7.4".
	Deps []string `json:"deps,omitempty"`
	Dir  string   `json:"dir,omitempty"`
	// Copilot extension tools (Kind "copilot"): the tool's name inside the
	// extension, its entry file, and a fingerprint of the extension's files.
	Tool  string `json:"tool,omitempty"`
	Entry string `json:"entry,omitempty"`
	Tree  string `json:"tree,omitempty"`
	// Managed is set (by the app, never stored by users) on tools an admin
	// published in managed-tools.json; they are read-only for everyone else.
	Managed *ManagedInfo `json:"managed,omitempty"`
}

// ManagedInfo describes a managed tool's publication and its check result.
type ManagedInfo struct {
	Version  int       `json:"version"`
	SignedBy string    `json:"signedBy"`
	Signer   string    `json:"signer"`
	SignedAt time.Time `json:"signedAt"`
	Status   string    `json:"status"` // verified | signature invalid | signed by an untrusted key
}

// TestResult is the outcome of "Test with AI".
type TestResult struct {
	Pass    bool       `json:"pass"`
	Summary string     `json:"summary"`
	Checks  []Check    `json:"checks"`
	Output  string     `json:"output"`
	At      time.Time  `json:"at"`
	Steps   []TestStep `json:"steps"`
}

// Check is one line of the test checklist.
type Check struct {
	Label string `json:"label"`
	OK    bool   `json:"ok"`
}

// TestStep is one row of the test transcript (say | call | run | out).
type TestStep struct {
	K string `json:"k"`
	V string `json:"v"`
}

// NameRE is the allowed extension name shape.
var NameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

// Fingerprint hashes what approval covers: the command and the script.
func (e Extension) Fingerprint() string {
	k := e.Kind + "\x00" + e.Command + "\x00" + e.Script
	if len(e.Deps) > 0 { // approval covers the dependencies too
		k += "\x00" + strings.Join(e.Deps, "\n")
	}
	if e.Kind == KindCopilot { // and, for a Copilot extension, all its files
		k += "\x00" + e.Dir + "\x00" + e.Tool + "\x00" + e.Tree
	}
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:12])
}

// Runnable reports whether the agent may call e now.
func (e Extension) Runnable() error {
	if !e.Approved {
		return fmt.Errorf("extension %s is not approved", e.Name)
	}
	if e.Hash != e.Fingerprint() {
		return fmt.Errorf("extension %s changed since it was approved; review it again", e.Name)
	}
	if e.Status == "in review" {
		return fmt.Errorf("extension %s is waiting for catalog review", e.Name)
	}
	return nil
}

type file struct {
	Version    int         `json:"version"`
	Extensions []Extension `json:"extensions"`
}

// Store is the extensions.json of one workspace.
type Store struct {
	mu   sync.Mutex
	root string
}

// NewStore opens the store for workspace root.
func NewStore(root string) *Store { return &Store{root: root} }

// Path is <root>/.codegen/extensions.json.
func (s *Store) Path() string { return filepath.Join(s.root, ".codegen", "extensions.json") }

func (s *Store) read() ([]Extension, error) {
	b, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path(), err)
	}
	return f.Extensions, nil
}

func (s *Store) write(exts []Extension) error {
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		return err
	}
	sort.SliceStable(exts, func(i, j int) bool { return exts[i].Name < exts[j].Name })
	b, err := json.MarshalIndent(file{Version: 1, Extensions: exts}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path())
}

// List returns saved script extensions merged with the workspace's ext:*
// npm scripts.
func (s *Store) List() ([]Extension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return nil, err
	}
	saved := map[string]int{}
	for i, e := range exts {
		saved[e.Name] = i
	}
	for _, n := range npmScripts(s.root) {
		cmd := "npm run --silent " + n.script
		if i, ok := saved[n.name]; ok && exts[i].Kind == "npm" {
			exts[i].Command, exts[i].Script = cmd, n.body
			if exts[i].Approved && exts[i].Hash != exts[i].Fingerprint() {
				exts[i].Status = "needs approval"
			}
			continue
		}
		if _, ok := saved[n.name]; ok {
			continue
		}
		exts = append(exts, Extension{Name: n.name, Desc: "npm script " + n.script + ": " + n.body, Kind: "npm", Scope: "project",
			Status: "needs approval", Lang: "Node.js", Command: cmd, Script: n.body, Perms: []string{"runs Node.js"}, Revision: 1})
	}
	return mergeCopilot(s.root, exts), nil
}

// Get finds one extension by name.
func (s *Store) Get(name string) (Extension, error) {
	exts, err := s.List()
	if err != nil {
		return Extension{}, err
	}
	for _, e := range exts {
		if e.Name == name {
			return e, nil
		}
	}
	return Extension{}, fmt.Errorf("no extension named %q", name)
}

// Save validates, approves (the user reviewed it to save it) and stores e,
// writing its script file next to extensions.json.
func (s *Store) Save(e Extension) (Extension, error) {
	if !NameRE.MatchString(e.Name) {
		return e, errors.New("name must start with a letter and use lowercase letters, digits, - and _ (max 40)")
	}
	if e.Kind == "" {
		e.Kind = "script"
	}
	if e.Scope == "" {
		e.Scope = "project"
	}
	if e.Kind == "script" && HasDeps(e) {
		rel := ToolDir(e.Scope, e.Name)
		var err error
		if e, err = WriteTool(filepath.Join(s.root, filepath.FromSlash(rel)), rel, e); err != nil {
			return e, err
		}
		EnsureGitignore(s.root)
	} else if e.Kind == "script" && e.Script != "" {
		e.Deps, e.Dir = nil, ""
		ext := ScriptExt(e.Lang)
		dir := "project"
		if e.Scope == "global" {
			dir = "global"
		}
		rel := filepath.ToSlash(filepath.Join(".codegen", "tools", dir, e.Name+ext))
		abs := filepath.Join(s.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return e, err
		}
		if err := os.WriteFile(abs, []byte(e.Script), 0o755); err != nil {
			return e, err
		}
		// point the command at the saved file (word-level, so quoted paths survive)
		e.Command = RepointScript(e.Command, e.Name, rel)
		e.ScriptPath = rel
	}
	e.Approved, e.ApprovedAt, e.Hash = true, time.Now(), ""
	e.Hash = e.Fingerprint()
	if e.Status == "" || e.Status == "needs approval" {
		e.Status = "untested"
	}
	if e.Scope == "global" && e.Kind == "script" {
		e.Status = "in review"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return e, err
	}
	found := false
	for i := range exts {
		if exts[i].Name == e.Name {
			exts[i], found = e, true
		}
	}
	if !found {
		exts = append(exts, e)
	}
	return e, s.write(exts)
}

// ScriptExt is the file extension for a tool written in lang.
func ScriptExt(lang string) string {
	switch lang {
	case "Node.js":
		return ".js"
	case "Go":
		return ".go"
	case "Java":
		return ".java"
	case "Python":
		return ".py"
	}
	return ".sh"
}

// Approve marks an existing extension (typically an ext:* npm script) as
// approved at its current fingerprint.
func (s *Store) Approve(name string) (Extension, error) {
	e, err := s.Get(name)
	if err != nil {
		return e, err
	}
	if e.Kind == "npm" {
		e.Approved, e.ApprovedAt = true, time.Now()
		e.Hash = e.Fingerprint()
		e.Status = "untested"
		s.mu.Lock()
		defer s.mu.Unlock()
		exts, err := s.read()
		if err != nil {
			return e, err
		}
		found := false
		for i := range exts {
			if exts[i].Name == name {
				exts[i], found = e, true
			}
		}
		if !found {
			exts = append(exts, e)
		}
		return e, s.write(exts)
	}
	return s.Save(e)
}

// SetTest records a test result and updates status.
func (s *Store) SetTest(name string, tr TestResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return err
	}
	for i := range exts {
		if exts[i].Name == name {
			exts[i].LastTest = &tr
			if exts[i].Status != "in review" {
				exts[i].Status = map[bool]string{true: "tested", false: "untested"}[tr.Pass]
			}
			return s.write(exts)
		}
	}
	return nil
}

// Delete removes an extension (its script file stays for the user to delete).
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return err
	}
	out := exts[:0]
	for _, e := range exts {
		if e.Name != name {
			out = append(out, e)
		}
	}
	return s.write(out)
}

type npmScript struct{ name, script, body string }

func npmScripts(root string) []npmScript {
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return nil
	}
	var out []npmScript
	for k, v := range pkg.Scripts {
		if n, ok := strings.CutPrefix(k, "ext:"); ok && NameRE.MatchString(n) {
			out = append(out, npmScript{name: n, script: k, body: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// RunResult is the outcome of executing an extension.
type RunResult struct {
	OK         bool     `json:"ok"`
	Output     string   `json:"output"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	Command    string   `json:"command"` // argv, quoted as a command template
	Argv       []string `json:"argv,omitempty"`
	Error      string   `json:"error,omitempty"`
	// AI is what the tool spent on AI itself (claude / copilot calls it
	// made), when SpecWright's AI launchers were on its PATH.
	AI []aishim.Record `json:"ai,omitempty"`
}

const maxOutput = 64 * 1024

// Exec runs e in root with args and secret env values. No shell is
// involved: the command template is split into words first and {{param}}
// placeholders are substituted inside words, so arguments cannot inject
// shell syntax.
func Exec(ctx context.Context, root string, e Extension, args map[string]any, secrets map[string]string, extraEnv []string, timeout time.Duration) RunResult {
	return ExecInput(ctx, root, e, args, secrets, extraEnv, timeout, nil)
}

// ExecInput is Exec with data on the tool's standard input (flows pass the
// previous step's output this way; nil means no input).
func ExecInput(ctx context.Context, root string, e Extension, args map[string]any, secrets map[string]string, extraEnv []string, timeout time.Duration, stdin *string) RunResult {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	for _, p := range e.Params {
		if v, ok := args[p.Name]; p.Required && (!ok || fmt.Sprint(v) == "") {
			return RunResult{Error: fmt.Sprintf("missing required argument %q", p.Name)}
		}
	}
	var argv []string
	words := SplitWords(e.Command)
	if e.Kind == KindCopilot {
		a, err := copilotArgv(root, e, args)
		if err != nil {
			return RunResult{Error: err.Error()}
		}
		argv, words = a, nil
	} else if len(words) == 0 {
		return RunResult{Error: "extension has no command"}
	}
	for _, w := range words {
		only := placeholderRE.FindStringSubmatch(w)
		sub := placeholderRE.ReplaceAllStringFunc(w, func(m string) string {
			k := placeholderRE.FindStringSubmatch(m)[1]
			if v, ok := args[k]; ok && v != nil {
				return fmt.Sprint(v)
			}
			return ""
		})
		if only != nil && only[0] == w && sub == "" {
			continue // optional parameter left out
		}
		argv = append(argv, sub)
	}
	if e.Kind == "npm" && len(argv) > 0 {
		if _, err := exec.LookPath("npm"); err != nil {
			return RunResult{Error: "Node.js (npm) is needed to run ext:* scripts but was not found on PATH"}
		}
		// pass arguments to the script as --name=value after "--"
		var keys []string
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			argv = append(argv, "--")
			for _, k := range keys {
				argv = append(argv, fmt.Sprintf("--%s=%v", k, args[k]))
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	env := append(os.Environ(), extraEnv...)
	for _, s := range e.Secrets {
		if v, ok := secrets[s.Name]; ok {
			env = append(env, s.Name+"="+v)
		}
	}
	// count the AI the tool uses itself: the claude/copilot launchers go
	// first on its PATH and report each call to a file of its own
	usageFile := ""
	for _, kv := range extraEnv {
		if dir, ok := strings.CutPrefix(kv, aishim.EnvDir+"="); ok && dir != "" {
			if f, err := os.CreateTemp("", "specwright-ai-*.jsonl"); err == nil {
				usageFile = f.Name()
				f.Close()
				env = append(env, "PATH="+dir+string(os.PathListSeparator)+envValue(env, "PATH"), aishim.EnvUsage+"="+usageFile)
			}
		}
	}
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	}
	var buf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{w: &buf, n: maxOutput}, &limited{w: &buf, n: maxOutput}
	if e.Kind == KindCopilot {
		// the result is what the tool returned; the extension's own logging
		// (stderr) is shown only when the call fails
		cmd.Stderr = &limited{w: &errBuf, n: maxOutput}
	}
	start := time.Now()
	err := cmd.Run()
	out := strings.TrimRight(buf.String(), "\n")
	if err != nil && errBuf.Len() > 0 {
		out = strings.TrimSpace(strings.Join([]string{out, strings.TrimSpace(errBuf.String())}, "\n"))
	}
	res := RunResult{Output: out, DurationMS: time.Since(start).Milliseconds(), Command: JoinWords(argv), Argv: argv}
	if usageFile != "" {
		res.AI = aishim.ReadUsage(usageFile)
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.Error = fmt.Sprintf("timed out after %s", timeout)
	case err != nil && res.ExitCode == 0:
		res.Error = err.Error()
	}
	res.OK = err == nil
	return res
}

var placeholderRE = regexp.MustCompile(`\{\{(\w+)\}\}`)

type limited struct {
	w *bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}

// envValue is the last value of key in env (later entries win, as in exec).
func envValue(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && (k == key || (runtime.GOOS == "windows" && strings.EqualFold(k, key))) {
			v = val
		}
	}
	return v
}
