// Run flows: execute a flow from the Flow designer.
//   left     flows and their recent runs
//   main     run setup (inputs, credentials, options) or the live run: steps,
//            the review panel (accept, stop, refine with AI, run a command,
//            edit, change settings and retry), outputs and activity
//   sidebar  progress and metrics: steps, elapsed time, tokens, credits, tools
import { ModelPicker } from "../ModelPicker.jsx";
import { useEffect, useRef, useState } from "react";
import { ContentView } from "../ContentView.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, usageOf, fmtK, fmtMs, isCopilot, refreshExtensions } from "../store.js";
import { Icon, Pill } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { StepIcon, openFlowInDesigner } from "./Flows.jsx";
import { RunPanel } from "./Workflow.jsx";
import { continueInPlayground } from "./Playground.jsx";
import { ResultsLink } from "../RunResults.jsx";
import { AiProgress } from "../AiProgress.jsx";
import { AttachBar, DropZone, onPasteAttach, onPasteFiles } from "../Attachments.jsx";
import { FileLink, Linkify } from "../FileLinks.jsx";
import { mdRender } from "../markdown.js";
import { CopyButton } from "../CopyButton.jsx";

const frs = () => (S.frun ||= { flows: null, flowId: "", flow: null, creds: [], keychain: false, runs: [], runId: "", inputs: {}, files: {}, opts: defaultOpts(), secretVals: {}, showOpts: false, tab: "refine", decision: {} });
const defaultOpts = () => ({ review: "ai", repeat: 1, forEach: { input: "", split: "lines", filter: "", max: 100 }, timeoutMinutes: 0, stepTimeoutSeconds: 0, startAt: "", skip: [], stopAfter: "", model: "" });
const LIVE = ["running", "review", "scheduled"];
const STATUS_LABEL = { running: "Running", review: "Waiting for you", scheduled: "Scheduled", done: "Completed", failed: "Failed", stopped: "Stopped", interrupted: "Interrupted", pending: "Pending", skipped: "Skipped" };
const pill = (s) => ({ review: "paused", scheduled: "pending", interrupted: "failed", stopped: "cancelled", skipped: "pending" }[s] || s);

// Open the runner on a flow (from the designer's Run button).
export function openFlowRunner(id) { const r = frs(); r.flowId = id; r.runId = ""; S.view = "flowrun"; loadFlow(id); update(); }

async function loadAll() {
  const r = frs();
  r.flows = await backend.Flows().catch(() => []);
  r.runs = await backend.FlowRuns(100).catch(() => []);
  r.keychain = await backend.KeychainAvailable().catch(() => false);
  if (!r.flowId && r.flows.length) await loadFlow(r.flows[0].id);
  update();
}

async function loadFlow(id) {
  const r = frs();
  try {
    r.flow = await backend.LoadFlow(id);
    r.flowId = id;
    r.inputs = {}; r.files = {};
    for (const i of r.flow.inputs) { r.inputs[i.name] = i.default || ""; r.files[i.name] = (i.files || []).map((p) => ({ path: p, name: p.split("/").pop(), kind: /\.pdf$/i.test(p) ? "pdf" : /\.(png|jpe?g|gif|webp)$/i.test(p) ? "image" : "text", size: 0 })); }
    r.creds = await backend.FlowCredentials(id).catch(() => []);
  } catch (e) { toast(errMsg(e)); }
  update();
}

async function openRun(id) {
  const r = frs();
  r.runId = id;
  if (!S.flowRuns[id]) { try { S.flowRuns[id] = await backend.FlowRun(id); } catch (e) { toast(errMsg(e)); } }
  if (!S.loaded[id]) { S.loaded[id] = true; const evs = (await backend.RunEvents(id).catch(() => [])) || []; const seen = new Set((S.events[id] || []).map((e) => e.seq)); S.events[id] = [...(S.events[id] || []), ...evs.filter((e) => !seen.has(e.seq))].sort((a, b) => a.seq - b.seq); }
  const st = S.flowRuns[id];
  if (st && st.flowId !== r.flowId) await loadFlow(st.flowId);
  update();
}

async function start() {
  const r = frs(), f = r.flow, o = r.opts;
  const inputs = { ...r.inputs }, files = {};
  if (f.inputs.some((i) => (r.files[i.name] || []).some((a) => a.saving))) return toast("Wait until the attached files are saved.");
  // files go separately; the backend checks them and builds each input's value by its type
  for (const i of f.inputs) {
    const paths = (r.files[i.name] || []).filter((a) => a.path).map((a) => a.path);
    if (["file", "files", "clipboard", "multiline"].includes(i.type) && paths.length) files[i.name] = paths;
    if (["file", "files"].includes(i.type) && paths.length) inputs[i.name] = "";
  }
  const opts = { inputs, files, review: o.review, repeat: +o.repeat || 1, timeoutMinutes: +o.timeoutMinutes || 0, stepTimeoutSeconds: +o.stepTimeoutSeconds || 0, skip: o.skip, stopAfter: o.stopAfter, model: o.model.trim() };
  if (o.forEach.input) opts.forEach = { input: o.forEach.input, split: o.forEach.split, filter: o.forEach.filter, max: +o.forEach.max || 100 };
  if (o.startAt) opts.startAt = new Date(o.startAt).toISOString();
  try {
    const st = await backend.RunFlow(f.id, opts);
    S.flowRuns[st.id] = st; S.loaded[st.id] = true; S.events[st.id] ||= [];
    r.runId = st.id; r.decision = {};
    r.runs = [{ id: st.id, workflow: f.id, state: st.status, started: st.started, usage: {} }, ...r.runs];
    update();
  } catch (e) { toast(errMsg(e)); }
}

