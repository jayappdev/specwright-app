// Data shapes (a subset of JSON Schema): learning them from example JSON,
// listing their fields, looking up paths and writing them compactly.
// Mirrors internal/flow/schema.go; shapes() mirrors flow.Shapes for the
// browser preview's mock backend.

const isObj = (x) => x && typeof x === "object" && !Array.isArray(x);
const DATE = /^\d{4}-\d{2}-\d{2}$/,
  DATETIME = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}/;
const SCHEMA_KEYS = new Set([
  "type",
  "description",
  "properties",
  "required",
  "items",
  "enum",
  "format",
  "$schema",
  "title",
  "additionalProperties",
]);
const prop = (s, name) => s?.properties?.[name];
const known = (s) => !!s && !!s.type;

export function inferSchema(v) {
  if (v === null || v === undefined) return { type: "null" };
  if (typeof v === "boolean") return { type: "boolean" };
  if (typeof v === "number") return { type: Number.isInteger(v) ? "integer" : "number" };
  if (typeof v === "string")
    return {
      type: "string",
      ...(DATE.test(v) ? { format: "date" } : DATETIME.test(v) ? { format: "date-time" } : {}),
    };
  if (Array.isArray(v)) {
    let items;
    for (const e of v.slice(0, 200)) items = merge(items, inferSchema(e));
    return items ? { type: "array", items } : { type: "array" };
  }
  const properties = {},
    required = [];
  for (const k of Object.keys(v)) {
    properties[k] = inferSchema(v[k]);
    required.push(k);
  }
  return { type: "object", properties, required };
}

function merge(a, b) {
  if (!a) return b;
  if (!b) return a;
  if (a.type === "null") return b;
  if (b.type === "null") return a;
  if (a.type !== b.type)
    return (a.type === "integer" && b.type === "number") || (a.type === "number" && b.type === "integer")
      ? { type: "number" }
      : {};
  const out = { type: a.type };
  if (a.description || b.description) out.description = a.description || b.description;
  if (a.format && a.format === b.format) out.format = a.format;
  if (a.type === "array") {
    const it = merge(a.items, b.items);
    if (it) out.items = it;
  }
  if (a.type === "object") {
    out.properties = {};
    for (const k of Object.keys(a.properties || {})) out.properties[k] = merge(a.properties[k], prop(b, k));
    for (const k of Object.keys(b.properties || {}))
      if (!(k in out.properties)) out.properties[k] = b.properties[k];
    out.required = (a.required || []).filter((r) => (b.required || []).includes(r));
  }
  return out;
}

export function schemaOfText(s) {
  const t = String(s ?? "").trim();
  if (t && (t[0] === "{" || t[0] === "[")) {
    try {
      return inferSchema(JSON.parse(t));
    } catch {
      /* text */
    }
  }
  return { type: "string" };
}

// parseSchema: a JSON Schema as it is, or the shape of example JSON
export function parseSchema(text) {
  const t = String(text || "").trim();
  if (!t) return null;
  let v;
  try {
    v = JSON.parse(t);
  } catch (e) {
    throw new Error("not JSON: " + e.message);
  }
  if (
    isObj(v) &&
    ["object", "array", "string", "number", "integer", "boolean", "null"].includes(v.type) &&
    Object.keys(v).every((k) => SCHEMA_KEYS.has(k))
  )
    return v;
  return inferSchema(v);
}

// lookup: the shape at a path; ok false only when the path cannot exist
export function lookup(s, path) {
  path = String(path || "")
    .trim()
    .replace(/^\$/, "")
    .replace(/^\./, "");
  if (!s) return { ok: true, s: null };
  if (!path) return { ok: true, s };
  let cur = s,
    wild = false;
  for (const m of path.matchAll(/\[(\d+|\*)\]|\["([^"]+)"\]|([^.[\]]+)/g)) {
    if (!known(cur)) return { ok: true, s: {} };
    if (m[1] != null) {
      if (cur.type === "array") {
        if (m[1] === "*") wild = true;
        cur = cur.items;
      } else if (cur.type === "object" && m[1] === "*") {
        wild = true;
        cur = null;
      } else return { ok: false };
    } else {
      const k = m[2] ?? m[3];
      if (cur.type === "array") {
        wild = true;
        cur = cur.items;
        if (!known(cur)) return { ok: true, s: {} };
      }
      if (cur.type !== "object") return { ok: false };
      const p = prop(cur, k);
      if (!p) return Object.keys(cur.properties || {}).length ? { ok: false } : { ok: true, s: {} };
      cur = p;
    }
  }
  return { ok: true, s: wild ? { type: "array", items: cur || {} } : cur || {} };
}

