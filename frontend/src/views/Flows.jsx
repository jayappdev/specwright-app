// Flow designer: string tools together into a flow.
//   left      the project's flows (new, import)
//   palette   step types (AI prompt, merge, transform, output) and tools; drag onto the canvas
//   canvas    steps as cards; drag a card to move it, drag from its ● port to another card to connect
//   inspector the selected step, or the flow (name, context, skills) when nothing is selected
// Flows are saved to .codegen/flows/<id>.json (autosave). Running them comes with the executor.
import { registerPlace } from "../navigation.js";
import { loadLayout } from "../listLayout.jsx";
import { stepRef, toolRef, isBuiltin, loopInfo, uniqueTitle } from "../flowNames.js";
import { useEffect, useRef } from "react";
import { openFlowRunner } from "./FlowRun.jsx";
import { confirmDialog, askText } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg, saveText, refreshExtensions } from "../store.js";
import { Icon, Menu, usePopover } from "../ui.jsx";
import { AIDesigner } from "./flows/AIDesigner.jsx";
import { FlowWizard, openWizard, wz } from "./flows/FlowWizard.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { ToolIcon } from "../ToolTile.jsx";
import { openFlowGuide } from "./FlowGuide.jsx";
import { Canvas } from "./flows/Canvas.jsx";
import { Palette } from "./flows/Palette.jsx";
import { FlowLibrary } from "./flows/FlowLibrary.jsx";
import { Inspector, loopFromInputs } from "./flows/Inspector.jsx";
import { branchName, newCond, nextBranch } from "./flows/Conditions.jsx";

export const NODE_W = 216,
  PORT_Y = 30,
  GRID = 10;
export const snap = (v) => Math.round(v / GRID) * GRID;
const clone = (x) => JSON.parse(JSON.stringify(x));

export const STEP_TYPES = [
  {
    type: "chat",
    name: "Chat",
    help: "Makes this a Chatflow: the steps before it set up a chat session; each path after it is something the chat can run",
    keywords: "chatflow conversation assistant session",
  },
  { type: "ai", name: "AI prompt", help: "Ask the agent; uses the flow's context and skills" },
  {
    type: "improve",
    name: "Improve until done",
    help: "Pairs with a tool: fixes its run, or has it check the AI's result, round after round until done",
  },
  {
    type: "context",
    name: "Context",
    help: "Gather files, notes, past runs and steps' results; keep what matters within a budget; the AI steps after it get it",
  },
  { type: "merge", name: "Merge", help: "Combine several steps' outputs" },
  { type: "transform", name: "Transform", help: "Reshape data: JSON, CSV, text, Markdown → HTML, AI" },
  {
    type: "loop",
    name: "Loop (for each)",
    help: "Split a list into items; the steps after it run once per item, until a Gather",
  },
  {
    type: "gather",
    name: "Gather",
    help: "Ends a loop: collects every item's result as text, sections or JSON",
  },
  {
    type: "decision",
    name: "Decision",
    help: "Sends the flow down one path (or several), chosen by conditions on the data",
  },
  { type: "filter", name: "Filter", help: "Keeps or drops the rows that match conditions" },
  {
    type: "aggregate",
    name: "Aggregate",
    help: "Groups rows: count, sum, min, max, distinct count, windows (rank, running sum)",
  },
  {
    type: "output",
    name: "Output",
    help: "md, txt, html, csv or json, then save, call a tool, copy or show",
  },
];
export const FORMATS = [
  ["md", "Markdown"],
  ["txt", "Text"],
  ["html", "HTML"],
  ["csv", "CSV"],
  ["json", "JSON"],
];
export const INPUT_TYPES = [
  ["text", "Text"],
  ["multiline", "Long text"],
  ["secret", "Secret"],
  ["number", "Number"],
  ["date", "Date"],
  ["datetime", "Date and time"],
  ["boolean", "Yes / no"],
  ["choice", "Choice"],
  ["multichoice", "Multiple choice"],
  ["file", "File"],
  ["files", "Files"],
  ["clipboard", "Clipboard"],
];
export const SPLIT_MODES = [
  ["auto", "Detect (JSON array, else one per line)"],
  ["lines", "One item per line"],
  ["json", "JSON array"],
  ["csv", "CSV rows (each a JSON object)"],
  ["regex", "Matches of a pattern"],
];
export const GATHER_MODES = [
  ["concat", "Join as text"],
  ["sections", "Sections (## item, then its result)"],
  ["json-array", "JSON array"],
  ["json-object", "JSON object (item → result)"],
  ["template", "Template per item"],
];
export const ROW_FORMATS = [
  ["auto", "Detect (JSON, CSV or one row per line)"],
  ["json", "JSON array"],
  ["csv", "CSV with a header line"],
  ["lines", "One row per line"],
];
export const MATCHES = [
  ["all", "all of"],
  ["any", "any of"],
  ["none", "none of"],
];
export const MERGE_MODES = [
  ["concat", "Join as text"],
  ["json-object", "JSON object (one key per step)"],
  ["json-array", "JSON array"],
  ["template", "Template"],
];

export const fl = () =>
  (S.fl ||= {
    list: null,
    id: "",
    flow: null,
    issues: [],
    sel: null,
    selEdge: null,
    dirty: false,
    saving: false,
    zoom: 1,
    ops: null,
    samples: {},
    preview: {},
    q: "",
  });

// --- step icons --------------------------------------------------------------------

const sv = (d) =>
  function I() {
    return (
      <svg
        viewBox="0 0 24 24"
        width="18"
        height="18"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {d}
      </svg>
    );
  };
