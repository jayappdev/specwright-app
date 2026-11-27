// RefComplete: in the flow designer's fields, typing "{{" opens a list of
// what the step can use (the flow's inputs, earlier steps' outputs and their
// JSON fields, loop items, variables, stored secrets); it narrows as you type,
// and Enter or Tab puts the whole reference in, closed with "}}".
// It works on every text field of the Inspector, without changing them: it
// listens to the document while the designer is open.
import { useEffect, useRef, useState } from "react";
import { S } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { stepRef } from "../../flowNames.js";
import { ancestors, fl, nodeOf } from "../Flows.jsx";
import { varsFor } from "./Fields.jsx";
import { refQuery, jsonRefs, rankRefs, applyRef } from "./refcomplete.js";

const AREA = ".fl-inspector";

// every reference the selected step can use
function allRefs() {
  const f = fl(),
    flow = f.flow;
  if (!flow) return [];
  const node = f.sel ? flow.nodes.find((n) => n.id === f.sel) : null;
  const list = varsFor(flow, node);
  // the JSON fields of earlier steps whose output shape is known
  const anc = node ? [...ancestors(flow, node.id)].filter((id) => id !== "start") : [];
  for (const id of anc) {
    const sh = f.shapes?.[id]?.out;
    const l = nodeOf(id)?.title || nodeOf(id)?.tool || id;
    list.push(...jsonRefs(stepRef(flow, id) + ".buffer_output", sh, l));
  }
  if (node?.type !== "ai")
    for (const s of S.projectSecrets || [])
      list.push(["project_secret." + s.name, s.name + " · project secret", true]);
  const seen = new Set();
  return list.filter(([r]) => (seen.has(r) ? false : seen.add(r)));
}

// set a field's value so React sees it (as insertText does)
function setValue(el, value, caret) {
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.focus();
  el.setSelectionRange(caret, caret);
}

const fieldOf = (t) =>
  t &&
  (t.tagName === "TEXTAREA" || (t.tagName === "INPUT" && /^(text|search|)$/.test(t.type))) &&
  t.closest(AREA)
    ? t
    : null;

export function RefComplete() {
  const [st, setSt] = useState(null); // { el, start, query, items, i, rect }
  const ref = useRef(null);
  ref.current = st;
  useEffect(() => {
    const look = (el) => {
      const q = refQuery(el.value, el.selectionStart ?? el.value.length);
      if (!q) return setSt(null);
      const items = rankRefs(allRefs(), q.query);
      if (!items.length) return setSt(null);
      const cur = ref.current;
      setSt({
        el,
        start: q.start,
        query: q.query,
        items,
        i: cur && cur.el === el && cur.query === q.query ? Math.min(cur.i, items.length - 1) : 0,
        rect: el.getBoundingClientRect(),
      });
    };
    const onInput = (e) => {
      const el = fieldOf(e.target);
      if (el) look(el);
    };
    const pick = (x, k) => {
      const caret = x.el.selectionStart ?? x.el.value.length;
      const r = applyRef(x.el.value, x.start, caret, x.items[k][0]);
      setSt(null);
      setValue(x.el, r.value, r.caret);
    };
    const onKey = (e) => {
      const x = ref.current;
      if (!x || e.target !== x.el) return;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        e.stopPropagation();
        const n = x.items.length;
        setSt({ ...x, i: (x.i + (e.key === "ArrowDown" ? 1 : n - 1)) % n });
      } else if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        e.stopPropagation();
        pick(x, x.i);
      } else if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setSt(null);
      }
    };
    // moving the caret with the mouse or the arrows (left/right) re-reads it
    const onCaret = (e) => {
      const el = fieldOf(e.target);
      if (el && (ref.current || e.type === "click")) look(el);
    };
    const onBlur = (e) => {
      if (ref.current?.el === e.target) setTimeout(() => setSt((x) => (x?.el === e.target ? null : x)), 120);
    };
    document.addEventListener("input", onInput, true);
    document.addEventListener("keydown", onKey, true);
    const onKeyUp = (e) => ["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key) && onCaret(e);
    document.addEventListener("keyup", onKeyUp, true);
    document.addEventListener("click", onCaret, true);
    document.addEventListener("focusout", onBlur, true);
    RefComplete.pick = pick;
    return () => {
      document.removeEventListener("input", onInput, true);
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("keyup", onKeyUp, true);
      document.removeEventListener("click", onCaret, true);
      document.removeEventListener("focusout", onBlur, true);
    };
  }, []);
  if (!st) return null;
  const { rect } = st;
  const below = window.innerHeight - rect.bottom > 260;
  const style = {
    left: Math.max(8, Math.min(rect.left, window.innerWidth - 440)),
    width: Math.min(Math.max(rect.width, 300), 432),
    ...(below ? { top: rect.bottom + 4 } : { bottom: window.innerHeight - rect.top + 4 }),
  };
  return (
    <div className="ref-complete" style={style} role="listbox" aria-label="References">
      {st.items.map(([r, label, secret, type], k) => (
        <button
          key={r}
          type="button"
          role="option"
          aria-selected={k === st.i}
          className={"ref-opt" + (k === st.i ? " on" : "")}
          onMouseDown={(e) => {
            e.preventDefault(); // keep the field's focus and caret
            RefComplete.pick(st, k);
          }}
        >
          <code>{`{{${r}}}`}</code>
          <span className="muted">
            {secret && <Icon.lock />}
            {label}
            {type ? " · " + type : ""}
          </span>
        </button>
      ))}
      <div className="ref-hint muted">
        <kbd>↑</kbd>
        <kbd>↓</kbd> choose · <kbd>Enter</kbd> or <kbd>Tab</kbd> insert · <kbd>Esc</kbd> close
      </div>
    </div>
  );
}
