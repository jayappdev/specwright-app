// Run flows: running part of a flow again (core/flowrerun.go).
//   Run again from here   the steps before keep their results; what the step
//                         receives and the flow's inputs can be changed first
//   Edit this step        change a step by hand and try it (as Fix with AI)
//   Compare               two runs side by side, step by step
//   Try on an earlier run a flow version run on another version's run: only
//                         what it changed runs again
import { useEffect, useState } from "react";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { LIVE, frs, openRun, showFlowRun } from "../FlowRun.jsx";

export const canRerun = (st, s) => !LIVE.includes(st.status) && s.type !== "start";

// startTrial: a trial run that just started, shown in Run flows
export function startTrial(t, message) {
  const r = frs();
  S.flowRuns[t.id] = t;
  S.loaded[t.id] = true;
  S.events[t.id] ||= [];
  r.runs = [
    { id: t.id, workflow: t.flowId, state: t.status, started: t.started, kind: "flow-trial", usage: {} },
    ...(r.runs || []),
  ];
  r.fix = r.rerun = r.edit = null;
  r.runId = t.id;
  toast(message);
  update();
}

// --- Run again from here ---------------------------------------------------------------

export async function openRerun(st, s) {
  const r = frs();
  if (r.rerun?.runId === st.id && r.rerun?.stepId === s.id) {
    r.rerun = null;
    return update();
  }
  r.fix = r.edit = null;
  r.rerun = { runId: st.id, stepId: s.id, plan: null, error: "", received: {}, inputs: {}, busy: false };
  update();
  try {
    const plan = await backend.PlanRerun(st.id, s.id);
    if (r.rerun?.stepId !== s.id) return;
    r.rerun.plan = plan;
    r.rerun.received = Object.fromEntries(plan.received.map((x) => [x.id, x.output]));
    r.rerun.inputs = { ...plan.values };
  } catch (e) {
    r.rerun.error = errMsg(e);
  }
  update();
}

export function RerunPanel() {
  const r = frs(),
    x = r.rerun;
  if (!x) return null;
  const p = x.plan;
  const go = async () => {
    x.busy = true;
    update();
    try {
      // only what was changed is sent
      const received = Object.fromEntries(
        p.received
          .filter((i) => i.editable && x.received[i.id] !== i.output)
          .map((i) => [i.id, x.received[i.id]]),
      );
      const changedInputs = Object.entries(x.inputs).some(([k, v]) => v !== p.values[k]);
      const t = await backend.RerunFromStep({
        runId: x.runId,
        stepId: x.stepId,
        received,
        inputs: changedInputs ? x.inputs : null,
      });
      startTrial(t, `Running again from ${p.title}: the steps before it keep their results.`);
    } catch (e) {
      x.error = errMsg(e);
      x.busy = false;
      update();
    }
  };
  return (
    <div className="fr-fix fr-rerun" role="region" aria-label={"Run again from " + (p?.title || "this step")}>
      {!p && !x.error && <p className="muted">Loading…</p>}
      {p && (
        <>
          <p className="muted fl-help">
            <b>Keeps its result:</b> {p.reused.length ? p.reused.join(", ") : "nothing"}. <b>Runs again:</b>{" "}
            {p.rerun.join(", ")}. Nothing is saved to the flow.
          </p>
          {p.received.length > 0 && <h4 className="fr-rerun-h">What {p.title} receives</h4>}
          {p.received.map((i) => (
            <label key={i.id} className="field">
              <span className="muted">
                From <b>{i.title}</b>
                {!i.editable && " · runs again too, so it cannot be changed here"}
                {i.editable && x.received[i.id] !== i.output && (
                  <>
                    {" "}
                    · <span className="badge badge-info">changed</span>{" "}
                    <button
                      className="link"
                      onClick={() => {
                        x.received[i.id] = i.output;
                        update();
                      }}
                    >
                      undo
                    </button>
                  </>
                )}
              </span>
              <textarea
                rows={Math.min(10, Math.max(3, (x.received[i.id] || "").split("\n").length))}
                value={x.received[i.id] ?? ""}
                disabled={!i.editable}
                style={{ fontFamily: "var(--font-mono)" }}
                onChange={(e) => {
                  x.received[i.id] = e.target.value;
                  update();
                }}
              />
            </label>
          ))}
          {p.inputs.filter((i) => i.type !== "secret").length > 0 && (
            <details
              className="fr-rerun-inputs"
              open={Object.entries(x.inputs).some(([k, v]) => v !== p.values[k])}
            >
              <summary className="muted">
                The flow's inputs (the steps from here on read the new values)
              </summary>
              {p.inputs
                .filter((i) => i.type !== "secret")
                .map((i) => (
                  <label key={i.name} className="field">
                    <span className="muted">{i.label || i.name}</span>
                    <input
                      value={x.inputs[i.name] ?? ""}
                      onChange={(e) => {
                        x.inputs[i.name] = e.target.value;
                        update();
                      }}
                    />
                  </label>
                ))}
            </details>
          )}
          <div className="row-actions">
            <button className="btn primary" disabled={x.busy} onClick={go}>
              <Icon.run /> {x.busy ? "Starting…" : "Run again from " + p.title}
            </button>
            <button className="btn secondary" onClick={() => ((r.rerun = null), update())}>
              Cancel
            </button>
          </div>
        </>
      )}
      {x.error && <div className="banner error">{x.error}</div>}
    </div>
  );
}