const TYPE_ICON = {
  chat: {
    color: "--ansi-magenta",
    Icon: sv(
      <>
        <path d="M4 5h16v10H9l-5 4z" />
        <path d="M8 9h8" />
        <path d="M8 12h5" />
      </>,
    ),
  },
  context: {
    color: "--ansi-cyan",
    Icon: sv(
      <>
        <path d="M4 5h11" />
        <path d="M4 10h8" />
        <path d="M4 15h5" />
        <circle cx="16.5" cy="15.5" r="3.5" />
        <path d="M19 18l2.5 2.5" />
      </>,
    ),
  },
  start: {
    color: "--ok",
    Icon: sv(
      <>
        <path d="M4 12h11" />
        <path d="M11 7l5 5-5 5" />
        <path d="M19 4v16" />
      </>,
    ),
  },
  ai: {
    color: "--btn-bg",
    Icon: sv(
      <>
        <path d="M12 3l1.8 4.9L18.7 9.7 13.8 11.5 12 16.4 10.2 11.5 5.3 9.7 10.2 7.9z" />
        <path d="M18.5 15.5l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" />
      </>,
    ),
  },
  improve: {
    color: "--ansi-blue",
    Icon: sv(
      <>
        <path d="M20 12a8 8 0 1 1-2.3-5.6" />
        <path d="M20 4v5h-5" />
        <path d="M9 12l2 2 4-4" />
      </>,
    ),
  },
  merge: {
    color: "--ansi-magenta",
    Icon: sv(
      <>
        <path d="M5 4v4c0 3 7 4 7 8v4M19 4v4c0 3-7 4-7 8" />
      </>,
    ),
  },
  transform: {
    color: "--ansi-cyan",
    Icon: sv(
      <>
        <path d="M4 7h11l-3-3M20 17H9l3 3" />
      </>,
    ),
  },
  output: {
    color: "--warn",
    Icon: sv(
      <>
        <path d="M12 3v12M7 10l5 5 5-5" />
        <path d="M4 17v3h16v-3" />
      </>,
    ),
  },
  loop: {
    color: "--ansi-green",
    Icon: sv(
      <>
        <path d="M17 2l3 3-3 3" />
        <path d="M4 11V9a4 4 0 014-4h12" />
        <path d="M7 22l-3-3 3-3" />
        <path d="M20 13v2a4 4 0 01-4 4H4" />
      </>,
    ),
  },
  gather: {
    color: "--ansi-yellow",
    Icon: sv(
      <>
        <path d="M4 4l6 6M20 4l-6 6M12 4v6" />
        <path d="M5 14h14l-2 6H7z" />
      </>,
    ),
  },
  decision: {
    color: "--ansi-magenta",
    Icon: sv(
      <>
        <path d="M12 3l8 9-8 9-8-9z" />
        <path d="M12 8v5M12 16v.5" />
      </>,
    ),
  },
  filter: {
    color: "--ansi-cyan",
    Icon: sv(
      <>
        <path d="M4 5h16l-6 7.5V18l-4 2v-7.5z" />
      </>,
    ),
  },
  aggregate: {
    color: "--ok",
    Icon: sv(
      <>
        <path d="M18 5H7l6 7-6 7h11" />
      </>,
    ),
  },
};

export function StepIcon({ n, tools, size = 34 }) {
  if (n.type === "tool") {
    const t = tools.find((x) => x.name === n.tool) || { name: n.tool || "tool", desc: "" };
    return <ToolIcon t={t} size={size} />;
  }
  const s = TYPE_ICON[n.type] || TYPE_ICON.ai;
  return (
    <span className="tool-icon" style={{ "--tc": `var(${s.color})`, width: size, height: size }}>
      <s.Icon />
    </span>
  );
}

// --- loading and saving -------------------------------------------------------------

export async function loadList() {
  const f = fl();
  try {
    f.list = await backend.Flows();
  } catch (e) {
    f.list = [];
    toast(errMsg(e), "error");
  }
  if (!f.ops) f.ops = await backend.FlowOps().catch(() => null);
  update();
}

// Open a flow in the designer from another screen.
export function openFlowInDesigner(id, sel) {
  fl().id = "";
  fl().flow = null;
  S.view = "flows";
  openFlow(id).then(() => {
    if (sel && nodeOf(sel)) {
      fl().sel = sel;
      update();
    }
  });
}

export async function openFlow(id) {
  const f = fl();
  if (f.conflict && f.flow && f.flow.id !== id)
    return toast("Choose which version of this flow to keep first (the banner above the canvas).", "error");
  if (f.dirty) await save();
  try {
    const flow = await backend.LoadFlow(id);
    const repairs = takeRepairs(flow);
    Object.assign(f, {
      id,
      flow,
      sel: null,
      selEdge: null,
      dirty: false,
      preview: {},
      kbLink: null,
      conflict: null,
      repairs,
    });
    resetHistory();
    f.issues = (await backend.ValidateFlow(flow).catch(() => [])) || [];
    await refreshShapes();
  } catch (e) {
    toast(errMsg(e), "error");
  }
  update();
}

// closeFlow goes back to the list of flows (saving the open one first).
export async function closeFlow() {
  const f = fl();
  if (!f.flow) return;
  if (f.conflict)
    return toast("Choose which version of this flow to keep first (the banner above the canvas).", "error");
  if (f.dirty) await save();
  if (f.dirty) return; // the save failed: stay, so nothing is lost
  Object.assign(f, { id: "", flow: null, sel: null, selEdge: null, multi: [], repairs: null });
  update();
  loadList();
}

// What every step gets and gives (designer metadata; never sent to the AI).
export async function refreshShapes() {
  const f = fl();
  if (!f.flow) return;
  const samples = Object.fromEntries(
    Object.entries(f.samples[f.flow.id]?.steps || {}).filter(([, v]) => String(v).trim()),
  );
  f.shapes = (await backend.FlowShapes(f.flow, samples).catch(() => null)) || {};
}

