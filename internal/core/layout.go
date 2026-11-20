package core

// Headings for the Tools and Flows lists: purely how the lists are
// arranged. Each list has its own headings, in order, and the tools or
// flows placed under them. Nothing about a tool or a flow changes: the
// arrangement is kept per project in the app's own folder (lists/<project
// key>.json), like tool tags. Renaming a tool or a flow keeps its place.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ListLayout is the arrangement of the Tools and Flows lists.
type ListLayout struct {
	Tools ListSection `json:"tools"`
	Flows ListSection `json:"flows"`
}

// ListSection is one list's headings (in order) and where items are
// (tool name or flow id → heading).
type ListSection struct {
	Headings []string          `json:"headings"`
	Items    map[string]string `json:"items"`
}

const (
	maxHeadings   = 60
	maxHeadingLen = 60
)

func (h *Hub) layoutPath(root string) string {
	return filepath.Join(h.Store.Dir(), "lists", workspaceKey(root)+".json")
}

// Layout returns the open project's list headings.
func (h *Hub) Layout() (ListLayout, error) {
	root, err := h.need()
	if err != nil {
		return ListLayout{}, err
	}
	var l ListLayout
	if b, err := os.ReadFile(h.layoutPath(root)); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return tidyLayout(l), nil
}

// SaveLayout stores the open project's list headings (tidied: headings
// trimmed and unique, items only under headings that exist).
func (h *Hub) SaveLayout(l ListLayout) (ListLayout, error) {
	root, err := h.need()
	if err != nil {
		return ListLayout{}, err
	}
	l = tidyLayout(l)
	if len(l.Tools.Headings) > maxHeadings || len(l.Flows.Headings) > maxHeadings {
		return l, errors.New("at most 60 headings in a list")
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return l, err
	}
	p := h.layoutPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return l, err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return l, err
	}
	return l, os.Rename(tmp, p)
}

func tidyLayout(l ListLayout) ListLayout {
	l.Tools, l.Flows = tidySection(l.Tools), tidySection(l.Flows)
	return l
}

func tidySection(s ListSection) ListSection {
	out := ListSection{Headings: []string{}, Items: map[string]string{}}
	have := map[string]bool{}
	for _, h := range s.Headings {
		h = strings.Join(strings.Fields(h), " ")
		if len(h) > maxHeadingLen {
			h = strings.TrimSpace(h[:maxHeadingLen])
		}
		if h == "" || have[strings.ToLower(h)] {
			continue
		}
		have[strings.ToLower(h)] = true
		out.Headings = append(out.Headings, h)
	}
	for id, h := range s.Items {
		for _, x := range out.Headings {
			if strings.EqualFold(x, strings.Join(strings.Fields(h), " ")) && id != "" {
				out.Items[id] = x
			}
		}
	}
	return out
}

// moveLayoutItem keeps a renamed tool or flow under its heading.
func (h *Hub) moveLayoutItem(list, from, to string) {
	l, err := h.Layout()
	if err != nil {
		return
	}
	s := &l.Tools
	if list == "flows" {
		s = &l.Flows
	}
	if hd, ok := s.Items[from]; ok {
		delete(s.Items, from)
		s.Items[to] = hd
		_, _ = h.SaveLayout(l)
	}
}
