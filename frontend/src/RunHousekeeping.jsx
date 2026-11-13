// Keeping the Playground's and History's run lists tidy: search, Active /
// Archived, groups, and selecting several runs to archive, restore or group.
// Runs are archived automatically after the days set under Appearance ›
// Housekeeping.
import { useEffect } from "react";
import { backend } from "./backend.js";
import { S, update, toast, errMsg } from "./store.js";
import { askText } from "./Dialog.jsx";

// the per-list state: hk("playground") / hk("history")
export const hk = (area) => ((S.hk ||= {})[area] ||= { view: "active", q: "", selecting: false, picked: [], collapsed: {} });

export async function loadHousekeeping() {
  if (S.housekeeping) return;
  S.housekeeping = (await backend.Housekeeping().catch(() => null)) || { playgroundDays: 0, historyDays: 0 };
  update();
}

// visibleRuns: the runs of the chosen view matching the search, by group
// (named groups first, A–Z; then the ungrouped ones)
export function visibleRuns(area, runs, text) {
  const h = hk(area), q = h.q.trim().toLowerCase();
  const list = runs.filter((r) => !!r.archived === (h.view === "archived") && (!q || (text(r) + " " + (r.group || "")).toLowerCase().includes(q)));
  const groups = new Map();
  for (const r of list) { const g = r.group || ""; if (!groups.has(g)) groups.set(g, []); groups.get(g).push(r); }
  return [...groups.entries()].sort(([a], [b]) => (!a) - (!b) || a.localeCompare(b));
}

async function act(area, runs, kind, ids) {
  const h = hk(area);
  ids = ids || h.picked;
  if (!ids.length) return toast("Select runs first.");
  try {
    if (kind === "archive" || kind === "restore") {
      const n = await backend.ArchiveRuns(ids, kind === "archive");
      for (const r of runs) if (ids.includes(r.id)) r.archived = kind === "archive";
      toast(`${kind === "archive" ? "Archived" : "Restored"} ${n} run${n === 1 ? "" : "s"}.`);
    } else {
      const known = [...new Set(runs.map((r) => r.group).filter(Boolean))];
      const name = await askText("Group", { label: `Group for ${ids.length} run${ids.length === 1 ? "" : "s"} (empty: no group)${known.length ? ". Existing: " + known.join(", ") : ""}`, value: runs.find((r) => ids.includes(r.id))?.group || "", ok: "Save" });
      if (name == null) return;
      await backend.GroupRuns(ids, name);
      for (const r of runs) if (ids.includes(r.id)) r.group = name.trim();
      toast(name.trim() ? `Put ${ids.length} run${ids.length === 1 ? "" : "s"} in “${name.trim()}”.` : "Removed from the group.");
    }
    h.picked = []; h.selecting = false; update();
  } catch (e) { toast(errMsg(e)); }
}

// The bar above a run list.
export function RunListBar({ area, runs }) {
  const h = hk(area), days = S.housekeeping?.[area === "playground" ? "playgroundDays" : "historyDays"] || 0;
  useEffect(() => { loadHousekeeping(); }, []);
  const archived = runs.filter((r) => r.archived).length;
  const set = (k, v) => { h[k] = v; update(); };
  return <div className="hk-bar">
    <input className="hk-search" type="search" value={h.q} placeholder="Search runs and groups" aria-label="Search runs" onChange={(e) => set("q", e.target.value)} />
    <div className="hk-row">
      <div className="seg hk-seg" role="group" aria-label="Show">
        <button type="button" aria-pressed={h.view === "active"} onClick={() => { set("view", "active"); h.picked = []; }}>Active</button>
        <button type="button" aria-pressed={h.view === "archived"} onClick={() => { set("view", "archived"); h.picked = []; }}>Archived{archived ? ` · ${archived}` : ""}</button></div>
      <button className="link" onClick={() => { h.selecting = !h.selecting; h.picked = []; update(); }}>{h.selecting ? "Cancel" : "Select"}</button></div>
    {h.selecting && <div className="hk-actions">
      <span className="muted">{h.picked.length} selected</span>
      {h.view === "active" ? <button className="btn secondary small" disabled={!h.picked.length} onClick={() => act(area, runs, "archive")}>Archive</button>
        : <button className="btn secondary small" disabled={!h.picked.length} onClick={() => act(area, runs, "restore")}>Restore</button>}
      <button className="btn secondary small" disabled={!h.picked.length} onClick={() => act(area, runs, "group")}>Group…</button></div>}
    <small className="muted hk-note">{days ? `Finished runs are archived after ${days} day${days === 1 ? "" : "s"}` : "Runs are not archived automatically"} · <button className="link" onClick={() => { S.view = "appearance"; S.scrollTo = "housekeeping"; update(); }}>change</button></small>
  </div>;
}

