import { useEffect, useState } from "react";
import { RUN_KIND, runKind } from "../labels.js";
import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg, fmtK } from "../store.js";
import { Pill, RefreshButton, usePoll } from "../ui.jsx";
import { RunPanel } from "../components/RunPanel.jsx";
import { Icon } from "../ui.jsx";
import { continueInPlayground } from "./Playground.jsx";
import { ago } from "../time.js";
import { register } from "../shortcuts.js";
import { CopyButton } from "../CopyButton.jsx";
import { registerPlace } from "../navigation.js";
import { RunListBar, RunGroup, PickBox, RunActions, visibleRuns, hk } from "../RunHousekeeping.jsx";

let timer;
function stop() {
  clearTimeout(timer);
  S.history.playing = false;
}

function play() {
  const h = S.history,
    evs = S.events[h.sel] || [];
  if (h.pos >= evs.length) h.pos = 0;
  h.playing = true;
  update();
  const tick = () => {
    if (!h.playing) return;
    h.pos++;
    update();
    if (h.pos >= evs.length) {
      h.playing = false;
      return update();
    }
    const gap = (Date.parse(evs[h.pos].ts) - Date.parse(evs[h.pos - 1].ts)) / (h.speed || 4);
    timer = setTimeout(tick, Math.max(30, Math.min(gap, 1500)));
  };
  tick();
}

async function select(id) {
  stop();
  S.history.sel = id;
  if (!S.loaded[id] || !S.events[id]?.length) {
    S.events[id] = await backend.RunEvents(id).catch((e) => (toast(errMsg(e), "error"), []));
    S.loaded[id] = true;
  }
  S.history.pos = S.events[id].length;
  update();
}

// run ids are a timestamp plus a UUID: show the start and the end
const shortId = (id) => (id && id.length > 22 ? id.slice(0, 10) + "…" + id.slice(-6) : id);

// A run's usage can be missing or partial (still running, or stopped before
// its final usage event): count what is there, and say "in progress" otherwise.
const num = (v) => (Number.isFinite(+v) ? +v : 0);
function usageLine(r) {
  const u = r.usage || {},
    tokens = num(u.input_tokens) + num(u.output_tokens);
  if (!tokens && !num(u.tool_calls) && ["running", "paused", "waiting"].includes(r.state))
    return "in progress";
  return (
    `${fmtK(tokens)} tokens · ${num(u.tool_calls)} tools · ${num(u.files_created) + num(u.files_modified)} files` +
    (num(u.cost_usd) ? ` · $${num(u.cost_usd).toFixed(3)}` : "") +
    (num(u.premium_requests) ? ` · ${num(u.premium_requests)} premium` : "")
  );
}

// Space plays or pauses the replay, ← and → step through it (not while typing)
function useReplayKeys(evCount) {
  useEffect(
    () =>
      register({
        id: "replay",
        scope: "history",
        keys: [" ", "arrowleft", "arrowright"],
        help: [
          [" ", "Play or pause the replay"],
          ["arrowleft", "Previous event"],
          ["arrowright", "Next event"],
        ],
        run: (e) => {
          const h = S.history;
          // a focused button or the scrubber keeps its own Space and arrows
          if (!h?.sel || e.target.closest?.("button, input[type=range]")) return false;
          e.preventDefault();
          if (e.key === " ") {
            if (h.playing) {
              stop();
              update();
            } else play();
            return;
          }
          stop();
          h.pos = e.key === "ArrowLeft" ? Math.max(0, h.pos - 1) : Math.min(evCount, h.pos + 1);
          update();
        },
      }),
    [evCount],
  );
}

// loadRuns reads the run list again (and the open run's events, which grow
// while it runs); it is also the list's Refresh button.
// a search snippet: the matched words (between \x01 and \x02) marked
function Snippet({ text }) {
  // "a \x01b\x02 c" → ["a ", "b", " c"]: odd parts are the matches
  const parts = String(text || "")
    .split(String.fromCharCode(1))
    .flatMap((p, i) => (i ? p.split(String.fromCharCode(2)) : [p]));
  return (
    <span className="hist-snippet">
      {parts.map((p, i) => (i % 2 ? <mark key={i}>{p}</mark> : <span key={i}>{p}</span>))}
    </span>
  );
}

