package core

// PlantUML diagrams in HTML and Markdown previews: the UI finds them and
// asks for each one's SVG here. PlantUML runs on this computer only (no
// diagram is sent to a server): the `plantuml` command, or Java with a
// plantuml.jar (PLANTUML_JAR, the app's data folder, or where Homebrew and
// Linux packages put it). It runs in PlantUML's SANDBOX security profile, so
// a diagram cannot read files or fetch URLs with !include.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codegen/internal/safeenv"
)

const maxPlantUML = 200 << 10

var plantCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// plantumlCommand: how PlantUML runs here (argv before its own flags), or
// an error saying how to set it up.
func (h *Hub) plantumlCommand() ([]string, error) {
	if p, err := exec.LookPath("plantuml"); err == nil {
		return []string{p}, nil
	}
	java, jerr := exec.LookPath("java")
	jars := []string{os.Getenv("PLANTUML_JAR"), filepath.Join(h.Store.Dir(), "plantuml.jar")}
	if home, err := os.UserHomeDir(); err == nil {
		jars = append(jars, filepath.Join(home, "plantuml.jar"))
	}
	jars = append(jars, "/opt/homebrew/opt/plantuml/libexec/plantuml.jar", "/usr/local/opt/plantuml/libexec/plantuml.jar", "/usr/share/plantuml/plantuml.jar", "/usr/share/java/plantuml.jar")
	for _, j := range jars {
		if j == "" {
			continue
		}
		if st, err := os.Stat(j); err == nil && !st.IsDir() {
			if jerr != nil {
				return nil, fmt.Errorf("found %s, but Java is not installed to run it", j)
			}
			return []string{java, "-Djava.awt.headless=true", "-DPLANTUML_SECURITY_PROFILE=SANDBOX", "-jar", j}, nil
		}
	}
	return nil, fmt.Errorf("PlantUML is not installed on this computer: install it (macOS: brew install plantuml), or put plantuml.jar in %s or set PLANTUML_JAR", h.Store.Dir())
}

// RenderPlantUML draws a PlantUML diagram as SVG.
func (h *Hub) RenderPlantUML(src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", errors.New("the diagram is empty")
	}
	if len(src) > maxPlantUML {
		return "", errors.New("the diagram is too large to draw")
	}
	if !strings.HasPrefix(src, "@start") {
		src = "@startuml\n" + src + "\n@enduml"
	}
	sum := sha256.Sum256([]byte(src))
	key := hex.EncodeToString(sum[:])
	plantCache.Lock()
	if svg, ok := plantCache.m[key]; ok {
		plantCache.Unlock()
		return svg, nil
	}
	plantCache.Unlock()
	argv, err := h.plantumlCommand()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], "-tsvg", "-pipe", "-charset", "UTF-8", "-nometadata")...)
	cmd.Env = append(safeenv.Clean(os.Environ()), "PLANTUML_SECURITY_PROFILE=SANDBOX", "JAVA_TOOL_OPTIONS=-Djava.awt.headless=true")
	cmd.Stdin = strings.NewReader(src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		msg := strings.TrimSpace(errb.String())
		if ctx.Err() != nil {
			msg = "PlantUML took too long"
		}
		return "", fmt.Errorf("PlantUML could not draw it: %s", firstNonEmpty(msg, err.Error()))
	}
	svg := out.String()
	if i := strings.Index(svg, "<svg"); i > 0 {
		svg = svg[i:] // drop an XML prolog
	}
	if !strings.HasPrefix(svg, "<svg") {
		return "", errors.New("PlantUML gave no picture")
	}
	plantCache.Lock()
	if len(plantCache.m) > 200 {
		plantCache.m = map[string]string{}
	}
	plantCache.m[key] = svg
	plantCache.Unlock()
	return svg, nil
}
