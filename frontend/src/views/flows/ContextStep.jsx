// Flow Designer › a Context step (flow/context.go): what it is for, its
// sources, its budget, and whether the AI condenses it. The steps connected
// into it are sources too. The run view shows what it kept (ContextReport).
import { Icon } from "../../ui.jsx";
import { changed } from "../Flows.jsx";

const KINDS = [
  ["files", "Files", "Project files matching patterns: their contents"],
  ["text", "Text", "Notes, rules, a policy (can use {{inputs.x}})"],
  ["inputs", "Inputs", "The flow's input values"],
  ["history", "Past runs", "What earlier runs of this project found about something"],
  ["lessons", "Lessons", "What reviewers asked for in this flow's steps"],
  ["url", "Web page", "A page's readable text"],
];
const PRIORITIES = [
  ["high", "High"],
  ["normal", "Normal"],
  ["low", "Low"],
];

export function ContextInspector({ n, set }) {
  const sources = (n.sources ||= []);
  const edit = (i, k, v) => {
    sources[i][k] = v;
    if (v === "" || v == null) delete sources[i][k];
    changed();
  };
  return (
    <div className="fl-ctx">
      <label className="field">
        <span className="muted">What is the context for?</span>
        <textarea
          rows={2}
          value={n.goal || ""}
          placeholder="e.g. Decide whether the release can go out today, and what to check first"
          onChange={(e) => set("goal", e.target.value)}
        />
        <small className="muted">
          Its words rank what is kept: parts that say nothing about it are left out (each source keeps its
          start).
        </small>
      </label>
      <h4>Sources</h4>
      <p className="muted fl-help">
        The steps connected into this one are sources too, each on its own. Each source gets a share of the
        budget by its priority; what one does not need goes to the others.
      </p>
      {sources.map((s, i) => (
        <div key={i} className="fl-ctx-src">
          <div className="fl-ctx-src-head">
            <b>{KINDS.find(([k]) => k === s.kind)?.[1] || s.kind}</b>
            <input
              value={s.label || ""}
              placeholder="Name (optional)"
              aria-label="Name of the source"
              onChange={(e) => edit(i, "label", e.target.value)}
            />
            <select
              value={s.priority || "normal"}
              aria-label="Priority"
              onChange={(e) => edit(i, "priority", e.target.value === "normal" ? "" : e.target.value)}
            >
              {PRIORITIES.map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
            <button
              className="icon-btn"
              aria-label={`Remove the source ${s.label || s.kind}`}
              onClick={() => {
                sources.splice(i, 1);
                changed();
              }}
            >
              <Icon.x />
            </button>
          </div>
          {s.kind === "files" && (
            <textarea
              rows={2}
              value={(s.paths || []).join("\n")}
              placeholder={"docs/**/*.md\nsrc/billing/**/*.java"}
              aria-label="File patterns, one a line"
              onChange={(e) =>
                edit(
                  i,
                  "paths",
                  e.target.value.split("\n").filter((x, k, a) => x.trim() || k < a.length - 1),
                )
              }
            />
          )}
          {s.kind === "text" && (
            <textarea
              rows={3}
              value={s.text || ""}
              placeholder="e.g. Refunds over 500 EUR need a manager's approval."
              aria-label="Text"
              onChange={(e) => edit(i, "text", e.target.value)}
            />
          )}
          {s.kind === "url" && (
            <input
              value={s.url || ""}
              placeholder="https://…"
              aria-label="Web address"
              onChange={(e) => edit(i, "url", e.target.value)}
            />
          )}
          {(s.kind === "history" || s.kind === "files" || s.kind === "text" || s.kind === "url") && (
            <input
              value={s.query || ""}
              placeholder={
                s.kind === "history"
                  ? "What to look for, e.g. refund failures"
                  : "Also focus on these words (optional)"
              }
              aria-label={s.kind === "history" ? "What to look for in past runs" : "Words to focus on"}
              onChange={(e) => edit(i, "query", e.target.value)}
            />
          )}
          <small className="muted">{KINDS.find(([k]) => k === s.kind)?.[2]}</small>
        </div>
      ))}
      <div className="chips" role="group" aria-label="Add a source">
        {KINDS.map(([k, l]) => (
          <button
            key={k}
            type="button"
            className="chip"
            onClick={() => {
              sources.push(k === "files" ? { kind: k, paths: ["docs/**/*.md"] } : { kind: k });
              changed();
            }}
          >
            + {l}
          </button>
        ))}
      </div>
      <label className="field">
        <span className="muted">Budget (tokens; about 4 characters each)</span>
        <input
          type="number"
          min={500}
          max={100000}
          step={500}
          value={n.budget || 8000}
          aria-label="Budget in tokens"
          onChange={(e) => set("budget", Math.max(500, Math.min(100000, +e.target.value || 8000)))}
        />
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={!!n.condense}
          onChange={(e) => set("condense", e.target.checked || undefined)}
        />{" "}
        Condense with the AI into a short brief for the purpose (one AI call; keeps the [S1] labels)
      </label>
      <p className="muted fl-help">
        The AI and Improve steps after this one get the context in their prompt by themselves. A step
        connected straight to it gets it as its input; use {"{{steps." + n.id + "}}"} to put it somewhere
        else.
      </p>
    </div>
  );
}

// what a Context step kept, in the run view
export function ContextReport({ r }) {
  if (!r) return null;
  return (
    <div className="fr-ctx">
      <div className="muted">
        Used {r.used.toLocaleString()} of {r.budget.toLocaleString()} tokens
        {r.condensed ? " · condensed by the AI" : ""}
        {r.focus?.length ? ` · ranked by: ${r.focus.join(", ")}` : ""}
      </div>
      <table className="fr-ctx-table">
        <thead>
          <tr>
            <th>Source</th>
            <th>Kept</th>
            <th>Notes</th>
          </tr>
        </thead>
        <tbody>
          {r.sources.map((s) => (
            <tr key={s.id} className={s.kept ? "" : "none"}>
              <td>
                <b>[{s.id}]</b> {s.label} <span className="muted">({s.kind})</span>
              </td>
              <td>
                {s.kept.toLocaleString()} / {s.tokens.toLocaleString()}
              </td>
              <td className="muted">
                {[s.note, s.duplicates ? `${s.duplicates} repeated` : ""].filter(Boolean).join(" · ")}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
