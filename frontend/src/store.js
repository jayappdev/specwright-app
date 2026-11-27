// App state. Server data (workspace, workflow, runs) comes from the backend;
// UI preferences (appearance, current view, chosen agent) live in
// localStorage. Components read `S` and call `update()` after mutating it.
import { useSyncExternalStore } from "react";
import { backend } from "./backend.js";

const PREFS_KEY = "codegen.prefs.v1";
const loadPrefs = () => {
  try {
    return JSON.parse(localStorage.getItem(PREFS_KEY) || "null");
  } catch {
    return null;
  }
};
// savePrefs writes the persisted fields, but only when they changed: update()
// runs for every streamed event and keystroke, and most of those touch no pref.
let savedPrefs = "";
const savePrefs = () => {
  const text = JSON.stringify({
    appearance: S.appearance,
    view: S.view,
    setup: S.setup,
    lastWorkspace: S.ws?.root || S.lastWorkspace,
    tab: S.run.tab,
    panes: S.panes,
    layout: S.layout,
    rail: S.rail,
  });
  if (text === savedPrefs) return;
  try {
    localStorage.setItem(PREFS_KEY, text);
    savedPrefs = text;
  } catch {
    /* storage unavailable */
  }
};

const p = loadPrefs() || {};
// layout flags (see S.layout), e.g. "flows:pal:c" = Flow designer palette collapsed
const LAYOUT_FLAGS = ["files:ai:c", "flows:insp:c", "flows:list:c", "flows:pal:c", "wf:lc:c", "wiz:all"];
export const S = {
  appearance: {
    theme: "system",
    skin: "workbench",
    fontSize: 13,
    font: "",
    codeFont: "",
    ...(p.appearance || {}),
  },
  view: p.view || "home",
  setup: { done: false, provider: "", source: "", model: "", ...(p.setup || {}) },
  lastWorkspace: p.lastWorkspace || "",
  // side pane widths in px, by pane key (PaneHandle); persisted
  panes: Object.fromEntries(
    Object.entries(p.panes || {}).filter(([k, v]) => typeof v === "number" && !LAYOUT_FLAGS.includes(k)),
  ),
  // hidden/collapsed parts of the layout, by key (true when on); persisted.
  // Older prefs kept these in panes: they move over here.
  layout: {
    ...Object.fromEntries(LAYOUT_FLAGS.filter((k) => p.panes?.[k]).map((k) => [k, true])),
    ...(p.layout || {}),
  },
  rail: p.rail || "", // sidebar labels: "open" | "closed" | "" (automatic: open on wide windows)
  info: null,
  ws: null, // WorkspaceState from the hub
  wsError: "",
  wf: null, // workflow
  sel: 0, // selected step index
  events: {}, // runId -> events[]
  loaded: {}, // runId -> true when history was fetched
  run: { tab: p.tab || "activity", dbgFilter: "all", dbgOpen: [], dbgAll: false },
  files: { list: [], sel: "", content: "", open: {}, compile: null },
  tb: null, // tool builder state
  secrets: {}, // name -> bool: has a value without asking; values never kept here
  secretInfo: {}, // name -> { source, env, envSet, via: session|env|ask|missing }
  permAsks: [], // pending "permission.request" events (the AI asks to use a tool)
  secretAsks: [], // pending "secret.request" events (a tool waits for the user)
  questionAsks: [], // pending "question.request" events (ask_user waits for the user)
  flowRuns: {}, // flow run id → latest state (from "flow.state" events)
  results: {}, // run id → its results folder (RunResults), loaded on demand
  threads: {}, // run id → the conversation it continues (RunThread)
  history: { runs: [], sel: "", pos: 0, playing: false },
  // tool reliability: name → latest calls' numbers (toolStats.js)
  toolStats: null,
  // the flow lists' search and tag filters, per list (flowLists.jsx)
  flowFilter: {},
  metrics: null,
  wizSection: "w-lang",
  toast: "",
  toastKind: "", // "info" | "error"
  notices: [], // the last 20 toasts { msg, kind, at }, newest first (Notices.jsx)
  noticesUnread: 0,

  // --- session state owned by one feature module (created there on first use) ---
  // Each module keeps its own factory with defaults (e.g. fl() in Flows.jsx);
  // they are listed here so the whole shape of S is in one place.
  admin: null, // AdminStatus from the hub (store.refreshAdmin)
  managed: null, // managed tool catalog (store.js, Admin.jsx)
  builtins: null, // built-in tools (Builtins.jsx)
  flowBuiltins: null, // built-in tools usable as flow steps (Flows.jsx, Inputs.jsx)
  toolTags: null, // tool name -> tags (Tools.jsx)
  pkg: null, // package and proxy settings (Packages.jsx)
  housekeeping: null, // auto-archive settings (RunHousekeeping.jsx)
  models: null, // model list for ModelPicker
  recentRuns: null, // runs offered by "Refer to earlier runs" (Playground.jsx)
  tf: null, // Tools list filter (Tools.jsx tf())
  bt: null, // selected built-in tool (Builtins.jsx)
  cx: null, // selected Copilot extension (CopilotExts.jsx)
  sk: null, // Spec Kit card state (Inputs.jsx sk())
  fa: null, // Files › Ask AI (Files.jsx fa())
  pg: null, // Prompt playground (Playground.jsx pg())
  pgPicker: null, // "Refer to earlier runs" dialog (Playground.jsx)
  fl: null, // Flow designer (Flows.jsx fl())
  flowClip: null, // copied flow steps (Flows.jsx)
  frun: null, // Run flows (FlowRun.jsx frs())
  guide: "", // Flow step guide: step type in view (FlowGuide.jsx)
  hk: null, // run list selection and groups (RunHousekeeping.jsx)
  rulesUi: null, // rule drawer and bulk editor (RulesEditors.jsx)
  libEdit: null, // skill / script editor drawer (LibraryEditor.jsx)
  preview: null, // file preview panel (FilePreview.jsx)
  dialog: null, // confirm / ask dialog (Dialog.jsx)
  palette: null, // command palette { q } while open (CommandPalette.jsx)
  cmdFlows: null, // flows listed in the command palette
  metricsRange: null, // Metrics range (Metrics.jsx)
  metricsShown: null,
  metricsLoading: false,
  settingsTab: "appearance", // Settings tab (Appearance.jsx)
  themeFamily: "", // theme family tab in Settings › Appearance
  m3seed: "", // Android theme: own seed color and brightness
  m3dark: true,
  railOpen: false, // computed by App from rail and the window width
  scrollTo: "", // one-shot: element id to scroll to after a view opens
  wfChanges: false, // approval banner: "Request changes" field open (Workflow.jsx)
  zipBusy: false, // project zip being written
  toolShare: null, // Tools export / import dialog (views/tools/Share.jsx)
};

