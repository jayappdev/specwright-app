package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
)

// Project scripts (.codegen/scripts/) can be written by the AI from a plain
// description, for people who know what the script should do but not how to
// write it. The agent only returns the script text: nothing is saved until
// the user presses Save in the editor.

// ScriptRequest asks for a new script, or a change to the current one.
type ScriptRequest struct {
	Description string `json:"description"`       // what the script should do, or what to change
	Language    string `json:"language"`          // bash | python | node | "" (let the AI choose)
	FileName    string `json:"fileName"`          // wanted file name, may be empty
	Current     string `json:"current,omitempty"` // the script so far, to revise it
	Model       string `json:"model,omitempty"`
}

// ScriptDraft is the AI's script.
type ScriptDraft struct {
	FileName string `json:"fileName"`
	Content  string `json:"content"`
	Summary  string `json:"summary"` // what it does and how to run it
}

const scriptInstructions = `You write a small, self-contained script for a software project. It is saved in the project's .codegen/scripts/ folder, run from the project root, and an AI coding agent may also run it.

Rules:
- Start with a shebang line and a one-line comment saying what the script does.
- Read options from command-line arguments; print a short usage message for -h or --help and when a required argument is missing.
- Fail clearly: a non-zero exit code and a message on stderr. In bash use "set -euo pipefail" and quote every variable.
- Use only the standard library or tools a developer machine normally has, unless the description names others. Never hard-code passwords, tokens or keys: read them from environment variables and say which.
- Paths are relative to the project root unless given as arguments.
- Keep it short and readable, with brief comments where the logic is not obvious.

Do not create, edit or run any files. Reply with only a JSON object, no other text:
{"fileName": "kebab-case-name.ext", "content": "<the whole script>", "summary": "<one or two sentences: what it does and an example command to run it>"}`

var scriptLang = map[string]string{
	"bash":   "Bash (.sh)",
	"python": "Python 3 (.py)",
	"node":   "Node.js (.mjs)",
}

// GenerateScript asks the agent to write (or revise) a project script.
func (h *Hub) GenerateScript(r ScriptRequest) (ScriptDraft, error) {
	root, err := h.need()
	if err != nil {
		return ScriptDraft{}, err
	}
	desc := strings.TrimSpace(r.Description)
	if desc == "" {
		if strings.TrimSpace(r.Current) == "" {
			return ScriptDraft{}, errors.New("describe what the script should do")
		}
		return ScriptDraft{}, errors.New("describe what to change in the script")
	}
	var b strings.Builder
	b.WriteString(scriptInstructions)
	b.WriteString("\n\n")
	if strings.TrimSpace(r.Current) != "" {
		fmt.Fprintf(&b, "Revise this script. Change request: %s\n", desc)
		if r.FileName != "" {
			fmt.Fprintf(&b, "File name: %s (keep it unless the language changes)\n", r.FileName)
		}
		fmt.Fprintf(&b, "\nCurrent script:\n%s\n", trunc(r.Current, 20000))
	} else {
		fmt.Fprintf(&b, "What the script should do: %s\n", desc)
		if l := scriptLang[r.Language]; l != "" {
			fmt.Fprintf(&b, "Language: %s\n", l)
		} else {
			b.WriteString("Language: choose the simplest fit (Bash for running commands and files, Python for data and text processing, Node.js for JSON and web APIs).\n")
		}
		if r.FileName != "" {
			fmt.Fprintf(&b, "File name: %s\n", r.FileName)
		}
	}
	if langs := h.projectLanguages(); langs != "" {
		fmt.Fprintf(&b, "The project uses: %s\n", langs)
	}

	h.mu.RLock()
	name, model := h.agentName, h.model
	h.mu.RUnlock()
	if r.Model != "" {
		model = r.Model
	}
	drv, err := h.Agents.Get(name)
	if err != nil {
		return ScriptDraft{}, err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 4*time.Minute)
	defer cancel()
	var text strings.Builder
	res, err := drv.Run(ctx, agent.Request{Purpose: "script", Prompt: b.String(), Dir: root, Model: model, Env: h.childEnv()}, func(ev agent.Event) {
		if ev.Type == "model.text" {
			if t, _ := ev.Data["text"].(string); t != "" {
				text.WriteString(t + "\n")
			}
		}
		h.Bus.Publish(bus.Event{Type: "gen." + ev.Type, Summary: ev.Summary})
	})
	if err != nil {
		return ScriptDraft{}, err
	}
	out := res.Text
	if out == "" {
		out = text.String()
	}
	return parseScriptDraft(out, r)
}

var codeFenceRE = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\\n(.*?)```")

// parseScriptDraft reads the agent's JSON; a reply with only a code block
// is accepted too.
func parseScriptDraft(out string, r ScriptRequest) (ScriptDraft, error) {
	var d ScriptDraft
	if err := json.Unmarshal([]byte(firstJSON(out)), &d); err != nil || strings.TrimSpace(d.Content) == "" {
		m := codeFenceRE.FindStringSubmatch(out)
		if m == nil || !strings.HasPrefix(strings.TrimSpace(m[1]), "#!") {
			return ScriptDraft{}, errors.New("the AI did not return a script; try describing it again")
		}
		d = ScriptDraft{Content: m[1]}
	}
	if !strings.HasSuffix(d.Content, "\n") {
		d.Content += "\n"
	}
	d.FileName = strings.TrimSpace(filepath.Base(d.FileName))
	if r.FileName != "" && (strings.TrimSpace(r.Current) == "" || d.FileName == "" || filepath.Ext(d.FileName) == filepath.Ext(r.FileName)) {
		d.FileName = r.FileName
	}
	if !scriptNameRE.MatchString(d.FileName) {
		d.FileName = "script" + extForShebang(d.Content)
	}
	return d, nil
}

func extForShebang(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	switch {
	case strings.Contains(first, "python"):
		return ".py"
	case strings.Contains(first, "node"):
		return ".mjs"
	}
	return ".sh"
}

// projectLanguages names the project's language and build tool, for context.
func (h *Hub) projectLanguages() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.profile == nil || h.profile.Lang == "" {
		return ""
	}
	if h.profile.Build != "" {
		return h.profile.Lang + " with " + h.profile.Build
	}
	return h.profile.Lang
}
