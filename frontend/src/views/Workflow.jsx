import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { ModelPicker } from "../ModelPicker.jsx";
import { ProjectPicker } from "../ProjectPicker.jsx";
import { confirmDialog, askText } from "../Dialog.jsx";
import { Linkify } from "../FileLinks.jsx";
import { AiProgress } from "../AiProgress.jsx";
import { EventIcon } from "../EventIcon.jsx";
import { ResultsLink } from "../RunResults.jsx";
import { S, update, toast, errMsg, saveWorkflow, currentStep, usageOf, stepUsage, totals, isCopilot, fmtK, fmtMs, fmtCredits, loadStepEvents, saveText, downloadProjectZip } from "../store.js";
import { Icon, Pill, Chip, Stat } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { agentLabel } from "./Setup.jsx";

export const STATE_LABEL = { locked: "Locked", collecting: "Collecting inputs", ready: "Ready to run", running: "Running…", paused: "Awaiting approval", done: "Approved", failed: "Rejected", cancelled: "Cancelled", interrupted: "Interrupted" };
const STATE_PILL = { locked: "pending", collecting: "pending", ready: "running", running: "running", paused: "paused", done: "done", failed: "failed", cancelled: "cancelled", interrupted: "interrupted" };
const PHASES = [["Govern", 0, 0], ["Define", 1, 2], ["Design", 3, 4], ["Build", 5, 5]];
const EV_TONE = { flow: "--activity-accent", secret: "--warn", run: "--muted", prompt: "--syn-type", model: "--info", tool: "--syn-function", fs: "--ok", gate: "--warn", loop: "--error", budget: "--error", error: "--error", ext: "--syn-property" };
const DBG_FILTERS = [["all", "All"], ["model", "Model"], ["tool", "Tools"], ["fs", "Files"], ["gate", "Gates"], ["flow", "Flow"], ["run", "Run"], ["ext", "Extensions"]];

export function stepState(wf, k) {
  const raw = wf.steps[k].status;
  if (raw !== "pending") return raw;
  if (k > 0 && wf.steps[k - 1].status !== "done") return "locked";
  return wf.steps[k].input.trim() ? "ready" : "collecting";
}
const statePill = (st) => <span className={`pill pill-${STATE_PILL[st]}`}>{STATE_LABEL[st]}</span>;