let version = 0;
const subs = new Set();
// afterUpdate: called after every update, before the screen redraws (the
// Back / Forward history records where you are: navigation.js)
export const afterUpdate = new Set();
export const savePrefsNow = () => savePrefs();
// update() after changing S. Several calls in one task (an event handler, a
// burst of streamed events) share one notification, sent in a microtask: it
// still runs before any timer, so code that does update() then setTimeout(…)
// sees the new render.
//
// Typing is the exception: a text box bound to S must re-render inside the
// input event. Otherwise React puts the old value back when the event ends
// and the new one arrives a moment later, which moves the caret to the end,
// so text could only be typed at the end of a box. React batches these
// notifications itself and renders once, before the event ends.
let pending = false;
const TYPING = new Set([
  "input",
  "change",
  "beforeinput",
  "compositionend",
  "paste",
  "cut",
  "drop",
  "keydown",
]);
export function update() {
  version++;
  const ev = typeof window !== "undefined" ? window.event : undefined;
  if (ev && TYPING.has(ev.type)) subs.forEach((f) => f());
  if (pending) return;
  pending = true;
  queueMicrotask(() => {
    pending = false;
    savePrefs();
    afterUpdate.forEach((f) => f());
    subs.forEach((f) => f());
  });
}
export function useApp() {
  return useSyncExternalStore(
    (f) => (subs.add(f), () => subs.delete(f)),
    () => version,
  );
}

