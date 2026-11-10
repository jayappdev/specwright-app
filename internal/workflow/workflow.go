// Package workflow defines workflows (ordered steps with human approval
// gates, retries and token budgets), the project profile the wizard edits,
// prompt composition, and the runner that executes one step at a time.
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Options choose which context blocks are sent with a step.
type Options struct {
	Rules    bool `json:"rules"`
	Global   bool `json:"global"`
	Project  bool `json:"project"`
	Diagrams bool `json:"diagrams"`
	Tools    bool `json:"tools"`
}

// Step is one workflow step, e.g. /speckit.specify.
type Step struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Cmd          string  `json:"cmd"`
	Ask          string  `json:"ask,omitempty"`
	Placeholder  string  `json:"placeholder,omitempty"`
	Template     string  `json:"template,omitempty"` // suggested agent instructions
	Input        string  `json:"input"`
	Instructions string  `json:"instructions"`
	Feedback     string  `json:"feedback,omitempty"`
	Options      Options `json:"options"`
	Gate         bool    `json:"gate"`    // stop for human approval after the step
	Retries      int     `json:"retries"` // automatic retries on agent failure
	Status       string  `json:"status"`  // pending|running|paused|done|failed|cancelled|interrupted
	LastRun      string  `json:"lastRun,omitempty"`
}

// Budget caps what one step run may consume. Zero means no limit.
type Budget struct {
	MaxTokens          int     `json:"maxTokens"`
	MaxUSD             float64 `json:"maxUsd"`
	MaxPremiumRequests int     `json:"maxPremiumRequests"`
}

// Workflow is saved as <workspace>/.codegen/workflows/<id>.json.
type Workflow struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	Budget  Budget `json:"budget"`
	Steps   []Step `json:"steps"`
}

// Current is the first step not yet approved (or the last step).
func (w *Workflow) Current() int {
	for i, s := range w.Steps {
		if s.Status != "done" {
			return i
		}
	}
	return len(w.Steps) - 1
}

