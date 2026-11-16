package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A tool that prints more than the old 64 KB limit: the whole, valid result
// comes back for flows; an agent gets a saved file and a summary instead of
// a broken prefix.
func TestLargeToolOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses bash")
	}
	root := t.TempDir()
	s := NewStore(root)
	// about 300 KB of JSON
	script := "#!/usr/bin/env bash\necho '{\"items\": ['\nfor i in $(seq 1 6000); do echo \"{\\\"id\\\": $i, \\\"name\\\": \\\"item number $i\\\"},\"; done\necho '{\"id\": 0}]}'\n"
	e, err := s.Save(Extension{Name: "big-list", Desc: "Lists a lot", Lang: "Bash", Command: "bash .codegen/tools/big-list.sh", Script: script})
	if err != nil {
		t.Fatal(err)
	}
	r := Exec(context.Background(), root, e, nil, nil, nil, 0)
	if !r.OK || r.Truncated || len(r.Output) < 200<<10 || !json.Valid([]byte(r.Output)) {
		t.Fatalf("ok=%v truncated=%v %d bytes valid=%v %s", r.OK, r.Truncated, len(r.Output), json.Valid([]byte(r.Output)), r.Error)
	}

	msg := forAgent(root, e.Name, r.Output, r.Truncated)
	if len(msg) > 12<<10 || !strings.Contains(msg, "JSON") || !strings.Contains(msg, "items: array of 6001") {
		t.Fatalf("agent message (%d bytes):\n%s", len(msg), msg)
	}
	i := strings.Index(msg, ".codegen/tool-output/")
	rel := strings.Fields(msg[i:])[0]
	full, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || string(full) != r.Output {
		t.Fatalf("saved %s: %v (%d bytes)", rel, err, len(full))
	}
	if forAgent(root, "small", "ok", false) != "ok" {
		t.Fatal("small results pass through")
	}
}

func TestLimitedMarksOverflow(t *testing.T) {
	var buf bytes.Buffer
	l := &limited{w: &buf, n: 10}
	l.Write([]byte("12345"))
	if l.over {
		t.Fatal("not over yet")
	}
	l.Write([]byte("678901234"))
	if !l.over || buf.String() != "1234567890" {
		t.Fatalf("over=%v kept %q", l.over, buf.String())
	}
}
