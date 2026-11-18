import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { ModelPicker } from "../ModelPicker.jsx";
import { ProjectPicker } from "../ProjectPicker.jsx";
import { confirmDialog } from "../Dialog.jsx";
import { Linkify } from "../FileLinks.jsx";
import {
  S,
  update,
  toast,
  errMsg,
  saveWorkflow,
  currentStep,
  totals,
  isCopilot,
  fmtK,
  loadStepEvents,
  downloadProjectZip,
} from "../store.js";
import { Icon } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { agentLabel } from "./Setup.jsx";
import { RunPanel } from "../components/RunPanel.jsx";
import { RunSettings } from "../components/RunSettings.jsx";

export const STATE_LABEL = {
  locked: "Locked",
  collecting: "Collecting inputs",
  ready: "Ready to run",
  running: "Running…",
  paused: "Awaiting approval",
  done: "Approved",
  failed: "Rejected",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
};
const STATE_PILL = {
  locked: "pending",
  collecting: "pending",
  ready: "running",
  running: "running",
  paused: "paused",
  done: "done",
  failed: "failed",
  cancelled: "cancelled",
  interrupted: "interrupted",
};
const PHASES = [
  ["Govern", 0, 0],
  ["Define", 1, 2],
  ["Design", 3, 4],
  ["Build", 5, 5],
];

export function stepState(wf, k) {
  const raw = wf.steps[k].status;
  if (raw !== "pending") return raw;
  if (k > 0 && wf.steps[k - 1].status !== "done") return "locked";
  return wf.steps[k].input.trim() ? "ready" : "collecting";
}
const statePill = (st) => <span className={`pill pill-${STATE_PILL[st]}`}>{STATE_LABEL[st]}</span>;

