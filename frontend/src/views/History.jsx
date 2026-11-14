import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, fmtK } from "../store.js";
import { Pill } from "../ui.jsx";
import { RunPanel } from "./Workflow.jsx";
import { Icon } from "../ui.jsx";
import { continueInPlayground } from "./Playground.jsx";
import { RunListBar, RunGroup, PickBox, RunActions, visibleRuns } from "../RunHousekeeping.jsx";

const KIND = { playground: "Prompt", workflow: "Workflow", flow: "Flow" };
const kindOf = (r) => r.kind || (r.step === "flow" ? "flow" : r.workflow === "playground" ? "playground" : "workflow");

let timer;
function stop() { clearTimeout(timer); S.history.playing = false; }

function play() {
  const h = S.history, evs = S.events[h.sel] || [];
  if (h.pos >= evs.length) h.pos = 0;
  h.playing = true; update();
  const tick = () => {
    if (!h.playing) return;
    h.pos++; update();
    if (h.pos >= evs.length) { h.playing = false; return update(); }
    const gap = (Date.parse(evs[h.pos].ts) - Date.parse(evs[h.pos - 1].ts)) / (h.speed || 4);
    timer = setTimeout(tick, Math.max(30, Math.min(gap, 1500)));
  };
  tick();
}

async function select(id) {
  stop();
  S.history.sel = id;
  if (!S.loaded[id] || !S.events[id]?.length) { S.events[id] = await backend.RunEvents(id).catch((e) => (toast(errMsg(e)), [])); S.loaded[id] = true; }
  S.history.pos = S.events[id].length; update();
}

export function History() {
  const h = S.history;
  useEffect(() => { backend.Runs(500).then((r) => { h.runs = r || []; update(); const first = h.runs.find((x) => !x.archived); if (!h.sel && first) select(first.id); }, (e) => toast(errMsg(e))); return stop; }, [S.ws?.root]);
  const run = h.runs.find((r) => r.id === h.sel), evs = S.events[h.sel] || [];
  const shown = evs.slice(0, h.pos);
  return (
    <div className="hist">
      <nav className="ftree" aria-label="Run history"><h1 style={{ padding: "0 14px 8px" }}>History</h1>
        {!h.runs.length && <p className="muted" style={{ padding: "0 14px" }}>No runs recorded{S.ws ? " for this project" : ""} yet.</p>}
        {h.runs.length > 0 && <RunListBar area="history" runs={h.runs} />}
        {visibleRuns("history", h.runs, (r) => (r.title || "") + " " + (r.ask || "") + " " + (r.workflow || "") + " " + (r.step || "")).map(([g, list]) => <RunGroup key={g || "-"} area="history" name={g} count={list.length}>
        {list.map((r) => (
          <button key={r.id} className={"run-item" + (r.id === h.sel ? " active" : "")} onClick={() => select(r.id)}>
            <PickBox area="history" id={r.id} />
            <span className="run-item-top"><span className="hist-title"><span className={"pg-turn-kind k-" + kindOf(r)}>{KIND[kindOf(r)]}</span> <b>{r.title || (kindOf(r) === "flow" ? r.workflow : r.step)}</b></span><Pill s={r.state} /></span>
            <small>{new Date(r.started).toLocaleString()} · {r.agent}{r.model ? " · " + r.model : ""}</small>
            <small>{fmtK(r.usage.input_tokens + r.usage.output_tokens)} tokens · {r.usage.tool_calls} tools · {r.usage.files_created + r.usage.files_modified} files{r.usage.cost_usd ? ` · $${r.usage.cost_usd.toFixed(3)}` : ""}{r.usage.premium_requests ? ` · ${r.usage.premium_requests} premium` : ""}</small>
          </button>
        ))}</RunGroup>)}
      </nav>
      <div className="fview">
        {run ? <div className="replay">
          <header className="view-head"><h1>Replay · {run.title || run.step}</h1><span className="muted">{run.id}{run.reason ? " · " + run.reason : ""}</span>
            <div className="head-controls"><RunActions area="history" runs={h.runs} run={run} /><button className="btn secondary" onClick={() => continueInPlayground([run.id])}><Icon.history /> Continue in the playground</button></div></header>
          {run.ask && <div className="pg-you"><span className="pg-you-tag">{kindOf(run) === "flow" ? "Inputs" : kindOf(run) === "workflow" ? "Input" : "You"}</span><div>{run.ask}</div></div>}
          <div className="replay-bar card">
            <button className="btn primary" onClick={() => (h.playing ? (stop(), update()) : play())}>{h.playing ? "Pause" : h.pos >= evs.length ? "Replay" : "Play"}</button>
            <label className="field inline"><span>Speed</span><select value={h.speed || 4} onChange={(e) => { h.speed = +e.target.value; update(); }}>{[1, 2, 4, 8, 32].map((s) => <option key={s} value={s}>{s}×</option>)}</select></label>
            <div className="scrubber" style={{ flex: 1 }}><input type="range" min="0" max={evs.length} value={h.pos} aria-label="Replay position" onChange={(e) => { stop(); h.pos = +e.target.value; update(); }} /><b>{h.pos}/{evs.length}</b></div>
          </div>
          <RunPanel st={{ id: run.step, cmd: run.step, status: h.pos >= evs.length ? run.state : "running", lastRun: run.id }} evs={shown} running={h.playing} />
        </div> : <div className="empty"><h3>Pick a run</h3><p>Every step run is recorded (redacted) and can be replayed here.</p></div>}
      </div>
    </div>
  );
}
