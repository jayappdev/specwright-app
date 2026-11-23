package flow

import (
	"slices"
	"testing"
)

func TestTagsAndGroup(t *testing.T) {
	got := NormTags([]string{" Nightly ", "#Jira", "jira", "release notes, Weekly Report", "", "a!!b"})
	if !slices.Equal(got, []string{"a-b", "jira", "nightly", "release-notes", "weekly-report"}) {
		t.Fatalf("%q", got)
	}
	if g := NormGroup("  billing/refunds  "); g != "billing / refunds" {
		t.Fatalf("%q", g)
	}
	root := t.TempDir()
	f := New("rel", "Release notes")
	f.Group, f.Tags = "Releases ", []string{"Weekly", "git"}
	f.Nodes = append(f.Nodes, Node{ID: "log", Type: "tool", Title: "Read the log", Tool: "builtin:git"})
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	l, _ := List(root)
	if len(l) != 1 || l[0].Group != "Releases" || !slices.Equal(l[0].Tags, []string{"git", "weekly"}) || !slices.Contains(l[0].Keywords, "git") || !slices.Contains(l[0].Keywords, "Read the log") {
		t.Fatalf("%+v", l)
	}
	back, _ := Load(root, "rel")
	if back.Group != "Releases" || len(back.Tags) != 2 {
		t.Fatalf("kept in the file: %+v %+v", back.Group, back.Tags)
	}
}
