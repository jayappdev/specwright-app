// Keyboard shortcuts: one keydown listener for the whole app.
//
// Views and features register handlers instead of adding their own window
// listeners, so the rules are the same everywhere:
// - while a modal dialog is open, only handlers with scope "dialog" run (the
//   dialog's own keys, Tab and Escape, are handled by the dialog itself);
// - keys typed into a field are left alone unless the handler says
//   `inFields: true` (⌘S should still save while you type; arrows should not
//   move a flow step);
// - handlers of the current view run before global ones, newest first; the
//   first that handles the key (does not return false) wins.
//
// register({ id, scope, keys, label, help, inFields, run }) returns an unregister
// function. `keys` is a list like ["mod+k"], ["mod+shift+z"], ["arrowleft"],
// [" "], or null to see every key (run(e) then returns false for keys it does
// not take). `scope` is "global", "dialog", or a view id ("flows"). `help`
// lists [keys, label] pairs for a handler that sees every key.
import { S } from "./store.js";

const handlers = [];
let installed = false;

const isMac =
  typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

export function keyName(e) {
  const k = e.key.length === 1 ? e.key.toLowerCase() : e.key.toLowerCase();
  return [e.metaKey || e.ctrlKey ? "mod" : "", e.altKey ? "alt" : "", e.shiftKey ? "shift" : "", k]
    .filter(Boolean)
    .join("+");
}

const typing = (t) =>
  !!t?.closest?.(
    "input:not([type=range]):not([type=checkbox]):not([type=radio]), textarea, select, [contenteditable=true]",
  );

function onKeyDown(e) {
  const modal = !!document.querySelector('[aria-modal="true"]');
  const inField = typing(e.target),
    name = keyName(e);
  const order = [...handlers].reverse().sort((a, b) => rank(a) - rank(b));
  for (const h of order) {
    if (modal ? h.scope !== "dialog" : h.scope === "dialog") continue;
    if (h.scope !== "global" && h.scope !== "dialog" && h.scope !== S.view) continue;
    if (inField && !h.inFields) continue;
    if (h.keys && !h.keys.includes(name)) continue;
    if (h.run(e) !== false) return;
  }
}
// the current view first, then global
const rank = (h) => (h.scope === "global" ? 1 : 0);

export function register(h) {
  if (!installed && typeof window !== "undefined") {
    window.addEventListener("keydown", onKeyDown);
    installed = true;
  }
  const entry = { scope: "global", inFields: false, keys: null, ...h };
  handlers.push(entry);
  return () => {
    const i = handlers.indexOf(entry);
    if (i >= 0) handlers.splice(i, 1);
  };
}

// shortcut labels for help: [{ keys: "⌘K", label, scope }]
export function listShortcuts() {
  const pretty = (k) =>
    k
      .split("+")
      .map(
        (p) =>
          ({
            mod: isMac ? "⌘" : "Ctrl+",
            shift: isMac ? "⇧" : "Shift+",
            alt: isMac ? "⌥" : "Alt+",
            " ": "Space",
            arrowleft: "←",
            arrowright: "→",
            arrowup: "↑",
            arrowdown: "↓",
            escape: "Esc",
          })[p] ?? p.toUpperCase(),
      )
      .join("");
  const out = [];
  for (const h of handlers) {
    if (h.label && h.keys) out.push({ keys: h.keys.map(pretty).join(" / "), label: h.label, scope: h.scope });
    for (const [k, label] of h.help || []) out.push({ keys: pretty(k), label, scope: h.scope });
  }
  return out;
}