// Undo / redo: snapshots of the flow, taken when it changes. Changes less than
// 600 ms apart (typing) share one snapshot. At most 60 are kept per flow.
const snap0 = (flow) => JSON.stringify({ ...flow, updated: undefined });
function resetHistory() {
  const f = fl();
  f.undo = [];
  f.redo = [];
  f.last = f.flow ? snap0(f.flow) : "";
  f.lastAt = 0;
}
function record() {
  const f = fl(),
    now = Date.now(),
    cur = snap0(f.flow);
  if (cur === f.last) return;
  if (!f.undo) resetHistory();
  if (now - (f.lastAt || 0) > 600 || !f.undo.length) {
    f.undo.push(f.last);
    if (f.undo.length > 60) f.undo.shift();
  }
  f.redo = [];
  f.last = cur;
  f.lastAt = now;
}
function restore(from, to) {
  const f = fl();
  if (!f.flow || !f[from]?.length) return;
  (f[to] ||= []).push(snap0(f.flow));
  const s = f[from].pop(),
    updated = f.flow.updated;
  f.flow = { ...JSON.parse(s), updated };
  f.last = s;
  f.lastAt = 0;
  f.sel = null;
  f.multi = [];
  f.selEdge = null;
  f.dirty = true;
  update();
  backend
    .ValidateFlow(f.flow)
    .then((is) => {
      f.issues = is || [];
      update();
    })
    .catch(() => {});
  clearTimeout(timer);
  timer = setTimeout(save, 700);
}
export const undo = () => restore("undo", "redo"),
  redo = () => restore("redo", "undo");

let timer = 0;
export function changed() {
  const f = fl();
  record();
  f.dirty = true;
  update();
  clearTimeout(timer);
  timer = setTimeout(save, 700);
}

export async function save() {
  const f = fl();
  if (!f.flow || f.saving || f.conflict) return; // a conflict waits for the user's choice
  clearTimeout(timer);
  f.saving = true;
  update();
  try {
    const st = await backend.SaveFlow(f.flow);
    f.issues = st.issues || [];
    f.dirty = false;
    await refreshShapes();
    f.flow.updated = st.flow.updated;
    f.flow.rev = st.flow.rev; // the file as it is now: the next save is checked against it
    const s = (f.list || []).find((x) => x.id === f.flow.id);
    if (s) {
      s.name = f.flow.name;
      s.updated = st.flow.updated;
      s.group = st.flow.group || ""; // as saved (tidied: flow/tags.go)
      s.tags = st.flow.tags || [];
      s.description = f.flow.description;
    }
  } catch (e) {
    if (/flow changed on disk/.test(errMsg(e))) {
      // the file changed since it was opened here: never write over it silently
      f.saving = false;
      await checkDisk("save");
      return;
    }
    toast(errMsg(e), "error");
  }
  f.saving = false;
  update();
}

// takeRepairs: what the backend repaired when the flow's file did not fit
// the format (an agent edited it); shown once above the canvas.
function takeRepairs(flow) {
  const r = flow?.repairs || [];
  if (flow) delete flow.repairs;
  return r.length ? r : null;
}

// checkDisk keeps the designer and the flow's file the same. When the file
// changed outside the designer (an agent in the Playground, another window,
// git): with no unsaved edits the new version is loaded; with unsaved edits
// the user chooses (f.conflict). Called when flow files change, when the
// designer is shown, when the window gets the focus, and when a save is
// refused.
export async function checkDisk(why = "") {
  const f = fl();
  if (!f.flow?.id || f.saving) return;
  const id = f.flow.id;
  let disk;
  try {
    disk = await backend.LoadFlow(id);
  } catch {
    if (fl().flow?.id === id && !f.conflict) {
      f.conflict = { gone: true };
      update();
    }
    return;
  }
  if (fl().flow?.id !== id) return; // switched meanwhile
  if (!disk.rev || disk.rev === f.flow.rev) return; // the same
  if (!f.dirty && why !== "save") {
    clearTimeout(timer);
    Object.assign(f, { flow: disk, dirty: false, conflict: null, repairs: takeRepairs(disk) });
    resetHistory();
    f.issues = (await backend.ValidateFlow(disk).catch(() => [])) || [];
    await refreshShapes();
    toast(`Reloaded ${disk.name}: it was changed outside the designer.`);
    loadList();
    update();
    return;
  }
  clearTimeout(timer);
  f.conflict = { disk };
  update();
}

// resolveConflict: "theirs" loads the file's version, "mine" writes the
// designer's version over it.
export async function resolveConflict(choice) {
  const f = fl(),
    c = f.conflict;
  if (!c) return;
  if (choice === "theirs" && c.disk) {
    Object.assign(f, { flow: c.disk, dirty: false, conflict: null });
    resetHistory();
    f.issues = (await backend.ValidateFlow(c.disk).catch(() => [])) || [];
    await refreshShapes();
    toast("Loaded the version from disk.");
  } else if (choice === "mine") {
    f.flow.rev = c.disk?.rev || ""; // overwrite what is on disk (or recreate a deleted file)
    f.conflict = null;
    f.dirty = true;
    await save();
  }
  loadList();
  update();
}

// keep the designer and the files in step: flow files changed on disk, the
// window gets the focus again
if (typeof window !== "undefined") {
  let t = 0;
  const later = (why) => {
    clearTimeout(t);
    t = setTimeout(() => {
      if (S.view !== "flows" || !S.ws) return;
      checkDisk(why);
      loadList();
    }, 250);
  };
  window.addEventListener("specwright:flows-changed", () => later("files"));
  window.addEventListener("focus", () => later("focus"));
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") later("focus");
  });
}

// createFlow: a new flow from anywhere (the + New menu): it opens in the designer
export async function createFlow() {
  S.view = "flows";
  update();
  await newFlow();
}

