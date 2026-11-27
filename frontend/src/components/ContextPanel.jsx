// ContextPanel: what the AI works with in a conversation, the same five
// sections on Chats and Ask AI, so both read the same way:
//   Sources        what it reads (data, files, earlier runs)
//   Instructions   what steers its answers
//   Tools          what it can run, and what asks you first
//   Model          the agent, the model and the run's settings
//   Usage          what the conversation has taken so far
// Each section folds; which ones are folded is remembered in this browser.
import { useState } from "react";
import { S, update } from "../store.js";
import { Icon } from "../ui.jsx";

const KEY = "codegen.ctxpanel.folded.v1";

function readFolded() {
  try {
    return JSON.parse(localStorage.getItem(KEY) || "{}") || {};
  } catch {
    return {};
  }
}

// sections: [{ id, title, count?, action?, children }]; a section with no
// children is left out
export function ContextPanel({ label, sections }) {
  const [folded, setFolded] = useState(readFolded);
  const toggle = (id) => {
    const next = { ...folded, [id]: !folded[id] };
    setFolded(next);
    try {
      localStorage.setItem(KEY, JSON.stringify(next));
    } catch {
      /* per-browser convenience only */
    }
  };
  return (
    <aside className="chat-panel ctx-panel" aria-label={label}>
      <div className="ctx-title">Context</div>
      {sections
        .filter((s) => s && s.children)
        .map((s) => (
          <section key={s.id} className={"ctx-sec" + (folded[s.id] ? " folded" : "")}>
            <h4 className="ctx-h">
              <button
                className="ctx-fold"
                aria-expanded={!folded[s.id]}
                aria-controls={"ctx-" + s.id}
                onClick={() => toggle(s.id)}
              >
                <span className="ctx-caret" aria-hidden="true">
                  ▸
                </span>
                {s.title}
                {s.count != null && <span className="ctx-count">{s.count}</span>}
              </button>
              {s.action}
            </h4>
            {!folded[s.id] && (
              <div id={"ctx-" + s.id} className="ctx-body">
                {s.children}
              </div>
            )}
          </section>
        ))}
    </aside>
  );
}

// a fixed line in Instructions: something the app always tells the AI
export function Rule({ children }) {
  return <li className="ctx-rule">{children}</li>;
}

// whether the Context panel is shown (one choice for Chats and Ask AI,
// remembered in this browser)
const OPEN_KEY = "codegen.ctxpanel.open.v1";
export function panelOpen() {
  if (S.ctxPanel == null)
    try {
      S.ctxPanel = localStorage.getItem(OPEN_KEY) !== "0";
    } catch {
      S.ctxPanel = true;
    }
  return S.ctxPanel;
}

export function setPanelOpen(on) {
  S.ctxPanel = on;
  try {
    localStorage.setItem(OPEN_KEY, on ? "1" : "0");
  } catch {
    /* per-browser convenience only */
  }
  update();
}

export function PanelToggle() {
  const on = panelOpen();
  return (
    <button
      className="icon-btn"
      title={on ? "Hide the Context panel" : "Show the Context panel: what the AI works with"}
      aria-label="Context panel"
      aria-pressed={on}
      onClick={() => setPanelOpen(!on)}
    >
      <Icon.panelR />
    </button>
  );
}

// ContextMeter: how full the AI's context window was at its last call
// (the input tokens of that call, against the model's window).
export function ContextMeter({ tokens, window: win, compact }) {
  if (!tokens) return null;
  const pct = Math.min(100, Math.round((tokens / win) * 100));
  const k = (n) => (n >= 1000 ? Math.round(n / 1000) + "k" : String(n));
  const level = pct >= 80 ? " high" : pct >= 50 ? " mid" : "";
  return (
    <span
      className={"ctx-meter" + level + (compact ? " compact" : "")}
      title={`The AI's last call used ${tokens.toLocaleString()} tokens of about ${win.toLocaleString()} it can hold (${pct}%). Near the limit, start a new conversation or remove context.`}
    >
      <span className="ctx-meter-bar" aria-hidden="true">
        <i style={{ width: Math.max(2, pct) + "%" }} />
      </span>
      <span className="ctx-meter-text">
        {k(tokens)} of ~{k(win)} context
      </span>
    </span>
  );
}
