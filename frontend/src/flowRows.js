// Decision, Filter and Aggregate steps for the browser preview's mock
// backend. Mirrors internal/flow/cond.go and internal/flow/rows.go.

export const COND_OPS = [
  ["eq", "="],
  ["ne", "≠"],
  ["gt", ">"],
  ["gte", "≥"],
  ["lt", "<"],
  ["lte", "≤"],
  ["contains", "contains"],
  ["not_contains", "does not contain"],
  ["starts", "starts with"],
  ["ends", "ends with"],
  ["matches", "matches pattern"],
  ["in", "is one of (a, b, c)"],
  ["not_in", "is not one of"],
  ["between", "is between (low, high)"],
  ["empty", "is empty", true],
  ["not_empty", "is not empty", true],
  ["exists", "exists", true],
  ["not_exists", "does not exist", true],
  ["true", "is true", true],
  ["false", "is false", true],
  ["len_eq", "count / length ="],
  ["len_gt", "count / length >"],
  ["len_lt", "count / length <"],
].map(([id, name, unary]) => ({ id, name, unary: !!unary }));

export const AGG_FNS = [
  ["count", "count", 0, 1],
  ["count_distinct", "distinct count"],
  ["sum", "sum"],
  ["avg", "average"],
  ["min", "min"],
  ["max", "max"],
  ["median", "median"],
  ["first", "first"],
  ["last", "last"],
  ["list", "list of values"],
  ["distinct", "distinct values"],
  ["concat", "join as text", 0, 0, "Separator (default , )"],
  ["row_number", "row number", 1, 1],
  ["rank", "rank", 1, 1],
  ["dense_rank", "dense rank", 1, 1],
  ["running_sum", "running sum", 1],
  ["running_count", "running count", 1, 1],
  ["running_avg", "running average", 1],
  ["lag", "previous row's value (lag)", 1, 0, "Rows back (default 1)"],
  ["lead", "next row's value (lead)", 1, 0, "Rows ahead (default 1)"],
  ["pct_of_total", "% of total", 1],
  ["ntile", "bucket (ntile)", 1, 1, "Buckets (default 4)"],
].map(([id, name, window, noField, arg]) => ({
  id,
  name,
  window: !!window,
  noField: !!noField,
  arg: arg || "",
}));

const VAR = /\{\{\s*([^{}]+?)\s*\}\}/g;
const isObj = (x) => x && typeof x === "object" && !Array.isArray(x);
export const textOf = (v) =>
  v == null ? "" : typeof v === "string" ? v : typeof v === "object" ? JSON.stringify(v) : String(v);

// a path into JSON: a.b[0].c, items[*].qty, items.qty (a key on a list maps over it)
export function walk(v, path) {
  path = String(path || "")
    .trim()
    .replace(/^\$/, "")
    .replace(/^\./, "");
  if (!path) return { ok: true, v };
  let cur = [v],
    wild = false;
  for (const m of path.matchAll(/\[(\d+|\*)\]|\["([^"]+)"\]|([^.[\]]+)/g)) {
    const nx = [];
    for (const c of cur) {
      if (m[1] === "*") {
        wild = true;
        nx.push(...(Array.isArray(c) ? c : isObj(c) ? Object.values(c) : []));
      } else if (m[1] != null) {
        if (Array.isArray(c) && +m[1] < c.length) nx.push(c[+m[1]]);
      } else {
        const k = m[2] ?? m[3];
        if (isObj(c) && k in c) nx.push(c[k]);
        else if (Array.isArray(c)) {
          wild = true;
          for (const e of c) if (isObj(e) && k in e) nx.push(e[k]);
        }
      }
    }
    cur = nx;
    if (!cur.length) return { ok: false };
  }
  return { ok: true, v: wild ? cur : cur[0] };
}

