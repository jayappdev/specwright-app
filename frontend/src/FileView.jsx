// FileView: shows one file by type, the same way everywhere (the quick
// preview panel and Utils › Project files):
//   text      line numbers, syntax colors, a highlighted line
//   Markdown, HTML, JSON, CSV  a Preview / Raw data switch (formatted text,
//            the page rendered, tables)
//   image     fit to the pane or at a zoom level, on a checkerboard
//   PDF       the system PDF viewer
//   folder    its entries, each a preview link
//   binary    a short note
// A toolbar zooms in and out (buttons, ⌘/Ctrl + = − 0, ⌘/Ctrl + scroll or
// pinch). Zoom is kept per kind (text, image, PDF) for the whole app.
import { useEffect, useRef, useState } from "react";
import { backend } from "./backend.js";
import { errMsg, update } from "./store.js";
import { highlight } from "./highlight.js";
import { mdRender } from "./markdown.js";
import { Icon } from "./ui.jsx";
import { fmtSize } from "./Attachments.jsx";
import { FileLink } from "./FileLinks.jsx";
import { contentFormat, canPreview, RenderPreview } from "./ContentView.jsx";

export const STEPS = [25, 33, 50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300, 400];
const ZKEY = "specwright.viewerZoom";
const zoomPrefs = (() => { try { return { text: 100, image: "fit", pdf: 100, ...JSON.parse(localStorage.getItem(ZKEY) || "{}") }; } catch { return { text: 100, image: "fit", pdf: 100 }; } })();
const saveZoom = () => { try { localStorage.setItem(ZKEY, JSON.stringify(zoomPrefs)); } catch { /* storage unavailable */ } };

const b64ToBlobUrl = (b64, type) => {
  const bin = atob(b64), bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return URL.createObjectURL(new Blob([bytes], { type }));
};

// Load a file for viewing into `slot` (a plain object kept in the store):
// slot.data (PreviewFile result), slot.url (blob URL for images and PDFs),
// slot.err, slot.loading. Returns a function that cancels the update.
export function loadFileInto(slot, path) {
  let gone = false;
  if (slot.url) URL.revokeObjectURL(slot.url);
  Object.assign(slot, { path, loading: true, err: "", data: null, url: "" });
  update();
  backend.PreviewFile(path).then((d) => {
    if (gone || slot.path !== path) return;
    slot.data = d; slot.loading = false;
    if ((d.kind === "pdf" || d.kind === "image") && d.data) slot.url = b64ToBlobUrl(d.data, d.mime);
    update();
  }, (e) => { if (!gone && slot.path === path) { slot.err = errMsg(e); slot.loading = false; update(); } });
  return () => { gone = true; };
}

export const releaseFile = (slot) => { if (slot?.url) URL.revokeObjectURL(slot.url); };

const zoomKind = (d, md, mode) => (d?.kind === "image" ? "image" : d?.kind === "pdf" ? "pdf" : "text");

function step(cur, dir) {
  if (dir === 0) return 100;
  const i = STEPS.findIndex((s) => s >= cur);
  if (dir > 0) return STEPS[Math.min(STEPS.length - 1, STEPS[i] === cur ? i + 1 : Math.max(0, i))];
  return STEPS[Math.max(0, (i < 0 ? STEPS.length : i) - 1)];
}

