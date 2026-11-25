// What a chat session took: prompts, premium requests, tokens, cost, tool
// calls (chat.Usage on the Go side: per answer on each message, totals on
// the session, the setup apart). An ⓘ on each answer and one in the chat's
// header show it on hover; the header's opens the whole picture in a dialog.
import { useEffect, useState } from "react";
import { Icon } from "../ui.jsx";
import { toast } from "../store.js";

export const kfmt = (n) =>
  !n
    ? "0"
    : n >= 1e6
      ? (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M"
      : n >= 1e3
        ? (n / 1e3).toFixed(1).replace(/\.0$/, "") + "k"
        : String(n);
export const secs = (ms) =>
  !ms
    ? "—"
    : ms < 1000
      ? ms + " ms"
      : ms < 60000
        ? (ms / 1000).toFixed(1) + " s"
        : Math.floor(ms / 60000) + " min " + Math.round((ms % 60000) / 1000) + " s";
export const usd = (v) => "$" + (v || 0).toFixed((v || 0) < 1 ? 3 : 2);
const plural = (n, one, many = one + "s") => (n || 0).toLocaleString() + " " + (n === 1 ? one : many);
const tokensOf = (u) => (u?.inputTokens || 0) + (u?.outputTokens || 0);
const toolsOf = (u) => (u?.toolRuns || 0) + (u?.dataRuns || 0);

function sum(...us) {
  const t = {};
  for (const u of us)
    for (const [k, v] of Object.entries(u || {})) if (typeof v === "number") t[k] = (t[k] || 0) + v;
  return t;
}

// sessionTotals: everything the session took (its questions and its
// setup). The session's own token and cost totals also count answers from
// before usage was measured per answer: they win when larger.
export function sessionTotals(s) {
  const t = sum(s.usage, s.setupUsage);
  t.tokens = Math.max(tokensOf(t), s.tokens || 0);
  t.costUsd = Math.max(t.costUsd || 0, s.costUsd || 0);
  return t;
}

let tipSeq = 0;

// InfoTip: a small ⓘ that shows a card on hover or keyboard focus (and
// runs onClick when clicked). align: "start" opens to the right, "end" to
// the left (for icons near the right edge).
export function InfoTip({ label, children, onClick, align = "start" }) {
  const [id] = useState(() => "infotip-" + ++tipSeq);
  return (
    <span className={"infotip infotip-" + align}>
      <button
        type="button"
        className="infotip-btn"
        aria-label={label}
        aria-describedby={id}
        onClick={onClick}
        tabIndex={0}
      >
        <Icon.info />
      </button>
      <div role="tooltip" id={id} className="infotip-card">
        {children}
      </div>
    </span>
  );
}

function Rows({ rows }) {
  return (
    <dl className="infotip-rows">
      {rows.filter(Boolean).map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

const cachedNote = (u) =>
  u.inputTokens ? ` (${Math.round(((u.cachedTokens || 0) / u.inputTokens) * 100)}% cached)` : "";

// MessageUsage: what one answer took, on hover of its ⓘ
export function MessageUsage({ m }) {
  const u = m.usage;
  return (
    <InfoTip label="What this answer took">
      <div className="infotip-title">This answer</div>
      {u ? (
        <Rows
          rows={[
            m.model && ["Model", (m.agent ? m.agent + " · " : "") + m.model],
            [
              "AI prompts",
              `${u.prompts || 0}` + (u.modelCalls ? ` · ${plural(u.modelCalls, "model call")}` : ""),
            ],
            u.premiumRequests ? ["Premium requests", u.premiumRequests.toLocaleString()] : null,
            u.costUsd ? ["Cost", usd(u.costUsd)] : null,
            ["Tokens", `${kfmt(u.inputTokens)} in${cachedNote(u)} · ${kfmt(u.outputTokens)} out`],
            [
              "Tool calls",
              toolsOf(u)
                ? `${toolsOf(u)} · ${u.toolRuns || 0} Chatflow, ${u.dataRuns || 0} data` +
                  (u.toolErrors ? `, ${u.toolErrors} failed` : "")
                : "none",
            ],
            ["Time", secs(u.durationMs)],
          ]}
        />
      ) : (
        <p className="infotip-note">Not measured: answered before the app recorded usage per answer.</p>
      )}
    </InfoTip>
  );
}

// SessionUsage: the whole session, on hover of the ⓘ in the chat's
// header; a click opens the Usage dialog
export function SessionUsage({ s, msgs, onOpen }) {
  const t = sessionTotals(s),
    su = s.setupUsage || {};
  const questions = (msgs || []).filter((m) => m.role === "assistant").length;
  return (
    <InfoTip label="What this chat took (click for details)" onClick={onOpen} align="end">
      <div className="infotip-title">This chat</div>
      <Rows
        rows={[
          ["Questions", questions.toLocaleString()],
          [
            "AI prompts",
            `${t.prompts || 0}` + (t.modelCalls ? ` · ${plural(t.modelCalls, "model call")}` : ""),
          ],
          ["Premium requests", (t.premiumRequests || 0).toLocaleString()],
          ["Cost", t.costUsd ? usd(t.costUsd) : "—"],
          [
            "Tokens",
            `${kfmt(t.tokens)}` + (t.inputTokens ? ` · ${kfmt(t.inputTokens)} in${cachedNote(t)}` : ""),
          ],
          [
            "Tool calls",
            toolsOf(t)
              ? `${toolsOf(t)} · ${t.toolRuns || 0} Chatflow, ${t.dataRuns || 0} data` +
                (t.toolErrors ? `, ${t.toolErrors} failed` : "")
              : "none",
          ],
          tokensOf(su) || su.durationMs
            ? ["Setup", `${kfmt(tokensOf(su))} tokens · ${secs(su.durationMs)}`]
            : null,
        ]}
      />
      <p className="infotip-note">Click for each question and each tool.</p>
    </InfoTip>
  );
}

// the questions and their answers, paired
function answers(msgs) {
  const out = [];
  msgs.forEach((m, i) => {
    if (m.role !== "assistant") return;
    let q = null;
    for (let j = i - 1; j >= 0; j--)
      if (msgs[j].role === "user") {
        q = msgs[j];
        break;
      }
    out.push({ q, a: m });
  });
  return out;
}

function tools(msgs) {
  const by = {};
  for (const m of msgs)
    for (const r of m.steps || []) {
      const t = (by[r.title || r.tool] ||= {
        title: r.title || r.tool,
        builtin: (r.tool || "").startsWith("data."),
        runs: 0,
        failed: 0,
        ms: 0,
        tokens: 0,
      });
      t.runs++;
      if (r.error) t.failed++;
      t.ms += r.durationMs || 0;
      t.tokens += tokensOf(r.usage);
    }
  return Object.values(by).sort((a, b) => b.runs - a.runs);
}

function Card({ label, value, sub }) {
  return (
    <div className="usage-card">
      <div className="usage-label">{label}</div>
      <div className="usage-value">{value}</div>
      {sub && <div className="usage-sub">{sub}</div>}
    </div>
  );
}

export function UsageDialog({ s, msgs, onClose }) {
  useEffect(() => {
    const key = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener("keydown", key, true);
    return () => window.removeEventListener("keydown", key, true);
  }, [onClose]);
  const q = s.usage || {},
    su = s.setupUsage || {},
    all = sessionTotals(s);
  const qa = answers(msgs);
  const measured = qa.filter((x) => x.a.usage);
  const tl = tools(msgs);
  const totalTokens = all.tokens;
  const copilot = (all.premiumRequests || 0) > 0;
  const cachedPct = all.inputTokens ? Math.round(((all.cachedTokens || 0) / all.inputTokens) * 100) : 0;
  const avgMs = measured.length
    ? measured.reduce((n, x) => n + (x.a.usage.durationMs || 0), 0) / measured.length
    : 0;
  const share = (u) => (totalTokens ? Math.round((tokensOf(u) / totalTokens) * 100) : 0);
  const copy = () => {
    const lines = [
      `# ${s.title || "Chat"} — usage`,
      `- Questions: ${qa.length}`,
      `- AI prompts: ${all.prompts || 0} (${all.modelCalls || 0} model calls)`,
      `- Premium requests: ${all.premiumRequests || 0}`,
      `- Tokens: ${totalTokens} (in ${all.inputTokens || 0}, out ${all.outputTokens || 0}, cached ${all.cachedTokens || 0})`,
      `- Cost: ${usd(all.costUsd)}`,
      `- Tool calls: ${toolsOf(all)} (${all.toolRuns || 0} Chatflow tools, ${all.dataRuns || 0} data, ${all.toolErrors || 0} failed)`,
      "",
      "| # | Question | Prompts | Tools | Tokens | Premium | Cost | Time |",
      "|---|---|---|---|---|---|---|---|",
      ...qa.map(
        ({ q: qm, a }, i) =>
          `| ${i + 1} | ${(qm?.text || "").replace(/\|/g, "\\|").replace(/\n/g, " ").slice(0, 80)} | ${a.usage?.prompts ?? "—"} | ${a.usage ? toolsOf(a.usage) : "—"} | ${a.usage ? tokensOf(a.usage) : "—"} | ${a.usage?.premiumRequests ?? "—"} | ${a.usage ? usd(a.usage.costUsd) : "—"} | ${a.usage ? secs(a.usage.durationMs) : "—"} |`,
      ),
    ];
    navigator.clipboard
      ?.writeText(lines.join("\n"))
      .then(() => toast("Copied the usage as Markdown."))
      .catch(() => toast("Could not copy.", "error"));
  };
  return (
    <div
      className="modal-backdrop dlg-backdrop"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="dlg usage-dlg" role="dialog" aria-modal="true" aria-labelledby="usage-title">
        <header className="usage-head">
          <div>
            <h3 id="usage-title">Usage</h3>
            <small className="muted">
              {s.title || "New session"} · {s.flowName}
              {s.agent ? " · " + s.agent : ""}
              {s.model ? " · " + s.model : ""}
            </small>
          </div>
          <button className="icon-btn" aria-label="Close" onClick={onClose}>
            <Icon.x />
          </button>
        </header>

        <div className="usage-cards">
          <Card
            label="Questions"
            value={qa.length.toLocaleString()}
            sub={avgMs ? "avg " + secs(Math.round(avgMs)) + " per answer" : "none answered yet"}
          />
          <Card
            label="AI prompts"
            value={(all.prompts || 0).toLocaleString()}
            sub={plural(all.modelCalls || 0, "model call")}
          />
          <Card
            label="Premium requests"
            value={(all.premiumRequests || 0).toLocaleString()}
            sub={copilot ? "GitHub Copilot" : "only Copilot counts these"}
          />
          <Card
            label="Tokens"
            value={kfmt(totalTokens)}
            sub={`${kfmt(all.inputTokens)} in · ${kfmt(all.outputTokens)} out · ${cachedPct}% cached`}
          />
          <Card
            label="Cost"
            value={usd(all.costUsd)}
            sub={all.costUsd ? "as the AI reported it" : "not reported (Copilot: see premium requests)"}
          />
          <Card
            label="Tool calls"
            value={toolsOf(all).toLocaleString()}
            sub={`${all.toolRuns || 0} Chatflow · ${all.dataRuns || 0} data · ${all.toolErrors || 0} failed`}
          />
        </div>

        <section className="usage-sec">
          <h4>Where it went</h4>
          <table className="usage-table">
            <thead>
              <tr>
                <th>Part</th>
                <th className="num">Prompts</th>
                <th className="num">Tokens</th>
                <th className="num">Premium</th>
                <th className="num">Cost</th>
                <th className="num">Time</th>
                <th className="usage-bar-col">Share of tokens</th>
              </tr>
            </thead>
            <tbody>
              {[
                ["Setup (loading the data)", su],
                ["Questions (the chat and its tools)", q],
              ].map(([name, u]) => (
                <tr key={name}>
                  <td>{name}</td>
                  <td className="num">{u.prompts || 0}</td>
                  <td className="num">{kfmt(tokensOf(u))}</td>
                  <td className="num">{u.premiumRequests || 0}</td>
                  <td className="num">{usd(u.costUsd)}</td>
                  <td className="num">{secs(u.durationMs)}</td>
                  <td className="usage-bar-col">
                    <span className="usage-bar">
                      <i style={{ width: share(u) + "%" }} />
                    </span>
                    <small className="muted">{share(u)}%</small>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>

        <section className="usage-sec">
          <h4>Per question</h4>
          {qa.length ? (
            <div className="usage-scroll">
              <table className="usage-table">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Question</th>
                    <th className="num">Prompts</th>
                    <th className="num">Tools</th>
                    <th className="num">Tokens</th>
                    <th className="num">Premium</th>
                    <th className="num">Cost</th>
                    <th className="num">Time</th>
                  </tr>
                </thead>
                <tbody>
                  {qa.map(({ q: qm, a }, i) => {
                    const u = a.usage;
                    return (
                      <tr key={a.id}>
                        <td className="muted">{i + 1}</td>
                        <td className="usage-q" title={qm?.text}>
                          {qm?.text || "—"}
                          {a.error && <span className="badge badge-warn">{a.error}</span>}
                        </td>
                        <td className="num">{u ? u.prompts || 0 : "—"}</td>
                        <td className="num">
                          {u ? toolsOf(u) : "—"}
                          {u?.toolErrors ? (
                            <small className="usage-err"> ({u.toolErrors} failed)</small>
                          ) : null}
                        </td>
                        <td className="num">{u ? kfmt(tokensOf(u)) : "—"}</td>
                        <td className="num">{u ? u.premiumRequests || 0 : "—"}</td>
                        <td className="num">{u ? usd(u.costUsd) : "—"}</td>
                        <td className="num">{u ? secs(u.durationMs) : "—"}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="muted">No questions yet.</p>
          )}
          {measured.length < qa.length && (
            <small className="muted">— : answered before the app measured usage per answer.</small>
          )}
        </section>

        {tl.length > 0 && (
          <section className="usage-sec">
            <h4>Tools</h4>
            <table className="usage-table">
              <thead>
                <tr>
                  <th>Tool</th>
                  <th className="num">Calls</th>
                  <th className="num">Failed</th>
                  <th className="num">Avg time</th>
                  <th className="num">AI tokens</th>
                </tr>
              </thead>
              <tbody>
                {tl.map((t) => (
                  <tr key={t.title}>
                    <td>
                      {t.title} {t.builtin && <span className="badge">built in</span>}
                    </td>
                    <td className="num">{t.runs}</td>
                    <td className="num">{t.failed ? <span className="usage-err">{t.failed}</span> : 0}</td>
                    <td className="num">{secs(Math.round(t.ms / t.runs))}</td>
                    <td className="num">{t.tokens ? kfmt(t.tokens) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        )}

        <footer className="row-actions dlg-actions">
          <small className="muted usage-foot">
            Tokens and cost are what the AI reported; Copilot counts premium requests instead of cost.
          </small>
          <button className="btn secondary" onClick={copy}>
            <Icon.copy /> Copy as Markdown
          </button>
          <button className="btn primary" onClick={onClose}>
            Close
          </button>
        </footer>
      </div>
    </div>
  );
}
