// App state. Server data (workspace, workflow, runs) comes from the backend;
// UI preferences (appearance, current view, chosen agent) live in
// localStorage. Components read `S` and call `update()` after mutating it.
import { useSyncExternalStore } from "react";
import { backend } from "./backend.js";

const PREFS_KEY = "codegen.prefs.v1";
const loadPrefs = () => { try { return JSON.parse(localStorage.getItem(PREFS_KEY) || "null"); } catch { return null; } };
const savePrefs = () => {
  try { localStorage.setItem(PREFS_KEY, JSON.stringify({ appearance: S.appearance, view: S.view, setup: S.setup, lastWorkspace: S.ws?.root || S.lastWorkspace, tab: S.run.tab })); } catch { /* storage unavailable */ }
};

const p = loadPrefs() || {};
export const S = {
  appearance: { theme: "system", skin: "workbench", fontSize: 13, font: "", codeFont: "", ...(p.appearance || {}) },
  view: p.view || "wizard",
  setup: { done: false, provider: "", source: "", model: "", ...(p.setup || {}) },
  lastWorkspace: p.lastWorkspace || "",
  info: null,
  ws: null,          // WorkspaceState from the hub
  wsError: "",
  wf: null,          // workflow
  sel: 0,            // selected step index
  events: {},        // runId -> events[]
  loaded: {},        // runId -> true when history was fetched
  run: { tab: p.tab || "activity", dbgFilter: "all", dbgOpen: [], dbgAll: false },
  files: { list: [], sel: "", content: "", open: {}, compile: null },
  tb: null,          // tool builder state
  secrets: {},       // name -> bool: has a value without asking; values never kept here
  secretInfo: {},    // name -> { source, env, envSet, via: session|env|ask|missing }
  permAsks: [],      // pending "permission.request" events (the AI asks to use a tool)
  secretAsks: [],    // pending "secret.request" events (a tool waits for the user)
  flowRuns: {},      // flow run id → latest state (from "flow.state" events)
  results: {},       // run id → its results folder (RunResults), loaded on demand
  threads: {},       // run id → the conversation it continues (RunThread)
  history: { runs: [], sel: "", pos: 0, playing: false },
  metrics: null,
  wizSection: "w-lang",
  toast: "",
};

let version = 0;
const subs = new Set();
export function update() { version++; savePrefs(); subs.forEach((f) => f()); }
export function useApp() { return useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => version); }

let toastTimer;
export function toast(msg) { S.toast = String(msg); update(); clearTimeout(toastTimer); toastTimer = setTimeout(() => { S.toast = ""; update(); }, 3200); }
export const errMsg = (e) => String(e?.message || e || "error").replace(/^Error:\s*/, "");
export async function call(fn, ...args) {
  try { return await fn(...args); } catch (e) { toast(errMsg(e)); throw e; }
}

// Debounced saves so typing does not write a file per keystroke.
const timers = {};
export function debounce(key, ms, fn) { clearTimeout(timers[key]); timers[key] = setTimeout(fn, ms); }
export const saveProfile = () => debounce("profile", 350, () => S.ws && backend.SaveProfile(S.ws.profile).catch((e) => toast(errMsg(e))));
export const saveWorkflow = () => debounce("workflow", 350, () => S.wf && backend.SaveWorkflow(S.wf).catch((e) => toast(errMsg(e))));

export async function openWorkspace(path, viaPicker) {
  try {
    const ws = viaPicker ? await backend.PickFolder() : await backend.OpenWorkspace(path);
    if (!ws || !ws.root) return;
    // Go encodes empty slices as null
    ws.libraries = ws.libraries || []; ws.extensions = ws.extensions || [];
    if (ws.profile) for (const k of ["global", "project", "folders", "rules"]) ws.profile[k] = ws.profile[k] || [];
    if (ws.workflow) ws.workflow.steps ||= [];
    S.ws = ws; S.wf = ws.workflow; S.wsError = ""; S.lastWorkspace = ws.root;
    S.sel = currentStep(S.wf);
    S.files = { list: [], sel: "", content: "", open: {}, compile: null };
    S.history = { runs: [], sel: "", pos: 0, playing: false };
    S.pkg = null;
    update();
    loadStepEvents(S.sel);
    refreshSecrets();
  } catch (e) { S.wsError = errMsg(e); update(); }
}

