// The AI flow designer: describe a flow, get options built from your tools
// (each tried with sample data), and pick one; also "fix and improve" an
// existing flow. Opened from the Flow designer.
// Flow designer: string tools together into a flow.
//   left      the project's flows (new, import)
//   palette   step types (AI prompt, merge, transform, output) and tools; drag onto the canvas
//   canvas    steps as cards; drag a card to move it, drag from its ● port to another card to connect
//   inspector the selected step, or the flow (name, context, skills) when nothing is selected
// Flows are saved to .codegen/flows/<id>.json (autosave). Running them comes with the executor.
import { ModelPicker } from "../../ModelPicker.jsx";
import { toolRef } from "../../flowNames.js";
import { useState } from "react";
import { backend } from "../../backend.js";
import { update, toast, errMsg } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { compact } from "../../schema.js";
import { AiProgress } from "../../AiProgress.jsx";
import { StepIcon, fl, loadList, openFlow, save, tools } from "../Flows.jsx";
import { condCount } from "./Conditions.jsx";

const DESIGN_EXAMPLES = [
  "Every morning, tell me which blocking issues the quality gate found, grouped by file",
  "Find the TODOs in the project and give me a short list of the urgent ones",
  "Read a Jira ticket and write acceptance criteria for it",
  "Check the API spec for problems and summarise what to fix first",
];
const DESIGN_PHASES = [
  "Reading your tools and their contracts",
  "Sketching the options",
  "Wiring the steps",
  "Trying every option with sample data",
  "Fixing what did not work",
  "Ranking: tools and plain steps before AI",
];

export function openDesigner(mode) {
  const f = fl();
  const switching = f.ai?.mode !== (f.flow ? mode : "new");
  mode = "new"; // fixing a flow happens on Run flows, step by step
  f.ai = {
    ...(f.ai || {}),
    open: true,
    mode,
    prompt: switching ? "" : f.ai?.prompt || "",
    res: switching ? null : f.ai?.res,
  };
  update();
}

export function PathChips({ flow }) {
  const order = [],
    node = (id) => flow.nodes.find((x) => x.id === id);
  const visit = (id) => {
    if (order.includes(id)) return;
    order.push(id);
    flow.edges.filter((e) => e.from === id).forEach((e) => visit(e.to));
  };
  visit("start");
  return (
    <div className="guide-path">
      {order
        .filter((id) => id !== "start" && node(id))
        .map((id, i) => {
          const n = node(id),
            br = flow.edges.find((e) => e.to === id && e.branch);
          return (
            <span key={id} className={"guide-chip" + (n.type === "ai" ? " ai" : "")}>
              {i > 0 && (
                <span className="muted">
                  {br
                    ? `◆ ${br.branch === "else" ? "Otherwise" : (node(br.from)?.branches || []).find((b) => b.id === br.branch)?.label || br.branch} →`
                    : "→"}
                </span>
              )}
              <StepIcon n={n} tools={tools()} size={18} />
              {n.title || (n.type === "tool" ? toolRef(n.tool) : n.id)}
            </span>
          );
        })}
    </div>
  );
}

function stepDetail(n) {
  switch (n.type) {
    case "tool":
      return (
        toolRef(n.tool) +
        (Object.keys(n.args || {}).length
          ? " · " +
            Object.entries(n.args)
              .map(([k, v]) => `${k}=${v}`)
              .join(", ")
          : "") +
        (n.instruction ? " · AI fills: " + n.instruction : "")
      );
    case "ai":
      return (n.prompt || "").slice(0, 140) + (n.outputSchema ? " → " + compact(n.outputSchema) : "");
    case "improve":
      return (
        (n.pair === "after" ? "produce, the tool after checks" : "fix the tool before") +
        " · done when " +
        (n.goal || "").slice(0, 100)
      );
    case "transform":
      return n.op + (n.expr ? " " + n.expr : "");
    case "filter":
      return (
        (n.mode === "drop" ? "drop " : "keep ") +
        condCount(n.cond) +
        " condition(s)" +
        (n.orderBy?.length ? " · sort " + n.orderBy.join(", ") : "")
      );
    case "aggregate":
      return (
        (n.groupBy?.length ? "by " + n.groupBy.join(", ") + " · " : "") +
        (n.measures || []).map((m) => `${m.fn}(${m.field || ""})`).join(", ")
      );
    case "decision":
      return (n.branches || []).map((b) => b.label || b.id).join(" / ") + " / otherwise";
    case "output":
      return (
        (n.format || "").toUpperCase() +
        " · " +
        (n.actions || []).map((a) => a.type + (a.path ? " " + a.path : "")).join(", ")
      );
    default:
      return n.mode || n.split || "";
  }
}

