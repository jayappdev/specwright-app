// Chats: talk with Chatflows (docs/design/2026-11-24-chatflows.md).
// Step 2: the Chatflows and their sessions, starting a session (its
// inputs; the setup steps run once), and the conversation, streamed. The
// session panel, rich answers, attachments and commands come in step 4.
import { useEffect, useRef } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, setChatListener } from "../store.js";
import { Icon } from "../ui.jsx";
import { mdRender } from "../markdown.js";
import { ago } from "../time.js";
import { NeedWorkspace } from "./Wizard.jsx";

const cs = () =>
  (S.chat ||= {
    flows: null, // ChatflowInfo[]
    sessions: [], // every Chatflow's sessions
    sel: null, // { flow, id }
    starting: "", // a Chatflow whose start form is open
    inputs: {},
    messages: {}, // "<flow>/<id>" → Message[]
    live: {}, // "<flow>/<id>" → the answer being written
    liveSteps: {}, // "<flow>/<id>" → the steps run for it so far
    open: {}, // a chip shown open: "<flow>/<id>/<message>/<n>" → its result
    draft: "",
  });
const keyOf = (s) => s.flow + "/" + s.id;

export async function loadChats() {
  const c = cs();
  try {
    c.flows = (await backend.Chatflows()) || [];
    c.sessions = (await backend.ChatSessions("")) || [];
  } catch (e) {
    c.flows = [];
    toast(errMsg(e), "error");
  }
  update();
}

async function loadMessages(flow, id) {
  const c = cs();
  try {
    c.messages[flow + "/" + id] = (await backend.ChatMessages(flow, id)) || [];
  } catch (e) {
    toast(errMsg(e), "error");
  }
  update();
}

function select(flow, id) {
  const c = cs();
  c.sel = { flow, id };
  c.starting = "";
  loadMessages(flow, id);
}

// hub events: a session changed, an answer is being written or was saved
setChatListener((e) => onChatEvent(e));
export function onChatEvent(e) {
  const c = S.chat;
  if (!c) return;
  const k = e.flow + "/" + e.session;
  if (e.type === "chat.step") {
    // a step the chat runs: a chip while it runs, its result when done
    const runs = (c.liveSteps[k] ||= []);
    const i = runs.findIndex((r) => r.tool === e.tool && r.status === "running");
    const r = {
      tool: e.tool,
      title: e.title,
      status: e.status,
      durationMs: e.durationMs,
      error: e.error,
      data: e.data,
    };
    if (i >= 0) runs[i] = r;
    else runs.push(r);
    update();
    return;
  }
  if (e.type === "chat.text") {
    c.live[k] = (c.live[k] ? c.live[k] + "\n\n" : "") + (e.text || "");
    update();
    return;
  }
  if (e.type === "chat.message") {
    delete c.live[k];
    delete c.liveSteps[k];
    loadMessages(e.flow, e.session);
  }
  backend
    .ChatSession(e.flow, e.session)
    .then((s) => {
      const i = c.sessions.findIndex((x) => x.flow === s.flow && x.id === s.id);
      if (i >= 0) c.sessions[i] = s;
      else c.sessions.unshift(s);
      update();
    })
    .catch(() => {});
}

