package flow

// A Decision step that the AI decides (Decide "ai"): the step's question
// (Prompt), its paths with a hint each for when to take it (Branch.Hint),
// and the data and context that reach it go to the AI, which must answer
// with one of the paths, a reason and how sure it is. Below Confidence
// (percent, default DefaultConfidence) the step does what Unsure says: ask
// a person (default; in a run nobody reviews, it takes Otherwise when that
// path leads somewhere, else the AI's choice), take Otherwise, or accept
// the AI's choice. The runner is in core (flowrun_decide.go).

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DefaultConfidence is the confidence below which an AI decision is unsure.
const DefaultConfidence = 70

// UnsureModes: what an AI decision does when it is unsure.
var UnsureModes = []string{"ask", "else", "accept"}

// AIDecides says a Decision step is decided by the AI.
func AIDecides(n Node) bool { return n.Type == "decision" && n.Decide == "ai" }

// ConfidenceOf is the step's threshold (percent).
func ConfidenceOf(n Node) int {
	if n.Confidence <= 0 {
		return DefaultConfidence
	}
	return min(n.Confidence, 100)
}

// AIDecision is the AI's answer.
type AIDecision struct {
	Path       string // a branch id, or ElseBranch
	Reason     string
	Confidence int // 0–100
}

// ParseAIDecision reads the AI's reply: {"path", "reason", "confidence"}.
// The path may be given by id or by label; "else", "otherwise" and "none"
// mean Otherwise. Confidence may be 0–1 or 0–100.
func ParseAIDecision(reply string, n Node) (AIDecision, error) {
	s := strings.TrimSpace(reply)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	var raw struct {
		Path       any    `json:"path"`
		Reason     string `json:"reason"`
		Confidence any    `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return AIDecision{}, fmt.Errorf("the reply is not the JSON asked for: %v", err)
	}
	p := strings.TrimSpace(fmt.Sprint(raw.Path))
	out := AIDecision{Reason: strings.TrimSpace(raw.Reason)}
	switch strings.ToLower(p) {
	case "else", "otherwise", "none", "":
		out.Path = ElseBranch
	default:
		for _, b := range n.Branches {
			if strings.EqualFold(b.ID, p) || strings.EqualFold(strings.TrimSpace(b.Label), p) {
				out.Path = b.ID
			}
		}
		if out.Path == "" {
			return AIDecision{}, fmt.Errorf("%q is not one of the paths", p)
		}
	}
	switch c := raw.Confidence.(type) {
	case float64:
		if c <= 1 && c > 0 {
			c *= 100
		}
		out.Confidence = int(c + 0.5)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(c), "%"), 64)
		if err == nil {
			if f <= 1 && f > 0 {
				f *= 100
			}
			out.Confidence = int(f + 0.5)
		}
	}
	out.Confidence = min(max(out.Confidence, 0), 100)
	return out, nil
}

func (f *Flow) checkAIDecision(n Node, add func(node, level, format string, a ...any)) {
	label := firstNonEmptyStr(n.Title, n.ID)
	if strings.TrimSpace(n.Prompt) == "" {
		add(n.ID, "error", "%s: write the question the AI decides (e.g. Can this release go out today?).", label)
	}
	if n.Confidence < 0 || n.Confidence > 100 {
		add(n.ID, "error", "%s: confidence is 0 to 100%%.", label)
	}
	if n.Unsure != "" && !contains(UnsureModes, n.Unsure) {
		add(n.ID, "error", "%s: when unsure it asks, takes Otherwise, or accepts the AI's choice.", label)
	}
	for _, b := range n.Branches {
		if strings.TrimSpace(b.Label) == "" {
			add(n.ID, "error", "%s: give the path %s a name the AI can choose.", label, b.ID)
		}
	}
	if len(n.Branches) < 1 {
		add(n.ID, "error", "%s: add the paths the AI chooses between.", label)
	}
}
