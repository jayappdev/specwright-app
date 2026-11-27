// Ask AI: a conversation with the chosen agent about the project, laid out
// as Chats (conversations on the left, the thread in the middle, the message
// box at the bottom). Each question is a run; a follow-up is a run that
// continues the one before (its parents), so the agent gets the earlier
// questions and answers. Each answer shows the tools it ran and, on its ⓘ,
// the tokens, cost or premium requests, model calls and time it took.
import { registerPlace } from "../navigation.js";
import { ModelPicker } from "../ModelPicker.jsx";
import { PROMPT_STATUS_LABEL, RUN_KIND_LONG } from "../labels.js";
import { CopyButton } from "../CopyButton.jsx";
import { RunListBar, RunGroup, PickBox, visibleRuns } from "../RunHousekeeping.jsx";
import { useEffect, useRef, useState } from "react";
import { ContentView } from "../ContentView.jsx";
import { backend } from "../backend.js";
import { EmptyState, RefreshButton, usePoll, ListHead } from "../ui.jsx";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg, usageOf, isCopilot, fmtK, fmtMs, fmtCredits } from "../store.js";
import { Icon, Pill, Chip, Crumbs } from "../ui.jsx";
import { mdRender } from "../markdown.js";
import { ago } from "../time.js";
import { InfoTip } from "./ChatUsage.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { RunPanel } from "../components/RunPanel.jsx";
import { agentLabel } from "./Setup.jsx";
import { Linkify, FileLink } from "../FileLinks.jsx";
import { EventIcon } from "../EventIcon.jsx";
import { openFlowRunner } from "./FlowRun.jsx";
import { ResultsLink } from "../RunResults.jsx";
import { AiProgress } from "../AiProgress.jsx";
import { applyToolRevision, reloadTool } from "./Tools.jsx";
import { proposedTool } from "./tools/toolHistory.js";
import { conversations } from "./askai.js";
import { ContextPanel, Rule, PanelToggle, panelOpen } from "../components/ContextPanel.jsx";

const EXAMPLES = [
  "Summarize what this project does and how it is organised",
  "List the open TODOs and FIXMEs with file and line",
  "Which tests cover the order service? Run them and report failures",
  "Explain the data model in specs/ in plain language",
];

const pg = () =>
  (S.pg ||= { prompt: "", tools: true, ask: true, model: "", runs: [], sel: "", loaded: false, parents: [] });

// Which tool each conversation is about (run id → tool name), kept in this
// browser so Apply works after a reload.
const TOOL_KEY = "codegen.pg.tools.v1";
function toolFor() {
  const p = pg();
  if (!p.toolFor)
    try {
      p.toolFor = JSON.parse(localStorage.getItem(TOOL_KEY) || "{}") || {};
    } catch {
      p.toolFor = {};
    }
  return p.toolFor;
}
function setToolFor(id, name) {
  const m = toolFor();
  m[id] = name;
  try {
    localStorage.setItem(TOOL_KEY, JSON.stringify(Object.fromEntries(Object.entries(m).slice(-200))));
  } catch {
    /* per-browser convenience only */
  }
}

// openToolChat: a new conversation about a tool, its prompt (the tool, its
// history and last test) ready to read and edit before sending.
export function openToolChat(prompt, name) {
  const p = pg();
  p.parents = [];
  p.sel = "";
  p.prompt = prompt;
  p.toolNext = name;
  S.view = "playground";
  update();
  setTimeout(() => document.querySelector(".pg-composer textarea")?.focus(), 50);
}

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

const runTitle = (id) => {
  const x = pg().runs.find((r) => r.id === id) || (S.recentRuns || []).find((r) => r.id === id);
  return x
    ? (x.title || S.flowRuns[id]?.flowName || x.prompt || x.flow || id).split("\n")[0].slice(0, 70)
    : id.slice(0, 15) + "…";
};

// How often the run list is read again while the Playground is open (as
// History): runs started elsewhere (a flow, a trigger, an agent's run_flow)
// show up without leaving the view.
export const PLAYGROUND_POLL = 30;

