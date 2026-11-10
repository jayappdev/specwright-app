// Theme families built from a few seed colors, next to the GitHub themes in
// themes.generated.js:
//
//   Copilot  dark and light themes in the style of GitHub Copilot's app:
//            cool near-black or paper-white surfaces with violet, pink and
//            teal accents.
//   Android  Material 3 ("Material You") color schemes: tonal palettes from
//            one seed color, mapped to Material's color roles (primary,
//            secondary container, surface containers, outline, …). Any seed
//            color works; the theme id "m3:#rrggbb:dark|light" builds one.
//
// Colors are worked out in OKLCH (perceptual lightness, so a "tone" looks
// equally light whatever the hue) and written out as sRGB hex, which is
// what the rest of the app expects in the --* variables.

// --- color math ------------------------------------------------------------

const clamp = (x, lo = 0, hi = 1) => Math.min(hi, Math.max(lo, x));
const hex2 = (x) => Math.round(clamp(x) * 255).toString(16).padStart(2, "0");
const toLin = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const toGamma = (c) => (c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055);

function oklabToRgb(L, a, b) {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
}

function inGamut([r, g, b]) { const e = 1e-4; return r >= -e && r <= 1 + e && g >= -e && g <= 1 + e && b >= -e && b <= 1 + e; }

// oklch → hex, lowering chroma until the color fits in sRGB.
export function oklch(L, C, H, alpha) {
  const h = (H * Math.PI) / 180;
  let c = C, rgb = oklabToRgb(L, c * Math.cos(h), c * Math.sin(h));
  for (let i = 0; i < 24 && !inGamut(rgb); i++) { c *= 0.9; rgb = oklabToRgb(L, c * Math.cos(h), c * Math.sin(h)); }
  return "#" + rgb.map((x) => hex2(toGamma(clamp(x)))).join("") + (alpha != null ? hex2(alpha) : "");
}