function OptionCard({ o, improving, apply }) {
  const [steps, setSteps] = useState(false);
  const errs = o.issues.filter((i) => i.level === "error"),
    warns = o.issues.length - errs.length;
  return (
    <div className={"ai-option" + (o.recommended ? " recommended" : "")}>
      <div className="ai-option-head">
        <b>{o.title}</b>
        {o.recommended && <span className="badge badge-ok">Recommended</span>}
        <span
          className={"badge " + (o.deterministic ? "badge-info" : "badge-warn")}
          title={
            o.deterministic
              ? "Runs the same way every time: tools and plain steps only"
              : "Some steps ask the AI"
          }
        >
          {o.deterministic ? "No AI" : `AI in ${o.aiSteps} step${o.aiSteps === 1 ? "" : "s"}`}
        </span>
        {o.trial && (
          <span
            className={"badge " + (o.trial.ok ? "badge-ok" : "badge-warn")}
            title="Tried with sample data: tools and AI steps mocked, every other step run for real"
          >
            {o.trial.ok ? "✓ Tried" : "✗ Trial failed"}
          </span>
        )}
        {improving && o.fixes > 0 && (
          <span className="badge badge-ok">
            Fixes {o.fixes} problem{o.fixes === 1 ? "" : "s"}
          </span>
        )}
        {improving && o.rewrite && (
          <span className="badge badge-warn" title="It keeps few of the current steps">
            Rewrites most of it
          </span>
        )}
        {errs.length > 0 && (
          <span className="badge badge-warn" title={errs.map((i) => i.message).join("\n")}>
            {errs.length} problem{errs.length === 1 ? "" : "s"}
          </span>
        )}
        {!errs.length && warns > 0 && (
          <span className="badge" title={o.issues.map((i) => i.message).join("\n")}>
            {warns} note{warns === 1 ? "" : "s"}
          </span>
        )}
      </div>
      <p>{o.summary}</p>
      {o.why && <p className="muted fl-help">{o.why}</p>}
      <PathChips flow={o.flow} />
      {o.tools.length > 0 && (
        <div className="muted fl-help">
          Uses:{" "}
          {o.tools
            .map((t) => (
              <code key={t} className="inline-code" data-nofile>
                {toolRef(t)}
              </code>
            ))
            .reduce((a, b) => [a, " ", b])}
        </div>
      )}
      {improving && o.changes?.length > 0 && (
        <ul className="ai-changes">
          {o.changes.map((c, i) => (
            <li key={i}>{c}</li>
          ))}
        </ul>
      )}
      <details open={steps} onToggle={(e) => setSteps(e.currentTarget.open)}>
        <summary className="muted">
          {o.flow.nodes.length - 1} steps
          {o.flow.inputs?.length
            ? ` · inputs: ${o.flow.inputs.map((i) => i.label || i.name).join(", ")}`
            : ""}
        </summary>
        <ol className="ai-steps">
          {o.flow.nodes
            .filter((n) => n.type !== "start")
            .map((n) => (
              <li key={n.id}>
                <StepIcon n={n} tools={tools()} size={16} /> <b>{n.title || n.id}</b>{" "}
                <span className="muted">
                  {n.type} · {stepDetail(n)}
                </span>
              </li>
            ))}
        </ol>
        {errs.length > 0 && <div className="banner error">{errs.map((i) => i.message).join(" ")}</div>}
      </details>
      {o.trial && <TrialReport t={o.trial} />}
      <div className="row-actions">
        <button className="btn primary" onClick={() => apply(o)}>
          {improving ? "Save as a new version" : "Use this flow"}
        </button>
      </div>
    </div>
  );
}

