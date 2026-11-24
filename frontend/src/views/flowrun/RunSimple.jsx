// Run flows, the simple view: where the run is, how long it has taken and
// about how long is left, in plain words, with a little fun while you wait.
// The details (steps, outputs, the activity log) are one click away.
import { useEffect, useState } from "react";
import { fmtMs } from "../../store.js";

// the run's steps a person counts: not the Inputs (start) step
export const workSteps = (st) => (st.steps || []).filter((s) => s.type !== "start");

// how far the run is, 0…1: finished steps count whole, the one in progress
// half (or by its loop's items), across every pass and item of the run
export function runProgress(st) {
  if (st.status === "done") return 1;
  const work = workSteps(st);
  if (!work.length) return 0;
  let done = 0;
  for (const s of work) {
    if (["done", "skipped", "failed"].includes(s.status)) done += 1;
    else if (s.status === "running" || s.status === "review")
      done += s.iterations > 1 ? Math.min(1, (s.iteration - 1) / s.iterations) : 0.5;
  }
  const units = Math.max(1, (st.passes || 1) * (st.items || 1)),
    unit = ((st.pass || 1) - 1) * (st.items || 1) + (st.item || 1) - 1;
  return Math.min(0.99, (unit + done / work.length) / units);
}

const median = (xs) => {
  const s = [...xs].sort((a, b) => a - b);
  return s.length ? s[Math.floor(s.length / 2)] : 0;
};

// usualMs: how long this flow's finished runs took (the median of the last
// ones), 0 when it has none
export function usualMs(runs, flowId, exceptId) {
  const ds = (runs || [])
    .filter((x) => x.workflow === flowId && x.id !== exceptId && x.state === "done" && x.started && x.ended)
    .slice(0, 10)
    .map((x) => new Date(x.ended) - new Date(x.started))
    .filter((d) => d > 0);
  return median(ds);
}

// estimate of what is left: from the usual time when known, else from the
// share done so far (once there is enough to go on)
export function leftMs(st, elapsed, usual) {
  if (usual > 0) return Math.max(0, usual - elapsed);
  const p = runProgress(st);
  if (p < 0.15 || elapsed < 5000) return null;
  return (elapsed / p) * (1 - p);
}

// a step in plain words
function doing(s, st) {
  if (!s) return st.status === "scheduled" ? "Waiting for its start time" : "Getting ready";
  const loop = s.iterations > 1 ? ` (${s.iteration} of ${s.iterations})` : "";
  if (s.status === "review") return `Waiting for you: ${s.title}`;
  switch (s.type) {
    case "ai":
    case "improve":
      return `Thinking: ${s.title}${loop}`;
    case "loop":
      return `Going through the items${loop}`;
    case "output":
      return `Saving the results: ${s.title}`;
    case "decision":
      return `Deciding which way to go`;
    case "context":
      return `Gathering what it needs to know`;
    default:
      return `Working on: ${s.title}${loop}`;
  }
}

const WAITING = [
  ["☕", "Perfect time for a coffee. We'll keep an eye on things."],
  ["🍪", "Grab a biscuit. Maybe two. Nobody's counting."],
  ["🧘", "Stretch, breathe, admire your desk plant."],
  ["🚶", "Go for a little walk; your flow won't miss you."],
  ["📞", "Long enough to call someone you like."],
];
const QUIPS = [
  "Teaching the robots their manners…",
  "Counting the bytes. Twice, to be sure.",
  "Polishing the results to a shine…",
  "Asking politely, as one does.",
  "Lining up the ducks. They are wriggly today.",
  "Reading the small print so you don't have to.",
  "Tidying up after itself…",
];

// mood: the picture and the line for where the run is
export function mood(st, left, elapsed, usual, tick) {
  const quip = QUIPS[Math.floor(tick / 8) % QUIPS.length];
  switch (st.status) {
    case "scheduled":
      return {
        art: "⏰",
        line: `It starts at ${new Date(st.options?.startAt).toLocaleString()}. Keep the app open.`,
      };
    case "review":
      return { art: "👋", line: "Your turn! The flow is waiting for you to have a look.", hot: true };
    case "done": {
      const faster = usual > 0 && elapsed < usual * 0.85;
      return {
        art: "🎉",
        line: faster
          ? `All done, and ${fmtMs(usual - elapsed)} quicker than usual. Nice.`
          : `All done in ${fmtMs(elapsed)}.`,
        party: true,
      };
    }
    case "failed":
      return { art: "🙈", line: "Something tripped along the way. The details show where." };
    case "stopped":
      return { art: "🛑", line: "Stopped. Nothing more will happen." };
    case "interrupted":
      return { art: "🔌", line: "The app closed while this was running, so it stopped part way." };
  }
  if (usual > 0 && elapsed > usual * 1.5)
    return { art: "🐢", line: "Taking longer than usual. Good things come to those who wait.", sub: quip };
  if (left != null && left > 5 * 60000) {
    const [art, line] = WAITING[Math.floor(tick / 20) % WAITING.length];
    return { art, line, sub: quip, cup: art === "☕" };
  }
  if (left != null && left > 2 * 60000)
    return { art: "🧘", line: "A few minutes yet: stretch your legs.", sub: quip };
  if (left != null && left < 30000) return { art: "🏁", line: "Almost there. Don't go anywhere!", sub: quip };
  return { art: "🛠️", line: quip };
}

