// Shared icons and small components.
const svgProps = { width: 18, height: 18, viewBox: "0 0 16 16", fill: "none", stroke: "currentColor", strokeWidth: 1.4, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true };
const mk = (paths, size) => function IconSvg() {
  return <svg {...svgProps} {...(size ? { width: size, height: size } : {})}>{paths}</svg>;
};

export const Icon = {
  spark: mk(<path d="M8 1.8l1.5 4.2 4.2 1.5-4.2 1.5L8 13.2 6.5 9 2.3 7.5 6.5 6zM13 11.5l.6 1.4 1.4.6-1.4.6-.6 1.4-.6-1.4-1.4-.6 1.4-.6z" />),
  wizard: mk(<path d="M2.5 13.5l7-7M9 2v2M13 5h-2M12 2l-1.4 1.4M6.5 3.5l.8.8M12.5 9l-.8-.8" />),
  inputs: mk(<path d="M2 4.5h4l1.2 1.5H14v7H2z" />),
  workflow: mk(<><circle cx="3.5" cy="3.5" r="1.6" /><circle cx="12.5" cy="8" r="1.6" /><circle cx="3.5" cy="12.5" r="1.6" /><path d="M5 3.8c4 .2 5.5 1.5 6 3.2M5 12.2c4-.2 5.5-1.5 6-3.2" /></>),
  files: mk(<><path d="M4 2h5.5L12.5 5v9h-8.5z" /><path d="M9.5 2v3h3M6 8h4.5M6 10.5h4.5" /></>),
  metrics: mk(<path d="M3 13V8.5M7 13V3.5M11 13V6.5M14.5 13.5h-13" />),
  playground: mk(<><path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" /><path d="M5.5 6.2l1.4 1.3-1.4 1.3M8.3 8.8h2.2" /></>),
  history: mk(<><circle cx="8" cy="8" r="5.8" /><path d="M8 4.8V8l2.2 1.6" /></>),
  appearance: mk(<><circle cx="8" cy="8" r="5.8" /><path d="M8 2.2v11.6" /><path d="M8 2.2a5.8 5.8 0 010 11.6z" fill="currentColor" stroke="none" /></>),
  shield: mk(<><path d="M8 1.8l5 1.8v4.2c0 3.1-2.1 5.5-5 6.5-2.9-1-5-3.4-5-6.5V3.6z" /><path d="M5.8 8l1.6 1.6 2.9-3.2" /></>),
  lock: mk(<><rect x="3.5" y="7" width="9" height="6.5" rx="1.2" /><path d="M5.5 7V5a2.5 2.5 0 015 0v2" /></>, 13),
  check: mk(<path d="M3.5 8.5l3 3 6-7" />, 14),
  flow: mk(<><rect x="1.8" y="2.5" width="4.5" height="3.5" rx="1" /><rect x="9.7" y="6.3" width="4.5" height="3.5" rx="1" /><rect x="1.8" y="10" width="4.5" height="3.5" rx="1" /><path d="M6.3 4.2h1.4a1.3 1.3 0 011.3 1.3v1.9h.7M6.3 11.8h1.4A1.3 1.3 0 009 10.5V8.6" /></>),
  run: mk(<path d="M4.5 3l9 5-9 5z" />),
  plus: mk(<path d="M8 3.5v9M3.5 8h9" />, 14),
  minus: mk(<path d="M3.5 8h9" />, 14),
  clip: mk(<path d="M10.5 4.5L5.8 9.2a1.4 1.4 0 002 2l5-5a2.8 2.8 0 00-4-4l-5.2 5.2a4.2 4.2 0 006 6l4.4-4.4" />, 14),
  paste: mk(<><path d="M5.5 2.5h5v2h-5z" /><path d="M5 3.5H3.5v10h9v-10H11" /><path d="M6 8h4M6 10.5h3" /></>, 14),
  x: mk(<path d="M4.5 4.5l7 7M11.5 4.5l-7 7" />, 14),
  warn: mk(<><path d="M8 2.5l6 10.5H2z" /><path d="M8 6.8v2.8M8 11.6v.1" /></>, 14),
  file: mk(<path d="M4 2h5l3 3v9H4z" />, 14),
  folder: mk(<path d="M2 4.5h4l1.2 1.5H14v7H2z" />, 14),
  tool: mk(<path d="M9.5 3a3 3 0 00-2.6 4.2L2.5 11.6l2 2 4.4-4.4A3 3 0 0013 6.5l-1.8 1.8-1.5-.5-.5-1.5L11 4.5A3 3 0 009.5 3z" />),
  pkg: mk(<><path d="M8 1.8l5.5 3v6.4L8 14.2l-5.5-3V4.8z" /><path d="M2.5 4.8L8 7.8l5.5-3M8 7.8v6.4" /></>, 14),
};

export const STATUS_LABEL = { pending: "Pending", running: "Running", paused: "Waiting", done: "Approved", failed: "Rejected", idle: "Idle", cancelled: "Cancelled", interrupted: "Interrupted" };
export function Pill({ s, label }) { return <span className={`pill pill-${s}`}>{label || STATUS_LABEL[s] || s}</span>; }

export function Seg({ value, options, onChange }) {
  return <div className="seg" role="group">{options.map(([v, l]) => <button key={v} type="button" aria-pressed={value === v} onClick={() => onChange(v)}>{l}</button>)}</div>;
}

export function Chip({ on, onClick, children }) {
  return <button type="button" className="chip" aria-pressed={!!on} onClick={onClick}>{children}</button>;
}

export function Spinner({ label }) { return <span className="spinner" aria-label={label} />; }

export function Card({ id, index, title, badge, extra, className = "", style, children }) {
  return (
    <div className={"card " + className} id={id} style={style}>
      {(title || index) && <div className="card-head">{index != null && <span className="card-index">{index}</span>}<h3>{title}</h3>{badge}{extra}</div>}
      {children}
    </div>
  );
}

export function Stat({ v, l, sub, cls = "mstat" }) {
  return <div className={cls}><b>{v}</b><span>{l}</span>{sub ? <small>{sub}</small> : null}</div>;
}
