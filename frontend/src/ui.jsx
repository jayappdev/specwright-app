// Shared icons and small components.
import { useEffect, useRef, useState } from "react";
import { STEP_STATUS_LABEL } from "./labels.js";
export { STEP_STATUS_LABEL as STATUS_LABEL };
const svgProps = {
  width: 18,
  height: 18,
  viewBox: "0 0 16 16",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.4,
  strokeLinecap: "round",
  strokeLinejoin: "round",
  "aria-hidden": true,
};
const mk = (paths, size) =>
  function IconSvg() {
    return (
      <svg {...svgProps} {...(size ? { width: size, height: size } : {})}>
        {paths}
      </svg>
    );
  };

export const Icon = {
  spark: mk(
    <path d="M8 1.8l1.5 4.2 4.2 1.5-4.2 1.5L8 13.2 6.5 9 2.3 7.5 6.5 6zM13 11.5l.6 1.4 1.4.6-1.4.6-.6 1.4-.6-1.4-1.4-.6 1.4-.6z" />,
  ),
  wizard: mk(<path d="M2.5 13.5l7-7M9 2v2M13 5h-2M12 2l-1.4 1.4M6.5 3.5l.8.8M12.5 9l-.8-.8" />),
  inputs: mk(<path d="M2 4.5h4l1.2 1.5H14v7H2z" />),
  workflow: mk(
    <>
      <circle cx="3.5" cy="3.5" r="1.6" />
      <circle cx="12.5" cy="8" r="1.6" />
      <circle cx="3.5" cy="12.5" r="1.6" />
      <path d="M5 3.8c4 .2 5.5 1.5 6 3.2M5 12.2c4-.2 5.5-1.5 6-3.2" />
    </>,
  ),
  files: mk(
    <>
      <path d="M4 2h5.5L12.5 5v9h-8.5z" />
      <path d="M9.5 2v3h3M6 8h4.5M6 10.5h4.5" />
    </>,
  ),
  metrics: mk(<path d="M3 13V8.5M7 13V3.5M11 13V6.5M14.5 13.5h-13" />),
  chat: mk(
    <>
      <path d="M2.5 3h11v7.5H7l-3.5 2.8V10.5h-1z" />
      <path d="M5.5 6h5M5.5 8h3" />
    </>,
  ),
  playground: mk(
    <>
      <path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" />
      <path d="M5.5 6.2l1.4 1.3-1.4 1.3M8.3 8.8h2.2" />
    </>,
  ),
  history: mk(
    <>
      <circle cx="8" cy="8" r="5.8" />
      <path d="M8 4.8V8l2.2 1.6" />
    </>,
  ),
  appearance: mk(
    <>
      <circle cx="8" cy="8" r="5.8" />
      <path d="M8 2.2v11.6" />
      <path d="M8 2.2a5.8 5.8 0 010 11.6z" fill="currentColor" stroke="none" />
    </>,
  ),
  shield: mk(
    <>
      <path d="M8 1.8l5 1.8v4.2c0 3.1-2.1 5.5-5 6.5-2.9-1-5-3.4-5-6.5V3.6z" />
      <path d="M5.8 8l1.6 1.6 2.9-3.2" />
    </>,
  ),
  lock: mk(
    <>
      <rect x="3.5" y="7" width="9" height="6.5" rx="1.2" />
      <path d="M5.5 7V5a2.5 2.5 0 015 0v2" />
    </>,
    13,
  ),
  check: mk(<path d="M3.5 8.5l3 3 6-7" />, 14),
  book: mk(
    <path d="M3 2.5h4.5a1.5 1.5 0 011.5 1.5v9.5a1.5 1.5 0 00-1.5-1.5H3zM13 2.5H9.5A1.5 1.5 0 008 4v9.5a1.5 1.5 0 011.5-1.5H13z" />,
  ),
  flow: mk(
    <>
      <rect x="1.8" y="2.5" width="4.5" height="3.5" rx="1" />
      <rect x="9.7" y="6.3" width="4.5" height="3.5" rx="1" />
      <rect x="1.8" y="10" width="4.5" height="3.5" rx="1" />
      <path d="M6.3 4.2h1.4a1.3 1.3 0 011.3 1.3v1.9h.7M6.3 11.8h1.4A1.3 1.3 0 009 10.5V8.6" />
    </>,
  ),
  run: mk(<path d="M4.5 3l9 5-9 5z" />),
  plus: mk(<path d="M8 3.5v9M3.5 8h9" />, 14),
  minus: mk(<path d="M3.5 8h9" />, 14),
  bell: mk(
    <>
      <path d="M4 11.5V7.2a4 4 0 018 0v4.3l1 1H3z" />
      <path d="M6.6 14a1.5 1.5 0 002.8 0" />
    </>,
    14,
  ),
  search: mk(
    <>
      <circle cx="7" cy="7" r="4.2" />
      <path d="M10.2 10.2L13.5 13.5" />
    </>,
  ),
  fit: mk(
    <>
      <path d="M2.5 6V3.5a1 1 0 011-1H6M10 2.5h2.5a1 1 0 011 1V6M13.5 10v2.5a1 1 0 01-1 1H10M6 13.5H3.5a1 1 0 01-1-1V10" />
    </>,
    14,
  ),
  undo: mk(
    <>
      <path d="M5.5 4.5L3 7l2.5 2.5" />
      <path d="M3 7h6.5a3.5 3.5 0 010 7H8" />
    </>,
    14,
  ),
  back: mk(<path d="M10 3.5L5.5 8l4.5 4.5" />, 16),
  forward: mk(<path d="M6 3.5L10.5 8 6 12.5" />, 16),
  refresh: mk(
    <>
      <path d="M13 8a5 5 0 11-1.5-3.6" />
      <path d="M13 2.5v3h-3" />
    </>,
    14,
  ),
  redo: mk(
    <>
      <path d="M10.5 4.5L13 7l-2.5 2.5" />
      <path d="M13 7H6.5a3.5 3.5 0 000 7H8" />
    </>,
    14,
  ),
  panelL: mk(
    <>
      <rect x="2" y="2.5" width="12" height="11" rx="1.5" />
      <path d="M6 2.5v11" />
    </>,
    14,
  ),
  panelR: mk(
    <>
      <rect x="2" y="2.5" width="12" height="11" rx="1.5" />
      <path d="M10 2.5v11" />
    </>,
    14,
  ),
  clip: mk(
    <path d="M10.5 4.5L5.8 9.2a1.4 1.4 0 002 2l5-5a2.8 2.8 0 00-4-4l-5.2 5.2a4.2 4.2 0 006 6l4.4-4.4" />,
    14,
  ),
  paste: mk(
    <>
      <path d="M5.5 2.5h5v2h-5z" />
      <path d="M5 3.5H3.5v10h9v-10H11" />
      <path d="M6 8h4M6 10.5h3" />
    </>,
    14,
  ),
  x: mk(<path d="M4.5 4.5l7 7M11.5 4.5l-7 7" />, 14),
  warn: mk(
    <>
      <path d="M8 2.5l6 10.5H2z" />
      <path d="M8 6.8v2.8M8 11.6v.1" />
    </>,
    14,
  ),
  file: mk(<path d="M4 2h5l3 3v9H4z" />, 14),
  folder: mk(<path d="M2 4.5h4l1.2 1.5H14v7H2z" />, 14),
  terminal: mk(
    <>
      <rect x="2" y="3" width="12" height="10" rx="1.5" />
      <path d="M4.8 6.3l2 1.7-2 1.7M8.5 10.2h2.7" />
    </>,
    14,
  ),
  tool: mk(
    <path d="M9.5 3a3 3 0 00-2.6 4.2L2.5 11.6l2 2 4.4-4.4A3 3 0 0013 6.5l-1.8 1.8-1.5-.5-.5-1.5L11 4.5A3 3 0 009.5 3z" />,
  ),
  pkg: mk(
    <>
      <path d="M8 1.8l5.5 3v6.4L8 14.2l-5.5-3V4.8z" />
      <path d="M2.5 4.8L8 7.8l5.5-3M8 7.8v6.4" />
    </>,
    14,
  ),
  pencil: mk(<path d="M10.5 2.5l3 3L6 13H3v-3zM9 4l3 3" />),
  download: mk(<path d="M8 2v8M4.5 6.5L8 10l3.5-3.5M2.5 13.5h11" />),
  archive: mk(
    <>
      <path d="M2 3h12v3H2zM3 6v7h10V6" />
      <path d="M6.5 8.5h3" />
    </>,
  ),
  trash: mk(<path d="M2.5 4h11M6 4V2.5h4V4M4 4l.7 9.5h6.6L12 4M6.8 6.5v5M9.2 6.5v5" />),
  send: mk(<path d="M2 8l12-5.5L9.5 14 7.5 9zM7.5 9L14 2.5" />),
  copy: mk(
    <>
      <rect x="5" y="5" width="8.5" height="8.5" rx="1.5" />
      <path d="M3 10.5V3.5A1 1 0 0 1 4 2.5h7" />
    </>,
  ),
  clock: mk(
    <>
      <circle cx="8" cy="8" r="6" />
      <path d="M8 4.5V8l2.5 1.5" />
    </>,
  ),
  user: mk(
    <>
      <circle cx="8" cy="5.5" r="2.8" />
      <path d="M2.8 14c.6-2.8 2.7-4.3 5.2-4.3s4.6 1.5 5.2 4.3" />
    </>,
  ),
};