function Lifecycle({ wf }) {
  const n = wf.steps.length, W = 132, G = 52, X0 = 14, Y = 72, H = 62, cur = currentStep(wf), allDone = wf.steps.every((s) => s.status === "done");
  const x = (k) => X0 + k * (W + G), total = x(n - 1) + W + X0;
  const tone = { done: "--ok", running: "--info", paused: "--warn", failed: "--error", cancelled: "--muted", interrupted: "--error", ready: "--focus", collecting: "--muted", locked: "--border" };
  const by = Y + H + 52, sx = x(Math.min(1, n - 1)) + 16, ex = x(n - 1) + W - 16;
  const marker = (id, v) => <marker id={"lc-arrow-" + id} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10z" style={{ fill: `var(${v})` }} /></marker>;
  const phases = n === 6 ? PHASES : [];
  return (
    <svg className="lc-svg" viewBox={`0 0 ${total} ${by + 26}`} style={{ minWidth: Math.round(total * 0.78) }} role="group" aria-label="Spec workflow lifecycle">
      <defs>{marker("ok", "--ok")}{marker("dim", "--faint")}{marker("warn", "--warn")}</defs>
      {phases.map(([name, a, b]) => { const x1 = x(a) - 6, x2 = x(b) + W + 6; return <g key={name}><path d={`M${x1} 26 V20 H${x2} V26`} style={{ fill: "none", stroke: "var(--border)" }} /><text x={(x1 + x2) / 2} y="14" textAnchor="middle" className="lc-phase">{name.toUpperCase()}</text></g>; })}
      {wf.steps.slice(0, -1).map((s, k) => {
        const ax = x(k) + W, bx = x(k + 1), my = Y + H / 2, cx = (ax + bx) / 2, col = s.status === "done" ? "--ok" : s.status === "paused" ? "--warn" : "--faint";
        return <g key={"c" + k}><path d={`M${ax} ${my} H${bx - 2}`} style={{ fill: "none", stroke: `var(${col})`, strokeWidth: 1.5 }} markerEnd={`url(#lc-arrow-${s.status === "done" ? "ok" : "dim"})`} />
          {s.gate && <g><title>Human approval gate</title><rect x={cx - 7} y={my - 7} width="14" height="14" transform={`rotate(45 ${cx} ${my})`} style={{ fill: "var(--bg)", stroke: `var(${col})`, strokeWidth: 1.5 }} />{s.status === "done" && <path d={`M${cx - 3.5} ${my} l2.5 2.5 l4.5 -5`} style={{ fill: "none", stroke: "var(--ok)", strokeWidth: 1.6 }} />}</g>}</g>;
      })}
      {wf.steps.map((s, k) => {
        const st = stepState(wf, k), col = tone[st], sel = S.sel === k, here = k === cur && !allDone, dim = st === "locked" || st === "collecting";
        const pick = () => { S.sel = k; update(); loadStepEvents(k); };
        return <g key={s.id}>
          <g className="lc-node" role="button" tabIndex={0} aria-label={`${s.title}: ${STATE_LABEL[st]}${here ? ", current step" : ""}`} onClick={pick} onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), pick())}>
            <rect x={x(k)} y={Y} width={W} height={H} rx="8" style={{ fill: `var(${sel ? "--select-bg" : "--surface"})`, stroke: `var(${here ? "--focus" : col})`, strokeWidth: here ? 2.5 : 1.3, strokeDasharray: st === "locked" ? "4 3" : undefined }} />
            <circle cx={x(k) + 18} cy={Y + 21} r="9" style={{ fill: `var(${dim ? "--code-bg" : col})` }} />
            <text x={x(k) + 18} y={Y + 25} textAnchor="middle" className="lc-num" style={{ fill: `var(${dim ? "--muted" : "--bg"})` }}>{st === "done" ? "✓" : k + 1}</text>
            <text x={x(k) + 34} y={Y + 25} className="lc-title">{s.title}</text>
            <text x={x(k) + 12} y={Y + 47} className="lc-state" style={{ fill: `var(${st === "locked" ? "--faint" : col === "--border" ? "--muted" : col})` }}>{STATE_LABEL[st]}</text>
          </g>
          {here && <g aria-hidden="true"><path d={`M${x(k) + W / 2 - 6} ${Y - 9} h12 l-6 7z`} style={{ fill: "var(--focus)" }} /><rect x={x(k) + W / 2 - 40} y={Y - 27} width="80" height="17" rx="8.5" style={{ fill: "var(--focus)" }} /><text x={x(k) + W / 2} y={Y - 15} textAnchor="middle" className="lc-here">YOU ARE HERE</text></g>}
        </g>;
      })}
      {!allDone && (() => { const a = x(cur) + W * 0.72, b = x(cur) + W * 0.28; return <><path d={`M${a} ${Y + H} C ${a} ${Y + H + 24}, ${b} ${Y + H + 24}, ${b} ${Y + H + 3}`} style={{ fill: "none", stroke: "var(--warn)", strokeWidth: 1.3, strokeDasharray: "3 3" }} markerEnd="url(#lc-arrow-warn)" /><text x={x(cur) + W / 2} y={Y + H + 34} textAnchor="middle" className="lc-note" style={{ fill: "var(--warn)" }}>request changes ↺ rerun</text></>; })()}
      <path d={`M${ex} ${Y + H} V${by} H${sx} V${Y + H + 4}`} style={{ fill: "none", stroke: `var(${allDone ? "--ok" : "--faint"})`, strokeWidth: 1.3, strokeDasharray: "5 4" }} markerEnd={`url(#lc-arrow-${allDone ? "ok" : "dim"})`} />
      <text x={(sx + ex) / 2} y={by + 16} textAnchor="middle" className="lc-note">next feature or iteration: back to Specify (constitution stays)</text>
    </svg>
  );
}

const OPTS = [
  ["rules", "Apply functionality rules", () => `${S.ws.profile.rules.length} rules from .codegen/rules`],
  ["global", "Use corporate skills", () => S.ws.libraries.filter((l) => S.ws.profile.global.includes(l.id)).map((l) => l.name).join(", ") || "none selected"],
  ["project", "Use project skills", () => S.ws.libraries.filter((l) => S.ws.profile.project.includes(l.id)).map((l) => l.name).join(", ") || "none selected"],
  ["diagrams", "Attach input diagrams", () => Object.keys(S.ws.profile.diagrams || {}).join(" + ") || "none attached"],
  ["tools", "Allow tools and extensions", () => (S.ws.extensions || []).filter((t) => t.approved && t.status !== "in review" && t.status !== "needs approval").map((t) => t.name).join(", ") || "none approved"],
];

async function decide(st, kind) {
  try {
    await backend.Decide(st.lastRun, kind, kind === "changes" ? st.feedback || "" : "");
    if (kind === "changes") { st.feedback = ""; saveWorkflow(); }
    if (kind === "approve" && S.sel < S.wf.steps.length - 1) S.sel++;
    update();
  } catch (e) { toast(errMsg(e), "error"); }
}

