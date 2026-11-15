import { useEffect } from "react";
import { backend } from "../backend.js";
import { EmptyState } from "../ui.jsx";
import { S, update, toast, errMsg, saveText, fmtK, isCopilot } from "../store.js";
import { Pill, Stat } from "../ui.jsx";

const TITLE = { constitution: "Constitution", specify: "Specify", clarify: "Clarify", plan: "Plan", tasks: "Tasks", analyze: "Analyze", implement: "Implement" };

// Every list and number present, even on a brand-new machine with no runs.
function normalizeMetrics(m = {}) {
  const usage = (u) => ({ input_tokens: 0, output_tokens: 0, cached_tokens: 0, cost_usd: 0, premium_requests: 0, model_calls: 0, tool_calls: 0, ...(u || {}) });
  return {
    ...m,
    project: { filesCreated: 0, files: 0, rules: 0, hoursSaved: 0, ...(m.project || {}), usage: usage(m.project?.usage) },
    allTime: { timesUsed: 0, hoursSaved: 0, ...(m.allTime || {}), usage: usage(m.allTime?.usage) },
    perStep: m.perStep || [],
    recent: m.recent || [],
    flows: { executed: 0, completed: 0, failed: 0, stopped: 0, toolExecutions: 0, aiCalls: 0, hoursSaved: 0, projectRuns: 0, projectHours: 0, ...(m.flows || {}), usage: usage(m.flows?.usage), top: m.flows?.top || [] },
  };
}

// The time window: a preset, the last N hours/days, or a start and end.
const PRESETS = [["24h", "Last 24 hours"], ["7d", "Last 7 days"], ["30d", "Last 30 days"], ["all", "All time"], ["last", "Last…"], ["custom", "Custom"]];
const HOUR = 3600e3, DAY = 24 * HOUR;
const mr = () => (S.metricsRange ||= { preset: "all", n: 12, unit: "hours", from: "", to: "" });
const pad = (n) => String(n).padStart(2, "0");
const localInput = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;

// rangeOf: the {from, to} (RFC 3339) of the chosen window; "" is unbounded
export function rangeOf(r, now = Date.now()) {
  const since = (ms) => ({ from: new Date(now - ms).toISOString(), to: "" });
  switch (r.preset) {
    case "24h": return since(DAY);
    case "7d": return since(7 * DAY);
    case "30d": return since(30 * DAY);
    case "last": return since(Math.max(1, +r.n || 1) * (r.unit === "days" ? DAY : HOUR));
    case "custom": {
      const t = (v) => (v ? new Date(v).toISOString() : "");
      return { from: t(r.from), to: t(r.to) };
    }
    default: return { from: "", to: "" };
  }
}

function rangeLabel(r) {
  const p = PRESETS.find(([k]) => k === r.preset);
  if (r.preset === "last") return `Last ${Math.max(1, +r.n || 1)} ${r.unit}`;
  if (r.preset !== "custom") return p?.[1] || "All time";
  const f = (v) => (v ? new Date(v).toLocaleString() : "");
  return r.from && r.to ? `${f(r.from)} – ${f(r.to)}` : r.from ? `Since ${f(r.from)}` : r.to ? `Until ${f(r.to)}` : "All time";
}

async function loadMetrics() {
  const r = mr(), q = rangeOf(r);
  if (q.from && q.to && q.from >= q.to) { toast("The start time must be before the end time."); return; }
  S.metricsLoading = true; update();
  try { S.metrics = normalizeMetrics(await backend.MetricsIn(q)); S.metricsShown = rangeLabel(r); }
  catch (e) { toast(errMsg(e), "error"); }
  finally { S.metricsLoading = false; update(); }
}