function fieldOf(field, data, render) {
  field = String(field || "").trim();
  if (field.includes("{{")) {
    const s = renderWith(field, data, render);
    try {
      return { ok: true, v: s.trim() ? JSON.parse(s) : s };
    } catch {
      return { ok: true, v: s };
    }
  }
  field = field.replace(/^this/, "").replace(/^\./, "");
  if (!field || field === "$") return { ok: true, v: data };
  return walk(data, field);
}
const renderWith = (tpl, data, render) =>
  String(tpl || "").replace(VAR, (m, v) => {
    if (v === "this" || v.startsWith("this.")) {
      const r = fieldOf(v.slice(4), data, render);
      return r.ok ? textOf(r.v) : "";
    }
    return render ? render(m) : "";
  });

const num = (x) => {
  if (typeof x === "number") return [x, true];
  if (typeof x === "string" && x.trim() !== "" && !isNaN(+x)) return [+x, true];
  return [0, false];
};
const isEmpty = (x) =>
  x == null ||
  (typeof x === "string" && !x.trim()) ||
  (Array.isArray(x) && !x.length) ||
  (isObj(x) && !Object.keys(x).length);
const lengthOf = (x) =>
  x == null
    ? 0
    : typeof x === "string"
      ? [...x].length
      : Array.isArray(x)
        ? x.length
        : isObj(x)
          ? Object.keys(x).length
          : textOf(x).length;
const truthy = (x) => {
  if (x == null) return false;
  if (typeof x === "boolean") return x;
  if (typeof x === "string") return !["", "false", "no", "0", "off"].includes(x.trim().toLowerCase());
  const [f, ok] = num(x);
  return ok ? f !== 0 : !isEmpty(x);
};
const splitList = (s) => {
  s = String(s || "").trim();
  if (s.startsWith("[")) {
    try {
      return JSON.parse(s).map(textOf);
    } catch {
      /* text */
    }
  }
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
};

function compare(x, want, fold) {
  if (x == null) return String(want).trim() === "" || want === "null" ? [0, true] : [-1, false];
  let a = textOf(x);
  const [fa, okA] = num(x),
    fb = String(want).trim();
  if (okA && fb !== "" && !isNaN(+fb)) return [fa < +fb ? -1 : fa > +fb ? 1 : 0, true];
  if (typeof x === "boolean" && /^(true|false)$/i.test(fb))
    return [x === (fb.toLowerCase() === "true") ? 0 : 1, true];
  if (typeof x === "object") {
    try {
      want = JSON.stringify(JSON.parse(want));
    } catch {
      /* text */
    }
  }
  if (fold) {
    a = a.toLowerCase();
    want = String(want).toLowerCase();
  }
  const w = String(want).trim();
  return [a < w ? -1 : a > w ? 1 : 0, true];
}