async function runStep(st) {
  try { await backend.SaveWorkflow(S.wf); const id = await backend.RunStep(st.id); st.lastRun = id; st.status = "running"; S.loaded[id] = true; S.events[id] ||= []; update(); }
  catch (e) { toast(errMsg(e), "error"); }
}

function goSetUpSpecKit() {
  S.view = "inputs"; update();
  setTimeout(() => document.querySelector(".sk-card")?.scrollIntoView({ block: "start" }), 50);
}

export function Workflow() {
  if (!S.ws || !S.wf) return <NeedWorkspace what="the workflow" />;
  const wf = S.wf, i = Math.min(S.sel, wf.steps.length - 1), st = wf.steps[i], state = stepState(wf, i), cur = currentStep(wf);
  const allDone = wf.steps.every((s) => s.status === "done");
  const lcHidden = !!S.panes["wf:lc:c"];
  const editable = !["running", "paused"].includes(state);
  const evs = st.lastRun ? S.events[st.lastRun] || [] : [];
  const gate = state === "paused" ? [...evs].reverse().find((e) => e.type === "gate.wait")?.gate || "approval" : null;
  const prov = S.setup.provider;
  const checks = [
    [i === 0 || wf.steps[i - 1].status === "done", i === 0 ? "No earlier step" : `${wf.steps[i - 1].title} approved`],
    [!!st.input.trim(), "Inputs collected"],
    [prov && prov !== "skipped", `Agent: ${agentLabel()}${S.setup.source ? " · " + S.setup.source : ""}`],
  ];
  // the backend refuses /speckit.* steps until Spec Kit is set up for the project (hub.go RunStep)
  const needsSpecKit = st.cmd.startsWith("/speckit") && !S.ws.speckit?.initialized;
  if (st.cmd.startsWith("/speckit")) checks.push([!needsSpecKit, "Spec Kit set up for this project"]);
  const canRun = checks.every((c) => c[0]) && editable;
  const t = totals();
  const opts = st.options;
  const edit = (k, v) => { st[k] = v; update(); saveWorkflow(); };
  return (
    <div className="wf">
      <Pane k="wf" v="--pane-l" pane=".wf-steps" min={200} max={420} label="Resize the step list" />
      <nav className="wf-steps" aria-label="Spec workflow steps"><h1>{wf.name}</h1>
        {wf.steps.map((s, k) => { const ks = stepState(wf, k); return (
          <button key={s.id} className={"wf-step" + (k === i ? " active" : "")} aria-current={k === cur ? "step" : undefined} onClick={() => { S.sel = k; update(); loadStepEvents(k); }}>
            <span className={"card-index" + (ks === "done" ? " done" : "")}>{ks === "done" ? <Icon.check /> : k + 1}</span>
            <span className="grow">{s.title}{k === cur && !allDone && <> <span className="badge">current</span></>}<code>{s.cmd}</code></span></button>); })}
        <p className="wf-gate-note">After every gated step the run stops for your approval. It moves to the next step only when you approve.</p>
        <RunSettings wf={wf} />
      </nav>
      <div className="wf-pane">
        <div className={"card lc-card" + (lcHidden ? " is-collapsed" : "")}><div className="card-head"><h3><button className="lc-toggle" aria-expanded={!lcHidden} aria-controls="lc-body" title={lcHidden ? "Show the lifecycle diagram" : "Hide the lifecycle diagram (the step list on the left has the same steps)"}
            onClick={() => { if (lcHidden) delete S.panes["wf:lc:c"]; else S.panes["wf:lc:c"] = 1; update(); }}><span className="lc-caret" aria-hidden="true">{lcHidden ? "▸" : "▾"}</span> Lifecycle</button></h3>
          {t.in > 0 && <><span className="badge">{fmtK(t.in + t.out)} tokens</span><span className="badge">{isCopilot() ? t.premium + " premium req" : "$" + t.usd.toFixed(3)}</span><span className="badge">{t.tools} tool calls</span><span className="badge">{t.files} files</span></>}
          <span className="muted" style={{ marginLeft: "auto" }}>Step {cur + 1} of {wf.steps.length} · {wf.steps[cur].title} · {STATE_LABEL[stepState(wf, cur)]}</span></div>
          {!lcHidden && <div id="lc-body"><div className="lc-scroll"><Lifecycle wf={wf} /></div>
          <div className="lc-legend"><span><i style={{ borderColor: "var(--focus)" }} />current</span><span><i style={{ borderColor: "var(--ok)" }} />approved</span><span><i style={{ borderColor: "var(--warn)" }} />awaiting approval</span><span><i className="dash" />locked</span><span><b className="gate" />approval gate</span>
            {allDone && <button className="link" style={{ marginLeft: "auto" }} onClick={async () => { if (await confirmDialog("Every step goes back to pending; inputs are kept.", { title: "Start the next iteration?", ok: "Start" })) { await backend.ResetWorkflow().catch((e) => toast(errMsg(e), "error")); } }}>Start next iteration</button>}</div></div>}
          {lcHidden && allDone && <div className="row-actions"><button className="btn secondary" onClick={async () => { if (await confirmDialog("Every step goes back to pending; inputs are kept.", { title: "Start the next iteration?", ok: "Start" })) { await backend.ResetWorkflow().catch((e) => toast(errMsg(e), "error")); } }}>Start next iteration</button></div>}
        </div>

        <header className="view-head"><h1>{i + 1} · {st.title} <span className="muted" style={{ fontWeight: 400, fontSize: ".7em" }}><code>{st.cmd}</code></span></h1>{statePill(state)}
          {(S.ws.projects || []).length > 0 && <span className="wf-project" title="Each project has its own workflow progress"><ProjectPicker value={S.ws.speckit?.project || ""} disabled={state === "running" || state === "paused"}
            onNew={() => { S.sk = { ...(S.sk || {}), newMode: true, name: "", result: null }; S.view = "inputs"; update(); setTimeout(() => document.querySelector(".sk-card")?.scrollIntoView({ block: "start" }), 50); }} /></span>}
          <span className="wf-model"><ModelPicker compact label="Model for workflow steps" value={S.wf.model || ""} disabled={state === "running"}
            onChange={(v) => { S.wf.model = v; update(); saveWorkflow(); }} /></span></header>
        {wf.steps.some((x) => x.id === "implement" && x.status === "done") && <div className="banner ok zip-banner"><Icon.check /> <span><b>Implementation is done.</b> Download the whole project{S.ws.speckit?.project ? <> (<code className="inline-code">{S.ws.speckit.project}/</code> with the skills, rules and diagrams it uses)</> : null} as one zip.</span>
          <button className="btn primary" disabled={S.zipBusy} onClick={() => downloadProjectZip(true)}>{S.zipBusy ? "Zipping…" : "Download project (.zip)"}</button></div>}
        {i !== cur && !allDone && state !== "locked" && <div className="banner">You are viewing <b>{st.title}</b>. The workflow is currently at <b>{wf.steps[cur].title}</b>. <button className="link" onClick={() => { S.sel = cur; update(); loadStepEvents(cur); }}>Go to current step</button></div>}
        {state === "paused" && (
          <div className={"gate-banner" + (gate === "loop" ? " loop" : "")}><div className="gate-text"><Icon.warn /><div>
            {gate === "loop" ? <><b>The agent seems stuck in a loop</b><p>The run was stopped. Accept what it produced, send feedback and rerun, or reject the step.</p></>
              : <><b>Human approval needed</b><p>Review the {st.title.toLowerCase()} output below. Approving {i < wf.steps.length - 1 ? <>unlocks <b>{wf.steps[i + 1].title}</b></> : "finishes the workflow"}.</p></>}</div></div>
            {!S.wfChanges ? <div className="gate-actions"><button className="btn primary" onClick={() => decide(st, "approve")}>{gate === "loop" ? "Accept output" : "Approve and continue"}</button>
              <button className="btn secondary" aria-expanded="false" onClick={() => { S.wfChanges = true; update(); setTimeout(() => document.getElementById("wf-feedback")?.focus(), 30); }}>{gate === "loop" ? "Send feedback and rerun…" : "Request changes…"}</button>
              <button className="btn secondary danger-quiet" onClick={async () => { if (await confirmDialog(`The ${st.title} step is marked as rejected; you can run it again later.`, { title: "Reject this output?", ok: "Reject", danger: true })) decide(st, "reject"); }}>Reject</button></div>
            : <div className="gate-changes"><label className="field"><span className="muted">What should change? The agent reruns the step with this feedback.</span>
                <textarea id="wf-feedback" rows={3} value={st.feedback || ""} placeholder="e.g. Add a principle about accessibility; keep it under one page."
                  onChange={(e) => { st.feedback = e.target.value; update(); saveWorkflow(); }} onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && st.feedback?.trim()) { e.preventDefault(); S.wfChanges = false; decide(st, "changes"); } }} /></label>
              <div className="gate-actions"><button className="btn primary" disabled={!st.feedback?.trim()} title="⌘Enter / Ctrl+Enter" onClick={() => { S.wfChanges = false; decide(st, "changes"); }}>Send and rerun</button>
                <button className="btn secondary" onClick={() => { S.wfChanges = false; update(); }}>Cancel</button></div></div>}</div>
        )}
        {state === "locked" && <div className="banner wf-locked"><Icon.lock /><span><b>{st.title}</b> is locked until <b>{wf.steps[i - 1].title}</b> is approved. You can prepare its inputs and instructions now.</span>
          {i !== cur && !allDone && <button className="btn secondary" onClick={() => { S.sel = cur; update(); loadStepEvents(cur); }}>Go to {wf.steps[cur].title}</button>}</div>}
        {state === "interrupted" && <div className="banner error">The app closed while this step was running. Run it again to continue.</div>}

        <div className="two-col">
          <div className="card"><div className="card-head"><span className="card-index">1</span><h3>Inputs</h3>{st.input.trim() ? <span className="badge badge-ok">collected</span> : <span className="badge">needed</span>}</div>
            <p className="wf-desc">{st.ask}. Passed to <code className="inline-code">{st.cmd}</code> as its argument.</p>
            <textarea rows={5} placeholder={st.placeholder} disabled={!editable} value={st.input} onChange={(e) => edit("input", e.target.value)} />
          </div>
          <div className="card"><div className="card-head"><span className="card-index">2</span><h3>Instructions for the agent</h3>{st.template && <button className="link" onClick={() => edit("instructions", st.template)}>Use template</button>}</div>
            <p className="wf-desc">How the agent should work on this step. Sent with the command; not part of the spec.</p>
            <textarea rows={5} placeholder={st.template} disabled={!editable} value={st.instructions} onChange={(e) => edit("instructions", e.target.value)} />
            <ul className="lib-list">{OPTS.map(([k, l, d]) => <li key={k}><input type="checkbox" id={"opt-" + k} checked={!!opts[k]} disabled={!editable} onChange={(e) => { opts[k] = e.target.checked; update(); saveWorkflow(); }} /><label className="grow" htmlFor={"opt-" + k}>{l}<small>{d()}</small></label></li>)}</ul>
          </div>
        </div>

        <div className={"card ready-card" + (canRun ? " is-ready" : "")}><div className="card-head"><span className="card-index">3</span><h3>{canRun ? "Ready to run" : state === "running" ? "Running" : state === "paused" ? "Ran — awaiting your approval" : "Not ready yet"}</h3></div>
          <ul className="checklist">{checks.map(([ok, l]) => <li key={l} className={ok ? "ok" : ""}>{ok ? <Icon.check /> : <Icon.x />}<span>{l}</span>
            {!ok && l.startsWith("Spec Kit") && <button className="link" onClick={goSetUpSpecKit}>Set up Spec Kit</button>}</li>)}<li className="ok"><Icon.check /><span>{st.gate ? "Pauses for approval when done" : "No approval gate (runs straight through)"}</span></li></ul>
          <pre className="code" aria-label="Command preview">{st.cmd} {st.input.trim().split("\n")[0].slice(0, 90) || "…"}
            {st.instructions.trim() && <>{"\n"}<span className="syn-comment"># instructions: {st.instructions.trim().split("\n")[0].slice(0, 80)}</span></>}
            {"\n"}<span className="syn-comment"># context: {OPTS.filter(([k]) => opts[k]).map(([, l]) => l.toLowerCase()).join(", ") || "none"}</span></pre>
          <div className="row-actions">
            <button className="btn primary" disabled={!canRun} aria-describedby={!canRun && editable ? "wf-why" : undefined} onClick={() => runStep(st)}>{["done", "failed", "cancelled", "interrupted"].includes(state) ? "Run again" : "Run " + st.cmd}</button>
            {!canRun && editable && <span id="wf-why" className="muted run-why">Needed first: {checks.find((c) => !c[0])?.[1]}</span>}
            {state === "running" && <button className="btn secondary" onClick={() => backend.CancelRun(st.lastRun).catch((e) => toast(errMsg(e), "error"))}>Cancel run</button>}
            <button className="link" onClick={async () => { try { const p = await backend.Compose(st.id, ""); S._prompt = S._prompt === p ? null : p; update(); } catch (e) { toast(errMsg(e), "error"); } }}>{S._prompt ? "Hide full prompt" : "Show full prompt"}</button>
            <span className="muted">Agent: {agentLabel()} · reads {S.ws.root}</span></div>
          {S._prompt && <pre className="code console-out"><Linkify text={S._prompt} /></pre>}
        </div>
        <RunPanel st={st} evs={evs} running={state === "running"} />
      </div>
    </div>
  );
}

