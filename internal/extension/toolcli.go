package extension

// SpecWright's tools without MCP. Where an agent's CLI does not load the
// agentbridge MCP server (a company policy for MCP servers, say), the agent
// runs them with its own shell instead:
//
//	"$CODEGEN_TOOL" tool list
//	"$CODEGEN_TOOL" tool save_flow '{"flow": {...}}'
//
// CODEGEN_TOOL is this program; the call goes to the app over the same
// loopback bridge (CODEGEN_BRIDGE_URL / _TOKEN) the MCP server uses, so it
// gets the same checks, policy and approvals. The Copilot permission hook
// lets exactly that command through (ownToolCommand) and nothing chained to
// it.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ToolEnv names this program for agents: "$CODEGEN_TOOL" tool …
const ToolEnv = "CODEGEN_TOOL"

// RunToolCLI implements `SpecWright tool …` and returns the exit code.
func RunToolCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	url, tok := os.Getenv("CODEGEN_BRIDGE_URL"), os.Getenv("CODEGEN_BRIDGE_TOKEN")
	if url == "" || tok == "" {
		fmt.Fprintln(stderr, "SpecWright tool: run it from an agent that SpecWright started (CODEGEN_BRIDGE_URL is not set)")
		return 2
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(stdout, `Usage:
  "$CODEGEN_TOOL" tool list                       SpecWright's tools and the project's tools
  "$CODEGEN_TOOL" tool <name> '<arguments JSON>'  run one (arguments: a JSON object; - reads it from stdin)`)
		return 0
	}
	c := &bridgeClient{url: url, tok: tok, hc: &http.Client{Timeout: 15 * time.Minute}, agent: firstNonBlank(os.Getenv("CODEGEN_AGENT_ID"), agentID())}
	bs, err := c.builtins()
	if err != nil {
		fmt.Fprintln(stderr, "SpecWright tool: the app does not answer:", err)
		return 2
	}
	if args[0] == "list" {
		sort.Slice(bs, func(i, j int) bool { return bs[i].App && !bs[j].App })
		for _, b := range bs {
			fmt.Fprintf(stdout, "%s\t%s\n", b.Name, firstLine(b.Description))
		}
		if ts, err := c.list(); err == nil {
			for _, t := range ts {
				fmt.Fprintf(stdout, "%s\t(project tool) %s\n", t.Name, firstLine(t.Desc))
			}
		}
		fmt.Fprintln(stdout, "\nArguments are a JSON object; `\"$CODEGEN_TOOL\" tool <name> --schema` shows a tool's.")
		return 0
	}
	name := AppToolName(args[0])
	raw := "{}"
	if len(args) > 1 {
		raw = args[1]
	}
	if raw == "--schema" {
		for _, b := range bs {
			if b.Name == name {
				s, _ := json.MarshalIndent(b.InputSchema, "", "  ")
				fmt.Fprintf(stdout, "%s: %s\n%s\n", b.Name, b.Description, s)
				return 0
			}
		}
		if ts, err := c.list(); err == nil {
			for _, t := range ts {
				if t.Name == name {
					s, _ := json.MarshalIndent(t.Params, "", "  ")
					fmt.Fprintf(stdout, "%s: %s\nparameters: %s\n", t.Name, t.Desc, s)
					return 0
				}
			}
		}
		fmt.Fprintf(stderr, "no tool named %s (see: tool list)\n", name)
		return 1
	}
	if raw == "-" {
		b, err := io.ReadAll(io.LimitReader(stdin, 16<<20))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		raw = string(b)
	}
	var a map[string]any
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			fmt.Fprintf(stderr, "the arguments are not a JSON object: %v\n", err)
			return 2
		}
	}
	for _, b := range bs {
		if b.Name == name {
			res, err := c.builtin(name, a)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintln(stdout, res.Output)
			if res.Error != "" {
				return 1
			}
			return 0
		}
	}
	res, err := c.call(name, a) // a project tool (or SpecWright's, by extension_managed's rules)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if res.Error != "" {
		fmt.Fprintln(stderr, res.Error)
		if res.Output != "" {
			fmt.Fprintln(stdout, res.Output)
		}
		return 1
	}
	fmt.Fprintln(stdout, res.Output)
	if !res.OK {
		return 1
	}
	return 0
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ownToolCommand: the shell call is only `"$CODEGEN_TOOL" tool …` (this
// program by its path, or the variable), its words plain or quoted, with
// nothing that could chain another command (; & | < > ` $( newline).
func ownToolCommand(tool string, input map[string]any) bool {
	switch strings.ToLower(tool) {
	case "bash", "shell", "powershell", "run_command", "run_in_terminal":
	default:
		return false
	}
	cmd, _ := input["command"].(string)
	words, ok := shellWords(strings.TrimSpace(cmd))
	if !ok || len(words) < 2 || words[1] != "tool" {
		return false
	}
	self := os.Getenv(ToolEnv)
	return words[0] == "$"+ToolEnv || words[0] == "${"+ToolEnv+"}" || (self != "" && words[0] == self)
}

// shellWords splits a simple command into its words; false for anything a
// shell would treat as more than one plain command. "$CODEGEN_TOOL" and
// ${CODEGEN_TOOL} are kept as written (the only expansion allowed).
func shellWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	in := false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
			i++
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			in = true
			i += j + 2
		case c == '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, false
			}
			part := s[i+1 : i+1+j]
			if part != "$"+ToolEnv && part != "${"+ToolEnv+"}" && strings.ContainsAny(part, "$`\\") {
				return nil, false
			}
			cur.WriteString(part)
			in = true
			i += j + 2
		case c == '$':
			for _, v := range []string{"$" + ToolEnv, "${" + ToolEnv + "}"} {
				if strings.HasPrefix(s[i:], v) && len(words) == 0 && !in {
					cur.WriteString(v)
					in = true
					i += len(v)
					goto next
				}
			}
			return nil, false
		case strings.IndexByte(";&|<>`()\n\r\\*?[]{}~!#", c) >= 0:
			return nil, false
		default:
			cur.WriteByte(c)
			in = true
			i++
		}
	next:
	}
	if in {
		words = append(words, cur.String())
	}
	return words, true
}