// --- Edit this step --------------------------------------------------------------------

// the settings a person edits by hand, by kind of step: [field, label, rows]
const EDITABLE = [
  ["title", "Title", 1],
  ["prompt", "Prompt", 6],
  ["instructions", "Instructions", 4],
  ["template", "Template", 5],
  ["expr", "Expression or command", 3],
  ["request", "What to ask the tool for", 3],
];

export function openEdit(st, s) {
  const r = frs();
  if (r.edit?.runId === st.id && r.edit?.stepId === s.id) {
    r.edit = null;
    return update();
  }
  const trialNode = st.trial?.steps?.find((x) => x.id === s.id)?.node;
  const n = trialNode || (r.flow?.nodes || []).find((x) => x.id === s.id);
  if (!n) return toast("This step is no longer in the flow.", "error");
  r.fix = r.rerun = null;
  r.edit = {
    runId: st.id,
    stepId: s.id,
    node: JSON.parse(JSON.stringify(n)),
    was: n,
    error: "",
    busy: false,
  };
  update();
}

export function EditStepPanel() {
  const r = frs(),
    x = r.edit;
  if (!x) return null;
  const n = x.node;
  const fields = EDITABLE.filter(([k]) => k === "title" || typeof x.was[k] === "string" || n[k]);
  const args = n.args && typeof n.args === "object" ? Object.entries(n.args) : [];
  const changed = JSON.stringify(n) !== JSON.stringify(x.was);
  const go = async () => {
    x.busy = true;
    update();
    try {
      const t = await backend.TryStepEdit(x.runId, n);
      startTrial(t, "Trying your change: the steps before it keep this run's results.");
    } catch (e) {
      x.error = errMsg(e);
      x.busy = false;
      update();
    }
  };
  return (
    <div className="fr-fix fr-edit" role="region" aria-label="Edit this step">
      <p className="muted fl-help">
        Change this step and try it: the steps before it keep this run's results, this step and the ones after
        it run again. Nothing is saved until you apply it or save it as a new version.
      </p>
      {fields.map(([k, label, rows]) => (
        <label key={k} className="field">
          <span className="muted">{label}</span>
          {rows === 1 ? (
            <input value={n[k] || ""} onChange={(e) => ((n[k] = e.target.value), update())} />
          ) : (
            <textarea
              rows={rows}
              value={n[k] || ""}
              style={k === "title" ? undefined : { fontFamily: "var(--font-mono)" }}
              onChange={(e) => ((n[k] = e.target.value), update())}
            />
          )}
        </label>
      ))}
      {args.length > 0 && (
        <>
          <h4 className="fr-rerun-h">Arguments</h4>
          {args.map(([k, v]) => (
            <label key={k} className="field">
              <span className="muted">{k}</span>
              <input
                value={typeof v === "string" ? v : JSON.stringify(v)}
                style={{ fontFamily: "var(--font-mono)" }}
                onChange={(e) => {
                  n.args = { ...n.args, [k]: e.target.value };
                  update();
                }}
              />
            </label>
          ))}
        </>
      )}
      <p className="muted fl-help">
        Other settings (the step's kind, its tool, connections) change in the designer.
      </p>
      <div className="row-actions">
        <button className="btn primary" disabled={x.busy || !changed} onClick={go}>
          <Icon.run /> {x.busy ? "Starting…" : "Try the change"}
        </button>
        <button className="btn secondary" onClick={() => ((r.edit = null), update())}>
          Cancel
        </button>
      </div>
      {x.error && <div className="banner error">{x.error}</div>}
    </div>
  );
}

// --- Compare two runs ------------------------------------------------------------------

export function openCompare(a, b) {
  S.compare = { a, b };
  update();
}

async function runState(id) {
  if (!S.flowRuns[id]) S.flowRuns[id] = await backend.FlowRun(id);
  return S.flowRuns[id];
}

export function CompareDialog() {
  const c = S.compare;
  const [runs, setRuns] = useState(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!c) return;
    setRuns(null);
    setErr("");
    Promise.all([runState(c.a), runState(c.b)])
      .then(setRuns)
      .catch((e) => setErr(errMsg(e)));
  }, [c?.a, c?.b]);
  if (!c) return null;
  const close = () => {
    S.compare = null;
    update();
  };
  const [was, now] = runs || [];
  const ids = now
    ? [...new Set([...now.steps.map((s) => s.id), ...(was?.steps || []).map((s) => s.id)])]
    : [];
  const t = now?.trial;
  const label = (st) => `${st.flowName}${st.flowId !== was?.flowId || st === now ? "" : ""}`;
  return (
    <div className="modal-backdrop dlg-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="dlg fr-compare" role="dialog" aria-modal="true" aria-label="Compare two runs">
        <div className="fr-compare-head">
          <h3>Compare runs</h3>
          <button className="icon-btn" aria-label="Close" onClick={close}>
            <Icon.x />
          </button>
        </div>
        {err && <div className="banner error">{err}</div>}
        {!runs && !err && <p className="muted">Loading…</p>}
        {runs && (
          <>
            <div className="fr-compare-grid fr-compare-cols">
              <span />
              <b>
                Earlier · {label(was)} · {new Date(was.started).toLocaleString()}
              </b>
              <b>
                This run · {label(now)} · {new Date(now.started).toLocaleString()}
              </b>
            </div>
            <div className="fr-compare-body">
              {ids.map((id) => {
                const a = was.steps.find((s) => s.id === id),
                  b = now.steps.find((s) => s.id === id);
                if ((a || b)?.type === "start") return null;
                const edited = t?.edited?.includes(id),
                  reused = t?.reused?.includes(id) && !edited,
                  changedStep =
                    t?.changed?.includes(id) || t?.steps?.some((x) => x.id === id) || t?.from === id;
                const same = a && b && (a.output || "") === (b.output || "") && a.status === b.status;
                return (
                  <div key={id} className={"fr-compare-grid fr-compare-row" + (same ? " same" : "")}>
                    <div className="fr-compare-step">
                      <b>{(b || a).title}</b>
                      {changedStep && <span className="badge badge-info">changed</span>}
                      {edited && <span className="badge badge-info">changed by hand</span>}
                      {reused && <span className="badge">reused</span>}
                      {!a && <span className="badge badge-info">new</span>}
                      {!b && <span className="badge">removed</span>}
                      {a && b && !reused && !edited && (
                        <span className={"badge " + (same ? "" : "badge-warn")}>
                          {same ? "same" : "different"}
                        </span>
                      )}
                    </div>
                    <StepCell s={a} />
                    <StepCell s={b} />
                  </div>
                );
              })}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function StepCell({ s }) {
  if (!s) return <div className="fr-compare-cell muted">—</div>;
  return (
    <div className={"fr-compare-cell" + (s.status === "failed" ? " failed" : "")}>
      {s.status !== "done" && <span className="badge">{s.status}</span>}
      {s.error ? <pre className="fr-compare-out err">{s.error}</pre> : null}
      {s.output ? (
        <pre className="fr-compare-out">{s.output}</pre>
      ) : (
        !s.error && <span className="muted">no output</span>
      )}
    </div>
  );
}

// --- Try a flow version on an earlier run ---------------------------------------------

export async function openTryVersion(flowId, name) {
  S.tryVersion = { flowId, name: name || flowId, runs: null, sel: "", busy: false, error: "" };
  update();
  try {
    const runs = (await backend.VersionRuns(flowId)) || [];
    if (S.tryVersion?.flowId !== flowId) return;
    S.tryVersion.runs = runs.filter((x) => x.workflow !== flowId);
    S.tryVersion.sel = S.tryVersion.runs[0]?.id || "";
  } catch (e) {
    S.tryVersion.error = errMsg(e);
  }
  update();
}

export function TryVersionDialog() {
  const k = S.tryVersion;
  if (!k) return null;
  const close = () => {
    S.tryVersion = null;
    update();
  };
  const go = async () => {
    k.busy = true;
    update();
    try {
      const t = await backend.TryVersionOnRun(k.flowId, k.sel);
      S.tryVersion = null;
      showFlowRun(t.id);
      S.flowRuns[t.id] = t;
      toast("Trying the new version: steps it did not change reuse the earlier run's results.");
    } catch (e) {
      k.error = errMsg(e);
      k.busy = false;
      update();
    }
  };
  return (
    <div className="modal-backdrop dlg-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="dlg fr-tryversion" role="dialog" aria-modal="true" aria-label="Try on an earlier run">
        <h3>Try {k.name} on an earlier run</h3>
        <p className="dlg-msg">
          It runs with that run's inputs. Steps this version did not change keep that run's results; the
          changed steps and the ones after them run again. Then compare the two runs.
        </p>
        {k.runs === null && !k.error && <p className="muted">Loading…</p>}
        {k.runs?.length === 0 && (
          <p className="muted">No finished runs of the earlier versions yet: run one of them first.</p>
        )}
        {k.runs?.length > 0 && (
          <div className="fr-tryversion-list" role="radiogroup" aria-label="Earlier runs">
            {k.runs.slice(0, 20).map((x) => (
              <label key={x.id} className="check">
                <input
                  type="radio"
                  name="tv-run"
                  checked={k.sel === x.id}
                  onChange={() => ((k.sel = x.id), update())}
                />{" "}
                <span>
                  <b>{x.title || x.workflow}</b>{" "}
                  <span className="muted">
                    · {new Date(x.started).toLocaleString()} · {x.state === "done" ? "finished" : "failed"}
                  </span>
                </span>
              </label>
            ))}
          </div>
        )}
        {k.error && <div className="banner error">{k.error}</div>}
        <div className="row-actions">
          <button className="btn primary" disabled={!k.sel || k.busy} onClick={go}>
            <Icon.run /> {k.busy ? "Starting…" : "Try it"}
          </button>
          <button className="btn secondary" onClick={close}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}

// back to the run a trial started from
export const backToBase = (t) => openRun(t.against || t.baseRun);
