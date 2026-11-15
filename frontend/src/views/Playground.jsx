// Prompt playground: send any prompt to the chosen agent in the workspace
// and see its answer, every tool call with arguments and result, and the
// tokens, cost or premium requests, model calls and timing of the run.
import { ModelPicker } from "../ModelPicker.jsx";
import { CopyButton } from "../CopyButton.jsx";
import { RunListBar, RunGroup, PickBox, RunActions, visibleRuns } from "../RunHousekeeping.jsx";
import { useEffect, useRef, useState } from "react";
import { ContentView } from "../ContentView.jsx";
import { backend } from "../backend.js";
import { EmptyState } from "../ui.jsx";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg, usageOf, isCopilot, fmtK, fmtMs, fmtCredits } from "../store.js";
import { Icon, Pill, Stat, Chip } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { RunPanel } from "./Workflow.jsx";
import { agentLabel } from "./Setup.jsx";
import { Linkify, FileLink } from "../FileLinks.jsx";
import { EventIcon } from "../EventIcon.jsx";
import { openFlowRunner } from "./FlowRun.jsx";
import { ResultsLink } from "../RunResults.jsx";
import { AiProgress } from "../AiProgress.jsx";

const EXAMPLES = [
  "Summarize what this project does and how it is organised",
  "List the open TODOs and FIXMEs with file and line",
  "Which tests cover the order service? Run them and report failures",
  "Explain the data model in specs/ in plain language",
];

const pg = () =>
  (S.pg ||= { prompt: "", tools: true, model: "", runs: [], sel: "", loaded: false, parents: [] });

// Start a new prompt in the playground that continues from earlier runs.
export function continueInPlayground(ids) {
  const p = pg();
  p.parents = [...new Set([...(p.parents || []), ...ids])];
  p.sel = "";
  p.prompt = "";
  S.view = "playground";
  update();
  setTimeout(() => document.querySelector(".pg-composer textarea")?.focus(), 50);
}

const KIND = { playground: "Prompt", workflow: "Workflow step", flow: "Flow run" };
const runTitle = (id) => {
  const x = pg().runs.find((r) => r.id === id) || (S.recentRuns || []).find((r) => r.id === id);
  return x
    ? (x.title || S.flowRuns[id]?.flowName || x.prompt || x.flow || id).split("\n")[0].slice(0, 70)
    : id.slice(0, 15) + "…";
};

async function loadPast() {
  const p = pg();
  if (p.loaded || !S.ws) return;
  p.loaded = true;
  const runs = (await backend.Runs(500).catch(() => [])) || [];
  for (const r of runs.filter((x) => x.step === "playground" || x.step === "flow")) {
    const had = p.runs.find((x) => x.id === r.id);
    if (had) Object.assign(had, { archived: !!r.archived, group: r.group || "" });
    else
      p.runs.push({
        id: r.id,
        prompt: r.ask || "",
        title: r.title || "",
        parents: r.parents || [],
        at: r.started,
        state: r.state,
        archived: !!r.archived,
        group: r.group || "",
        ...(r.step === "flow" ? { kind: "flow", flow: r.workflow } : {}),
      });
  }
  p.runs.sort((a, b) => String(b.at).localeCompare(String(a.at)));
  if (!p.sel && p.runs[0]) select(p.runs[0].id);
  update();
}

async function select(id) {
  const p = pg();
  p.sel = id;
  update();
  const x = p.runs.find((r) => r.id === id);
  if (x?.kind === "flow" && !S.flowRuns[id])
    backend
      .FlowRun(id)
      .then((st) => {
        S.flowRuns[id] = st;
        update();
      })
      .catch(() => {});
  if (!S.loaded[id]) {
    S.loaded[id] = true;
    const evs = (await backend.RunEvents(id).catch(() => [])) || [];
    const live = S.events[id] || [],
      seen = new Set(evs.map((e) => e.seq));
    S.events[id] = evs.concat(live.filter((e) => !seen.has(e.seq))).sort((a, b) => a.seq - b.seq);
    update();
  }
}