// toast(msg, kind): kind "error" stays 8 s (and can be closed), the rest 3.2 s;
// pointer over the toast holds it (toastHold/toastRelease).
let toastTimer;
const TOAST_MS = { error: 8000 };
export function hideToast() {
  clearTimeout(toastTimer);
  S.toast = "";
  S.toastKind = "";
  update();
}
export function toastRelease() {
  clearTimeout(toastTimer);
  if (S.toast) toastTimer = setTimeout(hideToast, TOAST_MS[S.toastKind] || 3200);
}
export function toastHold() {
  clearTimeout(toastTimer);
}
export function toast(msg, kind = "info") {
  S.toast = String(msg);
  S.toastKind = kind;
  // the message list on the status bar keeps them after the toast is gone
  S.notices = [{ msg: S.toast, kind, at: Date.now() }, ...(S.notices || [])].slice(0, 20);
  S.noticesUnread = (S.noticesUnread || 0) + 1;
  update();
  toastRelease();
}
export const errMsg = (e) => String(e?.message || e || "error").replace(/^Error:\s*/, "");
export async function call(fn, ...args) {
  try {
    return await fn(...args);
  } catch (e) {
    toast(errMsg(e), "error");
    throw e;
  }
}

// Debounced saves so typing does not write a file per keystroke.
const timers = {};
export function debounce(key, ms, fn) {
  clearTimeout(timers[key]);
  timers[key] = setTimeout(fn, ms);
}
export const saveProfile = () =>
  debounce(
    "profile",
    350,
    () => S.ws && backend.SaveProfile(S.ws.profile).catch((e) => toast(errMsg(e), "error")),
  );
export const saveWorkflow = () =>
  debounce("workflow", 350, () => S.wf && backend.SaveWorkflow(S.wf).catch((e) => toast(errMsg(e), "error")));

export async function openWorkspace(path, viaPicker) {
  try {
    const ws = viaPicker ? await backend.PickFolder() : await backend.OpenWorkspace(path);
    if (!ws || !ws.root) return;
    // Go encodes empty slices as null
    ws.libraries = ws.libraries || [];
    ws.extensions = ws.extensions || [];
    if (ws.profile)
      for (const k of ["global", "project", "folders", "rules"]) ws.profile[k] = ws.profile[k] || [];
    if (ws.workflow) ws.workflow.steps ||= [];
    S.ws = ws;
    S.wf = ws.workflow;
    S.wsError = "";
    S.lastWorkspace = ws.root;
    S.sel = currentStep(S.wf);
    S.files = { list: [], sel: "", content: "", open: {}, compile: null };
    S.history = { runs: [], sel: "", pos: 0, playing: false };
    S.pkg = null;
    update();
    loadStepEvents(S.sel);
    refreshSecrets();
  } catch (e) {
    S.wsError = errMsg(e);
    update();
  }
}

export async function refreshWorkflow() {
  if (!S.ws) return;
  const wf = await backend.Workflow();
  // keep local edits that may not have been saved yet
  if (!wf) return;
  wf.steps ||= [];
  if (S.wf)
    for (const st of wf.steps) {
      const o = S.wf.steps.find((x) => x.id === st.id);
      if (o)
        Object.assign(st, {
          input: o.input,
          instructions: o.instructions,
          options: o.options,
          feedback: o.feedback,
        });
    }
  S.wf = wf;
  update();
}

