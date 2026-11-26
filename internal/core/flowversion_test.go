package core

import (
	"encoding/json"
	"strings"
	"testing"

	"codegen/internal/flow"
)

// A version (saved by the AI with save_flow, which seldom repeats them)
// keeps its base's group, tags and place under a heading.
func TestFlowVersionKeepsGroupTagsAndHeading(t *testing.T) {
	h, ws := flowHub(t)
	base, _ := flow.Load(ws, "notes")
	base.Group, base.Tags = "Releases", []string{"weekly", "jira"}
	base.Rev = ""
	if _, err := h.SaveFlow(*base); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SaveLayout(ListLayout{Flows: ListSection{Headings: []string{"Team"}, Items: map[string]string{"notes": "Team"}}}); err != nil {
		t.Fatal(err)
	}

	// the AI's flow: no group, one tag of its own (and one the base has, in another case)
	f := *base
	f.ID, f.Rev, f.Group, f.Tags = "", "", "", []string{"Weekly", "ai-fixed"}
	b, _ := json.Marshal(f)
	out, err := h.agentSaveFlow(map[string]any{"flow": string(b), "base_flow_id": "notes", "note": "a fix"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := flow.List(ws)
	var v flow.Summary
	for _, s := range list {
		if s.Parent == "notes" {
			v = s
		}
	}
	if v.ID == "" {
		t.Fatalf("no version saved: %s", out)
	}
	if v.Group != "Releases" || strings.Join(v.Tags, ",") != "ai-fixed,jira,weekly" {
		t.Fatalf("version %s: group %q tags %v", v.ID, v.Group, v.Tags)
	}
	l, _ := h.Layout()
	if l.Flows.Items[v.ID] != "Team" {
		t.Fatalf("heading %q", l.Flows.Items[v.ID])
	}

	// the AI fixing its version (update_version_id) without them keeps them too
	f.Tags = nil
	b, _ = json.Marshal(f)
	if _, err := h.agentSaveFlow(map[string]any{"flow": string(b), "update_version_id": v.ID}); err != nil {
		t.Fatal(err)
	}
	again, _ := flow.Load(ws, v.ID)
	if again.Group != "Releases" || strings.Join(again.Tags, ",") != "ai-fixed,jira,weekly" {
		t.Fatalf("after the update: group %q tags %v", again.Group, again.Tags)
	}
}