function Lifecycle({ wf }) {
  const n = wf.steps.length,
    W = 132,
    G = 52,
    X0 = 14,
    Y = 72,
    H = 62,
    cur = currentStep(wf),
    allDone = wf.steps.every((s) => s.status === "done");
  const x = (k) => X0 + k * (W + G),
    total = x(n - 1) + W + X0;
  const tone = {
    done: "--ok",
    running: "--info",
    paused: "--warn",
    failed: "--error",
    cancelled: "--muted",
    interrupted: "--error",
    ready: "--focus",
    collecting: "--muted",
    locked: "--border",
  };
  const by = Y + H + 52,
    sx = x(Math.min(1, n - 1)) + 16,
    ex = x(n - 1) + W - 16;
  const marker = (id, v) => (
    <marker
      id={"lc-arrow-" + id}
      viewBox="0 0 10 10"
      refX="9"
      refY="5"
      markerWidth="7"
      markerHeight="7"
      orient="auto-start-reverse"
    >
      <path d="M0 0 L10 5 L0 10z" style={{ fill: `var(${v})` }} />
    </marker>
  );
  const phases = n === 6 ? PHASES : [];
  return (
    <svg
      className="lc-svg"
      viewBox={`0 0 ${total} ${by + 26}`}
      style={{ minWidth: Math.round(total * 0.78) }}
      role="group"
      aria-label="Spec workflow lifecycle"
    >
      <defs>
        {marker("ok", "--ok")}
        {marker("dim", "--faint")}
        {marker("warn", "--warn")}
      </defs>
      {phases.map(([name, a, b]) => {
        const x1 = x(a) - 6,
          x2 = x(b) + W + 6;
        return (
          <g key={name}>
            <path d={`M${x1} 26 V20 H${x2} V26`} style={{ fill: "none", stroke: "var(--border)" }} />
            <text x={(x1 + x2) / 2} y="14" textAnchor="middle" className="lc-phase">
              {name.toUpperCase()}
            </text>
          </g>
        );
      })}
      {wf.steps.slice(0, -1).map((s, k) => {
        const ax = x(k) + W,
          bx = x(k + 1),
          my = Y + H / 2,
          cx = (ax + bx) / 2,
          col = s.status === "done" ? "--ok" : s.status === "paused" ? "--warn" : "--faint";
        return (
          <g key={"c" + k}>
            <path
              d={`M${ax} ${my} H${bx - 2}`}
              style={{ fill: "none", stroke: `var(${col})`, strokeWidth: 1.5 }}
              markerEnd={`url(#lc-arrow-${s.status === "done" ? "ok" : "dim"})`}
            />
            {s.gate && (
              <g>
                <title>Human approval gate</title>
                <rect
                  x={cx - 7}
                  y={my - 7}
                  width="14"
                  height="14"
                  transform={`rotate(45 ${cx} ${my})`}
                  style={{ fill: "var(--bg)", stroke: `var(${col})`, strokeWidth: 1.5 }}
                />
                {s.status === "done" && (
                  <path
                    d={`M${cx - 3.5} ${my} l2.5 2.5 l4.5 -5`}
                    style={{ fill: "none", stroke: "var(--ok)", strokeWidth: 1.6 }}
                  />
                )}
              </g>
            )}
          </g>
        );
      })}
      {wf.steps.map((s, k) => {
        const st = stepState(wf, k),
          col = tone[st],
          sel = S.sel === k,
          here = k === cur && !allDone,
          dim = st === "locked" || st === "collecting";
        const pick = () => {
          S.sel = k;
          update();
          loadStepEvents(k);
        };
        return (
          <g key={s.id}>
            <g
              className="lc-node"
              role="button"
              tabIndex={0}
              aria-label={`${s.title}: ${STATE_LABEL[st]}${here ? ", current step" : ""}`}
              onClick={pick}
              onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), pick())}
            >
              <rect
                x={x(k)}
                y={Y}
                width={W}
                height={H}
                rx="8"
                style={{
                  fill: `var(${sel ? "--select-bg" : "--surface"})`,
                  stroke: `var(${here ? "--focus" : col})`,
                  strokeWidth: here ? 2.5 : 1.3,
                  strokeDasharray: st === "locked" ? "4 3" : undefined,
                }}
              />
              <circle cx={x(k) + 18} cy={Y + 21} r="9" style={{ fill: `var(${dim ? "--code-bg" : col})` }} />
              <text
                x={x(k) + 18}
                y={Y + 25}
                textAnchor="middle"
                className="lc-num"
                style={{ fill: `var(${dim ? "--muted" : "--bg"})` }}
              >
                {st === "done" ? "✓" : k + 1}
              </text>
              <text x={x(k) + 34} y={Y + 25} className="lc-title">
                {s.title}
              </text>
              <text
                x={x(k) + 12}
                y={Y + 47}
                className="lc-state"
                style={{ fill: `var(${st === "locked" ? "--faint" : col === "--border" ? "--muted" : col})` }}
              >
                {STATE_LABEL[st]}
              </text>
            </g>
            {here && (
              <g aria-hidden="true">
                <path d={`M${x(k) + W / 2 - 6} ${Y - 9} h12 l-6 7z`} style={{ fill: "var(--focus)" }} />
                <rect
                  x={x(k) + W / 2 - 40}
                  y={Y - 27}
                  width="80"
                  height="17"
                  rx="8.5"
                  style={{ fill: "var(--focus)" }}
                />
                <text x={x(k) + W / 2} y={Y - 15} textAnchor="middle" className="lc-here">
                  YOU ARE HERE
                </text>
              </g>
            )}
          </g>
        );
      })}
      {!allDone &&
        (() => {
          const a = x(cur) + W * 0.72,
            b = x(cur) + W * 0.28;
          return (
            <>
              <path
                d={`M${a} ${Y + H} C ${a} ${Y + H + 24}, ${b} ${Y + H + 24}, ${b} ${Y + H + 3}`}
                style={{ fill: "none", stroke: "var(--warn)", strokeWidth: 1.3, strokeDasharray: "3 3" }}
                markerEnd="url(#lc-arrow-warn)"
              />
              <text
                x={x(cur) + W / 2}
                y={Y + H + 34}
                textAnchor="middle"
                className="lc-note"
                style={{ fill: "var(--warn)" }}
              >
                request changes ↺ rerun
              </text>
            </>
          );
        })()}
      <path
        d={`M${ex} ${Y + H} V${by} H${sx} V${Y + H + 4}`}
        style={{
          fill: "none",
          stroke: `var(${allDone ? "--ok" : "--faint"})`,
          strokeWidth: 1.3,
          strokeDasharray: "5 4",
        }}
        markerEnd={`url(#lc-arrow-${allDone ? "ok" : "dim"})`}
      />
      <text x={(sx + ex) / 2} y={by + 16} textAnchor="middle" className="lc-note">
        next feature or iteration: back to Specify (constitution stays)
      </text>
    </svg>
  );
}

