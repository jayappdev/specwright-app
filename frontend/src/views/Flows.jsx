// Flow designer: string tools together into a flow.
//   left      the project's flows (new, import)
//   palette   step types (AI prompt, merge, transform, output) and tools; drag onto the canvas
//   canvas    steps as cards; drag a card to move it, drag from its ● port to another card to connect
//   inspector the selected step, or the flow (name, context, skills) when nothing is selected
// Flows are saved to .codegen/flows/<id>.json (autosave). Running them comes with the executor.
import { useEffect, useRef, useState } from "react";
import { openFlowRunner } from "./FlowRun.jsx";
import { confirmDialog, askText } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, saveText, refreshExtensions } from "../store.js";
import { Icon } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { ToolIcon } from "../ToolTile.jsx";
import { AttachBar, AttachmentChips, onPasteAttach } from "../Attachments.jsx";
import { FileLink } from "../FileLinks.jsx";

const NODE_W = 216, PORT_Y = 30, GRID = 10;
const snap = (v) => Math.round(v / GRID) * GRID;
const clone = (x) => JSON.parse(JSON.stringify(x));

const STEP_TYPES = [
  { type: "ai", name: "AI prompt", help: "Ask the agent; uses the flow's context and skills" },
  { type: "merge", name: "Merge", help: "Combine several steps' outputs" },
  { type: "transform", name: "Transform", help: "Reshape data: JSON, CSV, text, Markdown → HTML, AI" },
  { type: "output", name: "Output", help: "md, txt, html, csv or json, then save, call a tool, copy or show" },
];
const FORMATS = [["md", "Markdown"], ["txt", "Text"], ["html", "HTML"], ["csv", "CSV"], ["json", "JSON"]];
const INPUT_TYPES = [["text", "Text"], ["multiline", "Long text"], ["number", "Number"], ["boolean", "Yes / no"], ["choice", "Choice"], ["file", "File"], ["files", "Files"], ["clipboard", "Clipboard"]];
const MERGE_MODES = [["concat", "Join as text"], ["json-object", "JSON object (one key per step)"], ["json-array", "JSON array"], ["template", "Template"]];

const fl = () => (S.fl ||= { list: null, id: "", flow: null, issues: [], sel: null, selEdge: null, dirty: false, saving: false, zoom: 1, ops: null, samples: {}, preview: {}, q: "" });

// --- step icons --------------------------------------------------------------------

const sv = (d) => function I() { return <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{d}</svg>; };
const TYPE_ICON = {
  start: { color: "--ok", Icon: sv(<><path d="M4 12h11" /><path d="M11 7l5 5-5 5" /><path d="M19 4v16" /></>) },
  ai: { color: "--btn-bg", Icon: sv(<><path d="M12 3l1.8 4.9L18.7 9.7 13.8 11.5 12 16.4 10.2 11.5 5.3 9.7 10.2 7.9z" /><path d="M18.5 15.5l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" /></>) },
  merge: { color: "--ansi-magenta", Icon: sv(<><path d="M5 4v4c0 3 7 4 7 8v4M19 4v4c0 3-7 4-7 8" /></>) },
  transform: { color: "--ansi-cyan", Icon: sv(<><path d="M4 7h11l-3-3M20 17H9l3 3" /></>) },
  output: { color: "--warn", Icon: sv(<><path d="M12 3v12M7 10l5 5 5-5" /><path d="M4 17v3h16v-3" /></>) },
};

export function StepIcon({ n, tools, size = 34 }) {
  if (n.type === "tool") {
    const t = tools.find((x) => x.name === n.tool) || { name: n.tool || "tool", desc: "" };
    return <ToolIcon t={t} size={size} />;
  }
  const s = TYPE_ICON[n.type] || TYPE_ICON.ai;
  return <span className="tool-icon" style={{ "--tc": `var(${s.color})`, width: size, height: size }}><s.Icon /></span>;
}

// --- loading and saving -------------------------------------------------------------

async function loadList() {
  const f = fl();
  try { f.list = await backend.Flows(); } catch (e) { f.list = []; toast(errMsg(e)); }
  if (!f.ops) f.ops = await backend.FlowOps().catch(() => null);
  update();
}

// Open a flow in the designer from another screen.
export function openFlowInDesigner(id) { fl().id = ""; fl().flow = null; S.view = "flows"; openFlow(id); }

async function openFlow(id) {
  const f = fl();
  if (f.dirty) await save();
  try {
    const flow = await backend.LoadFlow(id);
    Object.assign(f, { id, flow, sel: null, selEdge: null, dirty: false, preview: {} });
    f.issues = (await backend.ValidateFlow(flow).catch(() => [])) || [];
  } catch (e) { toast(errMsg(e)); }
  update();
}

let timer = 0;
function changed() {
  const f = fl();
  f.dirty = true; update();
  clearTimeout(timer);
  timer = setTimeout(save, 700);
}

async function save() {
  const f = fl();
  if (!f.flow || f.saving) return;
  clearTimeout(timer);
  f.saving = true; update();
  try {
    const st = await backend.SaveFlow(f.flow);
    f.issues = st.issues || []; f.dirty = false;
    f.flow.updated = st.flow.updated;
    const s = (f.list || []).find((x) => x.id === f.flow.id);
    if (s) { s.name = f.flow.name; s.updated = st.flow.updated; }
  } catch (e) { toast(errMsg(e)); }
  f.saving = false; update();
}

async function newFlow() {
  const name = await askText("New flow", { label: "Name", value: "New flow", ok: "Create" });
  if (name == null) return;
  try { const flow = await backend.NewFlow(name); await loadList(); await openFlow(flow.id); }
  catch (e) { toast(errMsg(e)); }
}

async function importFlow(file) {
  try {
    const st = await backend.ImportFlow(await file.text());
    await loadList(); await openFlow(st.flow.id);
    toast(`Imported ${st.flow.name}.` + (st.notes?.length ? " " + st.notes.join(" · ") : ""));
  } catch (e) { toast(errMsg(e)); }
}

async function exportFlow() {
  const f = fl();
  if (f.dirty) await save();
  try { saveText(`${f.flow.id}.flow.json`, await backend.ExportFlow(f.flow.id), "application/json"); }
  catch (e) { toast(errMsg(e)); }
}

async function deleteFlow() {
  const f = fl();
  if (!(await confirmDialog(`This removes .codegen/flows/${f.flow.id}.json from the project.`, { title: `Delete the flow ${f.flow.name}?`, ok: "Delete", danger: true }))) return;
  try { await backend.DeleteFlow(f.flow.id); Object.assign(f, { id: "", flow: null, sel: null, dirty: false }); clearTimeout(timer); await loadList(); }
  catch (e) { toast(errMsg(e)); }
}