export function Pill({ s, label }) {
  return <span className={`pill pill-${s}`}>{label || STEP_STATUS_LABEL[s] || s}</span>;
}

export function Seg({ value, options, onChange }) {
  return (
    <div className="seg" role="group">
      {options.map(([v, l]) => (
        <button key={v} type="button" aria-pressed={value === v} onClick={() => onChange(v)}>
          {l}
        </button>
      ))}
    </div>
  );
}

export function Chip({ on, onClick, children }) {
  return (
    <button type="button" className="chip" aria-pressed={!!on} onClick={onClick}>
      {children}
    </button>
  );
}

// usePoll calls load every `ms` while the screen is shown and the window is
// in front, and once when the window comes back to the front (runs a
// schedule started meanwhile show up without pressing Refresh).
export function usePoll(load, ms, deps = []) {
  const fn = useRef(load);
  fn.current = load;
  useEffect(() => {
    let busy = false;
    const tick = async () => {
      if (busy || document.hidden) return;
      busy = true;
      try {
        await fn.current();
      } catch {
        /* the next tick tries again */
      } finally {
        busy = false;
      }
    };
    const id = setInterval(tick, ms);
    const shown = () => !document.hidden && tick();
    document.addEventListener("visibilitychange", shown);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", shown);
    };
  }, [ms, ...deps]);
}

