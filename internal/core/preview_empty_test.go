package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An empty file and an empty folder: the fields are there (an empty text,
// an empty list), not left out — the preview read a missing text as text
// and the whole app went blank.
func TestPreviewEmptyFileAndFolder(t *testing.T) {
	h, ws := newHub(t)
	_ = os.WriteFile(filepath.Join(ws, "empty.md"), nil, 0o644)
	_ = os.MkdirAll(filepath.Join(ws, "nothing"), 0o755)
	for name, want := range map[string]string{"empty.md": `"text":""`, "nothing": `"entries":[]`} {
		pv, err := h.PreviewFile(name)
		if err != nil {
			t.Fatal(name, err)
		}
		b, _ := json.Marshal(pv)
		if !strings.Contains(string(b), want) {
			t.Fatalf("%s: %s has no %s", name, b, want)
		}
	}
	if _, err := h.PreviewFile("out/missing.md"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal("a broken link is an error the preview shows:", err)
	}
}