async function duplicateFlow() {
  const f = fl();
  if (f.dirty) await save();
  const copy = { ...clone(f.flow), id: "", name: f.flow.name + " (copy)" };
  try { const st = await backend.SaveFlow(copy); await loadList(); await openFlow(st.flow.id); } catch (e) { toast(errMsg(e)); }
}

// --- graph helpers ---------------------------------------------------------------------

const nodeOf = (id) => fl().flow?.nodes.find((n) => n.id === id);
const parentsOf = (flow, id) => flow.edges.filter((e) => e.to === id).map((e) => e.from);
function ancestors(flow, id) {
  const seen = new Set(), walk = (n) => parentsOf(flow, n).forEach((p) => { if (!seen.has(p)) { seen.add(p); walk(p); } });
  walk(id); return seen;
}
const tools = () => (S.ws?.extensions || []).filter((t) => !t.managed || t.managed.status === "verified");

function uniqueId(base) {
  const ids = new Set(fl().flow.nodes.map((n) => n.id));
  base = (base || "step").replace(/[^A-Za-z0-9_-]+/g, "_").replace(/^[^A-Za-z]+/, "") || "step";
  let id = base, k = 2;
  while (ids.has(id)) id = base + k++;
  return id;
}

function addNode(spec, x, y) {
  const f = fl(), flow = f.flow;
  const right = flow.nodes.reduce((m, n) => Math.max(m, n.x), 40);
  const n = { id: uniqueId(spec.type === "tool" ? spec.tool : spec.type), type: spec.type, title: spec.type === "tool" ? spec.tool : STEP_TYPES.find((t) => t.type === spec.type)?.name, x: snap(x ?? right + NODE_W + 60), y: snap(y ?? 120) };
  if (spec.type === "tool") { n.tool = spec.tool; n.args = {}; const t = tools().find((x) => x.name === spec.tool); (t?.params || []).forEach((p) => { n.args[p.name] = ""; }); }
  if (spec.type === "ai") n.prompt = "";
  if (spec.type === "merge") { n.mode = "concat"; n.separator = "\\n\\n"; }
  if (spec.type === "transform") n.op = "json-path";
  if (spec.type === "output") { n.format = "md"; n.actions = [{ type: "show" }]; }
  // connect from the selected step (or the inputs) so the new step is wired up
  const from = f.sel && nodeOf(f.sel)?.type !== "output" ? f.sel : x == null ? "start" : null;
  flow.nodes.push(n);
  if (from) { flow.edges.push({ from, to: n.id }); const note = autoWire(from, n); if (note && from !== "start") toast(note); }
  f.sel = n.id; f.selEdge = null;
  changed();
}

// When a step is connected into a tool, pass its output into the tool's best
// matching empty argument (from Inputs: arguments named like an input).
// Returns a short note for the user, or "".
const DATA_PARAM = /input|text|data|content|body|json|payload|items|list|query|sql|file|message|value|source|doc/i;
// fields that expect a file path rather than the text itself
export const FILE_PARAM = (p) => /file/i.test(p.name) && !/name$/i.test(p.name);
const feedFor = (p, from) => (FILE_PARAM(p) ? "{{input_file}}" : `{{steps.${from}}}`);
function autoWire(from, n) {
  if (n.type !== "tool" || (n.instruction || "").trim()) return "";
  const t = tools().find((x) => x.name === n.tool), a = nodeOf(from);
  if (!t || !a) return "";
  const args = (n.args ||= {}), empty = (t.params || []).filter((p) => !(args[p.name] || "").trim());
  if (a.type === "start") {
    const done = [];
    for (const p of empty) { const i = fl().flow.inputs.find((x) => x.name.toLowerCase() === p.name.toLowerCase()); if (i) { args[p.name] = `{{inputs.${i.name}}}`; done.push(p.name); } }
    return done.length ? `Passing the inputs into ${done.join(", ")} of ${n.title || n.tool}.` : "";
  }
  const pick = empty.find((p) => p.required && DATA_PARAM.test(p.name)) || empty.find((p) => DATA_PARAM.test(p.name)) || empty.find((p) => p.required);
  if (pick) { args[pick.name] = feedFor(pick, from); return FILE_PARAM(pick) ? `“${pick.name}” of ${n.title || n.tool} gets a file with the result of ${a.title || a.id}.` : `Passing the result of ${a.title || a.id} into “${pick.name}” of ${n.title || n.tool}.`; }
  return `${n.title || n.tool} gets the result of ${a.title || a.id} automatically (piped in).`;
}

function connect(from, to) {
  const f = fl(), flow = f.flow;
  if (!from || !to || from === to) return;
  const a = nodeOf(from), b = nodeOf(to);
  if (!a || !b) return;
  if (b.type === "start") return toast("Nothing can feed the Inputs step.");
  if (a.type === "output") return toast("An output is the end of a path.");
  if (flow.edges.some((e) => e.from === from && e.to === to)) return;
  if (ancestors(flow, from).has(to)) return toast("That would make a loop.");
  flow.edges.push({ from, to });
  const note = autoWire(from, b);
  if (note) toast(note);
  changed();
}

function removeSelected() {
  const f = fl(), flow = f.flow;
  if (f.selEdge != null) { flow.edges.splice(f.selEdge, 1); f.selEdge = null; return changed(); }
  const n = nodeOf(f.sel);
  if (!n || n.type === "start") return;
  flow.nodes = flow.nodes.filter((x) => x.id !== n.id);
  flow.edges = flow.edges.filter((e) => e.from !== n.id && e.to !== n.id);
  f.sel = null; changed();
}

// rename a step and every {{steps.<id>…}} that refers to it
function renameNode(n, id) {
  const flow = fl().flow;
  id = id.replace(/[^A-Za-z0-9_-]/g, "");
  if (!id || !/^[A-Za-z]/.test(id) || flow.nodes.some((x) => x.id === id && x !== n)) return toast("Step ids start with a letter and must be unique.");
  const re = new RegExp("\\{\\{\\s*steps\\." + n.id.replace(/[-]/g, "\\-") + "(?=[.}\\s])", "g"), rep = "{{steps." + id;
  const fix = (s) => (typeof s === "string" ? s.replace(re, rep) : s);
  for (const x of flow.nodes) {
    for (const k of ["prompt", "template", "source", "expr"]) if (x[k]) x[k] = fix(x[k]);
    if (x.args) for (const k in x.args) x.args[k] = fix(x.args[k]);
    for (const a of x.actions || []) { a.path = fix(a.path); if (a.args) for (const k in a.args) a.args[k] = fix(a.args[k]); }
  }
  flow.edges.forEach((e) => { if (e.from === n.id) e.from = id; if (e.to === n.id) e.to = id; });
  if (fl().sel === n.id) fl().sel = id;
  n.id = id; changed();
}