function ruleEval(r, data, render) {
  const f = fieldOf(r.field, data, render),
    v = f.v,
    want = renderWith(r.value, data, render);
  switch (r.op) {
    case "exists":
      return f.ok && v != null;
    case "not_exists":
      return !f.ok || v == null;
    case "empty":
      return isEmpty(v);
    case "not_empty":
      return !isEmpty(v);
    case "len_eq":
    case "len_gt":
    case "len_lt": {
      const n = parseInt(want, 10);
      if (isNaN(n)) throw new Error(`${r.field}: the count "${want}" is not a whole number`);
      const l = lengthOf(v);
      return r.op === "len_eq" ? l === n : r.op === "len_gt" ? l > n : l < n;
    }
    case "contains":
    case "not_contains": {
      let has;
      if (Array.isArray(v)) has = v.some((e) => compare(e, want, r.ignoreCase)[0] === 0);
      else {
        let a = textOf(v),
          b = want;
        if (r.ignoreCase) {
          a = a.toLowerCase();
          b = b.toLowerCase();
        }
        has = f.ok && a.includes(b);
      }
      return has === (r.op === "contains");
    }
  }
  if (!f.ok) return r.op === "ne" || r.op === "not_in";
  let re = null;
  if (r.op === "matches") {
    try {
      re = new RegExp(want, r.ignoreCase ? "i" : "");
    } catch {
      throw new Error(`the pattern "${want}" is not a valid regular expression`);
    }
  }
  const one = (x) => {
    switch (r.op) {
      case "true":
        return truthy(x);
      case "false":
        return !truthy(x);
      case "matches":
        return re.test(textOf(x));
      case "starts":
      case "ends": {
        let a = textOf(x),
          b = want;
        if (r.ignoreCase) {
          a = a.toLowerCase();
          b = b.toLowerCase();
        }
        return r.op === "starts" ? a.startsWith(b) : a.endsWith(b);
      }
      case "in":
      case "not_in":
        return splitList(want).some((w) => compare(x, w, r.ignoreCase)[0] === 0) === (r.op === "in");
      case "between": {
        const lh = splitList(want);
        if (lh.length !== 2) return false;
        const [lo, a] = compare(x, lh[0], r.ignoreCase),
          [hi, b] = compare(x, lh[1], r.ignoreCase);
        return a && b && lo >= 0 && hi <= 0;
      }
    }
    const [c, ok] = compare(x, want, r.ignoreCase);
    return (
      { eq: c === 0, ne: c !== 0, gt: ok && c > 0, gte: ok && c >= 0, lt: ok && c < 0, lte: ok && c <= 0 }[
        r.op
      ] ?? false
    );
  };
  if (Array.isArray(v) && !((r.op === "eq" || r.op === "ne") && want.trim().startsWith("["))) {
    if (!v.length) return !!r.every && r.op !== "true";
    return r.every ? v.every(one) : v.some(one);
  }
  return one(v);
}

// condEval: does data satisfy the condition (nested groups of rules)?
export function condEval(c, data, render) {
  if (!c || !(c.rules || []).length) return true;
  const match = c.match || "all";
  let hits = 0;
  for (const r of c.rules) {
    const ok = r.group ? condEval(r.group, data, render) : ruleEval(r, data, render);
    if (ok) hits++;
    if (match === "all" && !ok) return false;
    if (match === "any" && ok) return true;
    if (match === "none" && ok) return false;
  }
  return match !== "any" || hits > 0;
}

export function condCheck(c) {
  if (!c || !(c.rules || []).length) return "add at least one condition";
  for (const [i, r] of c.rules.entries()) {
    if (r.group) {
      const e = condCheck(r.group);
      if (e) return `group ${i + 1}: ${e}`;
      continue;
    }
    if (!COND_OPS.some((o) => o.id === r.op)) return `condition ${i + 1}: choose a comparison`;
    if (r.op === "matches" && !String(r.value || "").includes("{{")) {
      try {
        new RegExp(r.value || "");
      } catch {
        return `condition ${i + 1}: the pattern is not a valid regular expression`;
      }
    }
    if (r.op?.startsWith("len_") && !String(r.value || "").includes("{{") && isNaN(parseInt(r.value, 10)))
      return `condition ${i + 1}: the count must be a whole number`;
    if (r.op === "between" && !String(r.value || "").includes("{{") && splitList(r.value).length !== 2)
      return `condition ${i + 1}: write the range as low, high`;
  }
  return "";
}
export const condTemplates = (c) =>
  (c?.rules || []).flatMap((r) => (r.group ? condTemplates(r.group) : [r.field || "", r.value || ""]));