// RefreshButton reloads a list; it turns while the load runs.
export function RefreshButton({ onRefresh, label = "Refresh", every }) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      className={"icon-btn refresh-btn" + (busy ? " busy" : "")}
      aria-label={label}
      title={label + (every ? ` (it also refreshes by itself every ${every} s)` : "")}
      disabled={busy}
      onClick={async () => {
        setBusy(true);
        try {
          await onRefresh();
        } finally {
          setBusy(false);
        }
      }}
    >
      <Icon.refresh />
    </button>
  );
}

export function Spinner({ label }) {
  return <span className="spinner" aria-label={label} />;
}

export function Card({ id, index, title, badge, extra, className = "", style, children }) {
  return (
    <div className={"card " + className} id={id} style={style}>
      {(title || index) && (
        <div className="card-head">
          {index != null && <span className="card-index">{index}</span>}
          <h3>{title}</h3>
          {badge}
          {extra}
        </div>
      )}
      {children}
    </div>
  );
}

// PaneHandle: a drag handle on the edge of a side pane. It sits in the pane's grid
// container (absolutely positioned, so it takes no grid cell), follows the pane's
// edge, and sets the CSS variable `v` on the container, which the container's
// grid-template-columns reads. Widths are kept per `k` in S.panes (saved with prefs).
// Drag, or focus it and use ←/→ (⇧ for bigger steps); double-click or Home resets.
// It hides itself while the pane is hidden (narrow windows).
export function PaneHandle({
  k,
  v,
  pane,
  edge = "right",
  min = 160,
  max = 640,
  label = "Resize the side panel",
  panes,
  save,
  className = "",
}) {
  const ref = useRef(null),
    [pos, setPos] = useState(null);
  const box = () => ref.current?.parentElement,
    target = () => box()?.querySelector(pane);
  const apply = (w) => {
    const b = box();
    if (!b) return;
    if (w == null) b.style.removeProperty(v);
    else b.style.setProperty(v, w + "px");
  };
  const measure = () => {
    const t = target(),
      b = box();
    if (!t || !b || !t.offsetWidth || getComputedStyle(t).display === "none") return setPos(null);
    setPos({ x: edge === "right" ? t.offsetLeft + t.offsetWidth : t.offsetLeft, w: t.offsetWidth });
  };
  useEffect(() => {
    apply(panes[k]);
    const t = target(),
      b = box();
    if (!t || !b) return;
    const ro = new ResizeObserver(measure);
    ro.observe(t);
    ro.observe(b);
    measure();
    return () => ro.disconnect();
  }, [k]);
  const set = (w) => {
    const x = Math.round(Math.min(max, Math.max(min, w)));
    panes[k] = x;
    apply(x);
    save();
    measure();
  };
  const reset = () => {
    delete panes[k];
    apply(null);
    save();
    setTimeout(measure, 0);
  };
  const down = (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const x0 = e.clientX,
      w0 = target()?.offsetWidth || 0,
      sign = edge === "right" ? 1 : -1;
    document.body.classList.add("pane-dragging");
    const move = (ev) => set(w0 + sign * (ev.clientX - x0));
    const up = () => {
      document.body.classList.remove("pane-dragging");
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  const key = (e) => {
    const step = e.shiftKey ? 48 : 16,
      w = target()?.offsetWidth || 0,
      sign = edge === "right" ? 1 : -1;
    if (e.key === "ArrowRight") {
      e.preventDefault();
      set(w + sign * step);
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      set(w - sign * step);
    } else if (e.key === "Home") {
      e.preventDefault();
      reset();
    }
  };
  return (
    <div
      ref={ref}
      className={"pane-handle " + className}
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={pos?.w}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={pos ? 0 : -1}
      title={label + " · double-click to reset"}
      style={pos ? { left: pos.x - 3 } : { display: "none" }}
      onPointerDown={down}
      onDoubleClick={reset}
      onKeyDown={key}
    />
  );
}

// usePopover: open state for a button + popover that closes on Esc (focus back
// on the button) and on a click outside the wrapper.
export function usePopover() {
  const [open, setOpen] = useState(false),
    wrap = useRef(null),
    btn = useRef(null);
  useEffect(() => {
    if (!open) return;
    const down = (e) => {
      if (!wrap.current?.contains(e.target)) setOpen(false);
    };
    const key = (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        setOpen(false);
        btn.current?.focus();
      }
    };
    window.addEventListener("mousedown", down);
    window.addEventListener("keydown", key, true);
    return () => {
      window.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key, true);
    };
  }, [open]);
  return { open, setOpen, wrap, btn };
}

