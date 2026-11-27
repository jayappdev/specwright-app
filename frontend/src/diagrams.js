// Mermaid and PlantUML diagrams inside HTML and Markdown previews.
//
// HTML previews run in a sandboxed frame with no scripts, so a page's own
// Mermaid script cannot draw anything. Instead the app finds the diagrams
// in the page before it is shown, draws each one as SVG here (Mermaid in
// the app; PlantUML on this computer, through the backend), and puts the
// pictures in their place. The frame then only shows SVG. A diagram that
// cannot be drawn keeps its source, with a note saying why.
//
// Found:  <pre class="mermaid">, <div class="mermaid">, <code class="language-mermaid">
//         (what Markdown gives a ```mermaid block), and the same with
//         plantuml / puml; a <pre> whose text starts with @startuml.
import { backend } from "./backend.js";

const MERMAID_START =
  /^(graph|flowchart|sequenceDiagram|classDiagram(-v2)?|stateDiagram(-v2)?|erDiagram|gantt|pie|journey|mindmap|timeline|gitGraph|quadrantChart|requirementDiagram|C4Context|C4Container|C4Component|C4Dynamic|C4Deployment|xychart(-beta)?|sankey(-beta)?|block(-beta)?|packet(-beta)?|architecture(-beta)?|kanban|radar(-beta)?|treemap(-beta)?)\b/;

// hasDiagrams: a cheap first look, so pages without one are shown at once
export function hasDiagrams(html) {
  return /class=["'][^"']*\b(mermaid|plantuml|puml|language-mermaid|language-plantuml|language-puml)\b|@startuml|@startmindmap|@startgantt/i.test(
    String(html || ""),
  );
}

// diagramKind: what an element holds, from its classes and its text
export function diagramKind(classes, text) {
  const c = " " + String(classes || "").toLowerCase() + " ";
  const t = String(text || "").trim();
  if (/[\s-](plantuml|puml)\s/.test(c) || /^@start(uml|mindmap|gantt|salt|wbs|json|yaml)\b/.test(t))
    return "plantuml";
  if (/[\s-]mermaid\s/.test(c)) return "mermaid";
  // a ``` block without a language whose first line is a Mermaid diagram
  if (!c.trim() && MERMAID_START.test(t.replace(/^%%.*\n/gm, "").trim())) return "mermaid";
  return null;
}

// --- drawing ------------------------------------------------------------------------------

let mermaidReady = null;
let seq = 0;
function loadMermaid() {
  const dark = document.documentElement.dataset.mode === "dark";
  mermaidReady ||= import("mermaid").then((m) => {
    m.default.initialize({
      startOnLoad: false,
      securityLevel: "strict", // no HTML labels or click handlers in diagrams
      theme: dark ? "dark" : "default",
      // a named stack: a diagram drawn as an <img> cannot inherit the page's font
      fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Roboto, Helvetica, Arial, sans-serif",
      // labels as SVG text, so a diagram also draws as an <img>
      flowchart: { htmlLabels: false },
      htmlLabels: false,
    });
    return m.default;
  });
  return mermaidReady;
}

const drawn = new Map(); // kind + source -> svg (or an Error)
const remember = (k, v) => {
  if (drawn.size > 200) drawn.clear();
  drawn.set(k, v);
  return v;
};

export async function drawDiagram(kind, src) {
  const key = kind + "\n" + src;
  if (drawn.has(key)) {
    const v = drawn.get(key);
    if (v instanceof Error) throw v;
    return v;
  }
  try {
    if (kind === "plantuml") return remember(key, await backend.RenderPlantUML(src));
    const mermaid = await loadMermaid();
    const { svg } = await mermaid.render("sw-mmd-" + ++seq, src);
    return remember(key, svg);
  } catch (e) {
    // Mermaid's message: where, the line with a pointer, what it expected
    const lines = String(e?.message || e)
      .split("\n")
      .filter((l) => l.trim() && !/^-*\^$/.test(l.trim()));
    const err = new Error(
      lines.length > 1 ? lines[0] + " " + lines[lines.length - 1] : lines[0] || "unknown error",
    );
    remember(key, err);
    throw err;
  }
}

