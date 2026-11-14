package core

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"codegen/internal/flow"
)

// A flow's inputs reach its steps as text ({{inputs.name}}), whatever the
// type: a number, true/false, the chosen option, or — for files — the
// files' workspace paths, one per line. Clipboard and long-text inputs can
// hold pasted or dropped images, PDFs and documents as well as text: the
// text comes first, then the attached files' paths. AI steps are told to
// open every file given as an input, so a pasted screenshot or diagram is
// looked at, not just named.

// flowInputValue checks and builds one input's value from what the runner
// sent: its text and the files attached to it.
func flowInputValue(root string, in flow.Input, text string, files []string) (string, error) {
	label := firstNonEmpty(in.Label, in.Name)
	var paths []string
	for _, f := range files {
		p, err := inputFilePath(root, f)
		if err != nil {
			return "", fmt.Errorf("%s: %v", label, err)
		}
		if p != "" && !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	if in.Type == "secret" { // as typed: no default, no trimming, no files
		if strings.ContainsAny(text, "\r\n") {
			return "", fmt.Errorf("%s must be on one line", label)
		}
		return text, nil
	}
	if strings.TrimSpace(text) == "" && len(paths) == 0 {
		text, paths = in.Default, in.Files
	}
	t := strings.TrimSpace(text)
	switch in.Type {
	case "number":
		if t == "" {
			return "", nil
		}
		if _, err := strconv.ParseFloat(t, 64); err != nil {
			return "", fmt.Errorf("%s must be a number, not %q", label, clipLine(t, 40))
		}
		return t, nil
	case "boolean":
		switch strings.ToLower(t) {
		case "true", "yes", "y", "1", "on":
			return "true", nil
		case "", "false", "no", "n", "0", "off":
			return "false", nil
		}
		return "", fmt.Errorf("%s must be yes or no, not %q", label, clipLine(t, 40))
	case "multichoice": // the picks, one per line, in the options' order
		picked := map[string]bool{}
		for _, l := range strings.Split(text, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				if len(in.Options) > 0 && !slices.Contains(in.Options, l) {
					return "", fmt.Errorf("%s: %q is not one of: %s", label, clipLine(l, 40), strings.Join(in.Options, ", "))
				}
				picked[l] = true
			}
		}
		var out []string
		for _, o := range in.Options {
			if picked[o] {
				out = append(out, o)
				delete(picked, o)
			}
		}
		for l := range picked { // no options listed: keep what was given
			out = append(out, l)
		}
		return strings.Join(out, "\n"), nil
	case "choice":
		if t != "" && len(in.Options) > 0 && !slices.Contains(in.Options, t) {
			return "", fmt.Errorf("%s must be one of: %s", label, strings.Join(in.Options, ", "))
		}
		return t, nil
	case "file", "files":
		if len(paths) == 0 { // paths typed in, one per line
			for _, l := range strings.Split(t, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					paths = append(paths, l)
				}
			}
		}
		if in.Type == "file" && len(paths) > 1 {
			return "", fmt.Errorf("%s takes one file; %d were given", label, len(paths))
		}
		return strings.Join(paths, "\n"), nil
	}
	// text, multiline, clipboard: the text, then any attached files
	if len(paths) == 0 {
		return text, nil
	}
	if t == "" {
		return strings.Join(paths, "\n"), nil
	}
	return strings.TrimRight(text, "\n") + "\n\nAttached files:\n- " + strings.Join(paths, "\n- "), nil
}

// inputFilePath checks a file given to an input: a workspace path to an
// existing file.
func inputFilePath(root, p string) (string, error) {
	p = strings.TrimSpace(filepath.ToSlash(p))
	if p == "" {
		return "", nil
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("%s is not inside the project", p)
		}
		p = filepath.ToSlash(rel)
	}
	p = path.Clean(p)
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("%s is not inside the project", p)
	}
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "", fmt.Errorf("the file %s was not found", p)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s is a folder, not a file", p)
	}
	return p, nil
}

// inputFiles lists the files the flow's inputs hold, for AI steps.
func inputFiles(root string, f *flow.Flow, inputs map[string]string) []string {
	var out []string
	for _, in := range f.Inputs {
		switch in.Type {
		case "file", "files", "clipboard", "multiline":
		default:
			continue
		}
		for _, l := range strings.Split(inputs[in.Name], "\n") {
			l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
			if l == "" || len(l) > 400 || strings.ContainsAny(l, "\t") || slices.Contains(out, l) {
				continue
			}
			if p, err := inputFilePath(root, l); err == nil && p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

var imageExt = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg"}

// inputFilesNote tells an AI step to open the input files.
func inputFilesNote(files []string) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Files given as inputs\nOpen and read these files (paths are relative to the project root); they are part of the input:\n")
	for _, p := range files {
		kind := ""
		if slices.Contains(imageExt, strings.ToLower(path.Ext(p))) {
			kind = " (an image: look at it, e.g. a screenshot or diagram)"
		} else if strings.EqualFold(path.Ext(p), ".pdf") {
			kind = " (a PDF)"
		}
		fmt.Fprintf(&b, "- %s%s\n", p, kind)
	}
	return b.String()
}
