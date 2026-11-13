package extension

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ToolName is the single MCP tool the agentbridge server exposes.
const ToolName = "extension_managed"

// PermissionTool answers Claude's permission prompts
// (--permission-prompt-tool mcp__agentbridge__permission_prompt): the CLI
// calls it, not the model, and the user decides in the app.
const PermissionTool = "permission_prompt"

// ServeMCP runs the agentbridge MCP server over newline-delimited JSON-RPC
// on in/out. It forwards tools/call to the loopback bridge named by
// CODEGEN_BRIDGE_URL / CODEGEN_BRIDGE_TOKEN.
func ServeMCP(in io.Reader, out io.Writer) error {
	url, tok := os.Getenv("CODEGEN_BRIDGE_URL"), os.Getenv("CODEGEN_BRIDGE_TOKEN")
	// long enough for the user to type a secret the tool asks for
	c := &bridgeClient{url: url, tok: tok, hc: &http.Client{Timeout: 15 * time.Minute}, agent: agentID()}
	enc := json.NewEncoder(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{Code: -32700, Message: "parse error"}})
			continue
		}
		if len(req.ID) == 0 { // notification
			continue
		}
		res, rerr := handleMCP(c, req)
		resp := rpcResp{JSONRPC: "2.0", ID: req.ID, Result: res, Error: rerr}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func handleMCP(c *bridgeClient, req rpcReq) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agentbridge", "version": "1.0.0"},
			"instructions":    "SpecWright's built-in workspace tools: read_file, list_directory, file_search, text_search, find_usages, workspace_changes, create_file, create_directory, edit_file, move_path, delete_path, run_command, get_command_output, get_problems, fetch_webpage and todo_list. Paths are relative to the workspace root. Prefer read_file/text_search/edit_file over shell equivalents. Call extension_managed to run a project or admin tool the user approved.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		var out []any
		// built-in tools: one MCP tool each, with a typed schema
		if bs, err := c.builtins(); err == nil {
			for _, b := range bs {
				out = append(out, map[string]any{"name": b.Name, "title": b.Title, "description": b.Description, "inputSchema": b.InputSchema,
					"annotations": map[string]any{"readOnlyHint": b.ReadOnly, "destructiveHint": b.Name == "delete_path", "openWorldHint": b.Name == "fetch_webpage"}})
			}
		}
		out = append(out, map[string]any{"name": PermissionTool, "title": "Ask the user for permission",
			"description": "Used by the CLI to ask the user whether a tool may run. Do not call it yourself.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"tool_name": map[string]any{"type": "string"}, "input": map[string]any{"type": "object", "additionalProperties": true}, "tool_use_id": map[string]any{"type": "string"}},
				"required": []string{"tool_name"}}})
		if os.Getenv("CODEGEN_BRIDGE_EXT") == "0" {
			return map[string]any{"tools": out}, nil
		}
		tools, err := c.list()
		desc := "Run a tool the user approved for this project."
		var names []string
		if err != nil {
			desc += " (The tool list is unavailable: " + err.Error() + ")"
		} else {
			sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
			var b strings.Builder
			b.WriteString(desc + " Available tools:\n")
			for _, t := range tools {
				names = append(names, t.Name)
				fmt.Fprintf(&b, "- %s: %s", t.Name, t.Desc)
				if len(t.Params) > 0 {
					var ps []string
					for _, p := range t.Params {
						s := p.Name + " (" + p.Type
						if !p.Required {
							s += ", optional"
						}
						s += "): " + p.Desc
						if p.Schema != nil && p.Schema.Known() { // a JSON parameter: the shape to send
							s += " Shape: " + p.Schema.Compact()
						}
						ps = append(ps, s)
					}
					fmt.Fprintf(&b, " Arguments: %s.", strings.Join(ps, "; "))
				}
				b.WriteString("\n")
			}
			desc = b.String()
		}
		nameSchema := map[string]any{"type": "string", "description": "Tool name"}
		if len(names) > 0 {
			nameSchema["enum"] = names
		}
		out = append(out, map[string]any{
			"name":        ToolName,
			"description": desc,
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": nameSchema, "arguments": map[string]any{"type": "object", "description": "Arguments for the tool", "additionalProperties": true}},
				"required":   []string{"name"},
			},
		})
		return map[string]any{"tools": out}, nil
	case "tools/call":
		var head struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &head); err != nil {
			return nil, &rpcErr{Code: -32602, Message: "invalid params"}
		}
		if head.Name == PermissionTool {
			return c.permission(head.Arguments), nil
		}
		if head.Name != ToolName {
			res, err := c.builtin(head.Name, decodeArgs(head.Arguments))
			if err != nil {
				return toolText(err.Error(), true), nil
			}
			return toolText(res.Output, res.Error != ""), nil
		}
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{Code: -32602, Message: "invalid params"}
		}
		if p.Name != ToolName {
			return nil, &rpcErr{Code: -32602, Message: "unknown tool " + p.Name}
		}
		res, err := c.call(p.Arguments.Name, decodeArgs(p.Arguments.Arguments))
		if err != nil {
			return toolText(err.Error(), true), nil
		}
		if res.Error != "" {
			return toolText(res.Error+"\n"+res.Output, true), nil
		}
		txt := res.Output
		if !res.OK {
			txt = fmt.Sprintf("exit code %d\n%s", res.ExitCode, res.Output)
		}
		return toolText(txt, !res.OK), nil
	}
	return nil, &rpcErr{Code: -32601, Message: "method not found: " + req.Method}
}

