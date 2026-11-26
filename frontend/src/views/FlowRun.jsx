// Run flows: execute a flow from the Flow designer.
//   left     flows and their recent runs
//   main     run setup (inputs, credentials, options) or the live run: steps,
//            the review panel (accept, stop, refine with AI, run a command,
//            edit, change settings and retry), outputs and activity
//   sidebar  progress and metrics: steps, elapsed time, tokens, credits, tools
import { TAG } from "./flows/Triggers.jsx";
import { registerPlace } from "../navigation.js";
import { FlowFilterBar, FlowGroup, FlowTagChips, flowFilter, groupFlows } from "../flowLists.jsx";
import { dragProps, loadLayout } from "../listLayout.jsx";
import { dateDefault } from "../dates.js";
import { useEffect, useState } from "react";
import { FLOW_STATUS_LABEL as STATUS_LABEL } from "../labels.js";
export { STATUS_LABEL };
import { backend } from "../backend.js";
import { openFlowGuide } from "./FlowGuide.jsx";
import { EmptyState, RefreshButton, usePoll, ListHead } from "../ui.jsx";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg, usageOf, fmtK, fmtMs, isCopilot, refreshExtensions } from "../store.js";
import { Icon, Pill } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { ago } from "../time.js";
import { Setup } from "./flowrun/Setup.jsx";
import { runProgress, workSteps } from "./flowrun/RunSimple.jsx";
import { RunView } from "./flowrun/RunView.jsx";

export const frs = () =>
  (S.frun ||= {
    flows: null,
    flowId: "",
    flow: null,
    creds: [],
    keychain: false,
    runs: [],
    runId: "",
    inputs: {},
    files: {},
    opts: defaultOpts(),
    secretVals: {},
    showOpts: false,
    tab: "refine",
    decision: {},
  });
const defaultOpts = () => ({
  review: "ai",
  repeat: 1,
  forEach: { input: "", split: "lines", filter: "", max: 100 },
  timeoutMinutes: 0,
  stepTimeoutSeconds: 0,
  startAt: "",
  skip: [],
  stopAfter: "",
  model: "",
});
export const LIVE = ["running", "review", "scheduled"];
export const pill = (s) =>
  ({
    review: "paused",
    scheduled: "pending",
    interrupted: "failed",
    stopped: "cancelled",
    skipped: "pending",
  })[s] || s;

// Open the runner on a flow (from the designer's Run button).
export function openFlowRunner(id) {
  const r = frs();
  r.flowId = id;
  r.runId = "";
  S.view = "flowrun";
  loadFlow(id);
  update();
}

// how often the list refreshes by itself (seconds)
const FLOWRUN_POLL = 30;

async function loadAll() {
  const r = frs();
  // Chatflows are listed too: their run is a chat's setup (Start chat)
  r.flows = (await backend.Flows().catch(() => [])) || [];
  r.runs = await backend.FlowRuns(100).catch(() => []);
  r.triggers = (await backend.FlowTriggers("").catch(() => [])) || [];
  r.queue = (await backend.TriggerQueue().catch(() => [])) || [];
  r.keychain = await backend.KeychainAvailable().catch(() => false);
  if (!r.flowId && r.flows.length) await loadFlow(r.flows[0].id);
  update();
}

export async function loadFlow(id) {
  const r = frs();
  try {
    r.flow = await backend.LoadFlow(id);
    r.flowId = id;
    r.inputs = {};
    r.files = {};
    for (const i of r.flow.inputs) {
      r.inputs[i.name] =
        i.type === "secret"
          ? ""
          : i.type === "date" || i.type === "datetime"
            ? dateDefault(i.type, i.default)
            : i.default || "";
      r.files[i.name] = (i.files || []).map((p) => ({
        path: p,
        name: p.split("/").pop(),
        kind: /\.pdf$/i.test(p) ? "pdf" : /\.(png|jpe?g|gif|webp)$/i.test(p) ? "image" : "text",
        size: 0,
      }));
    }
    r.creds = await backend.FlowCredentials(id).catch(() => []);
    r.cmds = (await backend.FlowCommands(id).catch(() => [])) || [];
    r.secretStatus = Object.fromEntries(
      ((await backend.FlowSecretStatus(id).catch(() => [])) || []).map((s) => [s.name, s]),
    );
    r.typeSecret = {};
    r.saveSecret = {};
    r.reveal = {};
  } catch (e) {
    toast(errMsg(e), "error");
  }
  update();
}