export function typeName(s) {
  if (!s || !s.type) return "any";
  if (s.type === "array") return typeName(s.items) + "[]";
  if (s.type === "string" && s.format) return s.format;
  return s.type;
}

// paths: the fields of a shape (customer.name, items[*].qty, …)
export function paths(s, max = 300) {
  const out = [];
  const walk = (s, prefix, depth) => {
    if (!s || depth > 6 || out.length >= max) return;
    if (s.type === "object")
      for (const [k, p] of Object.entries(s.properties || {})) {
        if (out.length >= max) return;
        const path = prefix ? prefix + "." + k : k;
        out.push({ path, type: typeName(p), description: p?.description || "", depth });
        walk(p, path, depth + 1);
      }
    else if (s.type === "array" && (s.items?.type === "object" || s.items?.type === "array"))
      walk(s.items, prefix + "[*]", depth);
  };
  walk(s, "", 0);
  return out;
}

export const fields = (s) => Object.keys((s?.type === "array" ? s.items : s)?.properties || {});

export function compact(s, depth = 0) {
  if (!s || !s.type) return "any";
  if (depth > 8) return "…";
  if (s.type === "object") {
    const ks = Object.keys(s.properties || {});
    if (!ks.length) return "object";
    const req = s.required || [];
    return (
      "{" +
      ks
        .map((k) => {
          const p = s.properties[k];
          return (
            k +
            (req.length && !req.includes(k) ? "?" : "") +
            ": " +
            compact(p, depth + 1) +
            (p?.description && p.description.length <= 60 ? ` /* ${p.description} */` : "")
          );
        })
        .join(", ") +
      "}"
    );
  }
  if (s.type === "array") return compact(s.items, depth + 1) + "[]";
  if (s.enum?.length) return s.enum.map((e) => JSON.stringify(e)).join(" | ");
  return typeName(s);
}

// toolOutput: the shape a tool step gives — the one its arguments select
// (fetch_webpage with format=json), else the tool's declared one.
export function toolOutput(out, by, args = {}) {
  for (const [p, m] of Object.entries(by || {})) {
    const s = m?.[String(args?.[p] ?? "").trim()];
    if (s) return s;
  }
  return out || null;
}

// --- shapes of a flow's steps (mirrors flow.Shapes) -----------------------------

const WHOLE = /^\{\{\s*([^{}]+?)\s*\}\}$/;
const measureName = (m) =>
  (m.as || "").trim() ||
  (m.field
    ? m.fn + "_" + m.field.replace(/\./g, "_").replace(/\[/g, "_").replace(/\]/g, "").replace(/\*/g, "all")
    : m.fn);

function templateShape(tpl, out) {
  const t = String(tpl || "").trim(),
    m = t.match(WHOLE);
  if (m) {
    const v = m[1].trim(),
      i = v.indexOf("."),
      head = i < 0 ? v : v.slice(0, i),
      rest = i < 0 ? "" : v.slice(i + 1);
    if (head === "steps") {
      const [id, ...p] = rest.split("."),
        path = p.join(".");
      if (out[id]) {
        if (path.startsWith("json")) return lookup(out[id].out, path.slice(4).replace(/^\./, "")).s;
        if (!path) return out[id].out;
      }
    } else if (rest === "buffer_output" && out[head]) return out[head].out;
    else if (rest.startsWith("buffer_output.json") && out[head])
      return lookup(out[head].out, rest.slice(18).replace(/^\./, "")).s;
    else if (rest === "item" && out[head]) return out[head].item;
  }
  return t ? { type: "string" } : null;
}