// Reply to the selected run's answer: a new prompt that continues it.
async function reply(runId, text) {
  const p = pg();
  if (!text.trim()) return;
  p.prompt = text;
  p.parents = [runId];
  p.reply = "";
  return run();
}

// Did the answer end by asking the user something?
const asksUser = (t) =>
  /\?\s*(\**)?\s*$/.test(t.trim()) ||
  /\b(should I|shall I|do you want|would you like|let me know|please confirm|which (one|option))\b/i.test(
    t.split("\n").slice(-4).join(" "),
  );

async function run() {
  const p = pg(),
    prompt = p.prompt.trim();
  if (!prompt) return toast("Type a prompt first.");
  try {
    const parents = [...(p.parents || [])];
    const id = await backend.RunPrompt(prompt, { tools: p.tools, model: p.model.trim(), parents });
    S.loaded[id] = true;
    S.events[id] ||= [];
    p.runs.unshift({
      id,
      prompt,
      title: prompt.split("\n")[0].slice(0, 100),
      parents,
      at: new Date().toISOString(),
      state: "running",
    });
    p.sel = id;
    p.parents = [];
    p.prompt = "";
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

const promptOf = (r) =>
  r.prompt ||
  r.title ||
  (S.events[r.id] || []).find((e) => e.type === "prompt.compose")?.prompt ||
  "(prompt)";
const PG_LABEL = { done: "Completed", failed: "Failed", cancelled: "Stopped", running: "Running" };
export const stateOf = (evs) =>
  [...evs].reverse().find((e) => e.type === "run.end")?.state || (evs.length ? "running" : "pending");

// Pair each tool call with its result (by id, else the next result of that tool).
export function toolCalls(evs) {
  const out = [],
    used = new Set();
  for (const e of evs) {
    if (e.type !== "tool.call") continue;
    const res = evs.find(
      (x) =>
        x.type === "tool.result" &&
        x.seq > e.seq &&
        !used.has(x.seq) &&
        (e.id ? x.id === e.id : x.tool === e.tool),
    );
    if (res) used.add(res.seq);
    const ext = /extension_managed$/.test(e.tool || "")
      ? evs.find((x) => x.type === "ext.result" && x.seq > e.seq && (!res || x.seq <= res.seq))
      : null;
    out.push({ call: e, res, ext });
  }
  return out;
}

// The saved files of the n-th tool call of a run (tools/<nn>-<tool>/…), or
// the ones a flow tool step reported.
function callFiles(runId, n, res) {
  if (res?.output_file)
    return [
      res.input_file && { label: "input", path: res.input_file },
      res.args_file && { label: "args.json", path: res.args_file },
      { label: "output", path: res.output_file },
    ].filter(Boolean);
  const r = S.results[runId];
  if (!r?.files) return [];
  const pre = `tools/${String(n).padStart(2, "0")}-`;
  return r.files
    .filter((f) => f.startsWith(pre))
    .map((f) => ({ label: f.split("/").pop(), path: r.dir + "/" + f }));
}

function ToolCall({ c, n, runId }) {
  const { call, res, ext } = c,
    args = call.args || {};
  const name = /extension_managed$/.test(call.tool || "")
    ? `project tool → ${args.name || "?"}`
    : (call.tool || "").replace(/^mcp__agentbridge__/, "");
  const output = ext?.output ?? res?.output;
  const state = !res ? "running" : res.ok ? "ok" : "failed";
  return (
    <details
      className={"pg-tool" + (state === "failed" ? " is-error" : "")}
      open={state === "failed" || undefined}
    >
      <summary>
        <span className="pg-tool-n">{n}</span>
        <EventIcon e={call} />
        <b>{name}</b>
        <span className="tool-arg">
          {/extension_managed$/.test(call.tool || "") ? (
            ""
          ) : (
            <Linkify text={call.summary?.replace(call.tool, "").trim()} />
          )}
        </span>
        <span className="tool-state">
          {state === "running" ? (
            <span className="spinner" />
          ) : (
            <>
              {state}
              {res?.duration_ms != null ? " · " + fmtMs(res.duration_ms) : ""}
            </>
          )}
        </span>
      </summary>
      <div className="pg-tool-body">
        <div className="muted">Input</div>
        <ContentView
          compact
          format="json"
          title={name + " input"}
          text={JSON.stringify(
            /extension_managed$/.test(call.tool || "") ? (args.arguments ?? args) : args,
            null,
            2,
          )}
          files={callFiles(runId, n, res).filter((f) => /input|args/.test(f.label))}
        />
        {res && (
          <>
            <div className="muted">Result{ext ? ` · exit ${ext.exit_code}` : ""}</div>
            <ContentView
              title={name + " output"}
              text={output || (res.ok ? "" : "failed")}
              files={callFiles(runId, n, res).filter((f) => /output/.test(f.label))}
            />
          </>
        )}
      </div>
    </details>
  );
}

// Composer: the prompt box. Full (examples, notes) for a new prompt; compact and
// docked at the bottom while a run is open, so it stays in reach under long answers.
function Composer({ compact, box, r, running, anyRunning }) {
  const p = pg();
  return (
    <div className={"card pg-composer" + (compact ? " pg-dock" : "")}>
      {compact && r?.kind !== "flow" && !running && (
        <label className="check pg-dock-cont">
          <input
            type="checkbox"
            checked={(p.parents || []).includes(r.id)}
            onChange={(e) => {
              p.parents = e.target.checked
                ? [...new Set([...(p.parents || []), r.id])]
                : (p.parents || []).filter((x) => x !== r.id);
              update();
            }}
          />{" "}
          Continue from this run (it goes to the agent as context)
        </label>
      )}
      <textarea
        ref={box}
        rows={compact ? 2 : 5}
        value={p.prompt}
        placeholder={
          compact
            ? "Ask a follow-up or a new question…  (⌘/Ctrl + Enter to run)"
            : "Ask the agent anything about this project…  (⌘/Ctrl + Enter to run)"
        }
        onChange={(e) => {
          p.prompt = e.target.value;
          update();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            run();
          }
        }}
      />
      {p.parents?.length > 0 && (
        <div className="pg-cont">
          <span className="muted">
            <Icon.history /> Continuing from:
          </span>
          {p.parents.map((id) => (
            <span key={id} className="pg-cont-chip" title={id}>
              {runTitle(id)}
              <button
                aria-label="Remove"
                onClick={() => {
                  p.parents = p.parents.filter((x) => x !== id);
                  update();
                }}
              >
                <Icon.x />
              </button>
            </span>
          ))}
          <span className="muted pg-cont-hint">
            The earlier questions, answers and results folders go to the agent as context.
          </span>
        </div>
      )}
      {!compact && (
        <div className="chips" style={{ marginTop: 8 }}>
          {EXAMPLES.map((x) => (
            <Chip
              key={x}
              onClick={() => {
                p.prompt = x;
                update();
                box.current?.focus();
              }}
            >
              {x.split(" ").slice(0, 5).join(" ")}…
            </Chip>
          ))}
        </div>
      )}
      <div className="row-actions">
        <button className="btn primary" disabled={anyRunning || !p.prompt.trim()} onClick={run}>
          {anyRunning ? "Running…" : "Run prompt"}
        </button>
        {running && (
          <button
            className="btn secondary"
            onClick={() => backend.CancelRun(r.id).catch((e) => toast(errMsg(e), "error"))}
          >
            Stop
          </button>
        )}
        <button className="btn secondary" onClick={() => openPicker()}>
          <Icon.history /> Refer to earlier runs…
        </button>
        <label className="check">
          <input
            type="checkbox"
            checked={p.tools}
            onChange={(e) => {
              p.tools = e.target.checked;
              update();
            }}
          />{" "}
          Also offer project and admin tools
        </label>
      </div>
      {!compact && (
        <p className="muted" style={{ margin: "8px 0 0" }}>
          Runs in <code className="inline-code">{S.ws.root}</code> with the workflow's token and cost budget.
          The agent can read and edit files there, as in a workflow step. The agent always has SpecWright's
          built-in tools for reading, searching and editing files and running commands.
        </p>
      )}
    </div>
  );
}

export function Playground() {
  const p = pg(),
    box = useRef(null);
  useEffect(() => {
    loadPast();
  }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="the playground" />;
  for (const [id, fst] of Object.entries(S.flowRuns))
    if (!p.runs.some((x) => x.id === id))
      p.runs.unshift({ id, prompt: "", at: fst.started, state: fst.status, kind: "flow", flow: fst.flowId });
  const r = p.runs.find((x) => x.id === p.sel);
  const evs = r ? S.events[r.id] || [] : [];
  const state = r ? stateOf(evs) : null,
    running = state === "running";
  const u = usageOf(evs),
    calls = toolCalls(evs);
  const answer = evs
    .filter((e) => e.type === "model.text")
    .map((e) => e.text)
    .join("\n\n");
  const problems = evs.filter((e) =>
    ["error", "loop.detected", "budget.exceeded", "run.retry"].includes(e.type),
  );
  const model = [...evs].reverse().find((e) => e.type === "model.request" && e.model)?.model;
  const anyRunning = p.runs.some((x) => stateOf(S.events[x.id] || []) === "running");
  return (
    <div className="files pg">
      <Pane k="playground" v="--pane-l" pane=".ftree" min={200} max={560} label="Resize the run list" />
      <nav className="ftree" aria-label="Playground prompts">
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            padding: "0 8px 8px",
          }}
        >
          <h1>Playground</h1>
          <button
            className="btn primary"
            onClick={() => {
              p.sel = "";
              p.prompt = "";
              update();
              setTimeout(() => box.current?.focus(), 0);
            }}
          >
            + New
          </button>
        </div>
        {!p.runs.length && (
          <EmptyState compact title="No prompts yet">
            Prompts you run appear here, with their answers, tool calls and files changed. Write one on the
            right; ⌘Enter / Ctrl+Enter runs it.
          </EmptyState>
        )}
        {p.runs.length > 0 && <RunListBar area="playground" runs={p.runs} />}
        {visibleRuns(
          "playground",
          p.runs,
          (x) => (x.title || "") + " " + (x.prompt || "") + " " + (x.flow || ""),
        ).map(([g, list]) => (
          <RunGroup key={g || "-"} area="playground" name={g} count={list.length}>
            {list.map((x) => {
              const st = stateOf(S.events[x.id] || []),
                fst = x.kind === "flow" ? S.flowRuns[x.id] : null;
              return (
                <button
                  key={x.id}
                  className={
                    "run-item" +
                    (x.id === p.sel ? " active" : "") +
                    (x.kind === "flow" ? " pg-flow-item" : "")
                  }
                  onClick={() => select(x.id)}
                >
                  <PickBox area="playground" id={x.id} />
                  <span className="run-item-top">
                    {x.kind === "flow" ? (
                      <span className="pg-item-text">
                        <span className="pg-flow-tag">
                          <Icon.flow /> flow
                        </span>{" "}
                        {fst?.flowName || x.flow}
                      </span>
                    ) : (
                      <span className="pg-item-text">
                        {x.parents?.length ? (
                          <span className="pg-thread-mark" title="Continues earlier runs">
                            ↳{" "}
                          </span>
                        ) : null}
                        {(x.title || promptOf(x)).split("\n")[0]}
                      </span>
                    )}
                    {x.kind === "flow" ? (
                      <Pill
                        s={
                          {
                            review: "paused",
                            stopped: "cancelled",
                            interrupted: "failed",
                            scheduled: "pending",
                          }[fst?.status || x.state] ||
                          fst?.status ||
                          x.state
                        }
                        label={
                          {
                            review: "Waiting",
                            done: "Completed",
                            failed: "Failed",
                            stopped: "Stopped",
                            running: "Running",
                            scheduled: "Scheduled",
                            interrupted: "Interrupted",
                          }[fst?.status || x.state]
                        }
                      />
                    ) : S.events[x.id]?.length ? (
                      <Pill s={st} label={PG_LABEL[st]} />
                    ) : null}
                  </span>
                  <small>{new Date(x.at).toLocaleString()}</small>
                </button>
              );
            })}
          </RunGroup>
        ))}
      </nav>
      <div className="fview">
        <header className="view-head">
          <h1>Prompt playground</h1>
          <div className="head-controls head-model">
            <span className="muted">
              Agent: {agentLabel()} · {S.ws.name}
            </span>
            <ModelPicker
              value={p.model}
              onChange={(v) => {
                p.model = v;
                update();
              }}
              label="Model"
            />
          </div>
        </header>
        {!r && <Composer box={box} r={null} running={false} anyRunning={anyRunning} />}

        {r?.kind === "flow" && <FlowWatch r={r} evs={evs} />}
        {r && r.kind !== "flow" && (
          <>
            <Thread r={r} />
            <div className="pg-you">
              <span className="pg-you-tag">You</span>
              <div>{promptOf(r)}</div>
            </div>
            <div className="card run-card">
              <div className="card-head">
                <h3>Usage</h3>
                {running ? <span className="spinner" /> : <Pill s={state} label={PG_LABEL[state]} />}
                {model && <span className="badge">{model}</span>}
              </div>
              <div className="mstats">
                <Stat v={fmtMs(u.ms)} l="Elapsed" />
                <Stat
                  v={fmtK(u.in + u.out)}
                  l="Tokens"
                  sub={`${fmtK(u.in)} in · ${fmtK(u.out)} out · ${fmtK(u.cached)} cached`}
                />
                <Stat
                  v={fmtCredits(u)}
                  l={isCopilot() ? "AI credits" : "Cost"}
                  sub={isCopilot() ? "premium requests" : "reported by the CLI"}
                />
                <Stat v={u.calls} l="Model calls" />
                <Stat v={u.tools} l="Tool calls" sub={`${u.toolErr} failed`} />
                <Stat
                  v={u.files.length}
                  l="Files changed"
                  sub={u.files
                    .map((f) => f.path.split("/").pop())
                    .slice(0, 3)
                    .join(", ")}
                />
              </div>
              <ResultsLink id={r.id} ended={!running && evs.some((e) => e.type === "run.end")} />
              {!running && (
                <div className="row-actions hk-run-actions">
                  <RunActions area="playground" runs={p.runs} run={r} />
                </div>
              )}
            </div>
            {problems.map((e) => (
              <div key={e.seq} className={"banner" + (e.type === "run.retry" ? "" : " error")}>
                <Linkify text={e.summary} />
              </div>
            ))}
            <div className="card pg-answer">
              <div className="card-head">
                <h3>Output</h3>
                {answer && <CopyButton text={answer} format="md" />}
                {!running && evs.length > 0 && (
                  <button className="btn primary pg-continue" onClick={() => continueInPlayground([r.id])}>
                    <Icon.history /> Continue this conversation
                  </button>
                )}
              </div>
              {answer ? (
                <div className="row assistant pg-text">
                  <Linkify text={answer} />
                  {running && <span className="caret" />}
                </div>
              ) : (
                !running && <p className="muted">No text output.</p>
              )}
              {running && <AiProgress evs={evs} />}
              {running && S.permAsks.some((x) => x.run === r.id || !x.run) && (
                <div className="pg-waiting">
                  <Icon.shield /> Waiting for your approval to use <b>{S.permAsks[0].tool}</b>.
                </div>
              )}
              {!running && answer && evs.some((e) => e.type === "run.end") && (
                <div className={"pg-reply" + (asksUser(answer) ? " asks" : "")}>
                  {asksUser(answer) && (
                    <div className="pg-reply-q">
                      <span className="muted">The AI is asking you:</span>
                      {["Yes, go ahead", "No, don't do that", "Explain the options first"].map((x) => (
                        <button key={x} className="chip" disabled={anyRunning} onClick={() => reply(r.id, x)}>
                          {x}
                        </button>
                      ))}
                    </div>
                  )}
                  <div className="pg-reply-box">
                    <input
                      value={p.reply || ""}
                      placeholder={
                        asksUser(answer) ? "Answer the AI…" : "Reply to continue this conversation…"
                      }
                      aria-label="Reply"
                      onChange={(e) => {
                        p.reply = e.target.value;
                        update();
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && !e.shiftKey) {
                          e.preventDefault();
                          reply(r.id, p.reply || "");
                        }
                      }}
                    />
                    <button
                      className="btn primary"
                      disabled={anyRunning || !(p.reply || "").trim()}
                      onClick={() => reply(r.id, p.reply || "")}
                    >
                      Reply
                    </button>
                  </div>
                </div>
              )}
            </div>
            <div className="card">
              <div className="card-head">
                <h3>Tool usage</h3>
                <span className="badge">{calls.length}</span>
              </div>
              {calls.length ? (
                <div className="pg-tools">
                  {calls.map((c, k) => (
                    <ToolCall key={c.call.seq} c={c} n={k + 1} runId={r.id} />
                  ))}
                </div>
              ) : (
                <p className="muted">
                  {running ? "No tool calls yet." : "The agent answered without tools."}
                </p>
              )}
            </div>
            <details className="pg-more">
              <summary className="muted">Full activity, files changed and raw events</summary>
              <RunPanel
                st={{ id: "playground", cmd: "playground", status: state, lastRun: r.id }}
                evs={evs}
                running={running}
              />
            </details>
          </>
        )}
        {r && <Composer compact box={box} r={r} running={running} anyRunning={anyRunning} />}
      </div>
    </div>
  );
}