async function decide(action, extra = {}) {
  const r = frs();
  try { await backend.DecideFlow(r.runId, { action, ...extra }); r.decision = {}; update(); } catch (e) { toast(errMsg(e)); }
}

// --- setup ------------------------------------------------------------------------

// One input, by its type: text, long text (with attachments), number, yes/no,
// choice, one file, several files, or the clipboard (text, images, files).
function InputField({ i }) {
  const r = frs(), v = r.inputs[i.name] ?? "", set = (x) => { r.inputs[i.name] = x; update(); };
  const list = (r.files[i.name] ||= []);
  const label = <span><b>{i.label || i.name}</b>{i.required && <span className="err-text"> *</span>} <code className="inline-code" data-nofile>{`{{inputs.${i.name}}}`}</code></span>;
  const append = (t) => set((v ? v.replace(/\n*$/, "\n") : "") + t);
  let field, simple = true;
  switch (i.type) {
    case "multiline": simple = false; field = <DropZone list={list}><textarea rows={5} value={v} onChange={(e) => set(e.target.value)} onPaste={onPasteAttach(list)} /><AttachBar list={list} insertText={append} /></DropZone>; break;
    case "number": field = <input type="number" step="any" value={v} onChange={(e) => set(e.target.value)} />; break;
    case "boolean": field = <label className="check"><input type="checkbox" checked={v === "true"} onChange={(e) => set(e.target.checked ? "true" : "false")} /> Yes</label>; break;
    case "multichoice": {
      simple = false;
      const opts = (i.options || []).filter(Boolean), picked = new Set(v.split("\n").filter(Boolean));
      const setPicked = (next) => set(opts.filter((o) => next.has(o)).join("\n"));
      field = <div className="mc-list" role="group" aria-label={i.label || i.name}>
        {opts.map((o) => <label key={o} className="check"><input type="checkbox" checked={picked.has(o)} onChange={(e) => { const n = new Set(picked); e.target.checked ? n.add(o) : n.delete(o); setPicked(n); }} /> {o}</label>)}
        {opts.length > 3 && <span className="mc-all"><button type="button" className="link" onClick={() => setPicked(new Set(opts))}>All</button> <button type="button" className="link" onClick={() => setPicked(new Set())}>None</button> <span className="muted">{picked.size} of {opts.length}</span></span>}</div>;
      break;
    }
    case "choice": field = <select value={v} onChange={(e) => set(e.target.value)}><option value="">Choose…</option>{(i.options || []).filter(Boolean).map((o) => <option key={o}>{o}</option>)}</select>; break;
    case "file": case "files": {
      simple = false;
      const single = i.type === "file";
      field = <DropZone list={list} single={single} className="fr-drop">
        {!list.length && <div className="muted fr-drop-hint">{single ? "Drop a file here, or choose one" : "Drop files here, or choose them"}{i.accept ? <> · <code className="inline-code" data-nofile>{i.accept}</code></> : null}</div>}
        <AttachBar list={list} single={single} accept={i.accept} hint={single ? "one file · up to 25 MB" : "several files · up to 25 MB each"} /></DropZone>;
      break;
    }
    case "clipboard": simple = false; field = <DropZone list={list}>
      <textarea rows={4} value={v} placeholder="Paste here (⌘V): text stays text; images and files are attached" onChange={(e) => set(e.target.value)} onPaste={onPasteFiles(list)} />
      <AttachBar list={list} insertText={append} textInline hint="text, images (screenshots, diagrams), PDFs or files" /></DropZone>; break;
    default: field = <input value={v} onChange={(e) => set(e.target.value)} />;
  }
  const Wrap = simple ? "label" : "div";
  return <Wrap className="field fr-input">{label}{field}{i.description && <small className="muted">{i.description}</small>}</Wrap>;
}

const VIA = { env: (c) => <span className="badge badge-ok">${c.env} is set</span>, keychain: () => <span className="badge badge-ok"><Icon.lock /> in the keychain</span>, session: () => <span className="badge badge-ok">entered this session</span>, ask: () => <span className="badge">will ask when needed</span>, missing: (c) => <span className="badge badge-warn">{c.source === "env" ? "$" + c.env + " not set" : "no value"}</span> };

