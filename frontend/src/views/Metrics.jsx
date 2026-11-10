import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, saveText, fmtK, isCopilot } from "../store.js";
import { Pill, Stat } from "../ui.jsx";

const TITLE = { constitution: "Constitution", specify: "Specify", clarify: "Clarify", plan: "Plan", tasks: "Tasks", implement: "Implement" };

export function Metrics() {
  useEffect(() => { backend.Metrics().then((m) => { S.metrics = m; update(); }, (e) => toast(errMsg(e))); }, []);
  const m = S.metrics;
  if (!m) return <section className="view"><h1>Metrics</h1><p className="muted">Loading…</p></section>;
  const max = Math.max(...m.perStep.map((p) => p.hours), 1);
  const csv = () => {
    const rows = [["metric", "value"], ["files_created", m.project.filesCreated], ["files", m.project.files], ["rules", m.project.rules], ["hours_saved_project", m.project.hoursSaved],
      ["times_used", m.allTime.timesUsed], ["hours_saved_all_time", m.allTime.hoursSaved], ["input_tokens", m.allTime.usage.input_tokens], ["output_tokens", m.allTime.usage.output_tokens],
      ["cost_usd", m.allTime.usage.cost_usd.toFixed(4)], ["premium_requests", m.allTime.usage.premium_requests],
      ["flows_executed", m.flows?.executed || 0], ["flows_completed", m.flows?.completed || 0], ["flow_hours_saved", (m.flows?.hoursSaved || 0).toFixed(2)], ["flow_tool_executions", m.flows?.toolExecutions || 0]];
    saveText("codegen-metrics.csv", rows.map((r) => r.join(",")).join("\n") + "\n", "text/csv");
  };
  const S2 = (p) => <Stat cls="stat card" {...p} />;
  return (
    <section className="view"><header className="view-head"><h1>Metrics</h1><div className="head-controls"><button className="btn secondary" onClick={csv}>Download CSV</button></div></header>
      <h4>This project</h4>
      <div className="stats">{S2({ v: m.project.filesCreated, l: "Files created", sub: "by the agent" })}{S2({ v: m.project.files, l: "Files", sub: "in the workspace" })}{S2({ v: m.project.rules, l: "Functionality rules", sub: "in .codegen/rules" })}{S2({ v: m.project.hoursSaved.toFixed(1) + " h", l: "Human hours saved", sub: "estimate per approved step" })}</div>
      <h4>All time</h4>
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
      </tbody></table> : <p className="muted">No runs recorded yet.</p>}</div>
    </section>
  );
}
