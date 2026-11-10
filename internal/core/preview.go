package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"codegen/internal/watch"
)

// FilePreview is what the quick-preview panel shows for a file path found
// anywhere in the UI (agent answers, tool output, logs, descriptions).
type FilePreview struct {
	Path      string    `json:"path"`           // absolute path that was opened
	Rel       string    `json:"rel,omitempty"`  // workspace-relative, when inside the workspace
	Name      string    `json:"name"`           // base name
	Size      int64     `json:"size"`           // bytes
	Mod       time.Time `json:"mod"`            // last modified
	Kind      string    `json:"kind"`           // text | image | pdf | binary | dir
	Mime      string    `json:"mime,omitempty"` //
	Text      string    `json:"text,omitempty"` // text files (maybe truncated)
	Data      string    `json:"data,omitempty"` // images and PDFs, base64
	Truncated bool      `json:"truncated,omitempty"`
	Entries   []string  `json:"entries,omitempty"` // a directory's first entries
	Matches   []string  `json:"matches,omitempty"` // other files the name could mean
}

const (
	previewText   = 2 << 20  // text shown up to 2 MB
	previewBinary = 15 << 20 // images and PDFs up to 15 MB
)

// PreviewFile resolves a path as the UI shows it and returns its content
// for the quick preview. It accepts absolute paths, ~/ paths, paths relative
// to the workspace or to the app's data folder, and bare or partial names
// (for example "OrderService.java" or "orders/OrderService.java"), which are
// looked up in the workspace.
func (h *Hub) PreviewFile(p string) (FilePreview, error) {
	abs, matches, err := h.resolvePreview(p)
	if err != nil {
		return FilePreview{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return FilePreview{}, err
	}
	pv := FilePreview{Path: abs, Name: filepath.Base(abs), Size: fi.Size(), Mod: fi.ModTime(), Matches: matches}
	if root := h.Root(); root != "" {
		if r, err := filepath.Rel(root, abs); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			pv.Rel = filepath.ToSlash(r)
		}
	}
	if fi.IsDir() {
		pv.Kind = "dir"
		ents, _ := os.ReadDir(abs)
		for i, e := range ents {
			if i == 200 {
				pv.Entries = append(pv.Entries, fmt.Sprintf("… %d more", len(ents)-200))
				break
			}
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			pv.Entries = append(pv.Entries, n)
		}
		return pv, nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return FilePreview{}, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]
	ext := strings.ToLower(filepath.Ext(abs))
	mt := http.DetectContentType(head)
	if m := mime.TypeByExtension(ext); m != "" && (strings.HasPrefix(mt, "text/plain") || strings.HasPrefix(mt, "application/octet-stream")) {
		mt = m
	}
	if ext == ".svg" {
		mt = "image/svg+xml"
	}
	pv.Mime = strings.Split(mt, ";")[0]
	switch {
	case strings.HasPrefix(pv.Mime, "image/") || pv.Mime == "application/pdf":
		pv.Kind = map[bool]string{true: "pdf", false: "image"}[pv.Mime == "application/pdf"]
		if fi.Size() > previewBinary {
			pv.Truncated = true
			return pv, nil
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return FilePreview{}, err
		}
		pv.Data = base64.StdEncoding.EncodeToString(b)
	case looksText(head):
		pv.Kind = "text"
		b, err := io.ReadAll(io.NewSectionReader(f, 0, previewText))
		if err != nil {
			return FilePreview{}, err
		}
		pv.Truncated = fi.Size() > previewText
		for len(b) > 0 && !utf8.Valid(b) && pv.Truncated { // don't cut a rune in half
			b = b[:len(b)-1]
		}
		pv.Text = string(b)
	default:
		pv.Kind = "binary"
	}
	return pv, nil
}

func looksText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return utf8.Valid(b) || !strings.Contains(string(b), "\x00")
}

// resolvePreview turns a UI path into an absolute one. matches lists other
// candidates when a bare name fits several workspace files.
func (h *Hub) resolvePreview(p string) (string, []string, error) {
	p = strings.TrimSpace(strings.Trim(p, "`'\""))
	if p == "" {
		return "", nil, errors.New("no path")
	}
	if strings.HasPrefix(p, "file://") {
		p = strings.TrimPrefix(p, "file://")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	exists := func(x string) bool { _, err := os.Stat(x); return err == nil }
	if filepath.IsAbs(p) {
		if exists(p) {
			return filepath.Clean(p), nil, nil
		}
		return "", nil, fmt.Errorf("%s does not exist", p)
	}
	rel := filepath.FromSlash(strings.TrimPrefix(p, "./"))
	root := h.Root()
	var bases []string
	if root != "" {
		bases = append(bases, root)
	}
	bases = append(bases, h.Store.Dir())
	for _, b := range bases {
		if c := filepath.Join(b, rel); exists(c) {
			return c, nil, nil
		}
	}
	// a bare or partial name: look for workspace files ending with it
	if root != "" {
		want := "/" + filepath.ToSlash(rel)
		var found []string
		for _, f := range watch.ListFiles(root, 50000) {
			if strings.HasSuffix("/"+f, want) {
				found = append(found, f)
			}
		}
		if len(found) > 0 {
			sort.Slice(found, func(i, j int) bool {
				if len(found[i]) != len(found[j]) {
					return len(found[i]) < len(found[j])
				}
				return found[i] < found[j]
			})
			var others []string
			if len(found) > 1 {
				others = found[1:min(len(found), 20)]
			}
			return filepath.Join(root, filepath.FromSlash(found[0])), others, nil
		}
	}
	if root == "" {
		return "", nil, fmt.Errorf("%s: open a project to look up relative paths", p)
	}
	return "", nil, fmt.Errorf("%s was not found in the project", p)
}