const OPTS = [
  ["rules", "Apply functionality rules", () => `${S.ws.profile.rules.length} rules from .codegen/rules`],
  [
    "global",
    "Use corporate skills",
    () =>
      S.ws.libraries
        .filter((l) => S.ws.profile.global.includes(l.id))
        .map((l) => l.name)
        .join(", ") || "none selected",
  ],
  [
    "project",
    "Use project skills",
    () =>
      S.ws.libraries
        .filter((l) => S.ws.profile.project.includes(l.id))
        .map((l) => l.name)
        .join(", ") || "none selected",
  ],
  [
    "diagrams",
    "Attach input diagrams",
    () => Object.keys(S.ws.profile.diagrams || {}).join(" + ") || "none attached",
  ],
  [
    "tools",
    "Allow tools and extensions",
    () =>
      (S.ws.extensions || [])
        .filter((t) => t.approved && t.status !== "in review" && t.status !== "needs approval")
        .map((t) => t.name)
        .join(", ") || "none approved",
  ],
];

async function decide(st, kind) {
  try {
    await backend.Decide(st.lastRun, kind, kind === "changes" ? st.feedback || "" : "");
    if (kind === "changes") {
      st.feedback = "";
      saveWorkflow();
    }
    if (kind === "approve" && S.sel < S.wf.steps.length - 1) S.sel++;
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function runStep(st) {
  try {
    await backend.SaveWorkflow(S.wf);
    const id = await backend.RunStep(st.id);
    st.lastRun = id;
    st.status = "running";
    S.loaded[id] = true;
    S.events[id] ||= [];
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

function goSetUpSpecKit() {
  S.view = "inputs";
  update();
  setTimeout(() => document.querySelector(".sk-card")?.scrollIntoView({ block: "start" }), 50);
}

export function Workflow() {
  if (!S.ws || !S.wf) return <NeedWorkspace what="the workflow" />;
  const wf = S.wf,
    i = Math.min(S.sel, wf.steps.length - 1),
    st = wf.steps[i],
    state = stepState(wf, i),
    cur = currentStep(wf);
  const allDone = wf.steps.every((s) => s.status === "done");
  const lcHidden = !!S.layout["wf:lc:c"];
  const editable = !["running", "paused"].includes(state);
  const evs = st.lastRun ? S.events[st.lastRun] || [] : [];
  const gate =
    state === "paused" ? [...evs].reverse().find((e) => e.type === "gate.wait")?.gate || "approval" : null;
  const prov = S.setup.provider;
  const checks = [
    [
      i === 0 || wf.steps[i - 1].status === "done",
      i === 0 ? "No earlier step" : `${wf.steps[i - 1].title} approved`,
    ],
    [!!st.input.trim(), "Inputs collected"],
    [prov && prov !== "skipped", `Agent: ${agentLabel()}${S.setup.source ? " · " + S.setup.source : ""}`],
  ];
  // the backend refuses /speckit.* steps until Spec Kit is set up for the project (hub.go RunStep)
  const needsSpecKit = st.cmd.startsWith("/speckit") && !S.ws.speckit?.initialized;
  if (st.cmd.startsWith("/speckit")) checks.push([!needsSpecKit, "Spec Kit set up for this project"]);
  const canRun = checks.every((c) => c[0]) && editable;
  const t = totals();
  const opts = st.options;
  const edit = (k, v) => {
    st[k] = v;
    update();
    saveWorkflow();
  };
  return (
    <div className="wf">
      <Pane k="wf" v="--pane-l" pane=".wf-steps" min={200} max={420} label="Resize the step list" />
      <nav className="wf-steps" aria-label="Spec workflow steps">
        <h1>{wf.name}</h1>
        {wf.steps.map((s, k) => {
          const ks = stepState(wf, k);
          return (
            <button
              key={s.id}
              className={"wf-step" + (k === i ? " active" : "")}
              aria-current={k === cur ? "step" : undefined}
              onClick={() => {
                S.sel = k;
                update();
                loadStepEvents(k);
              }}
            >
              <span className={"card-index" + (ks === "done" ? " done" : "")}>
                {ks === "done" ? <Icon.check /> : k + 1}
              </span>
              <span className="grow">
                {s.title}
                {k === cur && !allDone && (
                  <>
                    {" "}
                    <span className="badge">current</span>
                  </>
                )}
                <code>{s.cmd}</code>
              </span>
            </button>
          );
        })}
        <p className="wf-gate-note">
          After every gated step the run stops for your approval. It moves to the next step only when you
          approve.
        </p>
        <RunSettings wf={wf} />
      </nav>
      <div className="wf-pane">
        <div className={"card lc-card" + (lcHidden ? " is-collapsed" : "")}>
          <div className="card-head">
            <h3>
              <button
                className="lc-toggle"
                aria-expanded={!lcHidden}
                aria-controls="lc-body"
                title={
                  lcHidden
                    ? "Show the lifecycle diagram"
                    : "Hide the lifecycle diagram (the step list on the left has the same steps)"
                }
                onClick={() => {
                  if (lcHidden) delete S.layout["wf:lc:c"];
                  else S.layout["wf:lc:c"] = 1;
                  update();
                }}
              >
                <span className="lc-caret" aria-hidden="true">
                  {lcHidden ? "▸" : "▾"}
                </span>{" "}
                Lifecycle
              </button>
            </h3>
            {t.in > 0 && (
              <>
                <span className="badge">{fmtK(t.in + t.out)} tokens</span>
                <span className="badge">
                  {isCopilot() ? t.premium + " premium req" : "$" + t.usd.toFixed(3)}
                </span>
                <span className="badge">{t.tools} tool calls</span>
                <span className="badge">{t.files} files</span>
              </>
            )}
            <span className="muted" style={{ marginLeft: "auto" }}>
              Step {cur + 1} of {wf.steps.length} · {wf.steps[cur].title} · {STATE_LABEL[stepState(wf, cur)]}
            </span>
          </div>
          {!lcHidden && (
            <div id="lc-body">
              <div className="lc-scroll">
                <Lifecycle wf={wf} />
              </div>
              <div className="lc-legend">
                <span>
                  <i style={{ borderColor: "var(--focus)" }} />
                  current
                </span>
                <span>
                  <i style={{ borderColor: "var(--ok)" }} />
                  approved
                </span>
                <span>
                  <i style={{ borderColor: "var(--warn)" }} />
                  awaiting approval
                </span>
                <span>
                  <i className="dash" />
                  locked
                </span>
                <span>
                  <b className="gate" />
                  approval gate
                </span>
                {allDone && (
                  <button
                    className="link"
                    style={{ marginLeft: "auto" }}
                    onClick={async () => {
                      if (
                        await confirmDialog("Every step goes back to pending; inputs are kept.", {
                          title: "Start the next iteration?",
                          ok: "Start",
                        })
                      ) {
                        await backend.ResetWorkflow().catch((e) => toast(errMsg(e), "error"));
                      }
                    }}
                  >
                    Start next iteration
                  </button>
                )}
              </div>
            </div>
          )}
          {lcHidden && allDone && (
            <div className="row-actions">
              <button
                className="btn secondary"
                onClick={async () => {
                  if (
                    await confirmDialog("Every step goes back to pending; inputs are kept.", {
                      title: "Start the next iteration?",
                      ok: "Start",
                    })
                  ) {
                    await backend.ResetWorkflow().catch((e) => toast(errMsg(e), "error"));
                  }
                }}
              >
                Start next iteration
              </button>
            </div>
          )}
        </div>

        <header className="view-head">
          <h1>
            {i + 1} · {st.title}{" "}
            <span className="muted" style={{ fontWeight: 400, fontSize: ".7em" }}>
              <code>{st.cmd}</code>
            </span>
          </h1>
          {statePill(state)}
          {(S.ws.projects || []).length > 0 && (
            <span className="wf-project" title="Each project has its own workflow progress">
              <ProjectPicker
                value={S.ws.speckit?.project || ""}
                disabled={state === "running" || state === "paused"}
                onNew={() => {
                  S.sk = { ...(S.sk || {}), newMode: true, name: "", result: null };
                  S.view = "inputs";
                  update();
                  setTimeout(
                    () => document.querySelector(".sk-card")?.scrollIntoView({ block: "start" }),
                    50,
                  );
                }}
              />
            </span>
          )}
          <span className="wf-model">
            <ModelPicker
              compact
              label="Model for workflow steps"
              value={S.wf.model || ""}
              disabled={state === "running"}
              onChange={(v) => {
                S.wf.model = v;
                update();
                saveWorkflow();
              }}
            />
          </span>
        </header>
        {wf.steps.some((x) => x.id === "implement" && x.status === "done") && (
          <div className="banner ok zip-banner">
            <Icon.check />{" "}
            <span>
              <b>Implementation is done.</b> Download the whole project
              {S.ws.speckit?.project ? (
                <>
                  {" "}
                  (<code className="inline-code">{S.ws.speckit.project}/</code> with the skills, rules and
                  diagrams it uses)
                </>
              ) : null}{" "}
              as one zip.
            </span>
            <button className="btn primary" disabled={S.zipBusy} onClick={() => downloadProjectZip(true)}>
              {S.zipBusy ? "Zipping…" : "Download project (.zip)"}
            </button>
          </div>
        )}
        {i !== cur && !allDone && state !== "locked" && (
          <div className="banner">
            You are viewing <b>{st.title}</b>. The workflow is currently at <b>{wf.steps[cur].title}</b>.{" "}
            <button
              className="link"
              onClick={() => {
                S.sel = cur;
                update();
                loadStepEvents(cur);
              }}
            >
              Go to current step
            </button>
          </div>
        )}
        {state === "paused" && (
          <div className={"gate-banner" + (gate === "loop" ? " loop" : "")}>
            <div className="gate-text">
              <Icon.warn />
              <div>
                {gate === "loop" ? (
                  <>
                    <b>The agent seems stuck in a loop</b>
                    <p>
                      The run was stopped. Accept what it produced, send feedback and rerun, or reject the
                      step.
                    </p>
                  </>
                ) : (
                  <>
                    <b>Human approval needed</b>
                    <p>
                      Review the {st.title.toLowerCase()} output below. Approving{" "}
                      {i < wf.steps.length - 1 ? (
                        <>
                          unlocks <b>{wf.steps[i + 1].title}</b>
                        </>
                      ) : (
                        "finishes the workflow"
                      )}
                      .
                    </p>
                  </>
                )}
              </div>
            </div>
            {!S.wfChanges ? (
              <div className="gate-actions">
                <button className="btn primary" onClick={() => decide(st, "approve")}>
                  {gate === "loop" ? "Accept output" : "Approve and continue"}
                </button>
                <button
                  className="btn secondary"
                  aria-expanded="false"
                  onClick={() => {
                    S.wfChanges = true;
                    update();
                    setTimeout(() => document.getElementById("wf-feedback")?.focus(), 30);
                  }}
                >
                  {gate === "loop" ? "Send feedback and rerun…" : "Request changes…"}
                </button>
                <button
                  className="btn secondary danger-quiet"
                  onClick={async () => {
                    if (
                      await confirmDialog(
                        `The ${st.title} step is marked as rejected; you can run it again later.`,
                        { title: "Reject this output?", ok: "Reject", danger: true },
                      )
                    )
                      decide(st, "reject");
                  }}
                >
                  Reject
                </button>
              </div>
            ) : (
              <div className="gate-changes">
                <label className="field">
                  <span className="muted">
                    What should change? The agent reruns the step with this feedback.
                  </span>
                  <textarea
                    id="wf-feedback"
                    rows={3}
                    value={st.feedback || ""}
                    placeholder="e.g. Add a principle about accessibility; keep it under one page."
                    onChange={(e) => {
                      st.feedback = e.target.value;
                      update();
                      saveWorkflow();
                    }}
                    onKeyDown={(e) => {
                      if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && st.feedback?.trim()) {
                        e.preventDefault();
                        S.wfChanges = false;
                        decide(st, "changes");
                      }
                    }}
                  />
                </label>
                <div className="gate-actions">
                  <button
                    className="btn primary"
                    disabled={!st.feedback?.trim()}
                    title="⌘Enter / Ctrl+Enter"
                    onClick={() => {
                      S.wfChanges = false;
                      decide(st, "changes");
                    }}
                  >
                    Send and rerun
                  </button>
                  <button
                    className="btn secondary"
                    onClick={() => {
                      S.wfChanges = false;
                      update();
                    }}
                  >
                    Cancel
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
        {state === "locked" && (
          <div className="banner wf-locked">
            <Icon.lock />
            <span>
              <b>{st.title}</b> is locked until <b>{wf.steps[i - 1].title}</b> is approved. You can prepare
              its inputs and instructions now.
            </span>
            {i !== cur && !allDone && (
              <button
                className="btn secondary"
                onClick={() => {
                  S.sel = cur;
                  update();
                  loadStepEvents(cur);
                }}
              >
                Go to {wf.steps[cur].title}
              </button>
            )}
          </div>
        )}
        {state === "interrupted" && (
          <div className="banner error">
            The app closed while this step was running. Run it again to continue.
          </div>
        )}

        <div className="two-col">
          <div className="card">
            <div className="card-head">
              <span className="card-index">1</span>
              <h3>Inputs</h3>
              {st.input.trim() ? (
                <span className="badge badge-ok">collected</span>
              ) : (
                <span className="badge">needed</span>
              )}
            </div>
            <p className="wf-desc">
              {st.ask}. Passed to <code className="inline-code">{st.cmd}</code> as its argument.
            </p>
            <textarea
              rows={5}
              placeholder={st.placeholder}
              disabled={!editable}
              value={st.input}
              onChange={(e) => edit("input", e.target.value)}
            />
          </div>
          <div className="card">
            <div className="card-head">
              <span className="card-index">2</span>
              <h3>Instructions for the agent</h3>
              {st.template && (
                <button className="link" onClick={() => edit("instructions", st.template)}>
                  Use template
                </button>
              )}
            </div>
            <p className="wf-desc">
              How the agent should work on this step. Sent with the command; not part of the spec.
            </p>
            <textarea
              rows={5}
              placeholder={st.template}
              disabled={!editable}
              value={st.instructions}
              onChange={(e) => edit("instructions", e.target.value)}
            />
            <ul className="lib-list">
              {OPTS.map(([k, l, d]) => (
                <li key={k}>
                  <input
                    type="checkbox"
                    id={"opt-" + k}
                    checked={!!opts[k]}
                    disabled={!editable}
                    onChange={(e) => {
                      opts[k] = e.target.checked;
                      update();
                      saveWorkflow();
                    }}
                  />
                  <label className="grow" htmlFor={"opt-" + k}>
                    {l}
                    <small>{d()}</small>
                  </label>
                </li>
              ))}
            </ul>
          </div>
        </div>

        <div className={"card ready-card" + (canRun ? " is-ready" : "")}>
          <div className="card-head">
            <span className="card-index">3</span>
            <h3>
              {canRun
                ? "Ready to run"
                : state === "running"
                  ? "Running"
                  : state === "paused"
                    ? "Ran — awaiting your approval"
                    : "Not ready yet"}
            </h3>
          </div>
          <ul className="checklist">
            {checks.map(([ok, l]) => (
              <li key={l} className={ok ? "ok" : ""}>
                {ok ? <Icon.check /> : <Icon.x />}
                <span>{l}</span>
                {!ok && l.startsWith("Spec Kit") && (
                  <button className="link" onClick={goSetUpSpecKit}>
                    Set up Spec Kit
                  </button>
                )}
              </li>
            ))}
            <li className="ok">
              <Icon.check />
              <span>
                {st.gate ? "Pauses for approval when done" : "No approval gate (runs straight through)"}
              </span>
            </li>
          </ul>
          <pre className="code" aria-label="Command preview">
            {st.cmd} {st.input.trim().split("\n")[0].slice(0, 90) || "…"}
            {st.instructions.trim() && (
              <>
                {"\n"}
                <span className="syn-comment">
                  # instructions: {st.instructions.trim().split("\n")[0].slice(0, 80)}
                </span>
              </>
            )}
            {"\n"}
            <span className="syn-comment">
              # context:{" "}
              {OPTS.filter(([k]) => opts[k])
                .map(([, l]) => l.toLowerCase())
                .join(", ") || "none"}
            </span>
          </pre>
          <div className="row-actions">
            <button
              className="btn primary"
              disabled={!canRun}
              aria-describedby={!canRun && editable ? "wf-why" : undefined}
              onClick={() => runStep(st)}
            >
              {["done", "failed", "cancelled", "interrupted"].includes(state) ? "Run again" : "Run " + st.cmd}
            </button>
            {!canRun && editable && (
              <span id="wf-why" className="muted run-why">
                Needed first: {checks.find((c) => !c[0])?.[1]}
              </span>
            )}
            {state === "running" && (
              <button
                className="btn secondary"
                onClick={() => backend.CancelRun(st.lastRun).catch((e) => toast(errMsg(e), "error"))}
              >
                Cancel run
              </button>
            )}
            <button
              className="link"
              onClick={async () => {
                try {
                  const p = await backend.Compose(st.id, "");
                  S._prompt = S._prompt === p ? null : p;
                  update();
                } catch (e) {
                  toast(errMsg(e), "error");
                }
              }}
            >
              {S._prompt ? "Hide full prompt" : "Show full prompt"}
            </button>
            <span className="muted">
              Agent: {agentLabel()} · reads {S.ws.root}
            </span>
          </div>
          {S._prompt && (
            <pre className="code console-out">
              <Linkify text={S._prompt} />
            </pre>
          )}
        </div>
        <RunPanel st={st} evs={evs} running={state === "running"} />
      </div>
    </div>
  );
}
