// Tool parameters in the browser preview: the same rules as
// internal/extension/params.go — inference without AI (placeholders, types
// and formats from names and descriptions) and checking arguments.

const RE = {
  int: /(^|_|-)(port|limit|max|min|count|size|top|depth|retries|retry|timeout|seconds|minutes|hours|days|lines|page|per_page|offset|n|num|number|year|width|height)$/i,
  num: /(^|_|-)(ratio|rate|threshold|percent|percentage|score|amount|price)$/i,
  bool: /^(is|has|enable|enabled|disable|disabled|dry|dry_run|dryrun|verbose|force|recursive|include|exclude|allow|use|skip|strict|debug|quiet|overwrite|fix|all|no)(_|-|$)|(_|-)(only|enabled|flag)$/i,
  path: /(^|_|-)(file|path|filepath|filename|src|source_file|input_file|output|output_path|dest|destination|config|manifest|spec)$/i,
  dir: /(^|_|-)(dir|directory|folder|root|workspace|cwd)$/i,
  url: /(^|_|-)(url|uri|endpoint|link|href|webhook)$/i,
  date: /(^|_|-)(date|day|since|until|from_date|to_date|start_date|end_date|due)$/i,
  time: /(^|_|-)(time|datetime|timestamp|at|when|start|end|deadline)$/i,
  text: /(^|_|-)(query|sql|body|content|text|template|markdown|md|html|script|code|prompt|message|description|draft|diagram|json|yaml|payload|notes)$/i,
  email: /(^|_|-)(email|mail)$/i,
  secret: /(^|_|-)(password|passwd|pwd|secret|token|api_key|apikey|key)$/i,
};
const ENUM_BAR = /[([]\s*([A-Za-z0-9_.-]+(?:\s*\|\s*[A-Za-z0-9_.-]+)+)\s*[)\]]/;
const ENUM_ONE_OF =
  /\b(?:one of|either|choose|allowed(?: values)?|values?)\s*[:=]?\s*([A-Za-z0-9_.-]+(?:\s*(?:,|\||\/|\bor\b)\s*[A-Za-z0-9_.-]+)+)/i;