function rowShape(inp, format) {
  if (format === "lines") return { type: "string" };
  if (!inp) return null;
  if (inp.type === "array") return inp.items || null;
  if (inp.type === "object") {
    const lists = Object.values(inp.properties || {}).filter((p) => p?.type === "array");
    return lists.length === 1 ? lists[0].items : inp;
  }
  if (inp.type === "string" && format !== "json" && format !== "csv") return { type: "string" };
  return null;
}

function aggRow(n, row) {
  const o = { type: "object", properties: {} },
    field = (f) => (row ? lookup(row, f).s || {} : {}),
    elem = (s) => (s?.type === "array" ? s.items || {} : s);
  if (n.mode === "window") {
    if (row?.type === "object") Object.assign(o.properties, row.properties);
    else o.properties.value = row || {};
  } else for (const g of n.groupBy || []) o.properties[g] = field(g);
  for (const m of n.measures || []) {
    const t = [
      "count",
      "count_distinct",
      "row_number",
      "rank",
      "dense_rank",
      "running_count",
      "ntile",
    ].includes(m.fn)
      ? { type: "integer" }
      : ["sum", "avg", "median", "running_sum", "running_avg", "pct_of_total"].includes(m.fn)
        ? { type: "number" }
        : ["min", "max", "first", "last", "lag", "lead"].includes(m.fn)
          ? elem(field(m.field))
          : ["list", "distinct"].includes(m.fn)
            ? { type: "array", items: elem(field(m.field)) }
            : m.fn === "concat"
              ? { type: "string" }
              : {};
    o.properties[measureName(m)] = t;
  }
  return o;
}