// rows: a JSON array, an object holding one array, CSV with a header, or lines
export function parseRows(input, format = "auto") {
  const s = String(input || "").trim();
  format ||= "auto";
  if (!s) return { rows: [], text: format === "lines" };
  if (format === "auto" || format === "json") {
    try {
      const v = JSON.parse(s);
      if (Array.isArray(v)) return { rows: v, text: false };
      if (isObj(v)) {
        const lists = Object.values(v).filter(Array.isArray);
        return { rows: lists.length === 1 ? lists[0] : [v], text: false };
      }
      return { rows: [v], text: false };
    } catch (e) {
      if (format === "json") throw new Error("the input is not JSON: " + e.message);
    }
  }
  const lines = s.replace(/\r\n/g, "\n").split("\n");
  if (format === "csv" || (format === "auto" && lines.length > 1 && lines[0].includes(","))) {
    const head = lines[0].split(",").map((x) => x.trim());
    return {
      rows: lines
        .slice(1)
        .filter((l) => l.trim())
        .map((l) => {
          const c = l.split(",");
          return Object.fromEntries(
            head.map((h, i) => {
              const x = (c[i] ?? "").trim();
              return [h, x !== "" && !isNaN(+x) ? +x : x];
            }),
          );
        }),
      text: false,
    };
  }
  return { rows: lines.map((l) => l.trim()).filter(Boolean), text: true };
}
const writeRows = (rows, text) => (text ? rows.map(textOf).join("\n") : JSON.stringify(rows, null, 2));

const cmpVals = (a, b) => {
  if (a == null && b == null) return 0;
  if (a == null) return 1;
  if (b == null) return -1;
  const [x, o1] = num(a),
    [y, o2] = num(b);
  if (o1 && o2) return x < y ? -1 : x > y ? 1 : 0;
  const s = textOf(a),
    t = textOf(b);
  return s < t ? -1 : s > t ? 1 : 0;
};
const rowLess = (a, b, by, render) => {
  for (let f of by) {
    f = f.trim();
    const desc = f.startsWith("-");
    f = f.replace(/^[-+]/, "");
    const c = cmpVals(fieldOf(f, a, render).v, fieldOf(f, b, render).v);
    if (c) return desc ? c > 0 : c < 0;
  }
  return false;
};
const sortRows = (rows, by, render) =>
  rows
    .map((r, i) => [r, i])
    .sort(([a, i], [b, j]) => (rowLess(a, b, by, render) ? -1 : rowLess(b, a, by, render) ? 1 : i - j))
    .map(([r]) => r);

export function filterRows(n, input, render) {
  const { rows, text } = parseRows(input, n.split),
    keep = n.mode !== "drop",
    seen = new Set();
  let out = [];
  for (const r of rows) {
    if (condEval(n.cond, r, render) !== keep) continue;
    if (n.unique) {
      const k = JSON.stringify(r);
      if (seen.has(k)) continue;
      seen.add(k);
    }
    out.push(r);
    if (n.max > 0 && out.length === n.max && !(n.orderBy || []).length) break;
  }
  if ((n.orderBy || []).length) out = sortRows(out, n.orderBy, render); // sort, then keep the first max: a top N
  if (n.max > 0) out = out.slice(0, n.max);
  return writeRows(out, text);
}

export function decide(n, input, render) {
  let data = input;
  try {
    if (String(input).trim()) data = JSON.parse(input);
  } catch {
    /* text */
  }
  const out = [];
  for (const b of n.branches || []) {
    let ok;
    try {
      ok = condEval(b.when, data, render);
    } catch (e) {
      throw new Error(`${b.label || b.id}: ${e.message}`);
    }
    if (ok) {
      out.push(b.id);
      if (n.mode !== "all") break;
    }
  }
  return out.length ? out : ["else"];
}
export const branchLabel = (n, id) =>
  id === "else" ? "Otherwise" : (n.branches || []).find((b) => b.id === id)?.label || id;

// liveParents: the connected steps whose output reaches id (on the path taken)
export function liveParents(f, id, off, branches) {
  const out = [];
  for (const e of f.edges) {
    if (e.to !== id || out.includes(e.from) || off(e.from)) continue;
    const pn = f.nodes.find((x) => x.id === e.from);
    if (pn?.type === "decision" && e.branch && !(branches[e.from] || []).includes(e.branch)) continue;
    out.push(e.from);
  }
  return out;
}