export function RunSimple({ st, runs, onDetails, onChat }) {
  const live = ["running", "review", "scheduled"].includes(st.status);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!live) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [live]);
  const started = st.started ? new Date(st.started).getTime() : now;
  const ended = st.ended ? new Date(st.ended).getTime() : now;
  const elapsed = st.status === "scheduled" ? 0 : Math.max(0, (live ? now : ended) - started);
  const usual = usualMs(runs, st.flowId, st.id);
  const left = live && st.status !== "scheduled" ? leftMs(st, elapsed, usual) : null;
  const p = runProgress(st);
  const pct = Math.round(p * 100);
  const steps = workSteps(st);
  const cur = steps.find((s) => s.status === "review") || steps.find((s) => s.status === "running");
  const idx = cur ? steps.indexOf(cur) + 1 : steps.filter((s) => s.status !== "pending").length;
  const m = mood(st, left, elapsed, usual, Math.floor(now / 1000));
  const runner =
    st.status === "done" ? "🏆" : st.status === "failed" ? "🤕" : st.status === "review" ? "🙋" : "🏃";
  return (
    <section className={"card frs" + (m.hot ? " frs-hot" : "")} aria-label="Where the run is">
      <div className="frs-top">
        <div
          className={"frs-art" + (m.party ? " frs-party" : "") + (m.cup ? " frs-cup" : "")}
          aria-hidden="true"
        >
          {m.cup && (
            <span className="frs-steam">
              <i />
              <i />
              <i />
            </span>
          )}
          {m.art}
        </div>
        <div className="frs-words">
          <div className="frs-line">{m.line}</div>
          {m.sub && <div className="frs-sub muted">{m.sub}</div>}
        </div>
      </div>
      <div
        className="frs-track"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
        aria-label="Progress"
      >
        <div className={"frs-fill s-" + st.status} style={{ width: pct + "%" }} />
        <span
          className={"frs-runner" + (live ? " moving" : "")}
          style={{ left: `calc(${pct}% - 12px)` }}
          aria-hidden="true"
        >
          {runner}
        </span>
        <span className="frs-flag" aria-hidden="true">
          🏁
        </span>
      </div>
      <div className="frs-facts">
        <div>
          <small className="muted">Where we are</small>
          <b>{st.status === "done" ? "Finished" : doing(cur, st)}</b>
          {steps.length > 0 && st.status !== "done" && (
            <small className="muted">
              Step {Math.max(1, Math.min(idx, steps.length))} of {steps.length} · {pct}%
            </small>
          )}
        </div>
        <div>
          <small className="muted">{live ? "Time so far" : "Time taken"}</small>
          <b>{st.status === "scheduled" ? "–" : fmtMs(elapsed)}</b>
        </div>
        <div>
          <small className="muted">{live ? "About how long is left" : "Usually takes"}</small>
          <b>
            {live
              ? st.status === "review"
                ? "Up to you"
                : left == null
                  ? "Working it out…"
                  : left < 15000
                    ? "Moments"
                    : "~" + fmtMs(Math.round(left / 5000) * 5000)
              : usual
                ? fmtMs(usual)
                : "First run"}
          </b>
        </div>
      </div>
      {steps.length > 1 && steps.length <= 12 && (
        <ol className="frs-dots" aria-label="Steps">
          {steps.map((s) => (
            <li key={s.id} className={"s-" + s.status} title={`${s.title}: ${s.status}`}>
              <span />
            </li>
          ))}
        </ol>
      )}
      {onChat && (
        <div className="frs-chat">
          <button className="btn primary" onClick={onChat}>
            💬 Keep chatting
          </button>
          <span className="muted">
            Ask follow-ups in the same conversation: what it gathered is kept, not fetched again.
          </span>
        </div>
      )}
      <div className="frs-more">
        <button className="link" onClick={onDetails}>
          Show the details: every step, its output and the activity log
        </button>
      </div>
    </section>
  );
}
