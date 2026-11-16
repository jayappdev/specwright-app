// The flow canvas: steps, connections, selection, drag, zoom and fit, and the
// keyboard model (move, connect, undo). Part of the Flow designer.
import { loopInfo } from "../../flowNames.js";
import { useEffect, useRef, useState } from "react";
import { S, update } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { register } from "../../shortcuts.js";
import {
  GATHER_MODES,
  GRID,
  MERGE_MODES,
  NODE_W,
  PORT_Y,
  SPLIT_MODES,
  StepIcon,
  addNode,
  changed,
  connect,
  copySteps,
  edgeLabel,
  fl,
  nodeOf,
  pasteFromClipboard,
  pasteSteps,
  readClip,
  redo,
  removeSelected,
  save,
  selectedIds,
  setSelection,
  snap,
  togglePane,
  toolLabel,
  tools,
  undo,
} from "../Flows.jsx";
import { condCount } from "./Conditions.jsx";

// --- canvas ------------------------------------------------------------------------------

// fitView: zoom and scroll so every step is in view
let canvasEl = null;
function fitView() {
  const f = fl(),
    el = canvasEl;
  if (!f.flow || !el) return;
  const ns = f.flow.nodes,
    x1 = Math.min(...ns.map((n) => n.x)),
    y1 = Math.min(...ns.map((n) => n.y));
  const x2 = Math.max(...ns.map((n) => n.x + NODE_W)),
    y2 = Math.max(...ns.map((n) => n.y + 90));
  const pad = 40,
    z = Math.min(
      1.2,
      Math.max(
        0.4,
        Math.min((el.clientWidth - pad * 2) / (x2 - x1 || 1), (el.clientHeight - pad * 2) / (y2 - y1 || 1)),
      ),
    );
  f.zoom = +z.toFixed(2);
  update();
  requestAnimationFrame(() => {
    el.scrollLeft = Math.max(0, x1 * f.zoom - pad);
    el.scrollTop = Math.max(0, y1 * f.zoom - pad);
  });
}

