package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// runJSON runs Copilot with --output-format json and reads its event stream:
//
//	assistant.message       content (text) and toolRequests [{toolCallId, name, arguments}]
//	model.call_start        one model request
//	tool.execution_start    toolCallId, toolName
//	tool.execution_complete toolCallId, success, result.content
//	result                  exitCode, usage.premiumRequests
//
// Token counts and AI units come from --usage-output-file at the end.
func (c *Copilot) runJSON(ctx context.Context, bin string, req Request, emit func(Event)) (Result, error) {
	uf, err := os.CreateTemp("", "specwright-copilot-usage-*.json")
	if err != nil {
		return Result{}, err
	}
	uf.Close()
	defer os.Remove(uf.Name())
	plugin, cleanup := permissionPlugin(req.MCPConfig, req.AddDirs...)
	defer cleanup()
	args := append(append(c.argsWith(req, plugin), c.extraArgs(ctx, bin, req)...), "--output-format", "json", "--usage-output-file", uf.Name())
	base, dropped := baseEnv()
	noteDropped(emit, dropped)
	p := &copilotJSON{emit: emit, names: map[string]string{}, started: map[string]time.Time{}, model: modelOr(req.Model, "copilot/default")}
	_, _, err = proc{bin: bin, args: args, dir: req.Dir, env: c.env(req), base: base, onLine: p.line,
		onErr: func(l string) {
			emit(Event{Type: "agent.log", Summary: short(l, 200), Data: map[string]any{"line": l, "stream": "stderr"}})
		},
	}.run(ctx)
	p.usage(uf.Name())
	if c.hasFlag(ctx, bin, "--session-id") {
		p.res.SessionID = req.SessionID
	}
	return p.res, err
}

type copilotJSON struct {
	emit    func(Event)
	names   map[string]string
	started map[string]time.Time
	model   string
	res     Result
	lastTxt string
}

type cpEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	// result
	ExitCode *int `json:"exitCode"`
	Usage    *struct {
		PremiumRequests float64 `json:"premiumRequests"`
	} `json:"usage"`
}

// toolName maps Copilot's MCP tool names ("agentbridge-read_file") to the
// mcp__server__tool form the rest of the app uses.
func cpToolName(n string) string {
	if rest, ok := strings.CutPrefix(n, "agentbridge-"); ok {
		return "mcp__agentbridge__" + rest
	}
	return n
}

func (p *copilotJSON) line(l string) {
	l = strings.TrimSpace(l)
	if l == "" || l[0] != '{' {
		if l != "" {
			p.emit(Event{Type: "agent.log", Summary: short(l, 200), Data: map[string]any{"line": l}})
		}
		return
	}
	var e cpEvent
	if json.Unmarshal([]byte(l), &e) != nil {
		return
	}
	switch e.Type {
	case "model.call_start":
		var d struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(e.Data, &d)
		if d.Model != "" {
			p.model = d.Model
			p.res.Model = d.Model
		}
		p.emit(Event{Type: "model.request", Summary: "Request → " + p.model, Data: map[string]any{"input_tokens": 0, "model": p.model}})
	case "assistant.message":
		var d struct {
			Content      string `json:"content"`
			Model        string `json:"model"`
			ToolRequests []struct {
				ID        string         `json:"toolCallId"`
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
				Title     string         `json:"intentionSummary"`
			} `json:"toolRequests"`
		}
		_ = json.Unmarshal(e.Data, &d)
		stop := "end_turn"
		if len(d.ToolRequests) > 0 {
			stop = "tool_use"
		}
		p.emit(Event{Type: "model.response", Summary: "Response · " + stop, Data: map[string]any{"output_tokens": 0, "stop_reason": stop}})
		if t := strings.TrimSpace(d.Content); t != "" && t != p.lastTxt {
			p.lastTxt = t
			p.res.Text = t
			p.emit(Event{Type: "model.text", Summary: short(t, 160), Data: map[string]any{"text": t}})
		}
		for _, tr := range d.ToolRequests {
			name := cpToolName(tr.Name)
			p.names[tr.ID] = name
			p.started[tr.ID] = time.Now()
			p.emit(Event{Type: "tool.call", Summary: strings.TrimSpace(name + " " + short(toolTarget(tr.Arguments), 120)),
				Data: map[string]any{"tool": name, "args": tr.Arguments, "id": tr.ID}})
		}
	case "tool.execution_complete":
		var d struct {
			ID      string `json:"toolCallId"`
			Success bool   `json:"success"`
			Result  struct {
				Content string `json:"content"`
			} `json:"result"`
			Error any `json:"error"`
		}
		_ = json.Unmarshal(e.Data, &d)
		name := p.names[d.ID]
		ms := time.Since(p.started[d.ID]).Milliseconds()
		out := d.Result.Content
		if out == "" && d.Error != nil {
			out = fmt.Sprint(d.Error)
		}
		p.emit(Event{Type: "tool.result", Summary: fmt.Sprintf("%s %s · %dms", name, map[bool]string{true: "ok", false: "failed"}[d.Success], ms),
			Data: map[string]any{"tool": name, "id": d.ID, "ok": d.Success, "duration_ms": ms, "bytes": len(out), "output": short(out, 2000)}})
	case "result":
		if e.Usage != nil {
			p.res.PremiumRequests = int(e.Usage.PremiumRequests + 0.5)
		}
		if e.ExitCode != nil {
			p.res.ExitCode = *e.ExitCode
		}
	}
}

// usage reads --usage-output-file and emits the run's totals.
func (p *copilotJSON) usage(path string) {
	var u struct {
		Premium  float64 `json:"totalPremiumRequestCost"`
		NanoAiu  float64 `json:"totalNanoAiu"`
		Duration int64   `json:"totalApiDurationMs"`
		Models   map[string]struct {
			Requests struct {
				Count int `json:"count"`
			} `json:"requests"`
			Usage struct {
				In        int `json:"inputTokens"`
				Out       int `json:"outputTokens"`
				CacheRead int `json:"cacheReadTokens"`
			} `json:"usage"`
		} `json:"modelMetrics"`
	}
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, &u)
	}
	in, out, cached := 0, 0, 0
	var models []string
	for m, mm := range u.Models {
		in += mm.Usage.In
		out += mm.Usage.Out
		cached += mm.Usage.CacheRead
		models = append(models, m)
	}
	if u.Premium > 0 {
		p.res.PremiumRequests = int(u.Premium + 0.5)
	}
	if len(models) == 1 {
		p.res.Model = models[0]
	}
	aiu := u.NanoAiu / 1e9
	p.emit(Event{Type: "run.usage", Summary: fmt.Sprintf("Copilot: %d premium request(s) · %.2f AI units · %d in / %d out tokens", p.res.PremiumRequests, aiu, in, out),
		Data: map[string]any{"premium_requests": p.res.PremiumRequests, "ai_units": aiu, "input_tokens": in, "output_tokens": out, "cached_tokens": cached,
			"model": p.res.Model, "api_duration_ms": u.Duration}})
}
