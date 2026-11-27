// Applies a theme (color variables) and a skin (layout/type) to <html>.
import { THEMES as GITHUB } from "./themes.generated.js";
import { SKINS as BASE_SKINS } from "./themes.generated.js";
import { PALETTE_THEMES, materialTheme, EXTRA_SKINS, UI_FONTS, CODE_FONTS } from "./palettes.js";

// Every skin: Workbench, Console, Paper, Material and Copilot.
export const ALL_SKINS = [...BASE_SKINS, ...EXTRA_SKINS];

// Every built-in theme: GitHub (Primer), Copilot and Android (Material 3).
export const ALL_THEMES = [...GITHUB.map((t) => ({ ...t, family: "github" })), ...PALETTE_THEMES];

const dark = () => typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches;

export function resolveTheme(id) {
  if (id === "system") id = dark() ? "dark-default" : "light-default";
  const m = /^m3:(#[0-9a-f]{6}):(dark|light)$/i.exec(id || "");
  if (m) return materialTheme(m[1], m[2] === "dark");
  return ALL_THEMES.find((t) => t.id === id) || ALL_THEMES[0];
}

export function applyAppearance(a) {
  const t = resolveTheme(a.theme),
    r = document.documentElement;
  for (const [k, v] of Object.entries(t.ui)) r.style.setProperty(k, v);
  r.style.colorScheme = t.type;
  r.dataset.mode = t.type; // light | dark: charts pick their colour steps by it
  r.dataset.theme = t.id;
  r.dataset.skin = a.skin;
  r.dataset.density = a.density === "compact" ? "compact" : "comfortable";
  r.style.setProperty("--font-size", a.fontSize + "px");
  // a chosen font overrides the skin's; "" leaves the skin's in place
  const ui = UI_FONTS.find((f) => f.id === a.font)?.stack,
    code = CODE_FONTS.find((f) => f.id === a.codeFont)?.stack;
  if (ui) r.style.setProperty("--font-ui", ui);
  else r.style.removeProperty("--font-ui");
  if (code) r.style.setProperty("--font-mono", code);
  else r.style.removeProperty("--font-mono");
  document.body.style.background = t.ui["--bg"];
}

export function onSystemThemeChange(cb) {
  if (typeof matchMedia !== "function") return () => {};
  const m = matchMedia("(prefers-color-scheme: dark)");
  m.addEventListener("change", cb);
  return () => m.removeEventListener("change", cb);
}
