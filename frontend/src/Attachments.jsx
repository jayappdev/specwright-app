// Files attached to a question: pasted (images, PDFs, long text), dropped,
// or picked from disk. Each is saved in the workspace under
// .codegen/attachments/ by the backend so the agent can open it and pass its
// path to a tool. `list` is an array of { path, name, size, kind, url? }.
import { useRef, useState } from "react";
import { backend } from "./backend.js";
import { update, toast, errMsg } from "./store.js";
import { Icon } from "./ui.jsx";

const MAX = 25 << 20;
// Pasted text longer than this (or with several lines) becomes a .txt attachment.
const LONG_TEXT = 400;

const kindOf = (type, name) =>
  type.startsWith("image/")
    ? "image"
    : type === "application/pdf" || /\.pdf$/i.test(name)
      ? "pdf"
      : type.startsWith("text/") || /\.(txt|md|json|csv|log|xml|ya?ml|sql|html?)$/i.test(name)
        ? "text"
        : "file";

const readDataURL = (blob) =>
  new Promise((res, rej) => {
    const r = new FileReader();
    r.onload = () => res(r.result);
    r.onerror = () => rej(r.error);
    r.readAsDataURL(blob);
  });

export const fmtSize = (n) =>
  n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + " MB" : n >= 1024 ? Math.round(n / 1024) + " KB" : n + " B";

// Save one Blob/File; returns the attachment or null.
export async function attachBlob(list, blob, name) {
  name =
    name ||
    blob.name ||
    (blob.type.startsWith("image/")
      ? "pasted-image." + (blob.type.split("/")[1] || "png").replace("jpeg", "jpg")
      : "pasted");
  if (blob.size > MAX) {
    toast(`${name} is ${fmtSize(blob.size)}; attachments can be at most 25 MB.`);
    return null;
  }
  const pending = {
    path: "",
    name,
    size: blob.size,
    kind: kindOf(blob.type, name),
    url: blob.type.startsWith("image/") ? URL.createObjectURL(blob) : "",
    saving: true,
  };
  list.push(pending);
  update();
  try {
    const a = await backend.SaveAttachment(name, await readDataURL(blob));
    Object.assign(pending, a, { name, saving: false });
  } catch (e) {
    list.splice(list.indexOf(pending), 1);
    toast(`Could not attach ${name}: ${errMsg(e)}`);
  }
  update();
  return pending;
}

export const attachText = (list, text, name = "pasted-text.txt") =>
  attachBlob(list, new Blob([text], { type: "text/plain" }), name);

// Paste handler for a text field: files and images become attachments, long
// or multi-line text becomes a .txt attachment, short text pastes normally.
export function onPasteAttach(list) {
  return (e) => {
    const cd = e.clipboardData;
    if (!cd) return;
    const files = [...(cd.files || [])];
    if (files.length) {
      e.preventDefault();
      files.forEach((f) => attachBlob(list, f));
      return;
    }
    const text = cd.getData("text/plain");
    if (text && (text.length > LONG_TEXT || text.trim().includes("\n"))) {
      e.preventDefault();
      attachText(list, text).then((a) => a && toast(`Long text attached as ${a.name} (${fmtSize(a.size)}).`));
    }
  };
}

// Paste handler for a field whose text should stay text (a clipboard input):
// only files and images become attachments.
export function onPasteFiles(list, opts) {
  return (e) => {
    const files = [...(e.clipboardData?.files || [])];
    if (files.length) {
      e.preventDefault();
      addFiles(list, files, opts);
    }
  };
}

// Attach several files; with { single: true } the new file replaces the old one.
export function addFiles(list, files, { single } = {}) {
  files = [...files];
  if (single && files.length) {
    if (files.length > 1) toast("Only one file can be attached here; using the first.");
    list.splice(0, list.length);
    files = files.slice(0, 1);
  }
  files.forEach((f) => attachBlob(list, f));
}

