// The "+ New" menu: start anything from anywhere (the sidebar's + New, Home,
// ⌘N). One list, so every screen creates things the same way.
import { useEffect, useRef, useState } from "react";
import { backend } from "./backend.js";
import { S, update } from "./store.js";
import { Icon } from "./ui.jsx";

export function openNewMenu(anchor) {
  const r = anchor?.getBoundingClientRect?.();
  S.newMenu = r ? { left: r.left, top: r.bottom + 6 } : { left: 80, top: 80 };
  update();
}
const close = () => {
  S.newMenu = null;
  update();
};

const goView = (v) => {
  S.view = v;
  update();
};

export function NewMenuHost() {
  const m = S.newMenu,
    box = useRef(null),
    [chatflows, setChatflows] = useState(null);
  useEffect(() => {
    if (!m) return;
    setChatflows(null);
    backend
      .Chatflows()
      .then((l) => setChatflows(l || []))
      .catch(() => setChatflows([]));
    setTimeout(() => box.current?.querySelector("button")?.focus(), 0);
    const down = (e) => {
      if (!box.current?.contains(e.target)) close();
    };
    const key = (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close();
      }
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const all = [...box.current.querySelectorAll("button")],
          k = all.indexOf(document.activeElement);
        all[(k + (e.key === "ArrowDown" ? 1 : all.length - 1)) % all.length]?.focus();
      }
    };
    window.addEventListener("mousedown", down);
    window.addEventListener("keydown", key, true);
    return () => {
      window.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key, true);
    };
  }, [m]);
  if (!m) return null;
  const item = (icon, label, desc, run) => (
    <button
      key={label}
      role="menuitem"
      className="new-item"
      onClick={() => {
        close();
        run();
      }}
    >
      <span className="new-item-icon" aria-hidden="true">
        {icon}
      </span>
      <span className="new-item-text">
        <b>{label}</b>
        <small>{desc}</small>
      </span>
    </button>
  );
  return (
    <div
      className="new-menu"
      role="menu"
      aria-label="New"
      ref={box}
      style={{
        left: Math.min(m.left, window.innerWidth - 340),
        top: Math.min(m.top, window.innerHeight - 420),
      }}
    >
      {item(<Icon.workflow />, "Flow", "Steps on a canvas: inputs, tools, AI, outputs", () =>
        import("./views/Flows.jsx").then((x) => x.createFlow()),
      )}
      {item(<Icon.spark />, "Flow with AI", "Describe it; the AI builds draft flows to try", () => {
        goView("flows");
        import("./views/flows/FlowWizard.jsx").then((x) => x.openWizard());
      })}
      {item(<Icon.tool />, "Tool", "A script the AI and your flows can run", () =>
        import("./views/Tools.jsx").then((x) => x.newTool(false)),
      )}
      {item(<Icon.playground />, "Ask AI", "A conversation with the AI about this project", () => {
        if (S.pg) Object.assign(S.pg, { sel: "", prompt: "" });
        goView("playground");
      })}
      <div className="new-sep" role="separator" />
      <div className="new-head">Chat with</div>
      {chatflows === null ? (
        <p className="new-note muted">Loading…</p>
      ) : chatflows.length ? (
        chatflows
          .slice(0, 6)
          .map((f) =>
            item(<Icon.chat />, f.name, "Start a chat: its setup runs, then the chat opens", () =>
              import("./views/FlowRun.jsx").then((x) => x.openFlowRunner(f.id)),
            ),
          )
      ) : (
        <p className="new-note muted">No Chatflows yet: add a Chat step to a flow.</p>
      )}
    </div>
  );
}
