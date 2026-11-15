// Run flows: a run in progress or finished — the review pause, the steps and
// their timing, outputs.
import { useEffect, useState } from "react";
import { ContentView } from "../../ContentView.jsx";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg, fmtMs } from "../../store.js";
import { Icon, Pill } from "../../ui.jsx";
import { StepIcon } from "../Flows.jsx";
import { RunPanel } from "../../components/RunPanel.jsx";
import { continueInPlayground } from "../Playground.jsx";
import { ResultsLink } from "../../RunResults.jsx";
import { AiProgress } from "../../AiProgress.jsx";
import { FileLink } from "../../FileLinks.jsx";
import { CopyButton } from "../../CopyButton.jsx";
import { REVIEW_HINT } from "./Setup.jsx";
import { StepFixPanel, TrialBanner, canFix, openFix } from "./StepFix.jsx";
import { LIVE, STATUS_LABEL, decide, frs, pill } from "../FlowRun.jsx";

// --- run view -----------------------------------------------------------------------

function Review({ st }) {
  const r = frs(),
    rv = st.review,
    d = r.decision,
    f = r.flow;
  const node = f?.nodes.find((n) => n.id === rv.step);
  const [tab, setTab] = useState(r.tab);
  useEffect(() => {
    d.edit = rv.output;
    d.node ||= node ? JSON.parse(JSON.stringify(node)) : null;
    update();
  }, [rv.step, rv.output]);
  const setD = (k, v) => {
    d[k] = v;
    update();
  };
  const go = (t) => {
    r.tab = t;
    setTab(t);
  };
  const saveBox = (what) => (
    <label className="check">
      <input type="checkbox" checked={!!d.save} onChange={(e) => setD("save", e.target.checked)} /> Save to
      the flow for next time <span className="muted">({what}); otherwise just this run</span>
    </label>
  );
  return (
    <div className="card fr-review" role="region" aria-label="Review">
      <div className="card-head">
        <span className="fr-review-dot" />
        <h3>Review: {rv.title}</h3>
        <span className="badge badge-warn">waiting for you</span>
      </div>
      <p className="wf-desc">
        Check the result. Continue, stop the flow, or improve it first: the improved result goes on to the
        next steps.
      </p>
      <ContentView
        title={rv.title}
        text={rv.output}
        files={(() => {
          const s = st.steps.find((x) => x.id === rv.step);
          return [
            s?.inputFile && { label: "input", path: s.inputFile },
            s?.outputFile && { label: "output", path: s.outputFile },
          ].filter(Boolean);
        })()}
      />
      <div className="row-actions">
        <button className="btn primary" onClick={() => decide("continue")}>
          <Icon.check /> Looks good, continue
        </button>
        <button className="btn secondary" onClick={() => decide("no-more-reviews")}>
          Continue without more reviews
        </button>
        <button className="btn secondary danger-text" onClick={() => decide("stop")}>
          <Icon.x /> Stop the flow
        </button>
      </div>
      <div className="tabs" role="tablist" style={{ marginTop: 12 }}>
        {[
          ["refine", "Refine with AI"],
          ["command", "Run a command"],
          ["edit", "Edit the result"],
          ["retry", "Change settings and run again"],
        ].map(([k, l]) => (
          <button key={k} role="tab" className="tab" aria-selected={tab === k} onClick={() => go(k)}>
            {l}
          </button>
        ))}
      </div>
      <p className="muted fr-tab-hint">{REVIEW_HINT[tab]}</p>
      <div className="fr-iter">
        {tab === "refine" && (
          <>
            <textarea
              rows={3}
              value={d.prompt || ""}
              placeholder={
                rv.type === "ai"
                  ? "e.g. Shorter, and add a risks section"
                  : "e.g. Keep only the failing tests and group them by package"
              }
              onChange={(e) => setD("prompt", e.target.value)}
            />
            {saveBox(rv.type === "ai" ? "added to the step's prompt" : "as an AI step after this one")}
            <div className="row-actions">
              <button
                className="btn primary"
                disabled={!d.prompt?.trim()}
                onClick={() => decide("refine", { prompt: d.prompt, save: !!d.save })}
              >
                <Icon.spark /> Refine
              </button>
            </div>
          </>
        )}
        {tab === "command" && (
          <>
            <input
              value={d.command || ""}
              placeholder="e.g. jq '.items[] | .name'   or   sort -u   or   grep -v DEBUG"
              style={{ fontFamily: "var(--font-mono)" }}
              onChange={(e) => setD("command", e.target.value)}
            />
            <small className="muted">
              Runs in the project with the result on stdin; its output replaces the result.
            </small>
            {saveBox("as a command step after this one")}
            <div className="row-actions">
              <button
                className="btn primary"
                disabled={!d.command?.trim()}
                onClick={() => decide("command", { command: d.command, save: !!d.save })}
              >
                Run command
              </button>
            </div>
          </>
        )}
        {tab === "edit" && (
          <>
            <textarea rows={8} value={d.edit ?? ""} onChange={(e) => setD("edit", e.target.value)} />
            <div className="row-actions">
              <button className="btn primary" onClick={() => decide("edit", { output: d.edit })}>
                Use this result
              </button>
              <span className="muted">For this run only.</span>
            </div>
          </>
        )}
        {tab === "retry" && d.node && (
          <>
            {d.node.type === "tool" && (
              <label className="field">
                <span className="muted">
                  What the step should do (AI builds the arguments; leave empty to use the values below)
                </span>
                <textarea
                  rows={3}
                  value={d.node.instruction || ""}
                  onChange={(e) => {
                    d.node.instruction = e.target.value;
                    update();
                  }}
                />
              </label>
            )}
            {d.node.type === "tool" &&
              Object.keys(d.node.args || {}).map((k) => (
                <label key={k} className="field">
                  <span className="muted">
                    {k}
                    {d.node.instruction ? " (empty: AI fills it)" : ""}
                  </span>
                  <textarea
                    rows={/sql|query|body|json/i.test(k) ? 3 : 1}
                    value={d.node.args[k]}
                    style={{ fontFamily: "var(--font-mono)" }}
                    onChange={(e) => {
                      d.node.args[k] = e.target.value;
                      update();
                    }}
                  />
                </label>
              ))}
            {d.node.type === "ai" && (
              <label className="field">
                <span className="muted">Prompt</span>
                <textarea
                  rows={5}
                  value={d.node.prompt || ""}
                  onChange={(e) => {
                    d.node.prompt = e.target.value;
                    update();
                  }}
                />
              </label>
            )}
            {d.node.type === "transform" && (
              <>
                {d.node.expr != null && (
                  <label className="field">
                    <span className="muted">Expression</span>
                    <input
                      value={d.node.expr || ""}
                      onChange={(e) => {
                        d.node.expr = e.target.value;
                        update();
                      }}
                    />
                  </label>
                )}
                {d.node.template != null && (
                  <label className="field">
                    <span className="muted">Template / instruction</span>
                    <textarea
                      rows={4}
                      value={d.node.template || ""}
                      onChange={(e) => {
                        d.node.template = e.target.value;
                        update();
                      }}
                    />
                  </label>
                )}
              </>
            )}
            {d.node.type === "merge" && (
              <label className="field">
                <span className="muted">Template</span>
                <textarea
                  rows={4}
                  value={d.node.template || ""}
                  onChange={(e) => {
                    d.node.template = e.target.value;
                    update();
                  }}
                />
              </label>
            )}
            {d.node.type === "output" && (
              <label className="field">
                <span className="muted">Content</span>
                <textarea
                  rows={4}
                  value={d.node.template || ""}
                  onChange={(e) => {
                    d.node.template = e.target.value;
                    update();
                  }}
                />
              </label>
            )}
            {saveBox("these settings")}
            <div className="row-actions">
              <button
                className="btn primary"
                onClick={() => decide("retry", { node: d.node, save: !!d.save })}
              >
                Run the step again
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function Steps({ st }) {
  const [open, setOpen] = useState({});
  const f = frs().flow;
  return (
    <div className="card">
      <div className="card-head">
        <Icon.workflow />
        <h3>Steps</h3>
        {st.items > 1 || st.passes > 1 ? (
          <span className="badge">
            {st.passes > 1 ? `run ${st.pass}/${st.passes}` : ""}
            {st.passes > 1 && st.items > 1 ? " · " : ""}
            {st.items > 1 ? `item ${st.item}/${st.items}` : ""}
          </span>
        ) : null}
        {st.itemLabel && <span className="muted fr-item">{st.itemLabel}</span>}
      </div>
      <ol className="fr-steps">
        {st.steps.map((s) => {
          const n = f?.nodes.find((x) => x.id === s.id) || { type: s.type, tool: s.tool };
          const can = !!(s.output || s.error || s.args);
          return (
            <li key={s.id} className={"fr-step s-" + s.status}>
              <button
                className="fr-step-head"
                disabled={!can}
                onClick={() => setOpen({ ...open, [s.id]: !open[s.id] })}
                aria-expanded={!!open[s.id]}
              >
                <StepIcon n={n} tools={S.ws?.extensions || []} size={28} />
                <span className="fr-step-title">
                  <b>{s.title}</b>
                  <small>
                    {s.type === "tool"
                      ? String(s.tool || "").replace(/^builtin:(.*)$/, "$1 · built-in") +
                        (s.why ? " · arguments by AI" : n.instruction ? " · AI builds the arguments" : "")
                      : s.type}
                    {s.iterations
                      ? s.type === "loop"
                        ? ` · ${s.status === "running" ? `item ${s.iteration || 0} of ${s.iterations}` : `${s.iterations} item${s.iterations === 1 ? "" : "s"}`}`
                        : ` · ↻ item ${s.iteration}/${s.iterations}`
                      : s.attempts > 1
                        ? ` · ${s.attempts} attempts`
                        : ""}
                    {s.failedItems ? ` · ${s.failedItems} failed` : ""}
                    {s.path ? ` · → ${s.path}` : ""}
                    {s.notes?.length ? " · " + s.notes[s.notes.length - 1] : ""}
                  </small>
                </span>
                {s.status === "running" ? (
                  <span className="spinner" />
                ) : (
                  <Pill s={pill(s.status)} label={STATUS_LABEL[s.status] || s.status} />
                )}
                <span className="muted fr-dur">{s.durationMs ? fmtMs(s.durationMs) : ""}</span>
              </button>
              {open[s.id] && (
                <div className="fr-step-body">
                  {s.error && (
                    <div className={"banner " + (s.status === "skipped" ? "" : "error")}>{s.error}</div>
                  )}
                  {s.args && Object.keys(s.args).length > 0 && (
                    <div className="fr-args">
                      <div className="muted">
                        {s.why ? (
                          <>
                            <Icon.spark /> Arguments chosen by AI · {s.why}
                          </>
                        ) : (
                          "Arguments"
                        )}
                      </div>
                      {Object.entries(s.args).map(([k, v]) => (
                        <div key={k} className="fr-arg">
                          <code className="inline-code" data-nofile>
                            {k}
                          </code>
                          <pre className="code fr-pre">
                            {typeof v === "string" ? v : JSON.stringify(v, null, 2)}
                          </pre>
                        </div>
                      ))}
                    </div>
                  )}
                  {(s.output || s.outputFile) && (
                    <ContentView
                      title={s.title}
                      text={s.output}
                      files={[
                        s.inputFile && { label: "input", path: s.inputFile },
                        s.outputFile &&
                          s.args && { label: "args.json", path: s.outputFile.replace(/[^/]+$/, "args.json") },
                        s.outputFile && { label: "output", path: s.outputFile },
                      ].filter(Boolean)}
                    />
                  )}
                  {s.truncated && (
                    <small className="muted">Shortened for display; the output file has all of it.</small>
                  )}
                </div>
              )}
              {canFix(st, s) && (
                <div className="fr-step-actions">
                  <button className="link" onClick={() => openFix(st, s)}>
                    <Icon.spark /> {s.status === "failed" ? "Fix with AI" : "Improve"}
                  </button>
                  {st.trial?.steps?.some((x) => x.id === s.id) && (
                    <span className="badge badge-info" title={st.trial.steps.find((x) => x.id === s.id)?.why}>
                      changed in this trial
                    </span>
                  )}
                </div>
              )}
              {frs().fix?.runId === st.id && frs().fix?.stepId === s.id && <StepFixPanel st={st} s={s} />}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

export function Outputs({ st }) {
  if (!st.outputs?.length) return null;
  return (
    <div className="card">
      <div className="card-head">
        <Icon.files />
        <h3>Outputs</h3>
        <span className="badge">{st.outputs.length}</span>
      </div>
      {st.outputs.map((o, k) => (
        <details key={k} className="fr-out" open={o.show || k === st.outputs.length - 1}>
          <summary>
            <b>{o.title}</b> <span className="badge">{o.format.toUpperCase()}</span>
            {o.item && <span className="muted"> · {o.item}</span>}
            {o.saved?.map((p) => (
              <span key={p}>
                {" "}
                · <FileLink path={p}>{p}</FileLink>
              </span>
            ))}
            {o.copied && <span className="badge badge-ok">copied</span>}
            {o.tools?.map((t) => (
              <span key={t} className="badge badge-info">
                sent to {t}
              </span>
            ))}
            <CopyButton text={o.content} format={o.format === "txt" ? "" : o.format} />
          </summary>
          {o.errors?.map((e, j) => (
            <div key={j} className="banner error">
              {e}
            </div>
          ))}
          <ContentView
            title={o.title}
            text={o.content}
            format={o.format === "txt" ? undefined : o.format}
            files={(o.saved || []).map((p) => ({ label: p.split("/").pop(), path: p }))}
          />
        </details>
      ))}
    </div>
  );
}

export function RunView({ st }) {
  const r = frs(),
    evs = S.events[st.id] || [],
    live = LIVE.includes(st.status);
  return (
    <>
      <header className="view-head">
        <h1>{st.flowName}</h1>
        <Pill s={pill(st.status)} label={STATUS_LABEL[st.status] || st.status} />
        <div className="head-controls">
          {live && (
            <button
              className="btn secondary danger-text"
              onClick={() => backend.StopFlowRun(st.id).catch((e) => toast(errMsg(e), "error"))}
            >
              <Icon.x /> Stop
            </button>
          )}
          <button
            className="btn secondary"
            onClick={() => {
              S.pg ||= { prompt: "", tools: true, model: "", runs: [], sel: "", loaded: false };
              S.pg.loaded = false;
              S.pg.sel = st.id;
              S.view = "playground";
              update();
            }}
          >
            Watch in the playground
          </button>
          {!live && (
            <button
              className="btn secondary"
              title="Ask follow-up questions about this run's results"
              onClick={() => continueInPlayground([st.id])}
            >
              <Icon.history /> Continue in the playground
            </button>
          )}
          {!live && (
            <button
              className="btn primary"
              onClick={() => {
                r.runId = "";
                update();
              }}
            >
              Run again
            </button>
          )}
          {!live && (
            <button
              className="btn secondary fr-switch"
              onClick={() => {
                r.runId = "";
                update();
              }}
            >
              All flows
            </button>
          )}
        </div>
      </header>
      {st.status === "scheduled" && (
        <div className="banner">
          Starts at {new Date(st.options.startAt).toLocaleString()}. Keep the app open; you can stop it before
          then.
        </div>
      )}
      {st.trial && <TrialBanner st={st} />}
      {st.error && st.status !== "done" && (
        <div className={"banner " + (st.status === "stopped" ? "" : "error")}>{st.error}</div>
      )}
      {!live && <ResultsLink id={st.id} ended />}
      {st.changes?.length > 0 && (
        <div className="banner fr-saved">
          <Icon.check /> Saved to the flow: {st.changes.join("; ")}
        </div>
      )}
      {st.status === "review" && st.review && <Review st={st} />}
      {st.status === "running" && <AiProgress evs={evs} />}
      {!live && st.outputs?.length > 0 && <Outputs st={st} />}
      <Steps st={st} />
      {(live || !st.outputs?.length) && <Outputs st={st} />}
      <details className="fr-activity">
        <summary className="muted">Activity and debug log · {evs.length} events</summary>
        <RunPanel
          st={{ id: "flow", cmd: st.flowName, status: live ? "running" : st.status, lastRun: st.id }}
          evs={evs}
          running={live}
        />
      </details>
    </>
  );
}