export function FileView({ slot, line = 0, label = "File contents" }) {
  const box = useRef(null), img = useRef(null), [mode, setMode] = useState("rendered"), [, force] = useState(0);
  const d = slot?.data;
  const fmt = d?.kind === "text" ? (/\.(md|markdown|mdx)$/i.test(d.name) ? "md" : contentFormat(d.text, d.name)) : "";
  // code files are shown as code; data and documents can be previewed
  const md = d?.kind === "text" && canPreview(fmt) && (fmt === "md" || !/\.(jsx?|tsx?|go|java|kt|py|rb|rs|sh|sql|ya?ml|xml|toml|properties|gradle)$/i.test(d.name));
  const kind = zoomKind(d, md, mode);
  const z = zoomPrefs[kind];
  const fitPct = () => (img.current?.naturalWidth ? Math.round((img.current.clientWidth / img.current.naturalWidth) * 100) : 100);
  const setZoom = (v) => { zoomPrefs[kind] = v; saveZoom(); force((n) => n + 1); };
  const zoom = (dir) => {
    if (kind === "image" && dir === 0) return setZoom(100);
    const cur = z === "fit" ? fitPct() : z;
    setZoom(step(cur, dir));
  };
  // keyboard and pinch / ⌘-scroll while the pointer is over the viewer
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let acc = 0;
    const wheel = (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      acc += e.deltaY;
      if (Math.abs(acc) >= 40) { zoom(acc < 0 ? 1 : -1); acc = 0; }
    };
    const key = (e) => {
      if (!(e.metaKey || e.ctrlKey) || !el.matches(":hover, :focus-within")) return;
      if (e.key === "=" || e.key === "+") { e.preventDefault(); zoom(1); }
      else if (e.key === "-" || e.key === "_") { e.preventDefault(); zoom(-1); }
      else if (e.key === "0") { e.preventDefault(); zoom(0); }
    };
    el.addEventListener("wheel", wheel, { passive: false });
    window.addEventListener("keydown", key);
    return () => { el.removeEventListener("wheel", wheel); window.removeEventListener("keydown", key); };
  });
  useEffect(() => {
    if (line && box.current) box.current.querySelector(`[data-ln="${line}"]`)?.scrollIntoView({ block: "center" });
  }, [d, line, mode]);

  if (!slot) return null;
  if (slot.loading) return <div className="fp-center"><span className="spinner" /> Loading…</div>;
  if (slot.err) return <div className="banner error">{slot.err}</div>;
  if (!d) return null;
  const lines = d.kind === "text" && !(md && mode === "rendered") ? highlight(d.text, fmt === "json" && !/\.json$/i.test(d.name) ? "x.json" : d.name) : [];
  const zoomable = ["text", "image", "pdf", "dir"].includes(d.kind) && !(d.kind === "text" && !d.text);
  const pct = z === "fit" ? "Fit" : z + "%";
  return (
    <div className="fv" ref={box} tabIndex={-1}>
      <div className="fv-bar">
        {md && <div className="seg"><button className={"btn " + (mode === "rendered" ? "primary" : "secondary")} onClick={() => setMode("rendered")}>Preview</button><button className={"btn " + (mode === "source" ? "primary" : "secondary")} onClick={() => setMode("source")}>Raw data</button></div>}
        <span className="fv-meta muted">{d.kind === "text" ? `${lines.length || d.text.split("\n").length} lines · ` : d.kind === "dir" ? `${d.entries?.length || 0} entries · ` : ""}{d.kind !== "dir" ? fmtSize(d.size) : ""}{d.mime && d.kind !== "text" ? " · " + d.mime : ""}</span>
        {zoomable && <div className="fv-zoom" role="group" aria-label="Zoom">
          <button className="icon-btn" title="Zoom out (⌘ −)" aria-label="Zoom out" onClick={() => zoom(-1)}><Icon.minus /></button>
          <button className="fv-pct" title="Reset to 100% (⌘ 0)" onClick={() => zoom(0)}>{pct}</button>
          <button className="icon-btn" title="Zoom in (⌘ =)" aria-label="Zoom in" onClick={() => zoom(1)}><Icon.plus /></button>
          {kind === "image" && <button className={"btn " + (z === "fit" ? "primary" : "secondary")} title="Fit to the pane" onClick={() => setZoom("fit")}>Fit</button>}
        </div>}
      </div>
      {d.matches?.length > 0 && <div className="banner fp-matches">Showing the shortest match. Also found: {d.matches.map((m) => <FileLink key={m} path={m}>{m}</FileLink>).reduce((a, b) => [a, ", ", b])}</div>}
      {d.truncated && <div className="banner">{d.kind === "text" ? `Showing the first ${fmtSize(d.text.length)} of ${fmtSize(d.size)}.` : `This file is ${fmtSize(d.size)}, too large to preview.`}</div>}
      <div className="fv-content" style={{ "--fv-zoom": (kind === "text" ? z : 100) / 100 }}>
        {d.kind === "text" && md && mode === "rendered" && (fmt === "md" ? <div className="fp-md rich md" dangerouslySetInnerHTML={{ __html: mdRender(d.text) }} /> : <RenderPreview text={d.text} format={fmt} />)}
        {d.kind === "text" && !(md && mode === "rendered") && (d.text ? <pre className="code lines fp-code" aria-label={label}>{lines.map((l, k) => <span className={"ln" + (line === k + 1 ? " ln-hl" : "")} data-ln={k + 1} key={k}>{l.length ? l.map(([cls, t], j) => cls ? <span key={j} className={cls}>{t}</span> : t) : " "}</span>)}</pre> : <p className="muted fp-center">Empty file.</p>)}
        {d.kind === "image" && slot.url && <div className={"fp-image" + (z === "fit" ? " fit" : "")}><img ref={img} src={slot.url} alt={d.name} onLoad={() => force((n) => n + 1)}
          style={z === "fit" ? undefined : { width: img.current?.naturalWidth ? Math.round(img.current.naturalWidth * z / 100) + "px" : z + "%", maxWidth: "none", maxHeight: "none" }} /></div>}
        {d.kind === "pdf" && slot.url && <iframe key={slot.url + "#" + z} className="fp-pdf" src={`${slot.url}#zoom=${z}`} title={d.name} />}
        {d.kind === "binary" && <p className="muted fp-center">{d.name} is a binary file ({d.mime || "unknown type"}, {fmtSize(d.size)}) and can't be previewed.</p>}
        {d.kind === "dir" && <ul className="fp-dir">{d.entries.map((e) => <li key={e}>{e.endsWith("/") ? <><Icon.folder /> {e}</> : e.startsWith("…") ? <span className="muted">{e}</span> : <><Icon.file /> <FileLink path={(d.rel || d.path) + "/" + e}>{e}</FileLink></>}</li>)}</ul>}
      </div>
    </div>
  );
}
