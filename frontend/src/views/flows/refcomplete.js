// {{ … }} auto-complete in the flow designer's fields: the pure part
// (RefComplete.jsx shows the list).
import { paths } from "../../schema.js";

// refQuery: when the text before the caret ends in an open "{{" (no "}}"
// after it), where it starts and what has been typed after it; else null.
export function refQuery(text, caret) {
  const before = String(text).slice(0, caret);
  const i = before.lastIndexOf("{{");
  if (i < 0) return null;
  const typed = before.slice(i + 2);
  if (typed.includes("}}") || !/^[\w.[\]*\- ]*$/.test(typed) || typed.length > 80) return null;
  return { start: i, query: typed.trim() };
}

// jsonRefs: the fields of a step's JSON output as references
// (ref.json.a.b, ref.json[0].a for a list), from its known shape.
export function jsonRefs(ref, shape, label, max = 40) {
  if (!shape || (shape.type !== "object" && shape.type !== "array")) return [];
  const base = ref + ".json" + (shape.type === "array" ? "[0]" : "");
  const root = shape.type === "array" ? shape.items : shape;
  return paths(root, max).map((p) => [
    base + "." + p.path.replace(/\[\*\]/g, "[0]"),
    `${label} · ${p.path.replace(/\[\*\]/g, "[]")}`,
    false,
    p.type,
  ]);
}

// rankRefs: the references matching what was typed, best first: the
// reference starting with it, then a word of it, then anywhere (in the
// reference or its label).
export function rankRefs(list, query, max = 12) {
  const q = query.toLowerCase();
  if (!q) return list.slice(0, max);
  const score = ([ref, label]) => {
    const r = ref.toLowerCase(),
      l = String(label).toLowerCase();
    if (r.startsWith(q)) return 0;
    if (r.split(/[.[\]]/).some((w) => w.startsWith(q))) return 1;
    if (l.startsWith(q)) return 2;
    if (r.includes(q) || l.includes(q)) return 3;
    return 9;
  };
  // equal matches keep the list's order (a step's text before its file)
  return list
    .map((x, i) => [score(x), i, x])
    .filter(([s]) => s < 9)
    .sort((a, b) => a[0] - b[0] || a[1] - b[1])
    .slice(0, max)
    .map(([, , x]) => x);
}

// applyRef: the field's text with the open "{{…" replaced by the whole
// reference, and where the caret goes (after its "}}").
export function applyRef(text, start, caret, ref) {
  const after = text.slice(caret);
  const close = after.startsWith("}}") ? "" : "}}";
  const value = text.slice(0, start) + "{{" + ref + close + after;
  return { value, caret: start + 2 + ref.length + 2 };
}
