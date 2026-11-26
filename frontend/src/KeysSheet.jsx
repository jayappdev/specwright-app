// The keyboard shortcuts sheet: press ? (outside a text field) to see every
// shortcut that works right now: the app's, then the current screen's.
// Built from the shortcut registry (shortcuts.js), so it is never out of date.
import { useEffect, useRef } from "react";
import { S, update } from "./store.js";
import { listShortcuts } from "./shortcuts.js";
import { Icon } from "./ui.jsx";

export function openKeysSheet() {
  S.keysSheet = true;
  update();
}
const close = () => {
  S.keysSheet = false;
  update();
};

// a few the registry cannot list: browser-level keys every screen has
const ALWAYS = [
  ["Tab / Shift+Tab", "Move between controls"],
  ["Esc", "Close a dialog, menu or panel"],
  ["?", "This list"],
];

export function KeysSheet({ viewLabel }) {
  const box = useRef(null);
  useEffect(() => {
    if (!S.keysSheet) return undefined;
    setTimeout(() => box.current?.querySelector("button")?.focus(), 0);
    const key = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
    };
    window.addEventListener("keydown", key, true);
    return () => window.removeEventListener("keydown", key, true);
  }, [S.keysSheet]);
  if (!S.keysSheet) return null;
  const all = listShortcuts().filter((s) => !/^Shortcut/.test(s.label));
  const seen = new Set();
  const uniq = (l) =>
    l.filter((s) => {
      const k = s.keys + s.label;
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  const groups = [
    ["Anywhere", uniq(all.filter((s) => s.scope === "global"))],
    [viewLabel || "This screen", uniq(all.filter((s) => s.scope !== "global" && s.scope !== "dialog"))],
  ].filter(([, l]) => l.length);
  return (
    <div className="modal-backdrop dlg-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="dlg keys-sheet" role="dialog" aria-modal="true" aria-labelledby="keys-title" ref={box}>
        <header className="keys-head">
          <h3 id="keys-title">Keyboard shortcuts</h3>
          <button className="icon-btn" aria-label="Close" onClick={close}>
            <Icon.x />
          </button>
        </header>
        <div className="keys-cols">
          {groups.map(([name, list]) => (
            <section key={name}>
              <h4>{name}</h4>
              <dl>
                {list.map((s) => (
                  <div key={s.keys + s.label}>
                    <dt>{s.label}</dt>
                    <dd>
                      <kbd>{s.keys}</kbd>
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
          <section>
            <h4>Everywhere</h4>
            <dl>
              {ALWAYS.map(([k, l]) => (
                <div key={k}>
                  <dt>{l}</dt>
                  <dd>
                    <kbd>{k}</kbd>
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        </div>
      </div>
    </div>
  );
}