// loadPast reads the recorded runs of this project into the list, merging
// with what it has: on opening, on Refresh, every PLAYGROUND_POLL seconds,
// and when a run it does not know sends an event. Another project starts a
// list of its own.
export async function loadPast(force) {
  const p = pg();
  if (!S.ws) return;
  if (p.root !== S.ws.root) {
    if (p.root) Object.assign(p, { runs: [], sel: "", parents: [] });
    p.root = S.ws.root;
    p.loaded = false;
  }
  if (p.loaded && !force) return;
  p.loaded = true;
  const runs = (await backend.Runs(500).catch(() => null)) || null;
  if (!runs) return;
  for (const r of runs.filter((x) => x.step === "playground" || x.step === "flow")) {
    const had = p.runs.find((x) => x.id === r.id);
    if (had)
      Object.assign(had, {
        archived: !!r.archived,
        group: r.group || "",
        title: had.title || r.title || "",
        state: r.state || had.state,
      });
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
  loadEvents(id);
}

// Reply to the selected run's answer: a new prompt that continues it.
async function reply(runId, text) {
  const p = pg();
  if (!text.trim()) return;
  p.prompt = text;
  p.parents = [runId, ...(p.parents || []).filter((x) => x !== runId)];
  if (toolFor()[runId]) p.toolNext = toolFor()[runId];
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
    const id = await backend.RunPrompt(prompt, {
      tools: p.tools,
      model: p.model.trim(),
      parents,
      interactive: p.ask !== false,
    });
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
    if (p.toolNext) setToolFor(id, p.toolNext);
    p.toolNext = "";
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

const TOOL_GROUPS = [
  ["flows", "SpecWright: flows and history"],
  ["tools", "SpecWright: tools"],
  ["read", "Read"],
  ["search", "Search"],
  ["edit", "Edit"],
  ["execute", "Run"],
  ["web", "Web"],
  ["chart", "Charts"],
  ["plan", "Plan"],
];

async function loadAgentTools() {
  S.agentTools = (await backend.AgentTools().catch(() => [])) || [];
  update();
}

// AgentToolsList: what the agent gets in a Playground run: SpecWright's own
// tools (save_flow, run_flow, …), the built-ins and, with "Also offer
// project and admin tools", the project's tools through extension_managed.
// Names as the agent's CLI shows them, so a prompt can name them.
function AgentToolsList({ withProject }) {
  const [open, setOpen] = useState(false);
  const toolKey = (S.ws?.extensions || []).map((t) => t.name + ":" + (t.revision || 0)).join(",");
  useEffect(() => {
    if (open) loadAgentTools(); // read again each time: tools and policy change
  }, [open, toolKey]);
  const all = S.agentTools || [];
  const project = withProject
    ? (S.ws?.extensions || []).filter((t) => !t.managed || t.managed.status === "verified")
    : [];
  const cop = isCopilot();
  const shown = (n) => (cop ? "agentbridge-" + n : "mcp__agentbridge__" + n);
  return (
    <div className="pg-agenttools">
      <button className="link" onClick={() => setOpen(!open)} aria-expanded={open}>
        {open ? "▾" : "▸"} Tools the agent can use{S.agentTools ? ` · ${all.length + project.length}` : ""}
      </button>
      {open && (
        <div className="pg-agenttools-body">
          <p className="muted">
            On SpecWright&apos;s agentbridge MCP server, named as {cop ? "GitHub Copilot" : "the agent"} sees
            them. SpecWright&apos;s own tools can also be called through extension_managed by their name.
          </p>
          {!S.agentTools && <span className="spinner" />}
          {TOOL_GROUPS.map(([g, label]) => {
            const ts = all.filter((t) => t.group === g);
            if (!ts.length) return null;
            return (
              <div key={g}>
                <div className="bt-group">{label}</div>
                <ul className="pg-agenttools-list">
                  {ts.map((t) => (
                    <li key={t.name} title={t.desc}>
                      <code className="inline-code" data-nofile>
                        {shown(t.name)}
                      </code>{" "}
                      <span className="muted">{t.title || t.desc}</span>
                    </li>
                  ))}
                </ul>
              </div>
            );
          })}
          {all
            .filter((t) => !TOOL_GROUPS.some(([g]) => g === t.group))
            .map((t) => (
              <div key={t.name}>
                <code className="inline-code" data-nofile>
                  {shown(t.name)}
                </code>{" "}
                <span className="muted">{t.title || t.desc}</span>
              </div>
            ))}
          {withProject ? (
            <div>
              <div className="bt-group">Project tools (through {shown("extension_managed")})</div>
              {project.length ? (
                <ul className="pg-agenttools-list">
                  {project.map((t) => (
                    <li key={t.name} title={t.desc}>
                      <code className="inline-code" data-nofile>
                        {t.name}
                      </code>{" "}
                      <span className="muted">{t.desc}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="muted">No project tools yet.</p>
              )}
            </div>
          ) : (
            <p className="muted">Project tools are not offered: tick “Also offer project and admin tools”.</p>
          )}
        </div>
      )}
    </div>
  );
}

// ToolApply: this conversation is about a tool: take the revision in the
// answer into the tool builder and test it, or read the tool's files again
// when the agent changed them.
function ToolApply({ name, answer, runId, proposed }) {
  const [busy, setBusy] = useState(false);
  const has = !!proposed || /```json[\s\S]*?"(script|command)"\s*:/.test(answer);
  const saved = (S.ws?.extensions || []).some((x) => x.name === name);
  const apply = async () => {
    const draft =
      S.tb?.draft && (S.tb.draft.name === name || S.tb.editing === name)
        ? S.tb.draft
        : (S.ws?.extensions || []).find((x) => x.name === name);
    if (!draft) return toast(`Open the tool ${name} in Tools first.`, "error");
    setBusy(true);
    try {
      const text = proposed
        ? "```json\n" + JSON.stringify({ ...proposed.tool, why: proposed.why }) + "\n```"
        : answer;
      const tr = await backend.ToolFromReply(text, draft);
      if (!tr.reply || proposed) tr.reply = answer;
      await applyToolRevision(tr, name, runId);
    } catch (e) {
      toast(errMsg(e), "error");
    }
    setBusy(false);
  };
  return (
    <div className="pg-toolapply">
      <span>
        <Icon.tool /> {proposed ? "The AI proposes a revision of" : "About the tool"} <b>{name}</b>
        {proposed?.why && <span className="muted"> · {proposed.why}</span>}
        {!has && (
          <span className="muted"> · no revised tool in this answer yet: ask for the whole tool as JSON</span>
        )}
      </span>
      <button className="btn primary" disabled={busy || !has} onClick={apply}>
        {busy ? "Applying…" : "Apply to the tool and test"}
      </button>
      {saved && (
        <button
          className="btn secondary"
          disabled={busy}
          onClick={() => reloadTool(name)}
          title="When the agent edited the tool's files in the project, read them again"
        >
          Reload from its files
        </button>
      )}
      <button
        className="btn secondary"
        onClick={() => {
          S.view = "tools";
          update();
        }}
      >
        Back to the tool
      </button>
    </div>
  );
}

// --- conversations ------------------------------------------------------------
// A reply is a run whose first parent is the turn it answers, so a chain of
// prompt runs is one conversation. A run continuing something else (a flow
// run, a workflow step) starts a conversation of its own, with that run as
// its context.

// the conversation a run belongs to (its turns), or [] for a flow run
function convOf(runs, id) {
  for (const turns of conversations(runs).values()) if (turns.some((t) => t.id === id)) return turns;
  return [];
}

// load a run's events once (the live ones are kept as they arrive)
async function loadEvents(id) {
  if (S.loaded[id]) return;
  S.loaded[id] = true;
  const evs = (await backend.RunEvents(id).catch(() => [])) || [];
  const live = S.events[id] || [],
    seen = new Set(evs.map((e) => e.seq));
  S.events[id] = evs.concat(live.filter((e) => !seen.has(e.seq))).sort((a, b) => a.seq - b.seq);
  update();
}

const answerOf = (evs) =>
  evs
    .filter((e) => e.type === "model.text")
    .map((e) => e.text)
    .join("\n\n");

const sumUsage = (list) =>
  list.reduce(
    (t, u) => {
      for (const k of ["in", "out", "cached", "calls", "premium", "usd", "tools", "toolErr", "ms"])
        t[k] += u[k] || 0;
      t.files.push(...(u.files || []));
      return t;
    },
    { in: 0, out: 0, cached: 0, calls: 0, premium: 0, usd: 0, tools: 0, toolErr: 0, ms: 0, files: [] },
  );

function UsageRows({ u, model }) {
  return (
    <dl className="infotip-rows">
      {[
        model && ["Model", model],
        ["Tokens", `${fmtK(u.in)} in (${fmtK(u.cached)} cached) · ${fmtK(u.out)} out`],
        [isCopilot() ? "AI credits" : "Cost", fmtCredits(u)],
        ["Model calls", String(u.calls)],
        ["Tool calls", u.tools ? `${u.tools}${u.toolErr ? ` · ${u.toolErr} failed` : ""}` : "none"],
        u.files.length ? ["Files changed", u.files.map((f) => f.path.split("/").pop()).join(", ")] : null,
        ["Time", fmtMs(u.ms)],
      ]
        .filter(Boolean)
        .map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
    </dl>
  );
}

// Turn: one question and the AI's answer, with what it ran
function Turn({ t, last, anyRunning }) {
  const evs = S.events[t.id] || [];
  const state = stateOf(evs),
    running = state === "running";
  const answer = answerOf(evs),
    calls = toolCalls(evs),
    u = usageOf(evs);
  const model = [...evs].reverse().find((e) => e.type === "model.request" && e.model)?.model;
  const problems = evs.filter((e) =>
    ["error", "loop.detected", "budget.exceeded", "run.retry", "tools.unreachable"].includes(e.type),
  );
  const ended = evs.some((e) => e.type === "run.end");
  const outside = (t.parents || []).filter((id) => !convOf(pg().runs, t.id).some((x) => x.id === id));
  return (
    <>
      <div className="chat-msg chat-user">
        <div className="chat-avatar chat-avatar-user" aria-hidden="true">
          <Icon.user />
        </div>
        <div className="chat-bubble">
          {outside.length > 0 && (
            <div className="pg-cont">
              <span className="muted">
                <Icon.history /> With the context of:
              </span>
              {outside.map((id) => (
                <span key={id} className="pg-cont-chip" title={id}>
                  {runTitle(id)}
                </span>
              ))}
            </div>
          )}
          <div className="chat-q">{promptOf(t)}</div>
        </div>
      </div>
      <div className="chat-msg chat-assistant">
        <div className="chat-avatar chat-avatar-assistant" aria-hidden="true">
          <Icon.spark />
        </div>
        <div className="chat-bubble">
          <div className="chat-who">
            Assistant
            {t.at && <span className="muted"> · {ago(t.at)}</span>}
            {running ? (
              <span className="muted"> · {answer ? "writing…" : "working…"}</span>
            ) : (
              ["failed", "cancelled", "stopped", "error"].includes(state) && (
                <Pill s={state} label={PROMPT_STATUS_LABEL[state] || state} />
              )
            )}
            {ended && (
              <InfoTip label="What this answer took">
                <div className="infotip-title">This answer</div>
                <UsageRows u={u} model={model} />
              </InfoTip>
            )}
          </div>
          {calls.length > 0 && (
            <details className="pg-calls">
              <summary className="chat-chip">
                <Icon.tool /> {running && !ended ? "Running" : "Ran"} {calls.length} tool
                {calls.length === 1 ? "" : "s"}
                {u.toolErr ? ` · ${u.toolErr} failed` : ""}
              </summary>
              <div className="pg-tools">
                {calls.map((c, k) => (
                  <ToolCall key={c.call.seq} c={c} n={k + 1} runId={t.id} />
                ))}
              </div>
            </details>
          )}
          {problems.map((e) => (
            <div
              key={e.seq}
              className={
                "banner" + (e.type === "run.retry" || e.type === "tools.unreachable" ? "" : " error")
              }
            >
              <Linkify text={e.summary} />
            </div>
          ))}
          {answer ? (
            <div className="rich md" dangerouslySetInnerHTML={{ __html: mdRender(answer) }} />
          ) : running ? (
            <span className="chat-typing" aria-label="Thinking">
              <i />
              <i />
              <i />
            </span>
          ) : (
            ended && <p className="muted">No text answer.</p>
          )}
          {running && <AiProgress evs={evs} />}
          {running && S.permAsks.some((x) => x.run === t.id || !x.run) && (
            <div className="pg-waiting">
              <Icon.shield /> Waiting for your approval to use <b>{S.permAsks[0].tool}</b>.
            </div>
          )}
          {!running && (toolFor()[t.id] || proposedTool(evs)) && (
            <ToolApply
              name={proposedTool(evs)?.tool.name || toolFor()[t.id]}
              answer={answer}
              runId={t.id}
              proposed={proposedTool(evs)}
            />
          )}
          {ended && (
            <div className="pg-turn-acts">
              {answer && <CopyButton text={answer} format="md" />}
              <details className="pg-more">
                <summary className="muted">Details: results folder and full activity</summary>
                <ResultsLink id={t.id} ended={ended} />
                <RunPanel
                  st={{ id: "playground", cmd: "playground", status: state, lastRun: t.id }}
                  evs={evs}
                  running={running}
                />
              </details>
            </div>
          )}
          {last && ended && answer && asksUser(answer) && (
            <div className="chat-follow">
              {["Yes, go ahead", "No, don't do that", "Explain the options first"].map((x) => (
                <button key={x} className="chip" disabled={anyRunning} onClick={() => reply(t.id, x)}>
                  {x}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  );
}

// AskComposer: the message box at the bottom, as in Chats; the run's
// options sit behind "Options"
function AskComposer({ box, last, running, anyRunning }) {
  const p = pg();
  const send = () => (last ? reply(last.id, p.prompt) : run());
  return (
    <div className="chat-composer pg-composer">
      <div className="chat-compose-card">
        {p.parents?.length > 0 && (
          <div className="pg-cont">
            <span className="muted">
              <Icon.history /> With the context of:
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
          </div>
        )}
        <textarea
          ref={box}
          rows={2}
          value={p.prompt}
          aria-label="Your question"
          placeholder={last ? "Ask a follow-up…" : "Ask anything about this project…"}
          onChange={(e) => {
            p.prompt = e.target.value;
            update();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              if (!anyRunning && p.prompt.trim()) send();
            }
          }}
        />
        <div className="chat-compose-row">
          <small className="chat-hint">
            <kbd>Enter</kbd> to send · <kbd>Shift</kbd>+<kbd>Enter</kbd> new line
          </small>
          {running ? (
            <button
              className="btn secondary"
              onClick={() => backend.CancelRun(last.id).catch((e) => toast(errMsg(e), "error"))}
            >
              Stop
            </button>
          ) : (
            <button
              className="btn primary chat-send"
              disabled={anyRunning || !p.prompt.trim()}
              onClick={send}
            >
              <Icon.send /> Send
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// files the AI read in these turns (read_file and the CLIs' own Read/View)
function filesRead(turns) {
  const seen = new Set();
  for (const t of turns)
    for (const c of toolCalls(S.events[t.id] || [])) {
      const name = (c.call.tool || "").replace(/^mcp__agentbridge__/, "");
      if (!/^(read_file|Read|view|read)$/i.test(name)) continue;
      const a = c.call.args || {};
      const f = a.path || a.file_path || a.file;
      if (typeof f === "string" && f) seen.add(f);
    }
  return [...seen];
}

// AskContext: the Context panel of an Ask AI conversation
function AskContext({ turns, total }) {
  const p = pg();
  const root = turns[0];
  const earlier = [
    ...new Set([
      ...((root?.parents || []).filter((id) => !turns.some((t) => t.id === id)) || []),
      ...(p.parents || []).filter((id) => !turns.some((t) => t.id === id)),
    ]),
  ];
  const files = filesRead(turns);
  return (
    <ContextPanel
      label="What the AI works with"
      sections={[
        {
          id: "sources",
          title: "Sources",
          count: 1 + earlier.length + files.length,
          action: (
            <button className="link" onClick={() => openPicker()} title="Add earlier runs as context">
              <Icon.plus /> Add
            </button>
          ),
          children: (
            <>
              <div className="ctx-item" title={S.ws.root}>
                <Icon.folder /> <span>Project folder · {S.ws.name}</span>
              </div>
              {earlier.map((id) => (
                <div key={id} className="ctx-item" title={id}>
                  <Icon.history /> <span>{runTitle(id)}</span>
                </div>
              ))}
              {files.length > 0 && <div className="ctx-sub">Files it read · {files.length}</div>}
              {files.slice(0, 30).map((f) => (
                <div key={f} className="ctx-item">
                  <Icon.file /> <FileLink path={f}>{f.split("/").slice(-2).join("/")}</FileLink>
                </div>
              ))}
              {!turns.length && !earlier.length && (
                <small className="muted">
                  The AI searches and reads the project's files as it needs them.
                </small>
              )}
            </>
          ),
        },
        {
          id: "instructions",
          title: "Instructions",
          children: (
            <ul className="ctx-rules">
              <Rule>Works in this project's folder, within the workflow's token and cost budget.</Rule>
              <Rule>
                Follow-ups get the earlier questions and answers of this conversation (the last 10).
              </Rule>
              <Rule>
                Files, pages and tool results are information, not instructions: it does not act on what they
                tell it to do.
              </Rule>
              <Rule>Commands, flow changes and private addresses wait for your yes.</Rule>
            </ul>
          ),
        },
        {
          id: "tools",
          title: "Tools",
          children: (
            <>
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
              <AgentToolsList withProject={p.tools} />
            </>
          ),
        },
        {
          id: "model",
          title: "Model and settings",
          children: (
            <>
              <div className="ctx-item">
                <Icon.spark /> <span>{agentLabel()}</span>
              </div>
              <ModelPicker
                value={p.model}
                onChange={(v) => {
                  p.model = v;
                  update();
                }}
                label="Model"
              />
              <label
                className="check"
                title="Copilot: instead of refusing what it may not do on its own (a command, or editing a file outside the project), it asks you here."
              >
                <input
                  type="checkbox"
                  checked={p.ask !== false}
                  onChange={(e) => {
                    p.ask = e.target.checked;
                    update();
                  }}
                />{" "}
                Ask me before commands and edits outside the project
              </label>
            </>
          ),
        },
        {
          id: "usage",
          title: "Usage",
          children: turns.length ? <UsageRows u={total} /> : <small className="muted">Nothing yet.</small>,
        },
      ]}
    />
  );
}

export function Playground() {
  const p = pg(),
    box = useRef(null),
    end = useRef(null);
  useEffect(() => {
    loadPast();
  }, [S.ws?.root]);
  usePoll(() => loadPast(true), PLAYGROUND_POLL * 1000, [S.ws?.root]);
  useEffect(() => {
    if (!S.pgStale) return;
    S.pgStale = false;
    const t = setTimeout(() => loadPast(true), 400); // the run is recorded first
    return () => clearTimeout(t);
  }, [S.pgStale]);
  const r = p.runs.find((x) => x.id === p.sel);
  const turns = r && r.kind !== "flow" ? convOf(p.runs, r.id) : [];
  const last = turns[turns.length - 1];
  const lastEvs = last ? S.events[last.id] || [] : [];
  useEffect(() => {
    turns.forEach((t) => loadEvents(t.id));
  }, [turns.map((t) => t.id).join(",")]);
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [last?.id, lastEvs.length]);
  if (!S.ws) return <NeedWorkspace what="Ask AI" />;
  for (const [id, fst] of Object.entries(S.flowRuns))
    if (!p.runs.some((x) => x.id === id))
      p.runs.unshift({ id, prompt: "", at: fst.started, state: fst.status, kind: "flow", flow: fst.flowId });
  const running = last ? stateOf(lastEvs) === "running" : false;
  const anyRunning = p.runs.some((x) => x.kind !== "flow" && stateOf(S.events[x.id] || []) === "running");
  const convs = conversations(p.runs);
  // the list: one row per conversation (its first question), and flow runs
  const items = [
    ...[...convs.entries()].map(([root, ts]) => ({
      ...ts[0],
      id: root,
      turns: ts,
      last: ts[ts.length - 1],
      at: ts[ts.length - 1].at,
    })),
    ...p.runs.filter((x) => x.kind === "flow"),
  ].sort((a, b) => String(b.at).localeCompare(String(a.at)));
  const title = turns.length ? (turns[0].title || promptOf(turns[0])).split("\n")[0] : "";
  const newConversation = () => {
    p.sel = "";
    p.prompt = "";
    p.parents = [];
    update();
    setTimeout(() => box.current?.focus(), 0);
  };
  const total = sumUsage(turns.map((t) => usageOf(S.events[t.id] || [])));
  return (
    <div className={"files chats askai" + (r?.kind !== "flow" && panelOpen() ? " with-panel" : "")}>
      <Pane
        k="playground"
        v="--pane-l"
        pane=".ftree"
        min={200}
        max={560}
        label="Resize the conversation list"
      />
      <nav className="ftree" aria-label="Ask AI conversations">
        <ListHead title="Ask AI" count={convs.size || null}>
          <RefreshButton label="Refresh the list" every={PLAYGROUND_POLL} onRefresh={() => loadPast(true)} />
          <button className="btn primary" onClick={newConversation}>
            <Icon.plus /> New
          </button>
        </ListHead>
        {!items.length && (
          <EmptyState compact title="No conversations yet">
            Your questions to the AI about this project appear here, each with its follow-ups.
          </EmptyState>
        )}
        {items.length > 0 && <RunListBar area="playground" runs={items} />}
        {visibleRuns(
          "playground",
          items,
          (x) =>
            (x.turns || [x]).map((t) => (t.title || "") + " " + (t.prompt || "")).join(" ") +
            " " +
            (x.flow || ""),
        ).map(([g, list]) => (
          <RunGroup key={g || "-"} area="playground" name={g} count={list.length}>
            {list.map((x) => {
              const fst = x.kind === "flow" ? S.flowRuns[x.id] : null;
              const active = x.kind === "flow" ? x.id === p.sel : x.turns.some((t) => t.id === p.sel);
              const lst = x.kind === "flow" ? null : stateOf(S.events[x.last.id] || []);
              return (
                <button
                  key={x.id}
                  className={
                    "run-item" + (active ? " active" : "") + (x.kind === "flow" ? " pg-flow-item" : "")
                  }
                  onClick={() => select(x.kind === "flow" ? x.id : x.last.id)}
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
                      <span className="pg-item-text">{(x.title || promptOf(x)).split("\n")[0]}</span>
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
                    ) : lst === "running" || lst === "failed" || lst === "cancelled" ? (
                      <Pill s={lst} label={PROMPT_STATUS_LABEL[lst]} />
                    ) : null}
                  </span>
                  <small>
                    {x.kind === "flow"
                      ? new Date(x.at).toLocaleString()
                      : `${x.turns.length} question${x.turns.length === 1 ? "" : "s"} · ${ago(x.at)}`}
                  </small>
                </button>
              );
            })}
          </RunGroup>
        ))}
      </nav>
      {r?.kind === "flow" ? (
        <div className="fview">
          <FlowWatch r={r} evs={S.events[r.id] || []} />
        </div>
      ) : (
        <section className="chat-main">
          <header className="chat-head">
            <div className="chat-head-title">
              <Crumbs
                items={[
                  ["Ask AI", turns.length ? newConversation : null],
                  ...(turns.length ? [[title, null]] : []),
                ]}
              />
              <h2>{title || "New conversation"}</h2>
              <div className="chat-head-meta">
                <span>
                  {agentLabel()} · {S.ws.name}
                </span>
                {turns.length > 0 && (
                  <span>
                    {turns.length} question{turns.length === 1 ? "" : "s"}
                  </span>
                )}
              </div>
            </div>
            <div className="chat-head-acts">
              {turns.length > 0 && (
                <InfoTip label="What this conversation took" align="end">
                  <div className="infotip-title">This conversation</div>
                  <UsageRows u={total} />
                </InfoTip>
              )}
              <PanelToggle />
            </div>
          </header>
          <div className="chat-scroll">
            {!turns.length && (
              <div className="chat-empty pg-empty">
                <h3>Ask AI about this project</h3>
                <p className="muted">
                  The AI reads, searches and edits the project's files and runs commands, asking you before
                  anything risky. Follow-up questions continue the same conversation.
                </p>
                <div className="chips">
                  {EXAMPLES.map((x) => (
                    <Chip
                      key={x}
                      onClick={() => {
                        p.prompt = x;
                        update();
                        box.current?.focus();
                      }}
                    >
                      {x}
                    </Chip>
                  ))}
                </div>
              </div>
            )}
            {turns[0] && <Thread r={turns[0]} />}
            {turns.map((t) => (
              <Turn key={t.id} t={t} last={t === last} anyRunning={anyRunning} />
            ))}
            <div ref={end} />
          </div>
          <AskComposer box={box} last={last} running={running} anyRunning={anyRunning} />
        </section>
      )}
      {r?.kind !== "flow" && panelOpen() && <AskContext turns={turns} total={total} />}
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
            <span className="pg-turn-kind">{RUN_KIND_LONG[d.kind] || "Run"}</span>
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
                <span className="pg-turn-kind">{RUN_KIND_LONG[d.kind] || "Run"}</span>
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

// Back / Forward: the run open in the Playground (navigation.js)
registerPlace("playground", { key: () => S.pg?.sel || "", restore: (id) => select(id) });
