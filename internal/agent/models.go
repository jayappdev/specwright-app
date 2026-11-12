package agent

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ModelInfo is one model an agent CLI can run with (for the model pickers).
type ModelInfo struct {
	ID    string `json:"id"`              // what is passed to --model
	Label string `json:"label"`           // display name
	Note  string `json:"note,omitempty"`  // one line on when to pick it
	Tier  string `json:"tier,omitempty"`  // fast | balanced | best | auto
	Alias bool   `json:"alias,omitempty"` // follows the latest version
}

// ModelLister is implemented by drivers that know their models.
type ModelLister interface {
	Models(ctx context.Context) []ModelInfo
}

// Models implements ModelLister. Claude Code takes an alias for the latest
// model of a family, or a model's full name.
func (c *Claude) Models(context.Context) []ModelInfo {
	return []ModelInfo{
		{ID: "opus", Label: "Opus", Note: "Most capable for long, complex coding work", Tier: "best", Alias: true},
		{ID: "fable", Label: "Fable", Note: "Frontier reasoning for the hardest problems", Tier: "best", Alias: true},
		{ID: "sonnet", Label: "Sonnet", Note: "Fast and strong; a good everyday default", Tier: "balanced", Alias: true},
		{ID: "haiku", Label: "Haiku", Note: "Fastest and cheapest, for quick questions", Tier: "fast", Alias: true},
		{ID: "claude-opus-5-5", Label: "Claude Opus 5.5", Tier: "best"},
		{ID: "claude-fable-5-1", Label: "Claude Fable 5.1", Tier: "best"},
		{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5", Tier: "balanced"},
		{ID: "claude-haiku-5-5", Label: "Claude Haiku 5.5", Tier: "fast"},
	}
}

var (
	copilotModelsOnce sync.Once
	copilotModels     []ModelInfo
	quoted            = regexp.MustCompile(`^\s*-\s*"([^"]+)"`)
)

// Models implements ModelLister: the list `copilot help config` prints
// under `model` (read once per app run), with Auto first.
func (c *Copilot) Models(ctx context.Context) []ModelInfo {
	copilotModelsOnce.Do(func() {
		bin, err := c.bin()
		if err != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, bin, "help", "config").Output()
		if err != nil {
			return
		}
		copilotModels = parseCopilotModels(string(out))
	})
	list := []ModelInfo{{ID: "auto", Label: "Auto", Note: "Copilot picks a model for each request", Tier: "auto"}}
	return append(list, copilotModels...)
}

func parseCopilotModels(help string) []ModelInfo {
	var out []ModelInfo
	in := false
	for _, l := range strings.Split(help, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "`model`:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		m := quoted.FindStringSubmatch(l)
		if m == nil {
			if t == "" || strings.HasPrefix(t, "`") {
				break
			}
			continue
		}
		out = append(out, ModelInfo{ID: m[1], Label: prettyModel(m[1]), Tier: tierOf(m[1])})
	}
	return out
}

func prettyModel(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		switch {
		case p == "gpt":
			parts[i] = "GPT"
		case p != "" && (p[0] < '0' || p[0] > '9'):
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func tierOf(id string) string {
	l := strings.ToLower(id)
	switch {
	case strings.Contains(l, "haiku"), strings.Contains(l, "mini"), strings.Contains(l, "flash"), strings.Contains(l, "nano"):
		return "fast"
	case strings.Contains(l, "opus"), strings.Contains(l, "fable"), strings.Contains(l, "pro"):
		return "best"
	}
	return "balanced"
}

// Models implements ModelLister for the scripted mock agent.
func (m *Mock) Models(context.Context) []ModelInfo {
	return []ModelInfo{{ID: "mock-fast", Label: "Mock fast", Tier: "fast"}, {ID: "mock-best", Label: "Mock best", Tier: "best"}}
}