export async function refreshExtensions() {
  if (!S.ws) return;
  S.ws.extensions = (await backend.Extensions().catch(() => [])) || [];
  update();
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
  for (const st of info || []) {
    S.secretInfo[st.name] = st;
    S.secrets[st.name] = st.via === "session" || st.via === "env";
  }
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

export const currentStep = (wf) => {
  if (!wf) return 0;
  const k = wf.steps.findIndex((s) => s.status !== "done");
  return k === -1 ? wf.steps.length - 1 : k;
};

// Events from the hub.
let wfTimer;
// Events of every run seen in a session would otherwise stay in memory. Keep
// the runs on screen, the live ones and the last MAX_RUNS touched; others are
// dropped and fetched again (S.loaded) when opened.
const MAX_RUNS = 30;
const touched = new Map(); // run id -> last time an event arrived or it was shown
export function touchRun(id) {
  if (id) touched.set(id, Date.now());
}
function onScreen() {
  const ids = new Set([
    S.history?.sel,
    S.pg?.sel,
    S.frun?.runId,
    S.fa?.runId,
    ...(S.wf?.steps || []).map((s) => s.lastRun),
  ]);
  for (const [id, st] of Object.entries(S.flowRuns || {}))
    if (["running", "review", "scheduled"].includes(st?.status)) ids.add(id);
  return ids;
}
function pruneEvents() {
  const ids = Object.keys(S.events);
  if (ids.length <= MAX_RUNS) return;
  const keep = onScreen();
  const old = ids
    .filter((id) => !keep.has(id) && (S.events[id] || []).some((x) => x.type === "run.end"))
    .sort((a, b) => (touched.get(a) || 0) - (touched.get(b) || 0));
  for (const id of old.slice(0, ids.length - MAX_RUNS)) {
    delete S.events[id];
    delete S.loaded[id];
    touched.delete(id);
  }
}

// the Chats screen's event handler (set by views/Chats.jsx)
let chatListener = null;
export const setChatListener = (f) => (chatListener = f);

export function onHubEvent(e) {
  if (e.run_id && e.type !== "workflow.changed") {
    if (!S.events[e.run_id]) pruneEvents();
    touchRun(e.run_id);
    const arr = (S.events[e.run_id] ||= []);
    // an event without a number (a permission or secret request) is never a repeat
    if (!e.seq || !arr.length || arr[arr.length - 1].seq < e.seq) arr.push(e);
    else if (!arr.some((x) => x.seq === e.seq)) {
      arr.push(e);
      arr.sort((a, b) => a.seq - b.seq);
    }
    // a run the Playground list does not have yet (a trigger's or an
    // agent's): it reads the list again
    if ((e.type === "run.start" || e.type === "run.end") && S.pg && !S.pg.runs.some((x) => x.id === e.run_id))
      S.pgStale = true;
  }
  switch (e.type) {
    case "workflow.changed":
      clearTimeout(wfTimer);
      wfTimer = setTimeout(refreshWorkflow, 60);
      break;
    case "workspace.files":
      // flows changed on disk (an agent in the Playground, another window, git):
      // the Flow designer reloads them or asks (views/Flows.jsx listens)
      if (
        typeof window !== "undefined" &&
        (e.changes || []).some((c) => c.path.startsWith(".codegen/flows/"))
      )
        window.dispatchEvent(new CustomEvent("specwright:flows-changed", { detail: e.changes }));
      if (S.view === "files" && S.ws)
        backend
          .ListFiles()
          .then((l) => {
            S.files.list = l || [];
            update();
          })
          .catch(() => {});
      if (
        (e.changes || []).some(
          (c) =>
            c.path === "package.json" ||
            c.path.startsWith(".codegen/skills") ||
            c.path.startsWith(".codegen/scripts") ||
            c.path === ".codegen/extensions.json" || // tools changed by an agent or another window
            c.path.startsWith(".codegen/tools/") ||
            c.path.startsWith(".github/extensions/"),
        )
      )
        backend
          .Workspace()
          .then((ws) => {
            S.ws = { ...S.ws, extensions: ws.extensions || [], libraries: ws.libraries || [] };
            update();
          })
          .catch(() => {});
      break;
    case "build.line":
      if (S.files.compile?.running) S.files.compile.out += (S.files.compile.out ? "\n" : "") + e.summary;
      break;
    case "store.error": // run history could not be written (disk full, no access)
    case "results.error": // a run's results folder could not be written
      toast(e.summary || "Could not save run history.", "error");
      break;
    case "results.saved":
      delete S.results[e.run_id];
      break;
    case "flow.state":
      if (e.state) S.flowRuns[e.run_id] = e.state;
      break;
    case "chat.session":
    case "chat.text":
    case "chat.message":
    case "chat.step":
      chatListener?.(e); // views/Chats.jsx
      break;
    case "question.request":
      if (!S.questionAsks.some((r) => r.id === e.id))
        S.questionAsks.push({
          id: e.id,
          title: e.title || "",
          intro: e.intro || "",
          questions: e.questions || [],
          deadline: e.deadline,
          step: e.step || "",
          run: e.run_id || "",
        });
      break;
    case "question.resolved":
      S.questionAsks = S.questionAsks.filter((r) => r.id !== e.id);
      break;
    case "secret.request":
      if (!S.secretAsks.some((r) => r.id === e.id))
        S.secretAsks.push({
          id: e.id,
          tool: e.tool,
          secrets: e.secrets || [],
          reason: e.reason || "",
          step: e.step || "",
          run: e.run_id || "",
          at: Date.now(),
        });
      break;
    case "notify": // the notify tool: also shown in the app (the OS shows its own notification)
      toast((e.title || e.summary) + (e.message ? ": " + e.message : ""), "info");
      break;
    case "trigger.queued": // a run waits for its turn (or was cancelled): Run flows shows it
      if (S.frun)
        backend
          .TriggerQueue()
          .then((q) => {
            S.frun.queue = q || [];
            update();
          })
          .catch(() => {});
      break;
    case "trigger.fired": // a trigger started a run: Run flows and History list it
      if (S.frun)
        backend
          .FlowRuns(100)
          .then((runs) => {
            S.frun.runs = runs || [];
            return backend.FlowTriggers("");
          })
          .then((t) => {
            S.frun.triggers = t || [];
            return backend.TriggerQueue();
          })
          .then((q) => {
            S.frun.queue = q || [];
            update();
          })
          .catch(() => {});
      if (S.view === "history" && S.history)
        backend
          .Runs(500)
          .then((r) => {
            S.history.runs = r || [];
            update();
          })
          .catch(() => {});
      break;
    case "design.progress": // the flow wizard's AI calls: what it is doing now
      if (e.wizard) {
        S.wizProgress = { id: e.wizard, phase: e.phase || "", msg: e.summary || "", at: Date.now() };
        update();
      }
      break;
    case "permission.request":
      if (!S.permAsks.some((r) => r.id === e.id))
        S.permAsks.push({
          id: e.id,
          tool: e.tool,
          input: e.input || {},
          target: e.target || "",
          step: e.step || "",
          run: e.run_id || "",
          at: Date.now(),
        });
      break;
    case "permission.resolved":
      S.permAsks = S.permAsks.filter((r) => r.id !== e.id);
      break;
    case "secret.resolved":
      S.secretAsks = S.secretAsks.filter((r) => r.id !== e.id);
      refreshSecrets();
      break;
    case "gen.model.text":
    case "gen.tool.call":
      if (S.tb?.gen) S.tb.gen.lines.push(e.summary);
      break;
  }
  update();
}

// Usage of one run, computed from its events exactly as the Go runner does.
export function usageOf(evs = []) {
  const u = {
    in: 0,
    out: 0,
    cached: 0,
    calls: 0,
    premium: 0,
    usd: 0,
    tools: 0,
    toolErr: 0,
    files: [],
    ms: 0,
  };
  for (const e of evs) {
    switch (e.type) {
      case "model.request":
        u.in += e.input_tokens || 0;
        u.cached += e.cached_tokens || 0;
        u.calls++;
        break;
      case "model.response":
        u.out += e.output_tokens || 0;
        break;
      case "model.usage":
        u.usd += e.cost_usd || 0;
        u.premium += e.premium_requests || 0;
        if (e.source === "tool") u.calls += e.calls || 0; // AI a tool used itself
        if (e.input_tokens) {
          u.in += e.input_tokens;
          u.out += e.output_tokens || 0;
          u.cached += e.cached_tokens || 0;
        }
        break;
      case "tool.call":
        u.tools++;
        break;
      case "tool.result":
        if (!e.ok) u.toolErr++;
        break;
      case "fs.write":
      case "fs.edit":
      case "fs.delete": {
        const f = u.files.find((x) => x.path === e.path);
        const op = e.type === "fs.write" ? "created" : e.type === "fs.edit" ? "modified" : "deleted";
        if (f) {
          f.added += e.added || 0;
          f.removed += e.removed || 0;
          if (f.op !== "created") f.op = op;
        } else u.files.push({ path: e.path, op, added: e.added || 0, removed: e.removed || 0 });
        break;
      }
    }
    if (e.dt > u.ms) u.ms = e.dt;
  }
  return u;
}

export function stepUsage(st) {
  return usageOf(st?.lastRun ? S.events[st.lastRun] : []);
}
export function totals() {
  const t = { in: 0, out: 0, premium: 0, usd: 0, tools: 0, files: 0 };
  for (const st of S.wf?.steps || []) {
    const u = stepUsage(st);
    t.in += u.in;
    t.out += u.out;
    t.premium += u.premium;
    t.usd += u.usd;
    t.tools += u.tools;
    t.files += u.files.length;
  }
  return t;
}
export const isCopilot = () => S.setup.provider === "copilot";
// What this computer calls its keychain: Keychain (macOS), Credential Manager (Windows), keyring (Linux).
export const isWindows = () =>
  S.info?.os === "windows" || (!S.info?.os && /Win/.test(navigator.platform || ""));
export const kcName = () =>
  isWindows() ? "Credential Manager" : S.info?.os === "linux" ? "keyring" : "Keychain";
export const fmtK = (n) => (n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + "k" : String(n));
export const fmtMs = (ms) => (ms < 1000 ? ms + " ms" : (ms / 1000).toFixed(1) + " s");
export const fmtCredits = (u) =>
  isCopilot() ? `${u.premium} premium req` : `$${u.usd.toFixed(u.usd < 1 ? 3 : 2)}`;

// Save text: native dialog in the desktop app, a download in the browser.
export async function saveText(name, text, type = "text/plain") {
  if (backend.kind === "desktop") {
    const p = await call(backend.SaveTextFile, name, text);
    if (p) toast("Saved " + p);
    return;
  }
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type }));
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// Download the project (the Spec Kit project folder, with the skills, rules
// and diagrams it uses) as a zip.
export async function downloadProjectZip(withContext = true) {
  if (S.zipBusy) return;
  S.zipBusy = true;
  update();
  try {
    const r = await backend.ExportProjectZip(withContext);
    if (r?.path)
      toast(
        `Saved ${r.files} files (${r.bytes >= 1 << 20 ? (r.bytes / (1 << 20)).toFixed(1) + " MB" : Math.max(1, Math.round(r.bytes / 1024)) + " KB"}) to ${r.path}${r.skipped?.length ? ` · ${r.skipped.length} large file(s) left out` : ""}`,
      );
  } catch (e) {
    toast(errMsg(e), "error");
  }
  S.zipBusy = false;
  update();
}

// Select another Spec Kit project of the workspace ("" = the top folder):
// its workflow, progress and history become the current ones.
export async function switchProject(name) {
  try {
    const ws = await backend.SwitchSpecProject(name);
    S.ws = { ...S.ws, ...ws };
    S.wf = ws.workflow;
    S.sel = currentStep(ws.workflow);
    toast(`Now working on ${name ? name + "/" : "the top folder"}.`);
    update();
    loadStepEvents(S.sel);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}