async function newFlow() {
  const name = await askText("New flow", { label: "Name", value: "New flow", ok: "Create" });
  if (name == null) return;
  try {
    const flow = await backend.NewFlow(name);
    await loadList();
    await openFlow(flow.id);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function importFlow(file) {
  try {
    const st = await backend.ImportFlow(await file.text());
    await loadList();
    await openFlow(st.flow.id);
    toast(`Imported ${st.flow.name}.` + (st.notes?.length ? " " + st.notes.join(" · ") : ""));
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function exportFlow() {
  const f = fl();
  if (f.dirty) await save();
  try {
    saveText(`${f.flow.id}.flow.json`, await backend.ExportFlow(f.flow.id), "application/json");
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function deleteFlow() {
  const f = fl();
  if (
    !(await confirmDialog(`This removes .codegen/flows/${f.flow.id}.json from the project.`, {
      title: `Delete the flow ${f.flow.name}?`,
      ok: "Delete",
      danger: true,
    }))
  )
    return;
  try {
    await backend.DeleteFlow(f.flow.id);
    Object.assign(f, { id: "", flow: null, sel: null, dirty: false });
    clearTimeout(timer);
    await loadList();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// renameFlow: a new name, and with it a new id: the flow's file and what
// refers to it follow (core/rename.go).
async function renameFlow() {
  const f = fl();
  if (f.dirty) await save();
  const old = f.flow;
  const name = (
    await askText(`Rename ${old.name}`, {
      value: old.name,
      ok: "Rename",
      message: `Its file (.codegen/flows/${old.id}.json) is renamed to match, and the flows that refer to it (after-flow triggers, versions, run_flow steps) are updated; past runs keep the old name.`,
    })
  )?.trim();
  if (!name || name === old.name) return;
  try {
    const st = await backend.RenameFlow(old.id, name);
    await loadList();
    await loadLayout(true);
    await openFlow(st.flow.id);
    toast((st.notes || [])[0] || `Renamed to ${st.flow.name}.`);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function duplicateFlow() {
  const f = fl();
  if (f.dirty) await save();
  const copy = { ...clone(f.flow), id: "", name: f.flow.name + " (copy)" };
  try {
    const st = await backend.SaveFlow(copy);
    await loadList();
    await openFlow(st.flow.id);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// --- graph helpers ---------------------------------------------------------------------

export const nodeOf = (id) => fl().flow?.nodes.find((n) => n.id === id);
export const parentsOf = (flow, id) => flow.edges.filter((e) => e.to === id).map((e) => e.from);
export function ancestors(flow, id) {
  const seen = new Set(),
    walk = (n) =>
      parentsOf(flow, n).forEach((p) => {
        if (!seen.has(p)) {
          seen.add(p);
          walk(p);
        }
      });
  walk(id);
  return seen;
}
export const projectTools = () =>
  (S.ws?.extensions || []).filter((t) => !t.managed || t.managed.status === "verified");
// built-in tools (read files, search, edit, run commands, fetch pages) run directly as steps: "builtin:<name>"
export const builtinTools = () => S.flowBuiltins || [];
export const tools = () => [...projectTools(), ...builtinTools()];
const BUILTIN_GROUPS = [
  ["read", "Read"],
  ["search", "Search"],
  ["edit", "Edit files"],
  ["execute", "Run"],
  ["web", "Web"],
  ["chart", "Charts"],
];
export const toolLabel = (name) => (isBuiltin(name) ? toolRef(name) + " · built-in" : name);
async function loadBuiltins() {
  try {
    S.flowBuiltins = (await backend.FlowBuiltinTools()) || [];
  } catch {
    S.flowBuiltins = [];
  }
  update();
}
// a tool <select>: project tools, then built-in tools by group
export function ToolOptions() {
  const p = projectTools(),
    b = builtinTools();
  return (
    <>
      {p.length > 0 && (
        <optgroup label="Project tools">
          {p.map((t) => (
            <option key={t.name} value={t.name}>
              {t.name}
            </option>
          ))}
        </optgroup>
      )}
      {BUILTIN_GROUPS.map(([g, l]) => {
        const ts = b.filter((t) => t.lang === g);
        return (
          ts.length > 0 && (
            <optgroup key={g} label={"Built-in · " + l}>
              {ts.map((t) => (
                <option key={t.name} value={t.name}>
                  {toolRef(t.name)}
                </option>
              ))}
            </optgroup>
          )
        );
      })}
    </>
  );
}

function uniqueId(base) {
  const ids = new Set(fl().flow.nodes.map((n) => n.id));
  base = (base || "step").replace(/[^A-Za-z0-9_-]+/g, "_").replace(/^[^A-Za-z]+/, "") || "step";
  let id = base,
    k = 2;
  while (ids.has(id)) id = base + k++;
  return id;
}

export function addNode(spec, x, y) {
  const f = fl(),
    flow = f.flow;
  const right = flow.nodes.reduce((m, n) => Math.max(m, n.x), 40);
  const id = uniqueId(spec.type === "tool" ? toolRef(spec.tool) : spec.type);
  const n = {
    id,
    type: spec.type,
    title: uniqueTitle(
      flow,
      spec.type === "tool"
        ? toolRef(spec.tool)
        : (STEP_TYPES.find((t) => t.type === spec.type)?.name || "").replace(/\s*\(.*\)$/, ""),
      id,
    ),
    x: snap(x ?? right + NODE_W + 60),
    y: snap(y ?? 120),
  };
  if (spec.type === "tool") {
    n.tool = spec.tool;
    n.args = {};
    const t = tools().find((x) => x.name === spec.tool);
    (t?.params || []).forEach((p) => {
      n.args[p.name] = "";
    });
    if (spec.tool === "builtin:fetch_webpage") n.args.format = "raw";
    if (spec.args) Object.assign(n.args, spec.args); // a preset (Visualization: a chart)
    if (spec.title) n.title = uniqueTitle(flow, spec.title, id);
  } // in a flow, the response is data for the next steps
  if (spec.type === "ai") n.prompt = "";
  if (spec.type === "chat") {
    n.instructions = "";
    n.starters = [];
  }
  if (spec.type === "improve") {
    n.pair = f.sel && nodeOf(f.sel)?.type === "tool" ? "before" : "after";
    n.prompt = "";
    n.goal = "";
    n.rounds = 5;
    n.approve = "done";
  }
  if (spec.type === "context") {
    n.goal = "";
    n.sources = [];
    n.budget = 8000;
  }
  if (spec.type === "merge") {
    n.mode = "concat";
    n.separator = "\\n\\n";
  }
  if (spec.type === "transform") n.op = "json-path";
  if (spec.type === "output") {
    n.format = "md";
    n.actions = [{ type: "show" }];
  }
  if (spec.type === "loop") {
    n.split = "auto";
    n.max = 100;
  }
  if (spec.type === "gather") {
    n.mode = "sections";
  }
  if (spec.type === "decision") {
    n.mode = "first";
    n.branches = [{ id: "path1", label: "Path 1", when: newCond() }];
  }
  if (spec.type === "filter") {
    n.mode = "keep";
    n.cond = newCond();
  }
  if (spec.type === "aggregate") {
    n.mode = "group";
    n.groupBy = [];
    n.measures = [{ fn: "count", as: "count" }];
  }
  // connect from the selected step (or the inputs) so the new step is wired up
  const from = f.sel && nodeOf(f.sel)?.type !== "output" ? f.sel : x == null ? "start" : null;
  flow.nodes.push(n);
  if (from) {
    const e = { from, to: n.id },
      fa = nodeOf(from);
    if (fa?.type === "decision") e.branch = nextBranch(fa);
    flow.edges.push(e);
    const note =
      fa?.type === "decision"
        ? `Connected on the path “${branchName(fa, e.branch)}”. Click the connection to change it.`
        : autoWire(from, n) || loopFromInputs(fa, n);
    if (note && (from !== "start" || n.type === "loop")) toast(note);
  }
  f.sel = n.id;
  f.selEdge = null;
  changed();
}

// When a step is connected into a tool, pass its output into the tool's best
// matching empty argument (from Inputs: arguments named like an input).
// Returns a short note for the user, or "".
const DATA_PARAM =
  /input|text|data|content|body|json|payload|items|list|query|sql|file|message|value|source|doc/i;
// fields that expect a file path rather than the text itself
export const FILE_PARAM = (p) => /file/i.test(p.name) && !/name$/i.test(p.name);
// a step's result: {{<tool>.output_file}} (path of the saved file) for file
// fields, {{<tool>.buffer_output}} (the text) for the rest
export const feedFor = (p, from) => {
  const r = stepRef(fl().flow, from);
  return FILE_PARAM(p) ? `{{${r}.output_file}}` : `{{${r}.buffer_output}}`;
};
function autoWire(from, n) {
  if (n.type !== "tool" || (n.instruction || "").trim()) return "";
  const t = tools().find((x) => x.name === n.tool),
    a = nodeOf(from);
  if (!t || !a) return "";
  const args = (n.args ||= {}),
    empty = (t.params || []).filter((p) => !(args[p.name] || "").trim());
  if (a.type === "start") {
    const done = [];
    for (const p of empty) {
      const i = fl().flow.inputs.find((x) => x.name.toLowerCase() === p.name.toLowerCase());
      if (i) {
        args[p.name] = `{{inputs.${i.name}}}`;
        done.push(p.name);
      }
    }
    return done.length ? `Passing the inputs into ${done.join(", ")} of ${n.title || n.tool}.` : "";
  }
  if (a.type === "loop") {
    // a loop's output is the current item
    const p = empty.find((x) => x.required) || empty[0];
    // nested in another loop: name the loop, so the items cannot be mixed up
    const v = (loopInfo(fl().flow).stack[from] || []).length
      ? `{{${stepRef(fl().flow, from)}.item}}`
      : "{{item}}";
    if (p) {
      args[p.name] = v;
      return `“${p.name}” of ${n.title || toolRef(n.tool)} gets each item of ${a.title || a.id} (${v}).`;
    }
  }
  const pick =
    empty.find((p) => p.required && DATA_PARAM.test(p.name)) ||
    empty.find((p) => DATA_PARAM.test(p.name)) ||
    empty.find((p) => p.required);
  if (pick) {
    args[pick.name] = feedFor(pick, from);
    return FILE_PARAM(pick)
      ? `“${pick.name}” of ${n.title || n.tool} gets a file with the result of ${a.title || a.id}.`
      : `Passing the result of ${a.title || a.id} into “${pick.name}” of ${n.title || n.tool}.`;
  }
  if (isBuiltin(n.tool))
    return `Use the ← ${a.title || a.id} button on a field of ${n.title || toolRef(n.tool)} to pass the result in (built-in tools read their fields only).`;
  return `${n.title || n.tool} gets the result of ${a.title || a.id} automatically (piped in).`;
}

export function connect(from, to) {
  const f = fl(),
    flow = f.flow;
  if (!from || !to || from === to) return;
  const a = nodeOf(from),
    b = nodeOf(to);
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

export function removeSelected() {
  const f = fl(),
    flow = f.flow;
  if (f.selEdge != null) {
    flow.edges.splice(f.selEdge, 1);
    f.selEdge = null;
    return changed();
  }
  const ids = new Set(selectedIds().filter((id) => nodeOf(id)?.type !== "start"));
  if (!ids.size) return;
  flow.nodes = flow.nodes.filter((x) => !ids.has(x.id));
  flow.edges = flow.edges.filter((e) => !ids.has(e.from) && !ids.has(e.to));
  f.sel = null;
  f.multi = [];
  changed();
  toast(
    ids.size > 1
      ? `Deleted ${ids.size} steps. ⌘Z / Ctrl+Z brings them back.`
      : "Deleted the step. ⌘Z / Ctrl+Z brings it back.",
  );
}

// --- several selected steps: select, copy, paste ------------------------------------

// the selected steps: the one in the inspector, and the others selected with it
export function selectedIds() {
  const f = fl(),
    out = [...(f.multi || [])];
  if (f.sel && !out.includes(f.sel)) out.push(f.sel);
  return out.filter((id) => nodeOf(id));
}
export function setSelection(ids) {
  const f = fl();
  ids = [...new Set(ids)].filter((id) => nodeOf(id));
  f.multi = ids.length > 1 ? ids : [];
  f.sel = ids[ids.length - 1] || null;
  f.selEdge = null;
  update();
}

const CLIP_FORMAT = "specwright-steps";

export function togglePane(k) {
  if (S.layout[k]) delete S.layout[k];
  else S.layout[k] = true;
  update();
}

// copySteps puts the selected steps, the connections between them and the
// inputs they use on the clipboard (and keeps them for pasting in the app).
export function copySteps(e) {
  const f = fl(),
    flow = f.flow,
    ids = new Set(selectedIds().filter((id) => nodeOf(id)?.type !== "start"));
  if (!ids.size) return false;
  const nodes = flow.nodes.filter((n) => ids.has(n.id)).map((n) => JSON.parse(JSON.stringify(n)));
  const edges = flow.edges.filter((x) => ids.has(x.from) && ids.has(x.to));
  const text = JSON.stringify(nodes),
    inputs = flow.inputs.filter((i) => text.includes("inputs." + i.name));
  const clip = { format: CLIP_FORMAT, version: 1, from: flow.name, nodes, edges, inputs };
  const json = JSON.stringify(clip, null, 2);
  S.flowClip = clip;
  if (e?.clipboardData) {
    e.clipboardData.setData("text/plain", json);
    e.preventDefault();
  } else writeClipboard(json);
  toast(
    `Copied ${nodes.length} step${nodes.length === 1 ? "" : "s"}${edges.length ? ` and ${edges.length} connection${edges.length === 1 ? "" : "s"}` : ""}. Paste them into this or another flow with ⌘V.`,
  );
  return true;
}

// the system clipboard: the desktop app's, else the browser's
function writeClipboard(text) {
  try {
    if (window.runtime?.ClipboardSetText) return window.runtime.ClipboardSetText(text);
  } catch {
    /* the browser's */
  }
  return navigator.clipboard?.writeText(text).catch(() => {});
}
async function readClipboard() {
  try {
    if (window.runtime?.ClipboardGetText) return await window.runtime.ClipboardGetText();
  } catch {
    /* the browser's */
  }
  try {
    return await navigator.clipboard.readText();
  } catch {
    return "";
  }
}
// pasteFromClipboard: copied steps from the clipboard (another window or
// flow), else the ones copied in this session
export async function pasteFromClipboard(asNew) {
  const c = readClip(await readClipboard()) || S.flowClip;
  if (!c) return toast("Nothing to paste: select steps and copy them (⌘C) first.");
  S.flowClip = c;
  if (asNew || !fl().flow) return pasteAsNewFlow(c);
  pasteSteps(c);
}

export const readClip = (text) => {
  try {
    const c = JSON.parse(text);
    return c?.format === CLIP_FORMAT && Array.isArray(c.nodes) ? c : null;
  } catch {
    return null;
  }
};

// pasteSteps adds copied steps to the open flow: new ids (their references
// to each other follow), placed next to what is there, the inputs they use
// added when the flow lacks them; the pasted steps end up selected.
export function pasteSteps(clip) {
  const f = fl(),
    flow = f.flow;
  if (!flow || !clip?.nodes?.length) return;
  const map = {},
    added = [];
  const minX = Math.min(...clip.nodes.map((n) => n.x || 0)),
    minY = Math.min(...clip.nodes.map((n) => n.y || 0));
  const same = clip.nodes.some((n) => nodeOf(n.id) && nodeOf(n.id).x === n.x && nodeOf(n.id).y === n.y); // pasting where the copies came from
  const right = Math.max(...flow.nodes.map((n) => n.x)) + NODE_W + 60;
  const ox = same ? 40 : right - minX,
    oy = same ? 60 : 120 - minY;
  for (const n of clip.nodes) {
    if (n.type === "start") continue;
    const id = uniqueId(n.id);
    map[n.id] = id;
    const c = JSON.parse(JSON.stringify(n));
    Object.assign(c, { id, x: snap((n.x || 0) + ox), y: snap((n.y || 0) + oy) });
    flow.nodes.push(c);
    added.push(c);
  }
  // references between the pasted steps follow their new ids
  const pairs = Object.entries(map)
    .filter(([a, b]) => a !== b)
    .sort((x, y) => y[0].length - x[0].length);
  const fix = (s) =>
    typeof s !== "string"
      ? s
      : pairs.reduce(
          (t, [a, b]) =>
            t.replace(
              new RegExp("\\{\\{(\\s*)(steps\\.)?" + a.replace(/[-]/g, "\\-") + "(?=[.}\\s])", "g"),
              "{{$1$2" + b,
            ),
          s,
        );
  for (const c of added) {
    for (const k of ["prompt", "template", "source", "expr", "instruction"]) if (c[k]) c[k] = fix(c[k]);
    for (const k in c.args || {}) c.args[k] = fix(c.args[k]);
    for (const a of c.actions || []) {
      a.path = fix(a.path);
      for (const k in a.args || {}) a.args[k] = fix(a.args[k]);
    }
  }
  for (const e of clip.edges || [])
    if (map[e.from] && map[e.to]) flow.edges.push({ ...e, from: map[e.from], to: map[e.to] });
  const newInputs = (clip.inputs || []).filter((i) => !flow.inputs.some((x) => x.name === i.name));
  flow.inputs.push(...newInputs.map((i) => JSON.parse(JSON.stringify(i))));
  setSelection(added.map((c) => c.id));
  changed();
  toast(
    `Pasted ${added.length} step${added.length === 1 ? "" : "s"}${newInputs.length ? `, and the input${newInputs.length === 1 ? "" : "s"} ${newInputs.map((i) => i.name).join(", ")}` : ""}. Connect them where they belong.`,
  );
}

export async function pasteAsNewFlow(clip) {
  clip ||= S.flowClip;
  if (!clip) return toast("Copy some steps in a flow first (select them, then ⌘C).");
  try {
    const flow = await backend.NewFlow("Steps from " + (clip.from || "a flow"));
    await loadList();
    await openFlow(flow.id);
    pasteSteps(clip);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// rename a step and every {{steps.<id>…}} / {{<id>.output_file|buffer_output}} that refers to it
export function renameNode(n, id) {
  const flow = fl().flow;
  id = id.replace(/[^A-Za-z0-9_-]/g, "");
  if (!id || !/^[A-Za-z]/.test(id) || flow.nodes.some((x) => x.id === id && x !== n))
    return toast("Step ids start with a letter and must be unique.");
  const re = new RegExp("\\{\\{\\s*steps\\." + n.id.replace(/[-]/g, "\\-") + "(?=[.}\\s])", "g"),
    rep = "{{steps." + id;
  const re2 = new RegExp(
    "\\{\\{(\\s*)" +
      n.id.replace(/[-]/g, "\\-") +
      "\\.(output_file|buffer_output|item_index|item_count|item)(?=[.}\\s])",
    "g",
  );
  const fix = (s) => (typeof s === "string" ? s.replace(re, rep).replace(re2, "{{$1" + id + ".$2") : s);
  for (const x of flow.nodes) {
    for (const k of ["prompt", "template", "source", "expr", "instruction"]) if (x[k]) x[k] = fix(x[k]);
    if (x.args) for (const k in x.args) x.args[k] = fix(x.args[k]);
    for (const a of x.actions || []) {
      a.path = fix(a.path);
      if (a.args) for (const k in a.args) a.args[k] = fix(a.args[k]);
    }
  }
  flow.edges.forEach((e) => {
    if (e.from === n.id) e.from = id;
    if (e.to === n.id) e.to = id;
  });
  if (fl().sel === n.id) fl().sel = id;
  n.id = id;
  changed();
}

export function edgeLabel(a, b, e) {
  if (a.type === "decision") return e?.branch ? "◆ " + branchName(a, e.branch) : "◆ always";
  if (b.type !== "tool") return "";
  const esc = (x) => x.replace(/[-.*+?^${}()|[\]\\]/g, "\\$&"),
    r = esc(stepRef(fl().flow, a.id)),
    id = esc(a.id);
  const re = new RegExp(
    "\\{\\{\\s*" +
      (a.type === "start"
        ? "inputs\\."
        : "(steps\\." + id + "|input_file|input|(" + r + "|" + id + ")\\.(output_file|buffer_output))") +
      "(?=[.}\\s])",
  );
  const used = Object.entries(b.args || {})
    .filter(([, v]) => re.test(v))
    .map(([k]) => k);
  if (used.length) return "→ " + used.join(", ");
  if ((b.instruction || "").trim()) return "→ AI";
  return a.type === "start" ? "" : "→ piped in";
}

function Issues() {
  const f = fl(),
    p = usePopover();
  const errs = f.issues.filter((i) => i.level === "error").length,
    warns = f.issues.length - errs;
  if (!f.issues.length) return <span className="badge badge-ok">no problems</span>;
  const label =
    (errs ? `${errs} problem${errs === 1 ? "" : "s"}` : "") +
    (errs && warns ? " · " : "") +
    (warns ? `${warns} warning${warns === 1 ? "" : "s"}` : "");
  return (
    <span className="fl-issues" ref={p.wrap}>
      <button
        ref={p.btn}
        className={"btn secondary fl-issue-btn" + (errs ? " has-errors" : "")}
        aria-haspopup="true"
        aria-expanded={p.open}
        title="Show the problems; pick one to go to its step"
        onClick={() => p.setOpen(!p.open)}
      >
        <Icon.warn /> {label} ▾
      </button>
      {p.open && (
        <div className="fl-issue-pop" role="list">
          {f.issues.map((i, k) => (
            <button
              key={k}
              role="listitem"
              className={"fl-issue " + i.level}
              onClick={() => {
                f.sel = i.node || null;
                f.selEdge = null;
                p.setOpen(false);
                update();
              }}
            >
              <b>{i.node ? nodeOf(i.node)?.title || i.node : "Flow"}</b> {i.message}
              {i.node && <span className="muted"> · go to step</span>}
            </button>
          ))}
        </div>
      )}
    </span>
  );
}

// --- screen --------------------------------------------------------------------------------

export function Flows() {
  const f = fl(),
    imp = useRef(null);
  useEffect(() => {
    if (S.ws) {
      loadList();
      refreshExtensions();
      loadBuiltins();
      loadLayout();
    }
  }, [S.ws?.root]);
  // shown again (switching from another view): the flow may have changed meanwhile
  useEffect(() => {
    if (S.ws && fl().flow) checkDisk("shown");
  }, []);
  useEffect(
    () => () => {
      if (fl().dirty) save();
    },
    [],
  );
  if (!S.ws) return <NeedWorkspace what="the flow designer" />;
  const importInput = (
    <input
      ref={imp}
      type="file"
      accept=".json,application/json"
      hidden
      onChange={(e) => {
        const file = e.target.files[0];
        e.target.value = "";
        if (file) importFlow(file);
      }}
    />
  );
  return (
    <div className={"flows" + (f.flow ? " is-editing" : " is-library")}>
      {importInput}
      {f.flow ? (
        <div className="fl-main">
          <header className="fl-bar">
            <button className="fl-back" title="Back to all flows" onClick={closeFlow}>
              <Icon.back /> Flows
            </button>
            <span className="fl-crumb" aria-hidden="true">
              /
            </span>
            <input
              className="fl-name"
              size={Math.min(48, Math.max(12, (f.flow.name || "").length + 2))}
              value={f.flow.name}
              aria-label="Flow name"
              onChange={(e) => {
                f.flow.name = e.target.value;
                changed();
              }}
            />
            {f.flow.nodes.some((n) => n.type === "chat") && (
              <span className="badge badge-info" title="A Chatflow: chatted with on the Chats screen">
                chat
              </span>
            )}
            <Issues />
            <span className={"fl-saved" + (f.dirty && !f.saving ? " is-dirty" : " muted")} role="status">
              {f.saving ? "Saving…" : f.dirty ? "Unsaved changes" : "Saved"}
            </span>
            <span className="status-spacer" />
            <button
              className="btn secondary"
              onClick={() => {
                f.sel = null;
                f.selEdge = null;
                update();
              }}
            >
              Flow settings
            </button>
            <button
              className="btn secondary"
              title={
                (f.dirty ? "Saves the flow, then opens it in Run flows" : "Open this flow in Run flows") +
                (f.flow.nodes.some((n) => n.type === "chat")
                  ? ": its setup runs there, then the chat opens"
                  : "")
              }
              onClick={async () => {
                if (f.dirty) await save();
                openFlowRunner(f.flow.id);
              }}
            >
              {f.flow.nodes.some((n) => n.type === "chat") ? (
                <>
                  <Icon.chat /> Start chat
                </>
              ) : (
                <>
                  <Icon.run /> Run
                </>
              )}
            </button>
            <button
              className="btn primary"
              disabled={f.saving || !f.dirty}
              title="Save (⌘S / Ctrl+S)"
              onClick={save}
            >
              Save
            </button>
            <Menu
              label="More flow actions"
              items={[
                ["Export…", exportFlow],
                ["Rename…", renameFlow],
                ["Duplicate", duplicateFlow],
                ["Import a flow…", () => imp.current?.click()],
                ["Delete flow…", deleteFlow, { danger: true }],
              ]}
            />
          </header>
          {f.repairs && (
            <div className="banner fl-conflict" role="status">
              <span>
                <b>This flow's file did not fit the Flow Designer format</b> (it was changed outside the
                designer, for example by the AI). It was repaired and saved:
                <ul className="ai-changes">
                  {f.repairs.map((r, i) => (
                    <li key={i}>{r}</li>
                  ))}
                </ul>
                Check the steps. Agents should change flows with the save_flow tool, which saves a new
                version.
              </span>
              <button
                className="link"
                onClick={() => {
                  f.repairs = null;
                  update();
                }}
              >
                Dismiss
              </button>
            </div>
          )}
          {f.conflict && (
            <div className="banner fl-conflict" role="alert">
              {f.conflict.gone ? (
                <>
                  <b>This flow's file was deleted outside the designer.</b> Your version is still here.
                  <span className="row-actions">
                    <button className="btn primary" onClick={() => resolveConflict("mine")}>
                      Save my version again
                    </button>
                  </span>
                </>
              ) : (
                <>
                  <b>This flow was changed outside the designer</b> (for example by the AI in the Playground)
                  while you had unsaved edits here. Nothing has been overwritten.
                  <span className="row-actions">
                    <button className="btn primary" onClick={() => resolveConflict("theirs")}>
                      Load the new version
                    </button>
                    <button className="btn secondary" onClick={() => resolveConflict("mine")}>
                      Keep mine (overwrite it)
                    </button>
                  </span>
                </>
              )}
            </div>
          )}
          <div
            className={
              "fl-work" +
              (S.layout["flows:pal:c"] ? " pal-c" : "") +
              (S.layout["flows:insp:c"] ? " insp-c" : "")
            }
          >
            {!S.layout["flows:pal:c"] && (
              <Pane
                k="flows-pal"
                v="--pane-pal"
                pane=".fl-palette"
                min={160}
                max={360}
                label="Resize the steps and tools panel"
              />
            )}
            {!S.layout["flows:insp:c"] && (
              <Pane
                k="flows-insp"
                v="--pane-insp"
                pane=".fl-inspector"
                edge="left"
                min={260}
                max={560}
                label="Resize the inspector"
              />
            )}
            <Palette />
            <Canvas />
            <Inspector />
            {f.ai?.open && <AIDesigner />}
            {wz().open && <FlowWizard />}
          </div>
        </div>
      ) : (
        <section className="view fl-lib-view">
          <FlowLibrary
            list={f.list}
            actions={{
              open: openFlow,
              run: openFlowRunner,
              create: newFlow,
              withAI: () => openWizard(),
              importFile: () => imp.current?.click(),
              paste: () => pasteFromClipboard(true),
              guide: () => openFlowGuide(""),
            }}
          />
          {f.ai?.open && <AIDesigner inline />}
          {wz().open && <FlowWizard />}
        </section>
      )}
    </div>
  );
}

// --- the AI designer ---------------------------------------------------------------

// Back / Forward: the flow open in the designer (navigation.js)
registerPlace("flows", {
  key: () => S.fl?.flow?.id || "",
  restore: (id) => (id ? openFlow(id) : closeFlow()),
  restoreEmpty: true, // "" is the list of flows: Back from a flow returns to it
});
