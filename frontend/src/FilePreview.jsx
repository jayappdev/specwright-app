// Quick preview: a side panel that shows any file the user clicks in the app.
// The body is the shared FileView (text, Markdown, images, PDFs, folders,
// with zoom). Esc or the × closes it; "Open in Files" jumps to Utils ›
// Project files, which shows the file the same way.
import { useEffect } from "react";
import { S, update, toast } from "./store.js";
import { Icon } from "./ui.jsx";
import { fmtSize } from "./Attachments.jsx";
import { Linkify, openPreview } from "./FileLinks.jsx";
import { FileView, loadFileInto, releaseFile } from "./FileView.jsx";

const close = () => { releaseFile(S.preview); S.preview = null; update(); };

function back() {
  const p = S.preview, prev = p?.history?.[p.history.length - 1];
  if (!prev) return;
  releaseFile(p);
  S.preview = { ...prev, history: p.history.slice(0, -1) };
  update();
}

export function FilePreview() {
  const p = S.preview;
  // load whenever the panel opens on a (new) path
  useEffect(() => { if (p && !p.data && !p.loading && !p.err) return loadFileInto(p, p.path); }, [p]);
  useEffect(() => {
    if (!p) return;
    const onKey = (e) => { if (e.key === "Escape" && !document.querySelector(".modal-backdrop")) close(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [!!p]);
  if (!p) return null;
  const d = p.data, inWs = d?.rel && S.ws;
  const openInFiles = () => {
    S.files.sel = d.rel; S.files.line = p.line || 0;
    const parts = d.rel.split("/");
    for (let k = 1; k < parts.length; k++) S.files.open[parts.slice(0, k).join("/")] = true;
    S.view = "files"; close();
  };
  return (
    <aside className="file-preview" role="dialog" aria-label={"Preview of " + p.path}>
      <header className="fp-head">
        {p.history?.length > 0 && <button className="icon-btn" title="Back" aria-label="Back" onClick={back}>‹</button>}
        <Icon.file />
        <div className="fp-title"><b title={d?.path || p.path}>{d?.name || p.path.split("/").pop()}</b>
          <small>{p.inline ? "result shown from memory" : d ? <>{d.rel || d.path}{p.line ? ":" + p.line : ""}{d.mod ? " · " + new Date(d.mod).toLocaleString() : ""}</> : p.path}</small></div>
        {d && !p.inline && <button className="btn secondary" title="Copy the full path" onClick={() => navigator.clipboard?.writeText(d.path).then(() => toast("Path copied."), () => {})}>Copy path</button>}
        {inWs && d.kind !== "dir" && <button className="btn secondary" onClick={openInFiles}>Open in Files</button>}
        <button className="icon-btn" aria-label="Close preview" title="Close (Esc)" onClick={close}><Icon.x /></button>
      </header>
      <div className="fp-body"><FileView slot={p} line={p.line} label={"Preview of " + p.path} /></div>
    </aside>
  );
}

export { Linkify, openPreview, fmtSize };
