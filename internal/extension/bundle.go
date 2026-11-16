package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codegen/internal/flow"
)

// Tool export and import.
//
// A tool file (format "specwright-tools", usually saved as *.tools.json)
// carries what is needed to recreate script tools in another project: the
// description, parameters, secrets (names and where they come from — never
// values), permissions, the script and its command, dependencies, the output
// shape and tags. Test results, approval and history are left out.
//
// An imported tool is never approved: it shows as "needs approval" until
// someone reads it and saves it in the tool editor, as agents may only call
// approved tools.

// ToolBundleFormat names the file format.
const ToolBundleFormat = "specwright-tools"

// maxBundleTools / maxScript keep an import from filling the disk.
const (
	maxBundleTools = 200
	maxScript      = 2 << 20
)

// ToolBundle is an exported set of tools.
type ToolBundle struct {
	Format   string        `json:"format"`
	Version  int           `json:"version"`
	Exported time.Time     `json:"exported"`
	Tools    []BundledTool `json:"tools"`
}

// BundledTool is one tool in a ToolBundle.
type BundledTool struct {
	Name     string                             `json:"name"`
	Desc     string                             `json:"desc"`
	Lang     string                             `json:"lang,omitempty"`
	Params   []Param                            `json:"params"`
	Secrets  []Secret                           `json:"secrets,omitempty"`
	Perms    []string                           `json:"perms,omitempty"`
	Command  string                             `json:"command"`
	Script   string                             `json:"script,omitempty"`
	Deps     []string                           `json:"deps,omitempty"`
	Output   *flow.Schema                       `json:"output,omitempty"`
	OutputBy map[string]map[string]*flow.Schema `json:"outputBy,omitempty"`
	Prompt   string                             `json:"prompt,omitempty"` // what it was asked to do, when the AI wrote it
	Tags     []string                           `json:"tags,omitempty"`
}

// Exportable reports why e cannot be exported ("" when it can).
func Exportable(e Extension) string {
	switch {
	case e.Managed != nil || IsManagedScope(e.Scope):
		return "it is managed by an admin (share it by publishing it)"
	case e.Kind == KindCopilot:
		return "it comes from a Copilot extension (share the extension folder)"
	case e.Kind == "npm":
		return "it is an npm script of this project's package.json"
	case e.Kind != "" && e.Kind != "script":
		return "this kind of tool cannot be exported"
	}
	return ""
}

// Bundle turns a tool into its exported form; root is the workspace, used to
// read the script when the record does not hold it.
func Bundle(root string, e Extension, tags []string) (BundledTool, error) {
	if why := Exportable(e); why != "" {
		return BundledTool{}, fmt.Errorf("%s cannot be exported: %s", e.Name, why)
	}
	script := e.Script
	if script == "" {
		p := e.ScriptPath
		if p == "" && e.Dir != "" {
			p = filepath.ToSlash(filepath.Join(e.Dir, EntryFile(e.Lang)))
		}
		if p != "" {
			if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))); err == nil {
				script = string(b)
			}
		}
	}
	return BundledTool{Name: e.Name, Desc: e.Desc, Lang: e.Lang, Params: e.Params, Secrets: e.Secrets, Perms: e.Perms,
		Command: e.Command, Script: script, Deps: e.Deps, Output: e.Output, OutputBy: e.OutputBy, Prompt: e.Prompt, Tags: tags}, nil
}

// MarshalBundle writes tools as a tool file.
func MarshalBundle(tools []BundledTool) (string, error) {
	b, err := json.MarshalIndent(ToolBundle{Format: ToolBundleFormat, Version: 1, Exported: time.Now().UTC().Truncate(time.Second), Tools: tools}, "", "  ")
	return string(b), err
}

// ParseBundle reads a tool file and checks every tool in it.
func ParseBundle(data string) ([]BundledTool, error) {
	var b ToolBundle
	if err := json.Unmarshal([]byte(data), &b); err != nil {
		return nil, fmt.Errorf("not a tool file: %v", err)
	}
	if b.Format != ToolBundleFormat {
		return nil, errors.New("not a SpecWright tool file (format " + fmt.Sprintf("%q", b.Format) + ")")
	}
	if b.Version > 1 {
		return nil, fmt.Errorf("this tool file is version %d; update SpecWright to import it", b.Version)
	}
	if len(b.Tools) == 0 {
		return nil, errors.New("the file has no tools")
	}
	if len(b.Tools) > maxBundleTools {
		return nil, fmt.Errorf("the file has %d tools; at most %d can be imported at once", len(b.Tools), maxBundleTools)
	}
	seen := map[string]bool{}
	for i, t := range b.Tools {
		if err := t.check(); err != nil {
			return nil, fmt.Errorf("tool %d (%s): %v", i+1, t.Name, err)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("the file has two tools named %s", t.Name)
		}
		seen[t.Name] = true
	}
	return b.Tools, nil
}

func (t BundledTool) check() error {
	switch {
	case !NameRE.MatchString(t.Name):
		return errors.New("the name must start with a letter and use lowercase letters, digits, - and _ (max 40)")
	case strings.TrimSpace(t.Command) == "" && strings.TrimSpace(t.Script) == "":
		return errors.New("it has neither a command nor a script")
	case len(t.Script) > maxScript:
		return fmt.Errorf("its script is larger than %d MB", maxScript>>20)
	}
	for _, s := range t.Secrets {
		if strings.TrimSpace(s.Name) == "" {
			return errors.New("a secret has no name")
		}
	}
	return nil
}

// Extension is the tool to save for an imported BundledTool: a project
// script tool, not approved.
func (t BundledTool) Extension() Extension {
	return Extension{Name: t.Name, Desc: t.Desc, Kind: "script", Scope: "project", Lang: t.Lang, Params: t.Params, Secrets: t.Secrets,
		Perms: t.Perms, Command: t.Command, Script: t.Script, Deps: t.Deps, Output: t.Output, OutputBy: t.OutputBy, Prompt: t.Prompt,
		Status: "needs approval"}
}

// MarkUnapproved marks a saved tool as needing approval (an imported tool:
// saved, but not yet read and approved by anyone here).
func (s *Store) MarkUnapproved(name string) (Extension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return Extension{}, err
	}
	for i := range exts {
		if exts[i].Name == name {
			exts[i].Approved, exts[i].ApprovedAt, exts[i].Hash = false, time.Time{}, ""
			exts[i].Status = "needs approval"
			return exts[i], s.write(exts)
		}
	}
	return Extension{}, fmt.Errorf("no tool named %s", name)
}
