// Small charts for Metrics (one series each, so no legend: the title names
// it). Built to the dataviz rules: one axis, thin marks (bars <= 24px with a
// 4px rounded end, square at the baseline; 2px lines), a recessive hairline
// grid, text in text colours, a hover/focus tooltip on every mark, and a
// table view for every chart. The colour is the validated categorical slot
// 1, stepped separately for dark mode (--viz-1 in components.css).
import { useEffect, useRef, useState } from "react";

function useWidth() {
  const ref = useRef(null),
    [w, setW] = useState(0);
  useEffect(() => {
    if (!ref.current) return undefined;
    const ro = new ResizeObserver(([e]) => setW(Math.floor(e.contentRect.width)));
    ro.observe(ref.current);
    return () => ro.disconnect();
  }, []);
  return [ref, w];
}

// nice axis: 0 and 3-4 round steps to a round top
// (integer: counts, so whole-number steps)
function niceTicks(max, integer) {
  if (!(max > 0)) return [0, 1];
  const raw = max / 4,
    mag = Math.pow(10, Math.floor(Math.log10(raw)));
  let step = [1, 2, 2.5, 5, 10].map((f) => f * mag).find((s) => s >= raw);
  if (integer) step = Math.max(1, Math.ceil(step));
  const out = [];
  for (let v = 0; v <= max + step * 0.001; v += step) out.push(+v.toFixed(10));
  if (out[out.length - 1] < max) out.push(+(out[out.length - 1] + step).toFixed(10));
  return out;
}

export const compact = (v) =>
  Math.abs(v) >= 1e6
    ? (v / 1e6).toFixed(1).replace(/\.0$/, "") + "M"
    : Math.abs(v) >= 1e4
      ? Math.round(v / 1e3) + "K"
      : (Math.round(v * 100) / 100).toLocaleString();

const M = { top: 10, right: 12, bottom: 24, left: 44 };