const measureName = (m) =>
  (m.as || "").trim() ||
  (m.field
    ? m.fn + "_" + m.field.replace(/\./g, "_").replace(/\[/g, "_").replace(/\]/g, "").replace(/\*/g, "all")
    : m.fn);
const round = (f) => Math.round(f * 1e9) / 1e9;

export function aggregateCheck(n) {
  const mode = n.mode || "group";
  if (!["group", "window"].includes(mode)) return "choose group or window";
  if (!(n.measures || []).length && !(n.groupBy || []).length)
    return "add a value to calculate, or a field to group by";
  const names = new Set();
  for (const [i, m] of (n.measures || []).entries()) {
    const fn = AGG_FNS.find((x) => x.id === m.fn);
    if (!fn) return `value ${i + 1}: choose a function`;
    if (fn.window && mode !== "window")
      return `value ${i + 1}: ${fn.name} works in window mode (every row, with its group's values)`;
    if (!fn.noField && !(m.field || "").trim() && !["count_distinct", "list", "distinct"].includes(m.fn))
      return `value ${i + 1}: choose the field to ${fn.name}`;
    if (["lag", "lead", "ntile"].includes(m.fn) && (m.arg || "").trim() && !(parseInt(m.arg, 10) >= 1))
      return `value ${i + 1}: ${fn.name} needs a whole number of at least 1`;
    const nm = measureName(m);
    if (names.has(nm)) return `two values are called "${nm}": give each its own name`;
    names.add(nm);
  }
  return "";
}

