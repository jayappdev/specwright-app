// Run flows: fixing or improving one step with AI, and the banner of a trial run.
import { ModelPicker } from "../../ModelPicker.jsx";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg } from "../../store.js";
import { AiProgress } from "../../AiProgress.jsx";
import { LIVE, frs, openRun } from "../FlowRun.jsx";

// --- fixing or improving one step with AI ----------------------------------------------

export const canFix = (st, s) =>
  !LIVE.includes(st.status) && s.type !== "start" && ["failed", "done"].includes(s.status);

export function openFix(st, s) {
  const r = frs();
  r.fix =
    r.fix?.runId === st.id && r.fix?.stepId === s.id
      ? null
      : {
          runId: st.id,
          stepId: s.id,
          prompt: "",
          expected: "",
          model: r.fix?.model || "",
          busy: false,
          res: null,
          error: "",
        };
  update();
}

const nodeSettings = (n) => {
  const c = { ...n };
  for (const k of ["id", "x", "y"]) delete c[k];
  return JSON.stringify(c, null, 2);
};

export function StepFixPanel({ st, s }) {
  const r = frs(),
    fx = r.fix,
    failed = s.status === "failed";
  const set = (k, v) => {
    fx[k] = v;
    update();
  };
  const suggest = async () => {
    Object.assign(fx, { busy: true, error: "", res: null });
    update();
    try {
      fx.res = await backend.FixFlowStep({
        runId: st.id,
        stepId: s.id,
        prompt: fx.prompt,
        expected: fx.expected,
        model: fx.model,
      });
    } catch (e) {
      fx.error = errMsg(e);
    }
    fx.busy = false;
    update();
  };
  const tryIt = async () => {
    fx.trying = true;
    update();
    try {
      const t = await backend.TryStepFix(fx.res);
      S.flowRuns[t.id] = t;
      S.loaded[t.id] = true;
      S.events[t.id] ||= [];
      r.runs = [
        { id: t.id, workflow: t.flowId, state: t.status, started: t.started, kind: "flow-trial", usage: {} },
        ...r.runs,
      ];
      r.fix = null;
      r.runId = t.id;
      toast("Trying the change: the steps before it reuse this run's results.");
    } catch (e) {
      fx.error = errMsg(e);
    }
    fx.trying = false;
    update();
  };
  const errs = (fx.res?.issues || []).filter((i) => i.level === "error");
  return (
    <div className="fr-fix">
      <p className="muted fl-help">
        {failed
          ? "The AI changes only this step's settings (or its tool) so it runs and gives the steps after it what they need. "
          : "Say what should be better; the AI changes only this step. "}
        It sees the step, what reached it in this run{failed ? ", its error" : ", its result"} and how the
        steps after it use it. Nothing is saved until you try the change and apply it.
      </p>
      <textarea
        rows={2}
        value={fx.prompt}
        placeholder={
          failed
            ? "Optional: what you know about the problem, e.g. the API now returns a list under data."
            : "e.g. keep only the open items, sort by date, and give JSON with id and title."
        }
        onChange={(e) => set("prompt", e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) suggest();
        }}
      />
      <details className="fr-fix-expect" open={!!fx.expected}>
        <summary className="muted">
          What should it give instead? (optional: an example of the right output)
        </summary>
        <textarea
          rows={4}
          value={fx.expected}
          style={{ fontFamily: "var(--font-mono)" }}
          placeholder={"e.g. | Ticker | Price |\n|---|---|\n| AMZN | 265.58 |"}
          onChange={(e) => set("expected", e.target.value)}
        />
      </details>
      {(() => {
        // what the AI is given, from this run
        const f = frs().flow,
          parents = (f?.edges || [])
            .filter((e) => e.to === s.id)
            .map((e) => st.steps.find((x) => x.id === e.from))
            .filter(Boolean);
        const size = (x) =>
          x.output
            ? `${x.output.length.toLocaleString()} characters${x.truncated ? "+" : ""}`
            : x.iterations
              ? "per loop item"
              : "no output";
        return (
          <p className="muted fl-help">
            The AI is given: this step's definition;{" "}
            {parents.length
              ? parents.map((x) => `the output of ${x.title} (${size(x)})`).join(", ")
              : "nothing connected into it"}
            ; {s.error ? "its error" : `its output (${size(s)})`}
            {s.args ? " and the arguments it ran with" : ""}; how the steps after it use it
            {fx.expected.trim() ? "; and your example of the right output" : ""}. Long outputs are given by
            their start and end, with their JSON shape.
          </p>
        );
      })()}
      <div className="ai-model">
        <span className="muted">Model</span>
        <ModelPicker value={fx.model} onChange={(v) => set("model", v)} label="Model for the fix" />
        <button className="btn primary" disabled={fx.busy} onClick={suggest}>
          {fx.busy
            ? "Working on it…"
            : fx.res
              ? "Suggest again"
              : failed
                ? "Suggest a fix"
                : "Suggest an improvement"}
        </button>
        <button
          className="link"
          onClick={() => {
            r.fix = null;
            update();
          }}
        >
          Cancel
        </button>
      </div>
      {fx.busy && (
        <AiProgress
          phases={[
            "Reading the step and what happened",
            "Working out the change",
            "Checking it against the flow",
          ]}
        />
      )}
      {fx.error && <div className="banner error">{fx.error}</div>}
      {fx.res && (
        <div className="fr-fix-res">
          <div>
            <b>Suggested change</b>
            {fx.res.why && <span className="muted"> · {fx.res.why}</span>}
          </div>
          <div className="chips">
            {fx.res.changes.map((c) => (
              <span key={c} className="chip">
                {c}
              </span>
            ))}
          </div>
          {fx.res.issues.map((i, k) => (
            <div key={k} className={"banner " + (i.level === "error" ? "error" : "")}>
              {i.message}
            </div>
          ))}
          <div className="fr-fix-diff">
            <div>
              <div className="muted">Now</div>
              <pre className="code fr-pre">{nodeSettings(fx.res.before)}</pre>
            </div>
            <div>
              <div className="muted">Changed</div>
              <pre className="code fr-pre">{nodeSettings(fx.res.node)}</pre>
            </div>
          </div>
          <div className="row-actions">
            <button className="btn primary" disabled={fx.trying || errs.length > 0} onClick={tryIt}>
              {fx.trying ? "Starting…" : "Try it"}
            </button>
            <span className="muted fl-help">
              Runs a copy of the flow with this change: the steps before it reuse this run's results; this
              step and the ones after it run again. The flow itself is not changed yet.
            </span>
          </div>
        </div>
      )}
    </div>
  );
}

