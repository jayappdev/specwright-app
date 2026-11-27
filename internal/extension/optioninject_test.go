package extension

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A value that fills a whole word and starts with "-" would be an option
// to the program: refused, except listed values and numbers (item 9).
func TestToolValuesCannotBecomeOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses echo")
	}
	root := t.TempDir()
	tool := Extension{Name: "say", Command: "echo {{q}}", Params: []Param{
		{Name: "q", Type: "string"}, {Name: "n", Type: "number"}, {Name: "mode", Enum: []string{"-n", "--plain"}},
	}}
	run := func(cmd string, args map[string]any) RunResult {
		e := tool
		e.Command = cmd
		return Exec(context.Background(), root, e, args, nil, nil, 10*time.Second)
	}
	if r := run("echo {{q}}", map[string]any{"q": "--output=/tmp/x"}); r.OK || !strings.Contains(r.Error, "would read it as an option") {
		t.Fatalf("option value ran: %+v", r)
	}
	if r := run("echo {{q}}", map[string]any{"q": "hello -world"}); !r.OK {
		t.Fatalf("an ordinary value: %+v", r)
	}
	if r := run("echo {{n}}", map[string]any{"n": "-5"}); !r.OK || !strings.Contains(r.Output, "-5") {
		t.Fatalf("a negative number: %+v", r)
	}
	if r := run("echo {{mode}} x", map[string]any{"mode": "--plain"}); !r.OK {
		t.Fatalf("a listed value: %+v", r)
	}
	if r := run("echo -- {{q}}", map[string]any{"q": "--x"}); !r.OK || !strings.Contains(r.Output, "--x") {
		t.Fatalf("after --: %+v", r)
	}
	if r := run("echo --q={{q}}", map[string]any{"q": "-x"}); !r.OK || !strings.Contains(r.Output, "--q=-x") {
		t.Fatalf("written-out option: %+v", r)
	}
	if r := run("echo {{q}}", map[string]any{"q": "-"}); !r.OK {
		t.Fatalf("a lone dash (stdin): %+v", r)
	}
}

// npm scripts get only the declared arguments, after "--".
func TestNpmToolGetsDeclaredArgumentsOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as npm")
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := Extension{Name: "lint", Kind: "npm", Command: "npm run ext:lint", Params: []Param{{Name: "dir", Type: "string"}}}
	r := Exec(context.Background(), t.TempDir(), e, map[string]any{"dir": "src", "registry": "https://evil.example"}, nil, nil, 10*time.Second)
	if !r.OK || !strings.Contains(r.Output, "--\n--dir=src") || strings.Contains(r.Output, "registry") {
		t.Fatalf("%+v", r)
	}
}
