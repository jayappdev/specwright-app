// The Spec Kit project picker: every project of the workspace (each with its
// own workflow progress), plus "New project…".
import { S, switchProject } from "./store.js";

export function ProjectPicker({ value, onNew, onPick, disabled, allowNew = true }) {
  const projects = S.ws?.projects || [];
  return (
    <select
      className="project-picker"
      aria-label="Spec Kit project"
      value={value}
      disabled={disabled}
      onChange={(e) => {
        const v = e.target.value;
        if (v === "+new") {
          onNew?.();
          return;
        }
        onPick?.(v);
        if (v !== (S.ws?.speckit?.project || "")) switchProject(v);
      }}
    >
      {projects.map((p) => (
        <option key={p.name} value={p.name}>
          {p.label}
          {p.steps ? ` · ${p.done}/${p.steps} done${p.next ? ", at " + p.next : ""}` : ""}
        </option>
      ))}
      {allowNew && <option value="+new">+ New project…</option>}
    </select>
  );
}
