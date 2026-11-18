// Tools: data shapes — the output shape card and the JSON shape editor.
import { useEffect, useState } from "react";
import { update, toast } from "../../store.js";
import { parseSchema, schemaOfText, compact, paths as schemaPaths } from "../../schema.js";

// --- data shapes: what a tool prints, and the shape of JSON parameters ---------------

export const JSON_TYPES = /^(json|object|array)$/i;

// Example JSON or a JSON Schema in; the shape (and its fields) shown.
export function ShapeEditor({ label, hint, value, onChange }) {
  const [text, setText] = useState(value ? JSON.stringify(value, null, 2) : "");
  const [err, setErr] = useState("");
  useEffect(() => {
    setText(value ? JSON.stringify(value, null, 2) : "");
  }, [JSON.stringify(value || null)]);
  const apply = () => {
    try {
      const s = parseSchema(text);
      setErr("");
      onChange(s || undefined);
    } catch (e) {
      setErr(e.message);
    }
  };
  return (
    <div className="shape-editor">
      <label className="field">
        <span className="muted">{label}</span>
        <textarea
          rows={value ? 6 : 3}
          spellCheck="false"
          style={{ fontFamily: "var(--font-mono)" }}
          value={text}
          placeholder={'{"id": "A-1001", "total": 120.5, "items": [{"sku": "KB-01", "qty": 2}]}'}
          onChange={(e) => setText(e.target.value)}
          onBlur={apply}
        />
        {hint && <small className="muted">{hint}</small>}
      </label>
      {err && <div className="banner error">{err}</div>}
      {value && <code className="fl-compact">{compact(value)}</code>}
    </div>
  );
}

export function OutputShapeCard({ d, test }) {
  const out = !test?.running ? test?.output || d.lastTest?.output || "" : "";
  const learned = out.trim() && /^[[{]/.test(out.trim()) ? schemaOfText(out) : null;
  const fieldsOf = d.output ? schemaPaths(d.output, 200) : [];
  return (
    <div className="card">
      <h3>Output shape</h3>
      <p className="wf-desc">
        When the tool prints JSON, describe its shape: the Flow designer then shows its fields to the steps
        after it, suggests them in filters, decisions and aggregates, and checks the paths flows use. It is
        metadata for the designer only and is never sent to the AI.
      </p>
      <ShapeEditor
        label="What the tool prints (example JSON or a JSON Schema)"
        value={d.output}
        onChange={(v) => {
          d.output = v;
          update();
        }}
      />
      {learned && (
        <div className="row-actions">
          <button
            className="btn secondary"
            onClick={() => {
              d.output = learned;
              update();
              toast(
                "Learned the output shape from the last test. Add descriptions to the fields if you like.",
              );
            }}
          >
            {d.output ? "Learn it again from the last test" : "Learn it from the last test"}
          </button>
          <span className="muted">{compact(learned)}</span>
        </div>
      )}
      {fieldsOf.length > 0 && (
        <details>
          <summary className="muted">
            {fieldsOf.length} field{fieldsOf.length === 1 ? "" : "s"}
          </summary>
          <ul className="shape-fields">
            {fieldsOf.map((p) => (
              <li key={p.path} style={{ paddingLeft: p.depth * 14 }}>
                <code>{p.path}</code> <span className="fl-shape-type">{p.type}</span>
                {p.description && <span className="muted"> · {p.description}</span>}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}