func toolText(s string, isErr bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}, "isError": isErr}
}

type bridgeClient struct {
	url, tok string
	hc       *http.Client
	agent    string // random id of this agent process (one run)
}

func agentID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// permission answers one permission prompt in the format Claude Code
// expects from a --permission-prompt-tool: a JSON text block with
// behavior allow (and the input to run with) or deny (and a message).
func (c *bridgeClient) permission(raw json.RawMessage) map[string]any {
	var p struct {
		Tool  string         `json:"tool_name"`
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.Input == nil {
		p.Input = map[string]any{}
	}
	var ans PermissionAnswer
	if err := c.do("POST", "/permission", PermissionRequest{Agent: c.agent, Tool: p.Tool, Input: p.Input}, &ans); err != nil {
		ans = PermissionAnswer{Message: "could not ask the user: " + err.Error()}
	}
	var out map[string]any
	if ans.Allow {
		out = map[string]any{"behavior": "allow", "updatedInput": p.Input}
	} else {
		out = map[string]any{"behavior": "deny", "message": firstNonBlank(ans.Message, "The user denied this.")}
	}
	b, _ := json.Marshal(out)
	return toolText(string(b), false)
}

func firstNonBlank(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (c *bridgeClient) do(method, path string, body any, out any) error {
	if c.url == "" {
		return fmt.Errorf("CODEGEN_BRIDGE_URL is not set; start agentbridge from the Codegen app")
	}
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, r)
	req.Header.Set("Authorization", "Bearer "+c.tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("bridge: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *bridgeClient) list() ([]ToolInfo, error) {
	var out []ToolInfo
	return out, c.do("GET", "/list", nil, &out)
}

func (c *bridgeClient) call(name string, args map[string]any) (RunResult, error) {
	var out RunResult
	return out, c.do("POST", "/call", map[string]any{"name": name, "arguments": args}, &out)
}

// decodeArgs accepts tool arguments as an object or a JSON-encoded string
// (some models send the nested object stringified).
func decodeArgs(raw json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && json.Unmarshal([]byte(s), &m) == nil {
		return m
	}
	return nil
}

// BuiltinInfo is a built-in tool as the bridge lists it.
type BuiltinInfo struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	ReadOnly    bool           `json:"readOnly"`
	Group       string         `json:"group"`
}

func (c *bridgeClient) builtins() ([]BuiltinInfo, error) {
	var out []BuiltinInfo
	return out, c.do("GET", "/builtins", nil, &out)
}

func (c *bridgeClient) builtin(name string, args map[string]any) (BuiltinResult, error) {
	var out BuiltinResult
	return out, c.do("POST", "/builtin", map[string]any{"name": name, "arguments": args}, &out)
}

// BuiltinResult is the bridge's answer to a built-in tool call.
type BuiltinResult struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}
