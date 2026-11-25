// Chats: talk with Chatflows (docs/design/2026-11-24-chatflows.md).
// Three columns: the Chatflows and their sessions | the conversation |
// the session panel (setup, inputs, variables, data, what it can run,
// budget). Answers stream; the steps the chat ran show as chips whose
// results open in the content viewer (tables, charts with Export PNG);
// charts show at once. Files can be attached; suggested follow-ups and
// /refresh, /data and /steps work in the message box.
import { useEffect, useRef, useState } from "react";
import { ContentView, chartOf, RenderPreview } from "../ContentView.jsx";
import { FileLink } from "../FileLinks.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, setChatListener } from "../store.js";
import { Icon } from "../ui.jsx";
import { mdRender } from "../markdown.js";
import { ago } from "../time.js";
import { NeedWorkspace } from "./Wizard.jsx";
import { confirmDialog, askText } from "../Dialog.jsx";
import { openRunnerWindow } from "./FlowRun.jsx";
import { MessageUsage, SessionUsage, UsageDialog } from "./ChatUsage.jsx";

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
    open: {}, // chips shown open: "<flow>/<id>/<message>/<n>" → true
    results: {}, // a step's result by "<flow>/<id>/<data name>"
    attach: {}, // "<flow>/<id>" → files attached to the next message [{name, data}]
    notes: {}, // "<flow>/<id>" → answers to /data, /steps, /help (not saved)
    panel: true, // the session panel is shown
    archived: false, // archived sessions are listed too
    preview: null, // { title, text }: a data item shown over the screen
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

