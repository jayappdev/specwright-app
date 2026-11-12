// Step names in flow templates: {{<step>.output_file}} / {{<step>.buffer_output}}
// where <step> is a step id, the tool it runs, or its title as a slug — when
// only one step has that name. Mirrors Flow.StepNames in internal/flow.
const RESERVED = new Set(["inputs", "steps", "input", "input_file", "output", "context", "skills", "flow", "date", "now"]);

export const slug = (s) => {
  let x = String(s || "").toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  if (x.length > 50) x = x.slice(0, 50).replace(/-+$/, "");
  return x || "flow";
};

// A built-in tool ("builtin:read_file") goes by its bare name.
export const toolRef = (t) => String(t || "").replace(/^builtin:/, "");
export const isBuiltin = (t) => String(t || "").startsWith("builtin:");

export function stepNames(flow) {
  const names = {}, ambiguous = new Set(), ids = new Set((flow?.nodes || []).map((n) => n.id));
  const add = (name, id) => {
    if (!name || RESERVED.has(name) || ids.has(name) || ambiguous.has(name)) return;
    if (name in names && names[name] !== id) { delete names[name]; ambiguous.add(name); return; }
    names[name] = id;
  };
  for (const n of flow?.nodes || []) {
    if (n.type === "tool") add(toolRef(n.tool), n.id);
    if ((n.title || "").trim()) add(slug(n.title), n.id);
  }
  return { names, ambiguous };
}

// The name to write for a step: its tool's name, else its title, else its id.
export function stepRef(flow, id) {
  const { names } = stepNames(flow), n = (flow?.nodes || []).find((x) => x.id === id);
  if (n?.type === "tool" && (names[toolRef(n.tool)] === id || n.id === toolRef(n.tool))) return toolRef(n.tool);
  if (n && (n.title || "").trim() && names[slug(n.title)] === id) return slug(n.title);
  return id;
}

export const isStepField = (rest) => rest === "output_file" || rest === "buffer_output" || String(rest).startsWith("buffer_output.json");