export async function refreshWorkflow() {
  if (!S.ws) return;
  const wf = await backend.Workflow();
  // keep local edits that may not have been saved yet
  if (!wf) return;
  wf.steps ||= [];
  if (S.wf) for (const st of wf.steps) { const o = S.wf.steps.find((x) => x.id === st.id); if (o) Object.assign(st, { input: o.input, instructions: o.instructions, options: o.options, feedback: o.feedback }); }
  S.wf = wf; update();
}

export async function refreshExtensions() {
  if (!S.ws) return;
  S.ws.extensions = (await backend.Extensions().catch(() => [])) || []; update();
}

// Admin mode state (key, lock, trust) and the global managed tools.
export async function refreshAdmin() {
  S.admin = await backend.AdminStatus().catch(() => null);
  S.managed = (await backend.ManagedTools().catch(() => [])) || [];
  update();
}

export async function refreshSecrets() {
  // the draft being edited wins over the saved copy of the same secret
  const byName = new Map();
  for (const s of (S.ws?.extensions || []).flatMap((e) => e.secrets || [])) byName.set(s.name, s);
  for (const s of S.tb?.draft?.secrets || []) byName.set(s.name, s);
  if (!byName.size) return;
  const info = await backend.SecretInfo([...byName.values()]).catch(() => []);
  for (const st of info || []) { S.secretInfo[st.name] = st; S.secrets[st.name] = st.via === "session" || st.via === "env"; }
  update();
}
export const refreshSecretsSoon = () => debounce("secrets", 250, refreshSecrets);

export async function loadStepEvents(i) {
  const st = S.wf?.steps[i];
  if (!st?.lastRun || S.loaded[st.lastRun]) return;
  S.loaded[st.lastRun] = true;
  const evs = await backend.RunEvents(st.lastRun).catch(() => []);
  const live = S.events[st.lastRun] || [];
  const seen = new Set(evs.map((e) => e.seq));
  S.events[st.lastRun] = evs.concat(live.filter((e) => !seen.has(e.seq))).sort((a, b) => a.seq - b.seq);
  update();
}

export const currentStep = (wf) => { if (!wf) return 0; const k = wf.steps.findIndex((s) => s.status !== "done"); return k === -1 ? wf.steps.length - 1 : k; };

// Events from the hub.
let wfTimer;
export function onHubEvent(e) {
  if (e.run_id && e.type !== "workflow.changed") {
    const arr = (S.events[e.run_id] ||= []);
    if (!arr.length || arr[arr.length - 1].seq < e.seq) arr.push(e);
    else if (!arr.some((x) => x.seq === e.seq)) { arr.push(e); arr.sort((a, b) => a.seq - b.seq); }
  }
  switch (e.type) {
    case "workflow.changed":
      clearTimeout(wfTimer); wfTimer = setTimeout(refreshWorkflow, 60);
      break;
    case "workspace.files":
      if (S.view === "files" && S.ws) backend.ListFiles().then((l) => { S.files.list = l || []; update(); }).catch(() => {});
      if ((e.changes || []).some((c) => c.path === "package.json" || c.path.startsWith(".codegen/skills") || c.path.startsWith(".codegen/scripts"))) backend.Workspace().then((ws) => { S.ws = { ...S.ws, extensions: ws.extensions || [], libraries: ws.libraries || [] }; update(); }).catch(() => {});
      break;
    case "build.line":
      if (S.files.compile?.running) S.files.compile.out += (S.files.compile.out ? "\n" : "") + e.summary;
      break;
    case "results.saved":
      delete S.results[e.run_id];
      break;
    case "flow.state":
      if (e.state) S.flowRuns[e.run_id] = e.state;
      break;
    case "secret.request":
      if (!S.secretAsks.some((r) => r.id === e.id)) S.secretAsks.push({ id: e.id, tool: e.tool, secrets: e.secrets || [], reason: e.reason || "", step: e.step || "", run: e.run_id || "", at: Date.now() });
      break;
    case "permission.request":
      if (!S.permAsks.some((r) => r.id === e.id)) S.permAsks.push({ id: e.id, tool: e.tool, input: e.input || {}, target: e.target || "", step: e.step || "", run: e.run_id || "", at: Date.now() });
      break;
    case "permission.resolved":
      S.permAsks = S.permAsks.filter((r) => r.id !== e.id);
      break;
    case "secret.resolved":
      S.secretAsks = S.secretAsks.filter((r) => r.id !== e.id);
      refreshSecrets();
      break;
    case "gen.model.text": case "gen.tool.call":
      if (S.tb?.gen) S.tb.gen.lines.push(e.summary);
      break;
  }
  update();
}