// --- variable chips: insert {{…}} where the cursor is ---------------------------------

let lastField = null;
const track = { onFocus: (e) => { lastField = e.target; } };
function insertVar(v) {
  const el = lastField;
  if (!el || !document.body.contains(el)) return toast("Click in a field first, then pick a variable.");
  const text = `{{${v}}}`, s = el.selectionStart ?? el.value.length, e = el.selectionEnd ?? s;
  const val = el.value.slice(0, s) + text + el.value.slice(e);
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, val);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.focus(); el.setSelectionRange(s + text.length, s + text.length);
}

function VarChips({ node, extra = [] }) {
  const f = fl(), flow = f.flow;
  const anc = node ? [...ancestors(flow, node.id)].filter((id) => id !== "start") : [];
  const fed = node && parentsOf(flow, node.id).some((p) => p !== "start");
  const vars = [...flow.inputs.map((i) => ["inputs." + i.name, i.label || i.name]), ...anc.map((id) => ["steps." + id, nodeOf(id)?.title || id]), ...(fed && node.type === "tool" ? [["input_file", "previous result, as a file"]] : []), ["context", "context"], ["skills", "skills"], ...extra.map((v) => [v, v])];
  return <div className="fl-vars"><span className="muted">Insert:</span>{vars.map(([v, l]) => <button key={v} type="button" className="fl-var" title={`{{${v}}}`} onMouseDown={(e) => e.preventDefault()} onClick={() => insertVar(v)}>{l}</button>)}</div>;
}

// What an edge carries, shown on the connection: the arguments of the
// target that read the source, "AI" for a described step, "stdin" for a tool
// that takes it on standard input only.
function edgeLabel(a, b) {
  if (b.type !== "tool") return "";
  const re = new RegExp("\\{\\{\\s*" + (a.type === "start" ? "inputs\\." : "(steps\\." + a.id.replace(/[-]/g, "\\-") + "|input_file|input)") + "(?=[.}\\s])");
  const used = Object.entries(b.args || {}).filter(([, v]) => re.test(v)).map(([k]) => k);
  if (used.length) return "→ " + used.join(", ");
  if ((b.instruction || "").trim()) return "→ AI";
  return a.type === "start" ? "" : "→ piped in";
}

// --- canvas ------------------------------------------------------------------------------