// "Found inside runs": the search box also looks through what runs did
// (their inputs, steps, tool output, reviews), in the history database
function ContentHits({ q }) {
  const [res, setRes] = useState({ q: "", hits: [], error: "" });
  const query = q.trim();
  useEffect(() => {
    if (query.length < 2) return;
    const t = setTimeout(
      () =>
        backend
          .SearchHistory(query, 30)
          .then((hits) => setRes({ q: query, hits: hits || [], error: "" }))
          .catch((e) => setRes({ q: query, hits: [], error: errMsg(e) })),
      300,
    );
    return () => clearTimeout(t);
  }, [query, S.history.runs.length]);
  if (query.length < 2 || res.q !== query) return null;
  if (res.error) return <p className="muted hist-hits-none">{res.error}</p>;
  if (!res.hits.length)
    return <p className="muted hist-hits-none">Nothing inside the runs matches “{query}”.</p>;
  const h = S.history;
  return (
    <div className="hist-hits" role="group" aria-label="Found inside runs">
      <div className="hist-hits-head muted">
        Found inside {res.hits.length} run{res.hits.length === 1 ? "" : "s"}
      </div>
      {res.hits.map(({ run: r, snippet, matches, field }) => (
        <button
          key={r.id}
          className={"run-item" + (r.id === h.sel ? " active" : "")}
          onClick={() => {
            if (!h.runs.some((x) => x.id === r.id)) h.runs = [...h.runs, r]; // older than the list shows
            select(r.id);
          }}
        >
          <span className="run-item-top">
            <span className="hist-title">
              <span className={"pg-turn-kind k-" + runKind(r)}>{RUN_KIND[runKind(r)]}</span>{" "}
              <b>{r.title || (runKind(r) === "flow" ? r.workflow : r.step)}</b>
            </span>
            <Pill s={r.state} />
          </span>
          <small>
            <Snippet text={snippet} />
          </small>
          <small className="muted">
            {ago(r.started)} · {matches} match{matches === 1 ? "" : "es"}
            {field && field !== "run" ? ` · in ${field}` : ""}
          </small>
        </button>
      ))}
    </div>
  );
}

// how often the list refreshes by itself (seconds)
const HISTORY_POLL = 30;

export async function loadRuns(again) {
  const h = S.history;
  try {
    h.runs = (await backend.Runs(500)) || [];
  } catch (e) {
    return toast(errMsg(e), "error");
  }
  update();
  const first = h.runs.find((x) => !x.archived);
  if (!h.sel && first) return select(first.id);
  if (again && h.sel && !h.playing) {
    S.loaded[h.sel] = false;
    await select(h.sel);
  }
}

