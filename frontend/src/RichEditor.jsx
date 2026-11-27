// A small rich-text editor over Markdown: toolbar + contentEditable, with a
// Markdown source view. value/onChange are Markdown.
import { useEffect, useRef, useState } from "react";
import { askText } from "./Dialog.jsx";
import { mdToHtml, htmlToMd, sanitizeHtml } from "./richtext.js";
import { mdRender } from "./markdown.js";
import { Icon } from "./ui.jsx";

const B = (props) => (
  <button type="button" className="rte-btn" onMouseDown={(e) => e.preventDefault()} {...props} />
);

export function RichEditor({
  value,
  onChange,
  placeholder = "",
  minHeight = 140,
  autoFocus,
  label = "Rule text",
  big,
}) {
  const ref = useRef(null);
  const [mode, setMode] = useState("rich");
  const last = useRef(value);
  // load Markdown into the editor when it changes from outside
  useEffect(() => {
    if (mode !== "rich" || !ref.current) return;
    if (value !== last.current || !ref.current.innerHTML) {
      ref.current.innerHTML = mdToHtml(value || "");
      last.current = value;
    }
  }, [value, mode]);
  useEffect(() => {
    if (autoFocus) setTimeout(() => ref.current?.focus(), 30);
  }, [autoFocus]);

  const emit = () => {
    const md = htmlToMd(ref.current.innerHTML);
    last.current = md;
    onChange(md);
  };
  const cmd = (c, arg) => {
    ref.current.focus();
    document.execCommand(c, false, arg);
    emit();
  };
  const block = (tag) => cmd("formatBlock", tag);
  const link = async () => {
    // the dialog takes focus, so keep the selection and put it back afterwards
    const sel = window.getSelection(),
      range = sel?.rangeCount ? sel.getRangeAt(0).cloneRange() : null;
    const url = await askText("Add a link", { label: "Link address", value: "https://", ok: "Add link" });
    if (!url || url === "https://") return;
    ref.current.focus();
    if (range) {
      sel.removeAllRanges();
      sel.addRange(range);
    }
    cmd("createLink", url);
  };
  const code = () => {
    const sel = window.getSelection(),
      text = sel?.toString();
    if (text)
      cmd(
        "insertHTML",
        "<code>" +
          text.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c]) +
          "</code>&#8203;",
      );
  };
  const onKey = (e) => {
    const mod = e.metaKey || e.ctrlKey;
    if (mod && e.key.toLowerCase() === "b") {
      e.preventDefault();
      cmd("bold");
    } else if (mod && e.key.toLowerCase() === "i") {
      e.preventDefault();
      cmd("italic");
    } else if (mod && e.key.toLowerCase() === "k") {
      e.preventDefault();
      link();
    } else if (mod && e.key.toLowerCase() === "e") {
      e.preventDefault();
      code();
    }
  };
  const onPaste = (e) => {
    e.preventDefault();
    const html = e.clipboardData.getData("text/html");
    const text = e.clipboardData.getData("text/plain");
    document.execCommand("insertHTML", false, html ? sanitizeHtml(html) : mdToHtml(text));
    emit();
  };
  return (
    <div className={"rte" + (big ? " rte-big" : "")}>
      <div className="rte-bar" role="toolbar" aria-label="Formatting">
        {mode === "rich" ? (
          <>
            <B title="Bold (⌘B)" onClick={() => cmd("bold")}>
              <b>B</b>
            </B>
            <B title="Italic (⌘I)" onClick={() => cmd("italic")}>
              <i>I</i>
            </B>
            <B title="Inline code (⌘E)" onClick={code}>
              <code>{"</>"}</code>
            </B>
            <B title="Link (⌘K)" onClick={link}>
              <Icon.link />
            </B>
            <span className="rte-sep" />
            <B title="Heading" onClick={() => block("h3")}>
              H
            </B>
            <B title="Paragraph" onClick={() => block("p")}>
              ¶
            </B>
            <B title="Bulleted list" onClick={() => cmd("insertUnorderedList")}>
              • List
            </B>
            <B title="Numbered list" onClick={() => cmd("insertOrderedList")}>
              1. List
            </B>
            <B title="Quote" onClick={() => block("blockquote")}>
              <Icon.quote />
            </B>
            <B title="Code block" onClick={() => block("pre")}>
              {"{ }"}
            </B>
            <span className="rte-sep" />
            <B title="Undo" onClick={() => cmd("undo")}>
              ↶
            </B>
            <B title="Redo" onClick={() => cmd("redo")}>
              ↷
            </B>
          </>
        ) : (
          <span className="muted">Markdown source</span>
        )}
        <span className="status-spacer" />
        <div className="seg" role="group" aria-label="Editor view">
          <button type="button" aria-pressed={mode === "rich"} onClick={() => setMode("rich")}>
            Rich text
          </button>
          <button
            type="button"
            aria-pressed={mode === "md"}
            onClick={() => {
              if (mode === "rich" && ref.current) emit();
              setMode("md");
            }}
          >
            Markdown
          </button>
        </div>
      </div>
      {mode === "rich" ? (
        <div
          ref={ref}
          className="rte-body rich"
          contentEditable
          suppressContentEditableWarning
          role="textbox"
          aria-multiline="true"
          aria-label={label}
          data-placeholder={placeholder}
          style={{ minHeight }}
          onInput={emit}
          onKeyDown={onKey}
          onPaste={onPaste}
        />
      ) : (
        <textarea
          className="rte-body rte-md"
          aria-label={label + " (Markdown)"}
          spellCheck="false"
          style={{ minHeight }}
          value={value || ""}
          placeholder={placeholder}
          onChange={(e) => {
            last.current = null;
            onChange(e.target.value);
          }}
        />
      )}
    </div>
  );
}

// Read-only rendering of a rule's Markdown.
export function RichText({ md, className = "" }) {
  return <div className={"rich md " + className} dangerouslySetInnerHTML={{ __html: mdRender(md || "") }} />;
}