export function Canvas() {
  const f = fl(),
    flow = f.flow,
    box = useRef(null),
    [drag, setDrag] = useState(null),
    [link, setLink] = useState(null),
    [band, setBand] = useState(null);
  const sel = new Set(selectedIds());
  const ts = tools();
  const z = f.zoom;
  const toLocal = (e) => {
    const r = box.current.getBoundingClientRect();
    return {
      x: (e.clientX - r.left + box.current.scrollLeft) / z,
      y: (e.clientY - r.top + box.current.scrollTop) / z,
    };
  };
  useEffect(() => {
    if (!drag && !link && !band) return;
    const move = (e) => {
      if (drag?.group) {
        const p = toLocal(e),
          dx = p.x - drag.p0.x,
          dy = p.y - drag.p0.y;
        for (const [id, s0] of Object.entries(drag.group)) {
          const n = nodeOf(id);
          if (n) {
            n.x = Math.max(0, snap(s0.x + dx));
            n.y = Math.max(0, snap(s0.y + dy));
          }
        }
        drag.moved = true;
        update();
      } else if (drag) {
        const p = toLocal(e);
        const n = nodeOf(drag.id);
        if (n) {
          n.x = Math.max(0, snap(p.x - drag.dx));
          n.y = Math.max(0, snap(p.y - drag.dy));
          drag.moved = true;
          update();
        }
      }
      if (band) setBand({ ...band, ...toLocal(e), moved: true });
      if (link) setLink({ ...link, ...toLocal(e) });
    };
    const up = (e) => {
      if (drag?.moved) changed();
      if (band) {
        // select the steps the rectangle touches
        if (band.moved) {
          const x1 = Math.min(band.x0, band.x),
            x2 = Math.max(band.x0, band.x),
            y1 = Math.min(band.y0, band.y),
            y2 = Math.max(band.y0, band.y);
          const hit = fl()
            .flow.nodes.filter(
              (n) => n.type !== "start" && n.x < x2 && n.x + NODE_W > x1 && n.y < y2 && n.y + 64 > y1,
            )
            .map((n) => n.id);
          setSelection(band.add ? [...selectedIds(), ...hit] : hit);
        } else {
          f.sel = null;
          f.multi = [];
          f.selEdge = null;
          update();
        }
        setBand(null);
      }
      if (link) {
        const el = document.elementFromPoint(e.clientX, e.clientY)?.closest("[data-node]");
        if (el) connect(link.from, el.dataset.node);
      }
      setDrag(null);
      setLink(null);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
    return () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
  }, [drag, link, band]);
  useEffect(() => {
    const busy = (e) =>
      e.target?.closest?.("input, textarea, select, [contenteditable=true]") ||
      document.querySelector(".drawer-scrim, .file-preview, .ai-designer") ||
      String(window.getSelection?.() || "").trim();
    const key = (e) => {
      // ⌘S / Ctrl+S saves the flow, also while typing in the inspector (not under another editor or dialog)
      if (
        (e.metaKey || e.ctrlKey) &&
        e.key.toLowerCase() === "s" &&
        !e.shiftKey &&
        !e.altKey &&
        fl().flow &&
        !document.querySelector(".drawer-scrim, .file-preview, .ai-designer, [role=dialog]")
      ) {
        e.preventDefault();
        if (fl().dirty && !fl().saving) save();
        return;
      }
      if ((e.metaKey || e.ctrlKey) && e.key === "\\" && !document.querySelector("[role=dialog]")) {
        e.preventDefault();
        return togglePane("flows:insp:c");
      }
      if (busy(e)) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "z") {
        e.preventDefault();
        return e.shiftKey ? redo() : undo();
      }
      if (mod && e.key.toLowerCase() === "y") {
        e.preventDefault();
        return redo();
      }
      if (e.key === "Escape" && f.kbLink) {
        f.kbLink = null;
        update();
        return;
      }
      // arrows move the selected steps (⇧: 5 grid steps)
      if (!mod && !e.altKey && e.key.startsWith("Arrow") && selectedIds().length) {
        e.preventDefault();
        const d = (e.shiftKey ? 5 : 1) * GRID,
          dx = e.key === "ArrowLeft" ? -d : e.key === "ArrowRight" ? d : 0,
          dy = e.key === "ArrowUp" ? -d : e.key === "ArrowDown" ? d : 0;
        for (const id of selectedIds()) {
          const n = nodeOf(id);
          if (n) {
            n.x = Math.max(0, n.x + dx);
            n.y = Math.max(0, n.y + dy);
          }
        }
        return changed();
      }
      // C: connect the selected step to another one (Tab to it, Enter)
      if (!mod && !e.altKey && e.key.toLowerCase() === "c" && f.sel && nodeOf(f.sel)?.type !== "output") {
        e.preventDefault();
        f.kbLink = f.sel;
        return update();
      }
      if (!mod && !e.altKey && e.key.toLowerCase() === "f") {
        e.preventDefault();
        return fitView();
      }
      if ((e.key === "Delete" || e.key === "Backspace") && (f.sel || f.selEdge != null)) {
        e.preventDefault();
        removeSelected();
      }
      if (e.key === "Escape") {
        f.sel = null;
        f.multi = [];
        f.selEdge = null;
        update();
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "a") {
        e.preventDefault();
        setSelection(
          fl()
            .flow.nodes.filter((n) => n.type !== "start")
            .map((n) => n.id),
        );
      }
      // ⌘C / ⌘V as keys too: the desktop app may have no Edit menu to fire copy and paste events
      if (
        (e.metaKey || e.ctrlKey) &&
        e.key.toLowerCase() === "c" &&
        selectedIds().some((id) => nodeOf(id)?.type !== "start")
      ) {
        e.preventDefault();
        copySteps();
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "v") {
        e.preventDefault();
        pasteFromClipboard();
      }
    };
    // copy and paste through the system clipboard (no permission needed)
    const copy = (e) => {
      if (!busy(e)) copySteps(e);
    };
    const paste = (e) => {
      if (busy(e)) return;
      const c = readClip(e.clipboardData?.getData("text/plain") || "");
      if (c) {
        e.preventDefault();
        S.flowClip = c;
        pasteSteps(c);
      }
    };
    // the canvas keys go through the app's shortcut registry; a key counts as
    // handled when the handler called preventDefault (others reach global ones)
    const off = register({
      id: "flow-canvas",
      scope: "flows",
      inFields: true,
      help: [
        ["mod+s", "Save the flow"],
        ["mod+z", "Undo"],
        ["mod+shift+z", "Redo"],
        ["mod+\\", "Show or hide the inspector"],
        ["f", "Fit all steps in view"],
        ["arrowup", "Move the selected steps (arrows; ⇧ for bigger steps)"],
        ["c", "Connect the selected step: then Tab to a step and Enter"],
        ["mod+a", "Select all steps"],
        ["mod+c", "Copy the selected steps"],
        ["mod+v", "Paste steps"],
      ],
      run: (e) => {
        key(e);
        return e.defaultPrevented ? undefined : false;
      },
    });
    window.addEventListener("copy", copy);
    window.addEventListener("paste", paste);
    return () => {
      off();
      window.removeEventListener("copy", copy);
      window.removeEventListener("paste", paste);
    };
  }, []);
  const wheel = (e) => {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    f.zoom = Math.min(1.6, Math.max(0.4, +(z - e.deltaY * 0.002).toFixed(2)));
    update();
  };
  const issuesFor = (id) => f.issues.filter((i) => i.node === id);
  const W = Math.max(1400, ...flow.nodes.map((n) => n.x + NODE_W + 400)),
    H = Math.max(800, ...flow.nodes.map((n) => n.y + 400));
  const port = (n, out) => ({ x: n.x + (out ? NODE_W : 0), y: n.y + PORT_Y });
  const path = (a, b) => {
    const dx = Math.max(40, Math.abs(b.x - a.x) / 2);
    return `M${a.x},${a.y} C${a.x + dx},${a.y} ${b.x - dx},${b.y} ${b.x},${b.y}`;
  };
  const loops = loopInfo(flow);
  // a Gather loops back to its Loop: a dashed path under the region, "next item"
  const back = Object.entries(loops.gather)
    .map(([l, g]) => {
      const a = nodeOf(g),
        b = nodeOf(l);
      if (!a || !b) return null;
      const region = flow.nodes.filter(
        (x) => (loops.stack[x.id] || []).includes(l) || x.id === l || x.id === g,
      );
      const low = Math.max(...region.map((x) => x.y)) + 92;
      const ax = a.x + NODE_W / 2,
        bx = b.x + NODE_W / 2;
      return {
        k: l,
        d: `M${ax},${a.y + 64} C${ax},${low} ${bx},${low} ${bx},${b.y + 64}`,
        x: (ax + bx) / 2,
        y: low - 12,
      };
    })
    .filter(Boolean);
  return (
    <div
      className="fl-canvas"
      ref={(el) => {
        box.current = el;
        canvasEl = el;
      }}
      onWheel={wheel}
      onDragOver={(e) => {
        if ([...e.dataTransfer.types].includes("application/x-flow-step")) e.preventDefault();
      }}
      onDrop={(e) => {
        const spec = e.dataTransfer.getData("application/x-flow-step");
        if (!spec) return;
        e.preventDefault();
        const p = toLocal(e);
        f.sel = null;
        addNode(JSON.parse(spec), p.x - NODE_W / 2, p.y - 24);
      }}
      onMouseDown={(e) => {
        // empty canvas: draw a rectangle to select steps
        if (
          e.button !== 0 ||
          !(
            e.target === box.current ||
            e.target.classList.contains("fl-plane") ||
            e.target.classList.contains("fl-edges")
          )
        )
          return;
        const p = toLocal(e);
        setBand({ x0: p.x, y0: p.y, x: p.x, y: p.y, add: e.shiftKey || e.metaKey || e.ctrlKey });
      }}
    >
      <div className="fl-plane" style={{ width: W, height: H, transform: `scale(${z})` }}>
        <svg className="fl-edges" width={W} height={H}>
          {flow.edges.map((e, k) => {
            const a = nodeOf(e.from),
              b = nodeOf(e.to);
            if (!a || !b) return null;
            const d = path(port(a, true), port(b, false));
            return (
              <g
                key={k}
                className={"fl-edge" + (f.selEdge === k ? " sel" : "")}
                onMouseDown={(ev) => {
                  ev.stopPropagation();
                  f.selEdge = k;
                  f.sel = null;
                  update();
                }}
              >
                <path d={d} className="hit" />
                <path d={d} className="line" markerEnd="url(#fl-arrow)" />
                {(() => {
                  const lab = edgeLabel(a, b, e);
                  if (!lab) return null;
                  const p1 = port(a, true),
                    p2 = port(b, false);
                  return (
                    <text
                      x={(p1.x + p2.x) / 2}
                      y={(p1.y + p2.y) / 2 - 6}
                      className="fl-edge-label"
                      textAnchor="middle"
                    >
                      {lab}
                    </text>
                  );
                })()}
              </g>
            );
          })}
          {back.map((b) => (
            <g key={"loop-" + b.k} className="fl-loopback">
              <path d={b.d} markerEnd="url(#fl-arrow)" />
              <text x={b.x} y={b.y} textAnchor="middle">
                next item ↺
              </text>
            </g>
          ))}
          {link &&
            (() => {
              const a = nodeOf(link.from);
              return a && link.x != null ? (
                <path d={path(port(a, true), { x: link.x, y: link.y })} className="fl-linking" />
              ) : null;
            })()}
          {band?.moved && (
            <rect
              className="fl-band"
              x={Math.min(band.x0, band.x)}
              y={Math.min(band.y0, band.y)}
              width={Math.abs(band.x - band.x0)}
              height={Math.abs(band.y - band.y0)}
            />
          )}
          <defs>
            <marker
              id="fl-arrow"
              viewBox="0 0 10 10"
              refX="9"
              refY="5"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M0,0 L10,5 L0,10 z" />
            </marker>
          </defs>
        </svg>
        {flow.nodes.map((n) => {
          const is = issuesFor(n.id),
            err = is.some((i) => i.level === "error");
          const sub =
            n.type === "tool"
              ? n.instruction?.trim()
                ? "✦ " + n.instruction.trim().slice(0, 44)
                : toolLabel(n.tool)
              : n.type === "start"
                ? `${flow.inputs.length} input${flow.inputs.length === 1 ? "" : "s"}`
                : n.type === "transform"
                  ? f.ops?.ops?.[n.op]?.name || n.op
                  : n.type === "merge"
                    ? MERGE_MODES.find(([m]) => m === n.mode)?.[1] || n.mode
                    : n.type === "output"
                      ? (n.format || "").toUpperCase() +
                        " · " +
                        (n.actions || []).map((a) => a.type).join(", ")
                      : n.type === "ai"
                        ? (n.prompt || "").slice(0, 40) || "No prompt yet"
                        : n.type === "improve"
                          ? (n.pair === "after" ? "Tool after checks · " : "Fixes the tool before · ") +
                            `≤ ${n.rounds || 5} rounds` +
                            ((n.approve || "done") !== "none" ? " · you confirm" : "")
                          : n.type === "loop"
                            ? (SPLIT_MODES.find(([m]) => m === (n.split || "auto"))?.[1] || n.split) +
                              (loops.gather[n.id]
                                ? " → " + (nodeOf(loops.gather[n.id])?.title || loops.gather[n.id])
                                : " · no Gather yet")
                            : n.type === "gather"
                              ? GATHER_MODES.find(([m]) => m === (n.mode || "concat"))?.[1] || n.mode
                              : n.type === "decision"
                                ? `${(n.branches || []).length} path${(n.branches || []).length === 1 ? "" : "s"} + otherwise` +
                                  (n.mode === "all" ? " · every match" : "")
                                : n.type === "filter"
                                  ? (n.mode === "drop" ? "Drop" : "Keep") +
                                    ` rows · ${condCount(n.cond)} condition${condCount(n.cond) === 1 ? "" : "s"}`
                                  : n.type === "aggregate"
                                    ? (n.mode === "window" ? "Window" : "Group") +
                                      ((n.groupBy || []).length
                                        ? " by " + n.groupBy.join(", ")
                                        : " · all rows")
                                    : "";
          const inLoop = loops.stack[n.id] || [],
            loopName = inLoop.length
              ? nodeOf(inLoop[inLoop.length - 1])?.title || inLoop[inLoop.length - 1]
              : "";
          return (
            <div
              key={n.id}
              data-node={n.id}
              className={
                "fl-node t-" +
                n.type +
                (f.sel === n.id || sel.has(n.id) ? " sel" : "") +
                (err ? " has-err" : is.length ? " has-warn" : "") +
                (link || f.kbLink ? " linking" : "") +
                (f.kbLink === n.id ? " kb-from" : "") +
                (inLoop.length ? " in-loop" : "")
              }
              style={{ left: n.x, top: n.y, width: NODE_W }}
              tabIndex={0}
              role="button"
              aria-label={`${n.title || n.id}, ${n.type} step${is.length ? `, ${is.length} problem${is.length === 1 ? "" : "s"}` : ""}`}
              aria-pressed={f.sel === n.id || sel.has(n.id)}
              onFocus={() => {
                if (!f.kbLink && f.sel !== n.id && !sel.has(n.id)) {
                  f.sel = n.id;
                  f.multi = [];
                  f.selEdge = null;
                  update();
                }
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && f.kbLink) {
                  e.preventDefault();
                  const from = f.kbLink;
                  f.kbLink = null;
                  if (from !== n.id) connect(from, n.id);
                  else update();
                }
              }}
              onMouseDown={(e) => {
                if (e.button !== 0 || e.target.closest(".fl-port")) return;
                const p = toLocal(e);
                if (e.shiftKey || e.metaKey || e.ctrlKey) {
                  const cur = selectedIds();
                  setSelection(cur.includes(n.id) ? cur.filter((x) => x !== n.id) : [...cur, n.id]);
                  return;
                } // add or remove
                if (sel.has(n.id) && sel.size > 1) {
                  f.sel = n.id;
                  update();
                  setDrag({
                    group: Object.fromEntries(
                      [...sel].map((id) => [id, { x: nodeOf(id).x, y: nodeOf(id).y }]),
                    ),
                    p0: p,
                  });
                  return;
                } // move them all
                f.sel = n.id;
                f.multi = [];
                f.selEdge = null;
                update();
                setDrag({ id: n.id, dx: p.x - n.x, dy: p.y - n.y });
              }}
            >
              {n.type !== "start" && <span className="fl-port in" title="Input" />}
              <div className="fl-node-head">
                <StepIcon n={n} tools={ts} />
                <div className="fl-node-text">
                  <b>{n.title || n.id}</b>
                  <small>{sub}</small>
                </div>
                {is.length > 0 && (
                  <span
                    className={"fl-badge " + (err ? "err" : "warn")}
                    title={is.map((i) => i.message).join("\n")}
                  >
                    {is.length}
                  </span>
                )}
              </div>
              {inLoop.length > 0 && (
                <div
                  className="fl-loop-chip"
                  title={`Runs once per item of ${loopName}${inLoop.length > 1 ? ` (nested ${inLoop.length} deep)` : ""}`}
                >
                  ↻ {inLoop.length > 1 ? "×" + inLoop.length + " " : ""}per item of {loopName}
                </div>
              )}
              {n.type === "start" && flow.inputs.length > 0 && (
                <div className="fl-node-body">
                  {flow.inputs.slice(0, 5).map((i) => (
                    <code
                      key={i.name}
                      title={
                        i.type === "secret"
                          ? `Secret input${i.source ? " · from " + i.source : ""}: masked, never saved with the run`
                          : undefined
                      }
                    >
                      {i.type === "secret" && <Icon.lock />}
                      {i.name}
                    </code>
                  ))}
                  {flow.inputs.length > 5 && <span className="muted">+{flow.inputs.length - 5}</span>}
                </div>
              )}
              {n.type !== "output" && (
                <span
                  className="fl-port out"
                  title="Drag to connect"
                  onMouseDown={(e) => {
                    e.stopPropagation();
                    e.preventDefault();
                    setLink({ from: n.id, ...toLocal(e) });
                  }}
                />
              )}
            </div>
          );
        })}
      </div>
      {flow.nodes.length === 1 && (
        <div className="fl-hint">
          <b>Start by dragging a tool or step here</b>
          <span>
            Drag from a step's ● to another step to connect them. Delete removes the selected step or
            connection.
          </span>
          <span>
            Select several steps with ⇧-click or by drawing a box on the canvas; ⌘C copies them, ⌘V pastes
            them here or into another flow.
          </span>
          <span>
            Keyboard: Tab to a step, arrows move it, C then Tab and Enter connects it, F fits the view, ⌘Z
            undoes.
          </span>
        </div>
      )}
      <div className="fl-zoom" role="toolbar" aria-label="Canvas">
        <button
          className="icon-btn"
          aria-label="Undo"
          title="Undo (⌘Z / Ctrl+Z)"
          disabled={!f.undo?.length}
          onClick={undo}
        >
          <Icon.undo />
        </button>
        <button
          className="icon-btn"
          aria-label="Redo"
          title="Redo (⇧⌘Z / Ctrl+Y)"
          disabled={!f.redo?.length}
          onClick={redo}
        >
          <Icon.redo />
        </button>
        <span className="fl-zoom-sep" />
        <button
          className="icon-btn"
          aria-label="Zoom out"
          title="Zoom out (⌘ + scroll)"
          onClick={() => {
            f.zoom = Math.max(0.4, +(z - 0.1).toFixed(2));
            update();
          }}
        >
          <Icon.minus />
        </button>
        <button
          className="fv-pct"
          title="Back to 100%"
          onClick={() => {
            f.zoom = 1;
            update();
          }}
        >
          {Math.round(z * 100)}%
        </button>
        <button
          className="icon-btn"
          aria-label="Zoom in"
          title="Zoom in (⌘ + scroll)"
          onClick={() => {
            f.zoom = Math.min(1.6, +(z + 0.1).toFixed(2));
            update();
          }}
        >
          <Icon.plus />
        </button>
        <button
          className="icon-btn"
          aria-label="Fit all steps in view"
          title="Fit all steps in view (F)"
          onClick={fitView}
        >
          <Icon.fit />
        </button>
      </div>
      {f.kbLink && (
        <div className="fl-kb-hint" role="status">
          Connecting from <b>{nodeOf(f.kbLink)?.title || f.kbLink}</b>: Tab to a step, Enter connects, Esc
          cancels.
        </div>
      )}
    </div>
  );
}