export function History() {
  const h = S.history;
  useEffect(() => {
    loadRuns();
    return stop;
  }, [S.ws?.root]);
  usePoll(() => loadRuns(), HISTORY_POLL * 1000, [S.ws?.root]);
  const run = h.runs.find((r) => r.id === h.sel),
    evs = S.events[h.sel] || [];
  useReplayKeys(evs.length);
  const shown = evs.slice(0, h.pos);
  return (
    <div className="hist">
      <Pane k="history" v="--pane-l" pane=".ftree" min={220} max={560} label="Resize the run list" />
      <nav className="ftree" aria-label="Run history">
        <h1 className="list-head" style={{ padding: "0 14px 8px" }}>
          History{" "}
          <RefreshButton
            label="Refresh the run history"
            every={HISTORY_POLL}
            onRefresh={() => loadRuns(true)}
          />
        </h1>
        {!h.runs.length && (
          <div className="empty-mini">
            <p className="muted">
              No runs recorded{S.ws ? " for this project" : ""} yet. Runs of workflow steps, flows and
              playground prompts show up here.
            </p>
            <div className="row-actions">
              <button
                className="btn secondary"
                onClick={() => {
                  S.view = "workflow";
                  update();
                }}
              >
                Run a workflow step
              </button>
              <button
                className="btn secondary"
                onClick={() => {
                  S.view = "flowrun";
                  update();
                }}
              >
                Run a flow
              </button>
            </div>
          </div>
        )}
        {h.runs.length > 0 && <RunListBar area="history" runs={h.runs} />}
        <ContentHits q={hk("history").q || ""} />
        {visibleRuns(
          "history",
          h.runs,
          (r) => (r.title || "") + " " + (r.ask || "") + " " + (r.workflow || "") + " " + (r.step || ""),
        ).map(([g, list]) => (
          <RunGroup key={g || "-"} area="history" name={g} count={list.length}>
            {list.map((r) => (
              <button
                key={r.id}
                className={"run-item" + (r.id === h.sel ? " active" : "")}
                onClick={() => select(r.id)}
              >
                <PickBox area="history" id={r.id} />
                <span className="run-item-top">
                  <span className="hist-title">
                    <span className={"pg-turn-kind k-" + runKind(r)}>{RUN_KIND[runKind(r)]}</span>{" "}
                    <b>{r.title || (runKind(r) === "flow" ? r.workflow : r.step)}</b>
                    {r.trigger && (
                      <span
                        className="badge badge-sched"
                        style={{ marginLeft: 6 }}
                        title={"Started by " + (r.triggerName || "a trigger")}
                      >
                        {r.trigger}
                      </span>
                    )}
                  </span>
                  <Pill s={r.state} />
                </span>
                <small title={new Date(r.started).toLocaleString()}>
                  {ago(r.started)} · {r.agent}
                  {r.model ? " · " + r.model : ""}
                </small>
                <small>{usageLine(r)}</small>
              </button>
            ))}
          </RunGroup>
        ))}
      </nav>
      <div className="fview">
        {run ? (
          <div className="replay">
            <header className="view-head">
              <h1>Replay · {run.title || run.step}</h1>
              <span className="muted run-id" title={run.id}>
                {shortId(run.id)} <CopyButton text={run.id} />
                {run.reason ? " · " + run.reason : ""}
              </span>
              <div className="head-controls">
                <RunActions area="history" runs={h.runs} run={run} />
                <button className="btn secondary" onClick={() => continueInPlayground([run.id])}>
                  <Icon.history /> Continue in the playground
                </button>
              </div>
            </header>
            {run.ask && (
              <div className="pg-you">
                <span className="pg-you-tag">
                  {runKind(run) === "flow" ? "Inputs" : runKind(run) === "workflow" ? "Input" : "You"}
                </span>
                <div>{run.ask}</div>
              </div>
            )}
            <div className="replay-bar card">
              <button
                className="btn secondary"
                aria-label="Previous event"
                title="Previous event (←)"
                disabled={h.pos <= 0}
                onClick={() => {
                  stop();
                  h.pos = Math.max(0, h.pos - 1);
                  update();
                }}
              >
                ‹
              </button>
              <button
                className="btn primary"
                title="Play or pause (Space)"
                onClick={() => (h.playing ? (stop(), update()) : play())}
              >
                {h.playing ? "Pause" : h.pos >= evs.length ? "Replay" : "Play"}
              </button>
              <button
                className="btn secondary"
                aria-label="Next event"
                title="Next event (→)"
                disabled={h.pos >= evs.length}
                onClick={() => {
                  stop();
                  h.pos = Math.min(evs.length, h.pos + 1);
                  update();
                }}
              >
                ›
              </button>
              <label className="field inline">
                <span>Speed</span>
                <select
                  value={h.speed || 4}
                  onChange={(e) => {
                    h.speed = +e.target.value;
                    update();
                  }}
                >
                  {[1, 2, 4, 8, 32].map((s) => (
                    <option key={s} value={s}>
                      {s}×
                    </option>
                  ))}
                </select>
              </label>
              <div className="scrubber" style={{ flex: 1 }}>
                <input
                  type="range"
                  min="0"
                  max={evs.length}
                  value={h.pos}
                  aria-label="Replay position"
                  aria-valuetext={`Event ${h.pos} of ${evs.length}${h.pos ? ": " + (evs[h.pos - 1]?.type || "") : ""}`}
                  onChange={(e) => {
                    stop();
                    h.pos = +e.target.value;
                    update();
                  }}
                />
                <b>
                  {h.pos}/{evs.length}
                </b>
                {h.pos > 0 && <span className="muted replay-at">{evs[h.pos - 1]?.type}</span>}
              </div>
            </div>
            <RunPanel
              st={{
                id: run.step,
                cmd: run.step,
                status: h.pos >= evs.length ? run.state : "running",
                lastRun: run.id,
              }}
              evs={shown}
              running={h.playing}
            />
          </div>
        ) : (
          <div className="empty">
            <h3>Pick a run</h3>
            <p>Every step run is recorded (redacted) and can be replayed here.</p>
          </div>
        )}
      </div>
    </div>
  );
}

// Back / Forward: the run being replayed (navigation.js)
registerPlace("history", { key: () => S.history?.sel || "", restore: (id) => select(id) });
