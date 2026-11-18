// RunSettings: budgets, retries and the approval gate of the spec workflow
// (Run spec workflow sidebar and Settings › Runs).
import { S, update, saveWorkflow } from "../store.js";

export function RunSettings({ wf, open }) {
  const b = wf.budget,
    st = wf.steps[Math.min(S.sel, wf.steps.length - 1)];
  const num = (v) => Math.max(0, Number(v) || 0);
  return (
    <details style={{ margin: open ? 0 : "10px 8px 0" }} open={open || undefined}>
      <summary className="muted" style={{ cursor: "pointer" }}>
        Run settings
      </summary>
      <div className="budget" style={{ gridTemplateColumns: "1fr" }}>
        <label className="field inline">
          <span>Token budget per run</span>
          <input
            type="number"
            min="0"
            step="10000"
            value={b.maxTokens}
            onChange={(e) => {
              b.maxTokens = num(e.target.value);
              update();
              saveWorkflow();
            }}
          />
        </label>
        <label className="field inline">
          <span>Cost budget per run (USD)</span>
          <input
            type="number"
            min="0"
            step="0.5"
            value={b.maxUsd}
            onChange={(e) => {
              b.maxUsd = num(e.target.value);
              update();
              saveWorkflow();
            }}
          />
        </label>
        <label className="field inline">
          <span>Premium requests per run</span>
          <input
            type="number"
            min="0"
            value={b.maxPremiumRequests}
            onChange={(e) => {
              b.maxPremiumRequests = num(e.target.value);
              update();
              saveWorkflow();
            }}
          />
        </label>
        <label className="field inline">
          <span>Retries for {st.title}</span>
          <input
            type="number"
            min="0"
            max="5"
            value={st.retries}
            onChange={(e) => {
              st.retries = Math.min(5, num(e.target.value));
              update();
              saveWorkflow();
            }}
          />
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={st.gate}
            onChange={(e) => {
              st.gate = e.target.checked;
              update();
              saveWorkflow();
            }}
          />{" "}
          Approval gate after {st.title}
        </label>
        <p className="muted" style={{ margin: 0 }}>
          0 means no limit. A run that exceeds a budget is stopped and marked rejected.
        </p>
      </div>
    </details>
  );
}
