// Flow designer: string tools together into a flow.
//   left      the project's flows (new, import)
//   palette   step types (AI prompt, merge, transform, output) and tools; drag onto the canvas
//   canvas    steps as cards; drag a card to move it, drag from its ● port to another card to connect
//   inspector the selected step, or the flow (name, context, skills) when nothing is selected
// Flows are saved to .codegen/flows/<id>.json (autosave). Running them comes with the executor.
import { ModelPicker } from "../ModelPicker.jsx";
import { stepRef, toolRef, isBuiltin, loopInfo, uniqueTitle } from "../flowNames.js";
import { useEffect, useRef, useState } from "react";
import { openFlowRunner } from "./FlowRun.jsx";
import { confirmDialog, askText } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, saveText, refreshExtensions, kcName, isWindows } from "../store.js";
import { Icon } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { ToolIcon } from "../ToolTile.jsx";
import { AttachBar, AttachmentChips, onPasteAttach } from "../Attachments.jsx";
import { FileLink } from "../FileLinks.jsx";
import { openFlowGuide, GUIDE_TYPES } from "./FlowGuide.jsx";
import { paths as schemaPaths, compact, typeName } from "../schema.js";
import { AiProgress } from "../AiProgress.jsx";

const NODE_W = 216, PORT_Y = 30, GRID = 10;
const snap = (v) => Math.round(v / GRID) * GRID;
const clone = (x) => JSON.parse(JSON.stringify(x));

const STEP_TYPES = [
  { type: "ai", name: "AI prompt", help: "Ask the agent; uses the flow's context and skills" },
  { type: "merge", name: "Merge", help: "Combine several steps' outputs" },
  { type: "transform", name: "Transform", help: "Reshape data: JSON, CSV, text, Markdown → HTML, AI" },
  { type: "loop", name: "Loop (for each)", help: "Split a list into items; the steps after it run once per item, until a Gather" },
  { type: "gather", name: "Gather", help: "Ends a loop: collects every item's result as text, sections or JSON" },
  { type: "decision", name: "Decision", help: "Sends the flow down one path (or several), chosen by conditions on the data" },
  { type: "filter", name: "Filter", help: "Keeps or drops the rows that match conditions" },
  { type: "aggregate", name: "Aggregate", help: "Groups rows: count, sum, min, max, distinct count, windows (rank, running sum)" },
  { type: "output", name: "Output", help: "md, txt, html, csv or json, then save, call a tool, copy or show" },
];
const FORMATS = [["md", "Markdown"], ["txt", "Text"], ["html", "HTML"], ["csv", "CSV"], ["json", "JSON"]];
const INPUT_TYPES = [["text", "Text"], ["multiline", "Long text"], ["secret", "Secret"], ["number", "Number"], ["boolean", "Yes / no"], ["choice", "Choice"], ["multichoice", "Multiple choice"], ["file", "File"], ["files", "Files"], ["clipboard", "Clipboard"]];
const SPLIT_MODES = [["auto", "Detect (JSON array, else one per line)"], ["lines", "One item per line"], ["json", "JSON array"], ["csv", "CSV rows (each a JSON object)"], ["regex", "Matches of a pattern"]];
const GATHER_MODES = [["concat", "Join as text"], ["sections", "Sections (## item, then its result)"], ["json-array", "JSON array"], ["json-object", "JSON object (item → result)"], ["template", "Template per item"]];
const ROW_FORMATS = [["auto", "Detect (JSON, CSV or one row per line)"], ["json", "JSON array"], ["csv", "CSV with a header line"], ["lines", "One row per line"]];
const MATCHES = [["all", "all of"], ["any", "any of"], ["none", "none of"]];
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
  loop: { color: "--ansi-green", Icon: sv(<><path d="M17 2l3 3-3 3" /><path d="M4 11V9a4 4 0 014-4h12" /><path d="M7 22l-3-3 3-3" /><path d="M20 13v2a4 4 0 01-4 4H4" /></>) },
  gather: { color: "--ansi-yellow", Icon: sv(<><path d="M4 4l6 6M20 4l-6 6M12 4v6" /><path d="M5 14h14l-2 6H7z" /></>) },
  decision: { color: "--ansi-magenta", Icon: sv(<><path d="M12 3l8 9-8 9-8-9z" /><path d="M12 8v5M12 16v.5" /></>) },
  filter: { color: "--ansi-cyan", Icon: sv(<><path d="M4 5h16l-6 7.5V18l-4 2v-7.5z" /></>) },
  aggregate: { color: "--ok", Icon: sv(<><path d="M18 5H7l6 7-6 7h11" /></>) },
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
export function openFlowInDesigner(id, sel) { fl().id = ""; fl().flow = null; S.view = "flows"; openFlow(id).then(() => { if (sel && nodeOf(sel)) { fl().sel = sel; update(); } }); }

async function openFlow(id) {
  const f = fl();
  if (f.dirty) await save();
  try {
    const flow = await backend.LoadFlow(id);
    Object.assign(f, { id, flow, sel: null, selEdge: null, dirty: false, preview: {} });
    f.issues = (await backend.ValidateFlow(flow).catch(() => [])) || [];
    await refreshShapes();
  } catch (e) { toast(errMsg(e)); }
  update();
}

// What every step gets and gives (designer metadata; never sent to the AI).
async function refreshShapes() {
  const f = fl();
  if (!f.flow) return;
  const samples = Object.fromEntries(Object.entries(f.samples[f.flow.id]?.steps || {}).filter(([, v]) => String(v).trim()));
  f.shapes = (await backend.FlowShapes(f.flow, samples).catch(() => null)) || {};
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
    await refreshShapes();
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
const projectTools = () => (S.ws?.extensions || []).filter((t) => !t.managed || t.managed.status === "verified");
// built-in tools (read files, search, edit, run commands, fetch pages) run directly as steps: "builtin:<name>"
const builtinTools = () => S.flowBuiltins || [];
const tools = () => [...projectTools(), ...builtinTools()];
const BUILTIN_GROUPS = [["read", "Read"], ["search", "Search"], ["edit", "Edit files"], ["execute", "Run"], ["web", "Web"]];
const toolLabel = (name) => (isBuiltin(name) ? toolRef(name) + " · built-in" : name);
async function loadBuiltins() {
  try { S.flowBuiltins = (await backend.FlowBuiltinTools()) || []; } catch { S.flowBuiltins = []; }
  update();
}
// a tool <select>: project tools, then built-in tools by group
function ToolOptions() {
  const p = projectTools(), b = builtinTools();
  return <>{p.length > 0 && <optgroup label="Project tools">{p.map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}</optgroup>}
    {BUILTIN_GROUPS.map(([g, l]) => { const ts = b.filter((t) => t.lang === g); return ts.length > 0 && <optgroup key={g} label={"Built-in · " + l}>{ts.map((t) => <option key={t.name} value={t.name}>{toolRef(t.name)}</option>)}</optgroup>; })}</>;
}

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
  const id = uniqueId(spec.type === "tool" ? toolRef(spec.tool) : spec.type);
  const n = { id, type: spec.type, title: uniqueTitle(flow, spec.type === "tool" ? toolRef(spec.tool) : (STEP_TYPES.find((t) => t.type === spec.type)?.name || "").replace(/\s*\(.*\)$/, ""), id), x: snap(x ?? right + NODE_W + 60), y: snap(y ?? 120) };
  if (spec.type === "tool") { n.tool = spec.tool; n.args = {}; const t = tools().find((x) => x.name === spec.tool); (t?.params || []).forEach((p) => { n.args[p.name] = ""; });
    if (spec.tool === "builtin:fetch_webpage") n.args.format = "raw"; } // in a flow, the response is data for the next steps
  if (spec.type === "ai") n.prompt = "";
  if (spec.type === "merge") { n.mode = "concat"; n.separator = "\\n\\n"; }
  if (spec.type === "transform") n.op = "json-path";
  if (spec.type === "output") { n.format = "md"; n.actions = [{ type: "show" }]; }
  if (spec.type === "loop") { n.split = "auto"; n.max = 100; }
  if (spec.type === "gather") { n.mode = "sections"; }
  if (spec.type === "decision") { n.mode = "first"; n.branches = [{ id: "path1", label: "Path 1", when: newCond() }]; }
  if (spec.type === "filter") { n.mode = "keep"; n.cond = newCond(); }
  if (spec.type === "aggregate") { n.mode = "group"; n.groupBy = []; n.measures = [{ fn: "count", as: "count" }]; }
  // connect from the selected step (or the inputs) so the new step is wired up
  const from = f.sel && nodeOf(f.sel)?.type !== "output" ? f.sel : x == null ? "start" : null;
  flow.nodes.push(n);
  if (from) { const e = { from, to: n.id }, fa = nodeOf(from); if (fa?.type === "decision") e.branch = nextBranch(fa); flow.edges.push(e); const note = fa?.type === "decision" ? `Connected on the path “${branchName(fa, e.branch)}”. Click the connection to change it.` : autoWire(from, n) || loopFromInputs(fa, n); if (note && (from !== "start" || n.type === "loop")) toast(note); }
  f.sel = n.id; f.selEdge = null;
  changed();
}

// When a step is connected into a tool, pass its output into the tool's best
// matching empty argument (from Inputs: arguments named like an input).
// Returns a short note for the user, or "".
const DATA_PARAM = /input|text|data|content|body|json|payload|items|list|query|sql|file|message|value|source|doc/i;
// fields that expect a file path rather than the text itself
export const FILE_PARAM = (p) => /file/i.test(p.name) && !/name$/i.test(p.name);
// a step's result: {{<tool>.output_file}} (path of the saved file) for file
// fields, {{<tool>.buffer_output}} (the text) for the rest
const feedFor = (p, from) => { const r = stepRef(fl().flow, from); return FILE_PARAM(p) ? `{{${r}.output_file}}` : `{{${r}.buffer_output}}`; };
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
  if (a.type === "loop") { // a loop's output is the current item
    const p = empty.find((x) => x.required) || empty[0];
    // nested in another loop: name the loop, so the items cannot be mixed up
    const v = (loopInfo(fl().flow).stack[from] || []).length ? `{{${stepRef(fl().flow, from)}.item}}` : "{{item}}";
    if (p) { args[p.name] = v; return `“${p.name}” of ${n.title || toolRef(n.tool)} gets each item of ${a.title || a.id} (${v}).`; }
  }
  const pick = empty.find((p) => p.required && DATA_PARAM.test(p.name)) || empty.find((p) => DATA_PARAM.test(p.name)) || empty.find((p) => p.required);
  if (pick) { args[pick.name] = feedFor(pick, from); return FILE_PARAM(pick) ? `“${pick.name}” of ${n.title || n.tool} gets a file with the result of ${a.title || a.id}.` : `Passing the result of ${a.title || a.id} into “${pick.name}” of ${n.title || n.tool}.`; }
  if (isBuiltin(n.tool)) return `Use the ← ${a.title || a.id} button on a field of ${n.title || toolRef(n.tool)} to pass the result in (built-in tools read their fields only).`;
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
  const e = { from, to };
  if (a.type === "decision") {
    e.branch = nextBranch(a);
    toast(`Connected on the path “${branchName(a, e.branch)}”. Click the connection to change it.`);
  }
  flow.edges.push(e);
  const note = autoWire(from, b) || loopFromInputs(a, b);
  if (note) toast(note);
  changed();
}

function removeSelected() {
  const f = fl(), flow = f.flow;
  if (f.selEdge != null) { flow.edges.splice(f.selEdge, 1); f.selEdge = null; return changed(); }
  const ids = new Set(selectedIds().filter((id) => nodeOf(id)?.type !== "start"));
  if (!ids.size) return;
  flow.nodes = flow.nodes.filter((x) => !ids.has(x.id));
  flow.edges = flow.edges.filter((e) => !ids.has(e.from) && !ids.has(e.to));
  f.sel = null; f.multi = []; changed();
  if (ids.size > 1) toast(`Deleted ${ids.size} steps.`);
}

// --- several selected steps: select, copy, paste ------------------------------------

// the selected steps: the one in the inspector, and the others selected with it
function selectedIds() {
  const f = fl(), out = [...(f.multi || [])];
  if (f.sel && !out.includes(f.sel)) out.push(f.sel);
  return out.filter((id) => nodeOf(id));
}
function setSelection(ids) {
  const f = fl();
  ids = [...new Set(ids)].filter((id) => nodeOf(id));
  f.multi = ids.length > 1 ? ids : []; f.sel = ids[ids.length - 1] || null; f.selEdge = null;
  update();
}

const CLIP_FORMAT = "specwright-steps";

