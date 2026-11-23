package extension

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PermissionHook answers one Copilot CLI "permissionRequest" hook.
//
// Copilot runs without a person at the keyboard (-p), so a tool call that
// would need approval is denied. The SpecWright plugin for a run registers
// this program as the run's permissionRequest hook: it forwards the request
// to the app over the bridge, where the user answers in the same dialog
// Claude's permission prompts use, and prints Copilot's answer:
//
//	{"behavior":"allow"}  or  {"behavior":"deny","message":"…"}
//
// If the app cannot be asked, it prints nothing: Copilot then falls back to
// its normal handling, which in -p mode denies the call.
func PermissionHook(in io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return err
	}
	tool, input := hookRequest(raw)
	// SpecWright's own tools (agentbridge: its built-ins, save_flow, and the
	// project tools the user approved): allowed, as they are for Claude
	// (--allowedTools mcp__agentbridge). Copilot's --allow-tool agentbridge
	// does not match them, so every call used to ask the user.
	if ownTool(tool) || ownToolCommand(tool, input) { // the same tools, run with the shell (toolcli.go)
		return json.NewEncoder(out).Encode(map[string]any{"behavior": "allow"})
	}
	describeCopilotTool(tool, input)
	// file edits inside the project (or a folder the run added) need no
	// question, as with Claude's acceptEdits; edits elsewhere are asked
	if editInside(tool, input, hookDirs(raw)) {
		return json.NewEncoder(out).Encode(map[string]any{"behavior": "allow"})
	}
	c := &bridgeClient{url: os.Getenv("CODEGEN_BRIDGE_URL"), tok: os.Getenv("CODEGEN_BRIDGE_TOKEN"),
		hc: &http.Client{Timeout: 15 * time.Minute}, agent: firstNonBlank(os.Getenv("CODEGEN_AGENT_ID"), "copilot")}
	var ans PermissionAnswer
	if err := c.do("POST", "/permission", PermissionRequest{Agent: c.agent, Tool: tool, Input: input}, &ans); err != nil {
		return nil // no answer: Copilot's default (deny in -p mode)
	}
	res := map[string]any{"behavior": "allow"}
	if !ans.Allow {
		res = map[string]any{"behavior": "deny", "message": firstNonBlank(ans.Message, "The user denied this.")}
	}
	return json.NewEncoder(out).Encode(res)
}

// hookRequest reads the tool name and its arguments from a hook payload.
// Copilot sends camelCase fields (toolName, toolArgs as a JSON string or an
// object); its Claude-compatible form uses tool_name and tool_input.
func hookRequest(raw []byte) (string, map[string]any) {
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	tool := ""
	for _, k := range []string{"toolName", "tool_name", "tool", "name"} {
		if s, ok := p[k].(string); ok && s != "" {
			tool = s
			break
		}
	}
	input := map[string]any{}
	for _, k := range []string{"toolArgs", "tool_input", "toolInput", "args", "input"} {
		switch v := p[k].(type) {
		case map[string]any:
			input = v
		case string:
			var m map[string]any
			if json.Unmarshal([]byte(v), &m) == nil {
				input = m
			} else if strings.TrimSpace(v) != "" {
				input = map[string]any{"value": v}
			}
		default:
			continue
		}
		break
	}
	// what kind of permission it is (shell, write, url, …), when given
	for _, k := range []string{"kind", "permissionKind", "permission"} {
		if s, ok := p[k].(string); ok && s != "" {
			input["permission"] = s
			if tool == "" {
				tool = s
			}
		}
	}
	if tool == "" {
		tool = "tool"
	}
	return tool, input
}

// ownTool reports whether a Copilot tool name is one of SpecWright's
// agentbridge tools ("agentbridge/read_file" in hooks, "agentbridge-…" in
// events).
func ownTool(tool string) bool {
	return strings.HasPrefix(tool, "agentbridge/") || strings.HasPrefix(tool, "agentbridge-") ||
		strings.HasPrefix(tool, "specwright_") // the same tools, as SpecWright's Copilot extension (copilotself.go)
}

// describeCopilotTool says in words what Copilot's own extension tools
// do, for the question in the app (they come with no arguments to show).
// Reloading lets the agent use an extension it just fixed in the same run;
// "Allow for this run" covers the reloads after it.
func describeCopilotTool(tool string, input map[string]any) {
	if _, ok := input["description"]; ok {
		return
	}
	switch tool {
	case "extensions_reload":
		input["description"] = "Reload the Copilot extensions in this session, so the changes just made take effect and the agent can run the extension again. This starts the extensions' current code."
	case "extensions_manage":
		op, _ := input["operation"].(string)
		name, _ := input["name"].(string)
		d := "Copilot extension: " + firstNonBlank(op, "manage")
		if name != "" {
			d += " " + name
		}
		if op == "scaffold" {
			d += " (creates its folder and a starter extension.mjs)"
		}
		input["description"] = d
	}
}

// editTools are Copilot's file-changing tools.
var editTools = map[string]bool{"edit": true, "create": true, "write": true, "Edit": true, "Write": true, "MultiEdit": true}

// hookDirs are the folders edits may go to without asking: the session's
// working directory from the payload, and the run's extra folders
// (CODEGEN_EDIT_DIRS, separated by the OS list separator).
func hookDirs(raw []byte) []string {
	var p struct {
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(raw, &p)
	var dirs []string
	if p.Cwd != "" {
		dirs = append(dirs, p.Cwd)
	}
	for _, d := range filepath.SplitList(os.Getenv("CODEGEN_EDIT_DIRS")) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// editInside reports whether this is a file edit whose file is inside one of dirs.
func editInside(tool string, input map[string]any, dirs []string) bool {
	if !editTools[tool] {
		return false
	}
	path, _ := input["file_path"].(string)
	if path == "" {
		path, _ = input["path"].(string)
	}
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	// resolve symlinks through the nearest existing parent: the file (and
	// its folders) may not exist yet, and macOS temp paths are links
	real := func(p string) string {
		p = filepath.Clean(p)
		rest := ""
		for q := p; ; q = filepath.Dir(q) {
			if r, err := filepath.EvalSymlinks(q); err == nil {
				return filepath.Join(r, rest)
			}
			if filepath.Dir(q) == q {
				return p
			}
			rest = filepath.Join(filepath.Base(q), rest)
		}
	}
	f := real(path)
	for _, d := range dirs {
		rel, err := filepath.Rel(real(d), f)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

// AutoAllowCopilot says whether a Copilot tool call needs no question:
// SpecWright's own tools, and file edits inside dirs (the run's folders).
// The -p hook and the SDK-hosted session (agent/copilot_sdk.go) share it.
func AutoAllowCopilot(tool string, input map[string]any, dirs []string) bool {
	return ownTool(tool) || ownToolCommand(tool, input) || editInside(tool, input, dirs)
}