export function RunSettings({ wf, open }) {
  const b = wf.budget, st = wf.steps[Math.min(S.sel, wf.steps.length - 1)];
  const num = (v) => Math.max(0, Number(v) || 0);
  return (
    <details style={{ margin: open ? 0 : "10px 8px 0" }} open={open || undefined}><summary className="muted" style={{ cursor: "pointer" }}>Run settings</summary>
      <div className="budget" style={{ gridTemplateColumns: "1fr" }}>
        <label className="field inline"><span>Token budget per run</span><input type="number" min="0" step="10000" value={b.maxTokens} onChange={(e) => { b.maxTokens = num(e.target.value); update(); saveWorkflow(); }} /></label>
        <label className="field inline"><span>Cost budget per run (USD)</span><input type="number" min="0" step="0.5" value={b.maxUsd} onChange={(e) => { b.maxUsd = num(e.target.value); update(); saveWorkflow(); }} /></label>
        <label className="field inline"><span>Premium requests per run</span><input type="number" min="0" value={b.maxPremiumRequests} onChange={(e) => { b.maxPremiumRequests = num(e.target.value); update(); saveWorkflow(); }} /></label>
        <label className="field inline"><span>Retries for {st.title}</span><input type="number" min="0" max="5" value={st.retries} onChange={(e) => { st.retries = Math.min(5, num(e.target.value)); update(); saveWorkflow(); }} /></label>
        <label className="check"><input type="checkbox" checked={st.gate} onChange={(e) => { st.gate = e.target.checked; update(); saveWorkflow(); }} /> Approval gate after {st.title}</label>
        <p className="muted" style={{ margin: 0 }}>0 means no limit. A run that exceeds a budget is stopped and marked rejected.</p>
      </div>
    </details>
  );
}

