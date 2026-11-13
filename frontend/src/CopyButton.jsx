// One copy button for every viewer: the clipboard icon copies the content in
// its most useful form, and the ▾ menu offers the others:
//   HTML      rich (pastes formatted into mail, docs, chat) + plain text
//   Markdown  rendered rich text + the Markdown as plain text
//   SVG       the picture (PNG) + its code as text
//   JSON/CSV  rows as a rich table (pastes into spreadsheets) + raw text
//   images    the image
//   other     text
import { useEffect, useRef, useState } from "react";
import { toast } from "./store.js";
import { mdRender } from "./markdown.js";

const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

export function kindOfContent(text, format, name = "") {
  const t = String(text || "").trim();
  if (/\.svg$/i.test(name) || /^(<\?xml[^>]*>\s*)?<svg[\s>]/i.test(t)) return "svg";
  if (format) return format;
  if (/^\s*(<!doctype|<html|<(div|p|h[1-6]|table|ul|ol|section|article|body)\b)/i.test(t)) return "html";
  if (/^[[{]/.test(t)) { try { JSON.parse(t); return "json"; } catch { /* not json */ } }
  return "txt";
}

// rows of JSON (an array of objects, or an object holding one) or CSV, as an HTML table
function tableHtml(text, kind) {
  let rows = null;
  if (kind === "json") {
    try { const v = JSON.parse(text); rows = Array.isArray(v) ? v : Object.values(v || {}).find(Array.isArray) || null; } catch { /* not rows */ }
    if (!rows?.length || !rows.every((r) => r && typeof r === "object" && !Array.isArray(r))) return "";
    const head = [...new Set(rows.slice(0, 500).flatMap(Object.keys))];
    return `<table><thead><tr>${head.map((h) => `<th>${esc(h)}</th>`).join("")}</tr></thead><tbody>${rows.map((r) => `<tr>${head.map((h) => `<td>${esc(typeof r[h] === "object" && r[h] !== null ? JSON.stringify(r[h]) : r[h] ?? "")}</td>`).join("")}</tr>`).join("")}</tbody></table>`;
  }
  if (kind === "csv") {
    const lines = String(text).trim().split(/\r?\n/).map((l) => l.split(",")); // simple CSV: enough for pasting
    if (lines.length < 2) return "";
    return `<table><thead><tr>${lines[0].map((h) => `<th>${esc(h)}</th>`).join("")}</tr></thead><tbody>${lines.slice(1).map((r) => `<tr>${r.map((c) => `<td>${esc(c)}</td>`).join("")}</tr>`).join("")}</tbody></table>`;
  }
  return "";
}

const htmlToText = (html) => { const d = new DOMParser().parseFromString(html, "text/html"); d.querySelectorAll("script,style").forEach((x) => x.remove()); return (d.body?.innerText || d.body?.textContent || "").trim(); };

async function pngFromSvg(svg) {
  const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
  try {
    const img = await new Promise((ok, bad) => { const i = new Image(); i.onload = () => ok(i); i.onerror = () => bad(new Error("the SVG could not be drawn")); i.src = url; });
    const scale = 2, w = img.naturalWidth || 800, h = img.naturalHeight || 600;
    const c = document.createElement("canvas"); c.width = w * scale; c.height = h * scale;
    const g = c.getContext("2d"); g.scale(scale, scale); g.drawImage(img, 0, 0, w, h);
    return await new Promise((ok) => c.toBlob(ok, "image/png"));
  } finally { URL.revokeObjectURL(url); }
}

async function pngFromImage(data, mime) {
  const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
  const blob = new Blob([bytes], { type: mime || "image/png" });
  if (blob.type === "image/png") return blob;
  const url = URL.createObjectURL(blob); // other formats: through a canvas, as PNG (what clipboards take)
  try {
    const img = await new Promise((ok, bad) => { const i = new Image(); i.onload = () => ok(i); i.onerror = bad; i.src = url; });
    const c = document.createElement("canvas"); c.width = img.naturalWidth; c.height = img.naturalHeight;
    c.getContext("2d").drawImage(img, 0, 0);
    return await new Promise((ok) => c.toBlob(ok, "image/png"));
  } finally { URL.revokeObjectURL(url); }
}

// copyEvent: the classic way, through a copy event — sets text and rich
// HTML together, and works where the async clipboard is refused (no focus,
// no permission, older webviews)
function copyEvent(parts) {
  let done = false;
  const on = (e) => { for (const [type, v] of Object.entries(parts)) if (typeof v === "string") e.clipboardData.setData(type, v); e.preventDefault(); done = true; };
  document.addEventListener("copy", on, { once: true });
  const ta = document.createElement("textarea"); // something must be selected for the copy command
  ta.value = parts["text/plain"] || " "; ta.style.cssText = "position:fixed;left:-9999px;top:0";
  document.body.appendChild(ta); ta.select();
  try { document.execCommand("copy"); } finally { ta.remove(); document.removeEventListener("copy", on); }
  return done;
}

async function writeText(text) {
  try { if (navigator.clipboard?.writeText) { await navigator.clipboard.writeText(text); return; } } catch { /* the other ways */ }
  if (copyEvent({ "text/plain": text })) return;
  if (window.runtime?.ClipboardSetText) return window.runtime.ClipboardSetText(text);
  throw new Error("the clipboard is not available");
}

// write several forms at once (rich + plain, image + text); text only where that is all the clipboard takes
async function writeItems(parts) {
  if (window.ClipboardItem && navigator.clipboard?.write) {
    try {
      const item = {};
      for (const [type, v] of Object.entries(parts)) item[type] = v instanceof Blob ? v : new Blob([v], { type });
      await navigator.clipboard.write([new ClipboardItem(item)]);
      return true;
    } catch (e) { if (parts["image/png"]) throw e; } // text and HTML: the copy event below
  }
  if (!parts["image/png"] && copyEvent(parts)) return true;
  if (parts["text/plain"] != null) { await writeText(parts["text/plain"]); return false; }
  throw new Error("this clipboard takes text only");
}

// the ways a content can be copied: [label, async () => message]
function options(text, kind, image) {
  const t = String(text ?? "");
  const opts = [];
  const rich = (html, plain, what) => async () => (await writeItems({ "text/html": html, "text/plain": plain })) ? `Copied ${what}` : "Copied as text";
  if (image?.data) opts.push(["Copy the image", async () => { await writeItems({ "image/png": await pngFromImage(image.data, image.mime) }); return "Copied the image"; }]);
  if (kind === "svg") {
    opts.push(["Copy the picture (PNG)", async () => { await writeItems({ "image/png": await pngFromSvg(t), "text/plain": t }); return "Copied the picture"; }]);
    opts.push(["Copy the SVG code", async () => { await writeText(t); return "Copied the SVG code"; }]);
  } else if (kind === "html") {
    opts.push(["Copy formatted (rich text)", rich(t, htmlToText(t), "formatted")]);
    opts.push(["Copy the HTML source", async () => { await writeText(t); return "Copied the HTML source"; }]);
    opts.push(["Copy the text only", async () => { await writeText(htmlToText(t)); return "Copied the text"; }]);
  } else if (kind === "md") {
    opts.push(["Copy formatted (rich text)", rich(mdRender(t), t, "formatted")]);
    opts.push(["Copy the Markdown", async () => { await writeText(t); return "Copied the Markdown"; }]);
    opts.push(["Copy as HTML source", async () => { await writeText(mdRender(t)); return "Copied the HTML"; }]);
  } else if (kind === "json" || kind === "csv") {
    const table = tableHtml(t, kind);
    if (table) opts.push(["Copy as a table (for spreadsheets)", rich(table, t, "as a table")]);
    opts.push([`Copy the ${kind.toUpperCase()}`, async () => { await writeText(t); return `Copied the ${kind.toUpperCase()}`; }]);
  }
  if (!opts.length || kind === "txt") opts.push(["Copy the text", async () => { await writeText(t); return "Copied"; }]);
  return opts;
}

// CopyButton: the icon copies the first (most useful) form; ▾ lists them all.
export function CopyButton({ text, format, name, image, label, className = "" }) {
  const [open, setOpen] = useState(false), ref = useRef(null);
  useEffect(() => {
    if (!open) return;
    const close = (e) => { if (!ref.current?.contains(e.target)) setOpen(false); };
    window.addEventListener("mousedown", close); return () => window.removeEventListener("mousedown", close);
  }, [open]);
  const kind = kindOfContent(text, format === "txt" ? "" : format, name);
  const opts = options(text, kind, image);
  if (!String(text ?? "").trim() && !image?.data) return null;
  const run = async (fn) => { setOpen(false); try { toast((await fn()) + "."); } catch (e) { toast("Copy failed: " + (e?.message || e)); } };
  return <span className={"copy-btn " + className} ref={ref}>
    <button type="button" className="icon-btn copy-main" title={opts[0][0]} aria-label={opts[0][0]} onClick={(e) => { e.preventDefault(); e.stopPropagation(); run(opts[0][1]); }}>
      <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true"><rect x="5" y="5" width="8.5" height="9" rx="1.5" /><path d="M10.5 5V3.5A1.5 1.5 0 009 2H3.5A1.5 1.5 0 002 3.5V10a1.5 1.5 0 001.5 1.5H5" /></svg>
      {label && <span>{label}</span>}</button>
    {opts.length > 1 && <button type="button" className="icon-btn copy-more" aria-label="More ways to copy" aria-expanded={open} title="More ways to copy" onClick={(e) => { e.preventDefault(); e.stopPropagation(); setOpen(!open); }}>▾</button>}
    {open && <div className="copy-menu" role="menu">{opts.map(([l, fn]) => <button key={l} type="button" role="menuitem" onClick={(e) => { e.preventDefault(); e.stopPropagation(); run(fn); }}>{l}</button>)}</div>}
  </span>;
}