// How an option's trial run went: each step with its (sample) output, and the checks.
const TRIAL_ICON = { done: "✓", failed: "✗", blocked: "✗", off: "–", "not run": "·" };
export function TrialReport({ t }) {
  const [open, setOpen] = useState(!t.ok);
  const passed = t.checks.filter((c) => c.ok).length;
  return (
    <details
      className={"ai-trial" + (t.ok ? "" : " failed")}
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary className="muted">
        Trial run with sample data · {passed} of {t.checks.length} check{t.checks.length === 1 ? "" : "s"}{" "}
        passed
      </summary>
      <ul className="ai-checks">
        {t.checks.map((c, i) => (
          <li key={i} className={c.ok ? "ok" : "bad"}>
            {c.ok ? "✓" : "✗"} {c.label}
            {c.note ? <span className="muted"> · {c.note}</span> : null}
          </li>
        ))}
      </ul>
      {Object.keys(t.inputs || {}).length > 0 && (
        <div className="muted fl-help">
          Inputs:{" "}
          {Object.entries(t.inputs)
            .map(([k, v]) => `${k} = ${v}`)
            .join(", ")}
        </div>
      )}
      <ol className="ai-trial-steps">
        {t.steps
          .filter((s) => s.type !== "start")
          .map((s) => (
            <li key={s.id} className={s.status}>
              <span className="ai-trial-st" title={s.status}>
                {TRIAL_ICON[s.status] || "·"}
              </span>{" "}
              <b>{s.title}</b>
              <span className="muted">
                {" "}
                {s.status === "off" ? "not on the path taken" : s.status}
                {s.mocked ? " · sample output (mocked)" : ""}
                {s.path ? " · → " + s.path : ""}
                {s.runs > 1 ? ` · ran ${s.runs}×` : ""}
              </span>
              {s.error && <div className="ai-trial-err">{s.error}</div>}
              {s.output && s.status === "done" && (
                <details>
                  <summary className="muted">output</summary>
                  <pre className="code guide-pre">{s.output}</pre>
                </details>
              )}
            </li>
          ))}
      </ol>
    </details>
  );
}

