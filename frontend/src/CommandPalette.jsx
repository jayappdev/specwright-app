// Command palette (⌘K / Ctrl+K): go to any view, open a flow, run one, or
// toggle the sidebar labels, by typing a few letters. ⌘1…⌘9 go straight to the
// views in sidebar order. Both are ignored while another dialog is open.
import { useEffect, useMemo, useRef, useState } from "react";
import { backend } from "./backend.js";
import { S, update } from "./store.js";

let commands = () => [];
// views: [{ id, label, group }] in sidebar order, from App
export function useCommandPalette(views, toggleRail) {
  commands = () => {
    const out = views.map((v, k) => ({ id: "view:" + v.id, label: v.label, hint: v.group + (k < 9 ? ` · ⌘${k + 1}` : ""), run: () => { S.view = v.id; update(); } }));
    for (const f of S.cmdFlows || []) {
      out.push({ id: "flow:" + f.id, label: "Open flow: " + f.name, hint: "Flow designer", run: () => import("./views/Flows.jsx").then((m) => m.openFlowInDesigner(f.id)) });
      out.push({ id: "run:" + f.id, label: "Run flow: " + f.name, hint: "Run flows", run: () => import("./views/FlowRun.jsx").then((m) => m.openFlowRunner(f.id)) });
    }
    out.push({ id: "rail", label: S.railOpen ? "Hide sidebar labels" : "Show sidebar labels", hint: "Sidebar", run: toggleRail });
    return out;
  };
  useEffect(() => {
    const key = (e) => {
      const mod = e.metaKey || e.ctrlKey;
      if (!mod || e.altKey || document.querySelector('[aria-modal="true"]')) return;
      if (e.key.toLowerCase() === "k" && !e.shiftKey) { e.preventDefault(); openPalette(); return; }
      const n = +e.key;
      if (!e.shiftKey && n >= 1 && n <= 9 && views[n - 1] && S.setup.done) { e.preventDefault(); S.view = views[n - 1].id; update(); }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [views]);
}

export function openPalette() {
  S.palette = { q: "" }; update();
  if (S.ws) backend.Flows().then((l) => { S.cmdFlows = l || []; update(); }).catch(() => {});
}

export function CommandPalette() {
  const p = S.palette, [q, setQ] = useState(""), [k, setK] = useState(0), list = useRef(null);
  useEffect(() => { setQ(""); setK(0); }, [!!p]);
  const items = useMemo(() => {
    if (!p) return [];
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    return commands().filter((c) => words.every((w) => (c.label + " " + c.hint).toLowerCase().includes(w))).slice(0, 40);
  }, [p, q, S.cmdFlows]);
  useEffect(() => { list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" }); }, [k]);
  if (!p) return null;
  const close = () => { S.palette = null; update(); };
  const go = (c) => { close(); c?.run(); };
  const key = (e) => {
    if (e.key === "Escape") { e.preventDefault(); close(); }
    else if (e.key === "ArrowDown") { e.preventDefault(); setK((k + 1) % Math.max(1, items.length)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setK((k - 1 + items.length) % Math.max(1, items.length)); }
    else if (e.key === "Enter") { e.preventDefault(); go(items[k]); }
  };
  return (
    <div className="modal-scrim cmd-scrim" role="presentation" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="cmd" role="dialog" aria-modal="true" aria-label="Command palette">
        <input autoFocus className="cmd-input" role="combobox" aria-expanded="true" aria-controls="cmd-list" aria-activedescendant={items[k] ? "cmd-" + k : undefined}
          placeholder="Go to a view, open or run a flow…" value={q} onChange={(e) => { setQ(e.target.value); setK(0); }} onKeyDown={key} />
        <ul id="cmd-list" className="cmd-list" role="listbox" ref={list}>
          {items.map((c, i) => <li key={c.id} id={"cmd-" + i} role="option" aria-selected={i === k} className={i === k ? "on" : ""} onMouseEnter={() => setK(i)} onMouseDown={(e) => { e.preventDefault(); go(c); }}>
            <span>{c.label}</span><small className="muted">{c.hint}</small></li>)}
          {!items.length && <li className="muted cmd-empty">Nothing matches “{q}”.</li>}
        </ul>
        <div className="cmd-foot muted">↑↓ to choose · Enter to open · Esc to close · ⌘1…⌘9 go to a view</div>
      </div>
    </div>
  );
}