function Canvas() {
  const f = fl(), flow = f.flow, box = useRef(null), [drag, setDrag] = useState(null), [link, setLink] = useState(null);
  const ts = tools();
  const z = f.zoom;
  const toLocal = (e) => { const r = box.current.getBoundingClientRect(); return { x: (e.clientX - r.left + box.current.scrollLeft) / z, y: (e.clientY - r.top + box.current.scrollTop) / z }; };
  useEffect(() => {
    if (!drag && !link) return;
    const move = (e) => {
      if (drag) { const p = toLocal(e); const n = nodeOf(drag.id); if (n) { n.x = Math.max(0, snap(p.x - drag.dx)); n.y = Math.max(0, snap(p.y - drag.dy)); drag.moved = true; update(); } }
      if (link) setLink({ ...link, ...toLocal(e) });
    };
    const up = (e) => {
      if (drag?.moved) changed();
      if (link) { const el = document.elementFromPoint(e.clientX, e.clientY)?.closest("[data-node]"); if (el) connect(link.from, el.dataset.node); }
      setDrag(null); setLink(null);
    };
    window.addEventListener("mousemove", move); window.addEventListener("mouseup", up);
    return () => { window.removeEventListener("mousemove", move); window.removeEventListener("mouseup", up); };
  }, [drag, link]);
  useEffect(() => {
    const key = (e) => {
      if (e.target?.closest?.("input, textarea, select, [contenteditable=true]") || document.querySelector(".drawer-scrim, .file-preview")) return;
      if ((e.key === "Delete" || e.key === "Backspace") && (f.sel || f.selEdge != null)) { e.preventDefault(); removeSelected(); }
      if (e.key === "Escape") { f.sel = null; f.selEdge = null; update(); }
    };
    window.addEventListener("keydown", key); return () => window.removeEventListener("keydown", key);
  }, []);
  const wheel = (e) => { if (!e.ctrlKey && !e.metaKey) return; e.preventDefault(); f.zoom = Math.min(1.6, Math.max(0.4, +(z - e.deltaY * 0.002).toFixed(2))); update(); };
  const issuesFor = (id) => f.issues.filter((i) => i.node === id);
  const W = Math.max(1400, ...flow.nodes.map((n) => n.x + NODE_W + 400)), H = Math.max(800, ...flow.nodes.map((n) => n.y + 400));
  const port = (n, out) => ({ x: n.x + (out ? NODE_W : 0), y: n.y + PORT_Y });
  const path = (a, b) => { const dx = Math.max(40, Math.abs(b.x - a.x) / 2); return `M${a.x},${a.y} C${a.x + dx},${a.y} ${b.x - dx},${b.y} ${b.x},${b.y}`; };
  return (
    <div className="fl-canvas" ref={box} onWheel={wheel}
      onDragOver={(e) => { if ([...e.dataTransfer.types].includes("application/x-flow-step")) e.preventDefault(); }}
      onDrop={(e) => { const spec = e.dataTransfer.getData("application/x-flow-step"); if (!spec) return; e.preventDefault(); const p = toLocal(e); f.sel = null; addNode(JSON.parse(spec), p.x - NODE_W / 2, p.y - 24); }}
      onMouseDown={(e) => { if (e.target === box.current || e.target.classList.contains("fl-plane")) { f.sel = null; f.selEdge = null; update(); } }}>
      <div className="fl-plane" style={{ width: W, height: H, transform: `scale(${z})` }}>
        <svg className="fl-edges" width={W} height={H}>
          {flow.edges.map((e, k) => { const a = nodeOf(e.from), b = nodeOf(e.to); if (!a || !b) return null; const d = path(port(a, true), port(b, false)); return <g key={k} className={"fl-edge" + (f.selEdge === k ? " sel" : "")} onMouseDown={(ev) => { ev.stopPropagation(); f.selEdge = k; f.sel = null; update(); }}><path d={d} className="hit" /><path d={d} className="line" markerEnd="url(#fl-arrow)" />{(() => { const lab = edgeLabel(a, b); if (!lab) return null; const p1 = port(a, true), p2 = port(b, false); return <text x={(p1.x + p2.x) / 2} y={(p1.y + p2.y) / 2 - 6} className="fl-edge-label" textAnchor="middle">{lab}</text>; })()}</g>; })}
          {link && (() => { const a = nodeOf(link.from); return a && link.x != null ? <path d={path(port(a, true), { x: link.x, y: link.y })} className="fl-linking" /> : null; })()}
          <defs><marker id="fl-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" /></marker></defs>
        </svg>
        {flow.nodes.map((n) => {
          const is = issuesFor(n.id), err = is.some((i) => i.level === "error");
          const sub = n.type === "tool" ? (n.instruction?.trim() ? "✦ " + n.instruction.trim().slice(0, 44) : n.tool) : n.type === "start" ? `${flow.inputs.length} input${flow.inputs.length === 1 ? "" : "s"}` : n.type === "transform" ? (f.ops?.ops?.[n.op]?.name || n.op) : n.type === "merge" ? (MERGE_MODES.find(([m]) => m === n.mode)?.[1] || n.mode) : n.type === "output" ? (n.format || "").toUpperCase() + " · " + (n.actions || []).map((a) => a.type).join(", ") : n.type === "ai" ? (n.prompt || "").slice(0, 40) || "No prompt yet" : "";
          return (
            <div key={n.id} data-node={n.id} className={"fl-node t-" + n.type + (f.sel === n.id ? " sel" : "") + (err ? " has-err" : is.length ? " has-warn" : "") + (link ? " linking" : "")}
              style={{ left: n.x, top: n.y, width: NODE_W }}
              onMouseDown={(e) => { if (e.button !== 0 || e.target.closest(".fl-port")) return; const p = toLocal(e); f.sel = n.id; f.selEdge = null; update(); setDrag({ id: n.id, dx: p.x - n.x, dy: p.y - n.y }); }}>
              {n.type !== "start" && <span className="fl-port in" title="Input" />}
              <div className="fl-node-head"><StepIcon n={n} tools={ts} /><div className="fl-node-text"><b>{n.title || n.id}</b><small>{sub}</small></div>
                {is.length > 0 && <span className={"fl-badge " + (err ? "err" : "warn")} title={is.map((i) => i.message).join("\n")}>{is.length}</span>}</div>
              {n.type === "start" && flow.inputs.length > 0 && <div className="fl-node-body">{flow.inputs.slice(0, 5).map((i) => <code key={i.name}>{i.name}</code>)}{flow.inputs.length > 5 && <span className="muted">+{flow.inputs.length - 5}</span>}</div>}
              {n.type !== "output" && <span className="fl-port out" title="Drag to connect" onMouseDown={(e) => { e.stopPropagation(); e.preventDefault(); setLink({ from: n.id, ...toLocal(e) }); }} />}
            </div>
          );
        })}
      </div>
      {flow.nodes.length === 1 && <div className="fl-hint"><b>Start by dragging a tool or step here</b><span>Drag from a step's ● to another step to connect them. Delete removes the selected step or connection.</span></div>}
      <div className="fl-zoom"><button className="icon-btn" aria-label="Zoom out" onClick={() => { f.zoom = Math.max(0.4, +(z - 0.1).toFixed(2)); update(); }}><Icon.minus /></button>
        <button className="fv-pct" onClick={() => { f.zoom = 1; update(); }}>{Math.round(z * 100)}%</button>
        <button className="icon-btn" aria-label="Zoom in" onClick={() => { f.zoom = Math.min(1.6, +(z + 0.1).toFixed(2)); update(); }}><Icon.plus /></button></div>
    </div>
  );
}

// --- palette ---------------------------------------------------------------------------

function Palette() {
  const f = fl(), ts = tools().filter((t) => !f.q || (t.name + " " + t.desc).toLowerCase().includes(f.q.toLowerCase()));
  const item = (spec, label, sub, icon) => (
    <div key={label} className="fl-pal-item" draggable title={sub} role="button" tabIndex={0}
      onDragStart={(e) => { e.dataTransfer.setData("application/x-flow-step", JSON.stringify(spec)); e.dataTransfer.effectAllowed = "copy"; }}
      onClick={() => addNode(spec)} onKeyDown={(e) => e.key === "Enter" && addNode(spec)}>
      {icon}<span><b>{label}</b><small>{sub}</small></span></div>
  );
  return (
    <aside className="fl-palette" aria-label="Steps and tools">
      <div className="tree-folder">Steps</div>
      {STEP_TYPES.map((s) => item({ type: s.type }, s.name, s.help, <StepIcon n={{ type: s.type }} tools={[]} size={30} />))}
      <div className="tree-folder">Tools · {tools().length}</div>
      <input className="fl-search" placeholder="Filter tools" value={f.q} onChange={(e) => { f.q = e.target.value; update(); }} />
      {ts.map((t) => item({ type: "tool", tool: t.name }, t.name, t.desc, <ToolIcon t={t} size={30} />))}
      {!tools().length && <p className="muted" style={{ padding: "0 6px" }}>No tools yet. Create one on the Tools screen.</p>}
      <p className="muted fl-pal-hint">Drag onto the canvas, or click to add after the selected step.</p>
    </aside>
  );
}

// --- inspector ----------------------------------------------------------------------------

const Field = ({ label, hint, children }) => <label className="field fl-field"><span className="muted">{label}</span>{children}{hint && <small className="muted">{hint}</small>}</label>;

function FlowSettings() {
  const f = fl(), flow = f.flow;
  const ctxFiles = useRef(null), skillFiles = useRef(null);
  ctxFiles.current ||= (flow.context.files || []).map((p) => ({ path: p, name: p.split("/").pop(), kind: /\.(png|jpe?g|gif|webp)$/i.test(p) ? "image" : /\.pdf$/i.test(p) ? "pdf" : "text", size: 0 }));
  // keep the attachment list and the flow's paths in sync
  useEffect(() => { const paths = ctxFiles.current.filter((a) => a.path && !a.saving).map((a) => a.path); if (JSON.stringify(paths) !== JSON.stringify(flow.context.files || [])) { flow.context.files = paths; changed(); } });
  const skills = (S.ws.libraries || []).filter((l) => l.kind === "skill");
  const has = (p) => flow.skills.some((s) => s.path === p);
  const toggle = (l) => { flow.skills = has(l.path) ? flow.skills.filter((s) => s.path !== l.path) : [...flow.skills, { path: l.path, name: l.name }]; changed(); };
  skillFiles.current ||= [];
  useEffect(() => { for (const a of skillFiles.current) if (a.path && !a.saving && !has(a.path)) { flow.skills.push({ path: a.path, name: a.name.replace(/\.md$/, "") }); changed(); } });
  return <>
    <h3>Flow</h3>
    <Field label="Name"><input value={flow.name} onChange={(e) => { flow.name = e.target.value; changed(); }} /></Field>
    <Field label="Description"><textarea rows={2} value={flow.description || ""} onChange={(e) => { flow.description = e.target.value; changed(); }} /></Field>
    <Field label="Manual work one run replaces (minutes)" hint="Counted as hours saved in Metrics when a run completes. Empty: estimated from the steps."><input type="number" min="0" value={flow.minutesSaved || ""} placeholder="estimate" onChange={(e) => { flow.minutesSaved = +e.target.value || 0; changed(); }} /></Field>
    <h4>Custom context</h4>
    <p className="muted fl-help">Given to every AI step (and available as {"{{context}}"}): background, conventions, constraints.</p>
    <textarea rows={6} value={flow.context.text || ""} placeholder="e.g. We build Java 21 Spring services. Specs follow the team template; keep them under two pages." {...track} onPaste={onPasteAttach(ctxFiles.current)} onChange={(e) => { flow.context.text = e.target.value; changed(); }} />
    <AttachBar list={ctxFiles.current} insertText={(t) => { flow.context.text = (flow.context.text ? flow.context.text + "\n" : "") + t; changed(); }} />
    <h4>Skills</h4>
    <p className="muted fl-help">Skill files the AI steps follow ({"{{skills}}"}).</p>
    <ul className="fl-checks">{skills.map((l) => <li key={l.id}><label className="check"><input type="checkbox" checked={has(l.path)} onChange={() => toggle(l)} /> <b>{l.name}</b> <span className="muted">{l.scope === "global" ? "corporate" : "project"}</span></label></li>)}
      {flow.skills.filter((s) => !skills.some((l) => l.path === s.path)).map((s) => <li key={s.path}><label className="check"><input type="checkbox" checked onChange={() => { flow.skills = flow.skills.filter((x) => x.path !== s.path); changed(); }} /> <FileLink path={s.path}>{s.name || s.path}</FileLink> <span className="muted">attached</span></label></li>)}
      {!skills.length && !flow.skills.length && <li className="muted">No skills in this project yet.</li>}</ul>
    <AttachBar list={skillFiles.current} />
    <p className="muted fl-help">Attach a skill file (.md) that is not in the project's libraries; it is saved with the project and embedded when you export the flow.</p>
  </>;
}

function InputsEditor() {
  const flow = fl().flow;
  const files = useRef({});
  const add = () => { let k = flow.inputs.length + 1, name = "input" + k; while (flow.inputs.some((i) => i.name === name)) name = "input" + ++k; flow.inputs.push({ name, label: "Input " + k, type: "text", required: false }); changed(); };
  return <>
    <h3>Inputs</h3>
    <p className="muted fl-help">What the flow asks for when it runs. Steps use them as {"{{inputs.name}}"}.</p>
    {flow.inputs.map((i, k) => {
      const fl_ = (files.current[i.name] ||= (i.files || []).map((p) => ({ path: p, name: p.split("/").pop(), kind: /\.pdf$/i.test(p) ? "pdf" : /\.(png|jpe?g|gif|webp)$/i.test(p) ? "image" : "text", size: 0 })));
      const sync = () => { const paths = fl_.filter((a) => a.path && !a.saving).map((a) => a.path); if (JSON.stringify(paths) !== JSON.stringify(i.files || [])) { i.files = paths; changed(); } };
      setTimeout(sync, 0);
      return (
        <div key={k} className="fl-input">
          <div className="fl-row">
            <Field label="Name"><input value={i.name} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => { i.name = e.target.value.replace(/[^A-Za-z0-9_]/g, ""); changed(); }} /></Field>
            <Field label="Type"><select value={i.type} onChange={(e) => { i.type = e.target.value; changed(); }}>{INPUT_TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
            <button className="icon-btn fl-del" title="Remove input" onClick={() => { flow.inputs.splice(k, 1); changed(); }}><Icon.x /></button>
          </div>
          <Field label="Label"><input value={i.label || ""} onChange={(e) => { i.label = e.target.value; changed(); }} /></Field>
          {i.type === "choice" && <Field label="Options (one per line)"><textarea rows={3} value={(i.options || []).join("\n")} onChange={(e) => { i.options = e.target.value.split("\n"); changed(); }} /></Field>}
          {["file", "files"].includes(i.type) && <Field label="Accepted files" hint="e.g. .pdf,.md,image/*"><input value={i.accept || ""} onChange={(e) => { i.accept = e.target.value; changed(); }} /></Field>}
          {["text", "multiline", "number", "clipboard", "choice"].includes(i.type) && <Field label="Default value">{i.type === "multiline" || i.type === "clipboard" ? <textarea rows={3} value={i.default || ""} onChange={(e) => { i.default = e.target.value; changed(); }} onPaste={onPasteAttach(fl_)} /> : <input value={i.default || ""} onChange={(e) => { i.default = e.target.value; changed(); }} />}</Field>}
          {i.type === "boolean" && <label className="check"><input type="checkbox" checked={i.default === "true"} onChange={(e) => { i.default = e.target.checked ? "true" : "false"; changed(); }} /> Yes by default</label>}
          {["file", "files", "clipboard", "multiline"].includes(i.type) && <><div className="muted fl-help">Default {i.type === "clipboard" ? "content" : "files"} (paste, drop or pick):</div><AttachBar list={fl_} insertText={(t) => { i.default = (i.default ? i.default + "\n" : "") + t; changed(); }} /></>}
          <div className="fl-row"><label className="check"><input type="checkbox" checked={!!i.required} onChange={(e) => { i.required = e.target.checked; changed(); }} /> Required</label>
            <Field label=""><input placeholder="Help text shown when the flow runs" value={i.description || ""} onChange={(e) => { i.description = e.target.value; changed(); }} /></Field></div>
        </div>
      );
    })}
    <button className="btn secondary" onClick={add}><Icon.plus /> Add input</button>
  </>;
}

function ArgsEditor({ node, args, toolName, extraVars, aiFills }) {
  const t = tools().find((x) => x.name === toolName);
  if (!t) return <p className="muted">Choose a tool.</p>;
  if (!(t.params || []).length) return <p className="muted">{t.name} takes no parameters.</p>;
  const long = (p) => /sql|query|script|body|json|text|prompt|code/i.test(p.name + " " + (p.desc || ""));
  const flow = fl().flow, feeders = node ? parentsOf(flow, node.id).map(nodeOf).filter((x) => x && x.type !== "start") : [];
  return <>
    {node && feeders.length > 0 && extraVars == null && <div className="fl-receives"><Icon.workflow /> This step gets the result of {feeders.map((x) => <b key={x.id}>{x.title || x.id}</b>).reduce((a, b) => [a, ", ", b])} automatically{aiFills ? ", and the AI uses it when it fills in the fields" : ""}.
      {!aiFills && <> To put that result into a field below, click <b>← {feeders[0].title || feeders[0].id}</b> next to the field.</>}
      <details className="fl-how"><summary>How is it passed?</summary>
        <ul><li><b>Into a field as text:</b> the field is set to <code className="inline-code" data-nofile>{`{{steps.${feeders[0].id}}}`}</code>.</li>
          <li><b>Into a field that wants a file</b> (like <code className="inline-code" data-nofile>content_file</code>): the field is set to <code className="inline-code" data-nofile>{"{{input_file}}"}</code>, which becomes the path of a temporary file holding the result. The ← button does this for fields with “file” in their name.</li>
          <li><b>Without a field:</b> tools that read piped data (like <code className="inline-code" data-nofile>jq</code> or <code className="inline-code" data-nofile>grep</code>) get it as their input, as with <code className="inline-code" data-nofile>a | b</code> in a terminal.</li></ul></details></div>}
    {t.params.map((p) => <Field key={p.name} label={<>{p.name}{p.required && !aiFills ? " *" : ""} <span className="muted">· {p.type}</span>{extraVars == null && feeders.map((x) => <button key={x.id} type="button" className="fl-use" title={FILE_PARAM(p) ? `Give ${p.name} a file holding the result of ${x.title || x.id}` : `Put the result of ${x.title || x.id} into ${p.name}`} onClick={(e) => { e.preventDefault(); args[p.name] = FILE_PARAM(p) && feeders.length === 1 ? "{{input_file}}" : `{{steps.${x.id}}}`; changed(); }}>← {x.title || x.id}{FILE_PARAM(p) ? " (as a file)" : ""}</button>)}</>} hint={p.desc}>
    {long(p) ? <textarea rows={3} value={args[p.name] || ""} placeholder={aiFills ? "AI fills this (or type a value to fix it)" : p.required ? "Required" : "Optional"} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => { args[p.name] = e.target.value; changed(); }} />
      : <input value={args[p.name] || ""} placeholder={aiFills ? "AI fills this (or type a value to fix it)" : p.required ? "Required" : "Optional"} {...track} onChange={(e) => { args[p.name] = e.target.value; changed(); }} />}</Field>)}
    <VarChips node={node} extra={extraVars} /></>;
}

