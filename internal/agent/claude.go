package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Claude drives `claude -p --output-format stream-json`.
type Claude struct {
	Bin            string // override; default looks up "claude"
	PermissionMode string // default acceptEdits
}

// NewClaude returns the Claude Code driver.
func NewClaude() *Claude { return &Claude{PermissionMode: "acceptEdits"} }

// Name implements Driver.
func (c *Claude) Name() string { return "claude" }

func (c *Claude) bin() (string, error) {
	if c.Bin != "" {
		return c.Bin, nil
	}
	return findBinary("claude")
}

// Status implements Driver.
func (c *Claude) Status(ctx context.Context) Status {
	s := Status{Name: "claude", Label: "Claude Code", Billing: "usd"}
	p, err := c.bin()
	if err != nil {
		s.Problem = "Install Claude Code (npm i -g @anthropic-ai/claude-code) and sign in with `claude`."
		return s
	}
	s.Path, s.Available = p, true
	s.Version = version(ctx, p)
	return s
}

// Args builds the CLI arguments for req (exported for tests and the
// headless runner's --dry-run).
func (c *Claude) Args(req Request) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if req.ReadOnly { // no edits, no commands: denied, not asked
		args = append(args, "--permission-mode", "default", "--disallowedTools", "Bash Edit Write MultiEdit NotebookEdit")
	} else if c.PermissionMode != "" {
		args = append(args, "--permission-mode", c.PermissionMode)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if len(req.AddDirs) > 0 {
		args = append(args, append([]string{"--add-dir"}, req.AddDirs...)...)
	}
	if req.MCPConfig != "" {
		// SpecWright's run_command replaces Claude's own Bash: in -p mode
		// Bash can only run pre-approved read-only commands, so builds and
		// package installs would stop at a permission prompt nobody sees.
		// run_command runs in the workspace with the proxy/registry
		// environment and a timeout, and shows up in the run's activity.
		args = append(args, "--mcp-config", req.MCPConfig, "--disallowedTools", "Bash")
		// Tools without standing permission would otherwise be denied in
		// -p mode with nobody asked; the bridge asks the user in the app.
		args = append(args, "--permission-prompt-tool", "mcp__agentbridge__permission_prompt")
	}
	if len(req.AllowTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowTools, " "))
	}
	if req.SessionID != "" { // the conversation, so it can be continued (or is)
		if req.Resume {
			args = append(args, "--resume", req.SessionID)
		} else {
			args = append(args, "--session-id", req.SessionID)
		}
	}
	return args
}

type claudeMsg struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Model   string `json:"model"`
	Message struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
		Usage      claudeUsage     `json:"usage"`
	} `json:"message"`
	Result       string      `json:"result"`
	IsError      bool        `json:"is_error"`
	TotalCostUSD float64     `json:"total_cost_usd"`
	DurationMS   int64       `json:"duration_ms"`
	NumTurns     int         `json:"num_turns"`
	Usage        claudeUsage `json:"usage"`
}

type claudeUsage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// Run implements Driver.
func (c *Claude) Run(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	bin, err := c.bin()
	if err != nil {
		return Result{}, err
	}
	p := newClaudeParser(emit)
	_, _, err = proc{bin: bin, args: c.Args(req), dir: req.Dir, env: req.Env, stdin: req.Prompt,
		onLine: p.line,
		onErr: func(l string) {
			emit(Event{Type: "agent.log", Summary: short(l, 200), Data: map[string]any{"line": l, "stream": "stderr"}})
		},
	}.run(ctx)
	res := p.result()
	res.SessionID = req.SessionID
	if err == nil && p.isError {
		err = fmt.Errorf("claude reported an error: %s", short(res.Text, 300))
	}
	return res, err
}

type claudeParser struct {
	emit     func(Event)
	seen     map[string]bool
	started  map[string]time.Time
	names    map[string]string
	res      Result
	isError  bool
	lastText string
}

