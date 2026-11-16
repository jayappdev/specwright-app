package bigout

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeCut(t *testing.T) {
	s := strings.Repeat("line of text\n", 100)
	if got := SafeCut(s, 60); !strings.HasSuffix(got, "line of text") || len(got) > 60 { // the line break at 51 is in the last fifth
		t.Fatalf("line boundary: %q", got)
	}
	u := strings.Repeat("é", 100) // 2 bytes each
	if got := SafeCut(u, 51); !utf8.ValidString(got) || len(got) > 51 {
		t.Fatalf("utf-8: %q", got)
	}
	if SafeCut("short", 50) != "short" {
		t.Fatal("short strings stay whole")
	}
}

func TestKind(t *testing.T) {
	for want, s := range map[string]string{
		"json":     `{"a":[1,2]}`,
		"html":     "<!DOCTYPE html><html><body>x</body></html>",
		"xml":      `<?xml version="1.0"?><a/>`,
		"csv":      "a,b,c\n1,2,3\n4,5,6\n7,8,9",
		"markdown": "# Title\n\n- one\n- two\n\n## More",
		"text":     "just a sentence",
	} {
		if got := Kind(s); got != want {
			t.Errorf("Kind(%q) = %s, want %s", s[:min(len(s), 20)], got, want)
		}
	}
	if Kind(`{"a": [1, 2`) == "json" {
		t.Error("broken JSON is not json")
	}
}

func TestForAgentSmallAndLarge(t *testing.T) {
	root := t.TempDir()
	if out, big := ForAgent(root, "lint", "small"); big || out != "small" {
		t.Fatal("small results pass through")
	}
	var items []map[string]any
	for i := 0; i < 3000; i++ {
		items = append(items, map[string]any{"id": i, "name": fmt.Sprintf("item %d", i), "tags": []string{"a", "b"}})
	}
	b, _ := json.MarshalIndent(map[string]any{"total": 3000, "items": items}, "", "  ")
	out, big := ForAgent(root, "list issues", string(b))
	if !big || len(out) > 12<<10 {
		t.Fatalf("big=%v, %d bytes", big, len(out))
	}
	for _, want := range []string{"JSON", ".codegen/tool-output/list-issues-", "read_file", "jq", "an object with 2 key(s)", "items: array of 3000", "total: number"} {
		if !strings.Contains(out, want) {
			t.Errorf("message lacks %q:\n%s", want, out)
		}
	}
	// the full result is saved, exactly
	ents, _ := os.ReadDir(filepath.Join(root, Dir))
	var saved []byte
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			saved, _ = os.ReadFile(filepath.Join(root, Dir, e.Name()))
		}
	}
	if string(saved) != string(b) {
		t.Fatalf("saved %d bytes, want %d", len(saved), len(b))
	}
	if _, err := os.Stat(filepath.Join(root, Dir, ".gitignore")); err != nil {
		t.Fatal("no .gitignore")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < keep+5; i++ {
		if _, err := Save(root, fmt.Sprintf("t%d", i), "text", "x"); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(filepath.Join(root, Dir))
	n := 0
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	if n != keep {
		t.Fatalf("kept %d, want %d", n, keep)
	}
}