function StartForm({ f }) {
  const c = cs();
  const set = (k, v) => {
    c.inputs[k] = v;
    update();
  };
  const start = async () => {
    try {
      const s = await backend.StartChatSession(f.id, { ...c.inputs });
      c.sessions.unshift(s);
      c.inputs = {};
      select(s.flow, s.id);
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <div className="chat-start card">
      <h2>{f.name}</h2>
      {f.desc && <p className="wf-desc">{f.desc}</p>}
      {f.issues?.length > 0 && (
        <div className="banner error">Fix the Chatflow in the Flow designer first: {f.issues.join(" ")}</div>
      )}
      {f.inputs.map((i) => {
        const v = c.inputs[i.name] ?? i.default ?? "";
        const label = (
          <span>
            <b>{i.label || i.name}</b>
            {i.required && <span className="err-text"> *</span>}
            {i.description && <small className="muted"> · {i.description}</small>}
          </span>
        );
        let field;
        if (i.type === "multiline")
          field = <textarea rows={4} value={v} onChange={(e) => set(i.name, e.target.value)} />;
        else if (i.type === "choice")
          field = (
            <select value={v} onChange={(e) => set(i.name, e.target.value)}>
              <option value="">—</option>
              {(i.options || []).map((o) => (
                <option key={o}>{o}</option>
              ))}
            </select>
          );
        else if (i.type === "boolean")
          field = (
            <input
              type="checkbox"
              checked={v === "true"}
              onChange={(e) => set(i.name, e.target.checked ? "true" : "false")}
            />
          );
        else
          field = (
            <input
              type={
                i.type === "secret"
                  ? "password"
                  : i.type === "number"
                    ? "number"
                    : i.type === "date"
                      ? "date"
                      : "text"
              }
              value={v}
              placeholder={i.type === "secret" ? "Kept for this session only" : ""}
              onChange={(e) => set(i.name, e.target.value)}
            />
          );
        return (
          <label key={i.name} className="field">
            {label}
            {field}
          </label>
        );
      })}
      <div className="muted chat-start-what">
        {f.setup.length > 0 ? (
          <>
            Starting runs the setup once: <b>{f.setup.join(", ")}</b>. Then ask anything.
          </>
        ) : (
          "Nothing to set up: the chat opens at once."
        )}
        {f.secrets?.length > 0 && (
          <> It uses the secrets {f.secrets.join(", ")} (asked for once if needed).</>
        )}
      </div>
      <div className="row-actions">
        <button className="btn secondary" onClick={() => ((c.starting = ""), update())}>
          Cancel
        </button>
        <button className="btn primary" disabled={f.issues?.length > 0} onClick={start}>
          Start the session
        </button>
      </div>
    </div>
  );
}

// StepChip: a step the chat ran ("▸ ran Query sales · 0.8 s"); open, its
// fields and what it gave
function StepChip({ r, s, k }) {
  const c = cs();
  const open = c.open[k];
  const toggle = async () => {
    if (open !== undefined) {
      delete c.open[k];
      return update();
    }
    c.open[k] = r.data ? "…" : "";
    update();
    if (r.data) {
      c.open[k] = await backend.ChatData(s.flow, s.id, r.data).catch((e) => errMsg(e));
      update();
    }
  };
  const label =
    r.status === "running"
      ? "running " + r.title + "…"
      : r.error
        ? r.title + " did not run"
        : "ran " + r.title + (r.durationMs ? " · " + (r.durationMs / 1000).toFixed(1) + " s" : "");
  return (
    <div className={"chat-chip-wrap" + (r.error ? " bad" : "")}>
      <button
        className="chat-chip"
        onClick={toggle}
        disabled={r.status === "running"}
        aria-expanded={open !== undefined}
      >
        {r.status === "running" ? (
          <span className="spinner" />
        ) : (
          <span aria-hidden="true">{open !== undefined ? "▾" : "▸"}</span>
        )}{" "}
        {label}
      </button>
      {open !== undefined && (
        <div className="chat-chip-body">
          {r.error && <div className="banner error">{r.error}</div>}
          {r.args && Object.keys(r.args).length > 0 && (
            <div className="chat-chip-args">
              {Object.entries(r.args).map(([a, v]) => (
                <div key={a}>
                  <code className="inline-code">{a}</code> {typeof v === "string" ? v : JSON.stringify(v)}
                </div>
              ))}
            </div>
          )}
          {open && <pre className="code chat-chip-out">{open}</pre>}
        </div>
      )}
    </div>
  );
}

function Message({ m, s }) {
  return (
    <div className={"chat-msg chat-" + m.role}>
      <div className="chat-avatar" aria-hidden="true">
        {m.role === "user" ? "🧑" : "🤖"}
      </div>
      <div className="chat-bubble">
        {(m.steps || []).map((r, i) => (
          <StepChip key={i} r={r} s={s} k={keyOf(s) + "/" + m.id + "/" + i} />
        ))}
        {m.role === "assistant" ? (
          <div className="rich md" dangerouslySetInnerHTML={{ __html: mdRender(m.text || "") }} />
        ) : (
          <div className="chat-q">{m.text}</div>
        )}
        {m.error && <div className="banner error chat-err">{m.error}</div>}
      </div>
    </div>
  );
}

function Conversation({ s, f }) {
  const c = cs(),
    k = keyOf(s),
    msgs = c.messages[k] || [],
    live = c.live[k],
    end = useRef(null);
  const answering = live !== undefined || (msgs.length && msgs[msgs.length - 1].role === "user");
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" }); // it returns a promise in some browsers: not a clean-up
  }, [msgs.length, live]);
  const send = async (text) => {
    const t = (text ?? c.draft).trim();
    if (!t) return;
    try {
      const q = await backend.SendChatMessage(s.flow, s.id, t);
      (c.messages[k] ||= []).push(q);
      c.live[k] = "";
      if (text === undefined) c.draft = "";
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <section className="chat-main">
      <header className="chat-head">
        <div>
          <h2>{s.title || "New session"}</h2>
          <small className="muted">
            {s.flowName} · {s.status === "ready" ? "ready" : s.status === "setup" ? "setting up…" : s.status}
            {s.setupAt ? ` · data loaded ${ago(s.setupAt)}` : ""}
            {Object.keys(s.inputs || {}).length > 0 &&
              " · " +
                Object.entries(s.inputs)
                  .map(([a, b]) => `${a}: ${b}`)
                  .join(", ")}
          </small>
        </div>
      </header>
      <div className="chat-scroll">
        {s.status === "setup" && (
          <div className="banner">⏳ Setting up the session: {f?.setup?.join(", ")}…</div>
        )}
        {s.status === "failed" && <div className="banner error">The setup failed: {s.error}</div>}
        {!msgs.length && s.status === "ready" && (
          <div className="chat-empty">
            <p className="muted">Ask anything about this session's data.</p>
            {(f?.starters || []).map((q) => (
              <button key={q} className="chip" onClick={() => send(q)}>
                {q}
              </button>
            ))}
          </div>
        )}
        {msgs.map((m) => (
          <Message key={m.id} m={m} s={s} />
        ))}
        {live !== undefined && (
          <div className="chat-msg chat-assistant">
            <div className="chat-avatar" aria-hidden="true">
              🤖
            </div>
            <div className="chat-bubble">
              {(c.liveSteps[k] || []).map((r, i) => (
                <StepChip key={i} r={r} s={s} k={k + "/live/" + i} />
              ))}
              {live ? (
                <div className="rich md" dangerouslySetInnerHTML={{ __html: mdRender(live) }} />
              ) : (
                <span className="chat-typing" aria-label="Thinking">
                  <i />
                  <i />
                  <i />
                </span>
              )}
            </div>
          </div>
        )}
        <div ref={end} />
      </div>
      <div className="chat-composer">
        <textarea
          rows={2}
          value={c.draft}
          disabled={s.status !== "ready"}
          placeholder={
            s.status === "ready" ? "Ask about the data…  (Enter to send, Shift+Enter for a new line)" : ""
          }
          onChange={(e) => {
            c.draft = e.target.value;
            update();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              if (!answering) send();
            }
          }}
        />
        {answering ? (
          <button
            className="btn secondary"
            onClick={() => backend.StopChatMessage(s.flow, s.id).catch((e) => toast(errMsg(e), "error"))}
          >
            Stop
          </button>
        ) : (
          <button
            className="btn primary"
            disabled={s.status !== "ready" || !c.draft.trim()}
            onClick={() => send()}
          >
            Send
          </button>
        )}
      </div>
    </section>
  );
}