// Example requests for a tool, by what it does.
function requestExample(t) {
  const s = ((t?.name || "") + " " + (t?.desc || "")).toLowerCase();
  if (/sql|postgres|mysql|database|query|db\b/.test(s)) return "e.g. The 10 customers with the most orders last month, with their email and order count";
  if (/jira|ticket|issue/.test(s)) return "e.g. Open tickets in the ORD project assigned to me, highest priority first";
  if (/http|rest|api|fetch|url/.test(s)) return "e.g. The current status of the payments service";
  if (/test/.test(s)) return "e.g. Run the tests for the package that handles refunds";
  if (/search|find|grep|todo/.test(s)) return "e.g. Everything still marked TODO in the checkout code";
  return "Describe what this call should achieve; AI works out the arguments";
}

function Preview({ node }) {
  const f = fl(), flow = f.flow;
  const parents = parentsOf(flow, node.id).filter((p) => p !== "start");
  const key = flow.id, sm = (f.samples[key] ||= { inputs: {}, steps: {} });
  const res = f.preview[node.id];
  const run = async () => {
    f.preview[node.id] = { busy: true }; update();
    const steps = { ...sm.steps };
    for (const id of ancestors(flow, node.id)) if (!(id in steps)) steps[id] = "";
    const r = await backend.PreviewFlowStep(flow, node.id, { inputs: sm.inputs, steps, context: flow.context.text || "", skills: flow.skills.map((s) => s.path).join("\n") }).catch((e) => ({ error: errMsg(e) }));
    f.preview[node.id] = r; update();
  };
  const usesInputs = flow.inputs.filter((i) => JSON.stringify(node).includes("inputs." + i.name));
  return (
    <details className="fl-preview" open={!!res}>
      <summary><b>Try it with sample data</b></summary>
      {parents.map((p) => <Field key={p} label={"Sample output of " + (nodeOf(p)?.title || p)}><textarea rows={3} value={sm.steps[p] || ""} placeholder={'e.g. {"summary": "Place order"}'} onChange={(e) => { sm.steps[p] = e.target.value; update(); }} /></Field>)}
      {usesInputs.map((i) => <Field key={i.name} label={"Sample " + (i.label || i.name)}><input value={sm.inputs[i.name] || ""} onChange={(e) => { sm.inputs[i.name] = e.target.value; update(); }} /></Field>)}
      <button className="btn secondary" onClick={run} disabled={res?.busy}>{res?.busy ? "Running…" : "Preview"}</button>
      {res && !res.busy && <>{res.error && <div className="banner error">{res.error}</div>}
        {res.output != null && res.output !== "" && (node.type === "output" && node.format === "html"
          ? <iframe className="fl-html" sandbox="" srcDoc={res.output} title="HTML preview" />
          : <pre className="code fl-out">{res.output}</pre>)}</>}
    </details>
  );
}

