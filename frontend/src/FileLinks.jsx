// File links everywhere: any file name or path with a known extension in
// text the app shows (agent answers, tool output, logs, descriptions)
// becomes a link that opens a quick preview of the file.
//
//  - <Linkify text="…"/> renders text with links (use it for dynamic text).
//  - installFileLinks() marks every <code> whose whole text is a path, so
//    static descriptions like <code>.codegen/tools/x.js</code> link too, and
//    handles clicks on any [data-file] element app-wide.
//  - openPreview(path, line) shows the preview panel (FilePreview.jsx).
import { S, update } from "./store.js";
import { findFileRefs, wholeFileRef } from "./filerefs.js";

export { findFileRefs, wholeFileRef, parseFileRef } from "./filerefs.js";

// Text with file references turned into preview links.
export function Linkify({ text }) {
  if (text == null) return null;
  const s = String(text),
    refs = findFileRefs(s);
  if (!refs.length) return s;
  const parts = [];
  let last = 0;
  refs.forEach((r, k) => {
    parts.push(s.slice(last, r.index));
    parts.push(
      <FileLink key={k} path={r.path} line={r.line}>
        {r.raw}
      </FileLink>,
    );
    last = r.index + r.length;
  });
  parts.push(s.slice(last));
  return <>{parts}</>;
}

export function FileLink({ path, line, children }) {
  return (
    <span
      className="file-link"
      role="link"
      tabIndex={0}
      data-file={path}
      data-line={line || undefined}
      title={`Preview ${path}${line ? ":" + line : ""}`}
    >
      {children ?? path}
    </span>
  );
}

// --- preview state ---------------------------------------------------------------

export function openPreview(path, line) {
  if (!path) return;
  if (S.preview?.url) URL.revokeObjectURL(S.preview.url);
  S.preview = {
    path,
    line: line || 0,
    history: [
      ...(S.preview?.history || []),
      ...(S.preview?.path ? [{ path: S.preview.path, line: S.preview.line }] : []),
    ].slice(-20),
  };
  update();
}

// --- app-wide wiring -------------------------------------------------------------

let installed = false;
export function installFileLinks() {
  if (installed || typeof document === "undefined") return;
  installed = true;
  const pick = (e) => e.target.closest?.("[data-file]");
  document.addEventListener(
    "click",
    (e) => {
      const el = pick(e);
      if (!el || e.defaultPrevented || el.closest("input, textarea, [contenteditable=true]")) return;
      e.preventDefault();
      e.stopPropagation();
      openPreview(el.dataset.file, +(el.dataset.line || 0));
    },
    true,
  );
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" && e.key !== " ") return;
    const el = pick(e);
    if (el) {
      e.preventDefault();
      openPreview(el.dataset.file, +(el.dataset.line || 0));
    }
  });
  // <code> elements whose whole text is a path become links too. Only a
  // data attribute is added, which React leaves alone on re-render.
  const mark = (root) => {
    for (const el of root.querySelectorAll
      ? root.querySelectorAll("code:not(pre code):not([data-file]):not([data-nofile])")
      : []) {
      if (
        el.closest(
          ".file-link, [contenteditable=true], button, a, summary, label, select, [role=tab], [role=button]",
        )
      )
        continue;
      const ref = wholeFileRef(el.textContent);
      if (ref) {
        el.dataset.file = ref.path;
        if (ref.line) el.dataset.line = ref.line;
        el.classList.add("file-code");
        el.title = "Preview " + ref.path;
      }
    }
  };
  // re-check codes whose text changed (React reuses the element)
  const recheck = (el) => {
    if (!el?.dataset?.file || !el.classList.contains("file-code")) return;
    const ref = wholeFileRef(el.textContent);
    if (!ref) {
      delete el.dataset.file;
      delete el.dataset.line;
      el.classList.remove("file-code");
      el.removeAttribute("title");
    } else {
      el.dataset.file = ref.path;
      if (ref.line) el.dataset.line = ref.line;
      else delete el.dataset.line;
    }
  };
  let queued = false;
  const obs = new MutationObserver((muts) => {
    for (const m of muts) if (m.type === "characterData") recheck(m.target.parentElement?.closest?.("code"));
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => {
      queued = false;
      mark(document.body);
    });
  });
  obs.observe(document.body, { childList: true, subtree: true, characterData: true });
  mark(document.body);
}