function Credentials() {
  const r = frs();
  if (!r.creds.length) return null;
  const save = async (c, where) => {
    const v = r.secretVals[c.name];
    if (!v) return toast("Type the value first.");
    try {
      if (where === "keychain") await backend.SaveSecretToKeychain(c.name, v); else await backend.SetSecret(c.name, v);
      r.secretVals[c.name] = ""; r.creds = await backend.FlowCredentials(r.flowId);
      toast(where === "keychain" ? `${c.name} saved in the keychain.` : `${c.name} set for this session.`); update();
    } catch (e) { toast(errMsg(e)); }
  };
  return (
    <div className="card"><div className="card-head"><Icon.lock /><h3>Credentials</h3><span className="badge">{r.creds.length}</span></div>
      <p className="wf-desc">The flow's tools need these. Each is read from the session, the environment variable or the system keychain; if none has it, the run pauses and asks you.</p>
      {r.creds.map((c) => (
        <div key={c.name} className="fr-cred">
          <div><code className="inline-code" data-nofile>{c.name}</code> {(VIA[c.via] || VIA.ask)(c)} <small className="muted">{c.desc}{c.tools?.length ? " · " + c.tools.join(", ") : ""}</small></div>
          <div className="fr-cred-row"><input type="password" autoComplete="off" placeholder={c.via === "ask" || c.via === "missing" ? "Enter a value" : "Replace the value"} value={r.secretVals[c.name] || ""} onChange={(e) => { r.secretVals[c.name] = e.target.value; update(); }} />
            <button className="btn secondary" onClick={() => save(c, "session")}>Use this session</button>
            {r.keychain && <button className="btn secondary" title="Kept by the operating system; found next time without asking" onClick={() => save(c, "keychain")}><Icon.lock /> Save in keychain</button>}</div>
        </div>))}
    </div>
  );
}

function Options() {
  const r = frs(), o = r.opts, f = r.flow, set = (k, v) => { o[k] = v; update(); };
  const steps = f.nodes.filter((n) => n.type !== "start");
  return (
    <details className="card fr-opts" open={r.showOpts} onToggle={(e) => { r.showOpts = e.target.open; }}>
      <summary><b>Options</b> <span className="muted">· reviews, repeat, run per item, filter, timing, steps</span></summary>
      <div className="fr-grid">
        <label className="field"><span className="muted">Pause for review</span><select value={o.review} onChange={(e) => set("review", e.target.value)}>
          <option value="ai">After AI steps and marked steps</option><option value="each">After every step</option><option value="marked">Only steps marked for review</option><option value="none">Never</option></select></label>
        <label className="field"><span className="muted">Run the whole flow</span><div className="fr-inline"><input type="number" min="1" max="50" value={o.repeat} onChange={(e) => set("repeat", e.target.value)} style={{ width: 70 }} /> time(s)</div></label>
        <label className="field"><span className="muted">Run once per item of</span><select value={o.forEach.input} onChange={(e) => { o.forEach.input = e.target.value; update(); }}><option value="">— no, run once —</option>{f.inputs.map((i) => <option key={i.name} value={i.name}>{i.label || i.name}</option>)}</select></label>
        {o.forEach.input && <>
          <label className="field"><span className="muted">Items are</span><select value={o.forEach.split} onChange={(e) => { o.forEach.split = e.target.value; update(); }}><option value="lines">One per line</option><option value="json">A JSON array</option><option value="csv">CSV rows</option></select></label>
          <label className="field"><span className="muted">Only items matching (pattern)</span><input value={o.forEach.filter} placeholder="e.g. ^ORD- or error|fail" style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => { o.forEach.filter = e.target.value; update(); }} /></label>
          <label className="field"><span className="muted">At most</span><input type="number" min="1" max="1000" value={o.forEach.max} onChange={(e) => { o.forEach.max = e.target.value; update(); }} /></label>
        </>}
        <label className="field"><span className="muted">Start</span><input type="datetime-local" value={o.startAt} onChange={(e) => set("startAt", e.target.value)} /><small className="muted">{o.startAt ? "Waits until then (keep the app open)" : "Empty: now"}</small></label>
        <label className="field"><span className="muted">Stop the run after (minutes)</span><input type="number" min="0" value={o.timeoutMinutes} onChange={(e) => set("timeoutMinutes", e.target.value)} /><small className="muted">0: no limit</small></label>
        <label className="field"><span className="muted">Each step may take (seconds)</span><input type="number" min="0" value={o.stepTimeoutSeconds} onChange={(e) => set("stepTimeoutSeconds", e.target.value)} /><small className="muted">0: 2 min for tools, 10 min for AI</small></label>
        <label className="field"><span className="muted">Stop after step</span><select value={o.stopAfter} onChange={(e) => set("stopAfter", e.target.value)}><option value="">— run to the end —</option>{steps.map((n) => <option key={n.id} value={n.id}>{n.title || n.id}</option>)}</select></label>
      </div>
      <div className="fr-skip"><span className="muted">Skip steps:</span>{steps.map((n) => <label key={n.id} className="check"><input type="checkbox" checked={o.skip.includes(n.id)} onChange={(e) => { o.skip = e.target.checked ? [...o.skip, n.id] : o.skip.filter((x) => x !== n.id); update(); }} /> {n.title || n.id}</label>)}</div>
    </details>
  );
}

