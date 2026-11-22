package extension

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// A tool gets its arguments by name too ($SPECWRIGHT_ARGS), so an empty
// optional argument does not shift the positional ones.
func TestArgsByName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	e := Extension{Name: "show", Kind: "script", Command: `sh -c 'printf "%s|%s" "$SPECWRIGHT_ARGS" "$1"' x {{b}}`,
		Params: []Param{{Name: "a", Type: "string"}, {Name: "b", Type: "string"}}}
	res := Exec(context.Background(), t.TempDir(), e, map[string]any{"a": "", "b": "two", "c": map[string]any{"k": 1}}, nil, nil, 0)
	if !res.OK || !strings.Contains(res.Output, `"b":"two"`) || !strings.Contains(res.Output, `"c":{"k":1}`) || !strings.HasSuffix(strings.TrimSpace(res.Output), "|two") {
		t.Fatalf("%+v", res)
	}
}
