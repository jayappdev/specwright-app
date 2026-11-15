// Model picker: a compact pill that opens a searchable list of the models
// the current agent CLI offers, grouped by what they are good at. Used
// wherever the app runs AI (Playground, Files › Ask AI, tool tests, flow
// runs and AI steps, workflow steps). "" means "inherit": the app-wide
// setting, or the CLI's own default. Any other name the CLI understands
// can be typed in.
import { useEffect, useMemo, useRef, useState } from "react";
import { backend } from "./backend.js";
import { S, update } from "./store.js";

const TIERS = [
  ["best", "Most capable", "◆"],
  ["balanced", "Balanced", "●"],
  ["fast", "Fast", "▲"],
  ["auto", "Automatic", "✦"],
];
const TIER = Object.fromEntries(TIERS.map(([k, l, g]) => [k, { l, g }]));

let loading = null;
export function loadModels(force) {
  if (S.models && !force) return Promise.resolve(S.models);
  loading ||= (backend.Models?.() || Promise.resolve([])).then(
    (l) => {
      S.models = l || [];
      loading = null;
      update();
      return S.models;
    },
    () => {
      S.models = [];
      loading = null;
      return [];
    },
  );
  return loading;
}

const agentName = () => (S.setup.provider === "skipped" || !S.setup.provider ? "mock" : S.setup.provider);
export const modelsFor = (agent = agentName()) =>
  (S.models || []).find((m) => m.agent === agent)?.models || [];
const find = (id) => modelsFor().find((m) => m.id === id);

/**
 * value: the chosen model id, "" to inherit.
 * inherit: what "" means here, shown as the default row (e.g. the flow's model).
 * compact: pill only (for toolbars).
 */
export function ModelPicker({ value = "", onChange, inherit, label = "Model", compact, disabled }) {
  const [open, setOpen] = useState(false);
  const [side, setSide] = useState("left");
  const [q, setQ] = useState("");
  const [hi, setHi] = useState(0);
  const wrap = useRef(null),
    search = useRef(null);
  useEffect(() => {
    loadModels();
  }, []);
  useEffect(() => {
    if (!open) return;
    // open toward the side with room, so the list never slides under a sidebar
    const r = wrap.current?.getBoundingClientRect();
    if (r) setSide(r.left + 410 < window.innerWidth ? "left" : "right");
    setQ("");
    setHi(0);
    setTimeout(() => search.current?.focus(), 20);
    const off = (e) => {
      if (!wrap.current?.contains(e.target)) setOpen(false);
    };
    document.addEventListener("mousedown", off);
    return () => document.removeEventListener("mousedown", off);
  }, [open]);

  const fallback = inherit ?? S.setup.model ?? "";
  const defLabel = fallback ? find(fallback)?.label || fallback : "CLI default";
  const models = modelsFor();
  const rows = useMemo(() => {
    const t = q.trim().toLowerCase();
    const hit = (m) => !t || (m.id + " " + m.label + " " + (m.note || "")).toLowerCase().includes(t);
    const out = [];
    if (!t || "default inherit".includes(t))
      out.push({
        id: "",
        label: "Default",
        note: fallback ? "Uses " + defLabel : "The agent CLI picks its default model",
        tier: "",
      });
    for (const [k] of TIERS)
      for (const m of models.filter((x) => (x.tier || "balanced") === k && hit(x))) out.push(m);
    if (t && !models.some((m) => m.id.toLowerCase() === t))
      out.push({
        id: q.trim(),
        label: "Use “" + q.trim() + "”",
        note: "Any model name the CLI understands",
        tier: "",
        custom: true,
      });
    return out;
  }, [q, models.length, fallback]);

  const pick = (id) => {
    onChange(id);
    setOpen(false);
  };
  const cur = value ? find(value) : null;
  const shown = value ? cur?.label || value : defLabel;
  const tier = (value ? cur?.tier : find(fallback)?.tier) || "";
  const onKey = (e) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHi((h) => Math.min(h + 1, rows.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHi((h) => Math.max(h - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (rows[hi]) pick(rows[hi].id);
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      setOpen(false);
    }
  };
  useEffect(() => {
    document.getElementById("mp-row-" + hi)?.scrollIntoView({ block: "nearest" });
  }, [hi]);

  let lastTier = null;
  return (
    <span className={"mp" + (compact ? " mp-compact" : "")} ref={wrap}>
      <button
        type="button"
        className={"mp-pill" + (value ? " mp-set" : "") + (open ? " open" : "")}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={label + ": " + (value || (fallback ? fallback + " (default)" : "the CLI's default"))}
        onClick={() => setOpen(!open)}
      >
        <span className={"mp-glyph mp-t-" + (tier || "none")} aria-hidden="true">
          {TIER[tier]?.g || "✦"}
        </span>
        {!compact && <span className="mp-label">{label}</span>}
        <span className="mp-value">{shown}</span>
        {!value && <span className="mp-def">default</span>}
        <svg className="mp-caret" viewBox="0 0 16 16" aria-hidden="true">
          <path
            d="M4 6l4 4 4-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>
      {open && (
        <div
          className={"mp-pop mp-" + side}
          role="dialog"
          aria-label={"Choose " + label.toLowerCase()}
          onKeyDown={onKey}
        >
          <div className="mp-search">
            <input
              ref={search}
              value={q}
              placeholder="Search or type a model name…"
              aria-label="Search models"
              onChange={(e) => {
                setQ(e.target.value);
                setHi(0);
              }}
            />
          </div>
          <ul className="mp-list" role="listbox" aria-label="Models">
            {rows.map((m, k) => {
              const t = m.tier || "",
                head = t && t !== lastTier ? TIER[t]?.l : null;
              lastTier = t || lastTier;
              return [
                head && (
                  <li key={"h" + t} className="mp-group" role="presentation">
                    {head}
                  </li>
                ),
                <li
                  key={m.id + (m.custom ? "*" : "")}
                  id={"mp-row-" + k}
                  role="option"
                  aria-selected={m.id === value}
                  className={"mp-row" + (k === hi ? " hi" : "") + (m.id === value ? " sel" : "")}
                  onMouseEnter={() => setHi(k)}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    pick(m.id);
                  }}
                >
                  <span className={"mp-glyph mp-t-" + (t || "none")} aria-hidden="true">
                    {m.custom ? "＋" : TIER[t]?.g || "↺"}
                  </span>
                  <span className="mp-row-text">
                    <b>{m.label}</b>
                    {m.alias && <span className="mp-latest">latest</span>}
                    {m.note && <small>{m.note}</small>}
                  </span>
                  {m.id && !m.custom && <code className="mp-id">{m.id}</code>}
                  {m.id === value && (
                    <span className="mp-check" aria-hidden="true">
                      ✓
                    </span>
                  )}
                </li>,
              ];
            })}
            {!rows.length && <li className="mp-empty muted">No models match.</li>}
          </ul>
          <div className="mp-foot muted">
            {models.length
              ? "↑↓ to move · Enter to choose · Esc to close"
              : "This agent did not list its models; type a name it understands."}
          </div>
        </div>
      )}
    </span>
  );
}