function Setup() {
  const r = frs(), f = r.flow;
  return <>
    <header className="view-head"><select className="fr-switch" aria-label="Flow" value={f.id} onChange={(e) => loadFlow(e.target.value)}>{(r.flows || []).map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}</select><h1>{f.name}</h1><div className="head-controls head-model"><button className="link" onClick={() => openFlowInDesigner(f.id)}>Edit in the designer</button><ModelPicker value={r.opts.model || ""} onChange={(v) => { r.opts.model = v; update(); }} label="Model for AI steps" /></div></header>
    {f.description && <p className="wf-desc">{f.description}</p>}
    <div className="card"><div className="card-head"><Icon.inputs /><h3>Inputs</h3><span className="badge">{f.inputs.length}</span></div>
      {f.inputs.length ? <div className="fr-inputs">{f.inputs.map((i) => <InputField key={i.name} i={i} />)}</div> : <p className="muted">This flow asks for nothing.</p>}</div>
    <Credentials />
    <Options />
    <div className="row-actions fr-go"><button className="btn primary fr-run" onClick={start}><Icon.workflow /> {r.opts.startAt ? "Schedule run" : "Run flow"}</button>
      <span className="muted">{f.nodes.length - 1} steps · {f.nodes.filter((n) => n.type === "output").length} outputs · saves about {estimate(f)} min each run</span></div>
  </>;
}

const estimate = (f) => f.minutesSaved || f.nodes.reduce((m, n) => m + ({ tool: 10, ai: 20, merge: 5, transform: 5, loop: 5, gather: 5, output: 5 }[n.type] || 0), 0);

// --- run view -----------------------------------------------------------------------

function RenderOutput({ o }) {
  if (o.format === "html") return <iframe className="fr-html" sandbox="" srcDoc={o.content} title={o.title} />;
  if (o.format === "md") return <div className="rich md fr-md" dangerouslySetInnerHTML={{ __html: mdRender(o.content) }} />;
  if (o.format === "csv") {
    const rows = o.content.trim().split("\n").slice(0, 200).map((l) => l.split(","));
    return <div className="fr-table"><table className="table"><thead><tr>{rows[0]?.map((c, k) => <th key={k}>{c}</th>)}</tr></thead><tbody>{rows.slice(1).map((r, k) => <tr key={k}>{r.map((c, j) => <td key={j}>{c}</td>)}</tr>)}</tbody></table></div>;
  }
  return <pre className="code fr-pre"><Linkify text={o.content} /></pre>;
}

function Review({ st }) {
  const r = frs(), rv = st.review, d = r.decision, f = r.flow;
  const node = f?.nodes.find((n) => n.id === rv.step);
  const [tab, setTab] = useState(r.tab);
  useEffect(() => { d.edit = rv.output; d.node ||= node ? JSON.parse(JSON.stringify(node)) : null; update(); }, [rv.step, rv.output]);
  const setD = (k, v) => { d[k] = v; update(); };
  const go = (t) => { r.tab = t; setTab(t); };
  const saveBox = (what) => <label className="check"><input type="checkbox" checked={!!d.save} onChange={(e) => setD("save", e.target.checked)} /> Save to the flow for next time <span className="muted">({what}); otherwise just this run</span></label>;
  return (
    <div className="card fr-review" role="region" aria-label="Review">
      <div className="card-head"><span className="fr-review-dot" /><h3>Review: {rv.title}</h3><span className="badge badge-warn">waiting for you</span></div>
      <p className="wf-desc">Check the result. Continue, stop the flow, or improve it first: the improved result goes on to the next steps.</p>
      <ContentView title={rv.title} text={rv.output} files={(() => { const s = st.steps.find((x) => x.id === rv.step); return [s?.inputFile && { label: "input", path: s.inputFile }, s?.outputFile && { label: "output", path: s.outputFile }].filter(Boolean); })()} />
      <div className="row-actions">
        <button className="btn primary" onClick={() => decide("continue")}><Icon.check /> Looks good, continue</button>
        <button className="btn secondary" onClick={() => decide("no-more-reviews")}>Continue without more reviews</button>
        <button className="btn secondary danger-text" onClick={() => decide("stop")}><Icon.x /> Stop the flow</button>
      </div>
      <div className="tabs" role="tablist" style={{ marginTop: 12 }}>{[["refine", "Refine with AI"], ["command", "Run a command"], ["edit", "Edit the result"], ["retry", "Change settings and run again"]].map(([k, l]) => <button key={k} role="tab" className="tab" aria-selected={tab === k} onClick={() => go(k)}>{l}</button>)}</div>
      <div className="fr-iter">
        {tab === "refine" && <><textarea rows={3} value={d.prompt || ""} placeholder={rv.type === "ai" ? "e.g. Shorter, and add a risks section" : "e.g. Keep only the failing tests and group them by package"} onChange={(e) => setD("prompt", e.target.value)} />
          {saveBox(rv.type === "ai" ? "added to the step's prompt" : "as an AI step after this one")}
          <div className="row-actions"><button className="btn primary" disabled={!d.prompt?.trim()} onClick={() => decide("refine", { prompt: d.prompt, save: !!d.save })}><Icon.spark /> Refine</button></div></>}
        {tab === "command" && <><input value={d.command || ""} placeholder="e.g. jq '.items[] | .name'   or   sort -u   or   grep -v DEBUG" style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => setD("command", e.target.value)} />
          <small className="muted">Runs in the project with the result on stdin; its output replaces the result.</small>
          {saveBox("as a command step after this one")}
          <div className="row-actions"><button className="btn primary" disabled={!d.command?.trim()} onClick={() => decide("command", { command: d.command, save: !!d.save })}>Run command</button></div></>}
        {tab === "edit" && <><textarea rows={8} value={d.edit ?? ""} onChange={(e) => setD("edit", e.target.value)} />
          <div className="row-actions"><button className="btn primary" onClick={() => decide("edit", { output: d.edit })}>Use this result</button><span className="muted">For this run only.</span></div></>}
        {tab === "retry" && d.node && <>
          {d.node.type === "tool" && <label className="field"><span className="muted">What the step should do (AI builds the arguments; leave empty to use the values below)</span><textarea rows={3} value={d.node.instruction || ""} onChange={(e) => { d.node.instruction = e.target.value; update(); }} /></label>}
          {d.node.type === "tool" && Object.keys(d.node.args || {}).map((k) => <label key={k} className="field"><span className="muted">{k}{d.node.instruction ? " (empty: AI fills it)" : ""}</span><textarea rows={/sql|query|body|json/i.test(k) ? 3 : 1} value={d.node.args[k]} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => { d.node.args[k] = e.target.value; update(); }} /></label>)}
          {d.node.type === "ai" && <label className="field"><span className="muted">Prompt</span><textarea rows={5} value={d.node.prompt || ""} onChange={(e) => { d.node.prompt = e.target.value; update(); }} /></label>}
          {d.node.type === "transform" && <>{d.node.expr != null && <label className="field"><span className="muted">Expression</span><input value={d.node.expr || ""} onChange={(e) => { d.node.expr = e.target.value; update(); }} /></label>}
            {d.node.template != null && <label className="field"><span className="muted">Template / instruction</span><textarea rows={4} value={d.node.template || ""} onChange={(e) => { d.node.template = e.target.value; update(); }} /></label>}</>}
          {d.node.type === "merge" && <label className="field"><span className="muted">Template</span><textarea rows={4} value={d.node.template || ""} onChange={(e) => { d.node.template = e.target.value; update(); }} /></label>}
          {d.node.type === "output" && <label className="field"><span className="muted">Content</span><textarea rows={4} value={d.node.template || ""} onChange={(e) => { d.node.template = e.target.value; update(); }} /></label>}
          {saveBox("these settings")}
          <div className="row-actions"><button className="btn primary" onClick={() => decide("retry", { node: d.node, save: !!d.save })}>Run the step again</button></div></>}
      </div>
    </div>
  );
}