// copySteps puts the selected steps, the connections between them and the
// inputs they use on the clipboard (and keeps them for pasting in the app).
function copySteps(e) {
  const f = fl(), flow = f.flow, ids = new Set(selectedIds().filter((id) => nodeOf(id)?.type !== "start"));
  if (!ids.size) return false;
  const nodes = flow.nodes.filter((n) => ids.has(n.id)).map((n) => JSON.parse(JSON.stringify(n)));
  const edges = flow.edges.filter((x) => ids.has(x.from) && ids.has(x.to));
  const text = JSON.stringify(nodes), inputs = flow.inputs.filter((i) => text.includes("inputs." + i.name));
  const clip = { format: CLIP_FORMAT, version: 1, from: flow.name, nodes, edges, inputs };
  const json = JSON.stringify(clip, null, 2);
  S.flowClip = clip;
  if (e?.clipboardData) { e.clipboardData.setData("text/plain", json); e.preventDefault(); }
  else writeClipboard(json);
  toast(`Copied ${nodes.length} step${nodes.length === 1 ? "" : "s"}${edges.length ? ` and ${edges.length} connection${edges.length === 1 ? "" : "s"}` : ""}. Paste them into this or another flow with ⌘V.`);
  return true;
}

// the system clipboard: the desktop app's, else the browser's
function writeClipboard(text) {
  try { if (window.runtime?.ClipboardSetText) return window.runtime.ClipboardSetText(text); } catch { /* the browser's */ }
  return navigator.clipboard?.writeText(text).catch(() => {});
}
async function readClipboard() {
  try { if (window.runtime?.ClipboardGetText) return await window.runtime.ClipboardGetText(); } catch { /* the browser's */ }
  try { return await navigator.clipboard.readText(); } catch { return ""; }
}
// pasteFromClipboard: copied steps from the clipboard (another window or
// flow), else the ones copied in this session
async function pasteFromClipboard(asNew) {
  const c = readClip(await readClipboard()) || S.flowClip;
  if (!c) return toast("Nothing to paste: select steps and copy them (⌘C) first.");
  S.flowClip = c;
  if (asNew || !fl().flow) return pasteAsNewFlow(c);
  pasteSteps(c);
}

const readClip = (text) => { try { const c = JSON.parse(text); return c?.format === CLIP_FORMAT && Array.isArray(c.nodes) ? c : null; } catch { return null; } };

// pasteSteps adds copied steps to the open flow: new ids (their references
// to each other follow), placed next to what is there, the inputs they use
// added when the flow lacks them; the pasted steps end up selected.
function pasteSteps(clip) {
  const f = fl(), flow = f.flow;
  if (!flow || !clip?.nodes?.length) return;
  const map = {}, added = [];
  const minX = Math.min(...clip.nodes.map((n) => n.x || 0)), minY = Math.min(...clip.nodes.map((n) => n.y || 0));
  const same = clip.nodes.some((n) => nodeOf(n.id) && nodeOf(n.id).x === n.x && nodeOf(n.id).y === n.y); // pasting where the copies came from
  const right = Math.max(...flow.nodes.map((n) => n.x)) + NODE_W + 60;
  const ox = same ? 40 : right - minX, oy = same ? 60 : 120 - minY;
  for (const n of clip.nodes) {
    if (n.type === "start") continue;
    const id = uniqueId(n.id);
    map[n.id] = id;
    const c = JSON.parse(JSON.stringify(n));
    Object.assign(c, { id, x: snap((n.x || 0) + ox), y: snap((n.y || 0) + oy) });
    flow.nodes.push(c); added.push(c);
  }
  // references between the pasted steps follow their new ids
  const pairs = Object.entries(map).filter(([a, b]) => a !== b).sort((x, y) => y[0].length - x[0].length);
  const fix = (s) => typeof s !== "string" ? s : pairs.reduce((t, [a, b]) => t.replace(new RegExp("\\{\\{(\\s*)(steps\\.)?" + a.replace(/[-]/g, "\\-") + "(?=[.}\\s])", "g"), "{{$1$2" + b), s);
  for (const c of added) {
    for (const k of ["prompt", "template", "source", "expr", "instruction"]) if (c[k]) c[k] = fix(c[k]);
    for (const k in c.args || {}) c.args[k] = fix(c.args[k]);
    for (const a of c.actions || []) { a.path = fix(a.path); for (const k in a.args || {}) a.args[k] = fix(a.args[k]); }
  }
  for (const e of clip.edges || []) if (map[e.from] && map[e.to]) flow.edges.push({ ...e, from: map[e.from], to: map[e.to] });
  const newInputs = (clip.inputs || []).filter((i) => !flow.inputs.some((x) => x.name === i.name));
  flow.inputs.push(...newInputs.map((i) => JSON.parse(JSON.stringify(i))));
  setSelection(added.map((c) => c.id));
  changed();
  toast(`Pasted ${added.length} step${added.length === 1 ? "" : "s"}${newInputs.length ? `, and the input${newInputs.length === 1 ? "" : "s"} ${newInputs.map((i) => i.name).join(", ")}` : ""}. Connect them where they belong.`);
}

async function pasteAsNewFlow(clip) {
  clip ||= S.flowClip;
  if (!clip) return toast("Copy some steps in a flow first (select them, then ⌘C).");
  try {
    const flow = await backend.NewFlow("Steps from " + (clip.from || "a flow"));
    await loadList(); await openFlow(flow.id);
    pasteSteps(clip);
  } catch (e) { toast(errMsg(e)); }
}

// rename a step and every {{steps.<id>…}} / {{<id>.output_file|buffer_output}} that refers to it
function renameNode(n, id) {
  const flow = fl().flow;
  id = id.replace(/[^A-Za-z0-9_-]/g, "");
  if (!id || !/^[A-Za-z]/.test(id) || flow.nodes.some((x) => x.id === id && x !== n)) return toast("Step ids start with a letter and must be unique.");
  const re = new RegExp("\\{\\{\\s*steps\\." + n.id.replace(/[-]/g, "\\-") + "(?=[.}\\s])", "g"), rep = "{{steps." + id;
  const re2 = new RegExp("\\{\\{(\\s*)" + n.id.replace(/[-]/g, "\\-") + "\\.(output_file|buffer_output|item_index|item_count|item)(?=[.}\\s])", "g");
  const fix = (s) => (typeof s === "string" ? s.replace(re, rep).replace(re2, "{{$1" + id + ".$2") : s);
  for (const x of flow.nodes) {
    for (const k of ["prompt", "template", "source", "expr", "instruction"]) if (x[k]) x[k] = fix(x[k]);
    if (x.args) for (const k in x.args) x.args[k] = fix(x.args[k]);
    for (const a of x.actions || []) { a.path = fix(a.path); if (a.args) for (const k in a.args) a.args[k] = fix(a.args[k]); }
  }
  flow.edges.forEach((e) => { if (e.from === n.id) e.from = id; if (e.to === n.id) e.to = id; });
  if (fl().sel === n.id) fl().sel = id;
  n.id = id; changed();
}

// --- secrets -------------------------------------------------------------------------

const SECRET_SOURCES = [
  ["ask", "Ask in the run form", "Typed when the flow runs, masked."],
  ["keychain", () => `System ${kcName()}`, () => `Read from the ${kcName()} when the run starts; nothing to type.`],
  ["env", "Environment variable", "For headless and CI runs."],
  ["auto", () => `${kcName()}, then environment, else ask`, "Uses whichever has a value; asks in the run form if none does."],
  ["prompt", "Ask in a dialog during the run", "Asked once the run has started: also scheduled runs, and on the terminal when headless."],
];
export const PASSWORD_PARAM = /(pass(word|wd|phrase)?|secret|token|api[_-]?key|private[_-]?key|credential|auth)/i;

// Where a secret input's value comes from. The flow file keeps only where
// to look, never the value.
function SecretSourceEditor({ i }) {
  const src = i.source || "ask", k = i.keychain || {}, [st, setSt] = useState(null), [val, setVal] = useState("");
  const ref = () => ({ service: k.service || "SpecWright", account: k.account || i.name, kind: k.kind || "" });
  const set = (patch) => { Object.assign(i, patch); setSt(null); changed(); };
  const setK = (patch) => { i.keychain = k; Object.assign(k, patch); for (const x of ["service", "account", "kind"]) if (!k[x]) delete k[x]; if (!Object.keys(k).length) delete i.keychain; setSt(null); changed(); };
  const check = async () => { try { setSt(await backend.CheckKeychain(ref())); } catch (e) { toast(errMsg(e)); } };
  const save = async () => { try { await backend.SaveToKeychain(ref(), val); setVal(""); toast(`Saved in the ${kcName()} (${ref().service} / ${ref().account}).`); check(); } catch (e) { toast(errMsg(e)); } };
  const usesKc = src === "keychain" || src === "auto", usesEnv = src === "env" || src === "auto";
  return <div className="fl-secret">
    <Field label="Where the value comes from"><select value={src} onChange={(e) => set({ source: e.target.value === "ask" ? undefined : e.target.value })}>{SECRET_SOURCES.map(([v, l]) => <option key={v} value={v}>{typeof l === "function" ? l() : l}</option>)}</select></Field>
    <p className="muted fl-help">{(() => { const h = SECRET_SOURCES.find(([v]) => v === src)?.[2]; return typeof h === "function" ? h() : h; })()} Never saved in the flow: run history, step outputs and results show <code className="inline-code" data-nofile>••••••••</code>. Tools get it through <code className="inline-code" data-nofile>{`{{inputs.${i.name}}}`}</code> in their arguments; AI steps should not use it.</p>
    {usesKc && <>
      <div className="fl-row">
        {isWindows() ? <p className="muted fl-help">Credential Manager › Windows Credentials. SpecWright's entries are named <code className="inline-code" data-nofile>service/account</code>; to read one another program saved, put its <b>Internet or network address</b> under Service.</p>
          : <Field label="Keychain item" hint={k.kind === "internet" ? "An internet password saved for a server (e.g. by Safari)" : "A password saved under a service and account"}><select value={k.kind || ""} onChange={(e) => setK({ kind: e.target.value })}><option value="">Password (service + account)</option><option value="internet">Internet password (server + account)</option></select></Field>}
      </div>
      <div className="fl-row">
        <Field label={k.kind === "internet" ? "Server" : "Service"}><input value={k.service || ""} placeholder={k.kind === "internet" ? "github.com" : "SpecWright"} onChange={(e) => setK({ service: e.target.value.trim() })} /></Field>
        <Field label="Account"><input value={k.account || ""} placeholder={i.name} onChange={(e) => setK({ account: e.target.value.trim() })} /></Field>
      </div>
      <div className="fl-row fl-secret-kc">
        <button type="button" className="btn secondary small" onClick={check}>Check the {kcName()}</button>
        {st && <span className={st.found ? "ok-text" : "muted"}>{!st.available ? "No keychain on this computer" : st.found ? `✓ Found: ${st.where}` : `Nothing in ${st.where} yet`}</span>}
      </div>
      {k.kind !== "internet" && <div className="fl-row fl-secret-kc">
        <input type="password" value={val} autoComplete="new-password" data-1p-ignore data-lpignore="true" placeholder="Save a value now (optional)" aria-label={`Value to save in the ${kcName()}`} onChange={(e) => setVal(e.target.value.replace(/[\r\n]/g, ""))} />
        <button type="button" className="btn secondary small" disabled={!val} onClick={save}>Save to {kcName()}</button>
      </div>}
      {!isWindows() && k.kind !== "internet" && (k.service || "SpecWright") !== "SpecWright" && <p className="muted fl-help">macOS asks once before reading an item another app saved (the prompt names <code className="inline-code" data-nofile>security</code>): choose <b>Always Allow</b> so later runs don't ask.</p>}
      {!isWindows() && k.kind === "internet" && <p className="muted fl-help">macOS asks once before reading it (the prompt names <code className="inline-code" data-nofile>security</code>): choose <b>Always Allow</b> so later runs don't ask.</p>}
    </>}
    {usesEnv && <Field label="Environment variable" hint={src === "auto" ? `Optional: tried when the ${kcName()} has nothing` : undefined}><input value={i.env || ""} placeholder={i.name.toUpperCase()} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => set({ env: e.target.value.replace(/[^A-Za-z0-9_]/g, "") || undefined })} /></Field>}
  </div>;
}

// "🔒 Use a secret" next to a tool argument: an existing secret input, or a
// new one (asked in the run form) named after the argument.
function SecretPick({ args, p }) {
  const flow = fl().flow, secrets = flow.inputs.filter((i) => i.type === "secret");
  const pick = (v) => {
    if (!v) return;
    let name = v;
    if (v === "+new") {
      const base = p.name.toLowerCase().replace(/[^a-z0-9_]/g, "_").replace(/^_+|_+$/g, "") || "secret";
      name = base; let k = 2; while (flow.inputs.some((i) => i.name === name)) name = base + "_" + k++;
      flow.inputs.push({ name, label: p.name.replace(/[_-]+/g, " ").replace(/^./, (c) => c.toUpperCase()), type: "secret", required: true });
      toast(`Added the secret input “${name}”. Choose where its value comes from under Inputs (run form, ${kcName()}, environment, or a dialog).`);
    }
    args[p.name] = `{{inputs.${name}}}`; changed();
  };
  return <select className="fl-use fl-secret-pick" value="" aria-label={`Use a secret for ${p.name}`} title={`Fill this argument from a secret (asked at run time or read from the ${kcName()})`} onChange={(e) => pick(e.target.value)}>
    <option value="">🔒 Secret…</option>{secrets.map((i) => <option key={i.name} value={i.name}>{i.label || i.name}</option>)}<option value="+new">+ New secret input</option></select>;
}

// --- variable chips: insert {{…}} where the cursor is ---------------------------------

let lastField = null;
const track = { onFocus: (e) => { lastField = e.target; } };
function insertVar(v) { insertText(`{{${v}}}`); }
function insertText(text, replace) {
  const el = lastField;
  if (!el || !document.body.contains(el)) return toast("Click in a field first, then pick a variable.");
  if (replace) { el.setSelectionRange(0, el.value.length); }
  const s = el.selectionStart ?? el.value.length, e = el.selectionEnd ?? s;
  const val = el.value.slice(0, s) + text + el.value.slice(e);
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, val);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.focus(); el.setSelectionRange(s + text.length, s + text.length);
}

