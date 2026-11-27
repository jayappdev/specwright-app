package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// PlantUML runs on this computer, in its sandbox profile, with the diagram on
// standard input; without it the error says how to set it up.
func TestRenderPlantUML(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as plantuml")
	}
	h, _ := newHub(t)
	if _, err := h.RenderPlantUML("  "); err == nil {
		t.Fatal("an empty diagram")
	}
	// no plantuml anywhere: a clear message
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PLANTUML_JAR", "")
	if _, err := h.RenderPlantUML("A -> B"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
	// a fake plantuml: echoes its input inside an SVG, and the profile
	bin := t.TempDir()
	fake := "#!/bin/sh\nin=$(cat)\nprintf '<?xml version=\"1.0\"?><svg data-profile=\"%s\">%s</svg>' \"$PLANTUML_SECURITY_PROFILE\" \"$in\"\n"
	if err := os.WriteFile(filepath.Join(bin, "plantuml"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin:/usr/bin")
	svg, err := h.RenderPlantUML("A -> B")
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, `data-profile="SANDBOX"`) || !strings.Contains(svg, "@startuml\nA -> B\n@enduml") {
		t.Fatalf("%q %v", svg, err)
	}
}

// With PlantUML installed (skipped where it is not, as on CI): a real
// diagram is drawn, and the sandbox keeps local files out of it.
func TestRealPlantUML(t *testing.T) {
	if _, err := exec.LookPath("plantuml"); err != nil {
		t.Skip("PlantUML is not installed")
	}
	h, _ := newHub(t)
	svg, err := h.RenderPlantUML("Alice -> Bob: hello\nBob --> Alice: hi")
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "Alice") {
		t.Fatalf("%v %.200q", err, svg)
	}
	out, _ := h.RenderPlantUML("@startuml\n!include /etc/hosts\nA -> B\n@enduml")
	if strings.Contains(out, "localhost") {
		t.Fatal("the sandbox let a local file into the diagram")
	}
}