function Steps({ st }) {
  const [open, setOpen] = useState({});
  const f = frs().flow;
  return (
    <div className="card"><div className="card-head"><Icon.workflow /><h3>Steps</h3>{st.items > 1 || st.passes > 1 ? <span className="badge">{st.passes > 1 ? `run ${st.pass}/${st.passes}` : ""}{st.passes > 1 && st.items > 1 ? " · " : ""}{st.items > 1 ? `item ${st.item}/${st.items}` : ""}</span> : null}{st.itemLabel && <span className="muted fr-item">{st.itemLabel}</span>}</div>
      <ol className="fr-steps">{st.steps.map((s) => {
        const n = f?.nodes.find((x) => x.id === s.id) || { type: s.type, tool: s.tool };
        const can = !!(s.output || s.error || s.args);
        return <li key={s.id} className={"fr-step s-" + s.status}>
          <button className="fr-step-head" disabled={!can} onClick={() => setOpen({ ...open, [s.id]: !open[s.id] })} aria-expanded={!!open[s.id]}>
            <StepIcon n={n} tools={S.ws?.extensions || []} size={28} />
            <span className="fr-step-title"><b>{s.title}</b><small>{s.type === "tool" ? String(s.tool || "").replace(/^builtin:(.*)$/, "$1 · built-in") + (s.why ? " · arguments by AI" : n.instruction ? " · AI builds the arguments" : "") : s.type}{s.iterations ? (s.type === "loop" ? ` · ${s.status === "running" ? `item ${s.iteration || 0} of ${s.iterations}` : `${s.iterations} item${s.iterations === 1 ? "" : "s"}`}` : ` · ↻ item ${s.iteration}/${s.iterations}`) : s.attempts > 1 ? ` · ${s.attempts} attempts` : ""}{s.failedItems ? ` · ${s.failedItems} failed` : ""}{s.path ? ` · → ${s.path}` : ""}{s.notes?.length ? " · " + s.notes[s.notes.length - 1] : ""}</small></span>
            {s.status === "running" ? <span className="spinner" /> : <Pill s={pill(s.status)} label={STATUS_LABEL[s.status] || s.status} />}
            <span className="muted fr-dur">{s.durationMs ? fmtMs(s.durationMs) : ""}</span>
          </button>
          {open[s.id] && <div className="fr-step-body">{s.error && <div className={"banner " + (s.status === "skipped" ? "" : "error")}>{s.error}</div>}
            {s.args && Object.keys(s.args).length > 0 && <div className="fr-args"><div className="muted">{s.why ? <><Icon.spark /> Arguments chosen by AI · {s.why}</> : "Arguments"}</div>
              {Object.entries(s.args).map(([k, v]) => <div key={k} className="fr-arg"><code className="inline-code" data-nofile>{k}</code><pre className="code fr-pre">{typeof v === "string" ? v : JSON.stringify(v, null, 2)}</pre></div>)}</div>}{(s.output || s.outputFile) && <ContentView title={s.title} text={s.output} files={[s.inputFile && { label: "input", path: s.inputFile }, s.outputFile && s.args && { label: "args.json", path: s.outputFile.replace(/[^/]+$/, "args.json") }, s.outputFile && { label: "output", path: s.outputFile }].filter(Boolean)} />}{s.truncated && <small className="muted">Shortened for display; the output file has all of it.</small>}</div>}
          {canFix(st, s) && <div className="fr-step-actions">
            <button className="link" onClick={() => openFix(st, s)}><Icon.spark /> {s.status === "failed" ? "Fix with AI" : "Improve"}</button>
            {st.trial?.steps?.some((x) => x.id === s.id) && <span className="badge badge-info" title={st.trial.steps.find((x) => x.id === s.id)?.why}>changed in this trial</span>}</div>}
          {frs().fix?.runId === st.id && frs().fix?.stepId === s.id && <StepFixPanel st={st} s={s} />}
        </li>; })}</ol>
    </div>
  );
}

