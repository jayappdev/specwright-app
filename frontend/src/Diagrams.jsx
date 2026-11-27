// HtmlFrame and MdView: HTML and Markdown previews with their Mermaid and
// PlantUML diagrams drawn (diagrams.js). The text shows at once; the
// diagrams take its place as they are drawn.
import { useEffect, useState } from "react";
import { hasDiagrams, renderDiagramsIn } from "./diagrams.js";
import { mdRender } from "./markdown.js";

const done = new Map(); // source -> drawn, so a preview opened again is instant
function useDrawn(html, page) {
  const key = (page ? "p" : "f") + html;
  const [out, setOut] = useState(() => done.get(key) || html);
  useEffect(() => {
    if (done.has(key)) return setOut(done.get(key));
    setOut(html);
    if (!hasDiagrams(html)) return;
    let live = true;
    renderDiagramsIn(html, { page })
      .then((x) => {
        if (done.size > 50) done.clear();
        done.set(key, x);
        if (live) setOut(x);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [key]);
  return out;
}

// HtmlFrame: a page in a sandboxed frame (no scripts), diagrams drawn
export function HtmlFrame({ html, ...props }) {
  const out = useDrawn(String(html || ""), true);
  return <iframe sandbox="" srcDoc={out} title="HTML preview" {...props} />;
}

// MdView: Markdown as HTML, diagrams drawn (```mermaid, ```plantuml)
export function MdView({ text, className }) {
  const out = useDrawn(mdRender(String(text || "")), false);
  return <div className={className} dangerouslySetInnerHTML={{ __html: out }} />;
}
