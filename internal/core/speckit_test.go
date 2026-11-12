package core

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/workflow"
)

func TestInitSpecKitAndSteps(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	if _, err := h.RunStep("constitution"); err == nil || !strings.Contains(err.Error(), "Spec Kit") {
		h.mu.Lock()
		h.wf.Steps[0].Input = "x"
		h.mu.Unlock()
		if _, err := h.RunStep("constitution"); err == nil || !strings.Contains(err.Error(), "Spec Kit") {
			t.Fatal("a step must not run before Spec Kit is set up:", err)
		}
	}
	seedSpecKit(t, h)
	_ = os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("# Team notes\nKeep this.\n"), 0o644)
	h.mu.Lock()
	h.profile.Rules = []workflow.Rule{{ID: "R-1", Text: "Money is BigDecimal"}}
	h.mu.Unlock()

	res, err := h.InitSpecKit("", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Info.Initialized || res.Info.Integration != "claude" || len(res.Created) < 10 || len(res.Context) != 1 {
		t.Fatalf("%+v", res)
	}
	md, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md"))
	if !strings.HasPrefix(string(md), "# Team notes\nKeep this.\n") || !strings.Contains(string(md), ctxBegin) || !strings.Contains(string(md), "R-1: Money is BigDecimal") {
		t.Fatalf("CLAUDE.md:\n%s", md)
	}
	// the block is replaced, not repeated
	if _, err := h.writeAgentContext(); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.profile.Rules[0].Text = "Money is always BigDecimal"
	h.mu.Unlock()
	if files, _ := h.writeAgentContext(); len(files) != 1 {
		t.Fatal("changed context must be rewritten")
	}
	md, _ = os.ReadFile(filepath.Join(ws, "CLAUDE.md"))
	if strings.Count(string(md), ctxBegin) != 1 || !strings.Contains(string(md), "always BigDecimal") {
		t.Fatalf("CLAUDE.md:\n%s", md)
	}

	// each step runs its installed Spec Kit skill, within its scope
	p, err := h.Compose("constitution", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "/speckit-constitution ") || !strings.Contains(p, "read `.claude/skills/speckit-constitution/SKILL.md`") || !strings.Contains(p, "Only create or update `.specify/memory/constitution.md`") {
		t.Fatalf("prompt:\n%s", p)
	}
	if p, _ := h.Compose("analyze", ""); !strings.HasPrefix(p, "/speckit-analyze ") || !strings.Contains(p, "Read-only") {
		t.Fatalf("analyze:\n%s", p)
	}
}

func TestSpecKitProjectFolderAndZip(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	// a selected project skill with a doc, a rule and a diagram in the workspace
	_ = os.MkdirAll(filepath.Join(ws, ".codegen/skills/project/orders/docs"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, ".codegen/skills/project/orders/SKILL.md"), []byte("# Orders\n"), 0o644)
	_ = os.WriteFile(filepath.Join(ws, ".codegen/skills/project/orders/docs/status.md"), []byte("statuses\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(ws, "inputs"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "inputs/context.svg"), []byte("<svg/>"), 0o644)
	h.mu.Lock()
	h.profile.Project = []string{"project:skill:orders"}
	h.profile.Diagrams = map[string]string{"context": "inputs/context.svg"}
	h.mu.Unlock()

	seedSpecKit(t, h)
	if _, err := h.InitSpecKit("../escape", "claude"); err == nil {
		t.Fatal("bad project names must be refused")
	}
	res, err := h.InitSpecKit("orders-service", "claude")
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(ws, "orders-service")
	if _, err := os.Stat(filepath.Join(proj, ".specify/memory/constitution.md")); err != nil {
		t.Fatal("Spec Kit must be set up in the project folder")
	}
	if _, err := os.Stat(filepath.Join(ws, ".specify")); err == nil {
		t.Fatal("nothing at the top folder")
	}
	if res.Info.Project != "orders-service" || !res.Info.Initialized || res.Context[0] != "orders-service/CLAUDE.md" {
		t.Fatalf("%+v", res)
	}
	md, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md"))
	if !strings.Contains(string(md), "`../.codegen/skills/project/orders/SKILL.md`") || !strings.Contains(string(md), "`../inputs/context.svg`") {
		t.Fatalf("CLAUDE.md paths must be relative to the project folder:\n%s", md)
	}
	p, _ := h.Compose("constitution", "")
	if !strings.Contains(p, "working directory is the project folder `orders-service/`") || !strings.Contains(p, "../.codegen/skills/project/orders/SKILL.md") {
		t.Fatalf("prompt:\n%s", p)
	}

	// the step runs inside the project folder
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	setInput(t, h, "constitution", "tests everywhere")
	id, err := h.RunStep("constitution")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, id, "gate.wait")
	b, _ := os.ReadFile(filepath.Join(proj, ".specify/memory/constitution.md"))
	if !strings.Contains(string(b), "Core principles") {
		t.Fatalf("constitution in the project folder: %q", b)
	}
	_ = h.Decide(id, "approve", "")
	waitFor(t, ch, id, "run.end")

	// download: the project folder plus the context it links to
	_ = os.MkdirAll(filepath.Join(proj, "node_modules/x"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, "node_modules/x/i.js"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(proj, "src.go"), []byte("package main\n"), 0o644)
	var buf bytes.Buffer
	z, err := h.WriteProjectZip(&buf, true)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"orders-service/.specify/memory/constitution.md", "orders-service/.claude/skills/speckit-plan/SKILL.md", "orders-service/CLAUDE.md", "orders-service/src.go",
		".codegen/skills/project/orders/SKILL.md", ".codegen/skills/project/orders/docs/status.md", "inputs/context.svg"} {
		if !names[want] {
			t.Errorf("zip lacks %s", want)
		}
	}
	if names["orders-service/node_modules/x/i.js"] || z.Files != len(zr.File) || !strings.HasPrefix(z.Name, "orders-service-") {
		t.Fatalf("%+v", z)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if r, err := h.ExportProjectZip(dest, false); err != nil || !strings.HasSuffix(r.Path, "out.zip") || r.Files >= z.Files {
		t.Fatal(r, err)
	}
	if _, err := h.ExportProjectZip(filepath.Join(ws, "x.zip"), false); err == nil {
		t.Fatal("the zip must not be written into the project")
	}
}
