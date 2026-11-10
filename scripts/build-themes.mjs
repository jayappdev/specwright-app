#!/usr/bin/env node
// Generates frontend/src/themes.generated.js from Primer's GitHub VS Code
// themes (https://github.com/primer/github-vscode-theme, MIT).
//
//   node scripts/build-themes.mjs <themes-dir>
//
// <themes-dir> holds the seven built theme JSON files, e.g. the `themes/`
// folder of a built checkout (npm ci && npm run build) or `extension/themes`
// inside the marketplace .vsix. Each VS Code color key maps to one CSS
// variable; syntax colors come from tokenColors scopes.
import { readFile, writeFile, readdir } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const OUT = join(here, "..", "frontend", "src", "themes.generated.js");

const FILES = {
  "dark-colorblind": ["dark-colorblind.json", "GitHub Dark Colorblind"],
  "dark-default": ["dark-default.json", "GitHub Dark Default"],
  "dark-dimmed": ["dark-dimmed.json", "GitHub Dark Dimmed"],
  "dark-high-contrast": ["dark-high-contrast.json", "GitHub Dark High Contrast"],
  "light-colorblind": ["light-colorblind.json", "GitHub Light Colorblind"],
  "light-default": ["light-default.json", "GitHub Light Default"],
  "light-high-contrast": ["light-high-contrast.json", "GitHub Light High Contrast"],
};

// CSS variable ← VS Code color keys (first one present wins).
const UI = {
  "--bg": ["editor.background"],
  "--fg": ["editor.foreground", "foreground"],
  "--muted": ["descriptionForeground"],
  "--faint": ["editorLineNumber.foreground"],
  "--surface": ["editorWidget.background", "sideBar.background"],
  "--sidebar-bg": ["sideBar.background"],
  "--sidebar-fg": ["sideBar.foreground", "foreground"],
  "--activity-bg": ["activityBar.background"],
  "--activity-fg": ["activityBar.foreground"],
  "--activity-dim": ["activityBar.inactiveForeground"],
  "--activity-accent": ["activityBar.activeBorder", "focusBorder"],
  "--border": ["panel.border", "editorGroup.border", "sideBar.border"],
  "--input-bg": ["input.background"],
  "--input-border": ["input.border"],
  "--btn-bg": ["button.background"],
  "--btn-fg": ["button.foreground"],
  "--btn-hover": ["button.hoverBackground"],
  "--btn2-bg": ["button.secondaryBackground"],
  "--btn2-fg": ["button.secondaryForeground"],
  "--focus": ["focusBorder"],
  "--link": ["textLink.foreground"],
  "--hover": ["list.hoverBackground"],
  "--select-bg": ["list.activeSelectionBackground"],
  "--select-fg": ["list.activeSelectionForeground", "foreground"],
  "--status-bg": ["statusBar.background"],
  "--status-fg": ["statusBar.foreground"],
  "--status-border": ["statusBar.border", "panel.border"],
  "--title-bg": ["titleBar.activeBackground"],
  "--badge-bg": ["badge.background"],
  "--badge-fg": ["badge.foreground"],
  "--error": ["errorForeground", "editorError.foreground"],
  "--warn": ["editorWarning.foreground", "list.warningForeground"],
  "--info": ["editorInfo.foreground", "terminal.ansiBlue"],
  "--ok": ["gitDecoration.addedResourceForeground", "terminal.ansiGreen"],
  "--diff-add": ["diffEditor.insertedTextBackground"],
  "--diff-del": ["diffEditor.removedTextBackground"],
  "--code-bg": ["textCodeBlock.background", "textPreformat.background"],
  "--ansi-red": ["terminal.ansiRed"],
  "--ansi-green": ["terminal.ansiGreen"],
  "--ansi-yellow": ["terminal.ansiYellow"],
  "--ansi-blue": ["terminal.ansiBlue"],
  "--ansi-magenta": ["terminal.ansiMagenta"],
  "--ansi-cyan": ["terminal.ansiCyan"],
};

// CSS variable ← TextMate scope (exact or prefix match in tokenColors).
const SYN = {
  "--syn-comment": "comment",
  "--syn-keyword": "keyword",
  "--syn-string": "string",
  "--syn-number": "constant",
  "--syn-function": "entity.name.function",
  "--syn-type": "storage.type",
  "--syn-variable": "variable.other",
  "--syn-property": "support.variable.property",
};

const SKINS = [
  { id: "workbench", name: "Workbench", note: "Activity bar, compact lists, editor-style panes." },
  { id: "console", name: "Console", note: "Monospace everywhere, square corners, dense rows." },
  { id: "paper", name: "Paper", note: "Roomy sans-serif with a centered reading column." },
];

function synColor(tokenColors, scope) {
  for (const tc of tokenColors || []) {
    const scopes = Array.isArray(tc.scope) ? tc.scope : String(tc.scope || "").split(",").map((s) => s.trim());
    if (tc.settings?.foreground && scopes.some((s) => s === scope || s.startsWith(scope + "."))) return tc.settings.foreground;
  }
  return null;
}

async function main() {
  const dir = process.argv[2];
  if (!dir) {
    console.error("usage: node scripts/build-themes.mjs <themes-dir>\n  (the built themes/ folder of primer/github-vscode-theme)");
    process.exit(2);
  }
  const present = new Set(await readdir(dir));
  const themes = [];
  for (const [id, [file, name]] of Object.entries(FILES)) {
    if (!present.has(file)) throw new Error(`${file} not found in ${dir}`);
    const t = JSON.parse(await readFile(join(dir, file), "utf8"));
    const c = t.colors || {};
    const ui = {};
    for (const [v, keys] of Object.entries(UI)) {
      const k = keys.find((x) => c[x]);
      if (!k) throw new Error(`${file}: none of ${keys.join(", ")} for ${v}`);
      ui[v] = c[k].toLowerCase();
    }
    for (const [v, scope] of Object.entries(SYN)) ui[v] = (synColor(t.tokenColors, scope) || ui["--fg"]).toLowerCase();
    themes.push({ id, name, type: id.startsWith("dark") ? "dark" : "light", contrast: id.includes("high-contrast"), colorblind: id.includes("colorblind"), ui });
  }
  const src = `// Generated by scripts/build-themes.mjs from primer/github-vscode-theme (MIT). Do not edit by hand.\n` +
    `export const THEMES = ${JSON.stringify(themes)};\n\nexport const SKINS = ${JSON.stringify(SKINS, null, 2)};\n`;
  await writeFile(OUT, src);
  console.log(`wrote ${themes.length} themes and ${SKINS.length} skins to ${OUT}`);
}

main().catch((e) => { console.error(e.message); process.exit(1); });