function RangeBar() {
  const r = mr(), set = (patch, reload = true) => { Object.assign(r, patch); update(); if (reload) loadMetrics(); };
  return <div className="card metrics-range">
    <div className="seg" role="group" aria-label="Time range">{PRESETS.map(([k, l]) => <button key={k} type="button" aria-pressed={r.preset === k}
      onClick={() => { const patch = { preset: k }; if (k === "custom" && !r.from) { patch.from = localInput(new Date(Date.now() - 7 * DAY)); patch.to = localInput(new Date()); } set(patch); }}>{l}</button>)}</div>
    {r.preset === "last" && <div className="metrics-range-row">
      <label className="muted" htmlFor="mr-n">Last</label>
      <input id="mr-n" type="number" min="1" max="10000" value={r.n} onChange={(e) => set({ n: e.target.value }, false)} onKeyDown={(e) => e.key === "Enter" && loadMetrics()} />
      <select aria-label="Unit" value={r.unit} onChange={(e) => set({ unit: e.target.value })}><option value="hours">hours</option><option value="days">days</option></select>
      <button className="btn secondary small" onClick={loadMetrics}>Apply</button></div>}
    {r.preset === "custom" && <div className="metrics-range-row">
      <label className="muted" htmlFor="mr-from">From</label><input id="mr-from" type="datetime-local" value={r.from} max={r.to || undefined} onChange={(e) => set({ from: e.target.value }, false)} />
      <label className="muted" htmlFor="mr-to">To</label><input id="mr-to" type="datetime-local" value={r.to} min={r.from || undefined} onChange={(e) => set({ to: e.target.value }, false)} />
      <button className="btn secondary small" onClick={loadMetrics}>Apply</button>
      <button className="link" onClick={() => set({ to: localInput(new Date()) })}>To now</button></div>}
    <small className="muted">{S.metricsLoading ? "Loading…" : `Showing ${S.metricsShown || "All time"} · ${S.metrics?.entries ?? 0} recorded step${S.metrics?.entries === 1 ? "" : "s"} and runs`}. Files and rules are the project's current counts.</small>
  </div>;
}

