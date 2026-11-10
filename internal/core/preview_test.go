package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewFile(t *testing.T) {
	h, ws := newHub(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(ws, "src", "main", "java", "orders"), 0o755))
	must(os.WriteFile(filepath.Join(ws, "src", "main", "java", "orders", "Order Service.java"), []byte("class OrderService {}\n"), 0o644))
	must(os.WriteFile(filepath.Join(ws, "logo.png"), []byte("\x89PNG\r\n\x1a\n\x00\x00"), 0o644))
	must(os.WriteFile(filepath.Join(ws, "blob.bin"), []byte{0, 1, 2, 3}, 0o644))

	for _, p := range []string{"src/main/java/orders/Order Service.java", "./src/main/java/orders/Order Service.java", "Order Service.java", "orders/Order Service.java", "`Order Service.java`", filepath.Join(ws, "src/main/java/orders/Order Service.java")} {
		pv, err := h.PreviewFile(p)
		if err != nil || pv.Kind != "text" || !strings.Contains(pv.Text, "OrderService") || pv.Rel != "src/main/java/orders/Order Service.java" {
			t.Fatalf("%q: %+v %v", p, pv, err)
		}
	}
	if pv, err := h.PreviewFile("logo.png"); err != nil || pv.Kind != "image" || pv.Data == "" {
		t.Fatalf("%+v %v", pv, err)
	}
	if pv, err := h.PreviewFile("blob.bin"); err != nil || pv.Kind != "binary" || pv.Text != "" {
		t.Fatalf("%+v %v", pv, err)
	}
	if pv, err := h.PreviewFile("src/main"); err != nil || pv.Kind != "dir" || len(pv.Entries) != 1 {
		t.Fatalf("%+v %v", pv, err)
	}
	if _, err := h.PreviewFile("Missing.java"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal(err)
	}
}