// Usage of one run, computed from its events exactly as the Go runner does.
export function usageOf(evs = []) {
  const u = { in: 0, out: 0, cached: 0, calls: 0, premium: 0, usd: 0, tools: 0, toolErr: 0, files: [], ms: 0 };
  for (const e of evs) {
    switch (e.type) {
      case "model.request": u.in += e.input_tokens || 0; u.cached += e.cached_tokens || 0; u.calls++; break;
      case "model.response": u.out += e.output_tokens || 0; break;
      case "model.usage":
        u.usd += e.cost_usd || 0; u.premium += e.premium_requests || 0;
        if (e.source === "tool") u.calls += e.calls || 0; // AI a tool used itself
        if (e.input_tokens) { u.in += e.input_tokens; u.out += e.output_tokens || 0; u.cached += e.cached_tokens || 0; }
        break;
      case "tool.call": u.tools++; break;
      case "tool.result": if (!e.ok) u.toolErr++; break;
      case "fs.write": case "fs.edit": case "fs.delete": {
        const f = u.files.find((x) => x.path === e.path);
        const op = e.type === "fs.write" ? "created" : e.type === "fs.edit" ? "modified" : "deleted";
        if (f) { f.added += e.added || 0; f.removed += e.removed || 0; if (f.op !== "created") f.op = op; }
        else u.files.push({ path: e.path, op, added: e.added || 0, removed: e.removed || 0 });
        break;
      }
    }
    if (e.dt > u.ms) u.ms = e.dt;
  }
  return u;
}

export function stepUsage(st) { return usageOf(st?.lastRun ? S.events[st.lastRun] : []); }
export function totals() {
  const t = { in: 0, out: 0, premium: 0, usd: 0, tools: 0, files: 0 };
  for (const st of S.wf?.steps || []) { const u = stepUsage(st); t.in += u.in; t.out += u.out; t.premium += u.premium; t.usd += u.usd; t.tools += u.tools; t.files += u.files.length; }
  return t;
}
export const isCopilot = () => S.setup.provider === "copilot";
export const fmtK = (n) => n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + "k" : String(n);
export const fmtMs = (ms) => ms < 1000 ? ms + " ms" : (ms / 1000).toFixed(1) + " s";
export const fmtCredits = (u) => isCopilot() ? `${u.premium} premium req` : `$${u.usd.toFixed(u.usd < 1 ? 3 : 2)}`;

// Save text: native dialog in the desktop app, a download in the browser.
export async function saveText(name, text, type = "text/plain") {
  if (backend.kind === "desktop") { const p = await call(backend.SaveTextFile, name, text); if (p) toast("Saved " + p); return; }
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type })); a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// Download the project (the Spec Kit project folder, with the skills, rules
// and diagrams it uses) as a zip.
export async function downloadProjectZip(withContext = true) {
  if (S.zipBusy) return;
  S.zipBusy = true; update();
  try {
    const r = await backend.ExportProjectZip(withContext);
    if (r?.path) toast(`Saved ${r.files} files (${r.bytes >= 1 << 20 ? (r.bytes / (1 << 20)).toFixed(1) + " MB" : Math.max(1, Math.round(r.bytes / 1024)) + " KB"}) to ${r.path}${r.skipped?.length ? ` · ${r.skipped.length} large file(s) left out` : ""}`);
  } catch (e) { toast(errMsg(e)); }
  S.zipBusy = false; update();
}
