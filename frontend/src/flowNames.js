// Steps are referred to by id in flow templates: {{<step id>.output_file}},
// {{<step id>.buffer_output}}, {{<loop id>.item}}. stepNames (mirrors
// Flow.StepNames in internal/flow) maps the other names a step goes by — its
// tool, its title as a slug — to its id, for moving older flows over to ids
// and for hints.
const RESERVED = new Set(["inputs", "steps", "input", "input_file", "output", "context", "skills", "flow", "date", "now", "item", "item_index", "item_count", "this", "this_index"]);

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

// The name to write for a step in a template: its id.
export const stepRef = (_flow, id) => id;

// Moves references by title or tool name ({{find-todos.output_file}}) over
// to step ids, as loading a flow does (Flow.useStepIDs).
export function useStepIDs(flow) {
  const { names } = stepNames(flow), ids = new Set((flow?.nodes || []).map((n) => n.id));
  const fix = (s) => typeof s !== "string" || !s.includes("{{") ? s : s.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (m, v) => {
    const i = v.indexOf("."), h = i < 0 ? v : v.slice(0, i), r = i < 0 ? "" : v.slice(i + 1);
    return names[h] && !ids.has(h) && (isStepField(r) || isLoopField(r)) ? `{{${names[h]}.${r}}}` : m; });
  for (const n of flow?.nodes || []) {
    for (const k of ["prompt", "template", "source", "expr", "instruction"]) if (n[k]) n[k] = fix(n[k]);
    for (const k in n.args || {}) n.args[k] = fix(n.args[k]);
    for (const a of n.actions || []) { a.path = fix(a.path); for (const k in a.args || {}) a.args[k] = fix(a.args[k]); }
  }
  return flow;
}

// {{<loop>.item}}, {{<loop>.item.json.x}}, {{<loop>.item_index}}, {{<loop>.item_count}}
export const isLoopField = (rest) => rest === "item" || rest === "item_index" || rest === "item_count" || String(rest).startsWith("item.json");

// A title no other step has yet ("Loop", "Loop 2", …), so steps are easy to
// tell apart on the canvas (templates use the step id, not the title).
export function uniqueTitle(flow, base, self) {
  const taken = new Set();
  for (const n of flow?.nodes || []) if (n.id !== self) { taken.add(n.id); if ((n.title || "").trim()) taken.add(slug(n.title)); }
  let t = base || "Step", k = 2;
  while (taken.has(slug(t))) t = `${base} ${k++}`;
  return t;
}

export const isStepField = (rest) => rest === "output_file" || rest === "buffer_output" || String(rest).startsWith("buffer_output.json");

// Loops (mirrors Flow.Loops in internal/flow): a Loop opens a region and the
// next Gather closes it, like brackets along the connections. stack[id] is
// the loops a step runs inside (outermost first); gather[loop] its Gather.
export function loopInfo(flow) {
  const nodes = new Map((flow?.nodes || []).map((n) => [n.id, n])), memo = {};
  const parents = (id) => (flow?.edges || []).filter((e) => e.to === id).map((e) => e.from);
  const into = (p) => { const s = stackOf(p); return nodes.get(p)?.type === "loop" ? [...s, p] : s; };
  function stackOf(id, seen = new Set()) {
    if (id in memo) return memo[id];
    if (seen.has(id)) return [];
    seen.add(id);
    const ps = parents(id);
    let s = ps.length ? into(ps[0]) : [];
    if (nodes.get(id)?.type === "gather" && s.length) s = s.slice(0, -1);
    return (memo[id] = s);
  }
  const stack = {}, gather = {};
  for (const id of nodes.keys()) stack[id] = stackOf(id);
  for (const [id, n] of nodes) if (n.type === "gather") { const ps = parents(id); const s = ps.length ? into(ps[0]) : []; if (s.length && !gather[s[s.length - 1]]) gather[s[s.length - 1]] = id; }
  return { stack, gather };
}