export function Metrics() {
  useEffect(() => { loadMetrics(); }, []);
  const m = S.metrics;
  if (!m) return <section className="view"><h1>Metrics</h1><p className="muted">Loading…</p></section>;
  const max = Math.max(...m.perStep.map((p) => p.hours), 1);
  const csv = () => {
    const q = rangeOf(mr());
    const rows = [["metric", "value"], ["range", `"${S.metricsShown || "All time"}"`], ["from", q.from], ["to", q.to], ["files_created", m.project.filesCreated], ["files", m.project.files], ["rules", m.project.rules], ["hours_saved_project", m.project.hoursSaved],
      ["times_used", m.allTime.timesUsed], ["hours_saved_all_time", m.allTime.hoursSaved], ["input_tokens", m.allTime.usage.input_tokens], ["output_tokens", m.allTime.usage.output_tokens],
      ["cost_usd", m.allTime.usage.cost_usd.toFixed(4)], ["premium_requests", m.allTime.usage.premium_requests],
      ["flows_executed", m.flows?.executed || 0], ["flows_completed", m.flows?.completed || 0], ["flow_hours_saved", (m.flows?.hoursSaved || 0).toFixed(2)], ["flow_tool_executions", m.flows?.toolExecutions || 0]];
    saveText("codegen-metrics.csv", rows.map((r) => r.join(",")).join("\n") + "\n", "text/csv");
  };
  const S2 = (p) => <Stat cls="stat card" {...p} />;
  return (
    <section className="view"><header className="view-head"><h1>Metrics</h1><div className="head-controls"><button className="btn secondary" onClick={csv}>Download CSV</button></div></header>
      <RangeBar />
      <h4>This project</h4>
      <div className="stats">{S2({ v: m.project.filesCreated, l: "Files created", sub: "by the agent" })}{S2({ v: m.project.files, l: "Files", sub: "in the workspace" })}{S2({ v: m.project.rules, l: "Functionality rules", sub: "in .codegen/rules" })}{S2({ v: m.project.hoursSaved.toFixed(1) + " h", l: "Human hours saved", sub: "estimate per approved step" })}</div>
      <h4>{mr().preset === "all" ? "All time" : "All projects"}</h4>
      <div className="stats">{S2({ v: m.allTime.timesUsed, l: "Times used", sub: "workflows completed" })}{S2({ v: m.allTime.hoursSaved.toFixed(1) + " h", l: "Cumulative hours saved", sub: "across all projects" })}
        {S2({ v: fmtK(m.allTime.usage.input_tokens + m.allTime.usage.output_tokens), l: "Tokens", sub: `${m.allTime.usage.model_calls} model calls` })}
        {S2({ v: "$" + m.allTime.usage.cost_usd.toFixed(2), l: "Claude cost", sub: `${m.allTime.usage.premium_requests} Copilot premium requests` })}</div>
      <h4>Flows</h4>
      <div className="stats">{S2({ v: m.flows?.executed || 0, l: "Flows executed", sub: `${m.flows?.completed || 0} completed · ${m.flows?.failed || 0} failed · ${m.flows?.stopped || 0} stopped` })}
        {S2({ v: (m.flows?.hoursSaved || 0).toFixed(1) + " h", l: "Hours saved by flows", sub: `${(m.flows?.projectHours || 0).toFixed(1)} h in this project` })}
        {S2({ v: m.flows?.toolExecutions || 0, l: "Tool executions", sub: `${m.flows?.aiCalls || 0} AI calls` })}
        {S2({ v: isCopilot() ? (m.flows?.usage?.premium_requests || 0) : "$" + (m.flows?.usage?.cost_usd || 0).toFixed(2), l: isCopilot() ? "Premium requests" : "Cost of flow runs", sub: fmtK((m.flows?.usage?.input_tokens || 0) + (m.flows?.usage?.output_tokens || 0)) + " tokens" })}</div>
      {m.flows?.top?.length > 0 && <div className="card"><h3>Flows by runs</h3><table className="table"><thead><tr><th>Flow</th><th>Runs</th><th>Completed</th><th>Tool runs</th><th>Hours saved</th><th>{isCopilot() ? "Premium req" : "Cost"}</th><th>Last run</th></tr></thead><tbody>
        {m.flows.top.map((t) => <tr key={t.flow}><td><b>{t.flow}</b></td><td>{t.runs}</td><td>{t.completed} <span className="muted">({Math.round((t.completed / t.runs) * 100)}%)</span></td><td>{t.tools}</td><td>{t.hoursSaved.toFixed(1)}</td><td>{isCopilot() ? t.premium : "$" + t.costUsd.toFixed(3)}</td><td>{new Date(t.last).toLocaleString()}</td></tr>)}
      </tbody></table></div>}
      <div className="card"><h3>Hours saved by step</h3><p className="wf-desc">Team estimate of manual effort for each step, credited once per approved step.</p>{m.perStep.map((p) => <div className="bar-row" key={p.step}><span className="bar-label">{TITLE[p.step] || p.step}</span><span className="bar-track"><span className="bar-fill" style={{ width: (p.hours / max) * 100 + "%" }} /></span><span className="bar-value">{p.hours} h</span></div>)}</div>
      <div className="card"><h3>Recent runs</h3>{m.recent.length ? <table className="table"><thead><tr><th>Project</th><th>Last activity</th><th>Steps</th><th>Files</th><th>Hours saved</th><th>Status</th></tr></thead><tbody>
        {m.recent.map((r) => <tr key={r.name + r.at}><td>{r.name}</td><td>{new Date(r.at).toLocaleString()}</td><td>{r.approved}/{r.steps}</td><td>{r.files}</td><td>{r.hours.toFixed(1)}</td><td><Pill s={r.status === "done" ? "done" : r.status === "failed" ? "failed" : "paused"} /></td></tr>)}
      </tbody></table> : <EmptyState compact title="No runs in this range" actions={[["Run a workflow step", () => { S.view = "workflow"; update(); }], ["Run a flow", () => { S.view = "flowrun"; update(); }]]}>Metrics fill in as workflow steps and flows run. Try a longer range above if you have older runs.</EmptyState>}</div>
    </section>
  );
}