// a session's actions: rename, export, archive or restore, delete
async function renameSession(x) {
  const title = await askText("Rename the session", { value: x.title || "", ok: "Rename" });
  if (!title?.trim()) return;
  try {
    Object.assign(x, await backend.RenameChatSession(x.flow, x.id, title));
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}
async function exportSession(x) {
  try {
    const p = await backend.ExportChatSession(x.flow, x.id);
    toast("Saved as " + p);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}
async function archiveSession(x) {
  try {
    Object.assign(x, await backend.ArchiveChatSession(x.flow, x.id, !x.archived));
    const c = cs();
    if (x.archived && c.sel?.id === x.id && !c.archived) c.sel = null;
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}
async function deleteSession(x) {
  if (
    !(await confirmDialog(
      `“${x.title || "New session"}”, its messages and data are deleted from the project. This cannot be undone.`,
      {
        title: "Delete the session?",
        ok: "Delete",
        danger: true,
      },
    ))
  )
    return;
  try {
    await backend.DeleteChatSession(x.flow, x.id);
    const c = cs();
    c.sessions = c.sessions.filter((y) => !(y.flow === x.flow && y.id === x.id));
    if (c.sel?.id === x.id) c.sel = null;
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
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

// result reads a step's result once (kept for the screen's life)
function useResult(s, name) {
  const c = cs(),
    rk = keyOf(s) + "/" + name;
  useEffect(() => {
    if (!name || c.results[rk] !== undefined) return;
    c.results[rk] = null;
    backend
      .ChatData(s.flow, s.id, name)
      .then((t) => {
        c.results[rk] = t;
        update();
      })
      .catch(() => {});
  }, [rk]);
  return name ? c.results[rk] : undefined;
}

// StepChip: a step the chat ran ("▸ ran Query sales · 0.8 s"); open, the
// fields it got and what it gave (a table, a chart, text). A chart shows
// at once.
function StepChip({ r, s, k }) {
  const c = cs();
  const result = useResult(s, r.status === "running" ? "" : r.data);
  const chart = result ? chartOf(result) : "";
  const open = !!c.open[k];
  const toggle = () => {
    if (open) delete c.open[k];
    else c.open[k] = true;
    update();
  };
  const label =
    r.status === "running"
      ? "running " + r.title + "…"
      : r.error
        ? r.title + " did not run"
        : "ran " + r.title + (r.durationMs ? " · " + (r.durationMs / 1000).toFixed(1) + " s" : "");
  return (
    <div className={"chat-chip-wrap" + (r.error ? " bad" : "")}>
      <button className="chat-chip" onClick={toggle} disabled={r.status === "running"} aria-expanded={open}>
        {r.status === "running" ? (
          <span className="spinner" />
        ) : (
          <span aria-hidden="true">{open ? "▾" : "▸"}</span>
        )}{" "}
        {label}
      </button>
      {(r.files || []).length > 0 && (
        <span className="chat-chip-files">
          Saved:{" "}
          {r.files.map((p) => (
            <FileLink key={p} path={p}>
              {p}
            </FileLink>
          ))}
        </span>
      )}
      {chart && !open && (
        <div className="chat-chart">
          <RenderPreview text={chart} format="html" />
        </div>
      )}
      {open && (
        <div className="chat-chip-body">
          {r.error && <div className="banner error">{r.error}</div>}
          {r.args && Object.keys(r.args).length > 0 && (
            <div className="chat-chip-args">
              {Object.entries(r.args).map(([a, v]) =>
                a === "sql" ? (
                  <pre key={a} className="chat-sql">
                    {v}
                  </pre>
                ) : (
                  <div key={a}>
                    <code className="inline-code">{a}</code> {typeof v === "string" ? v : JSON.stringify(v)}
                  </div>
                ),
              )}
            </div>
          )}
          {result === null && <p className="muted">Loading…</p>}
          {result && <ContentView title={r.title} text={result} name={r.data} compact />}
        </div>
      )}
    </div>
  );
}

function Avatar({ role }) {
  return (
    <div className={"chat-avatar chat-avatar-" + role} aria-hidden="true">
      {role === "user" ? <Icon.user /> : role === "note" ? <Icon.book /> : <Icon.spark />}
    </div>
  );
}

function Message({ m, s, last, onAsk }) {
  return (
    <div className={"chat-msg chat-" + m.role}>
      <Avatar role={m.role} />
      <div className="chat-bubble">
        {m.role !== "user" && (
          <div className="chat-who">
            {m.role === "note" ? "Note" : "Assistant"}
            {m.at && <span className="muted"> · {ago(m.at)}</span>}
            {m.role === "assistant" && <MessageUsage m={m} />}
          </div>
        )}
        {(m.steps || []).map((r, i) => (
          <StepChip key={i} r={r} s={s} k={keyOf(s) + "/" + m.id + "/" + i} />
        ))}
        {m.role === "user" ? (
          <div className="chat-q">{m.text}</div>
        ) : (
          <div className="rich md" dangerouslySetInnerHTML={{ __html: mdRender(m.text || "") }} />
        )}
        {(m.files || []).length > 0 && (
          <div className="chat-files">
            <Icon.clip /> {m.files.map((f) => f.replace(/^file-/, "")).join(", ")}
          </div>
        )}
        {m.error && <div className="banner error chat-err">{m.error}</div>}
        {last && (m.followups || []).length > 0 && (
          <div className="chat-follow">
            {m.followups.map((q) => (
              <button key={q} className="chip" onClick={() => onAsk(q)}>
                {q}
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

const SLASH = [
  ["/refresh", "Run the setup again: fresh data for the rest of the session"],
  ["/data", "What this session holds"],
  ["/steps", "What the chat can run"],
  ["/help", "These commands"],
];

// readFile: a dropped, pasted or picked file as base64
const readFile = (file) =>
  new Promise((res, rej) => {
    const r = new FileReader();
    r.onload = () => res(String(r.result).replace(/^data:[^,]*,/, ""));
    r.onerror = () => rej(r.error);
    r.readAsDataURL(file);
  });

function Conversation({ s, f }) {
  const c = cs(),
    k = keyOf(s),
    msgs = c.messages[k] || [],
    live = c.live[k],
    notes = c.notes[k] || [],
    files = c.attach[k] || [],
    end = useRef(null),
    picker = useRef(null);
  const answering = live !== undefined || (msgs.length && msgs[msgs.length - 1].role === "user");
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" }); // it returns a promise in some browsers: not a clean-up
  }, [msgs.length, live, notes.length]);
  const note = (text) => {
    (c.notes[k] ||= []).push({ id: "n" + Date.now(), role: "note", text, after: msgs.length });
    update();
  };
  const command = async (t) => {
    const [cmd] = t.split(/\s+/);
    if (cmd === "/refresh") {
      try {
        await backend.RefreshChatSession(s.flow, s.id);
        note("Refreshing the session's data: the setup runs again; the next answer uses the new data.");
      } catch (e) {
        toast(errMsg(e), "error");
      }
    } else if (cmd === "/data")
      note(
        (s.data || []).length
          ? "This session holds:\n\n" +
              s.data
                .map(
                  (d) =>
                    `- **${d.title || d.name}** · ${d.setup ? "setup" : d.step === "attachment" ? "attached" : "from a question"} · ${d.bytes} bytes`,
                )
                .join("\n")
          : "This session holds no data yet.",
      );
    else if (cmd === "/steps")
      note(
        (f?.tools || []).length
          ? "The chat can run:\n\n" +
              f.tools
                .map((x) => `- **${x.title}**${(f.asks || []).includes(x.id) ? " (asks you first)" : ""}`)
                .join("\n")
          : "This Chatflow has nothing to run: only the setup's data.",
      );
    else note(SLASH.map(([a, b]) => `- \`${a}\` ${b}`).join("\n"));
  };
  const send = async (text) => {
    const t = (text ?? c.draft).trim();
    if (!t) return;
    if (t.startsWith("/")) {
      if (text === undefined) c.draft = "";
      return command(t);
    }
    try {
      const q = await backend.SendChatMessage(
        s.flow,
        s.id,
        t,
        files.map((x) => x.data),
      );
      (c.messages[k] ||= []).push(q);
      c.live[k] = "";
      c.attach[k] = [];
      if (text === undefined) c.draft = "";
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const attach = async (list) => {
    for (const file of list) {
      try {
        const d = await backend.AttachChatFile(s.flow, s.id, file.name, await readFile(file));
        (c.attach[k] ||= []).push({ name: file.name, data: d.name });
      } catch (e) {
        toast(errMsg(e), "error");
      }
    }
    update();
  };
  // notes (/data, /steps) sit after the message they followed
  const rows = [];
  msgs.forEach((m, i) => {
    rows.push(
      <Message key={m.id} m={m} s={s} last={i === msgs.length - 1 && live === undefined} onAsk={send} />,
    );
    notes.filter((n) => n.after === i + 1).forEach((n) => rows.push(<Message key={n.id} m={n} s={s} />));
  });
  notes.filter((n) => n.after === 0).forEach((n) => rows.unshift(<Message key={n.id} m={n} s={s} />));
  const slash = c.draft.startsWith("/") ? SLASH.filter(([a]) => a.startsWith(c.draft.split(/\s/)[0])) : [];
  return (
    <section className="chat-main">
      <header className="chat-head">
        <div className="chat-head-title">
          <h2>{s.title || "New session"}</h2>
          <div className="chat-head-meta">
            <span className={"chat-status chat-status-" + s.status}>
              {s.status === "ready"
                ? "Ready"
                : s.status === "setup"
                  ? "Setting up…"
                  : s.status === "failed"
                    ? "Failed"
                    : s.status}
            </span>
            <span>{s.flowName}</span>
            {s.agent && (
              <span>
                {s.agent}
                {s.model ? " · " + s.model : ""}
              </span>
            )}
            <span>{(s.messages || 0).toLocaleString()} messages</span>
          </div>
        </div>
        <div className="chat-head-acts">
          <SessionUsage s={s} msgs={msgs} onOpen={() => ((c.usage = true), update())} />
          <button
            className="icon-btn"
            title={c.panel ? "Hide the session panel" : "Show the session panel"}
            aria-label="Session panel"
            aria-pressed={c.panel}
            onClick={() => ((c.panel = !c.panel), update())}
          >
            <Icon.panelR />
          </button>
        </div>
      </header>
      {c.usage && <UsageDialog s={s} msgs={msgs} onClose={() => ((c.usage = false), update())} />}
      <div className="chat-scroll">
        {s.status === "setup" && (
          <div className="banner">⏳ Setting up the session: {f?.setup?.join(", ")}…</div>
        )}
        {s.status === "failed" && <div className="banner error">The setup failed: {s.error}</div>}
        {!msgs.length && s.status === "ready" && (
          <div className="chat-empty">
            <p className="muted">Ask anything about this session's data. Type / for commands.</p>
            {(f?.starters || []).map((q) => (
              <button key={q} className="chip" onClick={() => send(q)}>
                {q}
              </button>
            ))}
          </div>
        )}
        {rows}
        {live !== undefined && (
          <div className="chat-msg chat-assistant">
            <Avatar role="assistant" />
            <div className="chat-bubble">
              <div className="chat-who">
                Assistant <span className="muted">· {live ? "writing…" : "thinking…"}</span>
              </div>
              {(c.liveSteps[k] || []).map((r, i) => (
                <StepChip key={i} r={r} s={s} k={k + "/live/" + i} />
              ))}
              {live ? (
                <div
                  className="rich md"
                  dangerouslySetInnerHTML={{ __html: mdRender(live.replace(/\n?\s*Follow-ups?:.*$/is, "")) }}
                />
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
      <div
        className="chat-composer"
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          if (e.dataTransfer.files.length) attach([...e.dataTransfer.files]);
        }}
      >
        {slash.length > 0 && (
          <div className="chat-slash" role="listbox" aria-label="Commands">
            {slash.map(([a, b]) => (
              <button key={a} role="option" onClick={() => ((c.draft = a), update())}>
                <code>{a}</code> {b}
              </button>
            ))}
          </div>
        )}
        <div className="chat-compose-card">
          {files.length > 0 && (
            <div className="chat-attach">
              {files.map((x, i) => (
                <span key={i} className="chip">
                  <Icon.clip /> {x.name}
                  <button
                    className="icon-btn"
                    aria-label={"Remove " + x.name}
                    onClick={() => ((c.attach[k] = files.filter((_, j) => j !== i)), update())}
                  >
                    <Icon.x />
                  </button>
                </span>
              ))}
            </div>
          )}
          <textarea
            rows={2}
            value={c.draft}
            disabled={s.status !== "ready"}
            aria-label="Your question"
            placeholder={s.status === "ready" ? "Ask about the data…" : "The session is not ready"}
            onChange={(e) => {
              c.draft = e.target.value;
              update();
            }}
            onPaste={(e) => {
              const list = [...(e.clipboardData?.files || [])];
              if (list.length) {
                e.preventDefault();
                attach(list);
              }
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                if (!answering || c.draft.trim().startsWith("/")) send();
              }
            }}
          />
          <div className="chat-compose-row">
            <button
              className="icon-btn"
              title="Attach files (or drop or paste them here)"
              aria-label="Attach files"
              disabled={s.status !== "ready"}
              onClick={() => picker.current?.click()}
            >
              <Icon.clip />
            </button>
            <input
              ref={picker}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                attach([...e.target.files]);
                e.target.value = "";
              }}
            />
            <small className="chat-hint">
              <kbd>Enter</kbd> to send · <kbd>Shift</kbd>+<kbd>Enter</kbd> new line · <kbd>/</kbd> commands
            </small>
            {answering ? (
              <button
                className="btn secondary"
                onClick={() => backend.StopChatMessage(s.flow, s.id).catch((e) => toast(errMsg(e), "error"))}
              >
                Stop
              </button>
            ) : (
              <button
                className="btn primary chat-send"
                disabled={s.status !== "ready" || !c.draft.trim()}
                onClick={() => send()}
              >
                <Icon.send /> Send
              </button>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

// Panel: the session at a glance (names only: never a secret's value)
// useCatalog: the session's indexed data (tables to query, documents to
// search), loaded again when the session changes.
function useCatalog(s) {
  const [cat, setCat] = useState({});
  const key = s.flow + "/" + s.id + "/" + (s.data || []).length + "/" + (s.setupAt || "");
  useEffect(() => {
    let live = true;
    if (s.status !== "ready") return undefined;
    backend
      .ChatCatalog(s.flow, s.id)
      .then((list) => live && setCat(Object.fromEntries((list || []).map((e) => [e.name, e]))))
      .catch(() => live && setCat({}));
    return () => {
      live = false;
    };
  }, [key, s.status]); // the key covers the session changes that matter
  return cat;
}

const SMALL_DATA = 20000; // as chatSmallData: larger data is queried or searched, not sent

function catalogLabel(e) {
  if (!e) return "";
  const n = (k, one, many) => (k || 0).toLocaleString() + " " + (k === 1 ? one : many);
  return e.kind === "table"
    ? "table · " + n(e.rows, "row", "rows")
    : "text · " + n(e.passages, "passage", "passages");
}

function Panel({ s, f }) {
  const c = cs();
  const cat = useCatalog(s);
  const entries = Object.values(cat);
  const tables = entries.filter((e) => e.kind === "table"),
    docs = entries.filter((e) => e.kind === "text");
  const show = async (d) => {
    try {
      c.preview = {
        title: d.title || d.name,
        name: d.name,
        text: await backend.ChatData(s.flow, s.id, d.name),
      };
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const limit = f?.costLimit || 0;
  return (
    <aside className="chat-panel" aria-label="Session">
      <section>
        <h4>Setup</h4>
        <div>
          {s.status === "ready"
            ? "✔ Ready"
            : s.status === "setup"
              ? "⏳ Setting up…"
              : s.status === "failed"
                ? "✖ Failed"
                : s.status}
          {s.setupAt && <small className="muted"> · loaded {ago(s.setupAt)}</small>}
        </div>
        {(f?.setup || []).length > 0 && <small className="muted">{f.setup.join(" → ")}</small>}
        <button
          className="btn secondary chat-refresh"
          disabled={s.status === "setup"}
          onClick={() =>
            backend
              .RefreshChatSession(s.flow, s.id)
              .then(() => toast("Refreshing the data: the setup runs again."))
              .catch((e) => toast(errMsg(e), "error"))
          }
        >
          <Icon.refresh /> Refresh data
        </button>
      </section>
      {Object.keys(s.inputs || {}).length > 0 && (
        <section>
          <h4>Inputs</h4>
          <dl className="chat-dl">
            {Object.entries(s.inputs).map(([a, b]) => (
              <div key={a}>
                <dt>{a}</dt>
                <dd>{b || "—"}</dd>
              </div>
            ))}
          </dl>
        </section>
      )}
      {((f?.vars || []).length > 0 || (f?.secrets || []).length > 0) && (
        <section>
          <h4>Environment</h4>
          {(f.vars || []).map((v) => (
            <div key={v} className="chat-env-name">
              <code>{v}</code>
            </div>
          ))}
          {(f.secrets || []).map((v) => (
            <div key={v} className="chat-env-name" title="A secret: its value is never shown">
              <code>{v}</code> 🔒
            </div>
          ))}
        </section>
      )}
      <section>
        <h4>Data · {(s.data || []).length}</h4>
        {(s.data || []).map((d) => (
          <button key={d.name} className="chat-data" onClick={() => show(d)} title="Show it">
            {d.setup ? "⚙" : d.step === "attachment" ? "📎" : "▸"} {d.title || d.name}
            <small className="muted">
              {" "}
              {d.bytes > 1024 ? Math.round(d.bytes / 1024) + " KB" : d.bytes + " B"}
              {cat[d.name] && " · " + catalogLabel(cat[d.name])}
            </small>
            {cat[d.name] && d.bytes > SMALL_DATA && (
              <span
                className="badge chat-indexed"
                title={
                  cat[d.name].kind === "table"
                    ? "Too large to send whole: the AI gets its columns and first rows, and queries it with SQL"
                    : "Too large to send whole: the AI gets the passages that match each question, and can search for more"
                }
              >
                {cat[d.name].kind === "table" ? "queried" : "searched"}
              </span>
            )}
          </button>
        ))}
        {!(s.data || []).length && <small className="muted">Nothing yet.</small>}
      </section>
      <section>
        <h4>Can run · {(f?.tools || []).length + (tables.length ? 1 : 0) + (docs.length ? 1 : 0)}</h4>
        {tables.length > 0 && (
          <div className="chat-tool" title={"SQL over " + tables.map((e) => e.table).join(", ")}>
            Query the data <span className="badge">built in</span>
          </div>
        )}
        {docs.length > 0 && (
          <div className="chat-tool" title="Full-text search of the documents">
            Search the documents <span className="badge">built in</span>
          </div>
        )}
        {(f?.tools || []).map((t) => (
          <div key={t.id} className="chat-tool">
            {t.title}
            {(f.asks || []).includes(t.id) && <span className="badge badge-warn">asks you</span>}
          </div>
        ))}
      </section>
      <section>
        <h4 className="chat-panel-h">
          Usage
          <button className="link" onClick={() => ((cs().usage = true), update())}>
            Details
          </button>
        </h4>
        <div>
          ${(s.costUsd || 0).toFixed(3)}
          {limit > 0 && <span className="muted"> of ${limit.toFixed(2)}</span>} ·{" "}
          {(s.tokens || 0).toLocaleString()} tokens
        </div>
        {limit > 0 && (
          <div className="chat-budget">
            <i style={{ width: Math.min(100, ((s.costUsd || 0) / limit) * 100) + "%" }} />
          </div>
        )}
      </section>
    </aside>
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
    <div className={"files chats" + (s && !startF && c.panel ? " with-panel" : "")}>
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
        {c.sessions.some((x) => x.archived) && (
          <label className="check chat-show-archived">
            <input
              type="checkbox"
              checked={c.archived}
              onChange={(e) => ((c.archived = e.target.checked), update())}
            />{" "}
            Show archived sessions
          </label>
        )}
        {flows
          .filter((f) => !S.runner || f.id === S.runner)
          .map((f) => {
            const ss = c.sessions.filter((x) => x.flow === f.id && (c.archived || !x.archived));
            return (
              <div key={f.id} className="chat-flow">
                <div className="tree-folder">
                  <span className="chat-flow-name">
                    <Icon.chat /> {f.name}
                  </span>
                  <span className="chat-flow-acts">
                    {!S.runner && (
                      <button
                        className="icon-btn"
                        title="Open this Chatflow in a window of its own (for people who only chat)"
                        aria-label={"Open " + f.name + " in its own window"}
                        onClick={() => openRunnerWindow(f.id)}
                      >
                        <Icon.panelR />
                      </button>
                    )}
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
                  </span>
                </div>
                {ss.map((x) => (
                  <div key={x.id} className="chat-sess-row">
                    <button
                      className={
                        "run-item chat-sess" + (c.sel?.id === x.id && c.sel?.flow === x.flow ? " active" : "")
                      }
                      onClick={() => select(x.flow, x.id)}
                    >
                      <span className="run-item-top">
                        <b>{x.title || "New session"}</b>
                        {x.status !== "ready" && <span className="badge">{x.status}</span>}
                        {x.archived && <span className="badge">archived</span>}
                      </span>
                      <small className="muted">
                        {x.messages || 0} messages · {ago(x.updated)}
                      </small>
                    </button>
                    <span className="chat-sess-acts">
                      <button
                        className="icon-btn"
                        title="Rename"
                        aria-label="Rename"
                        onClick={() => renameSession(x)}
                      >
                        <Icon.pencil />
                      </button>
                      <button
                        className="icon-btn"
                        title="Export as Markdown (out/chats)"
                        aria-label="Export"
                        onClick={() => exportSession(x)}
                      >
                        <Icon.download />
                      </button>
                      <button
                        className="icon-btn"
                        title={x.archived ? "Restore" : "Archive"}
                        aria-label={x.archived ? "Restore" : "Archive"}
                        onClick={() => archiveSession(x)}
                      >
                        {x.archived ? <Icon.undo /> : <Icon.archive />}
                      </button>
                      <button
                        className="icon-btn"
                        title="Delete"
                        aria-label="Delete"
                        onClick={() => deleteSession(x)}
                      >
                        <Icon.trash />
                      </button>
                    </span>
                  </div>
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
      {s && !startF && c.panel && <Panel s={s} f={flows.find((f) => f.id === s.flow)} />}
      {c.preview && (
        <div
          className="chat-preview"
          role="dialog"
          aria-label={c.preview.title}
          onClick={() => ((c.preview = null), update())}
        >
          <div className="card" onClick={(e) => e.stopPropagation()}>
            <div className="card-head">
              <h3>{c.preview.title}</h3>
              <button className="icon-btn" aria-label="Close" onClick={() => ((c.preview = null), update())}>
                <Icon.x />
              </button>
            </div>
            <ContentView title={c.preview.title} text={c.preview.text} name={c.preview.name} />
          </div>
        </div>
      )}
    </div>
  );
}