// Read the system clipboard (button press). Falls back to asking for ⌘V.
// textInline: put any text in the field (insertText), however long.
export async function pasteClipboard(list, insertText, { textInline, single } = {}) {
  try {
    if (!navigator.clipboard?.read) throw new Error("unsupported");
    const items = await navigator.clipboard.read();
    let n = 0;
    for (const it of items) {
      const type =
        it.types.find((t) => t.startsWith("image/")) || it.types.find((t) => t === "application/pdf");
      if (type) {
        if (single) list.splice(0, list.length);
        await attachBlob(list, await it.getType(type));
        n++;
        continue;
      }
      if (it.types.includes("text/plain")) {
        const text = await (await it.getType("text/plain")).text();
        if (!text.trim()) continue;
        if (insertText && (textInline || !(text.length > LONG_TEXT || text.trim().includes("\n"))))
          insertText(text);
        else await attachText(list, text);
        n++;
      }
    }
    if (!n) toast("The clipboard is empty, or holds something that can't be attached.");
  } catch {
    toast("Click in the question box and press ⌘V (Ctrl+V) to paste an image, PDF or text.");
  }
}

function Thumb({ a }) {
  if (a.kind === "image" && a.url) return <img src={a.url} alt="" />;
  return (
    <span className={"att-icon att-" + a.kind}>
      {a.kind === "pdf" ? "PDF" : a.kind === "text" ? "TXT" : a.kind === "image" ? "IMG" : <Icon.file />}
    </span>
  );
}

// A list of attachments as chips (removable when onRemove is given).
export function AttachmentChips({ list, onRemove }) {
  if (!list?.length) return null;
  return (
    <div className="att-list">
      {list.map((a, k) => (
        <span
          key={a.path || k}
          className={"att-chip" + (a.saving ? " saving" : "")}
          title={a.path || "saving…"}
        >
          <Thumb a={a} />
          <span
            className="att-name"
            {...(a.path
              ? { "data-file": a.path, role: "link", tabIndex: 0, title: "Preview " + a.name }
              : {})}
          >
            {a.name}
          </span>
          <span className="att-size">{a.saving ? <span className="spinner" /> : fmtSize(a.size)}</span>
          {onRemove && !a.saving && (
            <button className="att-x" aria-label={"Remove " + a.name} onClick={() => onRemove(k)}>
              <Icon.x />
            </button>
          )}
        </span>
      ))}
    </div>
  );
}

// The toolbar: Attach file… (OS file picker), Paste from clipboard, and the
// chips. Wrap the area that should accept drops in <DropZone>.
export function AttachBar({ list, insertText, disabled, accept, single, textInline, hint }) {
  const input = useRef(null),
    opts = { single, textInline };
  return (
    <div className="att-bar">
      <div className="att-actions">
        <button
          type="button"
          className="btn secondary"
          disabled={disabled}
          onClick={() => input.current?.click()}
        >
          <Icon.clip /> {single ? (list.length ? "Replace file…" : "Choose file…") : "Attach file…"}
        </button>
        <button
          type="button"
          className="btn secondary"
          disabled={disabled}
          onClick={() => pasteClipboard(list, insertText, opts)}
        >
          <Icon.paste /> Paste from clipboard
        </button>
        <span className="muted att-hint">
          {hint || "or drop files here, or paste with ⌘V · images, PDFs, text · up to 25 MB each"}
        </span>
        <input
          ref={input}
          type="file"
          multiple={!single}
          accept={accept || undefined}
          hidden
          onChange={(e) => {
            addFiles(list, e.target.files, opts);
            e.target.value = "";
          }}
        />
      </div>
      <AttachmentChips
        list={list}
        onRemove={(k) => {
          const [a] = list.splice(k, 1);
          if (a?.url) URL.revokeObjectURL(a.url);
          update();
        }}
      />
    </div>
  );
}

export function DropZone({ list, children, className = "", single }) {
  const [over, setOver] = useState(false);
  return (
    <div
      className={className + (over ? " att-drop-over" : "")}
      onDragOver={(e) => {
        if ([...e.dataTransfer.types].includes("Files")) {
          e.preventDefault();
          setOver(true);
        }
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget)) setOver(false);
      }}
      onDrop={(e) => {
        if (!e.dataTransfer.files.length) return;
        e.preventDefault();
        setOver(false);
        addFiles(list, e.dataTransfer.files, { single });
      }}
    >
      {children}
    </div>
  );
}
