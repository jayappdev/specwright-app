// RunPanel: a run's activity, files changed and debug events, with the
// timeline rows (ActivityRow). Used by the workflow, History, Playground and Run flows.
import { Linkify } from "../FileLinks.jsx";
import { AiProgress } from "../AiProgress.jsx";
import { EventIcon } from "../EventIcon.jsx";
import { ResultsLink } from "../RunResults.jsx";
import {
  S,
  update,
  toast,
  usageOf,
  stepUsage,
  isCopilot,
  fmtK,
  fmtMs,
  fmtCredits,
  saveText,
} from "../store.js";
import { Pill, Chip, Stat } from "../ui.jsx";

const EV_TONE = {
  flow: "--activity-accent",
  secret: "--warn",
  run: "--muted",
  prompt: "--syn-type",
  model: "--info",
  tool: "--syn-function",
  fs: "--ok",
  gate: "--warn",
  loop: "--error",
  budget: "--error",
  error: "--error",
  ext: "--syn-property",
};

const DBG_FILTERS = [
  ["all", "All"],
  ["model", "Model"],
  ["tool", "Tools"],
  ["fs", "Files"],
  ["gate", "Gates"],
  ["flow", "Flow"],
  ["run", "Run"],
  ["ext", "Extensions"],
];

const sec = (ms) =>
  ms == null ? "" : ms < 1000 ? "+" + ms + "ms" : "+" + (ms / 1000).toFixed(ms < 10000 ? 1 : 0) + "s";
const shortTool = (t) => String(t || "").replace(/^mcp__agentbridge__|^agentbridge-/, "");

// One line of the Activity tab: an icon, what happened, and when.
function Act({ e, kind = "", children, extra }) {
  return (
    <div className={"act " + kind}>
      <EventIcon e={e} />
      <div className="act-body">{children}</div>
      {extra}
      <span className="act-time" title={e.ts ? new Date(e.ts).toLocaleTimeString() : ""}>
        {sec(e.dt)}
      </span>
    </div>
  );
}