// --- fixing or improving one step with AI ----------------------------------------------

const canFix = (st, s) => !LIVE.includes(st.status) && s.type !== "start" && ["failed", "done"].includes(s.status);

function openFix(st, s) {
  const r = frs();
  r.fix = r.fix?.runId === st.id && r.fix?.stepId === s.id ? null : { runId: st.id, stepId: s.id, prompt: "", expected: "", model: r.fix?.model || "", busy: false, res: null, error: "" };
  update();
}

const nodeSettings = (n) => { const c = { ...n }; for (const k of ["id", "x", "y"]) delete c[k]; return JSON.stringify(c, null, 2); };

function StepFixPanel({ st, s }) {
  const r = frs(), fx = r.fix, failed = s.status === "failed";
  const set = (k, v) => { fx[k] = v; update(); };
  const suggest = async () => {
    Object.assign(fx, { busy: true, error: "", res: null }); update();
    try { fx.res = await backend.FixFlowStep({ runId: st.id, stepId: s.id, prompt: fx.prompt, expected: fx.expected, model: fx.model }); }
    catch (e) { fx.error = errMsg(e); }
    fx.busy = false; update();
  };
  const tryIt = async () => {
    fx.trying = true; update();
    try {
      const t = await backend.TryStepFix(fx.res);
      S.flowRuns[t.id] = t; S.loaded[t.id] = true; S.events[t.id] ||= [];
      r.runs = [{ id: t.id, workflow: t.flowId, state: t.status, started: t.started, kind: "flow-trial", usage: {} }, ...r.runs];
      r.fix = null; r.runId = t.id;
      toast("Trying the change: the steps before it reuse this run's results.");
    } catch (e) { fx.error = errMsg(e); }
    fx.trying = false; update();
  };
  const errs = (fx.res?.issues || []).filter((i) => i.level === "error");
  return <div className="fr-fix">
    <p className="muted fl-help">{failed ? "The AI changes only this step's settings (or its tool) so it runs and gives the steps after it what they need. " : "Say what should be better; the AI changes only this step. "}
      It sees the step, what reached it in this run{failed ? ", its error" : ", its result"} and how the steps after it use it. Nothing is saved until you try the change and apply it.</p>
    <textarea rows={2} value={fx.prompt} placeholder={failed ? "Optional: what you know about the problem, e.g. the API now returns a list under data." : "e.g. keep only the open items, sort by date, and give JSON with id and title."} onChange={(e) => set("prompt", e.target.value)}
      onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) suggest(); }} />
    <details className="fr-fix-expect" open={!!fx.expected}><summary className="muted">What should it give instead? (optional: an example of the right output)</summary>
      <textarea rows={4} value={fx.expected} style={{ fontFamily: "var(--font-mono)" }} placeholder={'e.g. | Ticker | Price |\n|---|---|\n| AMZN | 265.58 |'} onChange={(e) => set("expected", e.target.value)} /></details>
    {(() => { // what the AI is given, from this run
      const f = frs().flow, parents = (f?.edges || []).filter((e) => e.to === s.id).map((e) => st.steps.find((x) => x.id === e.from)).filter(Boolean);
      const size = (x) => (x.output ? `${x.output.length.toLocaleString()} characters${x.truncated ? "+" : ""}` : x.iterations ? "per loop item" : "no output");
      return <p className="muted fl-help">The AI is given: this step's definition; {parents.length ? parents.map((x) => `the output of ${x.title} (${size(x)})`).join(", ") : "nothing connected into it"}; {s.error ? "its error" : `its output (${size(s)})`}{s.args ? " and the arguments it ran with" : ""}; how the steps after it use it{fx.expected.trim() ? "; and your example of the right output" : ""}. Long outputs are given by their start and end, with their JSON shape.</p>;
    })()}
    <div className="ai-model"><span className="muted">Model</span><ModelPicker value={fx.model} onChange={(v) => set("model", v)} label="Model for the fix" />
      <button className="btn primary" disabled={fx.busy} onClick={suggest}>{fx.busy ? "Working on it…" : fx.res ? "Suggest again" : failed ? "Suggest a fix" : "Suggest an improvement"}</button>
      <button className="link" onClick={() => { r.fix = null; update(); }}>Cancel</button></div>
    {fx.busy && <AiProgress phases={["Reading the step and what happened", "Working out the change", "Checking it against the flow"]} />}
    {fx.error && <div className="banner error">{fx.error}</div>}
    {fx.res && <div className="fr-fix-res">
      <div><b>Suggested change</b>{fx.res.why && <span className="muted"> · {fx.res.why}</span>}</div>
      <div className="chips">{fx.res.changes.map((c) => <span key={c} className="chip">{c}</span>)}</div>
      {fx.res.issues.map((i, k) => <div key={k} className={"banner " + (i.level === "error" ? "error" : "")}>{i.message}</div>)}
      <div className="fr-fix-diff"><div><div className="muted">Now</div><pre className="code fr-pre">{nodeSettings(fx.res.before)}</pre></div>
        <div><div className="muted">Changed</div><pre className="code fr-pre">{nodeSettings(fx.res.node)}</pre></div></div>
      <div className="row-actions"><button className="btn primary" disabled={fx.trying || errs.length > 0} onClick={tryIt}>{fx.trying ? "Starting…" : "Try it"}</button>
        <span className="muted fl-help">Runs a copy of the flow with this change: the steps before it reuse this run's results; this step and the ones after it run again. The flow itself is not changed yet.</span></div>
    </div>}
  </div>;
}

