package core

import (
	"testing"
	"time"

	"codegen/internal/store"
)

func TestArchiveGroupAndAutoArchive(t *testing.T) {
	h, ws := newHub(t)
	old, fresh := time.Now().Add(-10*24*time.Hour), time.Now().Add(-time.Hour)
	for _, r := range []store.Run{
		{ID: "p-old", Workspace: ws, Step: PlaygroundStep, Kind: "playground", State: "done", Started: old, Ended: old},
		{ID: "p-new", Workspace: ws, Step: PlaygroundStep, Kind: "playground", State: "done", Started: fresh, Ended: fresh},
		{ID: "w-old", Workspace: ws, Step: "specify", Kind: "workflow", State: "done", Started: old, Ended: old},
		{ID: "f-live", Workspace: ws, Step: "flow", Kind: "flow", State: "running", Started: old},
	} {
		if err := h.Store.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	// group, archive by hand, restore
	if err := h.GroupRuns([]string{"p-new", "w-old"}, " release 2.3 "); err != nil {
		t.Fatal(err)
	}
	if g := h.RunGroups(); len(g) != 1 || g[0] != "release 2.3" {
		t.Fatal(g)
	}
	if n, _ := h.ArchiveRuns([]string{"p-new"}, true); n != 1 {
		t.Fatal(n)
	}
	r, _ := h.Store.GetRun("p-new")
	if !r.Archived || r.ArchivedAt.IsZero() || r.Group != "release 2.3" {
		t.Fatalf("%+v", r)
	}
	// a later save of the run (e.g. when it ends) keeps the user's flag and group
	_ = h.Store.SaveRun(store.Run{ID: "p-new", Workspace: ws, Step: PlaygroundStep, State: "done", Started: fresh})
	if r, _ = h.Store.GetRun("p-new"); !r.Archived || r.Group != "release 2.3" {
		t.Fatalf("kept: %+v", r)
	}
	_, _ = h.ArchiveRuns([]string{"p-new"}, false)
	if r, _ = h.Store.GetRun("p-new"); r.Archived {
		t.Fatal("restored")
	}
	// auto-archive: playground after 7 days, history never
	s, err := h.SaveHousekeeping(HousekeepingSettings{PlaygroundDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if s.LastArchived != 1 || s.LastRun.IsZero() {
		t.Fatalf("%+v", s)
	}
	for id, want := range map[string]bool{"p-old": true, "p-new": false, "w-old": false, "f-live": false} {
		if r, _ := h.Store.GetRun(id); r.Archived != want {
			t.Errorf("%s archived %v, want %v", id, r.Archived, want)
		}
	}
	_, _ = h.SaveHousekeeping(HousekeepingSettings{PlaygroundDays: 7, HistoryDays: 3})
	if r, _ := h.Store.GetRun("w-old"); !r.Archived {
		t.Fatal("history after 3 days")
	}
	if r, _ := h.Store.GetRun("f-live"); r.Archived {
		t.Fatal("a live run is never archived")
	}
	if _, err := h.SaveHousekeeping(HousekeepingSettings{HistoryDays: -1}); err == nil {
		t.Fatal("negative days")
	}
	// tool tags
	tags, err := h.SetToolTags("builtin:fetch_webpage", []string{"Web API", "web api", " http "})
	if err != nil || len(tags["builtin:fetch_webpage"]) != 2 || tags["builtin:fetch_webpage"][0] != "web-api" {
		t.Fatal(tags, err)
	}
	if tags, _ = h.SetToolTags("builtin:fetch_webpage", nil); len(tags) != 0 {
		t.Fatal("cleared", tags)
	}
}