// showFlowRun opens Run flows on one run (used by the status bar).
// openRunnerWindow opens a flow in a SpecWright window of its own (the
// desktop app starts a runner window; the browser preview a tab).
export async function openRunnerWindow(id) {
  if (backend.kind !== "desktop") {
    window.open(location.origin + location.pathname + "?runner=" + encodeURIComponent(id), "_blank");
    return;
  }
  try {
    await backend.OpenRunnerWindow(id);
    toast("Opening it in its own window…");
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

export function showFlowRun(id) {
  S.view = "flowrun";
  update();
  openRun(id);
}

export async function openRun(id) {
  const r = frs();
  r.runId = id;
  if (!S.flowRuns[id]) {
    try {
      S.flowRuns[id] = await backend.FlowRun(id);
    } catch (e) {
      toast(errMsg(e), "error");
    }
  }
  if (!S.loaded[id]) {
    S.loaded[id] = true;
    const evs = (await backend.RunEvents(id).catch(() => [])) || [];
    const seen = new Set((S.events[id] || []).map((e) => e.seq));
    S.events[id] = [...(S.events[id] || []), ...evs.filter((e) => !seen.has(e.seq))].sort(
      (a, b) => a.seq - b.seq,
    );
  }
  const st = S.flowRuns[id];
  if (st && st.flowId !== r.flowId) await loadFlow(st.flowId);
  update();
}

// isChatflow: the flow has a Chat step (Run flows starts a chat with it)
export const isChatflow = (f) => !!f?.nodes?.some((n) => n.type === "chat");

// start runs the flow; for a Chatflow it starts a chat (its setup as a run
// that hands over to a chat session), or with mode "setup-only" its setup alone
export async function start(mode) {
  const r = frs(),
    f = r.flow,
    o = r.opts;
  const inputs = { ...r.inputs },
    files = {};
  if (f.inputs.some((i) => (r.files[i.name] || []).some((a) => a.saving)))
    return toast("Wait until the attached files are saved.");
  // files go separately; the backend checks them and builds each input's value by its type
  for (const i of f.inputs) {
    const paths = (r.files[i.name] || []).filter((a) => a.path).map((a) => a.path);
    if (["file", "files", "clipboard", "multiline"].includes(i.type) && paths.length) files[i.name] = paths;
    if (["file", "files"].includes(i.type) && paths.length) inputs[i.name] = "";
  }
  const saveSecrets = f.inputs
    .filter((i) => i.type === "secret" && r.saveSecret?.[i.name] && inputs[i.name])
    .map((i) => i.name);
  const opts = {
    inputs,
    files,
    saveSecrets,
    review: o.review,
    repeat: +o.repeat || 1,
    timeoutMinutes: +o.timeoutMinutes || 0,
    stepTimeoutSeconds: +o.stepTimeoutSeconds || 0,
    skip: o.skip,
    stopAfter: o.stopAfter,
    model: o.model.trim(),
  };
  if (o.forEach.input)
    opts.forEach = {
      input: o.forEach.input,
      split: o.forEach.split,
      filter: o.forEach.filter,
      max: +o.forEach.max || 100,
    };
  if (o.startAt) opts.startAt = new Date(o.startAt).toISOString();
  if (isChatflow(f)) {
    opts.chat = { role: mode === "setup-only" ? "setup-only" : "setup" };
    delete opts.startAt; // a chat starts now
    delete opts.forEach;
    opts.repeat = 1;
  }
  try {
    const st = await backend.RunFlow(f.id, opts);
    S.flowRuns[st.id] = st;
    S.loaded[st.id] = true;
    S.events[st.id] ||= [];
    r.runId = st.id;
    r.decision = {};
    if (saveSecrets.length) {
      for (const n of saveSecrets) {
        r.saveSecret[n] = false;
        r.typeSecret[n] = false;
        r.inputs[n] = "";
      }
      backend.FlowSecretStatus(f.id).then(
        (l) => {
          r.secretStatus = Object.fromEntries((l || []).map((x) => [x.name, x]));
          update();
        },
        () => {},
      );
    }
    r.runs = [{ id: st.id, workflow: f.id, state: st.status, started: st.started, usage: {} }, ...r.runs];
    update();
  } catch (e) {
    // refused for unapproved commands (they may have changed since the flow was loaded): show the list
    if (/not approved yet/.test(errMsg(e))) {
      r.cmds = (await backend.FlowCommands(f.id).catch(() => r.cmds)) || [];
      update();
      document.querySelector(".fr-cmds")?.scrollIntoView({ block: "center" });
    }
    toast(errMsg(e), "error");
  }
}

async function approveCommands(steps) {
  const r = frs();
  try {
    r.cmds = (await backend.ApproveFlowCommands(r.flow.id, steps)) || [];
    update();
    toast(
      steps.length === 1
        ? "Command approved for this project."
        : `${steps.length} commands approved for this project.`,
    );
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// Commands: a flow's "Run a command" steps, and its steps that send data out
// of the computer (an HTTP POST, a Slack message), must be approved for the
// project before it runs
export function Commands() {
  const r = frs(),
    cmds = r.cmds || [],
    todo = cmds.filter((c) => !c.approved),
    sends = cmds.some((c) => c.kind === "sends"),
    shell = cmds.some((c) => c.kind !== "sends");
  if (!cmds.length) return null;
  return (
    <div
      className={"card fr-cmds" + (todo.length ? " needs" : "")}
      role="region"
      aria-label="Commands this flow runs"
    >
      <div className="card-head">
        <Icon.warn />
        <h3>
          {sends && !shell
            ? "What this flow sends out"
            : sends
              ? "Commands and what this flow sends out"
              : "Commands this flow runs"}
        </h3>
        {todo.length ? (
          <span className="badge badge-warn">{todo.length} not approved</span>
        ) : (
          <span className="badge badge-ok">approved</span>
        )}
      </div>
      <p className="wf-desc">
        {shell && "Some steps run shell commands in the project folder. "}
        {sends &&
          "Some steps send data out of this computer (an HTTP request that changes things, a message to Slack, Teams or a webhook). "}
        Flows can come from files, pasted steps or the AI, so each must be approved for this project before
        the flow runs; a changed step needs a new approval.
      </p>
      <ul className="fr-cmd-list">
        {cmds.map((c) => (
          <li key={c.step}>
            <b>{c.title}</b>
            <code className="fr-cmd">
              {c.kind === "sends" ? "↗ " : "$ "}
              {c.command}
            </code>
            {c.approved ? (
              <span className="badge badge-ok">approved</span>
            ) : (
              <button className="btn secondary" onClick={() => approveCommands([c.step])}>
                Approve
              </button>
            )}
          </li>
        ))}
      </ul>
      {todo.length > 1 && (
        <div className="row-actions">
          <button className="btn secondary" onClick={() => approveCommands(todo.map((c) => c.step))}>
            Approve all {todo.length}
          </button>
        </div>
      )}
    </div>
  );
}

export async function decide(action, extra = {}) {
  const r = frs();
  try {
    await backend.DecideFlow(r.runId, { action, ...extra });
    r.decision = {};
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// --- sidebar ------------------------------------------------------------------------

function Sidebar({ st }) {
  const evs = S.events[st.id] || [],
    u = st.usage || {},
    ev = usageOf(evs);
  const pct = Math.round(runProgress(st) * 100); // the same measure as the simple view
  const [, tick] = useState(0);
  useEffect(() => {
    if (!LIVE.includes(st.status)) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [st.status]);
  const elapsed = (st.ended ? Date.parse(st.ended) : Date.now()) - Date.parse(st.started);
  const tokens = Math.max(u.input_tokens + u.output_tokens || 0, ev.in + ev.out);
  const M = ({ v, l }) => (
    <div className="fr-m">
      <b>{v}</b>
      <span>{l}</span>
    </div>
  );
  return (
    <aside className="fr-side" aria-label="Progress and metrics">
      <div className="fr-ring" style={{ "--p": pct }}>
        <span>
          <b>{["done"].includes(st.status) ? 100 : pct}%</b>
          <small>{STATUS_LABEL[st.status]}</small>
        </span>
      </div>
      {(st.items > 1 || st.passes > 1) && (
        <p className="muted fr-center">
          {st.passes > 1 && `Run ${st.pass} of ${st.passes}`}
          {st.passes > 1 && st.items > 1 && " · "}
          {st.items > 1 && `item ${st.item} of ${st.items}`}
        </p>
      )}
      <ul className="fr-mini">
        {workSteps(st).map((s) => (
          <li key={s.id} className={"s-" + s.status}>
            <span className="fr-dot" />
            {s.title}
            <span className="muted">{s.durationMs ? fmtMs(s.durationMs) : ""}</span>
          </li>
        ))}
      </ul>
      <h4>Metrics</h4>
      <div className="fr-metrics">
        <M v={fmtMs(Math.max(0, elapsed))} l="Elapsed" />
        <M v={fmtK(tokens)} l="Tokens" />
        <M
          v={
            isCopilot() ? u.premium_requests || ev.premium || 0 : "$" + (u.cost_usd || ev.usd || 0).toFixed(3)
          }
          l={isCopilot() ? "Premium requests" : "Cost"}
        />
        <M v={u.model_calls || ev.calls || 0} l="AI calls" />
        <M v={u.tool_calls || ev.tools || 0} l="Tool runs" />
        <M v={st.outputs?.length || 0} l="Outputs" />
      </div>
      <p className="muted fr-saves">
        Saves about <b>{st.minutesSaved * Math.max(1, (st.passes || 1) * (st.items || 1))} min</b> of manual
        work when it completes.
      </p>
    </aside>
  );
}

// --- screen ---------------------------------------------------------------------------

export function FlowRun() {
  const r = frs();
  useEffect(() => {
    if (S.ws) {
      loadAll();
      refreshExtensions();
      loadLayout();
    }
  }, [S.ws?.root]);
  usePoll(() => S.ws && loadAll(), FLOWRUN_POLL * 1000, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="the flow runner" />;
  const st = r.runId ? S.flowRuns[r.runId] : null;
  const runsOf = (id) => r.runs.filter((x) => x.workflow === id).slice(0, 6);
  return (
    <div className={"files frun" + (st ? " with-side" : "") + (S.runner ? " frun-runner" : "")}>
      {!S.runner && (
        <Pane k="flowrun" v="--pane-l" pane=".ftree" min={200} max={480} label="Resize the flow list" />
      )}
      {st && (
        <Pane
          k="flowrun-side"
          v="--pane-r"
          pane=".fr-side"
          edge="left"
          min={200}
          max={480}
          label="Resize the progress panel"
        />
      )}
      {!S.runner && (
        <nav className="ftree" aria-label="Flows and runs">
          <ListHead title="Run flows" count={r.flows?.length}>
            <RefreshButton
              label="Refresh the flows, their runs and schedules"
              every={FLOWRUN_POLL}
              onRefresh={loadAll}
            />
          </ListHead>
          <FlowFilterBar area="runner" list={r.flows} />
          {(() => {
            const groups = groupFlows(r.flows, flowFilter("runner"));
            if ((r.flows || []).length && !groups.length)
              return (
                <p className="muted" style={{ padding: "4px 8px" }}>
                  No flow matches.
                </p>
              );
            return groups.map((g) => (
              <FlowGroup
                key={g.key}
                area="runner"
                name={g.name}
                heading={g.heading}
                count={g.items.length}
                groups={groups.length}
              >
                {g.items.map((f) => (
                  <div key={f.id} className="fr-flow" {...dragProps("flows", f.id)}>
                    <button
                      className={"run-item" + (r.flowId === f.id && !r.runId ? " active" : "")}
                      onClick={() => {
                        r.runId = "";
                        loadFlow(f.id);
                      }}
                    >
                      <span className="run-item-top">
                        <b>{f.name}</b>
                      </span>
                      <small>
                        {f.steps} steps · {f.outputs} outputs
                      </small>
                      <FlowTagChips tags={f.tags} area="runner" />
                      {(() => {
                        const next = (r.triggers || [])
                          .filter((t) => t.flowId === f.id && t.trigger.enabled && t.upcoming?.length)
                          .map((t) => new Date(t.upcoming[0]))
                          .sort((a, b) => a - b)[0];
                        return next ? (
                          <small className="fr-next">
                            <span className="badge badge-sched">scheduled</span> next{" "}
                            {next.toLocaleString([], {
                              weekday: "short",
                              hour: "2-digit",
                              minute: "2-digit",
                            })}
                          </small>
                        ) : null;
                      })()}
                      {(r.queue || [])
                        .filter((q) => q.flow === f.id)
                        .map((q) => (
                          <small key={q.id} className="fr-queued">
                            <span className="badge">waiting</span>{" "}
                            <span title={q.why}>
                              {q.triggerName}
                              {q.attempt ? ` · retry ${q.attempt}` : ""}
                              {q.notBefore && new Date(q.notBefore) > new Date()
                                ? ` · at ${new Date(q.notBefore).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`
                                : " · when the flow is free"}
                            </span>{" "}
                            <button
                              className="link"
                              onClick={async (e) => {
                                e.stopPropagation();
                                await backend.CancelQueued(q.id).catch((x) => toast(errMsg(x), "error"));
                                loadAll();
                              }}
                            >
                              Cancel
                            </button>
                          </small>
                        ))}
                    </button>
                    {runsOf(f.id).map((x) => {
                      const live = S.flowRuns[x.id]?.status || x.state;
                      return (
                        <button
                          key={x.id}
                          className={"run-item fr-run-item" + (r.runId === x.id ? " active" : "")}
                          onClick={() => openRun(x.id)}
                        >
                          <span className="run-item-top">
                            <span title={new Date(x.started).toLocaleString()}>
                              {ago(x.started)}
                              {(x.kind === "flow-trial" || S.flowRuns[x.id]?.trial) && (
                                <span className="badge badge-info" style={{ marginLeft: 6 }}>
                                  trial
                                </span>
                              )}
                              {(x.trigger || S.flowRuns[x.id]?.options?.trigger) && (
                                <span
                                  className="badge badge-sched"
                                  style={{ marginLeft: 6 }}
                                  title={
                                    "Started by " +
                                    (x.triggerName || S.flowRuns[x.id]?.options?.trigger?.name || "a trigger")
                                  }
                                >
                                  {x.trigger || TAG[S.flowRuns[x.id]?.options?.trigger?.kind] || "trigger"}
                                </span>
                              )}
                            </span>
                            <Pill s={pill(live)} label={STATUS_LABEL[live] || live} />
                          </span>
                        </button>
                      );
                    })}
                  </div>
                ))}
              </FlowGroup>
            ));
          })()}
          {r.flows && !r.flows.length && (
            <EmptyState
              compact
              title="No flows yet"
              actions={[
                [
                  "Design a flow",
                  () => {
                    S.view = "flows";
                    update();
                  },
                  true,
                ],
                ["Try an example", () => openFlowGuide("")],
              ]}
            >
              A flow strings tools, AI steps and outputs together. Design one, or start from an example in the
              Flow step guide.
            </EmptyState>
          )}
        </nav>
      )}
      <div className="fview">
        {st ? <RunView st={st} /> : r.flow ? <Setup /> : <p className="muted">Choose a flow.</p>}
      </div>
      {st && <Sidebar st={st} />}
    </div>
  );
}

// Back / Forward: the run open in Run flows, or the flow chosen there (navigation.js)
registerPlace("flowrun", {
  key: () => (S.frun?.runId ? "run:" + S.frun.runId : S.frun?.flowId ? "flow:" + S.frun.flowId : ""),
  restore: (k) => {
    const [kind, id] = [k.slice(0, k.indexOf(":")), k.slice(k.indexOf(":") + 1)];
    if (kind === "run") return openRun(id);
    frs().runId = "";
    return loadFlow(id);
  },
});
