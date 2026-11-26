package extension

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ToolGuide is a tool's usage guide: what it is for, when and how to use it,
// and worked examples. Examples can be run from the tool's page and opened
// as a flow in the flow designer. Like test cases, a guide does not change
// what runs, so saving one keeps the tool approved; it travels in tool files
// and in the admin catalog (stored, not signed).
type ToolGuide struct {
	Summary  string        `json:"summary,omitempty"`
	When     []string      `json:"when,omitempty"`
	How      []string      `json:"how,omitempty"`
	Examples []ToolExample `json:"examples,omitempty"`
	Updated  time.Time     `json:"updated,omitzero"`
}

// ToolExample is one worked example: the arguments to call the tool with in
// a flow step ({{...}} placeholders allowed), an optional sample of what the
// previous step gives it (available as {{sample.output}}, {{input_file}} and
// {{input}}), and the result it gives.
type ToolExample struct {
	Title  string            `json:"title"`
	Text   string            `json:"text,omitempty"`
	Args   map[string]string `json:"args"`
	Sample string            `json:"sample,omitempty"`
	Result string            `json:"result,omitempty"`
}

// Guide limits keep a tool record small.
const (
	MaxGuideExamples = 20
	maxGuideText     = 64 << 10
)

// Empty reports whether the guide says nothing.
func (g *ToolGuide) Empty() bool {
	return g == nil || (strings.TrimSpace(g.Summary) == "" && len(g.When) == 0 && len(g.How) == 0 && len(g.Examples) == 0)
}

// CleanGuide trims a guide and checks its limits; it returns nil for an
// empty guide.
func CleanGuide(g *ToolGuide) (*ToolGuide, error) {
	if g.Empty() {
		return nil, nil
	}
	out := &ToolGuide{Summary: strings.TrimSpace(g.Summary), When: cleanLines(g.When), How: cleanLines(g.How), Updated: g.Updated}
	if len(g.Examples) > MaxGuideExamples {
		return nil, fmt.Errorf("a guide has at most %d examples", MaxGuideExamples)
	}
	size := len(out.Summary)
	for _, l := range append(out.When, out.How...) {
		size += len(l)
	}
	for i, x := range g.Examples {
		x.Title = strings.TrimSpace(x.Title)
		if x.Title == "" {
			x.Title = fmt.Sprintf("Example %d", i+1)
		}
		if x.Args == nil {
			x.Args = map[string]string{}
		}
		for k, v := range x.Args {
			if strings.TrimSpace(k) == "" {
				return nil, errors.New(x.Title + ": an argument has no name")
			}
			size += len(k) + len(v)
		}
		size += len(x.Title) + len(x.Text) + len(x.Sample) + len(x.Result)
		out.Examples = append(out.Examples, x)
	}
	if size > maxGuideText {
		return nil, fmt.Errorf("the guide is too long (%d KB; at most %d KB)", size>>10, maxGuideText>>10)
	}
	return out, nil
}

func cleanLines(in []string) []string {
	var out []string
	for _, l := range in {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// UpdateGuide saves a tool's usage guide (nil or empty removes it). Like
// UpdateMetadata it keeps the tool's approval, and admin-managed tools are
// changed in the catalog instead.
func (s *Store) UpdateGuide(name string, g *ToolGuide) (Extension, error) {
	g, err := CleanGuide(g)
	if err != nil {
		return Extension{}, err
	}
	if g != nil {
		g.Updated = time.Now()
	}
	all, err := s.List()
	if err != nil {
		return Extension{}, err
	}
	var cur *Extension
	for i := range all {
		if all[i].Name == name {
			cur = &all[i]
		}
	}
	if cur == nil {
		return Extension{}, fmt.Errorf("no tool named %s", name)
	}
	if IsManagedScope(cur.Scope) || cur.Managed != nil {
		return Extension{}, errors.New("admin-managed tools are changed in the catalog")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exts, err := s.read()
	if err != nil {
		return Extension{}, err
	}
	i := -1
	for k := range exts {
		if exts[k].Name == name {
			i = k
		}
	}
	if i < 0 {
		exts = append(exts, *cur)
		i = len(exts) - 1
	}
	exts[i].Guide = g
	return exts[i], s.write(exts)
}