// Index finds a step by id.
func (w *Workflow) Index(id string) int {
	for i, s := range w.Steps {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// CanRun reports why step i may not run yet ("" if it may).
func (w *Workflow) CanRun(i int) string {
	if i < 0 || i >= len(w.Steps) {
		return "no such step"
	}
	if i > 0 && w.Steps[i-1].Status != "done" {
		return w.Steps[i-1].Title + " is not approved yet"
	}
	if strings.TrimSpace(w.Steps[i].Input) == "" {
		return "inputs are missing"
	}
	switch w.Steps[i].Status {
	case "running", "paused":
		return "the step is already " + w.Steps[i].Status
	}
	return ""
}

// DefaultName is what the default workflow is called in the UI. It is
// built on GitHub Spec Kit's /speckit.* commands, but users see a plain
// name; workflows saved under the old name are renamed on load.
const DefaultName = "Spec workflow"

// SpecKit returns the default spec-driven workflow (GitHub Spec Kit commands).
func SpecKit() *Workflow {
	o := Options{Rules: true, Global: true, Project: true, Diagrams: true}
	mk := func(id, title, ask, ph, tpl string) Step {
		return Step{ID: id, Title: title, Cmd: "/speckit." + id, Ask: ask, Placeholder: ph, Template: tpl, Options: o, Gate: true, Retries: 1, Status: "pending"}
	}
	return &Workflow{Version: 1, ID: "speckit", Name: DefaultName,
		Budget: Budget{MaxTokens: 2_000_000, MaxUSD: 10, MaxPremiumRequests: 30},
		Steps: []Step{
			mk("constitution", "Constitution", "Project principles (code quality, testing, security, performance)", "e.g. Every public service has unit tests; no secrets in code; follow ACME Java style.", "Keep principles short and testable. Reuse corporate rules instead of restating them."),
			mk("specify", "Specify", "What to build and why (no tech stack yet)", "e.g. An order service that accepts orders, validates stock and emits OrderPlaced events.", "Describe what and why only. Mark anything unknown as [NEEDS CLARIFICATION]."),
			mk("clarify", "Clarify", "Answers to open questions, or areas to probe", "e.g. Partial shipments are allowed; currency is always EUR.", "Ask at most 5 questions, highest impact first. Record answers in the spec."),
			mk("plan", "Plan", "Tech stack and architecture choices", "e.g. Java 21, Spring Boot 3, PostgreSQL, Kafka, hexagonal layout.", "Check the plan against the constitution. Prefer the corporate golden path."),
			mk("tasks", "Tasks", "Constraints for the task breakdown", "e.g. Tests first; keep tasks under half a day; mark parallel tasks.", "Order tasks by dependency, tests first. Mark tasks that can run in parallel with [P]."),
			mk("implement", "Implement", "Scope for this implementation pass", "e.g. Implement phase 1 and 2 tasks only.", "Work task by task, run the build after each one, stop on the first failure."),
		}}
}

// Path returns where workflow id is saved in root.
func Path(root, id string) string {
	return filepath.Join(root, ".codegen", "workflows", id+".json")
}

// Load reads workflow id from root, or returns the Spec Kit default.
func Load(root, id string) (*Workflow, error) {
	b, err := os.ReadFile(Path(root, id))
	if errors.Is(err, os.ErrNotExist) {
		if id == "speckit" {
			return SpecKit(), nil
		}
		return nil, fmt.Errorf("workflow %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// LoadFile reads a workflow from any JSON file (used by cmd/headless).
func LoadFile(path string) (*Workflow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates a workflow.
func Parse(b []byte) (*Workflow, error) {
	var w Workflow
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	if w.ID == "" || len(w.Steps) == 0 {
		return nil, errors.New("workflow needs an id and at least one step")
	}
	if w.Name == "Spec Kit" {
		w.Name = DefaultName
	}
	seen := map[string]bool{}
	for i := range w.Steps {
		s := &w.Steps[i]
		if s.ID == "" || seen[s.ID] {
			return nil, fmt.Errorf("step %d: missing or duplicate id", i+1)
		}
		seen[s.ID] = true
		if s.Status == "" {
			s.Status = "pending"
		}
		if s.Title == "" {
			s.Title = s.ID
		}
	}
	return &w, nil
}

// Save writes w into root.
func Save(root string, w *Workflow) error {
	p := Path(root, w.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Rule is one functionality rule.
type Rule struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// RulesMarkdown renders rules as one document: a "## <id>" section per
// rule with its Markdown body (the format of the wizard's bulk editor).
func RulesMarkdown(rules []Rule) string {
	var b strings.Builder
	b.WriteString("# Functionality rules\n")
	for _, r := range rules {
		t := strings.TrimSpace(r.Text)
		if t == "" {
			t = "_(empty)_"
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", r.ID, t)
	}
	return b.String()
}

// Folder is one compartment of the .codegen layout.
type Folder struct {
	Path string `json:"path"`
	Note string `json:"note"`
}

// Profile is what the development tool wizard edits; saved to
// <workspace>/.codegen/profile.json.
type Profile struct {
	Lang        string            `json:"lang"`
	Build       string            `json:"build"`
	Global      []string          `json:"global"`
	GlobalMode  string            `json:"globalMode"`
	Project     []string          `json:"project"`
	ProjectMode string            `json:"projectMode"`
	Folders     []Folder          `json:"folders"`
	FoldersMode string            `json:"foldersMode"`
	Rules       []Rule            `json:"rules"`
	RulesMode   string            `json:"rulesMode,omitempty"` // incremental | bulk (how the wizard edits rules)
	Diagrams    map[string]string `json:"diagrams"`            // context | sequence → workspace path
}

// Languages and their build tools.
var Languages = map[string][]string{
	"Java": {"Maven", "Gradle"}, "Kotlin": {"Gradle"}, "TypeScript": {"npm", "pnpm"},
	"Python": {"uv", "pip"}, "C#": {"dotnet"}, "Go": {"go"},
}

// LanguageOrder is the display order of Languages.
var LanguageOrder = []string{"Java", "Kotlin", "TypeScript", "Python", "C#", "Go"}

// DefaultProfile is the profile of a fresh workspace.
func DefaultProfile() *Profile {
	return &Profile{Lang: "Java", Build: "Maven", GlobalMode: "incremental", ProjectMode: "incremental", FoldersMode: "incremental",
		Global: []string{}, Project: []string{}, Diagrams: map[string]string{},
		Folders: []Folder{
			{".codegen/rules", "Functionality rules (exportable)"},
			{".codegen/skills/global", "Corporate skills, read-only copy"},
			{".codegen/skills/project", "Project skills"},
			{".codegen/scripts", "Deterministic scripts"},
			{".codegen/tools", "Tools and extensions"},
		},
		Rules: []Rule{}}
}

// LoadProfile reads profile.json or returns the default.
func LoadProfile(root string) (*Profile, error) {
	b, err := os.ReadFile(filepath.Join(root, ".codegen", "profile.json"))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultProfile(), nil
	}
	if err != nil {
		return nil, err
	}
	p := DefaultProfile()
	if err := json.Unmarshal(b, p); err != nil {
		return nil, err
	}
	if p.Diagrams == nil {
		p.Diagrams = map[string]string{}
	}
	return p, nil
}

// SaveProfile writes profile.json.
func SaveProfile(root string, p *Profile) error {
	if _, ok := Languages[p.Lang]; !ok {
		return fmt.Errorf("unsupported language %q", p.Lang)
	}
	ok := false
	for _, b := range Languages[p.Lang] {
		ok = ok || b == p.Build
	}
	if !ok {
		p.Build = Languages[p.Lang][0]
	}
	dir := filepath.Join(root, ".codegen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(dir, "profile.json"), append(b, '\n'), 0o644)
}

// Library is a skill or script found in a compartment folder.
type Library struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // skill | script
	Note  string `json:"note"`
	Path  string `json:"path"`
	Scope string `json:"scope"` // global | project
	// Managed is set for a corporate skill published by an admin (a signed
	// catalog entry copied into the project); nil for a local file.
	Managed *LibraryManaged `json:"managed,omitempty"`
}

// LibraryManaged describes the catalog entry behind a managed library.
type LibraryManaged struct {
	Version  int       `json:"version"`
	Signer   string    `json:"signer"`
	SignedAt time.Time `json:"signedAt"`
	Status   string    `json:"status"`
	Source   string    `json:"source"` // app | machine
}

// Compartment folders for libraries (workspace-relative, forward slashes).
const (
	GlobalSkillsDir  = ".codegen/skills/global"
	ProjectSkillsDir = ".codegen/skills/project"
	ScriptsDir       = ".codegen/scripts"
)

// LibraryDir is where a compartment lives for this profile: the folder in
// the profile's folder structure that starts with def, else def.
func (p *Profile) LibraryDir(def string) string {
	if p != nil {
		for _, f := range p.Folders {
			if strings.HasPrefix(f.Path, def) {
				return strings.TrimSuffix(f.Path, "/")
			}
		}
	}
	return def
}

// Libraries scans the skills and scripts compartments.
func Libraries(root string, p *Profile) []Library {
	var out []Library
	folder := func(_, def string) string { return p.LibraryDir(def) }
	add := func(rel, kind, scope string) {
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return
		}
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			path := rel + "/" + e.Name()
			note := ""
			name := e.Name()
			if kind == "skill" {
				if e.IsDir() {
					note = firstLine(filepath.Join(root, filepath.FromSlash(path), "SKILL.md"))
					path += "/SKILL.md"
				} else if strings.HasSuffix(name, ".md") {
					name = strings.TrimSuffix(name, ".md")
					note = firstLine(filepath.Join(root, filepath.FromSlash(path)))
				} else {
					continue
				}
			} else if e.IsDir() {
				continue
			}
			out = append(out, Library{ID: scope + ":" + kind + ":" + name, Name: name, Kind: kind, Note: note, Path: path, Scope: scope})
		}
	}
	add(folder(".codegen/skills/global", ".codegen/skills/global"), "skill", "global")
	add(folder(".codegen/skills/project", ".codegen/skills/project"), "skill", "project")
	add(folder(".codegen/scripts", ".codegen/scripts"), "script", "project")
	return out
}

func firstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(strings.TrimLeft(l, "#- "))
		if l != "" && l != "---" && !strings.HasPrefix(l, "name:") {
			return strings.TrimPrefix(l, "description: ")
		}
	}
	return ""
}

// ToolRef is the slice of an extension that prompt composition needs.
type ToolRef struct {
	Name    string
	Desc    string
	Secrets []string
}

// Compose builds the prompt for step i: the slash command and its input,
// then the instruction and context blocks the step's options select.
func Compose(w *Workflow, i int, p *Profile, libs []Library, tools []ToolRef, feedback string) string {
	s := w.Steps[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", s.Cmd, strings.TrimSpace(s.Input))
	if t := strings.TrimSpace(s.Instructions); t != "" {
		fmt.Fprintf(&b, "\n## Instructions\n%s\n", t)
	}
	if s.Options.Rules && len(p.Rules) > 0 {
		b.WriteString("\n## Rules\n")
		for _, r := range p.Rules {
			if t := strings.TrimSpace(r.Text); t != "" {
				// a rule may be multi-line Markdown: indent it under its bullet
				fmt.Fprintf(&b, "- %s: %s\n", r.ID, strings.ReplaceAll(t, "\n", "\n  "))
			}
		}
	}
	sel := func(scope string, ids []string) []Library {
		var out []Library
		for _, l := range libs {
			for _, id := range ids {
				if l.ID == id && (l.Scope == scope || scope == "") {
					out = append(out, l)
				}
			}
		}
		return out
	}
	if s.Options.Global {
		if ls := sel("global", p.Global); len(ls) > 0 {
			b.WriteString("\n## Corporate skills (read before you start)\n")
			for _, l := range ls {
				fmt.Fprintf(&b, "- %s (%s): `%s`\n", l.Name, l.Kind, l.Path)
			}
		}
	}
	if s.Options.Project {
		if ls := sel("project", p.Project); len(ls) > 0 {
			b.WriteString("\n## Project skills and scripts\n")
			for _, l := range ls {
				fmt.Fprintf(&b, "- %s (%s): `%s`\n", l.Name, l.Kind, l.Path)
			}
		}
	}
	if s.Options.Diagrams && len(p.Diagrams) > 0 {
		b.WriteString("\n## Input diagrams\n")
		for _, k := range []string{"context", "sequence"} {
			if v := p.Diagrams[k]; v != "" {
				fmt.Fprintf(&b, "- %s diagram: `%s`\n", k, v)
			}
		}
	}
	if s.Options.Tools && len(tools) > 0 {
		b.WriteString("\n## Tools available\nCall them with the extension_managed tool (MCP server agentbridge).\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- %s: %s", t.Name, t.Desc)
			if len(t.Secrets) > 0 {
				var env []string
				for _, n := range t.Secrets {
					env = append(env, n+"=[REDACTED]")
				}
				fmt.Fprintf(&b, " (env: %s)", strings.Join(env, ", "))
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\n## Project\nLanguage: %s · build: %s\n", p.Lang, p.Build)
	if f := strings.TrimSpace(feedback); f != "" {
		fmt.Fprintf(&b, "\n## Change request from the reviewer\n%s\nRevise your previous output for this step accordingly.\n", f)
	}
	return b.String()
}
