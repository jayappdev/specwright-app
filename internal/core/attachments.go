package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Attachments are files the user adds to a "Test with AI" question (pasted
// screenshots, PDFs, clipboard text, files picked from disk). They are
// saved inside the workspace, under .codegen/attachments/, so the agent can
// open them with its file tools and pass their paths to the tool under test.
// The folder carries its own .gitignore so they never get committed.

// AttachmentsDir is where attachments live, relative to the workspace.
const AttachmentsDir = ".codegen/attachments"

// MaxAttachment is the largest file accepted (after decoding).
const MaxAttachment = 25 << 20

// Attachment describes one saved file.
type Attachment struct {
	Path string `json:"path"` // workspace-relative, forward slashes
	Name string `json:"name"` // the original name
	Size int64  `json:"size"`
	Mime string `json:"mime"`
	Kind string `json:"kind"` // image | pdf | text | file
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SaveAttachment stores a file (base64 content) in the workspace and
// returns where it went.
func (h *Hub) SaveAttachment(name, dataB64 string) (Attachment, error) {
	root, err := h.need()
	if err != nil {
		return Attachment{}, err
	}
	if i := strings.IndexByte(dataB64, ','); strings.HasPrefix(dataB64, "data:") && i > 0 {
		dataB64 = dataB64[i+1:] // accept a data: URL as well
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return Attachment{}, fmt.Errorf("the file could not be read: %v", err)
	}
	if len(data) == 0 {
		return Attachment{}, errors.New("the file is empty")
	}
	if len(data) > MaxAttachment {
		return Attachment{}, fmt.Errorf("the file is %d MB; attachments can be at most %d MB", len(data)>>20, MaxAttachment>>20)
	}
	base := strings.Trim(unsafeName.ReplaceAllString(filepath.Base(name), "-"), "-.")
	if base == "" {
		base = "attachment"
	}
	ext := strings.ToLower(filepath.Ext(base))
	mt := http.DetectContentType(data)
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(strings.Split(mt, ";")[0]); len(exts) > 0 {
			ext = exts[0]
			base += ext
		}
	} else if m := mime.TypeByExtension(ext); m != "" && (strings.HasPrefix(mt, "application/octet-stream") || strings.HasPrefix(mt, "text/plain")) {
		mt = m
	}
	dir := filepath.Join(root, filepath.FromSlash(AttachmentsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Attachment{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# files attached to SpecWright test questions\n*\n"), 0o644)
	}
	file := time.Now().Format("20060102-150405") + "-" + base
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, file)); errors.Is(err, os.ErrNotExist) {
			break
		}
		file = fmt.Sprintf("%s-%d-%s", time.Now().Format("20060102-150405"), i, base)
	}
	if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
		return Attachment{}, err
	}
	return Attachment{Path: AttachmentsDir + "/" + file, Name: name, Size: int64(len(data)), Mime: strings.Split(mt, ";")[0], Kind: attachmentKind(mt, ext)}, nil
}

func attachmentKind(mt, ext string) string {
	switch {
	case strings.HasPrefix(mt, "image/"):
		return "image"
	case mt == "application/pdf" || ext == ".pdf":
		return "pdf"
	case strings.HasPrefix(mt, "text/") || strings.Contains(mt, "json") || strings.Contains(mt, "xml"):
		return "text"
	}
	return "file"
}

// checkAttachments keeps the paths that exist inside the workspace's
// attachments folder (the UI sends them back; nothing else is accepted).
func (h *Hub) checkAttachments(paths []string) []Attachment {
	root := h.Root()
	var out []Attachment
	for _, p := range paths {
		p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
		if !strings.HasPrefix(p, AttachmentsDir+"/") || strings.Contains(p, "..") {
			continue
		}
		fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || fi.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		mt := mime.TypeByExtension(ext)
		out = append(out, Attachment{Path: p, Name: filepath.Base(p), Size: fi.Size(), Mime: mt, Kind: attachmentKind(mt, ext)})
	}
	return out
}

// attachmentPrompt tells the agent about the attached files.
func attachmentPrompt(atts []Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThe developer attached these files (paths are relative to the workspace root):\n")
	for _, a := range atts {
		what := map[string]string{"image": "image", "pdf": "PDF document", "text": "text"}[a.Kind]
		if what == "" {
			what = "file"
		}
		fmt.Fprintf(&b, "- %s (%s, %s)\n", a.Path, what, humanSize(a.Size))
	}
	b.WriteString("Look at them when they help answer the question: open images and PDFs with your own file-reading tool (it can show images and PDF pages), text with read_file. If the tool takes a file or path argument, pass the attachment's path.")
	return b.String()
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