const sec = (ms) => (ms == null ? "" : ms < 1000 ? "+" + ms + "ms" : "+" + (ms / 1000).toFixed(ms < 10000 ? 1 : 0) + "s");
const shortTool = (t) => String(t || "").replace(/^mcp__agentbridge__|^agentbridge-/, "");

// One line of the Activity tab: an icon, what happened, and when.
function Act({ e, kind = "", children, extra }) {
  return <div className={"act " + kind}><EventIcon e={e} /><div className="act-body">{children}</div>{extra}<span className="act-time" title={e.ts ? new Date(e.ts).toLocaleTimeString() : ""}>{sec(e.dt)}</span></div>;
}

function ActivityRow({ e, evs }) {
  switch (e.type) {
    case "tool.call": {
      const res = evs.find((x) => x.type === "tool.result" && x.seq > e.seq && (e.id ? x.id === e.id : x.tool === e.tool));
      const t = shortTool(e.tool), ext = /extension_managed$/.test(t);
      const label = ext ? "project tool " + (e.args?.name || "") : t;
      const arg = (e.summary || "").replace(e.tool || "", "").replace(/^extension_managed\s*/, "").trim();
      return <Act e={e} kind={"act-tool" + (res && res.ok === false ? " is-error" : "")} extra={<span className={"act-state " + (!res ? "run" : res.ok === false ? "bad" : "good")}>{!res ? <span className="spinner" /> : res.ok === false ? "failed" : "ok"}{res?.duration_ms != null ? " · " + fmtMs(res.duration_ms) : ""}</span>}>
        <b className="act-tool-name">{label}</b> <span className="act-arg"><Linkify text={arg} /></span></Act>;
    }
    case "fs.write": case "fs.edit": case "fs.delete":
      return <Act e={e} kind="act-fs"><Linkify text={e.summary} /> {e.added != null && <span className="diffstat"><b className="add">+{e.added}</b>{e.removed ? <> <b className="del">−{e.removed}</b></> : null}</span>}</Act>;
    case "model.text": return <Act e={e} kind="act-say"><div className="act-text"><Linkify text={e.text} /></div></Act>;
    case "model.request": return <Act e={e} kind="act-quiet">Asked {e.model || "the model"}{e.input_tokens ? <span className="muted"> · {fmtK(e.input_tokens)} tokens in context</span> : null}</Act>;
    case "gate.wait": return <Act e={e} kind={"act-gate" + (e.gate === "loop" ? " bad" : "")}>{e.summary}</Act>;
    case "gate.approved": return <Act e={e} kind="act-good">{e.summary || "Approved"}</Act>;
    case "gate.changes": return <Act e={e} kind="act-gate">{e.summary || `Changes requested${e.feedback ? ": " + e.feedback : ""}. Rerunning…`}</Act>;
    case "gate.rejected": return <Act e={e} kind="act-bad">{e.summary || "Rejected. The workflow stops here."}</Act>;
    case "loop.detected": case "budget.exceeded": case "error": return <Act e={e} kind="act-bad"><Linkify text={e.summary} /></Act>;
    case "run.retry": return <Act e={e} kind="act-gate">{e.summary}</Act>;
    case "ext.result": return <Act e={e} kind="act-fs">Extension {e.name}: exit {e.exit_code} · {fmtMs(e.duration_ms || 0)}</Act>;
    case "secret.request": return <Act e={e} kind="act-gate">Waiting for credentials: {e.summary}</Act>;
    case "secret.resolved": return <Act e={e} kind="act-quiet">Credentials {e.summary}</Act>;
    case "flow.loop": return <Act e={e} kind="act-quiet act-loop">↻ {e.summary}</Act>;
    case "flow.step": return e.status === "running" ? null : <Act e={e} kind={e.status === "failed" ? "act-bad" : "act-step"}><b>{e.summary}</b>{e.duration_ms != null && <span className="muted"> · {fmtMs(e.duration_ms)}</span>}</Act>;
    case "flow.review": return <Act e={e} kind="act-gate"><b>{e.summary}</b></Act>;
    case "flow.output": return <Act e={e} kind="act-step">Output: {e.summary}{(e.saved || []).map((p) => <span key={p}> → <Linkify text={p} /></span>)}</Act>;
    case "flow.saved": return <Act e={e} kind="act-good">{e.summary}</Act>;
    case "flow.pass": return <div className="row marker run-marker"><span className="marker-line" /><span className="marker-text">{e.summary}</span><span className="marker-line" /></div>;
    case "flow.scheduled": return <Act e={e} kind="act-quiet">{e.summary}</Act>;
    case "run.start": return <div className="row marker run-marker"><span className="marker-line" /><span className="marker-text">{e.summary}{e.model ? " · " + e.model : ""}</span><span className="marker-line" /></div>;
    case "run.usage": return <Act e={e} kind="act-quiet act-usage">{e.summary}</Act>;
    // the AI a tool used itself (its own claude / copilot calls)
    case "model.usage": return e.source === "tool" ? <Act e={e} kind="act-usage act-tool-ai"><Linkify text={e.summary} />{e.model ? <span className="muted"> · {e.model}</span> : null}</Act> : null;
    case "run.end": return e.state === "cancelled" || e.state === "stopped" ? <Act e={e} kind="act-bad">{e.state === "stopped" ? "Stopped" : "Cancelled"}</Act> : e.state === "failed" ? <Act e={e} kind="act-bad">Failed{e.reason ? ": " + e.reason : ""}</Act> : <Act e={e} kind="act-good">Finished</Act>;
  }
  return null;
}