// ChartFrame: the title, a table toggle, the plot (or its table)
function ChartFrame({ title, sub, points, fmt, children }) {
  const [table, setTable] = useState(false);
  return (
    <figure className="viz">
      <figcaption className="viz-head">
        <span>
          <b>{title}</b>
          {sub && <small className="muted"> · {sub}</small>}
        </span>
        <button className="link viz-toggle" onClick={() => setTable(!table)} aria-pressed={table}>
          {table ? "Chart" : "Table"}
        </button>
      </figcaption>
      {table ? (
        <div className="viz-table">
          <table className="table">
            <thead>
              <tr>
                <th>Period</th>
                <th className="num">{title}</th>
              </tr>
            </thead>
            <tbody>
              {points.map((p) => (
                <tr key={p.key}>
                  <td>{p.label}</td>
                  <td className="num">{fmt(p.value)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        children
      )}
    </figure>
  );
}

function Tooltip({ p, x, fmt }) {
  if (!p) return null;
  return (
    <div className="viz-tip" style={{ left: x }} role="status">
      <b>{fmt(p.value)}</b>
      <span>{p.label}</span>
      {p.detail && <small>{p.detail}</small>}
    </div>
  );
}

// the x labels to print: about six, evenly spaced, always the last
const labelEvery = (n, w) => Math.max(1, Math.ceil(n / Math.max(2, Math.floor(w / 90))));

// ColumnChart: one value per period, as columns from a zero baseline.
// points: [{ key, label, short, value, detail }]
export function ColumnChart({ title, sub, points, fmt = compact, tick = compact, integer, height = 180 }) {
  const [ref, w] = useWidth(),
    [hover, setHover] = useState(-1);
  const pw = Math.max(0, w - M.left - M.right),
    ph = height - M.top - M.bottom;
  const ticks = niceTicks(Math.max(0, ...points.map((p) => p.value)), integer),
    top = ticks[ticks.length - 1] || 1;
  const band = points.length ? pw / points.length : 0,
    bw = Math.max(2, Math.min(24, band * 0.7)),
    y = (v) => M.top + ph - (v / top) * ph,
    every = labelEvery(points.length, pw);
  return (
    <ChartFrame title={title} sub={sub} points={points} fmt={fmt}>
      <div className="viz-plot" ref={ref} onMouseLeave={() => setHover(-1)}>
        {w > 0 && (
          <svg width={w} height={height} role="img" aria-label={title}>
            {ticks.map((t) => (
              <g key={t}>
                <line className="viz-grid" x1={M.left} x2={w - M.right} y1={y(t)} y2={y(t)} />
                <text className="viz-tick" x={M.left - 8} y={y(t)} dy="0.32em" textAnchor="end">
                  {tick(t)}
                </text>
              </g>
            ))}
            {points.map((p, i) => {
              const cx = M.left + band * i + band / 2,
                h = (p.value / top) * ph,
                r = Math.min(4, h, bw / 2);
              return (
                <g key={p.key}>
                  {p.value > 0 && (
                    <path
                      className={"viz-bar" + (hover === i ? " on" : "")}
                      d={`M${cx - bw / 2},${M.top + ph} V${M.top + ph - h + r} a${r},${r} 0 0 1 ${r},${-r} H${cx + bw / 2 - r} a${r},${r} 0 0 1 ${r},${r} V${M.top + ph} Z`}
                    />
                  )}
                  {(i % every === 0 || i === points.length - 1) && (
                    <text className="viz-tick" x={cx} y={height - 6} textAnchor="middle">
                      {p.short || p.label}
                    </text>
                  )}
                  <rect
                    className="viz-hit"
                    x={M.left + band * i}
                    y={M.top}
                    width={band}
                    height={ph}
                    tabIndex={0}
                    aria-label={`${p.label}: ${fmt(p.value)}`}
                    onMouseEnter={() => setHover(i)}
                    onFocus={() => setHover(i)}
                    onBlur={() => setHover(-1)}
                  />
                </g>
              );
            })}
            <line className="viz-axis" x1={M.left} x2={w - M.right} y1={M.top + ph} y2={M.top + ph} />
          </svg>
        )}
        <Tooltip p={points[hover]} x={M.left + band * hover + band / 2} fmt={fmt} />
      </div>
    </ChartFrame>
  );
}

// LineChart: one value per period, as a 2px line over a light wash, a
// crosshair that snaps to the nearest period, and the last value labelled.
export function LineChart({ title, sub, points, fmt = compact, tick = compact, integer, height = 180 }) {
  const [ref, w] = useWidth(),
    [hover, setHover] = useState(-1);
  const pw = Math.max(0, w - M.left - M.right - 36),
    ph = height - M.top - M.bottom;
  const ticks = niceTicks(Math.max(0, ...points.map((p) => p.value)), integer),
    top = ticks[ticks.length - 1] || 1;
  const n = points.length,
    x = (i) => M.left + (n > 1 ? (pw * i) / (n - 1) : pw / 2),
    y = (v) => M.top + ph - (v / top) * ph,
    every = labelEvery(n, pw);
  const line = points.map((p, i) => `${i ? "L" : "M"}${x(i)},${y(p.value)}`).join(" ");
  const area = n ? `${line} L${x(n - 1)},${M.top + ph} L${x(0)},${M.top + ph} Z` : "";
  const move = (e) => {
    const r = e.currentTarget.getBoundingClientRect(),
      px = e.clientX - r.left;
    setHover(n > 1 ? Math.max(0, Math.min(n - 1, Math.round(((px - M.left) / pw) * (n - 1)))) : 0);
  };
  const last = points[n - 1];
  return (
    <ChartFrame title={title} sub={sub} points={points} fmt={fmt}>
      <div className="viz-plot" ref={ref}>
        {w > 0 && (
          <svg
            width={w}
            height={height}
            role="img"
            aria-label={title}
            tabIndex={0}
            onMouseMove={move}
            onMouseLeave={() => setHover(-1)}
            onKeyDown={(e) => {
              if (e.key === "ArrowRight") setHover((h) => Math.min(n - 1, h + 1));
              if (e.key === "ArrowLeft") setHover((h) => Math.max(0, (h < 0 ? n : h) - 1));
            }}
            onBlur={() => setHover(-1)}
          >
            {ticks.map((t) => (
              <g key={t}>
                <line className="viz-grid" x1={M.left} x2={M.left + pw} y1={y(t)} y2={y(t)} />
                <text className="viz-tick" x={M.left - 8} y={y(t)} dy="0.32em" textAnchor="end">
                  {tick(t)}
                </text>
              </g>
            ))}
            {points.map(
              (p, i) =>
                (i % every === 0 || i === n - 1) && (
                  <text key={p.key} className="viz-tick" x={x(i)} y={height - 6} textAnchor="middle">
                    {p.short || p.label}
                  </text>
                ),
            )}
            <line className="viz-axis" x1={M.left} x2={M.left + pw} y1={M.top + ph} y2={M.top + ph} />
            {n > 0 && <path className="viz-area" d={area} />}
            {n > 0 && <path className="viz-line" d={line} />}
            {hover >= 0 && (
              <line className="viz-cross" x1={x(hover)} x2={x(hover)} y1={M.top} y2={M.top + ph} />
            )}
            {last && (
              <>
                <circle className="viz-dot" cx={x(n - 1)} cy={y(last.value)} r={4} />
                <text className="viz-end" x={x(n - 1) + 8} y={y(last.value)} dy="0.32em">
                  {fmt(last.value)}
                </text>
              </>
            )}
            {hover >= 0 && hover !== n - 1 && (
              <circle className="viz-dot" cx={x(hover)} cy={y(points[hover].value)} r={4} />
            )}
          </svg>
        )}
        <Tooltip p={points[hover]} x={hover >= 0 ? x(hover) : 0} fmt={fmt} />
      </div>
    </ChartFrame>
  );
}

// BarList: a ranked list as horizontal bars, the value at each bar's tip
// rows: [{ key, label, value }]
export function BarList({ title, sub, rows, fmt = compact }) {
  const max = Math.max(0, ...rows.map((r) => r.value)) || 1;
  return (
    <ChartFrame title={title} sub={sub} points={rows} fmt={fmt}>
      <div className="viz-bars">
        {rows.map((r) => (
          <div key={r.key} className="viz-barrow" title={`${r.label}: ${fmt(r.value)}`}>
            <span className="viz-barlabel">{r.label}</span>
            <span className="viz-bartrack">
              <span className="viz-barfill" style={{ width: Math.max(0.5, (r.value / max) * 100) + "%" }} />
              <span className="viz-barval">{fmt(r.value)}</span>
            </span>
          </div>
        ))}
      </div>
    </ChartFrame>
  );
}
