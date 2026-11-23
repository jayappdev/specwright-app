package core

// save_tool: an agent hands the person a revised (or new) tool. It is
// checked and given back as a proposal: nothing is saved, approved or run.
// The Prompt Playground shows it under the answer, where the person applies
// it to the tool and it is tested ("Apply to the tool and test"); saving
// stays theirs, in Tools. Agents looked for this tool by this name (by
// analogy with save_flow) and found nothing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codegen/internal/extension"
)

// SaveToolTool is the built-in agents propose tool revisions with.
const SaveToolTool = "save_tool"

func saveToolInfo() extension.BuiltinInfo {
	return extension.BuiltinInfo{
		Name:  SaveToolTool,
		Title: "Propose a tool revision",
		Group: "tools",
		Description: "Hand the user a revised SpecWright tool (or a new one): give the WHOLE tool (name, desc, lang, command, script, params, secrets, deps) and why. " +
			"It is checked and shown to the user, who applies it to the tool and tests it: it is NOT saved, approved or run by this call. " +
			"Use it instead of editing .codegen/extensions.json or the tool's files. The answer lists problems to fix (call it again with the fixed tool).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool": map[string]any{"description": "The whole tool: {\"name\", \"desc\", \"lang\", \"command\", \"script\", \"params\": [{\"name\", \"type\", \"required\", \"desc\", \"default\"}], \"secrets\": [{\"name\", \"desc\"}], \"deps\": []} (an object, or its JSON text)"},
				"why":  map[string]any{"type": "string", "description": "What you changed and why, in one to three sentences (shown to the user)."},
			},
			"required": []string{"tool"},
		},
	}
}

// agentSaveTool implements save_tool: check the tool, say what the user
// will see.
func (h *Hub) agentSaveTool(args map[string]any) (string, error) {
	if _, err := h.need(); err != nil {
		return "", err
	}
	var raw string
	switch v := args["tool"].(type) {
	case string:
		raw = v
	case map[string]any:
		b, _ := json.Marshal(v)
		raw = string(b)
	default:
		return "", errors.New("give tool: the whole tool as a JSON object")
	}
	d, _, err := toolFromText(raw)
	if err != nil {
		return "", fmt.Errorf("tool could not be read: %v", err)
	}
	var probs []string
	if !extension.NameRE.MatchString(d.Name) {
		probs = append(probs, "name: lowercase letters, digits, - and _, starting with a letter (at most 40)")
	}
	if strings.TrimSpace(d.Command) == "" && strings.TrimSpace(d.Script) == "" {
		probs = append(probs, "give the command it runs, or its script")
	}
	if len(probs) > 0 {
		return "", errors.New("fix the tool and call save_tool again: " + strings.Join(probs, "; "))
	}
	_, changes := extension.InferParams(d)
	var b strings.Builder
	if saved, err := h.exts.Get(d.Name); err == nil {
		if saved.Kind == extension.KindCopilot || saved.Managed != nil || extension.IsManagedScope(saved.Scope) {
			return "", fmt.Errorf("%s is not a project tool you can revise here (a Copilot extension or an admin tool)", d.Name)
		}
		fmt.Fprintf(&b, "Proposed revision %d of the tool %s.", max(saved.Revision, 1)+1, d.Name)
	} else {
		fmt.Fprintf(&b, "Proposed a new tool %s.", d.Name)
	}
	b.WriteString(" The user sees it under your answer and applies it to the tool, where it is tested; nothing is saved or run until they do. Do not also edit the tool's files.")
	if len(changes) > 0 {
		b.WriteString("\nNotes from checking it (fix them if they are wrong):")
		for _, c := range changes {
			fmt.Fprintf(&b, "\n- %s: %s", c.Param, c.What)
		}
	}
	return b.String(), nil
}