export function RunPanel({ st, evs, running }) {
  const u = usageOf(evs), tab = S.run.tab;
  if (!evs.length) return <div className="transcript" style={{ minHeight: 160 }}><div className="transcript-inner"><div className="empty"><h3>Not run yet</h3><p>Collect inputs, add instructions, then run <code className="inline-code">{st.cmd}</code>. Activity, tokens, credits and files appear here live.</p></div></div></div>;
  const filter = S.run.dbgFilter, shown = evs.filter((e) => filter === "all" || e.type.startsWith(filter + "."));
  const lastReq = [...evs].reverse().find((e) => e.type === "model.request");
  const ctxPct = Math.min(100, Math.round((lastReq?.input_tokens || 0) / 2000));
  const jsonl = () => evs.map((e) => JSON.stringify(e)).join("\n");
  const setTab = (v) => { S.run.tab = v; update(); };
  let body;
  if (tab === "activity") body = <div className="transcript" style={{ minHeight: 200 }}><div className="transcript-inner">{evs.map((e) => <ActivityRow key={e.seq} e={e} evs={evs} />)}{running && <AiProgress evs={evs} compact />}</div></div>;
  else if (tab === "files") body = u.files.length ? <table className="table"><thead><tr><th>File</th><th>Change</th><th>Lines</th><th /></tr></thead><tbody>{u.files.map((f) => (
    <tr key={f.path}><td><code>{f.path}</code></td><td><span className={"badge " + (f.op === "created" ? "badge-ok" : f.op === "deleted" ? "badge-warn" : "badge-info")}>{f.op}</span></td><td className="diffstat"><b className="add">+{f.added}</b>{f.removed ? <> <b className="del">−{f.removed}</b></> : null}</td>
      <td>{f.op !== "deleted" && <button className="link" onClick={() => { S.files.sel = f.path; S.view = "files"; update(); }}>Open</button>}</td></tr>))}</tbody></table> : <p className="muted">No files changed yet.</p>;
  else body = <>
    <div className="dbg-bar"><div className="chips">{DBG_FILTERS.map(([v, l]) => <Chip key={v} on={filter === v} onClick={() => { S.run.dbgFilter = v; update(); }}>{l} <span className="muted">{v === "all" ? evs.length : evs.filter((e) => e.type.startsWith(v + ".")).length}</span></Chip>)}</div>
      <span className="status-spacer" /><button className="btn secondary" onClick={() => { S.run.dbgAll = !S.run.dbgAll; update(); }}>{S.run.dbgAll ? "Collapse all" : "Expand all"}</button>
      <button className="btn secondary" onClick={() => navigator.clipboard?.writeText(jsonl()).then(() => toast(`Copied ${evs.length} events as JSONL.`), () => toast("Copy failed; use Download."))}>Copy JSONL</button>
      <button className="btn secondary" onClick={() => saveText(`run-${st.id}-${st.lastRun}.jsonl`, jsonl() + "\n", "application/x-ndjson")}>Download</button></div>
    <div className="dbg" role="log" aria-label="Debug events">
      <div className="dbg-row dbg-headrow"><span>#</span><span>+time</span><span>type</span><span>summary</span><span className="num">in</span><span className="num">out</span></div>
      {shown.map((e) => { const open = S.run.dbgAll || S.run.dbgOpen.includes(e.seq); return <div key={e.seq} style={{ display: "contents" }}>
        <button className={"dbg-row" + (open ? " open" : "")} aria-expanded={open} onClick={() => { const o = S.run.dbgOpen; o.includes(e.seq) ? o.splice(o.indexOf(e.seq), 1) : o.push(e.seq); update(); }}>
          <span className="muted">{e.seq}</span><span className="muted">+{fmtMs(e.dt || 0)}</span><span className="dbg-type" style={{ color: `var(${EV_TONE[e.type.split(".")[0]] || "--fg"})` }}><EventIcon e={e} />{e.type}</span><span className="dbg-sum"><Linkify text={e.summary} /></span><span className="num">{e.input_tokens ? fmtK(e.input_tokens) : ""}</span><span className="num">{e.output_tokens ? fmtK(e.output_tokens) : ""}</span></button>
        {open && <pre className="dbg-json">{JSON.stringify(e, null, 2)}</pre>}</div>; })}
      {running && <div className="dbg-row"><span /><span /><span className="spinner" /><span className="muted">streaming…</span></div>}
    </div>
    <p className="muted" style={{ marginTop: 6 }}>Token-shaped strings and secret values are redacted before events are published or recorded.</p></>;
  return <>
    <div className={"card run-card" + (running ? " is-running" : "")}>
      <div className="card-head"><h3>{running ? "Running" : "Run"} · <code>{st.cmd}</code></h3>{running ? <span className="spinner" /> : <Pill s={st.status} label={st.id === "flow" ? ({ done: "Completed", failed: "Failed", stopped: "Stopped", interrupted: "Interrupted" }[st.status]) : undefined} />}</div>
      {running && st.id !== "flow" && <AiProgress evs={evs} className="ai-in-card" />}
      <div className="mstats">
        <Stat v={fmtMs(u.ms)} l="Elapsed" />
        <Stat v={fmtK(u.in + u.out)} l="Tokens" sub={`${fmtK(u.in)} in · ${fmtK(u.out)} out · ${fmtK(u.cached)} cached`} />
        <Stat v={fmtCredits(u)} l={isCopilot() ? "Credits" : "Cost"} sub={`${u.calls} model calls`} />
        <Stat v={u.tools} l="Tool calls" sub={`${u.toolErr} failed`} />
        <Stat v={u.files.length} l="Files" sub={`${u.files.filter((f) => f.op === "created").length} created · ${u.files.filter((f) => f.op === "modified").length} modified`} />
        <div className="mstat"><b>{ctxPct}%</b><span>Context used</span><span className="bar-track" style={{ marginTop: 4 }}><span className="bar-fill" style={{ width: ctxPct + "%" }} /></span></div>
      </div>
      <ResultsLink id={st.lastRun} ended={!running && evs.some((e) => e.type === "run.end")} />
    </div>
    <div className="tabs" role="tablist">{[["activity", "Activity"], ["files", `Files changed · ${u.files.length}`], ["debug", `Debug · ${evs.length} events`]].map(([v, l]) => <button key={v} role="tab" className="tab" aria-selected={tab === v} onClick={() => setTab(v)}>{l}</button>)}</div>
    {body}
  </>;
}

export { stepUsage };