// A trial run: what changed, and Apply / Discard.
function TrialBanner({ st }) {
  const r = frs(), t = st.trial, live = LIVE.includes(st.status);
  const title = (id) => st.steps.find((x) => x.id === id)?.title || id;
  const apply = async () => {
    try { await backend.ApplyStepFix(st.id); t.applied = true; toast(`Applied to the flow ${st.flowName}.`); update(); } catch (e) { toast(errMsg(e)); }
  };
  return <div className={"banner fr-trial" + (t.applied ? " applied" : "")}>
    <div><b>{t.applied ? "Applied to the flow." : "Trial of a change — not saved yet."}</b> Changed: {t.steps.map((x) => <span key={x.id}><b>{title(x.id)}</b>{x.why ? ` (${x.why})` : ""}</span>).reduce((a, b) => [a, "; ", b])}.
      {t.reused?.length > 0 && <> Reused from the earlier run (not run again): {t.reused.map(title).join(", ")}.</>}</div>
    {!live && !t.applied && <div className="row-actions">
      <button className="btn primary" onClick={apply}>{st.status === "done" ? "Apply to the flow" : "Apply anyway"}</button>
      <button className="btn secondary" onClick={() => openRun(t.baseRun)}>Discard · back to the earlier run</button>
      <span className="muted fl-help">{st.status === "done" ? "Check the steps and outputs below. Apply writes only the changed step into the flow." : "The trial did not finish. You can improve the step again from here."}</span></div>}
  </div>;
}

function Outputs({ st }) {
  if (!st.outputs?.length) return null;
  return (
    <div className="card"><div className="card-head"><Icon.files /><h3>Outputs</h3><span className="badge">{st.outputs.length}</span></div>
      {st.outputs.map((o, k) => (
        <details key={k} className="fr-out" open={o.show || k === st.outputs.length - 1}>
          <summary><b>{o.title}</b> <span className="badge">{o.format.toUpperCase()}</span>{o.item && <span className="muted"> · {o.item}</span>}
            {o.saved?.map((p) => <span key={p}> · <FileLink path={p}>{p}</FileLink></span>)}{o.copied && <span className="badge badge-ok">copied</span>}{o.tools?.map((t) => <span key={t} className="badge badge-info">sent to {t}</span>)}
            <CopyButton text={o.content} format={o.format === "txt" ? "" : o.format} /></summary>
          {o.errors?.map((e, j) => <div key={j} className="banner error">{e}</div>)}
          <ContentView title={o.title} text={o.content} format={o.format === "txt" ? undefined : o.format} files={(o.saved || []).map((p) => ({ label: p.split("/").pop(), path: p }))} />
        </details>))}
    </div>
  );
}

function RunView({ st }) {
  const r = frs(), evs = S.events[st.id] || [], live = LIVE.includes(st.status);
  return <>
    <header className="view-head"><h1>{st.flowName}</h1><Pill s={pill(st.status)} label={STATUS_LABEL[st.status] || st.status} />
      <div className="head-controls">
        {live && <button className="btn secondary danger-text" onClick={() => backend.StopFlowRun(st.id).catch((e) => toast(errMsg(e)))}><Icon.x /> Stop</button>}
        <button className="btn secondary" onClick={() => { S.pg ||= { prompt: "", tools: true, model: "", runs: [], sel: "", loaded: false }; S.pg.loaded = false; S.pg.sel = st.id; S.view = "playground"; update(); }}>Watch in the playground</button>
        {!live && <button className="btn secondary" title="Ask follow-up questions about this run's results" onClick={() => continueInPlayground([st.id])}><Icon.history /> Continue in the playground</button>}
        {!live && <button className="btn primary" onClick={() => { r.runId = ""; update(); }}>Run again</button>}
        {!live && <button className="btn secondary fr-switch" onClick={() => { r.runId = ""; update(); }}>All flows</button>}
      </div></header>
    {st.status === "scheduled" && <div className="banner">Starts at {new Date(st.options.startAt).toLocaleString()}. Keep the app open; you can stop it before then.</div>}
    {st.trial && <TrialBanner st={st} />}
    {st.error && st.status !== "done" && <div className={"banner " + (st.status === "stopped" ? "" : "error")}>{st.error}</div>}
    {!live && <ResultsLink id={st.id} ended />}
    {st.changes?.length > 0 && <div className="banner fr-saved"><Icon.check /> Saved to the flow: {st.changes.join("; ")}</div>}
    {st.status === "review" && st.review && <Review st={st} />}
    {st.status === "running" && <AiProgress evs={evs} />}
    <Steps st={st} />
    <Outputs st={st} />
    <details className="fr-activity"><summary className="muted">Activity and debug log · {evs.length} events</summary>
      <RunPanel st={{ id: "flow", cmd: st.flowName, status: live ? "running" : st.status, lastRun: st.id }} evs={evs} running={live} /></details>
  </>;
}

