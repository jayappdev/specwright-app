package extension

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Copilot extension tools as SpecWright tools. Each tool an extension in
// .github/extensions registers becomes a project tool of kind "copilot":
// listed in Tools, offered to agents through agentbridge (Claude too),
// usable as a flow step and testable with AI, like any other tool. It runs
// through copilotshim.mjs, which stands in for the Copilot SDK. Approval
// covers the extension's files (Tree): change one and the tool needs
// approving again. Loading an extension into the project approves it.

// KindCopilot is the Extension.Kind of a Copilot extension tool.
const KindCopilot = "copilot"

//go:embed copilotshim.mjs
var copilotShim []byte

// CopilotShimPath writes the shim to the user cache folder (once per
// version) and returns its path.
func CopilotShimPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	sum := sha256.Sum256(copilotShim)
	p := filepath.Join(dir, "SpecWright", "copilot-shim-"+hex.EncodeToString(sum[:6])+".mjs")
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, copilotShim) {
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, copilotShim, 0o644); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// CopilotToolDef is a tool as the extension registers it.
type CopilotToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// DiscoverCopilotTools loads the extension (its module code runs, with the
// SDK stubbed) and returns the tools and hooks it registers.
func DiscoverCopilotTools(ctx context.Context, root string, x CopilotExt, env []string) ([]CopilotToolDef, []string, error) {
	shim, err := CopilotShimPath()
	if err != nil {
		return nil, nil, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, nil, errors.New("Node.js is needed to run Copilot extensions but was not found on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, shim, "list", filepath.Join(x.Dir, x.Entry))
	cmd.Dir, cmd.Env = root, append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{w: &out, n: maxOutput}, &limited{w: &errb, n: maxOutput}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "Cannot find package") || strings.Contains(msg, "ERR_MODULE_NOT_FOUND") {
			msg += " (install the extension's npm packages first)"
		}
		return nil, nil, fmt.Errorf("reading the tools of %s: %s", x.Name, clipText(msg, 600))
	}
	var r struct {
		Tools []CopilotToolDef `json:"tools"`
		Hooks []string         `json:"hooks"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &r); err != nil {
		return nil, nil, fmt.Errorf("reading the tools of %s: %v", x.Name, err)
	}
	return r.Tools, r.Hooks, nil
}

// CopilotToolExtensions turns discovered tools into approved project tools.
// taken are names already used by other tools; clashing names get the
// extension's name in front.
func CopilotToolExtensions(root string, x CopilotExt, defs []CopilotToolDef, hooks []string, taken map[string]bool) []Extension {
	rel := filepath.ToSlash(filepath.Join(ProjectCopilotExtDir, x.Name))
	tree := treeHash(x.Dir)
	var deps []string
	for k, v := range x.Deps {
		deps = append(deps, k+"@"+v)
	}
	sort.Strings(deps)
	var out []Extension
	for _, d := range defs {
		e := Extension{Name: copilotToolName(x.Name, d.Name, taken), Desc: d.Description, Kind: KindCopilot, Scope: "project", Status: "untested",
			Lang: "Node.js", Params: schemaParams(d.Parameters), Secrets: []Secret{}, Perms: []string{"runs Node.js", "Copilot extension " + x.Name},
			Tool: d.Name, Entry: x.Entry, Dir: rel, Tree: tree, Deps: deps, Revision: 1, Approved: true, ApprovedAt: time.Now()}
		if len(hooks) > 0 {
			e.Desc += " (Its Copilot hooks " + strings.Join(hooks, ", ") + " only run inside Copilot.)"
		}
		e.Command = copilotCommand(e)
		e.Hash = e.Fingerprint()
		taken[e.Name] = true
		out = append(out, e)
	}
	return out
}

func copilotCommand(e Extension) string {
	return "copilot-extension " + e.Dir + "/" + e.Entry + " " + e.Tool
}

func copilotToolName(ext, tool string, taken map[string]bool) string {
	n := sanitizeName(tool)
	if !taken[n] {
		return n
	}
	n = sanitizeName(ext + "-" + tool)
	for i := 2; taken[n]; i++ {
		n = sanitizeName(fmt.Sprintf("%s-%s-%d", ext, tool, i))
	}
	return n
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	n := strings.Trim(b.String(), "-")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "x-" + n
	}
	if len(n) > 40 {
		n = strings.TrimRight(n[:40], "-")
	}
	return n
}

// schemaParams reads a JSON schema's top-level properties as parameters.
func schemaParams(s map[string]any) []Param {
	props, _ := s["properties"].(map[string]any)
	req := map[string]bool{}
	if l, ok := s["required"].([]any); ok {
		for _, v := range l {
			if n, ok := v.(string); ok {
				req[n] = true
			}
		}
	}
	out := []Param{}
	for name, v := range props {
		p := Param{Name: name, Type: "string", Required: req[name]}
		if m, ok := v.(map[string]any); ok {
			if t, ok := m["type"].(string); ok {
				p.Type = t
			}
			if d, ok := m["description"].(string); ok {
				p.Desc = d
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// mergeCopilot brings the project's Copilot extensions into the tool list:
// saved (approved) tools get their extension's current files fingerprint,
// so a change shows as "needs approval"; an extension with no saved tools
// (cloned with the project, never approved here) is listed from its source
// as needing approval; tools of a removed extension are dropped.
func mergeCopilot(root string, exts []Extension) []Extension {
	proj := scanCopilotExts(filepath.Join(root, filepath.FromSlash(ProjectCopilotExtDir)), "project")
	byDir := map[string][]int{}
	taken := map[string]bool{}
	var out []Extension
	for _, e := range exts {
		if e.Kind == KindCopilot {
			continue
		}
		taken[e.Name] = true
		out = append(out, e)
	}
	for i, e := range exts {
		if e.Kind == KindCopilot {
			byDir[e.Dir] = append(byDir[e.Dir], i)
		}
	}
	for _, x := range proj {
		rel := filepath.ToSlash(filepath.Join(ProjectCopilotExtDir, x.Name))
		if idx := byDir[rel]; len(idx) > 0 {
			tree := treeHash(x.Dir)
			for _, i := range idx {
				e := exts[i]
				e.Tree, e.Entry = tree, x.Entry
				e.Command = copilotCommand(e)
				if e.Approved && e.Hash != e.Fingerprint() {
					e.Status = "needs approval"
				}
				taken[e.Name] = true
				out = append(out, e)
			}
			continue
		}
		tools := x.Tools
		if len(tools) == 0 {
			tools = []CopilotExtTool{{Name: x.Name, Desc: x.Desc}}
		}
		for _, t := range tools {
			e := Extension{Name: copilotToolName(x.Name, t.Name, taken), Desc: firstNonBlank(t.Desc, x.Desc), Kind: KindCopilot, Scope: "project", Status: "needs approval",
				Lang: "Node.js", Params: []Param{}, Secrets: []Secret{}, Perms: []string{"runs Node.js", "Copilot extension " + x.Name}, Tool: t.Name, Entry: x.Entry, Dir: rel, Tree: treeHash(x.Dir), Revision: 1}
			e.Command = copilotCommand(e)
			taken[e.Name] = true
			out = append(out, e)
		}
	}
	return out
}

// PutCopilotTools replaces the saved tools of one project extension (rel
// is .github/extensions/<name>) with tools.
func (s *Store) PutCopilotTools(rel string, tools []Extension) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return err
	}
	keep := exts[:0]
	for _, e := range exts {
		if !(e.Kind == KindCopilot && e.Dir == rel) {
			keep = append(keep, e)
		}
	}
	return s.write(append(keep, tools...))
}

// CopilotExtOf finds the project extension a copilot tool comes from.
func CopilotExtOf(root string, e Extension) (CopilotExt, error) {
	name := filepath.Base(filepath.FromSlash(e.Dir))
	if e.Kind != KindCopilot || ValidCopilotExtName(name) != nil {
		return CopilotExt{}, fmt.Errorf("%s is not a Copilot extension tool", e.Name)
	}
	x, ok := readCopilotExt(filepath.Join(root, filepath.FromSlash(ProjectCopilotExtDir), name), "project")
	if !ok {
		return CopilotExt{}, fmt.Errorf("the Copilot extension %s is no longer in %s", name, ProjectCopilotExtDir)
	}
	return x, nil
}

// copilotArgv is how a copilot tool runs: node, the shim, the extension's
// entry file, the tool and its arguments as JSON (no shell involved).
func copilotArgv(root string, e Extension, args map[string]any) ([]string, error) {
	shim, err := CopilotShimPath()
	if err != nil {
		return nil, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("Node.js is needed to run Copilot extension tools but was not found on PATH")
	}
	name := filepath.Base(filepath.FromSlash(e.Dir))
	if ValidCopilotExtName(name) != nil || filepath.ToSlash(filepath.Dir(filepath.FromSlash(e.Dir))) != ProjectCopilotExtDir {
		return nil, fmt.Errorf("%s: invalid extension folder %q", e.Name, e.Dir)
	}
	if args == nil {
		args = map[string]any{}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return []string{node, shim, "call", filepath.Join(root, filepath.FromSlash(e.Dir), e.Entry), e.Tool, string(b)}, nil
}