const STYLE = `
.sw-diagram{margin:12px 0;overflow:auto;max-width:100%}
.sw-diagram svg,.sw-diagram img{max-width:100%;height:auto}
.sw-diagram-error{margin:12px 0;border:1px solid #d97706;border-radius:6px;padding:8px 10px;font:13px/1.4 system-ui,sans-serif}
.sw-diagram-error pre{margin:6px 0 0;white-space:pre-wrap;font-size:12px;opacity:.8}
.sw-diagram-error b{color:#b45309}`;

// svgWidth: the width an SVG is drawn at (Mermaid: its max-width;
// PlantUML: its width), so it is not stretched as an <img>
export function svgWidth(svg) {
  const head = String(svg).slice(0, 2000);
  const m =
    /max-width:\s*([\d.]+)px/.exec(head) ||
    /<svg[^>]*\swidth="([\d.]+)(px)?"/.exec(head) ||
    /viewBox="[\d.-]+ [\d.-]+ ([\d.]+) [\d.]+"/.exec(head);
  return m ? Math.round(+m[1]) : 0;
}

// renderDiagramsIn: the HTML with its diagrams drawn (whole page or a
// fragment, as given)
export async function renderDiagramsIn(html, { page = true } = {}) {
  if (!hasDiagrams(html)) return html;
  const doc = new DOMParser().parseFromString(page ? html : `<body>${html}</body>`, "text/html");
  const found = [];
  for (const el of doc.querySelectorAll("pre, code, div.mermaid, div.plantuml")) {
    if (el.tagName === "PRE" && el.querySelector("code")) continue; // its <code> is looked at
    const target = el.tagName === "CODE" && el.parentElement?.tagName === "PRE" ? el.parentElement : el;
    if (el.tagName === "CODE" && target === el) continue; // inline code
    const kind = diagramKind(el.className + " " + (target !== el ? target.className : ""), el.textContent);
    if (kind) found.push({ target, kind, src: el.textContent.trim() });
  }
  if (!found.length) return html;
  for (const f of found) {
    // one after another: Mermaid draws in this page's DOM
    try {
      const svg = await drawDiagram(f.kind, f.src);
      const fig = doc.createElement("figure");
      fig.className = "sw-diagram sw-" + f.kind;
      if (page)
        fig.innerHTML = svg; // a page shows in a sandboxed frame: no script runs
      else {
        // a fragment goes into the app's own page: as a picture, which cannot run anything
        const img = doc.createElement("img");
        img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
        img.alt = (f.kind === "plantuml" ? "PlantUML" : "Mermaid") + " diagram";
        const w = svgWidth(svg);
        if (w) img.style.width = w + "px"; // its own size, at most the column's width
        fig.append(img);
      }
      f.target.replaceWith(fig);
    } catch (e) {
      const box = doc.createElement("div");
      box.className = "sw-diagram-error";
      const b = doc.createElement("b");
      b.textContent = (f.kind === "plantuml" ? "PlantUML" : "Mermaid") + " diagram not drawn: ";
      box.append(b, doc.createTextNode(e.message));
      const pre = doc.createElement("pre");
      pre.textContent = f.src;
      box.append(pre);
      f.target.replaceWith(box);
    }
  }
  const style = doc.createElement("style");
  style.textContent = STYLE;
  (doc.head || doc.body).append(style);
  if (!page) return doc.head.innerHTML + doc.body.innerHTML;
  const doctype = /^\s*<!doctype[^>]*>/i.exec(html)?.[0] || "<!doctype html>";
  return doctype + "\n" + doc.documentElement.outerHTML;
}