function VarChips({ node, extra = [] }) {
  const f = fl(), flow = f.flow;
  const anc = node ? [...ancestors(flow, node.id)].filter((id) => id !== "start") : [];
  const fed = node && parentsOf(flow, node.id).some((p) => p !== "start");
  const vars = [...flow.inputs.filter((i) => i.type !== "secret" || node?.type !== "ai").map((i) => ["inputs." + i.name, (i.type === "secret" ? "🔒 " : "") + (i.label || i.name)]),
    ...anc.flatMap((id) => { const r = stepRef(flow, id), l = nodeOf(id)?.title || nodeOf(id)?.tool || id; return [[r + ".buffer_output", l + " · text"], [r + ".output_file", l + " · file"]]; }),
    ...(fed && node.type === "tool" ? [["input_file", "connected steps, as a file"]] : []),
    ...(node && (loopInfo(flow).stack[node.id] || []).length ? [["item", "↻ item"], ["item_index", "↻ item no."], ["item_count", "↻ item count"]] : []),
    // nested loops: each loop's item by the loop's name
    ...(node && (loopInfo(flow).stack[node.id] || []).length > 1 ? loopInfo(flow).stack[node.id].flatMap((l) => { const r = stepRef(flow, l), t = nodeOf(l)?.title || l; return [[r + ".item", "↻ " + t + " · item"], [r + ".item_index", "↻ " + t + " · no."]]; }) : []),
    ["context", "context"], ["skills", "skills"], ...extra.map((v) => [v, v])];
  return <div className="fl-vars"><span className="muted">Insert:</span>{vars.map(([v, l]) => <button key={v} type="button" className="fl-var" title={`{{${v}}}`} onMouseDown={(e) => e.preventDefault()} onClick={() => insertVar(v)}>{l}</button>)}</div>;
}

// What an edge carries, shown on the connection: the arguments of the
// target that read the source, "AI" for a described step, "stdin" for a tool
// that takes it on standard input only.
function edgeLabel(a, b, e) {
  if (a.type === "decision") return e?.branch ? "◆ " + branchName(a, e.branch) : "◆ always";
  if (b.type !== "tool") return "";
  const esc = (x) => x.replace(/[-.*+?^${}()|[\]\\]/g, "\\$&"), r = esc(stepRef(fl().flow, a.id)), id = esc(a.id);
  const re = new RegExp("\\{\\{\\s*" + (a.type === "start" ? "inputs\\." : "(steps\\." + id + "|input_file|input|(" + r + "|" + id + ")\\.(output_file|buffer_output))") + "(?=[.}\\s])");
  const used = Object.entries(b.args || {}).filter(([, v]) => re.test(v)).map(([k]) => k);
  if (used.length) return "→ " + used.join(", ");
  if ((b.instruction || "").trim()) return "→ AI";
  return a.type === "start" ? "" : "→ piped in";
}

// --- canvas ------------------------------------------------------------------------------

function Canvas() {
  const f = fl(), flow = f.flow, box = useRef(null), [drag, setDrag] = useState(null), [link, setLink] = useState(null), [band, setBand] = useState(null);
  const sel = new Set(selectedIds());
  const ts = tools();
  const z = f.zoom;
  const toLocal = (e) => { const r = box.current.getBoundingClientRect(); return { x: (e.clientX - r.left + box.current.scrollLeft) / z, y: (e.clientY - r.top + box.current.scrollTop) / z }; };
  useEffect(() => {
    if (!drag && !link && !band) return;
    const move = (e) => {
      if (drag?.group) { const p = toLocal(e), dx = p.x - drag.p0.x, dy = p.y - drag.p0.y; for (const [id, s0] of Object.entries(drag.group)) { const n = nodeOf(id); if (n) { n.x = Math.max(0, snap(s0.x + dx)); n.y = Math.max(0, snap(s0.y + dy)); } } drag.moved = true; update(); }
      else if (drag) { const p = toLocal(e); const n = nodeOf(drag.id); if (n) { n.x = Math.max(0, snap(p.x - drag.dx)); n.y = Math.max(0, snap(p.y - drag.dy)); drag.moved = true; update(); } }
      if (band) setBand({ ...band, ...toLocal(e), moved: true });
      if (link) setLink({ ...link, ...toLocal(e) });
    };
    const up = (e) => {
      if (drag?.moved) changed();
      if (band) { // select the steps the rectangle touches
        if (band.moved) {
          const x1 = Math.min(band.x0, band.x), x2 = Math.max(band.x0, band.x), y1 = Math.min(band.y0, band.y), y2 = Math.max(band.y0, band.y);
          const hit = fl().flow.nodes.filter((n) => n.type !== "start" && n.x < x2 && n.x + NODE_W > x1 && n.y < y2 && n.y + 64 > y1).map((n) => n.id);
          setSelection(band.add ? [...selectedIds(), ...hit] : hit);
        } else { f.sel = null; f.multi = []; f.selEdge = null; update(); }
        setBand(null);
      }
      if (link) { const el = document.elementFromPoint(e.clientX, e.clientY)?.closest("[data-node]"); if (el) connect(link.from, el.dataset.node); }
      setDrag(null); setLink(null);
    };
    window.addEventListener("mousemove", move); window.addEventListener("mouseup", up);
    return () => { window.removeEventListener("mousemove", move); window.removeEventListener("mouseup", up); };
  }, [drag, link, band]);
  useEffect(() => {
    const busy = (e) => e.target?.closest?.("input, textarea, select, [contenteditable=true]") || document.querySelector(".drawer-scrim, .file-preview, .ai-designer") || String(window.getSelection?.() || "").trim();
    const key = (e) => {
      if (busy(e)) return;
      if ((e.key === "Delete" || e.key === "Backspace") && (f.sel || f.selEdge != null)) { e.preventDefault(); removeSelected(); }
      if (e.key === "Escape") { f.sel = null; f.multi = []; f.selEdge = null; update(); }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "a") { e.preventDefault(); setSelection(fl().flow.nodes.filter((n) => n.type !== "start").map((n) => n.id)); }
      // ⌘C / ⌘V as keys too: the desktop app may have no Edit menu to fire copy and paste events
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "c" && selectedIds().some((id) => nodeOf(id)?.type !== "start")) { e.preventDefault(); copySteps(); }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "v") { e.preventDefault(); pasteFromClipboard(); }
    };
    // copy and paste through the system clipboard (no permission needed)
    const copy = (e) => { if (!busy(e)) copySteps(e); };
    const paste = (e) => { if (busy(e)) return; const c = readClip(e.clipboardData?.getData("text/plain") || ""); if (c) { e.preventDefault(); S.flowClip = c; pasteSteps(c); } };
    window.addEventListener("keydown", key); window.addEventListener("copy", copy); window.addEventListener("paste", paste);
    return () => { window.removeEventListener("keydown", key); window.removeEventListener("copy", copy); window.removeEventListener("paste", paste); };
  }, []);
  const wheel = (e) => { if (!e.ctrlKey && !e.metaKey) return; e.preventDefault(); f.zoom = Math.min(1.6, Math.max(0.4, +(z - e.deltaY * 0.002).toFixed(2))); update(); };
  const issuesFor = (id) => f.issues.filter((i) => i.node === id);
  const W = Math.max(1400, ...flow.nodes.map((n) => n.x + NODE_W + 400)), H = Math.max(800, ...flow.nodes.map((n) => n.y + 400));
  const port = (n, out) => ({ x: n.x + (out ? NODE_W : 0), y: n.y + PORT_Y });
  const path = (a, b) => { const dx = Math.max(40, Math.abs(b.x - a.x) / 2); return `M${a.x},${a.y} C${a.x + dx},${a.y} ${b.x - dx},${b.y} ${b.x},${b.y}`; };
  const loops = loopInfo(flow);
  // a Gather loops back to its Loop: a dashed path under the region, "next item"
  const back = Object.entries(loops.gather).map(([l, g]) => { const a = nodeOf(g), b = nodeOf(l); if (!a || !b) return null; const region = flow.nodes.filter((x) => (loops.stack[x.id] || []).includes(l) || x.id === l || x.id === g); const low = Math.max(...region.map((x) => x.y)) + 92; const ax = a.x + NODE_W / 2, bx = b.x + NODE_W / 2; return { k: l, d: `M${ax},${a.y + 64} C${ax},${low} ${bx},${low} ${bx},${b.y + 64}`, x: (ax + bx) / 2, y: low - 12 }; }).filter(Boolean);
  return (
    <div className="fl-canvas" ref={box} onWheel={wheel}
      onDragOver={(e) => { if ([...e.dataTransfer.types].includes("application/x-flow-step")) e.preventDefault(); }}
      onDrop={(e) => { const spec = e.dataTransfer.getData("application/x-flow-step"); if (!spec) return; e.preventDefault(); const p = toLocal(e); f.sel = null; addNode(JSON.parse(spec), p.x - NODE_W / 2, p.y - 24); }}
      onMouseDown={(e) => { // empty canvas: draw a rectangle to select steps
        if (e.button !== 0 || !(e.target === box.current || e.target.classList.contains("fl-plane") || e.target.classList.contains("fl-edges"))) return;
        const p = toLocal(e); setBand({ x0: p.x, y0: p.y, x: p.x, y: p.y, add: e.shiftKey || e.metaKey || e.ctrlKey }); }}>
      <div className="fl-plane" style={{ width: W, height: H, transform: `scale(${z})` }}>
        <svg className="fl-edges" width={W} height={H}>
          {flow.edges.map((e, k) => { const a = nodeOf(e.from), b = nodeOf(e.to); if (!a || !b) return null; const d = path(port(a, true), port(b, false)); return <g key={k} className={"fl-edge" + (f.selEdge === k ? " sel" : "")} onMouseDown={(ev) => { ev.stopPropagation(); f.selEdge = k; f.sel = null; update(); }}><path d={d} className="hit" /><path d={d} className="line" markerEnd="url(#fl-arrow)" />{(() => { const lab = edgeLabel(a, b, e); if (!lab) return null; const p1 = port(a, true), p2 = port(b, false); return <text x={(p1.x + p2.x) / 2} y={(p1.y + p2.y) / 2 - 6} className="fl-edge-label" textAnchor="middle">{lab}</text>; })()}</g>; })}
          {back.map((b) => <g key={"loop-" + b.k} className="fl-loopback"><path d={b.d} markerEnd="url(#fl-arrow)" /><text x={b.x} y={b.y} textAnchor="middle">next item ↺</text></g>)}
          {link && (() => { const a = nodeOf(link.from); return a && link.x != null ? <path d={path(port(a, true), { x: link.x, y: link.y })} className="fl-linking" /> : null; })()}
          {band?.moved && <rect className="fl-band" x={Math.min(band.x0, band.x)} y={Math.min(band.y0, band.y)} width={Math.abs(band.x - band.x0)} height={Math.abs(band.y - band.y0)} />}
          <defs><marker id="fl-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" /></marker></defs>
        </svg>
        {flow.nodes.map((n) => {
          const is = issuesFor(n.id), err = is.some((i) => i.level === "error");
          const sub = n.type === "tool" ? (n.instruction?.trim() ? "✦ " + n.instruction.trim().slice(0, 44) : toolLabel(n.tool)) : n.type === "start" ? `${flow.inputs.length} input${flow.inputs.length === 1 ? "" : "s"}` : n.type === "transform" ? (f.ops?.ops?.[n.op]?.name || n.op) : n.type === "merge" ? (MERGE_MODES.find(([m]) => m === n.mode)?.[1] || n.mode) : n.type === "output" ? (n.format || "").toUpperCase() + " · " + (n.actions || []).map((a) => a.type).join(", ") : n.type === "ai" ? (n.prompt || "").slice(0, 40) || "No prompt yet"
            : n.type === "loop" ? (SPLIT_MODES.find(([m]) => m === (n.split || "auto"))?.[1] || n.split) + (loops.gather[n.id] ? " → " + (nodeOf(loops.gather[n.id])?.title || loops.gather[n.id]) : " · no Gather yet")
            : n.type === "gather" ? GATHER_MODES.find(([m]) => m === (n.mode || "concat"))?.[1] || n.mode
            : n.type === "decision" ? `${(n.branches || []).length} path${(n.branches || []).length === 1 ? "" : "s"} + otherwise` + (n.mode === "all" ? " · every match" : "")
            : n.type === "filter" ? (n.mode === "drop" ? "Drop" : "Keep") + ` rows · ${condCount(n.cond)} condition${condCount(n.cond) === 1 ? "" : "s"}`
            : n.type === "aggregate" ? (n.mode === "window" ? "Window" : "Group") + ((n.groupBy || []).length ? " by " + n.groupBy.join(", ") : " · all rows") : "";
          const inLoop = loops.stack[n.id] || [], loopName = inLoop.length ? nodeOf(inLoop[inLoop.length - 1])?.title || inLoop[inLoop.length - 1] : "";
          return (
            <div key={n.id} data-node={n.id} className={"fl-node t-" + n.type + (f.sel === n.id || sel.has(n.id) ? " sel" : "") + (err ? " has-err" : is.length ? " has-warn" : "") + (link ? " linking" : "") + (inLoop.length ? " in-loop" : "")}
              style={{ left: n.x, top: n.y, width: NODE_W }}
              onMouseDown={(e) => {
                if (e.button !== 0 || e.target.closest(".fl-port")) return;
                const p = toLocal(e);
                if (e.shiftKey || e.metaKey || e.ctrlKey) { const cur = selectedIds(); setSelection(cur.includes(n.id) ? cur.filter((x) => x !== n.id) : [...cur, n.id]); return; } // add or remove
                if (sel.has(n.id) && sel.size > 1) { f.sel = n.id; update(); setDrag({ group: Object.fromEntries([...sel].map((id) => [id, { x: nodeOf(id).x, y: nodeOf(id).y }])), p0: p }); return; } // move them all
                f.sel = n.id; f.multi = []; f.selEdge = null; update(); setDrag({ id: n.id, dx: p.x - n.x, dy: p.y - n.y }); }}>
              {n.type !== "start" && <span className="fl-port in" title="Input" />}
              <div className="fl-node-head"><StepIcon n={n} tools={ts} /><div className="fl-node-text"><b>{n.title || n.id}</b><small>{sub}</small></div>
                {is.length > 0 && <span className={"fl-badge " + (err ? "err" : "warn")} title={is.map((i) => i.message).join("\n")}>{is.length}</span>}</div>
              {inLoop.length > 0 && <div className="fl-loop-chip" title={`Runs once per item of ${loopName}${inLoop.length > 1 ? ` (nested ${inLoop.length} deep)` : ""}`}>↻ {inLoop.length > 1 ? "×" + inLoop.length + " " : ""}per item of {loopName}</div>}
              {n.type === "start" && flow.inputs.length > 0 && <div className="fl-node-body">{flow.inputs.slice(0, 5).map((i) => <code key={i.name}>{i.name}</code>)}{flow.inputs.length > 5 && <span className="muted">+{flow.inputs.length - 5}</span>}</div>}
              {n.type !== "output" && <span className="fl-port out" title="Drag to connect" onMouseDown={(e) => { e.stopPropagation(); e.preventDefault(); setLink({ from: n.id, ...toLocal(e) }); }} />}
            </div>
          );
        })}
      </div>
      {flow.nodes.length === 1 && <div className="fl-hint"><b>Start by dragging a tool or step here</b><span>Drag from a step's ● to another step to connect them. Delete removes the selected step or connection.</span><span>Select several steps with ⇧-click or by drawing a box on the canvas; ⌘C copies them, ⌘V pastes them here or into another flow.</span></div>}
      <div className="fl-zoom"><button className="icon-btn" aria-label="Zoom out" onClick={() => { f.zoom = Math.max(0.4, +(z - 0.1).toFixed(2)); update(); }}><Icon.minus /></button>
        <button className="fv-pct" onClick={() => { f.zoom = 1; update(); }}>{Math.round(z * 100)}%</button>
        <button className="icon-btn" aria-label="Zoom in" onClick={() => { f.zoom = Math.min(1.6, +(z + 0.1).toFixed(2)); update(); }}><Icon.plus /></button></div>
    </div>
  );
}