export function Chats() {
  const c = cs();
  useEffect(() => {
    if (S.ws) loadChats();
  }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="chats" />;
  const flows = c.flows || [];
  const s = c.sel && c.sessions.find((x) => x.flow === c.sel.flow && x.id === c.sel.id);
  const startF = c.starting && flows.find((f) => f.id === c.starting);
  return (
    <div className="files chats">
      <nav className="ftree" aria-label="Chatflows and sessions">
        <h1 className="list-head" style={{ padding: "0 8px 8px" }}>
          Chats
        </h1>
        {c.flows && !flows.length && (
          <p className="muted" style={{ padding: "0 8px" }}>
            No Chatflows yet. In the Flow designer, add a <b>Chat</b> step to a flow: the steps before it set
            up a session, the paths after it are what the chat can run.
          </p>
        )}
        {flows.map((f) => {
          const ss = c.sessions.filter((x) => x.flow === f.id && !x.archived);
          return (
            <div key={f.id} className="chat-flow">
              <div className="tree-folder">
                <span>💬 {f.name}</span>
                <button
                  className="link"
                  onClick={() => {
                    c.starting = f.id;
                    c.sel = null;
                    c.inputs = {};
                    update();
                  }}
                >
                  <Icon.plus /> New session
                </button>
              </div>
              {ss.map((x) => (
                <button
                  key={x.id}
                  className={
                    "run-item chat-sess" + (c.sel?.id === x.id && c.sel?.flow === x.flow ? " active" : "")
                  }
                  onClick={() => select(x.flow, x.id)}
                >
                  <span className="run-item-top">
                    <b>{x.title || "New session"}</b>
                    {x.status !== "ready" && <span className="badge">{x.status}</span>}
                  </span>
                  <small className="muted">
                    {x.messages || 0} messages · {ago(x.updated)}
                  </small>
                </button>
              ))}
            </div>
          );
        })}
      </nav>
      <section className="view chat-view">
        {startF ? (
          <StartForm f={startF} />
        ) : s ? (
          <Conversation s={s} f={flows.find((f) => f.id === s.flow)} />
        ) : (
          <div className="chat-pick muted">{flows.length ? "Pick a session, or start a new one." : ""}</div>
        )}
      </section>
    </div>
  );
}
