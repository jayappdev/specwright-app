package flow

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildContext(t *testing.T) {
	long := func(word string, n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(fmt.Sprintf("Paragraph %d about %s %s.\n\n", i, word, strings.Repeat("x", 200)))
		}
		return b.String()
	}
	pieces := []CtxPiece{
		{ID: "S1", Label: "Billing notes", Kind: "text", Priority: "high", Text: long("refunds", 30)},
		{ID: "S2", Label: "Other notes", Kind: "text", Priority: "low", Text: long("lunch", 30)},
		{ID: "S3", Label: "Copy", Kind: "text", Text: long("refunds", 30)}, // the same as S1
	}
	pack, rep := BuildContext("Release risk", "Which refunds changed?", 2000, pieces)
	if rep.Used > 2000 || rep.Used < 1500 {
		t.Fatalf("within the budget, and using it: %d", rep.Used)
	}
	if !strings.Contains(pack, "# Context: Release risk") || !strings.Contains(pack, "Purpose: Which refunds changed?") || !strings.Contains(pack, "## [S1] Billing notes") {
		t.Fatal(pack[:300])
	}
	s1, s2, s3 := rep.Sources[0], rep.Sources[1], rep.Sources[2]
	if s1.Kept <= s2.Kept*2 {
		t.Fatalf("high priority gets more: %d vs %d", s1.Kept, s2.Kept)
	}
	if s3.Duplicates != s3.Chunks || s3.Kept != 0 {
		t.Fatalf("a repeated part is dropped: %+v", s3)
	}
	if !strings.Contains(s1.Note, "most relevant") || rep.Focus[0] != "refunds" {
		t.Fatalf("%+v %v", s1, rep.Focus)
	}
	// relevance: with a small budget the part that matches the purpose wins
	pk, _ := BuildContext("x", "kafka consumer lag", 300, []CtxPiece{{ID: "S1", Label: "Ops", Kind: "text",
		Text: "Intro to the service.\n\n" + strings.Repeat("Unrelated text about lunch menus. ", 60) + "\n\nThe kafka consumer lag grew to 2 hours.\n\n" + strings.Repeat("More about the office. ", 80)}})
	if !strings.Contains(pk, "kafka consumer lag grew") || strings.Contains(pk, "lunch menus") {
		t.Fatal(pk)
	}
	// files keep their name on every part
	parts := FileParts("docs/a.md", long("billing", 12))
	if len(parts) < 2 || !strings.HasPrefix(parts[1], "### docs/a.md\n") {
		t.Fatalf("%d %q", len(parts), parts[1][:40])
	}
}

func TestContextChecks(t *testing.T) {
	f := New("c", "C")
	f.Nodes = append(f.Nodes,
		Node{ID: "empty", Type: "context"},
		Node{ID: "bad", Type: "context", Budget: 10, Condense: true, Sources: []CtxSource{{Kind: "files"}, {Kind: "nope"}, {Kind: "files", Paths: []string{"../secrets"}}, {Kind: "history"}, {Kind: "text", Text: "x", Priority: "urgent"}}})
	f.Edges = []Edge{{From: "start", To: "bad"}}
	var all []string
	for _, is := range Validate(f, nil) {
		all = append(all, is.Message)
	}
	msgs := strings.Join(all, "\n")
	for _, want := range []string{"empty: add a source", "say which files", `"nope" is not a kind`, "leaves the project", "what to look for in past runs", "priority is high, normal or low", "the budget is 500 to 100000", "say what the context is for"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("missing %q in\n%s", want, msgs)
		}
	}
}