export function AIDesigner({ inline }) {
  const f = fl(),
    ai = f.ai,
    improving = ai.mode === "improve" && !!f.flow;
  const set = (k, v) => {
    ai[k] = v;
    update();
  };
  const design = async () => {
    if (f.dirty) await save();
    Object.assign(ai, { busy: true, error: "", res: null });
    update();
    try {
      ai.res = await backend.DesignFlows({
        prompt: ai.prompt,
        flowId: improving ? f.flow.id : "",
        options: 3,
        model: ai.model || "",
      });
    } catch (e) {
      ai.error = errMsg(e);
    }
    ai.busy = false;
    update();
  };
  const apply = async (o) => {
    try {
      const designed =
        "Made by the AI designer" + (ai.prompt.trim() ? `: “${ai.prompt.trim().slice(0, 200)}”` : "") + ".";
      let id;
      if (improving) {
        const st = await backend.SaveFlowVersion(f.flow.id, { ...o.flow, designed });
        id = st.flow.id;
        toast(`Saved as ${st.flow.name}; the original is unchanged.`);
      } else {
        const base = await backend.NewFlow(o.flow.name || o.title);
        await backend.SaveFlow({
          ...base,
          name: o.flow.name || o.title,
          description: o.summary,
          inputs: o.flow.inputs || [],
          nodes: o.flow.nodes,
          edges: o.flow.edges,
          designed,
        });
        id = base.id;
        toast(`Created the flow ${o.flow.name || o.title}. Check the steps, then Run it.`);
      }
      ai.open = false;
      ai.res = null;
      await loadList();
      await openFlow(id);
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  // over the canvas it is a dialog (choosing between options needs the room); on the empty page it is inline
  const panel = (
    <div
      className={"ai-designer" + (inline ? " inline" : "")}
      role="dialog"
      aria-modal={inline ? undefined : "true"}
      aria-label="AI designer"
      onKeyDown={(e) => {
        if (e.key === "Escape" && !inline) {
          e.stopPropagation();
          set("open", false);
        }
      }}
    >
      <div className="ai-designer-head">
        <Icon.spark />
        <h3>{improving ? `Fix and improve “${f.flow.name}”` : "New flow with AI"}</h3>
        <span className="muted">
          {improving
            ? "The AI changes this flow; you get versions of it to choose from"
            : "Describe what you want in your own words"}
        </span>
        <button
          className="link"
          style={{ marginLeft: "auto" }}
          aria-label="Close"
          onClick={() => set("open", false)}
        >
          <Icon.x />
        </button>
      </div>
      {improving &&
        (() => {
          const n = f.issues.filter((i) => i.level === "error").length;
          return n ? (
            <div className="banner">
              This flow has {n} problem{n === 1 ? "" : "s"}: the AI fixes {n === 1 ? "it" : "them"} first.
            </div>
          ) : null;
        })()}
      <div className="ai-model">
        <span className="muted">Model</span>
        <ModelPicker
          value={ai.model || ""}
          onChange={(v) => set("model", v)}
          label="Model for the AI designer"
        />
        <small className="muted">Default: the app's model.</small>
      </div>
      <textarea
        rows={3}
        value={ai.prompt}
        placeholder={
          improving
            ? "What should be fixed or better? e.g. it fails on the scan step; stop when there is nothing to report; use the tools instead of AI. Leave empty to fix its problems and improve it in general."
            : "e.g. " + DESIGN_EXAMPLES[0]
        }
        onChange={(e) => set("prompt", e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) design();
        }}
      />
      {!improving && !ai.prompt && (
        <div className="chips">
          {DESIGN_EXAMPLES.map((x) => (
            <button key={x} type="button" className="chip" onClick={() => set("prompt", x)}>
              {x.split(" ").slice(0, 7).join(" ")}…
            </button>
          ))}
        </div>
      )}
      <div className="row-actions">
        <button
          className="btn primary"
          disabled={ai.busy || (!improving && !ai.prompt.trim())}
          onClick={design}
        >
          {ai.busy
            ? improving
              ? "Working on it…"
              : "Designing…"
            : ai.res
              ? "Try again"
              : improving
                ? "Fix and improve"
                : "Design options"}
        </button>
        <span className="muted fl-help">
          {improving
            ? "It keeps this flow's steps and changes only what is needed, using your tools first, then plain steps, and AI steps only where nothing else fits. Every version is tried with sample data before you see it (tools and AI mocked, everything else run for real); the one you choose is saved as a new version, and the original stays as it is."
            : "It uses your tools first, connected by their data shapes, then plain steps, and AI steps only where nothing else fits. Every option is tried with sample data before you see it (tools and AI mocked, everything else run for real); the one you choose opens here."}
        </span>
      </div>
      {ai.busy && <AiProgress phases={DESIGN_PHASES} />}
      {ai.error && <div className="banner error">{ai.error}</div>}
      {ai.res && (
        <>
          <div className="ai-options">
            {ai.res.options.map((o, i) => (
              <OptionCard key={i} o={o} improving={improving} apply={apply} />
            ))}
          </div>
          <p className="muted fl-help">
            {ai.res.repaired ? "Some options were fixed automatically after the check. " : ""}
            {ai.res.usage?.input_tokens
              ? `Used ${(ai.res.usage.input_tokens + ai.res.usage.output_tokens).toLocaleString()} tokens`
              : ""}
            {ai.res.usage?.premium_requests
              ? ` · ${ai.res.usage.premium_requests} premium request${ai.res.usage.premium_requests === 1 ? "" : "s"}`
              : ""}
            {ai.res.usage?.cost_usd ? ` · $${ai.res.usage.cost_usd.toFixed(3)}` : ""}
          </p>
        </>
      )}
    </div>
  );
  return inline ? (
    panel
  ) : (
    <div className="modal-scrim ai-scrim" role="presentation">
      {panel}
    </div>
  );
}