// A group heading (collapsible) for a run list.
export function RunGroup({ area, name, count, children }) {
  const h = hk(area), key = name || "\u0000", closed = !!h.collapsed[key];
  if (!name && !count) return null;
  return <div className="hk-group">
    {name ? <button className="hk-group-head" aria-expanded={!closed} onClick={() => { h.collapsed[key] = !closed; update(); }}>{closed ? "▸" : "▾"} {name} <span className="muted">· {count}</span></button>
      : <div className="hk-group-head plain muted">Not in a group · {count}</div>}
    {!closed && children}
  </div>;
}

// The checkbox on a run item while selecting.
export function PickBox({ area, id }) {
  const h = hk(area);
  if (!h.selecting) return null;
  const on = h.picked.includes(id);
  return <input type="checkbox" className="hk-pick" aria-label="Select this run" checked={on} onClick={(e) => e.stopPropagation()}
    onChange={() => { h.picked = on ? h.picked.filter((x) => x !== id) : [...h.picked, id]; update(); }} />;
}

// Archive / Restore and Group for the open run.
export function RunActions({ area, runs, run }) {
  if (!run) return null;
  return <>
    <button className="btn secondary" title="Group this run with others" onClick={() => act(area, runs, "group", [run.id])}>{run.group ? `Group: ${run.group}` : "Group…"}</button>
    <button className="btn secondary" onClick={() => act(area, runs, run.archived ? "restore" : "archive", [run.id])}>{run.archived ? "Restore" : "Archive"}</button>
  </>;
}

// Appearance › Housekeeping: after how many days runs are archived.
export function HousekeepingCard() {
  useEffect(() => { loadHousekeeping(); }, []);
  useEffect(() => { if (S.scrollTo === "housekeeping") { document.getElementById("housekeeping")?.scrollIntoView({ block: "start" }); S.scrollTo = ""; } }, []);
  const s = S.housekeeping;
  if (!s) return null;
  const save = async () => {
    try { S.housekeeping = await backend.SaveHousekeeping({ playgroundDays: +s.playgroundDays || 0, historyDays: +s.historyDays || 0 }); toast(S.housekeeping.lastArchived ? `Saved. Archived ${S.housekeeping.lastArchived} run${S.housekeeping.lastArchived === 1 ? "" : "s"} now.` : "Saved."); for (const a of ["playground", "history"]) hk(a); S.pg && (S.pg.loaded = false); update(); }
    catch (e) { toast(errMsg(e)); }
  };
  const days = (k, label) => <label className="field"><span className="muted">{label}</span>
    <div className="hk-days"><input type="number" min="0" max="3650" value={s[k] ?? 0} onChange={(e) => { s[k] = e.target.value; update(); }} /><span className="muted">days after a run ends (0: never)</span></div></label>;
  return <div className="card" id="housekeeping"><h3>Housekeeping</h3>
    <p className="muted" style={{ margin: "0 0 8px" }}>Archive finished runs automatically to keep the lists short. Archived runs are hidden, not deleted: open <b>Archived</b> above a list to find or restore them. Running runs are never archived.</p>
    <div className="two-col">{days("playgroundDays", "Playground prompts")}{days("historyDays", "History (workflow steps and flow runs)")}</div>
    <div className="row-actions"><button className="btn primary" onClick={save}>Save and apply now</button>
      {s.lastRun && <span className="muted">Last checked {new Date(s.lastRun).toLocaleString()}{s.lastArchived ? ` · archived ${s.lastArchived}` : ""}. Checked again every 6 hours while the app is open.</span>}</div>
  </div>;
}