// Watching a flow run: its steps, live progress and the full log.
function FlowWatch({ r, evs }) {
  const st = S.flowRuns[r.id],
    live = ["running", "review", "scheduled"].includes(st?.status);

  if (!st)
    return (
      <div className="card">
        <span className="spinner" /> Loading the flow run…
      </div>
    );
  return (
    <>
      <div className="card run-card pg-flow">
        <div className="card-head">
          <Icon.flow />
          <h3>{st.flowName}</h3>
          <Pill
            s={
              { review: "paused", stopped: "cancelled", interrupted: "failed", scheduled: "pending" }[
                st.status
              ] || st.status
            }
            label={
              {
                review: "Waiting for review",
                done: "Completed",
                failed: "Failed",
                stopped: "Stopped",
                running: "Running",
                scheduled: "Scheduled",
                interrupted: "Interrupted",
              }[st.status]
            }
          />
          <button
            className="link"
            style={{ marginLeft: "auto" }}
            onClick={() => {
              openFlowRunner(st.flowId);
              setTimeout(() => {
                S.frun.runId = st.id;
                update();
              }, 0);
            }}
          >
            {st.status === "review" ? "Review it in Run flows" : "Open in Run flows"}
          </button>
        </div>
        <div className="pg-flow-steps">
          {st.steps
            .filter((s) => s.type !== "start")
            .map((s) => (
              <span
                key={s.id}
                className={"pg-fs s-" + s.status}
                title={(s.error || s.status) + (s.durationMs ? " · " + fmtMs(s.durationMs) : "")}
              >
                <span className="fr-dot" />
                {s.title}
              </span>
            ))}
        </div>
        {live && <AiProgress evs={evs} />}
        {st.outputs?.length > 0 && (
          <p className="muted" style={{ margin: "4px 0 0" }}>
            {st.outputs.length} output{st.outputs.length === 1 ? "" : "s"}
            {st.outputs.flatMap((o) => o.saved || []).length
              ? " · saved " + [...new Set(st.outputs.flatMap((o) => o.saved || []))].join(", ")
              : ""}
          </p>
        )}
      </div>
      <RunPanel
        st={{ id: "flow", cmd: st.flowName, status: live ? "running" : st.status, lastRun: r.id }}
        evs={evs}
        running={live}
      />
    </>
  );
}

