package agent

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Copilot drives the GitHub Copilot CLI in programmatic mode
// (`copilot -p … --allow-all-tools`). CLIs that support it (1.0+) run with
// `--output-format json` (JSONL events) and `--usage-output-file` (tokens,
// premium requests, AI units); older ones print text, which a fallback
// parser reads.
type Copilot struct {
	Bin       string
	DenyTools []string

	jsonOnce sync.Once
	jsonOK   bool
}

// structured reports whether this CLI supports --output-format json.
func (c *Copilot) structured(ctx context.Context, bin string) bool {
	c.jsonOnce.Do(func() {
		out, _ := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
		c.jsonOK = strings.Contains(string(out), "--output-format") && strings.Contains(string(out), "--usage-output-file")
	})
	return c.jsonOK
}

// NewCopilot returns the Copilot CLI driver.
func NewCopilot() *Copilot {
	return &Copilot{DenyTools: []string{"shell(git push)", "shell(rm -rf)"}}
}

// Name implements Driver.
func (c *Copilot) Name() string { return "copilot" }

func (c *Copilot) bin() (string, error) {
	if c.Bin != "" {
		return c.Bin, nil
	}
	return findBinary("copilot")
}

// Status implements Driver.
func (c *Copilot) Status(ctx context.Context) Status {
	s := Status{Name: "copilot", Label: "GitHub Copilot CLI", Billing: "premium"}
	p, err := c.bin()
	if err != nil {
		s.Problem = "Install the Copilot CLI (npm i -g @github/copilot) and run `copilot` once to sign in."
		return s
	}
	s.Path, s.Available = p, true
	s.Version = version(ctx, p)
	return s
}

// Args builds the CLI arguments for req.
func (c *Copilot) Args(req Request) []string {
	args := []string{"-p", req.Prompt, "--allow-all-tools", "--no-color"}
	for _, d := range c.DenyTools {
		args = append(args, "--deny-tool", d)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.MCPConfig != "" {
		args = append(args, "--additional-mcp-config", "@"+req.MCPConfig)
	}
	return args
}

// Run implements Driver.
func (c *Copilot) Run(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	bin, err := c.bin()
	if err != nil {
		return Result{}, err
	}
	if c.structured(ctx, bin) {
		return c.runJSON(ctx, bin, req, emit)
	}
	p := &copilotParser{emit: emit}
	emit(Event{Type: "model.request", Summary: "Request → Copilot", Data: map[string]any{"input_tokens": 0, "model": modelOr(req.Model, "copilot/default")}})
	_, _, err = proc{bin: bin, args: c.Args(req), dir: req.Dir, env: req.Env, onLine: p.line,
		onErr: func(l string) {
			emit(Event{Type: "agent.log", Summary: short(l, 200), Data: map[string]any{"line": l, "stream": "stderr"}})
		},
	}.run(ctx)
	p.flush()
	return p.finish(), err
}

func modelOr(m, d string) string {
	if m != "" {
		return m
	}
	return d
}

var (
	cpToolRE    = regexp.MustCompile(`^\s*([●✓✔✗✘])\s+(.+)$`)
	cpPremiumRE = regexp.MustCompile(`(?i)total usage est:\s*([\d.]+)\s+premium request`)
	cpModelRE   = regexp.MustCompile(`(?i)^\s*([\w.\-/]+)\s+([\d.]+)(k|m)?\s+input,\s*([\d.]+)(k|m)?\s+output(?:,\s*([\d.]+)(k|m)?\s+cache read)?`)
)

type copilotParser struct {
	emit     func(Event)
	buf      []string
	res      Result
	in, out  int
	cached   int
	inUsage  bool
	lastTool string
}

func (p *copilotParser) line(l string) {
	if m := cpPremiumRE.FindStringSubmatch(l); m != nil {
		p.flush()
		f, _ := strconv.ParseFloat(m[1], 64)
		p.res.PremiumRequests = int(f + 0.5)
		p.inUsage = true
		return
	}
	if p.inUsage {
		if m := cpModelRE.FindStringSubmatch(l); m != nil {
			p.res.Model = m[1]
			p.in += num(m[2], m[3])
			p.out += num(m[4], m[5])
			if m[6] != "" {
				p.cached += num(m[6], m[7])
			}
		}
		return
	}
	if m := cpToolRE.FindStringSubmatch(l); m != nil {
		p.flush()
		desc := strings.TrimSpace(m[2])
		name, target, _ := strings.Cut(desc, " ")
		ok := m[1] != "✗" && m[1] != "✘"
		if m[1] == "●" {
			p.lastTool = name
			p.emit(Event{Type: "tool.call", Summary: short(desc, 140), Data: map[string]any{"tool": name, "args": map[string]any{"target": target}}})
			return
		}
		if p.lastTool == "" {
			p.emit(Event{Type: "tool.call", Summary: short(desc, 140), Data: map[string]any{"tool": name, "args": map[string]any{"target": target}}})
		}
		p.emit(Event{Type: "tool.result", Summary: fmt.Sprintf("%s %s", name, map[bool]string{true: "ok", false: "failed"}[ok]), Data: map[string]any{"tool": name, "ok": ok}})
		p.lastTool = ""
		return
	}
	if strings.TrimSpace(l) == "" {
		p.flush()
		return
	}
	p.buf = append(p.buf, l)
}

func (p *copilotParser) flush() {
	if len(p.buf) == 0 {
		return
	}
	t := strings.TrimSpace(strings.Join(p.buf, "\n"))
	p.buf = nil
	if t == "" {
		return
	}
	p.res.Text = t
	p.emit(Event{Type: "model.text", Summary: short(t, 160), Data: map[string]any{"text": t}})
}

func (p *copilotParser) finish() Result {
	p.emit(Event{Type: "model.response", Summary: "Copilot finished", Data: map[string]any{"output_tokens": p.out, "stop_reason": "end_turn"}})
	p.emit(Event{Type: "run.usage", Summary: fmt.Sprintf("Copilot: %d premium request(s)", p.res.PremiumRequests),
		Data: map[string]any{"premium_requests": p.res.PremiumRequests, "input_tokens": p.in, "output_tokens": p.out, "cached_tokens": p.cached, "model": p.res.Model}})
	return p.res
}

func num(s, unit string) int {
	f, _ := strconv.ParseFloat(s, 64)
	switch strings.ToLower(unit) {
	case "k":
		f *= 1e3
	case "m":
		f *= 1e6
	}
	return int(f + 0.5)
}