// hex → { L, C, H } in OKLCH.
export function toOklch(hex) {
  const n = hex.replace("#", ""), v = (i) => toLin(parseInt(n.slice(i, i + 2), 16) / 255);
  const [r, g, b] = [v(0), v(2), v(4)];
  const l = Math.cbrt(0.4122214708 * r + 0.5363015719 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s;
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  return { L, C: Math.hypot(A, B), H: ((Math.atan2(B, A) * 180) / Math.PI + 360) % 360 };
}

// A tonal palette: tone 0 (black) … 100 (white) at a fixed hue and chroma.
// Chroma tapers near black and white, as in Material's palettes.
const palette = (H, C) => (tone, alpha) => {
  // Material tones are CIE L*; OKLab lightness is close to cbrt(Y) = (L* + 16) / 116.
  const L = tone <= 0 ? 0 : (tone + 16) / 116, taper = Math.min(1, tone / 25, (100 - tone) / 12);
  return oklch(L, C * Math.max(0.15, taper), H, alpha);
};

// --- scheme → app variables -------------------------------------------------

// Builds the app's --* variables from a seed: primary/secondary/tertiary/
// neutral palettes in Material 3's tone layout for light or dark.
function scheme({ H, C = 0.14, H2 = H, C2 = C / 3, H3 = H + 60, C3 = C * 0.8, NH = H, NC = 0.012, NVC = 0.03, dark, surfaceTone }) {
  const p = palette(H, C), s = palette(H2, C2), t = palette(H3, C3), n = palette(NH, NC), nv = palette(NH, NVC);
  const err = palette(27, 0.19), warn = palette(75, 0.15), ok = palette(150, 0.15), info = palette(250, 0.13);
  const d = dark;
  const T = (light, darkTone) => (d ? darkTone : light);
  const surface = surfaceTone ?? T(98, 7);
  const ui = {
    "--bg": n(surface),
    "--fg": n(T(12, 91)),
    "--muted": nv(T(36, 74)),
    "--faint": nv(T(58, 50)),
    "--surface": n(T(surface - 3, surface + 4)),
    "--sidebar-bg": n(T(surface - 4, surface + 3)),
    "--sidebar-fg": n(T(12, 91)),
    "--activity-bg": n(T(surface - 6, surface + 2)),
    "--activity-fg": n(T(10, 93)),
    "--activity-dim": nv(T(44, 64)),
    "--activity-accent": p(T(42, 78)),
    "--border": nv(T(86, 24)),
    "--input-bg": n(T(100, surface + 2)),
    "--input-border": nv(T(76, 34)),
    "--btn-bg": p(T(42, 78)),
    "--btn-fg": p(T(100, 18)),
    "--btn-hover": p(T(36, 84)),
    "--btn2-bg": s(T(90, 28)),
    "--btn2-fg": s(T(14, 90)),
    "--focus": p(T(50, 72)),
    "--link": p(T(42, 80)),
    "--hover": n(T(12, 91), 0.07),
    "--select-bg": s(T(88, 30)),
    "--select-fg": s(T(10, 92)),
    "--status-bg": n(T(surface - 6, surface + 2)),
    "--status-fg": nv(T(36, 74)),
    "--status-border": nv(T(86, 24)),
    "--title-bg": n(surface),
    "--badge-bg": p(T(90, 30)),
    "--badge-fg": p(T(14, 90)),
    "--error": err(T(42, 78)),
    "--warn": warn(T(52, 80)),
    "--info": info(T(46, 78)),
    "--ok": ok(T(42, 78)),
    "--diff-add": ok(T(60, 50), 0.18),
    "--diff-del": err(T(60, 50), 0.18),
    "--code-bg": n(T(surface - 5, surface + 6)),
    "--ansi-red": err(T(46, 74)),
    "--ansi-green": ok(T(46, 76)),
    "--ansi-yellow": warn(T(56, 82)),
    "--ansi-blue": info(T(46, 74)),
    "--ansi-magenta": palette(320, 0.15)(T(46, 74)),
    "--ansi-cyan": palette(200, 0.12)(T(48, 78)),
    "--syn-comment": nv(T(54, 58)),
    "--syn-keyword": p(T(40, 78)),
    "--syn-string": ok(T(40, 80)),
    "--syn-number": t(T(44, 80)),
    "--syn-function": palette(H + 140, C)(T(40, 80)),
    "--syn-type": t(T(38, 76)),
    "--syn-variable": n(T(20, 88)),
    "--syn-property": info(T(42, 80)),
  };
  return ui;
}

// --- families ----------------------------------------------------------------

// Copilot-style: cool violet-tinted neutrals, a violet primary, a pink or
// teal second accent.
const COPILOT = [
  { id: "copilot-dark", name: "Copilot Dark", dark: true, H: 295, C: 0.17, H3: 345, NH: 285, NC: 0.03, surfaceTone: 5 },
  { id: "copilot-midnight", name: "Copilot Midnight", dark: true, H: 275, C: 0.16, H3: 200, NH: 270, NC: 0.035, surfaceTone: 9 },
  { id: "copilot-aurora", name: "Copilot Aurora", dark: true, H: 185, C: 0.12, H2: 295, C2: 0.06, H3: 300, NH: 230, NC: 0.02, surfaceTone: 8 },
  { id: "copilot-ember", name: "Copilot Ember", dark: true, H: 40, C: 0.15, H3: 330, NH: 300, NC: 0.02, surfaceTone: 6 },
  { id: "copilot-light", name: "Copilot Light", dark: false, H: 295, C: 0.19, H3: 345, NH: 280, NC: 0.01, surfaceTone: 99 },
  { id: "copilot-lavender", name: "Copilot Lavender", dark: false, H: 285, C: 0.16, H3: 200, NH: 285, NC: 0.03, surfaceTone: 97 },
];

// Material 3 seeds: the baseline purple and a few classic Android colors.
export const ANDROID_SEEDS = [
  { key: "baseline", name: "Baseline", seed: "#6750a4" },
  { key: "android", name: "Android Green", seed: "#3ddc84" },
  { key: "blue", name: "Google Blue", seed: "#0b57d0" },
  { key: "teal", name: "Teal", seed: "#006a6a" },
  { key: "coral", name: "Coral", seed: "#e8710a" },
  { key: "rose", name: "Rose", seed: "#b4446c" },
];

// Material 3 scheme from any seed color ("tonal spot": primary keeps the
// seed's hue, secondary is a muted version, tertiary is rotated 60°).
export function materialTheme(seed, dark, name, id) {
  const { C, H } = toOklch(seed);
  const c = Math.max(0.08, Math.min(0.17, C));
  return {
    id: id || `m3:${seed.toLowerCase()}:${dark ? "dark" : "light"}`,
    name: (name || "Android " + seed.toLowerCase()) + (dark ? " Dark" : " Light"),
    family: "android", seed, type: dark ? "dark" : "light", contrast: false, colorblind: false,
    ui: scheme({ H, C: c, C2: c / 3.2, H3: H + 60, C3: c * 0.75, NH: H, NC: 0.01, NVC: 0.025, dark }),
  };
}

export const PALETTE_THEMES = [
  ...COPILOT.map(({ id, name, dark, ...o }) => ({ id, name, family: "copilot", type: dark ? "dark" : "light", contrast: false, colorblind: false, ui: scheme({ dark, ...o }) })),
  ...ANDROID_SEEDS.flatMap((s) => [materialTheme(s.seed, true, s.name, `android-${s.key}-dark`), materialTheme(s.seed, false, s.name, `android-${s.key}-light`)]),
];

export const FAMILIES = [
  { id: "github", name: "GitHub", note: "Primer's GitHub VS Code themes, including colorblind and high-contrast variants." },
  { id: "copilot", name: "Copilot", note: "Dark and light themes in the style of GitHub Copilot's app: cool near-black or paper-white surfaces with violet, pink and teal accents." },
  { id: "android", name: "Android", note: "Material 3 (Material You) color schemes: tonal palettes from one seed color. Pick a preset or any color you like." },
];

// --- styles (skins) and fonts that go with the families ---------------------

// Extra skins next to the generated Workbench / Console / Paper. A skin
// changes type and shape only; colors still come from the theme.
export const EXTRA_SKINS = [
  { id: "material", name: "Material", family: "android", note: "Android's Material 3: Roboto, pill buttons, rounded cards, a navigation rail with a pill indicator." },
  { id: "copilot", name: "Copilot", family: "copilot", note: "Copilot's look: Mona Sans, soft rounded panels, gradient primary buttons, a glowing focus ring." },
];

const SYSTEM_UI = `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`;
const SYSTEM_MONO = `ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace`;

// Font choices; "" means "what the skin uses". All bundled fonts ship with
// the app (src/fonts.css), so nothing is downloaded at run time.
export const UI_FONTS = [
  { id: "", name: "Style default" },
  { id: "system", name: "System", stack: SYSTEM_UI },
  { id: "inter", name: "Inter", stack: `"Inter Variable", ${SYSTEM_UI}` },
  { id: "roboto", name: "Roboto", stack: `"Roboto Variable", Roboto, ${SYSTEM_UI}` },
  { id: "mona", name: "Mona Sans", stack: `"Mona Sans Variable", ${SYSTEM_UI}` },
];
export const CODE_FONTS = [
  { id: "", name: "Style default" },
  { id: "system", name: "System", stack: SYSTEM_MONO },
  { id: "jetbrains", name: "JetBrains Mono", stack: `"JetBrains Mono Variable", ${SYSTEM_MONO}` },
  { id: "roboto-mono", name: "Roboto Mono", stack: `"Roboto Mono Variable", ${SYSTEM_MONO}` },
];