function ActivityRow({ e, evs }) {
  switch (e.type) {
    case "tool.call": {
      const res = evs.find(
        (x) => x.type === "tool.result" && x.seq > e.seq && (e.id ? x.id === e.id : x.tool === e.tool),
      );
      const t = shortTool(e.tool),
        ext = /extension_managed$/.test(t);
      const label = ext ? "project tool " + (e.args?.name || "") : t;
      const arg = (e.summary || "")
        .replace(e.tool || "", "")
        .replace(/^extension_managed\s*/, "")
        .trim();
      return (
        <Act
          e={e}
          kind={"act-tool" + (res && res.ok === false ? " is-error" : "")}
          extra={
            <span className={"act-state " + (!res ? "run" : res.ok === false ? "bad" : "good")}>
              {!res ? <span className="spinner" /> : res.ok === false ? "failed" : "ok"}
              {res?.duration_ms != null ? " · " + fmtMs(res.duration_ms) : ""}
            </span>
          }
        >
          <b className="act-tool-name">{label}</b>{" "}
          <span className="act-arg">
            <Linkify text={arg} />
          </span>
        </Act>
      );
    }
    case "fs.write":
    case "fs.edit":
    case "fs.delete":
      return (
        <Act e={e} kind="act-fs">
          <Linkify text={e.summary} />{" "}
          {e.added != null && (
            <span className="diffstat">
              <b className="add">+{e.added}</b>
              {e.removed ? (
                <>
                  {" "}
                  <b className="del">−{e.removed}</b>
                </>
              ) : null}
            </span>
          )}
        </Act>
      );
    case "model.text":
      return (
        <Act e={e} kind="act-say">
          <div className="act-text">
            <Linkify text={e.text} />
          </div>
        </Act>
      );
    case "model.request":
      return (
        <Act e={e} kind="act-quiet">
          Asked {e.model || "the model"}
          {e.input_tokens ? <span className="muted"> · {fmtK(e.input_tokens)} tokens in context</span> : null}
        </Act>
      );
    case "gate.wait":
      return (
        <Act e={e} kind={"act-gate" + (e.gate === "loop" ? " bad" : "")}>
          {e.summary}
        </Act>
      );
    case "gate.approved":
      return (
        <Act e={e} kind="act-good">
          {e.summary || "Approved"}
        </Act>
      );
    case "gate.changes":
      return (
        <Act e={e} kind="act-gate">
          {e.summary || `Changes requested${e.feedback ? ": " + e.feedback : ""}. Rerunning…`}
        </Act>
      );
    case "gate.rejected":
      return (
        <Act e={e} kind="act-bad">
          {e.summary || "Rejected. The workflow stops here."}
        </Act>
      );
    case "loop.detected":
    case "budget.exceeded":
    case "error":
      return (
        <Act e={e} kind="act-bad">
          <Linkify text={e.summary} />
        </Act>
      );
    case "run.retry":
      return (
        <Act e={e} kind="act-gate">
          {e.summary}
        </Act>
      );
    case "ext.result":
      return (
        <Act e={e} kind="act-fs">
          Extension {e.name}: exit {e.exit_code} · {fmtMs(e.duration_ms || 0)}
        </Act>
      );
    case "secret.request":
      return (
        <Act e={e} kind="act-gate">
          Waiting for credentials: {e.summary}
        </Act>
      );
    case "secret.resolved":
      return (
        <Act e={e} kind="act-quiet">
          Credentials {e.summary}
        </Act>
      );
    case "flow.loop":
      return (
        <Act e={e} kind="act-quiet act-loop">
          ↻ {e.summary}
        </Act>
      );
    case "flow.step":
      return e.status === "running" ? null : (
        <Act e={e} kind={e.status === "failed" ? "act-bad" : "act-step"}>
          <b>{e.summary}</b>
          {e.duration_ms != null && <span className="muted"> · {fmtMs(e.duration_ms)}</span>}
        </Act>
      );
    case "flow.review":
      return (
        <Act e={e} kind="act-gate">
          <b>{e.summary}</b>
        </Act>
      );
    case "flow.output":
      return (
        <Act e={e} kind="act-step">
          Output: {e.summary}
          {(e.saved || []).map((p) => (
            <span key={p}>
              {" "}
              → <Linkify text={p} />
            </span>
          ))}
        </Act>
      );
    case "flow.saved":
      return (
        <Act e={e} kind="act-good">
          {e.summary}
        </Act>
      );
    case "flow.pass":
      return (
        <div className="row marker run-marker">
          <span className="marker-line" />
          <span className="marker-text">{e.summary}</span>
          <span className="marker-line" />
        </div>
      );
    case "flow.scheduled":
      return (
        <Act e={e} kind="act-quiet">
          {e.summary}
        </Act>
      );
    case "run.start":
      return (
        <div className="row marker run-marker">
          <span className="marker-line" />
          <span className="marker-text">
            {e.summary}
            {e.model ? " · " + e.model : ""}
          </span>
          <span className="marker-line" />
        </div>
      );
    case "run.usage":
      return (
        <Act e={e} kind="act-quiet act-usage">
          {e.summary}
        </Act>
      );
    // the AI a tool used itself (its own claude / copilot calls)
    case "model.usage":
      return e.source === "tool" ? (
        <Act e={e} kind="act-usage act-tool-ai">
          <Linkify text={e.summary} />
          {e.model ? <span className="muted"> · {e.model}</span> : null}
        </Act>
      ) : null;
    case "run.end":
      return e.state === "cancelled" || e.state === "stopped" ? (
        <Act e={e} kind="act-bad">
          {e.state === "stopped" ? "Stopped" : "Cancelled"}
        </Act>
      ) : e.state === "failed" ? (
        <Act e={e} kind="act-bad">
          Failed{e.reason ? ": " + e.reason : ""}
        </Act>
      ) : (
        <Act e={e} kind="act-good">
          Finished
        </Act>
      );
  }
  return null;
}