const DEFAULT_RE = /\bdefault(?:s)?(?: is| to|:)?\s*[:=]?\s*["'`]?([A-Za-z0-9_./:-]+)/i;

const splitEnum = (s) => {
  const out = s
    .split(/\s*(?:,|\||\/|\bor\b)\s*/)
    .map((x) => x.trim().replace(/^["'.]+|["'.]+$/g, ""))
    .filter(Boolean);
  return out.length >= 2 && out.length <= 20 ? out : null;
};

function inferOne(p) {
  const desc = (p.desc || "").toLowerCase();
  if (!(p.enum || []).length && p.type === "string") {
    const m = (p.desc || "").match(ENUM_BAR) || (p.desc || "").match(ENUM_ONE_OF);
    const e = m && splitEnum(m[1]);
    if (e) p.enum = e;
  }
  if (!p.default) {
    const m = (p.desc || "").match(DEFAULT_RE);
    if (m) p.default = m[1].replace(/[.,;)]+$/, "");
  }
  if (p.type !== "string" || (p.enum || []).length || p.format) return;
  const n = p.name;
  if (RE.bool.test(n) || desc.includes("true or false") || desc.includes("yes or no")) p.type = "boolean";
  else if (RE.int.test(n) || desc.includes("number of") || desc.includes("port number")) p.type = "integer";
  else if (RE.num.test(n)) p.type = "number";
  else if (RE.secret.test(n)) p.format = "password";
  else if (RE.dir.test(n) || desc.includes("folder") || desc.includes("directory")) p.format = "directory";
  else if (RE.url.test(n) || desc.includes("url")) p.format = "url";
  else if (RE.email.test(n)) p.format = "email";
  else if (RE.time.test(n) || desc.includes("timestamp") || desc.includes("date and time"))
    p.format = "date-time";
  else if (RE.date.test(n) || desc.includes("yyyy-mm-dd") || desc.includes("a date")) p.format = "date";
  else if (RE.path.test(n) || desc.includes("file path") || desc.includes("path to")) p.format = "path";
  else if (
    RE.text.test(n) ||
    desc.includes("markdown") ||
    desc.includes("multi-line") ||
    desc.includes("multiline")
  )
    p.format = "multiline";
  if (p.type === "string" && (desc.includes("json object") || desc.includes("json array"))) p.type = "json";
}

function describe(a, b) {
  const parts = [];
  if (a.type !== b.type) parts.push(`type ${a.type} → ${b.type}`);
  if ((a.format || "") !== (b.format || "")) parts.push("format " + (b.format || "none"));
  if ((a.enum || []).join("|") !== (b.enum || []).join("|")) parts.push("values: " + b.enum.join(", "));
  if ((a.default || "") !== (b.default || "")) parts.push("default " + b.default);
  return parts.join("; ");
}

// inferParams refines a tool's parameters without AI (see params.go).
export function inferParams(e) {
  const ps = (e.params || []).map((p) => ({ ...p, type: p.type || "string" }));
  const changes = [];
  const have = new Set(ps.map((p) => p.name));
  for (const m of String(e.command || "").matchAll(/\{\{(\w+)\}\}/g)) {
    const n = m[1];
    if (have.has(n) || n === "input_file" || n === "output_file") continue;
    have.add(n);
    ps.push({ name: n, type: "string", required: false, desc: `Used in the command as {{${n}}}` });
    changes.push({ param: n, what: `added: the command uses {{${n}}}` });
  }
  for (const p of ps) {
    const before = { ...p, enum: [...(p.enum || [])] };
    inferOne(p);
    const d = describe(before, p);
    if (d) changes.push({ param: p.name, what: d });
  }
  return { params: ps, changes };
}

// checkArgs checks and converts arguments (see CheckArgs in params.go).
export function checkArgs(e, args) {
  const out = { ...args },
    probs = [];
  for (const p of e.params || []) {
    let v = out[p.name];
    if (v === undefined || v === null || (typeof v === "string" && !v.trim())) {
      if (p.default) v = out[p.name] = p.default;
      else {
        if (p.required) probs.push(`${p.name} is required`);
        continue;
      }
    }
    const s = typeof v === "string" ? v.trim() : v;
    if (p.type === "integer") {
      if (!/^-?\d+$/.test(String(s))) probs.push(`${p.name}: "${s}" is not a whole number`);
      else out[p.name] = Number(s);
    } else if (p.type === "number") {
      if (isNaN(Number(s)) || s === "") probs.push(`${p.name}: "${s}" is not a number`);
      else out[p.name] = Number(s);
    } else if (p.type === "boolean") {
      if (/^(true|yes|y|1|on)$/i.test(String(s))) out[p.name] = true;
      else if (/^(false|no|n|0|off)$/i.test(String(s))) out[p.name] = false;
      else probs.push(`${p.name}: "${s}" is not true or false`);
    } else if (["json", "object", "array"].includes(p.type)) {
      if (typeof s === "string")
        try {
          const x = JSON.parse(s);
          if (p.type === "object" && (typeof x !== "object" || Array.isArray(x) || !x))
            probs.push(`${p.name}: must be a JSON object`);
          else if (p.type === "array" && !Array.isArray(x)) probs.push(`${p.name}: must be a JSON array`);
          else out[p.name] = x;
        } catch (err) {
          probs.push(`${p.name}: not valid JSON: ${err.message}`);
        }
    } else {
      if ((p.enum || []).length && !p.enum.includes(String(s)))
        probs.push(`${p.name}: "${s}" is not one of: ${p.enum.join(", ")}`);
      else if (p.format === "date" && !/^\d{4}-\d{2}-\d{2}$/.test(s))
        probs.push(`${p.name}: "${s}" is not a date (YYYY-MM-DD)`);
      else if (p.format === "url" && !String(s).includes("://")) probs.push(`${p.name}: "${s}" is not a URL`);
      else if (p.format === "email" && !String(s).includes("@"))
        probs.push(`${p.name}: "${s}" is not an email address`);
    }
  }
  return { args: out, problems: probs };
}