// A trial run: what changed, and Apply / Discard.
export function TrialBanner({ st }) {
  const t = st.trial,
    live = LIVE.includes(st.status);
  const title = (id) => st.steps.find((x) => x.id === id)?.title || id;
  const apply = async () => {
    try {
      await backend.ApplyStepFix(st.id);
      t.applied = true;
      toast(`Applied to the flow ${st.flowName}.`);
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <div className={"banner fr-trial" + (t.applied ? " applied" : "")}>
      <div>
        <b>{t.applied ? "Applied to the flow." : "Trial of a change — not saved yet."}</b> Changed:{" "}
        {t.steps
          .map((x) => (
            <span key={x.id}>
              <b>{title(x.id)}</b>
              {x.why ? ` (${x.why})` : ""}
            </span>
          ))
          .reduce((a, b) => [a, "; ", b])}
        .
        {t.reused?.length > 0 && (
          <> Reused from the earlier run (not run again): {t.reused.map(title).join(", ")}.</>
        )}
      </div>
      {!live && !t.applied && (
        <div className="row-actions">
          <button className="btn primary" onClick={apply}>
            {st.status === "done" ? "Apply to the flow" : "Apply anyway"}
          </button>
          <button className="btn secondary" onClick={() => openRun(t.baseRun)}>
            Discard · back to the earlier run
          </button>
          <span className="muted fl-help">
            {st.status === "done"
              ? "Check the steps and outputs below. Apply writes only the changed step into the flow."
              : "The trial did not finish. You can improve the step again from here."}
          </span>
        </div>
      )}
    </div>
  );
}