// --- palette ---------------------------------------------------------------------------

function Palette() {
  const f = fl(), match = (t) => !f.q || (t.name + " " + t.desc).toLowerCase().includes(f.q.toLowerCase());
  const ts = projectTools().filter(match), bs = builtinTools().filter(match);
  const item = (spec, label, sub, icon) => (
    <div key={label} className="fl-pal-item" draggable title={sub} role="button" tabIndex={0}
      onDragStart={(e) => { e.dataTransfer.setData("application/x-flow-step", JSON.stringify(spec)); e.dataTransfer.effectAllowed = "copy"; }}
      onClick={() => addNode(spec)} onKeyDown={(e) => e.key === "Enter" && addNode(spec)}>
      {icon}<span><b>{label}</b><small>{sub}</small></span></div>
  );
  return (
    <aside className="fl-palette" aria-label="Steps and tools">
      <div className="tree-folder fl-pal-head">Steps <button className="link" title="How each step works, with examples" onClick={() => openFlowGuide("")}>How to use them ↗</button></div>
      {STEP_TYPES.map((s) => item({ type: s.type }, s.name, s.help, <StepIcon n={{ type: s.type }} tools={[]} size={30} />))}
      <input className="fl-search" placeholder="Filter tools" value={f.q} onChange={(e) => { f.q = e.target.value; update(); }} />
      <div className="tree-folder">Project tools · {projectTools().length}</div>
      {ts.map((t) => item({ type: "tool", tool: t.name }, t.name, t.desc, <ToolIcon t={t} size={30} />))}
      {!projectTools().length && <p className="muted" style={{ padding: "0 6px" }}>No tools yet. Create one on the Tools screen.</p>}
      <div className="tree-folder" title="Built into SpecWright: they run directly in the project, without the AI">Built-in tools · {builtinTools().length}</div>
      {bs.map((t) => item({ type: "tool", tool: t.name }, toolRef(t.name), t.desc.replace(/^[^:]*:\s*/, ""), <ToolIcon t={t} size={30} />))}
      {!!f.q && !ts.length && !bs.length && <p className="muted" style={{ padding: "0 6px" }}>No tool matches “{f.q}”.</p>}
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
    {(flow.revision > 1 || flow.designed) && <p className="muted fl-help">{flow.revision > 1 && <>Version {flow.revision}{flow.parent ? <> of <button className="link" onClick={() => openFlow(flow.parent)}>{(f.list || []).find((x) => x.id === flow.parent)?.name || flow.parent}</button></> : ""}. </>}{flow.designed}</p>}
    <FlowInterface />
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
            <Field label="Type"><select value={i.type} onChange={(e) => { i.type = e.target.value; if (i.type === "secret") { delete i.default; i.files = []; fl_.length = 0; } else { delete i.source; delete i.keychain; delete i.env; } changed(); }}>{INPUT_TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
            <button className="icon-btn fl-del" title="Remove input" onClick={() => { flow.inputs.splice(k, 1); changed(); }}><Icon.x /></button>
          </div>
          <Field label="Label"><input value={i.label || ""} onChange={(e) => { i.label = e.target.value; changed(); }} /></Field>
          {["choice", "multichoice"].includes(i.type) && <Field label="Options (one per line)" hint={i.type === "multichoice" ? "People tick any number of them; the flow gets the ticked ones, one per line (a Loop can go through them)" : undefined}><textarea rows={3} value={(i.options || []).join("\n")} onChange={(e) => { i.options = e.target.value.split("\n"); changed(); }} /></Field>}
          {i.type === "secret" && <SecretSourceEditor i={i} />}
          {["file", "files"].includes(i.type) && <Field label="Accepted files" hint="e.g. .pdf,.md,image/*"><input value={i.accept || ""} onChange={(e) => { i.accept = e.target.value; changed(); }} /></Field>}
          {["text", "multiline", "number", "clipboard", "choice"].includes(i.type) && <Field label="Default value">{i.type === "multiline" || i.type === "clipboard" ? <textarea rows={3} value={i.default || ""} onChange={(e) => { i.default = e.target.value; changed(); }} onPaste={onPasteAttach(fl_)} /> : <input value={i.default || ""} onChange={(e) => { i.default = e.target.value; changed(); }} />}</Field>}
          {i.type === "multichoice" && (i.options || []).filter(Boolean).length > 0 && <div className="field"><span className="muted">Ticked by default</span><div className="mc-list">
            {(i.options || []).filter(Boolean).map((o) => { const on = (i.default || "").split("\n").includes(o); return <label key={o} className="check"><input type="checkbox" checked={on} onChange={(e) => { const cur = (i.default || "").split("\n").filter(Boolean).filter((x) => x !== o); i.default = (e.target.checked ? [...cur, o] : cur).sort((a, b) => i.options.indexOf(a) - i.options.indexOf(b)).join("\n"); changed(); }} /> {o}</label>; })}</div></div>}
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
        <ul><li><b>Into a field as text:</b> <code className="inline-code" data-nofile>{`{{${stepRef(flow, feeders[0].id)}.buffer_output}}`}</code> is the result itself.</li>
          <li><b>Into a field that wants a file</b> (like <code className="inline-code" data-nofile>content_file</code>): <code className="inline-code" data-nofile>{`{{${stepRef(flow, feeders[0].id)}.output_file}}`}</code> is the path of the file the result is saved in (in the run's results folder). The ← button picks this for fields with “file” in their name.</li>
          <li><b>From any earlier step,</b> not only the one connected here: use its tool's name (or its title, or its id), e.g. <code className="inline-code" data-nofile>{"{{html-report-generator.output_file}}"}</code>. The Insert chips below list them.</li>
          <li><code className="inline-code" data-nofile>{"{{input_file}}"}</code> still works: a file holding the results of every step connected into this one.</li>
          {!isBuiltin(t.name) && <li><b>Without a field:</b> tools that read piped data (like <code className="inline-code" data-nofile>jq</code> or <code className="inline-code" data-nofile>grep</code>) get it as their input, as with <code className="inline-code" data-nofile>a | b</code> in a terminal.</li>}
          {isBuiltin(t.name) && <li><b>Built-in tools</b> only read their fields: put the result into one (← button) or name a file from it.</li>}</ul></details></div>}
    {t.params.map((p) => <Field key={p.name} label={<>{p.name}{p.required && !aiFills ? " *" : ""} <span className="muted">· {p.type}</span>{extraVars == null && feeders.map((x) => <button key={x.id} type="button" className="fl-use" title={FILE_PARAM(p) ? `Give ${p.name} a file holding the result of ${x.title || x.id}` : `Put the result of ${x.title || x.id} into ${p.name}`} onClick={(e) => { e.preventDefault(); args[p.name] = feedFor(p, x.id); changed(); }}>← {x.title || x.id}{FILE_PARAM(p) ? " (as a file)" : ""}</button>)}{(PASSWORD_PARAM.test(p.name) || (!/bool|int|number|float/i.test(p.type || "") && !p.enum?.length && fl().flow.inputs.some((i) => i.type === "secret"))) && <SecretPick args={args} p={p} />}</>} hint={p.desc}>
    {p.enum?.length ? <select value={args[p.name] || ""} onChange={(e) => { args[p.name] = e.target.value; changed(); }}><option value="">{p.required ? "Choose…" : "(default)"}</option>{p.enum.map((v) => <option key={v} value={v}>{v}</option>)}{args[p.name] && !p.enum.includes(args[p.name]) && <option value={args[p.name]}>{args[p.name]}</option>}</select>
      : long(p) ? <textarea rows={3} value={args[p.name] || ""} placeholder={aiFills ? "AI fills this (or type a value to fix it)" : p.required ? "Required" : "Optional"} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => { args[p.name] = e.target.value; changed(); }} />
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
    f.preview[node.id] = r;
    await refreshShapes(); // sample data teaches the shapes of steps that do not declare one
    update();
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
    <div className="fl-insp-head"><StepIcon n={n} tools={tools()} size={36} /><div><h3>{n.title || n.id}</h3><small className="muted">{n.type === "tool" ? (isBuiltin(n.tool) ? "Built-in tool" : "Tool") : STEP_TYPES.find((t) => t.type === n.type)?.name || "Inputs"}</small></div>
      <span style={{ marginLeft: "auto", display: "flex", gap: 10 }}>{GUIDE_TYPES.has(n.type) && <button className="link" title="How this step works, with examples" onClick={() => openFlowGuide(n.type)}>How to use ↗</button>}
        {n.type !== "start" && <button className="link" title="Copy this step (⌘C); paste it with ⌘V here or in another flow" onClick={() => copySteps()}>Copy</button>}
        {n.type !== "start" && <button className="link" onClick={removeSelected}>Delete</button>}</span></div>
    {issues.map((i, k) => <div key={k} className={"banner " + (i.level === "error" ? "error" : "")}>{i.message}</div>)}
    {n.type === "start" ? <InputsEditor /> : <>
      <div className="fl-row">
        <Field label="Title"><input value={n.title || ""} onChange={(e) => set("title", e.target.value)} /></Field>
        <Field label="Step id" hint={`Later steps read it as {{${stepRef(fl().flow, n.id)}.buffer_output}} (text) or {{${stepRef(fl().flow, n.id)}.output_file}} (file)`}><input defaultValue={n.id} key={n.id} style={{ fontFamily: "var(--font-mono)" }} onBlur={(e) => e.target.value !== n.id && renameNode(n, e.target.value)} /></Field>
      </div>
      {n.type === "tool" && <>
        <Field label="Tool"><select value={n.tool || ""} onChange={(e) => { n.tool = e.target.value; n.args = {}; const t = tools().find((x) => x.name === n.tool); (t?.params || []).forEach((p) => { n.args[p.name] = ""; }); changed(); }}>
          <option value="">Choose…</option><ToolOptions /></select></Field>
        {isBuiltin(n.tool) && <p className="muted fl-builtin-desc">{(tools().find((x) => x.name === n.tool)?.desc || "").replace(/^[^:]*:\s*/, "")} <span>Built into SpecWright; runs in the project without the AI.</span></p>}
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
        <ExpectedReply n={n} set={set} />
        <div className="field fl-field"><span className="muted">Model</span><ModelPicker value={n.model || ""} onChange={(v) => set("model", v)} label="Model" /><small className="muted">Default: the model chosen when the flow runs, else the app's model.</small></div>
      </>}
      <DataPanel n={n} />
      {n.type === "decision" && <DecisionInspector n={n} set={set} />}
      {n.type === "filter" && <FilterInspector n={n} set={set} />}
      {n.type === "aggregate" && <AggregateInspector n={n} set={set} />}
      {n.type === "merge" && <>
        <Field label="How to merge"><select value={n.mode || "concat"} onChange={(e) => set("mode", e.target.value)}>{MERGE_MODES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
        {n.mode === "concat" && <Field label="Separator" hint="\n is a new line"><input value={n.separator ?? ""} onChange={(e) => set("separator", e.target.value)} /></Field>}
        {n.mode === "template" && <><Field label="Template"><textarea rows={6} value={n.template || ""} {...track} onChange={(e) => set("template", e.target.value)} /></Field><VarChips node={n} /></>}
        <p className="muted fl-help">Merges, in this order: {parents.map((p) => nodeOf(p)?.title || p).join(" → ") || "nothing yet; connect steps to it"}.</p>
        <Preview node={n} />
      </>}
      {n.type === "loop" && (() => {
        const g = loopInfo(fl().flow).gather[n.id];
        return <>
          <p className="muted fl-help">Every step connected after this one runs once per item, up to {g ? <b>{nodeOf(g)?.title || g}</b> : <>a <b>Gather</b> step (add one after the steps to repeat)</>}, which collects the results. In those steps use <code className="inline-code" data-nofile>{"{{item}}"}</code> (and <code className="inline-code" data-nofile>{"{{item.json.name}}"}</code> for JSON items), <code className="inline-code" data-nofile>{"{{item_index}}"}</code> and <code className="inline-code" data-nofile>{"{{item_count}}"}</code>. A tool or AI prompt connected after the loop gets the current item. With loops inside loops, use the loop's step id: <code className="inline-code" data-nofile>{`{{${stepRef(fl().flow, n.id)}.item}}`}</code>, <code className="inline-code" data-nofile>{`{{${stepRef(fl().flow, n.id)}.item.json.name}}`}</code>, <code className="inline-code" data-nofile>{`{{${stepRef(fl().flow, n.id)}.item_index}}`}</code>.</p>
          <LoopListFrom n={n} set={set} />
          <Field label="Split the list into items"><select value={n.split || "auto"} onChange={(e) => set("split", e.target.value)}>{SPLIT_MODES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
          {n.split === "regex" && <Field label="Pattern" hint="Each match is an item (its first group, if it has one), e.g. class (\w+)"><input value={n.expr || ""} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => set("expr", e.target.value)} /></Field>}
          <div className="fl-row">
            <Field label="Keep only items matching" hint="A regular expression; empty keeps all"><input value={n.filter || ""} style={{ fontFamily: "var(--font-mono)" }} placeholder="e.g. ^[A-Z]" onChange={(e) => set("filter", e.target.value)} /></Field>
            <Field label="At most" hint="items (up to 1000)"><input type="number" min={1} max={1000} value={n.max || ""} placeholder="1000" onChange={(e) => set("max", Math.max(0, Math.min(1000, +e.target.value || 0)))} /></Field>
          </div>
          <label className="check"><input type="checkbox" checked={!!n.unique} onChange={(e) => set("unique", e.target.checked)} /> Skip repeated items</label>
          <label className="check"><input type="checkbox" checked={!!n.continueOnError} onChange={(e) => set("continueOnError", e.target.checked)} /> Keep going when an item fails (its error is gathered)</label>
          <details><summary className="muted">The list (default: the output of the step before)</summary>
            <Field label="Use instead" hint="A template, e.g. {{steps.symbols.json.items}} or one name per line"><textarea rows={3} value={n.source || ""} placeholder="{{steps.…}}" {...track} onChange={(e) => set("source", e.target.value)} /></Field><VarChips node={n} /></details>
          <Preview node={n} />
        </>;
      })()}
      {n.type === "gather" && <>
        <p className="muted fl-help">Ends the loop {(() => { const li = loopInfo(fl().flow), l = Object.keys(li.gather).find((k) => li.gather[k] === n.id); return l ? <b>{nodeOf(l)?.title || l}</b> : <i>(connect it after a loop's steps)</i>; })()}: for each item it takes the result of the steps connected into it, then gathers them all. The steps after it run once, with everything.</p>
        <Field label="Gather as"><select value={n.mode || "concat"} onChange={(e) => set("mode", e.target.value)}>{GATHER_MODES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
        {(n.mode || "concat") === "concat" && <Field label="Separator" hint="\n is a new line (default: a blank line)"><input value={n.separator ?? ""} onChange={(e) => set("separator", e.target.value)} /></Field>}
        {n.mode === "template" && <><Field label="Template for each item" hint="{{input}} is the item's result; joined with new lines"><textarea rows={4} value={n.template || ""} placeholder="- **{{item}}**: {{input}}" {...track} onChange={(e) => set("template", e.target.value)} /></Field><VarChips node={n} extra={["input", "item", "item_index", "item_count"]} /></>}
        <Preview node={n} />
      </>}
      {n.type === "transform" && <>
        <Field label="Transformation"><select value={n.op || ""} onChange={(e) => set("op", e.target.value)}>
          {Object.entries(opGroups).map(([g, list]) => <optgroup key={g} label={g}>{list.map(([id, o]) => <option key={id} value={id}>{o.name}</option>)}</optgroup>)}</select></Field>
        {op.help && <p className="muted fl-help">{op.help}</p>}
        {op.expr && <Field label={op.expr}><input list={/^json-(path|template|stringify|parse)$/.test(n.op) ? "paths-" + n.id : undefined} data-path={/^json-(path|template|stringify|parse)$/.test(n.op) ? "1" : undefined} value={n.expr || ""} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => set("expr", e.target.value)} /></Field>}
        {op.replace && <Field label="Replace with"><input value={n.replace || ""} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => set("replace", e.target.value)} /></Field>}
        {op.template && <><Field label={op.ai ? "Instruction for the AI" : "Template"}><textarea value={n.template || ""} placeholder={op.ai ? "e.g. Turn {{input}} into a bulleted list of acceptance criteria." : n.op === "json-template" ? '{\n  "customer": "{{this.customer.name}}",\n  "orders": { "{{#each this.orders}}": {\n    "id": "{{this.id}}",\n    "lines": { "{{#each this.lines}}": {\n      "sku": "{{this.sku}}",\n      "order": "{{parent.id}}",\n      "n": "{{this_index}}"\n    } }\n  } }\n}' : "{{input}}"} style={n.op === "json-template" ? { fontFamily: "var(--font-mono)" } : undefined} rows={n.op === "json-template" ? 12 : 5} {...track} onChange={(e) => set("template", e.target.value)} /></Field><VarChips node={n} extra={n.op === "json-template" ? ["this", "this_index", "parent", "root"] : ["input"]} /></>}
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
            {a.type === "tool" && <><Field label="Tool"><select value={a.tool || ""} onChange={(e) => { a.tool = e.target.value; a.args = {}; changed(); }}><option value="">Choose…</option><ToolOptions /></select></Field>
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
  const f = fl(), n = f.sel ? nodeOf(f.sel) : null, e = !n && f.selEdge != null ? f.flow.edges[f.selEdge] : null, many = selectedIds();
  return <aside className="fl-inspector" aria-label="Inspector">{many.length > 1 ? <MultiInspector ids={many} /> : n ? <StepInspector key={n.id} n={n} /> : e ? <EdgeInspector key={f.selEdge} e={e} /> : <FlowSettings key={f.flow.id} />}</aside>;
}

function MultiInspector({ ids }) {
  const f = fl(), flow = f.flow, set = new Set(ids), inner = flow.edges.filter((e) => set.has(e.from) && set.has(e.to)).length;
  return <>
    <div className="fl-insp-head"><div><h3>{ids.length} steps selected</h3><small className="muted">{inner} connection{inner === 1 ? "" : "s"} between them</small></div>
      <button className="link" style={{ marginLeft: "auto" }} onClick={removeSelected}>Delete</button></div>
    <ul className="fl-multi">{ids.map((id) => { const n = nodeOf(id); return <li key={id}><StepIcon n={n} tools={tools()} size={22} /> <b>{n.title || n.id}</b> <span className="muted">{n.type === "tool" ? toolRef(n.tool) : n.type}</span>
      <button className="link" title="Remove from the selection" onClick={() => setSelection(ids.filter((x) => x !== id))}>✕</button></li>; })}</ul>
    <div className="row-actions">
      <button className="btn primary" onClick={() => copySteps()}>Copy</button>
      <button className="btn secondary" onClick={() => { copySteps(); pasteSteps(S.flowClip); }}>Duplicate</button>
      <button className="btn secondary" onClick={() => { copySteps(); pasteAsNewFlow(S.flowClip); }}>Copy to a new flow</button></div>
    <p className="muted fl-help">Drag one of them to move them all. ⌘C copies them with the connections between them (and the inputs they use); ⌘V pastes them into this flow, or open another flow and paste there. ⇧-click or ⌘-click adds or removes a step; drawing a box on the empty canvas selects the steps it touches; ⌘A selects all; Esc clears.</p>
  </>;
}

// --- decisions, filters, aggregates ---------------------------------------------------

const newRule = () => ({ field: "", op: "eq", value: "" });
const newCond = () => ({ match: "all", rules: [newRule()] });
const condCount = (c) => (c?.rules || []).reduce((k, r) => k + (r.group ? condCount(r.group) : 1), 0);
// the next path of a decision that leads nowhere yet
function nextBranch(a) {
  const used = new Set(fl().flow.edges.filter((x) => x.from === a.id).map((x) => x.branch));
  return [...(a.branches || []).map((x) => x.id), "else"].find((id) => !used.has(id)) || a.branches?.[0]?.id || "else";
}
function branchName(n, id) { return id === "else" ? "Otherwise" : (n?.branches || []).find((b) => b.id === id)?.label || id; }

// A connection: which path of a Decision it is on.
function EdgeInspector({ e }) {
  const f = fl(), a = nodeOf(e.from), b = nodeOf(e.to);
  if (!a || !b) return null;
  const set = (v) => { e.branch = v; changed(); };
  return <>
    <div className="fl-insp-head"><div><h3>{a.title || a.id} → {b.title || b.id}</h3><small className="muted">Connection</small></div>
      <button className="link" style={{ marginLeft: "auto" }} onClick={removeSelected}>Delete</button></div>
    {a.type === "decision" ? <>
      <Field label="Path" hint="The steps after this connection run only when the decision takes this path."><select value={e.branch || ""} onChange={(ev) => set(ev.target.value)}>
        <option value="">Always (not on a path)</option>
        {(a.branches || []).map((x) => <option key={x.id} value={x.id}>{x.label || x.id}</option>)}
        <option value="else">Otherwise (no path matched)</option></select></Field>
    </> : <p className="muted fl-help">{b.title || b.id} gets the output of {a.title || a.id}. Press Delete to remove the connection.</p>}
  </>;
}

// A condition: rules joined by all / any / none; a rule can be a group of its own.
function CondEditor({ cond, onChange, depth = 0, what = "the data", listId }) {
  const ops = fl().ops?.condOps || [];
  const c = cond || newCond();
  const put = () => onChange({ ...c });
  const setRule = (i, k, v) => { c.rules[i] = { ...c.rules[i], [k]: v }; put(); };
  return <div className={"fl-cond" + (depth ? " nested" : "")}>
    <div className="fl-cond-head"><select aria-label="Match" value={c.match || "all"} onChange={(e) => { c.match = e.target.value; put(); }}>{MATCHES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
      <span className="muted">{depth ? "these:" : "these conditions:"}</span>
      {depth > 0 && <button className="link" onClick={() => onChange(null)}>Remove group</button>}</div>
    {(c.rules || []).map((r, i) => r.group
      ? <CondEditor key={i} cond={r.group} depth={depth + 1} what={what} listId={listId} onChange={(g) => { if (g) c.rules[i] = { group: g }; else c.rules.splice(i, 1); put(); }} />
      : <div key={i} className="fl-rule">
        <input className="fl-rule-field" aria-label="Field" list={listId} data-path="1" placeholder="field, e.g. customer.tier (empty: the whole value)" value={r.field || ""} style={{ fontFamily: "var(--font-mono)" }} {...track} onChange={(e) => setRule(i, "field", e.target.value)} />
        <button className="link" aria-label="Remove condition" title="Remove" onClick={() => { c.rules.splice(i, 1); put(); }}>✕</button>
        <select aria-label="Comparison" value={r.op || "eq"} onChange={(e) => setRule(i, "op", e.target.value)}>{ops.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}</select>
        {!ops.find((o) => o.id === r.op)?.unary && <input className="fl-rule-value" aria-label="Value" placeholder={r.op === "between" ? "low, high" : r.op === "in" || r.op === "not_in" ? "a, b, c" : "value or {{…}}"} value={r.value || ""} {...track} onChange={(e) => setRule(i, "value", e.target.value)} />}
        <div className="fl-rule-opts">
          <label className="check"><input type="checkbox" checked={!!r.ignoreCase} onChange={(e) => setRule(i, "ignoreCase", e.target.checked)} /> ignore case</label>
          {/\[\*\]|\./.test(r.field || "") && <label className="check" title="When the field is a list (items[*].qty), every element must match, not just one"><input type="checkbox" checked={!!r.every} onChange={(e) => setRule(i, "every", e.target.checked)} /> every element of a list</label>}
        </div>
      </div>)}
    <div className="row-actions"><button className="btn secondary small" onClick={() => { (c.rules ||= []).push(newRule()); put(); }}>+ Condition</button>
      {depth < 4 && <button className="btn secondary small" onClick={() => { (c.rules ||= []).push({ group: { match: "any", rules: [newRule()] } }); put(); }}>+ Group</button>}</div>
    {depth === 0 && <p className="muted fl-help">A field is a path into {what}: <code className="inline-code" data-nofile>status</code>, <code className="inline-code" data-nofile>customer.address.city</code>, <code className="inline-code" data-nofile>items[0].sku</code>, <code className="inline-code" data-nofile>items[*].qty</code> (any element; tick “every element” for all). Leave it empty for the whole value. Values can use variables, and <code className="inline-code" data-nofile>{"{{this.<field>}}"}</code> compares two fields. Numbers compare as numbers, dates (2026-11-12) as text.</p>}
  </div>;
}

function RowsInput({ n, set }) {
  return <>
    <Field label="Rows" hint="JSON comes out as a JSON array; one row per line comes out the same way."><select value={n.split || "auto"} onChange={(e) => set("split", e.target.value)}>{ROW_FORMATS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
    <details><summary className="muted">Input (default: the output of the step before)</summary>
      <Field label="Use instead"><input value={n.source || ""} placeholder="{{steps.…}}" {...track} onChange={(e) => set("source", e.target.value)} /></Field></details>
  </>;
}

const listText = (a) => (a || []).join(", ");
const textList = (s) => String(s || "").split(",").map((x) => x.trim()).filter(Boolean);

function DecisionInspector({ n, set }) {
  const f = fl(), flow = f.flow;
  const targets = (id) => flow.edges.filter((e) => e.from === n.id && e.branch === id).map((e) => nodeOf(e.to)?.title || e.to);
  const put = () => set("branches", [...n.branches]);
  const nextId = () => { let k = (n.branches || []).length + 1; while ((n.branches || []).some((b) => b.id === "path" + k)) k++; return "path" + k; };
  return <>
    <p className="muted fl-help">Checks the data from the step before and sends the flow down a path: the steps connected on that path run, the others are skipped. The data passes on unchanged. Paths are checked in order; <b>Otherwise</b> runs when none matches.</p>
    <Field label="When several paths match"><select value={n.mode || "first"} onChange={(e) => set("mode", e.target.value)}><option value="first">Take the first one</option><option value="all">Take every one that matches</option></select></Field>
    {(n.branches || []).map((b, i) => <div key={b.id} className="fl-branch">
      <div className="fl-branch-head"><span className="fl-branch-n">{i + 1}</span>
        <input aria-label="Path name" value={b.label || ""} placeholder={b.id} onChange={(e) => { n.branches[i] = { ...b, label: e.target.value }; put(); }} />
        <button className="link" disabled={i === 0} title="Check earlier" onClick={() => { const x = n.branches.splice(i, 1)[0]; n.branches.splice(i - 1, 0, x); put(); }}>↑</button>
        <button className="link" aria-label="Remove path" title="Remove path" onClick={() => { n.branches.splice(i, 1); flow.edges.forEach((e) => { if (e.from === n.id && e.branch === b.id) e.branch = ""; }); put(); }}>✕</button></div>
      <CondEditor cond={b.when} what="the data" listId={"paths-" + n.id} onChange={(c) => { n.branches[i] = { ...b, when: c || newCond() }; put(); }} />
      <small className="muted">Leads to: {targets(b.id).join(", ") || "nothing yet: connect a step from this decision and choose this path on the connection"}</small>
    </div>)}
    <button className="btn secondary" onClick={() => { const id = nextId(); (n.branches ||= []).push({ id, label: "Path " + id.replace("path", ""), when: newCond() }); put(); }}>+ Path</button>
    <div className="fl-branch"><div className="fl-branch-head"><span className="fl-branch-n">∗</span><b>Otherwise</b></div>
      <small className="muted">Leads to: {targets("else").join(", ") || "nothing (the flow ends here when no path matches)"}</small></div>
    <details><summary className="muted">Input (default: the output of the step before)</summary>
      <Field label="Use instead"><input value={n.source || ""} placeholder="{{steps.…}}" {...track} onChange={(e) => set("source", e.target.value)} /></Field></details>
    <Preview node={n} />
  </>;
}

function FilterInspector({ n, set }) {
  return <>
    <Field label="Rows that match"><div className="seg">{[["keep", "Keep"], ["drop", "Drop"]].map(([v, l]) => <button key={v} type="button" aria-pressed={(n.mode || "keep") === v} onClick={() => set("mode", v)}>{l}</button>)}</div></Field>
    <CondEditor cond={n.cond} what="each row" listId={"paths-" + n.id} onChange={(c) => set("cond", c || newCond())} />
    <div className="fl-row">
      <Field label="Sort by" hint="Fields; -total sorts high to low"><input list={"paths-" + n.id} data-path="list" {...track} value={listText(n.orderBy)} placeholder="e.g. -total, id" onChange={(e) => set("orderBy", textList(e.target.value))} /></Field>
      <Field label="At most" hint="rows (empty: all)"><input type="number" min="0" value={n.max || ""} onChange={(e) => set("max", +e.target.value || 0)} /></Field>
    </div>
    <label className="check"><input type="checkbox" checked={!!n.unique} onChange={(e) => set("unique", e.target.checked)} /> Drop repeated rows</label>
    <RowsInput n={n} set={set} />
    <Preview node={n} />
  </>;
}

function AggregateInspector({ n, set }) {
  const fns = fl().ops?.aggFns || [], win = n.mode === "window";
  const ms = n.measures || [];
  const put = () => set("measures", [...ms]);
  return <>
    <Field label="Result"><div className="seg">{[["group", "One row per group"], ["window", "Every row, with its group's values"]].map(([v, l]) => <button key={v} type="button" aria-pressed={(n.mode || "group") === v} onClick={() => set("mode", v)}>{l}</button>)}</div></Field>
    <p className="muted fl-help">{win ? "Like SQL's OVER (PARTITION BY … ORDER BY …): every row is kept, with values over its group added: totals, ranks, running sums, the previous or next row's value." : "Like SQL's GROUP BY: one row per distinct combination of the group fields, with the values calculated over its rows. With no group fields, one row of totals."}</p>
    <Field label={win ? "Partition by" : "Group by"} hint="Fields, comma-separated: region, customer.tier"><input list={"paths-" + n.id} data-path="list" {...track} value={listText(n.groupBy)} placeholder="e.g. region, customer.tier" onChange={(e) => set("groupBy", textList(e.target.value))} /></Field>
    <div className="fl-measures">
      <div className="muted">Values to calculate</div>
      {ms.map((m, i) => { const fn = fns.find((x) => x.id === m.fn) || {}; return <div key={i} className="fl-measure">
        <select aria-label="Function" value={m.fn} onChange={(e) => { ms[i] = { ...m, fn: e.target.value }; put(); }}>
          <optgroup label="Over the group">{fns.filter((x) => !x.window).map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}</optgroup>
          {win && <optgroup label="Window (per row)">{fns.filter((x) => x.window).map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}</optgroup>}</select>
        {!fn.noField || m.fn === "count" ? <input aria-label="Field" list={"paths-" + n.id} data-path="1" {...track} placeholder={m.fn === "count" ? "field (empty: rows)" : "field, e.g. total"} value={m.field || ""} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => { ms[i] = { ...m, field: e.target.value }; put(); }} /> : <span />}
        <input aria-label="Name" placeholder="name in the result" value={m.as || ""} onChange={(e) => { ms[i] = { ...m, as: e.target.value }; put(); }} />
        <button className="link" aria-label="Remove value" onClick={() => { ms.splice(i, 1); put(); }}>✕</button>
        {fn.arg && <input className="fl-measure-arg" aria-label={fn.arg} placeholder={fn.arg} value={m.arg || ""} onChange={(e) => { ms[i] = { ...m, arg: e.target.value }; put(); }} />}
      </div>; })}
      <button className="btn secondary small" onClick={() => { ms.push({ fn: "sum", field: "", as: "" }); put(); }}>+ Value</button>
    </div>
    <div className="fl-row">
      <Field label={win ? "Order within the group" : "Sort the result by"} hint="Fields; -total for high to low"><input value={listText(n.orderBy)} placeholder="e.g. -total" onChange={(e) => set("orderBy", textList(e.target.value))} /></Field>
      <Field label="At most" hint="rows (empty: all)"><input type="number" min="0" value={n.max || ""} onChange={(e) => set("max", +e.target.value || 0)} /></Field>
    </div>
    <p className="muted fl-help">Fields are paths into each row (<code className="inline-code" data-nofile>customer.tier</code>); a list path like <code className="inline-code" data-nofile>items[*].qty</code> counts every element, so you can sum nested values.</p>
    <RowsInput n={n} set={set} />
    <Preview node={n} />
  </>;
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
  useEffect(() => { if (S.ws) { loadList(); refreshExtensions(); loadBuiltins(); } }, [S.ws?.root]);
  useEffect(() => { if (S.ws && f.list?.length && !f.flow && !f.id) openFlow(f.list[0].id); }, [f.list]);
  useEffect(() => () => { if (fl().dirty) save(); }, []);
  if (!S.ws) return <NeedWorkspace what="the flow designer" />;
  return (
    <div className="files flows">
      <nav className="ftree" aria-label="Flows">
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "0 8px 8px" }}><h1>Flows</h1><button className="btn primary" onClick={newFlow}>+ New</button></div>
        <div style={{ padding: "0 8px 8px", display: "grid", gap: 4, justifyItems: "start" }}><button className="link" title="Describe what you want; the AI designer proposes flows built from your tools" onClick={() => openDesigner("new")}><Icon.spark /> New flow with AI</button>
          {S.flowClip && <button className="link" title={`${S.flowClip.nodes.length} copied step(s) from ${S.flowClip.from || "a flow"}`} onClick={() => pasteFromClipboard(true)}>Paste {S.flowClip.nodes.length} step{S.flowClip.nodes.length === 1 ? "" : "s"} as a new flow</button>}</div>
        {(f.list || []).map((x) => <button key={x.id} className={"run-item" + (x.id === f.flow?.id ? " active" : "")} onClick={() => openFlow(x.id)}>
          <span className="run-item-top"><b>{x.name}</b>{x.revision > 1 && <span className="badge" title={"A version of " + ((f.list || []).find((y) => y.id === x.parent)?.name || x.parent)}>v{x.revision}</span>}</span><small>{x.steps} step{x.steps === 1 ? "" : "s"} · {x.outputs} output{x.outputs === 1 ? "" : "s"}{x.updated ? " · " + new Date(x.updated).toLocaleDateString() : ""}</small></button>)}
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
        <div className="fl-work"><Palette /><Canvas /><Inspector />{f.ai?.open && <AIDesigner />}</div>
      </div> : <section className="view"><div className="card"><h3>Design a flow</h3>
        <p className="wf-desc">Drag tools onto a canvas and connect them. Give the flow inputs (text, files, clipboard), a custom context and skills; merge and transform data between steps; and produce outputs as Markdown, text, HTML, CSV or JSON that are saved, passed to a tool, copied or shown.</p>
        <p className="wf-desc">New to flows? The <button className="link" onClick={() => openFlowGuide("")}>Flow step guide</button> explains every step with examples you can open here.</p>
        <div className="row-actions"><button className="btn primary" onClick={() => openDesigner("new")}><Icon.spark /> Describe it to the AI designer</button><button className="btn secondary" onClick={newFlow}>Create a flow</button><button className="btn secondary" onClick={() => imp.current?.click()}>Import a flow…</button></div></div>
        {f.ai?.open && <AIDesigner inline />}</section>}
    </div>
  );
}


// --- data shapes: what each step gets and gives ------------------------------------

const SHAPE_SOURCE = {
  declared: ["Declared", "badge-ok", "declared by the tool, the expected reply or the flow's inputs"],
  derived: ["Worked out", "badge-info", "worked out from the steps before it"],
  sample: ["Learned", "badge-warn", "learned from sample data or the flow's last run; declare it to be sure"],
  unknown: ["Not known yet", "", ""],
};
const jsonPathVar = (base, p) => `${base}.buffer_output.json${p.startsWith("[") ? "" : "."}${p}`;

// a field list; clicking a field puts it where the cursor is
function ShapeTree({ schema, pick, max = 40 }) {
  const [all, setAll] = useState(false);
  if (!schema) return null;
  const ps = schemaPaths(schema, 400);
  if (!ps.length) return <p className="muted fl-help">{typeName(schema) === "string" ? "Text (not JSON)." : "A " + typeName(schema) + " value."}{schema.description ? " " + schema.description : ""}</p>;
  const shown = all ? ps : ps.slice(0, max);
  return <div className="fl-shape">
    {shown.map((p) => <button key={p.path} type="button" className="fl-shape-row" style={{ paddingLeft: 6 + p.depth * 14 }} title={(p.description ? p.description + " · " : "") + p.path + " · click to use it"}
      onMouseDown={(e) => e.preventDefault()} onClick={() => pick(p.path)}>
      <code>{p.path.split(".").pop()}</code><span className="fl-shape-type">{p.type}</span>{p.description && <small className="muted">{p.description}</small>}</button>)}
    {ps.length > max && <button className="link" onClick={() => setAll(!all)}>{all ? "Show fewer" : `Show all ${ps.length} fields`}</button>}
  </div>;
}

function DataPanel({ n }) {
  const f = fl(), flow = f.flow, sh = f.shapes?.[n.id];
  if (!sh || n.type === "start") return null;
  const parents = parentsOf(flow, n.id), one = parents.length === 1 ? parents[0] : null;
  const inLoop = !!one && nodeOf(one)?.type === "loop";
  const rowsStep = n.type === "filter" || n.type === "aggregate", dataStep = rowsStep || n.type === "decision";
  const gets = rowsStep ? sh.rows : n.type === "loop" ? sh.item : sh.in;
  // a field of what the step gets: into a path field as it is, into a template as a variable
  const pickIn = (p) => {
    const el = lastField, isPath = el && document.body.contains(el) ? el.dataset?.path : "";
    if (isPath) return insertText(isPath === "list" && el.value.trim() ? el.value.replace(/,?\s*$/, ", ") + p : p, true);
    if (dataStep || n.type === "loop") return insertVar("this." + p);
    if (inLoop) return insertVar("item.json" + (p.startsWith("[") ? "" : ".") + p);
    if (one) return insertVar(jsonPathVar(one, p));
    toast("Connect one step into this one to use its fields.");
  };
  const pickOut = (p) => { const v = `{{${jsonPathVar(n.id, p)}}}`; navigator.clipboard?.writeText(v).catch(() => {}); toast(`Copied ${v}: paste it into a later step.`); };
  const [label, cls, why] = SHAPE_SOURCE[sh.source] || SHAPE_SOURCE.unknown;
  const unknownHint = n.type === "tool" ? <>Declare the tool's output on the <button className="link" onClick={() => { S.view = "tools"; update(); }}>Tools</button> screen, or give sample output (Try it with sample data) or run the flow once to learn it.</>
    : n.type === "ai" ? "Set the expected reply above, or run the flow once to learn it." : "Connect a step whose output is known.";
  return <details className="fl-data" open>
    <summary><b>Data</b> <span className="muted">the fields this step gets and gives</span></summary>
    <datalist id={"paths-" + n.id}>{(gets ? schemaPaths(gets, 300) : []).map((p) => <option key={p.path} value={p.path}>{p.type}{p.description ? " · " + p.description : ""}</option>)}</datalist>
    <div className="fl-data-sec">
      <div className="fl-data-head">{rowsStep ? "Each row it gets" : n.type === "loop" ? "Each item" : "Gets"}{one && <small className="muted">from {nodeOf(one)?.title || one}</small>}</div>
      {gets ? <><code className="fl-compact" title="The shape, compactly">{compact(gets)}</code><ShapeTree schema={gets} pick={pickIn} />
        <small className="muted">Click a field to use it where you were typing{dataStep || n.type === "loop" ? " (as a path in a field box, else as {{this.<field>}})" : inLoop ? " ({{item.json.<field>}})" : one ? ` ({{${one}.buffer_output.json.<field>}})` : ""}.</small></>
        : <p className="muted fl-help">{parents.length ? "Not known yet: " + (parents.length > 1 ? "several steps are connected, and their outputs arrive one after another as text." : "the step before has no known shape.") : "Nothing is connected into it."}</p>}
    </div>
    {n.type !== "output" && <div className="fl-data-sec">
      <div className="fl-data-head">Gives <span className={"badge " + cls} title={why}>{label}</span></div>
      {sh.out ? <><code className="fl-compact">{compact(sh.out)}</code><ShapeTree schema={sh.out} pick={pickOut} />
        <small className="muted">Later steps read a field as <code className="inline-code" data-nofile>{`{{${n.id}.buffer_output.json.<field>}}`}</code>; click one to copy it.</small></>
        : <p className="muted fl-help">{unknownHint}</p>}
    </div>}
  </details>;
}

// An AI step's expected reply: shown to later steps and, only when set, sent to the AI.
function ExpectedReply({ n, set }) {
  const [text, setText] = useState(n.outputSchema ? JSON.stringify(n.outputSchema, null, 2) : "");
  const apply = async () => {
    if (!text.trim()) { if (n.outputSchema) set("outputSchema", undefined); return; }
    try { const s = await backend.ParseSchema(text); set("outputSchema", s); setText(JSON.stringify(s, null, 2)); } catch (e) { toast("Expected reply: " + errMsg(e)); }
  };
  return <details className="fl-expect" open={!!n.outputSchema}>
    <summary><b>Expected reply</b> <span className="muted">{n.outputSchema ? compact(n.outputSchema) : "optional: JSON the next steps can rely on"}</span></summary>
    <p className="muted fl-help">Paste an example of the JSON you want back, or a JSON Schema. Later steps then know its fields, and the AI is asked to reply with only JSON in this shape. It is the only shape ever sent to the AI, so leave it empty for free-form answers.</p>
    <textarea rows={6} value={text} placeholder={'{"type": "bug", "title": "Checkout crashes", "priority": 2}'} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => setText(e.target.value)} onBlur={apply} />
    {n.outputSchema && <div className="row-actions"><button className="link" onClick={() => { setText(""); set("outputSchema", undefined); }}>Clear</button></div>}
  </details>;
}

// The flow's interface: its inputs and the shapes of its outputs, for whoever integrates it.
function FlowInterface() {
  const f = fl(), flow = f.flow, sh = f.shapes || {};
  const outs = flow.nodes.filter((n) => n.type === "output");
  const iface = () => ({ flow: flow.id, inputs: sh.start?.out || { type: "object" }, outputs: Object.fromEntries(outs.map((n) => [n.title || n.id, { format: n.format, ...(n.format === "json" && sh[n.id]?.out ? { schema: sh[n.id].out } : {}) }])) });
  return <details className="fl-data">
    <summary><b>Interface</b> <span className="muted">inputs and outputs, for integrating this flow</span></summary>
    <div className="fl-data-sec"><div className="fl-data-head">Inputs</div>{flow.inputs.length ? <code className="fl-compact">{compact(sh.start?.out)}</code> : <p className="muted fl-help">No inputs.</p>}</div>
    <div className="fl-data-sec"><div className="fl-data-head">Outputs</div>
      {outs.map((n) => <div key={n.id}><b>{n.title || n.id}</b> <span className="muted">{(n.format || "").toUpperCase()}</span>{n.format === "json" && <code className="fl-compact">{sh[n.id]?.out ? compact(sh[n.id].out) : "shape not known yet"}</code>}</div>)}
      {!outs.length && <p className="muted fl-help">No outputs yet.</p>}</div>
    <button className="btn secondary small" onClick={() => { navigator.clipboard?.writeText(JSON.stringify(iface(), null, 2)).catch(() => {}); toast("Copied the flow's interface as JSON Schema."); }}>Copy as JSON Schema</button>
  </details>;
}


// --- a loop's list ------------------------------------------------------------------

// A Loop connected to the Inputs step gets all the inputs as one JSON object:
// take the list from the input that holds one (several lines, picks, files).
function loopFromInputs(a, n) {
  if (a?.type !== "start" || n?.type !== "loop" || (n.field || "").trim() || (n.source || "").trim()) return "";
  const ins = fl().flow.inputs || [];
  if (!ins.length) return "";
  const i = ins.find((x) => ["multiline", "multichoice", "files"].includes(x.type)) || ins.find((x) => x.type !== "secret");
  if (!i) return "";
  n.field = i.name;
  return `The loop goes through the input “${i.label || i.name}”, one item per line. Change it in “List from”.`;
}

function LoopListFrom({ n, set }) {
  const f = fl(), sh = f.shapes?.[n.id], obj = sh?.in?.type === "object" ? sh.in : null;
  const ps = obj ? schemaPaths(obj, 200) : [];
  return <Field label="List from" hint={obj ? "The input is a JSON object: the field that holds the list. Empty: its only field, or its only list." : "For JSON input: the path of the list, e.g. items or data.rows. Empty: the whole input (or an object's only field)."}>
    <input list={"listpaths-" + n.id} data-path="1" {...track} value={n.field || ""} placeholder={obj ? (ps.find((p) => p.depth === 0)?.path || "field") : "e.g. data.items"} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => set("field", e.target.value)} />
    <datalist id={"listpaths-" + n.id}>{ps.map((p) => <option key={p.path} value={p.path}>{p.type}{p.description ? " · " + p.description : ""}</option>)}</datalist>
  </Field>;
}

// --- the AI designer ---------------------------------------------------------------

const DESIGN_EXAMPLES = [
  "Every morning, tell me which blocking issues the quality gate found, grouped by file",
  "Find the TODOs in the project and give me a short list of the urgent ones",
  "Read a Jira ticket and write acceptance criteria for it",
  "Check the API spec for problems and summarise what to fix first",
];
const DESIGN_PHASES = ["Reading your tools and their contracts", "Sketching the options", "Wiring the steps", "Trying every option with sample data", "Fixing what did not work", "Ranking: tools and plain steps before AI"];

function openDesigner(mode) {
  const f = fl();
  const switching = f.ai?.mode !== (f.flow ? mode : "new");
  mode = "new"; // fixing a flow happens on Run flows, step by step
  f.ai = { ...(f.ai || {}), open: true, mode, prompt: switching ? "" : f.ai?.prompt || "", res: switching ? null : f.ai?.res };
  update();
}

function PathChips({ flow }) {
  const order = [], node = (id) => flow.nodes.find((x) => x.id === id);
  const visit = (id) => { if (order.includes(id)) return; order.push(id); flow.edges.filter((e) => e.from === id).forEach((e) => visit(e.to)); };
  visit("start");
  return <div className="guide-path">{order.filter((id) => id !== "start" && node(id)).map((id, i) => {
    const n = node(id), br = flow.edges.find((e) => e.to === id && e.branch);
    return <span key={id} className={"guide-chip" + (n.type === "ai" ? " ai" : "")}>{i > 0 && <span className="muted">{br ? `◆ ${br.branch === "else" ? "Otherwise" : (node(br.from)?.branches || []).find((b) => b.id === br.branch)?.label || br.branch} →` : "→"}</span>}
      <StepIcon n={n} tools={tools()} size={18} />{n.title || (n.type === "tool" ? toolRef(n.tool) : n.id)}</span>;
  })}</div>;
}

function stepDetail(n) {
  switch (n.type) {
    case "tool": return toolRef(n.tool) + (Object.keys(n.args || {}).length ? " · " + Object.entries(n.args).map(([k, v]) => `${k}=${v}`).join(", ") : "") + (n.instruction ? " · AI fills: " + n.instruction : "");
    case "ai": return (n.prompt || "").slice(0, 140) + (n.outputSchema ? " → " + compact(n.outputSchema) : "");
    case "transform": return n.op + (n.expr ? " " + n.expr : "");
    case "filter": return (n.mode === "drop" ? "drop " : "keep ") + condCount(n.cond) + " condition(s)" + (n.orderBy?.length ? " · sort " + n.orderBy.join(", ") : "");
    case "aggregate": return (n.groupBy?.length ? "by " + n.groupBy.join(", ") + " · " : "") + (n.measures || []).map((m) => `${m.fn}(${m.field || ""})`).join(", ");
    case "decision": return (n.branches || []).map((b) => b.label || b.id).join(" / ") + " / otherwise";
    case "output": return (n.format || "").toUpperCase() + " · " + (n.actions || []).map((a) => a.type + (a.path ? " " + a.path : "")).join(", ");
    default: return n.mode || n.split || "";
  }
}

function OptionCard({ o, improving, apply }) {
  const [steps, setSteps] = useState(false);
  const errs = o.issues.filter((i) => i.level === "error"), warns = o.issues.length - errs.length;
  return <div className={"ai-option" + (o.recommended ? " recommended" : "")}>
    <div className="ai-option-head"><b>{o.title}</b>
      {o.recommended && <span className="badge badge-ok">Recommended</span>}
      <span className={"badge " + (o.deterministic ? "badge-info" : "badge-warn")} title={o.deterministic ? "Runs the same way every time: tools and plain steps only" : "Some steps ask the AI"}>{o.deterministic ? "No AI" : `AI in ${o.aiSteps} step${o.aiSteps === 1 ? "" : "s"}`}</span>
      {o.trial && <span className={"badge " + (o.trial.ok ? "badge-ok" : "badge-warn")} title="Tried with sample data: tools and AI steps mocked, every other step run for real">{o.trial.ok ? "✓ Tried" : "✗ Trial failed"}</span>}
      {improving && o.fixes > 0 && <span className="badge badge-ok">Fixes {o.fixes} problem{o.fixes === 1 ? "" : "s"}</span>}
      {improving && o.rewrite && <span className="badge badge-warn" title="It keeps few of the current steps">Rewrites most of it</span>}
      {errs.length > 0 && <span className="badge badge-warn" title={errs.map((i) => i.message).join("\n")}>{errs.length} problem{errs.length === 1 ? "" : "s"}</span>}
      {!errs.length && warns > 0 && <span className="badge" title={o.issues.map((i) => i.message).join("\n")}>{warns} note{warns === 1 ? "" : "s"}</span>}</div>
    <p>{o.summary}</p>
    {o.why && <p className="muted fl-help">{o.why}</p>}
    <PathChips flow={o.flow} />
    {o.tools.length > 0 && <div className="muted fl-help">Uses: {o.tools.map((t) => <code key={t} className="inline-code" data-nofile>{toolRef(t)}</code>).reduce((a, b) => [a, " ", b])}</div>}
    {improving && o.changes?.length > 0 && <ul className="ai-changes">{o.changes.map((c, i) => <li key={i}>{c}</li>)}</ul>}
    <details open={steps} onToggle={(e) => setSteps(e.currentTarget.open)}><summary className="muted">{o.flow.nodes.length - 1} steps{o.flow.inputs?.length ? ` · inputs: ${o.flow.inputs.map((i) => i.label || i.name).join(", ")}` : ""}</summary>
      <ol className="ai-steps">{o.flow.nodes.filter((n) => n.type !== "start").map((n) => <li key={n.id}><StepIcon n={n} tools={tools()} size={16} /> <b>{n.title || n.id}</b> <span className="muted">{n.type} · {stepDetail(n)}</span></li>)}</ol>
      {errs.length > 0 && <div className="banner error">{errs.map((i) => i.message).join(" ")}</div>}</details>
    {o.trial && <TrialReport t={o.trial} />}
    <div className="row-actions"><button className="btn primary" onClick={() => apply(o)}>{improving ? "Save as a new version" : "Use this flow"}</button></div>
  </div>;
}

// How an option's trial run went: each step with its (sample) output, and the checks.
const TRIAL_ICON = { done: "✓", failed: "✗", blocked: "✗", off: "–", "not run": "·" };
function TrialReport({ t }) {
  const [open, setOpen] = useState(!t.ok);
  const passed = t.checks.filter((c) => c.ok).length;
  return <details className={"ai-trial" + (t.ok ? "" : " failed")} open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
    <summary className="muted">Trial run with sample data · {passed} of {t.checks.length} check{t.checks.length === 1 ? "" : "s"} passed</summary>
    <ul className="ai-checks">{t.checks.map((c, i) => <li key={i} className={c.ok ? "ok" : "bad"}>{c.ok ? "✓" : "✗"} {c.label}{c.note ? <span className="muted"> · {c.note}</span> : null}</li>)}</ul>
    {Object.keys(t.inputs || {}).length > 0 && <div className="muted fl-help">Inputs: {Object.entries(t.inputs).map(([k, v]) => `${k} = ${v}`).join(", ")}</div>}
    <ol className="ai-trial-steps">{t.steps.filter((s) => s.type !== "start").map((s) => <li key={s.id} className={s.status}>
      <span className="ai-trial-st" title={s.status}>{TRIAL_ICON[s.status] || "·"}</span> <b>{s.title}</b>
      <span className="muted"> {s.status === "off" ? "not on the path taken" : s.status}{s.mocked ? " · sample output (mocked)" : ""}{s.path ? " · → " + s.path : ""}{s.runs > 1 ? ` · ran ${s.runs}×` : ""}</span>
      {s.error && <div className="ai-trial-err">{s.error}</div>}
      {s.output && s.status === "done" && <details><summary className="muted">output</summary><pre className="code guide-pre">{s.output}</pre></details>}</li>)}</ol>
  </details>;
}

function AIDesigner({ inline }) {
  const f = fl(), ai = f.ai, improving = ai.mode === "improve" && !!f.flow;
  const set = (k, v) => { ai[k] = v; update(); };
  const design = async () => {
    if (f.dirty) await save();
    Object.assign(ai, { busy: true, error: "", res: null }); update();
    try { ai.res = await backend.DesignFlows({ prompt: ai.prompt, flowId: improving ? f.flow.id : "", options: 3, model: ai.model || "" }); }
    catch (e) { ai.error = errMsg(e); }
    ai.busy = false; update();
  };
  const apply = async (o) => {
    try {
      const designed = "Made by the AI designer" + (ai.prompt.trim() ? `: “${ai.prompt.trim().slice(0, 200)}”` : "") + ".";
      let id;
      if (improving) {
        const st = await backend.SaveFlowVersion(f.flow.id, { ...o.flow, designed });
        id = st.flow.id; toast(`Saved as ${st.flow.name}; the original is unchanged.`);
      } else {
        const base = await backend.NewFlow(o.flow.name || o.title);
        await backend.SaveFlow({ ...base, name: o.flow.name || o.title, description: o.summary, inputs: o.flow.inputs || [], nodes: o.flow.nodes, edges: o.flow.edges, designed });
        id = base.id; toast(`Created the flow ${o.flow.name || o.title}. Check the steps, then Run it.`);
      }
      ai.open = false; ai.res = null;
      await loadList(); await openFlow(id);
    } catch (e) { toast(errMsg(e)); }
  };
  return <div className={"ai-designer" + (inline ? " inline" : "")} role="dialog" aria-label="AI designer">
    <div className="ai-designer-head"><Icon.spark /><h3>{improving ? `Fix and improve “${f.flow.name}”` : "New flow with AI"}</h3>
      <span className="muted">{improving ? "The AI changes this flow; you get versions of it to choose from" : "Describe what you want in your own words"}</span>
      <button className="link" style={{ marginLeft: "auto" }} aria-label="Close" onClick={() => set("open", false)}>✕</button></div>
    {improving && (() => { const n = f.issues.filter((i) => i.level === "error").length; return n ? <div className="banner">This flow has {n} problem{n === 1 ? "" : "s"}: the AI fixes {n === 1 ? "it" : "them"} first.</div> : null; })()}
    <div className="ai-model"><span className="muted">Model</span><ModelPicker value={ai.model || ""} onChange={(v) => set("model", v)} label="Model for the AI designer" /><small className="muted">Default: the app's model.</small></div>
    <textarea rows={3} value={ai.prompt} placeholder={improving ? "What should be fixed or better? e.g. it fails on the scan step; stop when there is nothing to report; use the tools instead of AI. Leave empty to fix its problems and improve it in general." : "e.g. " + DESIGN_EXAMPLES[0]} onChange={(e) => set("prompt", e.target.value)}
      onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) design(); }} />
    {!improving && !ai.prompt && <div className="chips">{DESIGN_EXAMPLES.map((x) => <button key={x} type="button" className="chip" onClick={() => set("prompt", x)}>{x.split(" ").slice(0, 7).join(" ")}…</button>)}</div>}
    <div className="row-actions"><button className="btn primary" disabled={ai.busy || (!improving && !ai.prompt.trim())} onClick={design}>{ai.busy ? (improving ? "Working on it…" : "Designing…") : ai.res ? "Try again" : improving ? "Fix and improve" : "Design options"}</button>
      <span className="muted fl-help">{improving ? "It keeps this flow's steps and changes only what is needed, using your tools first, then plain steps, and AI steps only where nothing else fits. Every version is tried with sample data before you see it (tools and AI mocked, everything else run for real); the one you choose is saved as a new version, and the original stays as it is." : "It uses your tools first, connected by their data shapes, then plain steps, and AI steps only where nothing else fits. Every option is tried with sample data before you see it (tools and AI mocked, everything else run for real); the one you choose opens here."}</span></div>
    {ai.busy && <AiProgress phases={DESIGN_PHASES} />}
    {ai.error && <div className="banner error">{ai.error}</div>}
    {ai.res && <>
      <div className="ai-options">{ai.res.options.map((o, i) => <OptionCard key={i} o={o} improving={improving} apply={apply} />)}</div>
      <p className="muted fl-help">{ai.res.repaired ? "Some options were fixed automatically after the check. " : ""}{ai.res.usage?.input_tokens ? `Used ${(ai.res.usage.input_tokens + ai.res.usage.output_tokens).toLocaleString()} tokens` : ""}{ai.res.usage?.premium_requests ? ` · ${ai.res.usage.premium_requests} premium request${ai.res.usage.premium_requests === 1 ? "" : "s"}` : ""}{ai.res.usage?.cost_usd ? ` · $${ai.res.usage.cost_usd.toFixed(3)}` : ""}</p>
    </>}
  </div>;
}