function StepInspector({ n }) {
  const f = fl(), flow = f.flow, ops = f.ops?.ops || {};
  const set = (k, v) => { n[k] = v; changed(); };
  const parents = parentsOf(flow, n.id);
  const issues = f.issues.filter((i) => i.node === n.id);
  const opGroups = Object.entries(ops).reduce((g, [id, o]) => ((g[o.group] ||= []).push([id, o]), g), {});
  const op = ops[n.op] || {};
  return <>
    <div className="fl-insp-head"><StepIcon n={n} tools={tools()} size={36} /><div><h3>{n.title || n.id}</h3><small className="muted">{n.type === "tool" ? "Tool" : STEP_TYPES.find((t) => t.type === n.type)?.name || "Inputs"}</small></div>
      {n.type !== "start" && <button className="link" style={{ marginLeft: "auto" }} onClick={removeSelected}>Delete</button>}</div>
    {issues.map((i, k) => <div key={k} className={"banner " + (i.level === "error" ? "error" : "")}>{i.message}</div>)}
    {n.type === "start" ? <InputsEditor /> : <>
      <div className="fl-row">
        <Field label="Title"><input value={n.title || ""} onChange={(e) => set("title", e.target.value)} /></Field>
        <Field label="Step id" hint="Used as {{steps.id}}"><input defaultValue={n.id} key={n.id} style={{ fontFamily: "var(--font-mono)" }} onBlur={(e) => e.target.value !== n.id && renameNode(n, e.target.value)} /></Field>
      </div>
      {n.type === "tool" && <>
        <Field label="Tool"><select value={n.tool || ""} onChange={(e) => { n.tool = e.target.value; n.args = {}; const t = tools().find((x) => x.name === n.tool); (t?.params || []).forEach((p) => { n.args[p.name] = ""; }); changed(); }}>
          <option value="">Choose…</option>{tools().map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}</select></Field>
        {n.tool && (() => {
          const describe = n.instruction != null || f.describe?.[n.id];
          const t = tools().find((x) => x.name === n.tool);
          const setMode = (d) => { (f.describe ||= {})[n.id] = d; if (!d) delete n.instruction; else n.instruction ??= ""; changed(); };
          return <>
            <Field label="How to fill in the arguments"><div className="seg">
              <button type="button" aria-pressed={!describe} onClick={() => setMode(false)}>Exact values</button>
              <button type="button" aria-pressed={describe} onClick={() => setMode(true)}><Icon.spark /> Describe it, AI builds them</button></div></Field>
            {describe && <>
              <Field label="What should this step do?" hint="When the flow runs, the agent investigates first (schema files, migrations, models, docs; it may also call the tool to explore, read-only), works out the arguments, then the tool runs with them. You see the chosen arguments and why.">
                <textarea rows={4} value={n.instruction || ""} placeholder={requestExample(t)} {...track} onChange={(e) => { n.instruction = e.target.value; changed(); }} /></Field>
              <VarChips node={n} />
              <h4>Fixed values <span className="muted" style={{ fontWeight: 400, textTransform: "none" }}>· optional; AI fills the rest</span></h4>
            </>}
            {!describe && <h4>Arguments</h4>}
            <ArgsEditor node={n} args={(n.args ||= {})} toolName={n.tool} aiFills={describe} />
          </>;
        })()}
      </>}
      {n.type === "ai" && <>
        <Field label="Prompt" hint="The flow's context and skills are added automatically."><textarea rows={8} value={n.prompt || ""} placeholder="e.g. Write a specification for {{inputs.ticket}} from {{steps.jira}}. Follow the skills." {...track} onChange={(e) => set("prompt", e.target.value)} /></Field>
        <VarChips node={n} />
        <Field label="Model (optional)"><input value={n.model || ""} placeholder="the agent's default" onChange={(e) => set("model", e.target.value)} /></Field>
      </>}
      {n.type === "merge" && <>
        <Field label="How to merge"><select value={n.mode || "concat"} onChange={(e) => set("mode", e.target.value)}>{MERGE_MODES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
        {n.mode === "concat" && <Field label="Separator" hint="\n is a new line"><input value={n.separator ?? ""} onChange={(e) => set("separator", e.target.value)} /></Field>}
        {n.mode === "template" && <><Field label="Template"><textarea rows={6} value={n.template || ""} {...track} onChange={(e) => set("template", e.target.value)} /></Field><VarChips node={n} /></>}
        <p className="muted fl-help">Merges, in this order: {parents.map((p) => nodeOf(p)?.title || p).join(" → ") || "nothing yet; connect steps to it"}.</p>
        <Preview node={n} />
      </>}
      {n.type === "transform" && <>
        <Field label="Transformation"><select value={n.op || ""} onChange={(e) => set("op", e.target.value)}>
          {Object.entries(opGroups).map(([g, list]) => <optgroup key={g} label={g}>{list.map(([id, o]) => <option key={id} value={id}>{o.name}</option>)}</optgroup>)}</select></Field>
        {op.help && <p className="muted fl-help">{op.help}</p>}
        {op.expr && <Field label={op.expr}><input value={n.expr || ""} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => set("expr", e.target.value)} /></Field>}
        {op.replace && <Field label="Replace with"><input value={n.replace || ""} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => set("replace", e.target.value)} /></Field>}
        {op.template && <><Field label={op.ai ? "Instruction for the AI" : "Template"}><textarea rows={5} value={n.template || ""} placeholder={op.ai ? "e.g. Turn {{input}} into a bulleted list of acceptance criteria." : "{{input}}"} {...track} onChange={(e) => set("template", e.target.value)} /></Field><VarChips node={n} extra={["input"]} /></>}
        <details><summary className="muted">Input (default: the output of the step before)</summary>
          <Field label="Use instead"><input value={n.source || ""} placeholder="{{steps.…}}" {...track} onChange={(e) => set("source", e.target.value)} /></Field></details>
        {!op.ai && <Preview node={n} />}
      </>}
      {n.type === "output" && <>
        <Field label="Format"><div className="seg">{FORMATS.map(([v, l]) => <button key={v} type="button" aria-pressed={n.format === v} onClick={() => set("format", v)}>{l}</button>)}</div></Field>
        <Field label="Content" hint="Empty: the output of the steps before. JSON, CSV, Markdown and HTML are converted to the format."><textarea rows={4} value={n.template || ""} placeholder="{{steps.…}}" {...track} onChange={(e) => set("template", e.target.value)} /></Field>
        <VarChips node={n} />
        <h4>Actions</h4>
        {(n.actions ||= []).map((a, k) => (
          <div key={k} className="fl-action">
            <div className="fl-row"><select value={a.type} onChange={(e) => { a.type = e.target.value; changed(); }}>
              <option value="save">Save to a file</option><option value="tool">Call a tool</option><option value="clipboard">Copy to the clipboard</option><option value="show">Show the result</option></select>
              <button className="icon-btn fl-del" title="Remove action" onClick={() => { n.actions.splice(k, 1); changed(); }}><Icon.x /></button></div>
            {a.type === "save" && <Field label="File (in the project)" hint={`e.g. out/{{inputs.ticket}}.${n.format || "md"}`}><input value={a.path || ""} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => { a.path = e.target.value; changed(); }} /></Field>}
            {a.type === "tool" && <><Field label="Tool"><select value={a.tool || ""} onChange={(e) => { a.tool = e.target.value; a.args = {}; changed(); }}><option value="">Choose…</option>{tools().map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}</select></Field>
              {a.tool && <ArgsEditor node={n} args={(a.args ||= {})} toolName={a.tool} extraVars={["output"]} />}</>}
          </div>))}
        <div className="row-actions">{[["save", "Save"], ["tool", "Call tool"], ["clipboard", "Copy"], ["show", "Show"]].map(([t, l]) => <button key={t} className="btn secondary" onClick={() => { n.actions.push({ type: t, ...(t === "save" ? { path: `out/${flow.id}.${n.format || "md"}` } : {}) }); changed(); }}><Icon.plus /> {l}</button>)}</div>
        <Preview node={n} />
      </>}
      <label className="check fl-review"><input type="checkbox" checked={!!n.review} onChange={(e) => set("review", e.target.checked)} /> Pause for review after this step <span className="muted">(accept, refine, run a command or stop)</span></label>
      <Field label="Note"><textarea rows={2} value={n.note || ""} placeholder="Why this step is here" onChange={(e) => set("note", e.target.value)} /></Field>
    </>}
  </>;
}