// Menu: a "⋯" button with a list of actions. items: [label, onClick, { danger, disabled, title }]
// Arrow keys move between items; Esc or a click outside closes it.
export function Menu({ label = "More actions", items, className = "" }) {
  const p = usePopover(),
    list = useRef(null);
  useEffect(() => {
    if (p.open) list.current?.querySelector("button:not(:disabled)")?.focus();
  }, [p.open]);
  const move = (e) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const all = [...list.current.querySelectorAll("button:not(:disabled)")],
      k = all.indexOf(document.activeElement);
    all[(k + (e.key === "ArrowDown" ? 1 : all.length - 1)) % all.length]?.focus();
  };
  return (
    <span className={"menu " + className} ref={p.wrap}>
      <button
        ref={p.btn}
        type="button"
        className="btn secondary menu-btn"
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={p.open}
        onClick={() => p.setOpen(!p.open)}
      >
        ⋯
      </button>
      {p.open && (
        <div className="menu-list" role="menu" ref={list} onKeyDown={move}>
          {items.filter(Boolean).map(([l, fn, o = {}]) => (
            <button
              key={l}
              type="button"
              role="menuitem"
              className={o.danger ? "danger" : ""}
              disabled={o.disabled}
              title={o.title}
              onClick={() => {
                p.setOpen(false);
                fn();
              }}
            >
              {l}
            </button>
          ))}
        </div>
      )}
    </span>
  );
}

// EmptyState: what goes here, and the next thing to do. actions: [label, onClick, primary?]
export function EmptyState({ icon, title, children, actions = [], compact }) {
  return (
    <div className={"empty-state" + (compact ? " compact" : "")} role="status">
      {icon && (
        <span className="empty-icon" aria-hidden="true">
          {icon}
        </span>
      )}
      {title && <b className="empty-title">{title}</b>}
      {children && <p className="muted">{children}</p>}
      {actions.length > 0 && (
        <div className="row-actions">
          {actions.filter(Boolean).map(([l, fn, primary]) => (
            <button
              key={l}
              type="button"
              className={"btn " + (primary ? "primary" : "secondary")}
              onClick={fn}
            >
              {l}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function Stat({ v, l, sub, cls = "mstat" }) {
  return (
    <div className={cls}>
      <b>{v}</b>
      <span>{l}</span>
      {sub ? <small>{sub}</small> : null}
    </div>
  );
}
