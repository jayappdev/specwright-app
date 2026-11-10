package core

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachments(t *testing.T) {
	h, ws := newHub(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	a, err := h.SaveAttachment("Screen Shot (1).png", base64.StdEncoding.EncodeToString(png))
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "image" || !strings.HasPrefix(a.Path, AttachmentsDir+"/") || strings.ContainsAny(a.Path, " ()") {
		t.Fatalf("%+v", a)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(a.Path))); string(b) != string(png) {
		t.Fatal("content not saved")
	}
	if _, err := os.Stat(filepath.Join(ws, AttachmentsDir, ".gitignore")); err != nil {
		t.Fatal("attachments folder must be git-ignored")
	}
	pdf, err := h.SaveAttachment("spec.pdf", "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n%…")))
	if err != nil || pdf.Kind != "pdf" {
		t.Fatalf("%+v %v", pdf, err)
	}
	txt, err := h.SaveAttachment("pasted", base64.StdEncoding.EncodeToString([]byte("hello\nworld\n")))
	if err != nil || txt.Kind != "text" {
		t.Fatalf("%+v %v", txt, err)
	}
	if _, err := h.SaveAttachment("x", ""); err == nil {
		t.Fatal("empty file accepted")
	}
	// only files inside the attachments folder are passed on
	_ = os.WriteFile(filepath.Join(ws, "secret.txt"), []byte("x"), 0o644)
	got := h.checkAttachments([]string{a.Path, pdf.Path, "secret.txt", AttachmentsDir + "/../../secret.txt", AttachmentsDir + "/missing.png"})
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	p := attachmentPrompt(got)
	if !strings.Contains(p, a.Path) || !strings.Contains(p, "PDF document") {
		t.Fatal(p)
	}
}
