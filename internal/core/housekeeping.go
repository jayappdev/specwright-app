package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"codegen/internal/store"
)

// Housekeeping: runs can be archived (hidden from the Playground and
// History lists, restorable) and grouped; the Playground's and History's
// runs can be archived automatically some days after they ended. Tools can
// be tagged. These are the user's settings, kept in the app's data folder.

// HousekeepingSettings says after how many days finished runs are archived
// automatically (0: never). Playground: prompts from the Prompt playground;
// History: workflow steps and flow runs.
type HousekeepingSettings struct {
	PlaygroundDays int       `json:"playgroundDays"`
	HistoryDays    int       `json:"historyDays"`
	LastRun        time.Time `json:"lastRun,omitzero"`
	LastArchived   int       `json:"lastArchived,omitempty"`
}

func (h *Hub) housekeepingPath() string { return filepath.Join(h.Store.Dir(), "housekeeping.json") }

// Housekeeping returns the auto-archive settings.
func (h *Hub) Housekeeping() HousekeepingSettings {
	var s HousekeepingSettings
	if b, err := os.ReadFile(h.housekeepingPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// SaveHousekeeping stores the auto-archive settings and applies them now.
func (h *Hub) SaveHousekeeping(s HousekeepingSettings) (HousekeepingSettings, error) {
	if s.PlaygroundDays < 0 || s.HistoryDays < 0 || s.PlaygroundDays > 3650 || s.HistoryDays > 3650 {
		return s, errors.New("days must be between 0 (never) and 3650")
	}
	if err := h.writeHousekeeping(s); err != nil {
		return s, err
	}
	h.AutoArchive()
	return h.Housekeeping(), nil
}

func (h *Hub) writeHousekeeping(s HousekeepingSettings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(h.housekeepingPath(), b, 0o600)
}

// runArea is where a run is listed: playground or history.
func runArea(r store.Run) string {
	if r.Kind == "playground" || r.Step == PlaygroundStep || r.Workflow == "playground" {
		return "playground"
	}
	return "history"
}

// AutoArchive archives finished runs older than the settings allow (in
// every workspace). It returns how many it archived.
func (h *Hub) AutoArchive() int {
	s := h.Housekeeping()
	if s.PlaygroundDays == 0 && s.HistoryDays == 0 {
		return 0
	}
	runs, err := h.Store.Runs("", 0)
	if err != nil {
		return 0
	}
	now, n := time.Now(), 0
	for _, r := range runs {
		if r.Archived || r.State == "running" || r.State == "paused" || r.State == "review" || r.State == "scheduled" {
			continue
		}
		days := s.HistoryDays
		if runArea(r) == "playground" {
			days = s.PlaygroundDays
		}
		if days <= 0 {
			continue
		}
		at := r.Ended
		if at.IsZero() {
			at = r.Started
		}
		if now.Sub(at) >= time.Duration(days)*24*time.Hour {
			yes := true
			if h.Store.SetRunMeta(r.ID, &yes, nil) == nil {
				n++
			}
		}
	}
	s.LastRun, s.LastArchived = now.UTC(), n
	_ = h.writeHousekeeping(s)
	return n
}

// autoArchiveLoop applies the settings at start and every six hours.
func (h *Hub) autoArchiveLoop() {
	h.AutoArchive()
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
			h.AutoArchive()
		}
	}
}

// ArchiveRuns archives (or, with archived false, restores) runs.
func (h *Hub) ArchiveRuns(ids []string, archived bool) (int, error) {
	n := 0
	for _, id := range ids {
		if a := h.activeRun(); a != nil && a.Session != nil && a.Session.ID == id && archived {
			continue // a live run stays
		}
		if err := h.Store.SetRunMeta(id, &archived, nil); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// GroupRuns puts runs in a group ("" takes them out of their group).
func (h *Hub) GroupRuns(ids []string, group string) error {
	group = strings.TrimSpace(group)
	if len(group) > 80 {
		return errors.New("a group name is at most 80 characters")
	}
	for _, id := range ids {
		if err := h.Store.SetRunMeta(id, nil, &group); err != nil {
			return err
		}
	}
	return nil
}

// RunGroups lists the groups the workspace's runs use, by name.
func (h *Hub) RunGroups() []string {
	runs, _ := h.Runs(0)
	var out []string
	for _, r := range runs {
		if r.Group != "" && !slices.Contains(out, r.Group) {
			out = append(out, r.Group)
		}
	}
	sort.Strings(out)
	return out
}

// --- tool tags ------------------------------------------------------------------------

func (h *Hub) toolTagsPath() string { return filepath.Join(h.Store.Dir(), "tool-tags.json") }

// ToolTags returns every tool's tags (tool name → tags), for tools of any
// kind: built-in, managed, global, project and Copilot extension tools.
func (h *Hub) ToolTags() map[string][]string {
	out := map[string][]string{}
	if b, err := os.ReadFile(h.toolTagsPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// SetToolTags replaces one tool's tags and returns them all.
func (h *Hub) SetToolTags(tool string, tags []string) (map[string][]string, error) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil, errors.New("which tool?")
	}
	all := h.ToolTags()
	var clean []string
	for _, t := range tags {
		t = strings.ToLower(strings.Join(strings.Fields(t), "-"))
		if t != "" && len(t) <= 40 && !slices.Contains(clean, t) {
			clean = append(clean, t)
		}
	}
	if len(clean) > 20 {
		return nil, errors.New("at most 20 tags per tool")
	}
	if len(clean) == 0 {
		delete(all, tool)
	} else {
		all[tool] = clean
	}
	b, _ := json.MarshalIndent(all, "", "  ")
	if err := os.WriteFile(h.toolTagsPath(), b, 0o600); err != nil {
		return nil, err
	}
	return all, nil
}
