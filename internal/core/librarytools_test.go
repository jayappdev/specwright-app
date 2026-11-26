package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"codegen/internal/extension"
)

// The library's tool files (library/tools/*.tools.json) are what admins
// publish: each must parse, match its readable source, and keep its
// parameters in the order its script reads them.
func TestLibraryToolFiles(t *testing.T) {
	root := filepath.Join("..", "..", "library", "tools")
	files, _ := filepath.Glob(filepath.Join(root, "*.tools.json"))
	if len(files) == 0 {
		t.Fatal("no library tool files")
	}
	h, _ := newHub(t)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tools, err := h.ReadToolFile(string(data))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, e := range tools {
			src, err := os.ReadFile(filepath.Join(root, e.Name, extension.EntryFile(e.Lang)))
			if err != nil || string(src) != e.Script {
				t.Fatalf("%s: the script in the tool file differs from %s/%s (regenerate the file)", e.Name, e.Name, extension.EntryFile(e.Lang))
			}
			if !extension.NameRE.MatchString(e.Name) {
				t.Fatalf("name %q", e.Name)
			}
			for _, d := range e.Deps {
				if regexp.MustCompile(`[\^~*]|latest`).MatchString(d) || !strings.Contains(d[1:], "@") {
					t.Fatalf("%s: dependency %q must be pinned to an exact version", e.Name, d)
				}
			}
			for _, s := range e.Secrets {
				if !s.Optional {
					t.Fatalf("%s: secret %s should be optional (a query may not need it)", e.Name, s.Name)
				}
			}
			// its guide: examples call only its parameters and give the required ones
			if e.Guide == nil || len(e.Guide.Examples) == 0 || e.Guide.Summary == "" {
				t.Fatalf("%s: give the tool a guide with examples", e.Name)
			}
			params := map[string]bool{}
			for _, p := range e.Params {
				params[p.Name] = true
			}
			for _, x := range e.Guide.Examples {
				for k := range x.Args {
					if !params[k] {
						t.Fatalf("%s: example %q uses %s, not a parameter", e.Name, x.Title, k)
					}
				}
				for _, p := range e.Params {
					if p.Required && strings.TrimSpace(x.Args[p.Name]) == "" {
						t.Fatalf("%s: example %q leaves out %s", e.Name, x.Title, p.Name)
					}
				}
			}
			// the command passes the parameters in their order, as the script reads them
			var want []string
			for _, p := range e.Params {
				want = append(want, "{{"+p.Name+"}}")
			}
			if !strings.HasSuffix(e.Command, strings.Join(want, " ")) {
				t.Fatalf("%s: the command does not pass the parameters in order: %s", e.Name, e.Command)
			}
		}
	}
	// duckdb_data_query reads its arguments in this order
	src, _ := os.ReadFile(filepath.Join(root, "duckdb_data_query", "index.js"))
	if !strings.Contains(string(src), `const ARGS = ["sql", "sources", "connections", "extensions", "input", "format", "save_to", "max_rows", "save_mode"];`) {
		t.Fatal("duckdb_data_query's arguments changed: update the tool file's parameters to match")
	}
	if _, err := h.ReadToolFile(`{"format":"specwright-tools","version":1,"tools":[]}`); err == nil {
		t.Fatal("a file without tools should be refused")
	}
}
