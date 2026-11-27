// Home: where the app starts. What needs you, what is running, what you
// worked on last, and (for a new project) how to get started. Every row
// goes straight to the place that deals with it.
import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update } from "../store.js";
import { Icon, Pill, Skeleton } from "../ui.jsx";
import { STATUS_LABEL, pill } from "./FlowRun.jsx";
import { ago } from "../time.js";
import { needsYou, runningNow } from "../attention.js";
import { NeedWorkspace } from "./Wizard.jsx";
import { openNewMenu } from "../NewMenu.jsx";

const hs = () => (S.home ||= { flows: null, chats: [], runs: [] });

export async function loadHome() {
  const h = hs();
  const [flows, chats, runs] = await Promise.all([
    backend.Flows().catch(() => []),
    backend.ChatSessions("").catch(() => []),
    backend.FlowRuns(20).catch(() => []),
  ]);
  Object.assign(h, { flows: flows || [], chats: chats || [], runs: runs || [] });
  update();
}

const open = {
  flow: (id) => import("./Flows.jsx").then((m) => m.openFlowInDesigner(id)),
  run: (id) => import("./FlowRun.jsx").then((m) => m.showFlowRun(id)),
  chat: (flow, id) => import("./Chats.jsx").then((m) => m.openChatSession(flow, id)),
  view: (v) => {
    S.view = v;
    update();
  },
};

function Section({ title, count, action, children }) {
  return (
    <section className="home-sec">
      <div className="home-sec-head">
        <h2>
          {title}
          {count ? <span className="list-count">{count}</span> : null}
        </h2>
        {action}
      </div>
      {children}
    </section>
  );
}

function Row({ icon, title, detail, meta, onClick, action }) {
  return (
    <button className="home-row" onClick={onClick}>
      <span className="home-row-icon" aria-hidden="true">
        {icon}
      </span>
      <span className="home-row-text">
        <b>{title}</b>
        {detail && <small>{detail}</small>}
      </span>
      {meta && <span className="home-row-meta">{meta}</span>}
      {action && <span className="home-row-go">{action} →</span>}
    </button>
  );
}

const KIND_ICON = { approval: <Icon.check />, review: <Icon.flow />, tools: <Icon.tool /> };

// the steps of a new project, done or not
function gettingStarted() {
  const h = hs();
  return [
    { id: "project", label: "Open a project", done: !!S.ws, go: () => open.view("inputs") },
    {
      id: "agent",
      label: "Choose the AI agent and model",
      done: !!S.setup?.done,
      go: () => import("./Appearance.jsx").then((m) => m.openSettings("agent")),
    },
    {
      id: "flow",
      label: "Make a flow: describe it, and the AI builds it",
      done: (h.flows || []).some((f) => !f.chat),
      go: () => open.view("flows"),
    },
    {
      id: "chat",
      label: "Chat with your data: a flow with a Chat step",
      done: (h.chats || []).length > 0,
      go: () => open.view("flows"),
    },
    {
      id: "spec",
      label: "Build from a spec: set up Spec Kit",
      done: !!S.ws?.speckit?.initialized,
      go: () => open.view("inputs"),
    },
  ];
}