function agg(m, vals, nrows) {
  const nums = vals
    .map((v) => num(v))
    .filter(([, ok]) => ok)
    .map(([f]) => f);
  const distinct = () => {
    const seen = new Set();
    return vals.filter((v) => {
      const k = JSON.stringify(v);
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  };
  switch (m.fn) {
    case "count":
      return (m.field || "").trim() ? vals.filter((v) => !isEmpty(v)).length : nrows;
    case "count_distinct":
      return distinct().length;
    case "sum":
      return round(nums.reduce((a, b) => a + b, 0));
    case "avg":
      return nums.length ? round(nums.reduce((a, b) => a + b, 0) / nums.length) : null;
    case "median": {
      if (!nums.length) return null;
      const s = [...nums].sort((a, b) => a - b),
        h = s.length >> 1;
      return round(s.length % 2 ? s[h] : (s[h - 1] + s[h]) / 2);
    }
    case "min":
    case "max": {
      let best = null;
      for (const v of vals) {
        if (v == null) continue;
        if (best == null || (m.fn === "min" ? cmpVals(v, best) < 0 : cmpVals(v, best) > 0)) best = v;
      }
      return best;
    }
    case "first":
      return vals.length ? vals[0] : null;
    case "last":
      return vals.length ? vals[vals.length - 1] : null;
    case "list":
      return vals;
    case "distinct":
      return distinct();
    case "concat":
      return vals
        .filter((v) => !isEmpty(v))
        .map(textOf)
        .join((m.arg || ", ").replace(/\\n/g, "\n"));
  }
  return null;
}

export function aggregateRows(n, input, render) {
  const err = aggregateCheck(n);
  if (err) throw new Error(err);
  const { rows } = parseRows(input, n.split),
    groups = [],
    byKey = new Map(),
    by = n.groupBy || [];
  rows.forEach((r, i) => {
    const key = by.map((g) => fieldOf(g, r, render).v ?? null),
      k = JSON.stringify(key);
    let g = byKey.get(k);
    if (!g) {
      g = { key, rows: [] };
      byKey.set(k, g);
      groups.push(g);
    }
    g.rows.push(i);
  });
  if (!rows.length && !by.length) groups.push({ key: [], rows: [] });
  const values = (idx, field) =>
    idx.flatMap((i) => {
      if (!(field || "").trim()) return [rows[i]];
      const r = fieldOf(field, rows[i], render);
      return !r.ok ? [] : Array.isArray(r.v) ? r.v : [r.v];
    });
  let out = [];
  if ((n.mode || "group") === "group") {
    for (const g of groups) {
      const o = {};
      by.forEach((f, j) => {
        o[f] = g.key[j];
      });
      for (const m of n.measures || []) o[measureName(m)] = agg(m, values(g.rows, m.field), g.rows.length);
      out.push(o);
    }
    if ((n.orderBy || []).length) out = sortRows(out, n.orderBy, render);
  } else {
    const extra = rows.map(() => ({}));
    for (const g of groups) {
      const ordered = (n.orderBy || []).length
        ? g.rows
            .map((i) => [i])
            .sort(([a], [b]) =>
              rowLess(rows[a], rows[b], n.orderBy, render)
                ? -1
                : rowLess(rows[b], rows[a], n.orderBy, render)
                  ? 1
                  : a - b,
            )
            .map(([i]) => i)
        : [...g.rows];
      for (const m of n.measures || []) {
        const name = measureName(m),
          fn = AGG_FNS.find((x) => x.id === m.fn),
          val = (i) => (!(m.field || "").trim() ? rows[i] : (fieldOf(m.field, rows[i], render).v ?? null));
        const k = parseInt(m.arg, 10) > 0 ? parseInt(m.arg, 10) : 1;
        if (!fn.window) {
          const v = agg(m, values(g.rows, m.field), g.rows.length);
          g.rows.forEach((i) => {
            extra[i][name] = v;
          });
          continue;
        }
        if (m.fn === "row_number" || m.fn === "running_count")
          ordered.forEach((i, p) => {
            extra[i][name] = p + 1;
          });
        if (m.fn === "rank" || m.fn === "dense_rank") {
          let rank = 0,
            dense = 0;
          ordered.forEach((i, p) => {
            if (
              !p ||
              !(n.orderBy || []).length ||
              rowLess(rows[ordered[p - 1]], rows[i], n.orderBy, render) ||
              rowLess(rows[i], rows[ordered[p - 1]], n.orderBy, render)
            ) {
              rank = p + 1;
              dense++;
            }
            extra[i][name] = m.fn === "rank" ? rank : dense;
          });
        }
        if (m.fn === "running_sum" || m.fn === "running_avg") {
          let s = 0,
            c = 0;
          ordered.forEach((i) => {
            const [f, ok] = num(val(i));
            if (ok) {
              s += f;
              c++;
            }
            extra[i][name] = m.fn === "running_sum" ? round(s) : c ? round(s / c) : null;
          });
        }
        if (m.fn === "lag" || m.fn === "lead")
          ordered.forEach((i, p) => {
            const j = m.fn === "lag" ? p - k : p + k;
            extra[i][name] = j >= 0 && j < ordered.length ? val(ordered[j]) : null;
          });
        if (m.fn === "pct_of_total") {
          const tot = ordered.reduce((a, i) => a + (num(val(i))[1] ? num(val(i))[0] : 0), 0);
          ordered.forEach((i) => {
            const [f, ok] = num(val(i));
            extra[i][name] = ok && tot ? Math.round((f / tot) * 10000) / 100 : null;
          });
        }
        if (m.fn === "ntile") {
          const b = parseInt(m.arg, 10) > 0 ? parseInt(m.arg, 10) : 4;
          ordered.forEach((i, p) => {
            extra[i][name] = Math.floor((p * b) / ordered.length) + 1;
          });
        }
      }
    }
    out = rows.map((r, i) => {
      const o = isObj(r) ? { ...r } : { value: r };
      for (const m of n.measures || []) o[measureName(m)] = extra[i][measureName(m)] ?? null;
      return o;
    });
  }
  if (n.max > 0) out = out.slice(0, n.max);
  return writeRows(out, false);
}