// --- sidebar ------------------------------------------------------------------------

function Sidebar({ st }) {
  const evs = S.events[st.id] || [], u = st.usage || {}, ev = usageOf(evs);
  const work = st.steps.filter((s) => s.type !== "start");
  const done = work.filter((s) => ["done", "skipped", "failed"].includes(s.status)).length;
  const totalUnits = Math.max(1, (st.passes || 1) * (st.items || 1)), unit = ((st.pass || 1) - 1) * (st.items || 1) + (st.item || 1) - 1;
  const pct = Math.round(((unit + done / Math.max(1, work.length)) / totalUnits) * 100);
  const [, tick] = useState(0);
  useEffect(() => { if (!LIVE.includes(st.status)) return; const t = setInterval(() => tick((n) => n + 1), 1000); return () => clearInterval(t); }, [st.status]);
  const elapsed = (st.ended ? Date.parse(st.ended) : Date.now()) - Date.parse(st.started);
  const tokens = Math.max(u.input_tokens + u.output_tokens || 0, ev.in + ev.out);
  const M = ({ v, l }) => <div className="fr-m"><b>{v}</b><span>{l}</span></div>;
  return (
    <aside className="fr-side" aria-label="Progress and metrics">
      <div className="fr-ring" style={{ "--p": pct }}><span><b>{["done"].includes(st.status) ? 100 : pct}%</b><small>{STATUS_LABEL[st.status]}</small></span></div>
      {(st.items > 1 || st.passes > 1) && <p className="muted fr-center">{st.passes > 1 && `Run ${st.pass} of ${st.passes}`}{st.passes > 1 && st.items > 1 && " · "}{st.items > 1 && `item ${st.item} of ${st.items}`}</p>}
      <ul className="fr-mini">{work.map((s) => <li key={s.id} className={"s-" + s.status}><span className="fr-dot" />{s.title}<span className="muted">{s.durationMs ? fmtMs(s.durationMs) : ""}</span></li>)}</ul>
      <h4>Metrics</h4>
      <div className="fr-metrics">
        <M v={fmtMs(Math.max(0, elapsed))} l="Elapsed" />
        <M v={fmtK(tokens)} l="Tokens" />
        <M v={isCopilot() ? (u.premium_requests || ev.premium || 0) : "$" + (u.cost_usd || ev.usd || 0).toFixed(3)} l={isCopilot() ? "Premium requests" : "Cost"} />
        <M v={u.model_calls || ev.calls || 0} l="AI calls" />
        <M v={u.tool_calls || ev.tools || 0} l="Tool runs" />
        <M v={st.outputs?.length || 0} l="Outputs" />
      </div>
      <p className="muted fr-saves">Saves about <b>{st.minutesSaved * Math.max(1, (st.passes || 1) * (st.items || 1))} min</b> of manual work when it completes.</p>
    </aside>
  );
}

// --- screen ---------------------------------------------------------------------------

export function FlowRun() {
  const r = frs();
  useEffect(() => { if (S.ws) { loadAll(); refreshExtensions(); } }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="the flow runner" />;
  const st = r.runId ? S.flowRuns[r.runId] : null;
  const runsOf = (id) => r.runs.filter((x) => x.workflow === id).slice(0, 6);
  return (
    <div className={"files frun" + (st ? " with-side" : "")}>
      <nav className="ftree" aria-label="Flows and runs">
        <h1 style={{ padding: "0 8px 8px" }}>Run flows</h1>
        {(r.flows || []).map((f) => <div key={f.id} className="fr-flow">
          <button className={"run-item" + (r.flowId === f.id && !r.runId ? " active" : "")} onClick={() => { r.runId = ""; loadFlow(f.id); }}>
            <span className="run-item-top"><b>{f.name}</b></span><small>{f.steps} steps · {f.outputs} outputs</small></button>
          {runsOf(f.id).map((x) => { const live = S.flowRuns[x.id]?.status || x.state; return <button key={x.id} className={"run-item fr-run-item" + (r.runId === x.id ? " active" : "")} onClick={() => openRun(x.id)}>
            <span className="run-item-top"><span>{new Date(x.started).toLocaleString()}{(x.kind === "flow-trial" || S.flowRuns[x.id]?.trial) && <span className="badge badge-info" style={{ marginLeft: 6 }}>trial</span>}</span><Pill s={pill(live)} label={STATUS_LABEL[live] || live} /></span></button>; })}
        </div>)}
        {r.flows && !r.flows.length && <p className="muted" style={{ padding: "4px 8px" }}>No flows yet. Design one under Tools › Flow designer.</p>}
      </nav>
      <div className="fview">{st ? <RunView st={st} /> : r.flow ? <Setup /> : <p className="muted">Choose a flow.</p>}</div>
      {st && <Sidebar st={st} />}
    </div>
  );
}
