package core

// Tool usage guides.
//
//   - SaveToolGuide: saves a project or global tool's guide (keeps its
//     approval; an admin-managed tool's guide is saved with SaveManagedTool).
//   - GenerateToolGuide: the AI reads the tool (source, parameters, tests)
//     and drafts a guide with worked examples, for the user to review.
//   - RunToolExample: runs one example the way a flow step would: its
//     arguments are rendered with the example's sample as the previous
//     step's output, then the tool runs as on its Test page.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// SaveToolGuide saves a tool's usage guide (nil removes it).
func (h *Hub) SaveToolGuide(name string, g *extension.ToolGuide) (extension.Extension, error) {
	if _, err := h.need(); err != nil {
		return extension.Extension{}, err
	}
	return h.exts.UpdateGuide(name, g)
}

// ToolGuideDraft is what GenerateToolGuide proposes.
type ToolGuideDraft struct {
	Guide extension.ToolGuide `json:"guide"`
	Why   string              `json:"why,omitempty"`
}

// GenerateToolGuide drafts a usage guide with examples for a tool. ask is
// what the user wants the guide to cover (optional).
func (h *Hub) GenerateToolGuide(e extension.Extension, model, ask string) (ToolGuideDraft, error) {
	root, err := h.need()
	if err != nil {
		return ToolGuideDraft{}, err
	}
	cur, _ := json.MarshalIndent(map[string]any{"params": e.Params, "output": e.Output, "secrets": e.Secrets, "tests": e.Tests, "guide": e.Guide}, "", "  ")
	var b strings.Builder
	fmt.Fprintf(&b, "You write the usage guide of a tool used as a step in automation flows. Read how it is run and its source, then explain what it is for and give worked examples a user can run.\n\nTOOL: %s\nDESCRIPTION: %s\nKIND: %s\nCOMMAND: %s\n", e.Name, e.Desc, firstNonEmpty(e.Kind, "script"), e.Command)
	b.WriteString("\nWHAT IT DECLARES NOW (parameters, output, secrets, test cases, the current guide):\n" + clipText(string(cur), 20000) + "\n")
	b.WriteString("\nSOURCE:\n" + toolSource(root, e) + "\n")
	if ask = strings.TrimSpace(ask); ask != "" {
		b.WriteString("\nTHE USER ASKS (do this first):\n" + clipText(ask, 4000) + "\n")
	}
	b.WriteString(`
Write:
- "summary": one or two sentences, what the tool does.
- "when": 2 to 5 short situations where it is the right step.
- "how": 3 to 6 short tips on setting its parameters, secrets and what it gives the next step.
- "examples": 3 to 6 worked examples, each a step in a flow. "args" maps parameter names (only ones the tool really has) to the text the step's field holds; every value is a string (JSON as text). A field can use the previous step's output: {{sample.buffer_output}} (its text), {{input}} (same), or {{input_file}} (a file holding it). When an example needs input data, put a small realistic sample in "sample" (the previous step's output) and refer to it that way. Prefer examples that run in any project (no real servers, paths or passwords) and are read-only; "result" is the output the example gives (short, as the tool prints it).

Reply with ONLY this JSON, no explanation and no code fences:
{"guide": {"summary": "...", "when": ["..."], "how": ["..."],
  "examples": [{"title": "...", "text": "<what it shows, one or two sentences>", "args": {"<param>": "<value>"}, "sample": "", "result": "..."}]},
 "why": "<one sentence: what you read to write this>"}`)
	h.designProgress("Reading " + e.Name + " to write its guide")
	text, u, err := h.designAgent(root, b.String(), model)
	h.recordDesignUsage(root, u, model)
	if err != nil {
		return ToolGuideDraft{}, err
	}
	var got struct {
		Guide struct {
			Summary  string   `json:"summary"`
			When     []string `json:"when"`
			How      []string `json:"how"`
			Examples []struct {
				Title  string         `json:"title"`
				Text   string         `json:"text"`
				Args   map[string]any `json:"args"`
				Sample any            `json:"sample"`
				Result any            `json:"result"`
			} `json:"examples"`
		} `json:"guide"`
		Why string `json:"why"`
	}
	if err := json.Unmarshal([]byte(firstJSON(jsonReply(text))), &got); err != nil {
		return ToolGuideDraft{}, fmt.Errorf("the AI's answer could not be read: %v", err)
	}
	d := ToolGuideDraft{Why: got.Why, Guide: extension.ToolGuide{Summary: got.Guide.Summary, When: got.Guide.When, How: got.Guide.How}}
	known := map[string]bool{}
	for _, p := range e.Params {
		known[p.Name] = true
	}
	for _, x := range got.Guide.Examples {
		ex := extension.ToolExample{Title: x.Title, Text: x.Text, Args: map[string]string{}, Sample: asText(x.Sample), Result: asText(x.Result)}
		for k, v := range x.Args {
			if len(known) == 0 || known[k] {
				ex.Args[k] = asText(v)
			}
		}
		d.Guide.Examples = append(d.Guide.Examples, ex)
	}
	g, err := extension.CleanGuide(&d.Guide)
	if err != nil {
		return ToolGuideDraft{}, err
	}
	if g == nil {
		return ToolGuideDraft{}, fmt.Errorf("the AI wrote an empty guide")
	}
	d.Guide = *g
	return d, nil
}

// asText is a JSON value as a field holds it: strings as they are, the
// rest as JSON.
func asText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// ExampleArgs renders an example's arguments as a flow step would, with
// the example's sample as the output of the step before it (id "sample").
// inputFile is the file holding the sample ({{input_file}}).
func ExampleArgs(ex extension.ToolExample, inputFile string) map[string]any {
	d := flow.Data{Inputs: map[string]string{}, Steps: map[string]string{}, Files: map[string]string{}, Input: ex.Sample, InputFile: inputFile}
	if ex.Sample != "" {
		d.Steps["sample"], d.Files["sample"] = ex.Sample, inputFile
	}
	keys := make([]string, 0, len(ex.Args))
	for k := range ex.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, k := range keys {
		out[k] = flow.Render(ex.Args[k], d)
	}
	return out
}

// RunToolExample runs one of a tool's guide examples (see ExampleArgs).
func (h *Hub) RunToolExample(e extension.Extension, ex extension.ToolExample, secrets map[string]string) (ToolRun, error) {
	if _, err := h.need(); err != nil {
		return ToolRun{}, err
	}
	file := ""
	if ex.Sample != "" {
		dir, err := os.MkdirTemp("", "specwright-example-")
		if err != nil {
			return ToolRun{}, err
		}
		defer os.RemoveAll(dir)
		file = filepath.Join(dir, "input."+contentExt(ex.Sample))
		if err := os.WriteFile(file, []byte(ex.Sample), 0o600); err != nil {
			return ToolRun{}, err
		}
	}
	return h.RunToolWith(e, ExampleArgs(ex, file), secrets, nil)
}
