// "How it was decided": every revision of the draft with what the AI was
// sent, its whole reply, what it says it changed, the script's diff and the
// test after it. From here the conversation moves to the Prompt
// Playground, where the tool can be improved by talking it through.
import { useState } from "react";
import { S, fmtMs } from "../../store.js";
import { Icon, Pill } from "../../ui.jsx";
import { CopyButton } from "../../CopyButton.jsx";
import { lineDiff, diffCount, kindWord, playgroundPrompt } from "./toolHistory.js";
import { openToolChat } from "../Playground.jsx";

// openToolInPlayground: start a Playground conversation about the draft,
// with its history and last test; the prompt is there to read and edit
// before it is sent.
export function openToolInPlayground(ask) {
  const tb = S.tb;
  if (!tb?.draft) return;
  openToolChat(
    playgroundPrompt(tb.draft, tb.history, tb.test, tb.testPrompt, ask ?? tb.fixAsk),
    tb.draft.name || tb.editing || "",
  );
}

function Diff({ before, after }) {
  const d = lineDiff(before, after);
  const { add, del } = diffCount(d);
  if (!add && !del) return <p className="muted">The script did not change.</p>;
  // show changed lines with two lines of context
  const keep = d.map((l, i) => d.slice(Math.max(0, i - 2), i + 3).some((x) => x.t !== " "));
  const rows = [];
  d.forEach((l, i) => {
    if (keep[i]) rows.push(l);
    else if (rows.length && rows[rows.length - 1].t !== "…") rows.push({ t: "…", s: "" });
  });
  return (
    <>
      <p className="muted">
        <span className="dl-add">+{add}</span> <span className="dl-del">−{del}</span> lines
      </p>
      <pre className="dl-diff">
        {rows.map((l, i) => (
          <div
            key={i}
            className={l.t === "+" ? "dl-add" : l.t === "-" ? "dl-del" : l.t === "…" ? "muted" : ""}
          >
            {l.t === "…" ? "  …" : l.t + " " + l.s}
          </div>
        ))}
      </pre>
    </>
  );
}

function Section({ title, text, open }) {
  const [o, setO] = useState(!!open);
  if (!text) return null;
  return (
    <div className="dl-sec">
      <button className="link" onClick={() => setO(!o)}>
        {o ? "▾" : "▸"} {title}
      </button>
      {o && (
        <div className="dl-body">
          <CopyButton text={text} />
          <pre className="dl-pre">{text}</pre>
        </div>
      )}
    </div>
  );
}

function Turn({ h, n }) {
  const [o, setO] = useState(false);
  const t = h.test;
  return (
    <li className="dl-turn">
      <button className="dl-head" onClick={() => setO(!o)} aria-expanded={o}>
        <span className="dl-n">{n}</span>
        <span className="dl-title">
          <b>{h.kind === "test" ? "Test" : kindWord(h)}</b>
          {h.kind !== "test" && <span className="muted"> → revision {h.revision}</span>}
          {h.model && <span className="muted"> · {h.model}</span>}
          {h.ms > 0 && <span className="muted"> · {fmtMs(h.ms)}</span>}
        </span>
        {t && <Pill s={t.pass ? "done" : "failed"} label={t.pass ? "test passed" : "test failed"} />}
        <span className="muted">{o ? "▾" : "▸"}</span>
      </button>
      {h.ask && h.kind !== "test" && (
        <p className="dl-ask">
          <span className="muted">{h.kind === "generate" ? "You described:" : "You asked:"}</span> {h.ask}
        </p>
      )}
      {h.why && (
        <p className="dl-why">
          <span className="muted">The AI says it changed:</span> {h.why}
        </p>
      )}
      {!h.why && h.kind === "fix" && <p className="muted dl-why">The AI did not say what it changed.</p>}
      {t && !t.pass && <p className="muted dl-why">Test after it: {h.testText}</p>}
      {o && (
        <div className="dl-detail">
          {h.kind !== "test" && h.kind !== "generate" && <Diff before={h.before} after={h.after} />}
          <Section title="What the AI was sent" text={h.prompt} />
          <Section title="The AI's whole reply" text={h.reply} />
          {t && (
            <Section
              title={`The test: “${(h.question || "").slice(0, 60)}”`}
              text={[
                ...(t.checks || []).map((c) => (c.ok ? "✓ " : "✗ ") + c.label),
                "",
                t.summary || "",
                ...(t.steps || []).map((s) => `[${s.k}] ${s.v}`),
              ].join("\n")}
            />
          )}
        </div>
      )}
    </li>
  );
}

export function DecisionLog({ tb }) {
  const h = tb.history || [];
  if (!h.length && !tb.draft) return null;
  return (
    <div className="card dl">
      <div className="card-head">
        <h3>How it was decided</h3>
        <span className="badge">{h.length}</span>
        {!tb.ro && tb.draft && (
          <button
            className="btn secondary"
            style={{ marginLeft: "auto" }}
            onClick={() => openToolInPlayground()}
            title="Opens an Ask AI conversation with this tool, its history and its last test; apply the AI's revision from there"
          >
            <Icon.playground /> Continue in Ask AI
          </button>
        )}
      </div>
      {h.length ? (
        <ol className="dl-list">
          {h.map((x, i) => (
            <Turn key={i} h={x} n={i + 1} />
          ))}
        </ol>
      ) : (
        <p className="muted">
          Nothing yet in this session. Each time the AI writes, changes or fixes the tool, and each test,
          shows here: what it was sent, its reply, what changed and how the test went.
        </p>
      )}
    </div>
  );
}
