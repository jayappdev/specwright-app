package speckit

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteSample writes a small project shaped like `specify init` output into
// dir, for tests (SpecWright's own and those of packages using it).
func WriteSample(dir, integration, version string) error {
	agent := map[string]string{"claude": ".claude/skills", "copilot": ".github/skills"}[integration]
	files := map[string]string{
		".specify/memory/constitution.md":         "# [PROJECT_NAME] Constitution\n\n### [PRINCIPLE_1_NAME]\n",
		".specify/templates/spec-template.md":     "# Feature Specification: [FEATURE NAME]\n",
		".specify/templates/plan-template.md":     "# Implementation Plan\n",
		".specify/scripts/bash/common.sh":         "#!/usr/bin/env bash\n",
		".specify/init-options.json":              `{"integration":"` + integration + `"}`,
		".specify/workflows/speckit/workflow.yml": "workflow:\n  id: speckit\n",
	}
	for _, s := range []string{"constitution", "specify", "clarify", "plan", "tasks", "analyze", "implement"} {
		files[agent+"/speckit-"+s+"/SKILL.md"] = "---\nname: speckit-" + s + "\n---\n$ARGUMENTS\n"
	}
	manifest := map[string]string{}
	for rel, body := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if filepath.Ext(rel) == ".sh" {
			mode = 0o755
		}
		if err := os.WriteFile(abs, []byte(body), mode); err != nil {
			return err
		}
		manifest[rel] = hash([]byte(body))
	}
	ij, _ := json.Marshal(map[string]any{"version": version, "installed_integrations": []string{integration},
		"integration_settings": map[string]any{integration: map[string]string{"script": "sh"}}, "integration": integration, "default_integration": integration})
	mf, _ := json.Marshal(map[string]any{"integration": integration, "version": version, "files": manifest})
	if err := os.MkdirAll(filepath.Join(dir, ".specify", "integrations"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".specify", "integration.json"), ij, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".specify", "integrations", integration+".manifest.json"), mf, 0o644)
}
