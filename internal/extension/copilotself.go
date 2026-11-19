package extension

// SpecWright's tools as a GitHub Copilot extension (copilotself.mjs), in
// the user's Copilot folder (~/.copilot/extensions/specwright). Where
// Copilot loads extensions but not SpecWright's MCP server, its agents
// still get save_flow and the rest (as specwright_save_flow, …). Outside
// a SpecWright run the extension registers nothing. SpecWright lists the
// user's extensions without it.

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// SelfCopilotExt is the folder name of SpecWright's own Copilot extension.
const SelfCopilotExt = "specwright"

//go:embed copilotself.mjs
var copilotSelfJS []byte

var copilotSelfPkg = []byte(`{
  "name": "specwright-tools",
  "private": true,
  "type": "module",
  "description": "SpecWright's tools for GitHub Copilot (written by SpecWright; changes are overwritten)"
}
`)

// EnsureCopilotSelfExt writes (or brings up to date) SpecWright's Copilot
// extension in the user's Copilot folder; it changes nothing when it is
// already current.
func EnsureCopilotSelfExt() (string, error) {
	base := UserCopilotExtDir()
	if base == "" {
		return "", os.ErrNotExist
	}
	dir := filepath.Join(base, SelfCopilotExt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir, err
	}
	for name, b := range map[string][]byte{"extension.mjs": copilotSelfJS, "package.json": copilotSelfPkg} {
		p := filepath.Join(dir, name)
		if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, b) {
			continue
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return dir, err
		}
	}
	return dir, nil
}