export function Home() {
  const h = hs();
  useEffect(() => {
    if (S.ws) loadHome();
  }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="your work" />;
  const needs = needsYou(),
    running = runningNow(),
    steps = gettingStarted(),
    left = steps.filter((s) => !s.done);
  const flows = [...(h.flows || [])]
    .sort((a, b) => Date.parse(b.updated || 0) - Date.parse(a.updated || 0))
    .slice(0, 6);
  const chats = [...(h.chats || [])]
    .filter((c) => !c.archived)
    .sort((a, b) => Date.parse(b.updated || 0) - Date.parse(a.updated || 0))
    .slice(0, 5);
  const runs = (h.runs || []).slice(0, 5);
  return (
    <section className="view home" aria-label="Home">
      <header className="home-head">
        <div>
          <h1>{S.ws.name || S.ws.root.split("/").pop()}</h1>
          <p className="muted">
            {needs.length
              ? `${needs.length} thing${needs.length === 1 ? "" : "s"} need${needs.length === 1 ? "s" : ""} you`
              : "Nothing needs you right now"}
            {running.length ? ` · ${running.length} running` : ""}
          </p>
        </div>
        <button className="btn primary" onClick={(e) => openNewMenu(e.currentTarget)}>
          <Icon.plus /> New
        </button>
      </header>

      <div className="home-grid">
        <div className="home-col">
          <Section title="Needs you" count={needs.length}>
            {needs.length ? (
              <div className="home-list">
                {needs.map((n) => (
                  <Row
                    key={n.id}
                    icon={KIND_ICON[n.kind] || <Icon.warn />}
                    title={n.title}
                    detail={n.detail}
                    action={n.action}
                    onClick={n.go}
                  />
                ))}
              </div>
            ) : (
              <p className="home-empty">
                <Icon.check /> You're all caught up: no approvals, reviews or tools waiting.
              </p>
            )}
          </Section>

          {running.length > 0 && (
            <Section title="Running now" count={running.length}>
              <div className="home-list">
                {running.map((r) => (
                  <Row
                    key={r.id}
                    icon={<span className="spinner" />}
                    title={r.title}
                    detail={r.detail}
                    onClick={r.go}
                  />
                ))}
              </div>
            </Section>
          )}

          <Section
            title="Recent runs"
            action={
              <button className="link" onClick={() => open.view("history")}>
                All history
              </button>
            }
          >
            {runs.length ? (
              <div className="home-list">
                {runs.map((r) => (
                  <Row
                    key={r.id}
                    icon={<Icon.run />}
                    title={r.title || r.workflow}
                    meta={
                      <>
                        <Pill s={pill(r.state)} label={STATUS_LABEL[r.state] || r.state} />
                        <small className="muted">{ago(r.started)}</small>
                      </>
                    }
                    onClick={() => open.run(r.id)}
                  />
                ))}
              </div>
            ) : (
              <p className="home-empty muted">No flow runs yet.</p>
            )}
          </Section>
        </div>

        <div className="home-col">
          {left.length > 0 && (
            <Section title="Get started" count={`${steps.length - left.length}/${steps.length}`}>
              <ol className="home-steps">
                {steps.map((s) => (
                  <li key={s.id} className={s.done ? "done" : ""}>
                    <span className="home-step-mark" aria-hidden="true">
                      {s.done ? <Icon.check /> : null}
                    </span>
                    {s.done ? (
                      <span>{s.label}</span>
                    ) : (
                      <button className="link" onClick={s.go}>
                        {s.label}
                      </button>
                    )}
                  </li>
                ))}
              </ol>
            </Section>
          )}

          <Section
            title="Flows"
            action={
              <button className="link" onClick={() => open.view("flows")}>
                All flows
              </button>
            }
          >
            {h.flows === null ? (
              <Skeleton rows={3} />
            ) : flows.length ? (
              <div className="home-list">
                {flows.map((f) => (
                  <Row
                    key={f.id}
                    icon={f.chat ? <Icon.chat /> : <Icon.workflow />}
                    title={f.name}
                    detail={`${f.steps} step${f.steps === 1 ? "" : "s"}${f.chat ? " · Chatflow" : ""}`}
                    meta={f.updated && <small className="muted">{ago(f.updated)}</small>}
                    onClick={() => open.flow(f.id)}
                  />
                ))}
              </div>
            ) : (
              <p className="home-empty muted">No flows yet.</p>
            )}
          </Section>

          <Section
            title="Chats"
            action={
              <button className="link" onClick={() => open.view("chats")}>
                All chats
              </button>
            }
          >
            {chats.length ? (
              <div className="home-list">
                {chats.map((c) => (
                  <Row
                    key={c.flow + "/" + c.id}
                    icon={<Icon.chat />}
                    title={c.title || "New session"}
                    detail={c.flowName}
                    meta={<small className="muted">{ago(c.updated)}</small>}
                    onClick={() => open.chat(c.flow, c.id)}
                  />
                ))}
              </div>
            ) : (
              <p className="home-empty muted">No chats yet.</p>
            )}
          </Section>
        </div>
      </div>
    </section>
  );
}
