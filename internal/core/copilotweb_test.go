package core

import (
	"testing"

	"codegen/internal/agent"
)

func TestSetCopilotWeb(t *testing.T) {
	h, _ := newHub(t)
	d, _ := h.Agents.Get("copilot")
	cp := d.(*agent.Copilot)
	if err := h.SetCopilotWeb(agent.WebAccess{Mode: "sites", Sites: []string{" docs.github.com ", "", "https://*.npmjs.com"}}); err != nil {
		t.Fatal(err)
	}
	if w := cp.Web(); w.Mode != "sites" || len(w.Sites) != 2 || w.Sites[0] != "docs.github.com" {
		t.Fatalf("saved %+v", w)
	}
	for _, bad := range []agent.WebAccess{{Mode: "sites"}, {Mode: "sites", Sites: []string{"a.com b.com"}}, {Mode: "sites", Sites: []string{"ftp://x"}}, {Mode: "everything"}} {
		if err := h.SetCopilotWeb(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if err := h.SetCopilotWeb(agent.WebAccess{}); err != nil || cp.Web().Mode != "none" {
		t.Fatalf("empty means none: %v %+v", err, cp.Web())
	}
}