function tplShape(t, sc) {
  if (typeof t === "string") {
    const m = t.match(WHOLE);
    return m ? scValue(sc, m[1]) : { type: "string" };
  }
  if (Array.isArray(t)) {
    let items;
    for (const e of t) {
      const b = eachOf(e);
      items = merge(items, b ? eachShape(sc, b[0], b[1]) : tplShape(e, sc));
    }
    return { type: "array", ...(items ? { items } : {}) };
  }
  if (isObj(t)) {
    const b = eachOf(t);
    if (b) return { type: "array", items: eachShape(sc, b[0], b[1]) };
    const o = { type: "object", properties: {} };
    for (const k of Object.keys(t)) {
      if (k.includes("{{")) return { type: "object" };
      o.properties[k] = tplShape(t[k], sc);
    }
    return o;
  }
  return inferSchema(t);
}
const eachOf = (t) => {
  if (!isObj(t)) return null;
  const ks = Object.keys(t),
    m = ks.length === 1 && ks[0].match(/^\{\{\s*#each\s+([^{}]+?)\s*\}\}$/);
  return m ? [m[1], t[ks[0]]] : null;
};
function scValue(sc, v) {
  v = v.trim();
  let up = false;
  for (;;) {
    if (v === "parent") return sc.parent ? sc.parent.this || {} : {};
    if (!v.startsWith("parent.")) break;
    if (!sc.parent) return {};
    sc = sc.parent;
    v = v.slice(7);
    up = true;
  }
  let [h, ...r] = v.split(".");
  if (up && !["this", "root", "this_index"].includes(h)) {
    r = [h, ...r];
    h = "this";
  }
  if (h === "this" || h === "root") {
    const base = h === "root" ? sc.root : sc.this;
    return base ? lookup(base, r.join(".")).s || {} : {};
  }
  return h === "this_index" ? { type: "integer" } : {};
}
const eachShape = (sc, path, body) => {
  const l = scValue(sc, path);
  return tplShape(body, { this: l?.type === "array" ? l.items : null, root: sc.root, parent: sc });
};

function quoteBare(tpl) {
  let out = "",
    s = false;
  for (let i = 0; i < tpl.length; ) {
    const c = tpl[i];
    if (s && c === "\\") {
      out += tpl.slice(i, i + 2);
      i += 2;
      continue;
    }
    if (c === '"') s = !s;
    else if (!s && tpl.startsWith("{{", i)) {
      const m = tpl.slice(i).match(/^\{\{\s*[^{}]+?\s*\}\}/);
      if (m) {
        out += JSON.stringify(m[0]);
        i += m[0].length;
        continue;
      }
    }
    out += c;
    i++;
  }
  return out;
}

function transformShape(n, inp, out) {
  switch (n.op) {
    case "json-path": {
      if (!known(inp)) return null;
      const r = lookup(inp, n.expr);
      return r.ok ? r.s : null;
    }
    case "json-pick": {
      if (!inp) return null;
      const keys = String(n.expr || "")
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean);
      const pick = (o) =>
        o?.type !== "object"
          ? o
          : {
              type: "object",
              properties: Object.fromEntries(keys.filter((k) => prop(o, k)).map((k) => [k, prop(o, k)])),
            };
      return inp.type === "array" ? { type: "array", items: pick(inp.items) } : pick(inp);
    }
    case "json-template": {
      let t;
      try {
        t = JSON.parse(quoteBare(n.template || ""));
      } catch {
        return null;
      }
      if ((n.expr || "").trim()) {
        const l = lookup(inp, n.expr).s;
        return {
          type: "array",
          items: tplShape(t, { this: l?.type === "array" ? l.items : null, root: inp }),
        };
      }
      return tplShape(t, { this: inp, root: inp });
    }
    case "json-stringify":
    case "json-parse": {
      const f = (n.expr || "").trim(),
        repl =
          n.op === "json-parse"
            ? { description: "parsed from a JSON string" }
            : { type: "string", description: "JSON as a string" };
      if (!f) return n.op === "json-parse" ? null : repl;
      if (!inp) return null;
      const c = JSON.parse(JSON.stringify(inp));
      let cur = c;
      const toks = [...f.replace(/^\$\.?/, "").matchAll(/\[(\d+|\*)\]|([^.[\]]+)/g)];
      toks.forEach((m, i) => {
        if (!cur) return;
        const last = i === toks.length - 1;
        if (cur.type === "array" && cur.items) {
          if (m[1] != null) {
            if (last) cur.items = repl;
            cur = cur.items;
            return;
          }
          cur = cur.items;
        }
        if (cur?.type === "object" && cur.properties?.[m[2]]) {
          if (last) cur.properties[m[2]] = repl;
          cur = cur.properties[m[2]];
        } else cur = null;
      });
      return c;
    }
    case "split-lines":
      return { type: "array", items: { type: "string" } };
    case "csv-to-json":
      return { type: "array", items: { type: "object" } };
    case "json-pretty":
    case "json-minify":
      return inp;
    case "template":
      return templateShape(n.template, out);
    case "ai":
    case "command":
      return null;
  }
  return { type: "string" };
}

export function shapes(flow, order, hints = {}) {
  const out = {},
    node = (id) => flow.nodes.find((x) => x.id === id),
    label = (n) => n?.title || n?.id;
  for (const id of order) {
    const n = node(id),
      parents = flow.edges.filter((e) => e.to === id).map((e) => e.from);
    let inp =
      parents.length === 1
        ? (out[parents[0]]?.out ?? null)
        : parents.length > 1
          ? { type: "string", description: "the connected steps' outputs, one after another" }
          : null;
    if ((n.source || "").trim() && ["transform", "loop", "decision", "filter", "aggregate"].includes(n.type))
      inp = templateShape(n.source, out);
    const s = { in: inp, source: "derived" };
    const sample = () => {
      const t = hints.samples?.[id];
      if (t && String(t).trim()) {
        s.out = schemaOfText(t);
        s.source = "sample";
      } else if (!s.out) s.source = "unknown";
    };
    switch (n.type) {
      case "start":
        s.out = {
          type: "object",
          description: "the flow's inputs (each is text)",
          properties: Object.fromEntries(
            (flow.inputs || []).map((i) => [
              i.name,
              {
                type: "string",
                description: i.label || i.name,
                ...(i.type === "choice" && i.options?.length ? { enum: i.options } : {}),
              },
            ]),
          ),
          required: (flow.inputs || []).filter((i) => i.required).map((i) => i.name),
        };
        s.source = "declared";
        break;
      case "tool": {
        const t = toolOutput(hints.tools?.[n.tool], hints.toolsBy?.[n.tool], n.args);
        if (t) {
          s.out = t;
          s.source = "declared";
        } else sample();
        break;
      }
      case "ai":
        if (n.outputSchema) {
          s.out = n.outputSchema;
          s.source = "declared";
        } else sample();
        break;
      case "merge":
        if (n.mode === "json-object")
          s.out = {
            type: "object",
            properties: Object.fromEntries(parents.map((p) => [label(node(p)), out[p]?.out || {}])),
          };
        else if (n.mode === "json-array") {
          let it;
          for (const p of parents) it = merge(it, out[p]?.out || {});
          s.out = { type: "array", ...(it ? { items: it } : {}) };
        } else s.out = n.mode === "template" ? templateShape(n.template, out) : { type: "string" };
        break;
      case "transform":
        s.out = transformShape(n, inp, out);
        if (!s.out) sample();
        break;
      case "loop": {
        let list = inp,
          item = null;
        if (n.split !== "regex" && list?.type === "object") {
          const ps = Object.values(list.properties || {}),
            arrs = ps.filter((p) => p?.type === "array");
          list = (n.field || "").trim()
            ? lookup(list, n.field).s
            : ps.length === 1
              ? ps[0]
              : arrs.length === 1
                ? arrs[0]
                : list;
        }
        if (n.split === "lines" || n.split === "regex") item = { type: "string" };
        else if (list?.type === "array") item = list.items || null;
        else if (list?.type === "string") item = { type: "string", description: "a line" };
        s.item = item;
        s.out = item;
        break;
      }
      case "gather": {
        let body;
        for (const p of parents) body = merge(body, out[p]?.out || {});
        s.out =
          n.mode === "json-array"
            ? { type: "array", ...(body ? { items: body } : {}) }
            : n.mode === "json-object"
              ? { type: "object", description: "one key per item" }
              : { type: "string" };
        break;
      }
      case "decision":
        s.out = inp;
        break;
      case "filter":
        s.rows = rowShape(inp, n.split);
        s.out =
          n.split === "lines" || (s.rows?.type === "string" && inp?.type === "string")
            ? { type: "string", description: "one row per line" }
            : { type: "array", ...(s.rows ? { items: s.rows } : {}) };
        break;
      case "aggregate":
        s.rows = rowShape(inp, n.split);
        s.out = { type: "array", items: aggRow(n, s.rows) };
        break;
      case "output":
        s.out = inp;
        break;
    }
    if (!s.out && s.source === "derived") s.source = "unknown";
    out[id] = s;
  }
  return out;
}

// checkPaths: warnings for fields a flow uses that its shapes say are not
// there (mirrors Flow.checkPaths); unknown shapes accept any path.
export function checkPaths(flow, sh, loopStack = {}) {
  const out = [],
    node = (id) => flow.nodes.find((x) => x.id === id),
    label = (n) => n?.title || n?.id;
  const warn = (n, what, path, s, of) => {
    path = String(path || "");
    if (path.includes("{{") || !known(s)) return;
    const p = path.trim().replace(/^this/, "").replace(/^\./, "");
    if (!p || lookup(s, p).ok) return;
    let fs = fields(s);
    if (fs.length > 12) fs = [...fs.slice(0, 12), "…"];
    out.push({
      node: n.id,
      level: "warning",
      message:
        `${label(n)}: ${what} "${p}" is not in ${of}.` + (fs.length ? ` Its fields: ${fs.join(", ")}.` : ""),
    });
  };
  const condFields = (c) => (c?.rules || []).flatMap((r) => (r.group ? condFields(r.group) : [r.field]));
  for (const n of flow.nodes) {
    const s = sh[n.id] || {},
      ps = flow.edges.filter((e) => e.to === n.id).map((e) => e.from),
      from = ps.length === 1 ? "what " + label(node(ps[0])) + " gives" : "the data it gets";
    if (n.type === "decision")
      for (const b of n.branches || [])
        for (const fl of condFields(b.when)) warn(n, "the field", fl, s.in, from);
    if (n.type === "filter") {
      for (const fl of condFields(n.cond)) warn(n, "the field", fl, s.rows, `its rows (${from})`);
      for (const o of n.orderBy || []) warn(n, "the sort field", o.replace(/^[-+]/, ""), s.rows, "its rows");
    }
    if (n.type === "aggregate") {
      for (const g of n.groupBy || []) warn(n, "the group field", g, s.rows, `its rows (${from})`);
      for (const m of n.measures || []) warn(n, "the field", m.field, s.rows, "its rows");
      for (const o of n.orderBy || [])
        warn(
          n,
          "the sort field",
          o.replace(/^[-+]/, ""),
          n.mode !== "window" && s.out ? s.out.items : s.rows,
          "its result",
        );
    }
    if (n.type === "loop" && (n.field || "").trim()) warn(n, "the list field", n.field, s.in, from);
    if (n.type === "transform" && n.op === "json-path") warn(n, "the path", n.expr, s.in, from);
    if (n.type === "transform" && n.op === "json-template" && (n.expr || "").trim())
      warn(n, "the list path", n.expr, s.in, from);
    const texts = [
      n.prompt,
      n.template,
      n.source,
      n.instruction,
      ...Object.values(n.args || {}),
      ...(n.actions || []).flatMap((a) => [a.path, ...Object.values(a.args || {})]),
    ];
    for (const t of texts)
      for (const m of String(t || "").matchAll(/\{\{\s*([^{}]+?)\s*\}\}/g)) {
        const v = m[1],
          i = v.indexOf("."),
          head = i < 0 ? v : v.slice(0, i),
          rest = i < 0 ? "" : v.slice(i + 1);
        let id, path, base;
        if (head === "steps") {
          const [sid, ...p] = rest.split("."),
            pp = p.join(".");
          if (!pp.startsWith("json")) continue;
          id = sid;
          path = pp.slice(4);
          base = sh[id]?.out;
        } else if (rest.startsWith("buffer_output.json")) {
          id = head;
          path = rest.slice(18);
          base = sh[id]?.out;
        } else if (rest.startsWith("item.json")) {
          id = head;
          path = rest.slice(9);
          base = sh[id]?.item;
        } else if (head === "item" && rest.startsWith("json")) {
          const st = loopStack[n.id] || [];
          if (!st.length) continue;
          id = st[st.length - 1];
          path = rest.slice(4);
          base = sh[id]?.item;
        } else continue;
        if (node(id))
          warn(n, `{{${v}}}: the path`, path.replace(/^\./, ""), base, "what " + label(node(id)) + " gives");
      }
  }
  return out;
}

// sampleOf: example data of a shape (a few varied list elements, the first
// allowed values, dates), for trial runs. Mirrors flow.SampleOf.
export function sampleOf(s, name = "value", i = 0, depth = 0) {
  if (!s || depth > 8) return "sample";
  switch (s.type) {
    case "object":
      return Object.fromEntries(
        Object.entries(s.properties || {}).map(([k, p]) => [k, sampleOf(p, k, i, depth + 1)]),
      );
    case "array":
      return [0, 1, 2].map((k) => sampleOf(s.items, name, k, depth + 1));
    case "integer":
      return i + 1;
    case "number":
      return (i + 1) * 10 + 0.5;
    case "boolean":
      return i % 2 === 0;
    case "null":
      return null;
    case "string":
      if (s.enum?.length) return s.enum[i % s.enum.length];
      if (s.format === "date") return `2026-11-${10 + i}`;
      if (s.format === "date-time") return `2026-11-${10 + i}T09:00:00Z`;
      return `${name} ${i + 1}`;
  }
  return `${name} ${i + 1}`;
}