// The earlier turns of a conversation (runs this one continues from).
function Thread({ r }) {
  const [open, setOpen] = useState({});
  useEffect(() => {
    if (r.parents?.length && !S.threads[r.id])
      backend
        .RunThread(r.id)
        .then((t) => {
          S.threads[r.id] = t;
          update();
        })
        .catch(() => {
          S.threads[r.id] = [];
        });
  }, [r.id, r.parents?.length]);
  const t = (S.threads[r.id] || []).filter((d) => d.id !== r.id);
  if (!r.parents?.length || !t.length) return null;
  return (
    <div className="pg-thread" aria-label="Earlier in this conversation">
      <div className="muted pg-thread-head">
        <Icon.history /> Earlier in this conversation · {t.length} run{t.length === 1 ? "" : "s"}
      </div>
      {t.map((d) => (
        <div key={d.id} className={"pg-turn k-" + d.kind}>
          <button
            className="pg-turn-head"
            onClick={() => setOpen({ ...open, [d.id]: !open[d.id] })}
            aria-expanded={!!open[d.id]}
          >
            <span className="pg-turn-kind">{KIND[d.kind] || "Run"}</span>
            <b>{d.title}</b>
            <span className="muted">{new Date(d.started).toLocaleString()}</span>
          </button>
          {d.ask && d.kind === "playground" && (
            <div className="pg-turn-ask">
              {d.ask.length > 400 && !open[d.id] ? d.ask.slice(0, 400) + "…" : d.ask}
            </div>
          )}
          <div className={"pg-turn-answer" + (open[d.id] ? " open" : "")}>
            <Linkify text={d.answer || "(no text answer)"} />
          </div>
          {d.results && (
            <div className="pg-turn-res">
              <Icon.folder /> <FileLink path={d.results}>{d.results}/</FileLink>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

// Pick earlier runs (prompts, workflow steps, flow runs) to continue from.
async function openPicker() {
  S.recentRuns = await backend.RecentRuns(100).catch(() => []);
  S.pgPicker = { q: "", kind: "", sel: [...(pg().parents || [])] };
  update();
}

export function RunPicker() {
  const k = S.pgPicker;
  if (!k) return null;
  const list = (S.recentRuns || []).filter(
    (d) =>
      (!k.kind || d.kind === k.kind) &&
      (!k.q || (d.title + " " + d.ask).toLowerCase().includes(k.q.toLowerCase())),
  );
  const close = () => {
    S.pgPicker = null;
    update();
  };
  return (
    <div className="modal-backdrop dlg-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div className="dlg pg-picker" role="dialog" aria-modal="true" aria-label="Refer to earlier runs">
        <h3>Refer to earlier runs</h3>
        <p className="dlg-msg">
          Their questions, answers and results folders become the context of your next prompt, so you can keep
          the conversation going.
        </p>
        <div className="pg-picker-bar">
          <input
            placeholder="Search"
            value={k.q}
            autoFocus
            onChange={(e) => {
              k.q = e.target.value;
              update();
            }}
          />
          <div className="seg">
            {[
              ["", "All"],
              ["playground", "Prompts"],
              ["workflow", "Workflow"],
              ["flow", "Flows"],
            ].map(([v, l]) => (
              <button
                key={v}
                type="button"
                aria-pressed={k.kind === v}
                onClick={() => {
                  k.kind = v;
                  update();
                }}
              >
                {l}
              </button>
            ))}
          </div>
        </div>
        <ul className="pg-picker-list">
          {list.map((d) => (
            <li key={d.id}>
              <label className="check">
                <input
                  type="checkbox"
                  checked={k.sel.includes(d.id)}
                  onChange={(e) => {
                    k.sel = e.target.checked ? [...k.sel, d.id] : k.sel.filter((x) => x !== d.id);
                    update();
                  }}
                />
                <span className="pg-turn-kind">{KIND[d.kind] || "Run"}</span>
                <span className="pg-picker-title">
                  <b>{d.title}</b>
                  <small className="muted">
                    {new Date(d.started).toLocaleString()} · {d.state}
                    {d.parents?.length ? " · continues " + d.parents.length : ""}
                  </small>
                </span>
              </label>
            </li>
          ))}
          {!list.length && <li className="muted">No runs yet.</li>}
        </ul>
        <div className="row-actions dlg-actions">
          <button className="btn secondary" onClick={close}>
            Cancel
          </button>
          <button
            className="btn primary"
            onClick={() => {
              pg().parents = k.sel;
              close();
              setTimeout(() => document.querySelector(".pg-composer textarea")?.focus(), 30);
            }}
          >
            Continue from {k.sel.length || "no"} run{k.sel.length === 1 ? "" : "s"}
          </button>
        </div>
      </div>
    </div>
  );
}