function Inspector() {
  const f = fl(), n = f.sel ? nodeOf(f.sel) : null;
  return <aside className="fl-inspector" aria-label="Inspector">{n ? <StepInspector key={n.id} n={n} /> : <FlowSettings key={f.flow.id} />}</aside>;
}

function Issues() {
  const f = fl(), [open, setOpen] = useState(false);
  const errs = f.issues.filter((i) => i.level === "error").length, warns = f.issues.length - errs;
  if (!f.issues.length) return <span className="badge badge-ok">no problems</span>;
  return <span className="fl-issues">
    <button className={"badge " + (errs ? "badge-warn" : "")} onClick={() => setOpen(!open)}>{errs ? `${errs} problem${errs === 1 ? "" : "s"}` : ""}{errs && warns ? " · " : ""}{warns ? `${warns} warning${warns === 1 ? "" : "s"}` : ""}</button>
    {open && <div className="fl-issue-pop" onMouseLeave={() => setOpen(false)}>{f.issues.map((i, k) => <button key={k} className={"fl-issue " + i.level} onClick={() => { f.sel = i.node || null; setOpen(false); update(); }}><b>{i.node ? (nodeOf(i.node)?.title || i.node) : "Flow"}</b> {i.message}</button>)}</div>}
  </span>;
}

