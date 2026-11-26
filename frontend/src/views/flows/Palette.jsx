// The Flow designer's palette: step types and tools to drag onto the canvas.
import { toolRef } from "../../flowNames.js";
import { S, update } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { ToolIcon } from "../../ToolTile.jsx";
import { openFlowGuide } from "../FlowGuide.jsx";
import { STEP_TYPES, StepIcon, addNode, builtinTools, fl, projectTools, togglePane } from "../Flows.jsx";

// --- palette ---------------------------------------------------------------------------

// Visualization: a chart step (the built-in chart tool, as a tool step).
// steps made of a built-in tool, listed with the step types
const VIZ = [
  {
    type: "visualization",
    name: "Visualization",
    tool: "builtin:chart",
    title: "Chart",
    args: { type: "bar" },
    help: "A chart from rows: bar, line, pie, donut, area, scatter, heatmap, KPI tiles, gauges, bullets, sparklines; shown as a picture, Export PNG",
    keywords: "chart graph plot bar line pie donut area scatter heatmap png kpi gauge bullet sparkline tiles",
  },
  {
    type: "formattedtable",
    name: "Formatted table",
    tool: "builtin:table",
    title: "Table",
    args: {},
    help: "Rows as an HTML table: formats, sorting, totals, colours by thresholds, data bars, sparklines",
    keywords: "table grid report format currency percent totals conditional sparkline",
  },
  {
    type: "dashboard",
    name: "Dashboard",
    tool: "builtin:dashboard",
    title: "Dashboard",
    args: { columns: "2" },
    help: "Several charts, tables and notes on one page, saved as HTML",
    keywords: "dashboard report page panels html summary",
  },
];

export function Palette() {
  const f = fl(),
    match = (t) => !f.q || (t.name + " " + t.desc).toLowerCase().includes(f.q.toLowerCase());
  const ts = projectTools().filter(match),
    bs = builtinTools().filter(match);
  const item = (spec, label, sub, icon) => (
    <div
      key={label}
      className="fl-pal-item"
      draggable
      title={sub}
      role="button"
      tabIndex={0}
      onDragStart={(e) => {
        e.dataTransfer.setData("application/x-flow-step", JSON.stringify(spec));
        e.dataTransfer.effectAllowed = "copy";
      }}
      onClick={() => addNode(spec)}
      onKeyDown={(e) => e.key === "Enter" && addNode(spec)}
    >
      {icon}
      <span>
        <b>{label}</b>
        <small>{sub}</small>
      </span>
    </div>
  );
  if (S.layout["flows:pal:c"])
    return (
      <aside className="fl-palette is-collapsed" aria-label="Steps and tools">
        <button
          className="icon-btn"
          aria-label="Show steps and tools"
          title="Show steps and tools"
          onClick={() => togglePane("flows:pal:c")}
        >
          <Icon.panelL />
        </button>
      </aside>
    );
  return (
    <aside className="fl-palette" aria-label="Steps and tools">
      <div className="tree-folder fl-pal-head">
        Steps{" "}
        <button className="link" title="How each step works, with examples" onClick={() => openFlowGuide("")}>
          How to use them ↗
        </button>
        <button
          className="icon-btn fl-collapse"
          aria-label="Hide steps and tools"
          title="Hide steps and tools"
          onClick={() => togglePane("flows:pal:c")}
        >
          <Icon.panelL />
        </button>
      </div>
      <input
        className="fl-search"
        type="search"
        placeholder="Filter steps and tools"
        aria-label="Filter steps and tools"
        value={f.q}
        onChange={(e) => {
          f.q = e.target.value;
          update();
        }}
      />
      {[...STEP_TYPES, ...VIZ]
        .filter(
          (s) =>
            !f.q ||
            (s.name + " " + s.help + " " + (s.keywords || "")).toLowerCase().includes(f.q.toLowerCase()),
        )
        .sort((a, b) => (a.type === "output") - (b.type === "output")) // Output stays last
        .map((s) =>
          s.tool
            ? item(
                { type: "tool", tool: s.tool, title: s.title, args: { ...s.args } },
                s.name,
                s.help,
                <StepIcon n={{ type: "tool", tool: s.tool }} tools={builtinTools()} size={30} />,
              )
            : item({ type: s.type }, s.name, s.help, <StepIcon n={{ type: s.type }} tools={[]} size={30} />),
        )}
      <div className="tree-folder">Project tools · {projectTools().length}</div>
      {ts.map((t) => item({ type: "tool", tool: t.name }, t.name, t.desc, <ToolIcon t={t} size={30} />))}
      {!projectTools().length && (
        <p className="muted" style={{ padding: "0 6px" }}>
          No tools yet. Create one on the Tools screen.
        </p>
      )}
      <div
        className="tree-folder"
        title="Built into SpecWright: they run directly in the project, without the AI"
      >
        Built-in tools · {builtinTools().length}
      </div>
      {bs.map((t) =>
        item(
          { type: "tool", tool: t.name },
          toolRef(t.name),
          t.desc.replace(/^[^:]*:\s*/, ""),
          <ToolIcon t={t} size={30} />,
        ),
      )}
      {!!f.q &&
        !ts.length &&
        !bs.length &&
        ![...STEP_TYPES, ...VIZ].some((s) =>
          (s.name + " " + s.help + " " + (s.keywords || "")).toLowerCase().includes(f.q.toLowerCase()),
        ) && (
          <p className="muted" style={{ padding: "0 6px" }}>
            Nothing matches “{f.q}”.
          </p>
        )}
      <p className="muted fl-pal-hint">Drag onto the canvas, or click to add after the selected step.</p>
    </aside>
  );
}