export function RunPanel({ st, evs, running }) {
  const u = usageOf(evs),
    tab = S.run.tab;
  if (!evs.length)
    return (
      <div className="transcript" style={{ minHeight: 160 }}>
        <div className="transcript-inner">
          <div className="empty">
            <h3>Not run yet</h3>
            <p>
              Collect inputs, add instructions, then run <code className="inline-code">{st.cmd}</code>.
              Activity, tokens, credits and files appear here live.
            </p>
          </div>
        </div>
      </div>
    );
  const filter = S.run.dbgFilter,
    shown = evs.filter((e) => filter === "all" || e.type.startsWith(filter + "."));
  const lastReq = [...evs].reverse().find((e) => e.type === "model.request");
  const ctxPct = Math.min(100, Math.round((lastReq?.input_tokens || 0) / 2000));
  const jsonl = () => evs.map((e) => JSON.stringify(e)).join("\n");
  const setTab = (v) => {
    S.run.tab = v;
    update();
  };
  let body;
  if (tab === "activity")
    body = (
      <div className="transcript" style={{ minHeight: 200 }}>
        <div className="transcript-inner">
          {/* seq can repeat when stored and live events overlap: the position keeps keys unique */}
          {evs.map((e, i) => (
            <ActivityRow key={`${e.seq}-${i}`} e={e} evs={evs} />
          ))}
          {running && <AiProgress evs={evs} compact />}
        </div>
      </div>
    );
  else if (tab === "files")
    body = u.files.length ? (
      <table className="table">
        <thead>
          <tr>
            <th>File</th>
            <th>Change</th>
            <th>Lines</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {u.files.map((f) => (
            <tr key={f.path}>
              <td>
                <code>{f.path}</code>
              </td>
              <td>
                <span
                  className={
                    "badge " +
                    (f.op === "created" ? "badge-ok" : f.op === "deleted" ? "badge-warn" : "badge-info")
                  }
                >
                  {f.op}
                </span>
              </td>
              <td className="diffstat">
                <b className="add">+{f.added}</b>
                {f.removed ? (
                  <>
                    {" "}
                    <b className="del">−{f.removed}</b>
                  </>
                ) : null}
              </td>
              <td>
                {f.op !== "deleted" && (
                  <button
                    className="link"
                    onClick={() => {
                      S.files.sel = f.path;
                      S.view = "files";
                      update();
                    }}
                  >
                    Open
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    ) : (
      <p className="muted">No files changed yet.</p>
    );
  else
    body = (
      <>
        <div className="dbg-bar">
          <div className="chips">
            {DBG_FILTERS.map(([v, l]) => (
              <Chip
                key={v}
                on={filter === v}
                onClick={() => {
                  S.run.dbgFilter = v;
                  update();
                }}
              >
                {l}{" "}
                <span className="muted">
                  {v === "all" ? evs.length : evs.filter((e) => e.type.startsWith(v + ".")).length}
                </span>
              </Chip>
            ))}
          </div>
          <span className="status-spacer" />
          <button
            className="btn secondary"
            onClick={() => {
              S.run.dbgAll = !S.run.dbgAll;
              update();
            }}
          >
            {S.run.dbgAll ? "Collapse all" : "Expand all"}
          </button>
          <button
            className="btn secondary"
            onClick={() =>
              navigator.clipboard?.writeText(jsonl()).then(
                () => toast(`Copied ${evs.length} events as JSONL.`),
                () => toast("Copy failed; use Download."),
              )
            }
          >
            Copy JSONL
          </button>
          <button
            className="btn secondary"
            onClick={() =>
              saveText(`run-${st.id}-${st.lastRun}.jsonl`, jsonl() + "\n", "application/x-ndjson")
            }
          >
            Download
          </button>
        </div>
        <div className="dbg" role="log" aria-label="Debug events">
          <div className="dbg-row dbg-headrow">
            <span>#</span>
            <span>+time</span>
            <span>type</span>
            <span>summary</span>
            <span className="num">in</span>
            <span className="num">out</span>
          </div>
          {shown.map((e, i) => {
            const open = S.run.dbgAll || S.run.dbgOpen.includes(e.seq);
            return (
              <div key={`${e.seq}-${i}`} style={{ display: "contents" }}>
                <button
                  className={"dbg-row" + (open ? " open" : "")}
                  aria-expanded={open}
                  onClick={() => {
                    const o = S.run.dbgOpen;
                    o.includes(e.seq) ? o.splice(o.indexOf(e.seq), 1) : o.push(e.seq);
                    update();
                  }}
                >
                  <span className="muted">{e.seq}</span>
                  <span className="muted">+{fmtMs(e.dt || 0)}</span>
                  <span
                    className="dbg-type"
                    style={{ color: `var(${EV_TONE[e.type.split(".")[0]] || "--fg"})` }}
                  >
                    <EventIcon e={e} />
                    {e.type}
                  </span>
                  <span className="dbg-sum">
                    <Linkify text={e.summary} />
                  </span>
                  <span className="num">{e.input_tokens ? fmtK(e.input_tokens) : ""}</span>
                  <span className="num">{e.output_tokens ? fmtK(e.output_tokens) : ""}</span>
                </button>
                {open && <pre className="dbg-json">{JSON.stringify(e, null, 2)}</pre>}
              </div>
            );
          })}
          {running && (
            <div className="dbg-row">
              <span />
              <span />
              <span className="spinner" />
              <span className="muted">streaming…</span>
            </div>
          )}
        </div>
        <p className="muted" style={{ marginTop: 6 }}>
          Token-shaped strings and secret values are redacted before events are published or recorded.
        </p>
      </>
    );
  return (
    <>
      <div className={"card run-card" + (running ? " is-running" : "")}>
        <div className="card-head">
          <h3>
            {running ? "Running" : "Run"} · <code>{st.cmd}</code>
          </h3>
          {running ? (
            <span className="spinner" />
          ) : (
            <Pill
              s={st.status}
              label={
                st.id === "flow"
                  ? { done: "Completed", failed: "Failed", stopped: "Stopped", interrupted: "Interrupted" }[
                      st.status
                    ]
                  : undefined
              }
            />
          )}
        </div>
        {running && st.id !== "flow" && <AiProgress evs={evs} className="ai-in-card" />}
        <div className="mstats">
          <Stat v={fmtMs(u.ms)} l="Elapsed" />
          <Stat
            v={fmtK(u.in + u.out)}
            l="Tokens"
            sub={`${fmtK(u.in)} in · ${fmtK(u.out)} out · ${fmtK(u.cached)} cached`}
          />
          <Stat v={fmtCredits(u)} l={isCopilot() ? "Credits" : "Cost"} sub={`${u.calls} model calls`} />
          <Stat v={u.tools} l="Tool calls" sub={`${u.toolErr} failed`} />
          <Stat
            v={u.files.length}
            l="Files"
            sub={`${u.files.filter((f) => f.op === "created").length} created · ${u.files.filter((f) => f.op === "modified").length} modified`}
          />
          <div className="mstat">
            <b>{ctxPct}%</b>
            <span>Context used</span>
            <span className="bar-track" style={{ marginTop: 4 }}>
              <span className="bar-fill" style={{ width: ctxPct + "%" }} />
            </span>
          </div>
        </div>
        <ResultsLink id={st.lastRun} ended={!running && evs.some((e) => e.type === "run.end")} />
      </div>
      <div className="tabs" role="tablist">
        {[
          ["activity", "Activity"],
          ["files", `Files changed · ${u.files.length}`],
          ["debug", `Debug · ${evs.length} events`],
        ].map(([v, l]) => (
          <button key={v} role="tab" className="tab" aria-selected={tab === v} onClick={() => setTab(v)}>
            {l}
          </button>
        ))}
      </div>
      {body}
    </>
  );
}

export { stepUsage };