// --- screen --------------------------------------------------------------------------------

export function Flows() {
  const f = fl(), imp = useRef(null);
  useEffect(() => { if (S.ws) { loadList(); refreshExtensions(); } }, [S.ws?.root]);
  useEffect(() => { if (S.ws && f.list?.length && !f.flow && !f.id) openFlow(f.list[0].id); }, [f.list]);
  useEffect(() => () => { if (fl().dirty) save(); }, []);
  if (!S.ws) return <NeedWorkspace what="the flow designer" />;
  return (
    <div className="files flows">
      <nav className="ftree" aria-label="Flows">
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "0 8px 8px" }}><h1>Flows</h1><button className="btn primary" onClick={newFlow}>+ New</button></div>
        {(f.list || []).map((x) => <button key={x.id} className={"run-item" + (x.id === f.flow?.id ? " active" : "")} onClick={() => openFlow(x.id)}>
          <span className="run-item-top"><b>{x.name}</b></span><small>{x.steps} step{x.steps === 1 ? "" : "s"} · {x.outputs} output{x.outputs === 1 ? "" : "s"}{x.updated ? " · " + new Date(x.updated).toLocaleDateString() : ""}</small></button>)}
        {f.list && !f.list.length && <p className="muted" style={{ padding: "4px 8px" }}>No flows yet. A flow strings tools together: inputs → tools, AI, merges and transforms → outputs.</p>}
        <div style={{ padding: "10px 8px" }}><button className="btn secondary" onClick={() => imp.current?.click()}>Import…</button>
          <input ref={imp} type="file" accept=".json,application/json" hidden onChange={(e) => { const file = e.target.files[0]; e.target.value = ""; if (file) importFlow(file); }} /></div>
      </nav>
      {f.flow ? <div className="fl-main">
        <header className="fl-bar">
          <select className="fl-switch" aria-label="Open a flow" value={f.flow.id} onChange={(e) => e.target.value === "+new" ? newFlow() : e.target.value === "+import" ? imp.current?.click() : openFlow(e.target.value)}>
            {(f.list || []).map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}<option value="+new">+ New flow…</option><option value="+import">Import…</option></select>
          <input className="fl-name" value={f.flow.name} aria-label="Flow name" onChange={(e) => { f.flow.name = e.target.value; changed(); }} />
          <Issues />
          <span className="muted fl-saved">{f.saving ? "Saving…" : f.dirty ? "Unsaved" : "Saved"}</span>
          <span className="status-spacer" />
          <button className="btn secondary" onClick={() => { f.sel = null; f.selEdge = null; update(); }}>Flow settings</button>
          <button className="btn primary" title="Run this flow" onClick={async () => { if (f.dirty) await save(); openFlowRunner(f.flow.id); }}><Icon.run /> Run</button>
          <button className="btn secondary" onClick={exportFlow}>Export</button>
          <button className="btn secondary" onClick={duplicateFlow}>Duplicate</button>
          <button className="btn secondary" onClick={deleteFlow}>Delete</button>
          <button className="btn primary" disabled={f.saving || !f.dirty} onClick={save}>Save</button>
        </header>
        <div className="fl-work"><Palette /><Canvas /><Inspector /></div>
      </div> : <section className="view"><div className="card"><h3>Design a flow</h3>
        <p className="wf-desc">Drag tools onto a canvas and connect them. Give the flow inputs (text, files, clipboard), a custom context and skills; merge and transform data between steps; and produce outputs as Markdown, text, HTML, CSV or JSON that are saved, passed to a tool, copied or shown.</p>
        <div className="row-actions"><button className="btn primary" onClick={newFlow}>Create a flow</button><button className="btn secondary" onClick={() => imp.current?.click()}>Import a flow…</button></div></div></section>}
    </div>
  );
}
