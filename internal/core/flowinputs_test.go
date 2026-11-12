package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestFlowInputValues(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".codegen", "attachments"), 0o755)
	img := ".codegen/attachments/pasted-image.png"
	doc := ".codegen/attachments/spec.pdf"
	_ = os.WriteFile(filepath.Join(root, img), []byte("png"), 0o644)
	_ = os.WriteFile(filepath.Join(root, doc), []byte("pdf"), 0o644)

	cases := []struct {
		in    flow.Input
		text  string
		files []string
		want  string
		err   string
	}{
		{flow.Input{Name: "c", Type: "clipboard"}, "", []string{img}, img, ""},
		{flow.Input{Name: "c", Type: "clipboard"}, "the flow", []string{img, img}, "the flow\n\nAttached files:\n- " + img, ""},
		{flow.Input{Name: "m", Type: "multiline"}, "notes", nil, "notes", ""},
		{flow.Input{Name: "f", Type: "file"}, "", []string{doc}, doc, ""},
		{flow.Input{Name: "f", Label: "Spec", Type: "file"}, "", []string{doc, img}, "", "takes one file"},
		{flow.Input{Name: "fs", Type: "files"}, "", []string{doc, filepath.Join(root, img)}, doc + "\n" + img, ""},
		{flow.Input{Name: "fs", Type: "files"}, "", []string{"../etc/passwd"}, "", "not inside the project"},
		{flow.Input{Name: "fs", Type: "files"}, "", []string{"missing.txt"}, "", "not found"},
		{flow.Input{Name: "n", Type: "number"}, " 42 ", nil, "42", ""},
		{flow.Input{Name: "n", Type: "number"}, "lots", nil, "", "must be a number"},
		{flow.Input{Name: "b", Type: "boolean"}, "Yes", nil, "true", ""},
		{flow.Input{Name: "b", Type: "boolean"}, "", nil, "false", ""},
		{flow.Input{Name: "o", Type: "choice", Options: []string{"devs", "managers"}}, "ceo", nil, "", "must be one of"},
		{flow.Input{Name: "m", Type: "multichoice", Options: []string{"Java", "Go", "TypeScript"}}, "TypeScript\nJava\n", nil, "Java\nTypeScript", ""},
		{flow.Input{Name: "m", Type: "multichoice", Options: []string{"Java", "Go"}}, "Rust", nil, "", "not one of"},
		{flow.Input{Name: "m", Type: "multichoice", Options: []string{"Java", "Go"}, Default: "Go"}, "", nil, "Go", ""},
		{flow.Input{Name: "d", Type: "clipboard", Default: "fallback", Files: []string{img}}, "", nil, "fallback\n\nAttached files:\n- " + img, ""},
	}
	for i, c := range cases {
		got, err := flowInputValue(root, c.in, c.text, c.files)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%d: want error %q, got %v (%q)", i, c.err, err, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%d: got %q, %v; want %q", i, got, err, c.want)
		}
	}

	f := &flow.Flow{Inputs: []flow.Input{{Name: "c", Type: "clipboard"}, {Name: "t", Type: "text"}}}
	note := inputFilesNote(inputFiles(root, f, map[string]string{"c": "see\n\nAttached files:\n- " + img, "t": img}))
	if !strings.Contains(note, img+" (an image") || strings.Count(note, img) != 1 {
		t.Fatalf("note: %q", note)
	}
}
