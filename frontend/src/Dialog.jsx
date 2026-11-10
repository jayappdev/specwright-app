// In-app dialogs. The desktop app's web view does not show the browser's
// prompt() / confirm() boxes (they return nothing), so the app asks with
// its own dialog instead:
//   if (await confirmDialog("Delete x?", { ok: "Delete", danger: true })) …
//   const name = await askText("Name of the new flow", { value: "New flow" });  // null = cancelled
import { useEffect, useRef } from "react";
import { S, update } from "./store.js";

let resolver = null;
function open(d) {
  if (resolver) resolver(d.input ? null : false); // replace an open dialog
  return new Promise((res) => { resolver = res; S.dialog = d; update(); });
}
function finish(v) {
  const r = resolver; resolver = null; S.dialog = null; update();
  r?.(v);
}

export const confirmDialog = (message, o = {}) => open({ title: o.title || "Are you sure?", message, ok: o.ok || "OK", danger: !!o.danger });
export const askText = (title, o = {}) => open({ title, message: o.message, ok: o.ok || "OK", input: { value: o.value ?? "", placeholder: o.placeholder || "", label: o.label || "" } });

export function DialogHost() {
  const d = S.dialog, field = useRef(null), okBtn = useRef(null);
  useEffect(() => {
    if (!d) return;
    setTimeout(() => { if (field.current) { field.current.focus(); field.current.select(); } else okBtn.current?.focus(); }, 20);
    const key = (e) => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); finish(d.input ? null : false); } };
    window.addEventListener("keydown", key, true);
    return () => window.removeEventListener("keydown", key, true);
  }, [d]);
  if (!d) return null;
  const ok = () => finish(d.input ? field.current.value : true);
  return (
    <div className="modal-backdrop dlg-backdrop" onMouseDown={(e) => e.target === e.currentTarget && finish(d.input ? null : false)}>
      <form className="dlg" role="dialog" aria-modal="true" aria-labelledby="dlg-title" onSubmit={(e) => { e.preventDefault(); ok(); }}>
        <h3 id="dlg-title">{d.title}</h3>
        {d.message && <p className="dlg-msg">{d.message}</p>}
        {d.input && <label className="field">{d.input.label && <span className="muted">{d.input.label}</span>}
          <input ref={field} defaultValue={d.input.value} placeholder={d.input.placeholder} /></label>}
        <div className="row-actions dlg-actions">
          <button type="button" className="btn secondary" onClick={() => finish(d.input ? null : false)}>Cancel</button>
          <button ref={okBtn} type="submit" className={"btn primary" + (d.danger ? " danger" : "")}>{d.ok}</button>
        </div>
      </form>
    </div>
  );
}