func newClaudeParser(emit func(Event)) *claudeParser {
	return &claudeParser{emit: emit, seen: map[string]bool{}, started: map[string]time.Time{}, names: map[string]string{}}
}

func (p *claudeParser) result() Result { return p.res }

func (p *claudeParser) line(l string) {
	l = strings.TrimSpace(l)
	if l == "" {
		return
	}
	var m claudeMsg
	if err := json.Unmarshal([]byte(l), &m); err != nil {
		p.emit(Event{Type: "agent.log", Summary: short(l, 200), Data: map[string]any{"line": l}})
		return
	}
	switch m.Type {
	case "system":
		if m.Subtype == "init" {
			p.res.Model = m.Model
		}
	case "assistant":
		var blocks []claudeBlock
		_ = json.Unmarshal(m.Message.Content, &blocks)
		// One message can arrive as several events (one per block); count its
		// usage once, keyed by message id.
		if id := m.Message.ID; id != "" && !p.seen[id] {
			p.seen[id] = true
			u := m.Message.Usage
			model := m.Message.Model
			if model == "" {
				model = p.res.Model
			}
			p.emit(Event{Type: "model.request", Summary: "Request → " + model,
				Data: map[string]any{"input_tokens": u.Input + u.CacheRead + u.CacheCreation, "cached_tokens": u.CacheRead, "model": model}})
			stop := m.Message.StopReason
			hasTool := false
			for _, b := range blocks {
				if b.Type == "tool_use" {
					hasTool = true
				}
			}
			if stop == "" {
				stop = map[bool]string{true: "tool_use", false: "end_turn"}[hasTool]
			}
			p.emit(Event{Type: "model.response", Summary: "Response · " + stop,
				Data: map[string]any{"output_tokens": u.Output, "stop_reason": stop}})
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" && t != p.lastText {
					p.lastText = t
					p.emit(Event{Type: "model.text", Summary: short(t, 160), Data: map[string]any{"text": t}})
				}
			case "tool_use":
				p.started[b.ID] = time.Now()
				p.names[b.ID] = b.Name
				p.emit(Event{Type: "tool.call", Summary: strings.TrimSpace(b.Name + " " + short(toolTarget(b.Input), 120)),
					Data: map[string]any{"tool": b.Name, "args": b.Input, "id": b.ID}})
			}
		}
	case "user":
		var blocks []claudeBlock
		_ = json.Unmarshal(m.Message.Content, &blocks)
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			name := p.names[b.ToolUseID]
			d := time.Since(p.started[b.ToolUseID]).Milliseconds()
			out := blockText(b.Content)
			state := "ok"
			if b.IsError {
				state = "failed"
			}
			p.emit(Event{Type: "tool.result", Summary: fmt.Sprintf("%s %s · %dms", name, state, d),
				Data: map[string]any{"tool": name, "id": b.ToolUseID, "ok": !b.IsError, "duration_ms": d, "bytes": len(out), "output": short(out, 2000)}})
		}
	case "result":
		p.res.Text = m.Result
		p.res.CostUSD = m.TotalCostUSD
		p.isError = m.IsError || (m.Subtype != "" && m.Subtype != "success")
		p.emit(Event{Type: "run.usage", Summary: fmt.Sprintf("Claude: %d turns · $%.4f", m.NumTurns, m.TotalCostUSD),
			Data: map[string]any{"cost_usd": m.TotalCostUSD, "turns": m.NumTurns, "duration_ms": m.DurationMS,
				// the run's real totals (a message's own usage arrives when it starts, before its output);
				// own keys, so code that adds up the messages' usage does not count them twice
				"total_input_tokens": m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheCreation, "total_cached_tokens": m.Usage.CacheRead,
				"total_output_tokens": m.Usage.Output}})
	}
}

// blockText flattens a tool_result content (string or [{type:text,text}]).
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return string(raw)
}
