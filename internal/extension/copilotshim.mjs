// SpecWright: run a GitHub Copilot CLI extension's tools without Copilot.
//
//   node copilot-shim.mjs list <extension.mjs>
//   node copilot-shim.mjs call <extension.mjs> <tool> '<arguments JSON>'
//
// Copilot supplies @github/copilot-sdk to its extensions at run time; this
// stands in for it. joinSession() records the tools (and hooks) the
// extension registers instead of connecting to a Copilot session, so a tool
// can be listed or called on its own: by Claude through agentbridge, in a
// flow step, or in Tools › Test with AI. Hooks and session events are not
// available outside Copilot.
import * as nodeModule from "node:module";
import { pathToFileURL } from "node:url";

const SDK = ["@github/copilot-sdk/extension", "@github/copilot-sdk"];
const STUB = `
const noop = () => {};
const session = new Proxy({}, { get: (t, k) => (k === "then" ? undefined : noop) });
export async function joinSession(options = {}) { (globalThis.__specwright ||= {}).options = options; return session; }
export function defineTool(name, def = {}) { return { name, ...def }; }
export default { joinSession, defineTool };
`;
const STUB_URL = "data:text/javascript," + encodeURIComponent(STUB);

// keep our stdout for the result: anything the extension prints goes to stderr
const writeOut = process.stdout.write.bind(process.stdout);
const toErr = (...a) => { process.stderr.write(a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ") + "\n"); };
console.log = console.info = console.debug = console.warn = toErr;
process.stdout.write = (chunk, enc, cb) => process.stderr.write(chunk, enc, cb);

function finish(text, code) {
  writeOut(text.endsWith("\n") || text === "" ? text : text + "\n", () => process.exit(code));
}
function fail(message) {
  process.stderr.write("error: " + message + "\n", () => process.exit(1));
}

if (nodeModule.registerHooks) {
  nodeModule.registerHooks({ resolve: (spec, ctx, next) => (SDK.includes(spec) ? { url: STUB_URL, shortCircuit: true } : next(spec, ctx)) });
} else if (nodeModule.register) {
  const loader = `export async function resolve(s, c, n) { return ${JSON.stringify(SDK)}.includes(s) ? { url: ${JSON.stringify(STUB_URL)}, shortCircuit: true } : n(s, c); }`;
  nodeModule.register("data:text/javascript," + encodeURIComponent(loader));
} else {
  fail("Node.js 20.6 or newer is needed to run Copilot extensions");
}

// toJSONSchema turns a tool's parameters into JSON Schema: plain JSON
// Schema stays as it is; a zod schema (v3 _def, or v4 _zod.def) is read.
function toJSONSchema(p) {
  if (!p || typeof p !== "object") return {};
  if (typeof p.toJSONSchema === "function") {
    try {
      return p.toJSONSchema();
    } catch {}
  }
  const def = p._zod?.def || p._def;
  if (!def) return p; // plain JSON Schema
  // zod 4: def.type is the kind ("object"); zod 3: def.typeName ("ZodObject"), and an
  // array's def.type is its element
  const kind = String(typeof def.type === "string" ? def.type : def.typeName || "").replace(/^Zod/, "").toLowerCase();
  const desc = p.description || def.description;
  const withDesc = (s) => (desc ? { ...s, description: desc } : s);
  const inner = def.innerType || def.schema || def.in;
  switch (kind) {
    case "object": {
      const shape = typeof def.shape === "function" ? def.shape() : def.shape || {};
      const properties = {}, required = [];
      for (const [k, v] of Object.entries(shape)) {
        properties[k] = toJSONSchema(v);
        const vd = v?._zod?.def || v?._def || {};
        const vk = String(typeof vd.type === "string" ? vd.type : vd.typeName || "").replace(/^Zod/, "").toLowerCase();
        if (!["optional", "default", "nullable"].includes(vk) && !properties[k].__optional) required.push(k);
        delete properties[k].__optional;
      }
      return withDesc({ type: "object", properties, ...(required.length ? { required } : {}) });
    }
    case "string": {
      const s = { type: "string" };
      for (const c of def.checks || []) {
        const f = c.kind || c._zod?.def?.format || c.format;
        if (["email", "url", "uuid", "datetime", "date"].includes(f)) s.format = f === "datetime" ? "date-time" : f;
      }
      if (def.format) s.format = def.format === "datetime" ? "date-time" : def.format;
      return withDesc(s);
    }
    case "number":
      return withDesc({ type: (def.checks || []).some((c) => c.kind === "int" || c.isInt || c._zod?.def?.format === "safeint") ? "integer" : "number" });
    case "int":
    case "bigint":
      return withDesc({ type: "integer" });
    case "boolean":
      return withDesc({ type: "boolean" });
    case "date":
      return withDesc({ type: "string", format: "date-time" });
    case "enum":
      return withDesc({ type: "string", enum: Array.isArray(def.values) ? def.values : Object.values(def.entries || def.values || {}) });
    case "nativeenum":
      return withDesc({ type: "string", enum: Object.values(def.values || {}).filter((v) => typeof v === "string") });
    case "literal": {
      const v = def.value ?? (def.values || [])[0];
      return withDesc({ type: typeof v === "number" ? "number" : typeof v === "boolean" ? "boolean" : "string", enum: [v] });
    }
    case "array":
      return withDesc({ type: "array", items: toJSONSchema(typeof def.type === "object" ? def.type : def.element || def.items) });
    case "optional":
    case "nullable":
      return { ...toJSONSchema(inner), __optional: true, ...(desc ? { description: desc } : {}) };
    case "default": {
      const s = toJSONSchema(inner);
      const dv = typeof def.defaultValue === "function" ? def.defaultValue() : def.defaultValue;
      return { ...s, ...(dv !== undefined ? { default: dv } : {}), __optional: true, ...(desc ? { description: desc } : {}) };
    }
    case "effects":
    case "pipe":
    case "readonly":
    case "branded":
      return toJSONSchema(inner || def.out);
    case "union": {
      const opts = (def.options || []).map(toJSONSchema);
      const lits = opts.filter((o) => o.enum?.length === 1);
      if (lits.length === opts.length) return withDesc({ type: lits[0].type, enum: lits.map((o) => o.enum[0]) });
      return withDesc({ anyOf: opts });
    }
    case "record":
      return withDesc({ type: "object" });
  }
  return withDesc({});
}

const [mode, entry, toolName, argsJSON] = process.argv.slice(2);
try {
  if (!entry) throw new Error("usage: copilot-shim.mjs list|call <extension.mjs> [tool] [arguments]");
  await import(pathToFileURL(entry).href);
  for (let i = 0; i < 100 && !globalThis.__specwright?.options; i++) await new Promise((r) => setTimeout(r, 10));
  const opts = globalThis.__specwright?.options;
  if (!opts) throw new Error("the extension did not call joinSession()");
  const tools = (opts.tools || []).filter((t) => t && t.name);
  if (mode === "list") {
    const hooks = opts.hooks && typeof opts.hooks === "object" ? Object.keys(opts.hooks) : [];
    finish(JSON.stringify({ tools: tools.map((t) => ({ name: t.name, description: t.description || "", parameters: toJSONSchema(t.parameters) })), hooks }), 0);
  } else if (mode === "call") {
    const t = tools.find((x) => x.name === toolName);
    if (!t) throw new Error(`the extension has no tool named ${toolName}`);
    if (typeof t.handler !== "function") throw new Error(`${toolName} has no handler`);
    let args = {};
    if (argsJSON) args = JSON.parse(argsJSON);
    const invocation = { sessionId: "specwright", toolCallId: "specwright-" + Date.now(), toolName, arguments: args };
    const r = await t.handler(args, invocation);
    let text = "", failed = false;
    if (r == null) text = "";
    else if (typeof r === "string") text = r;
    else if (typeof r === "object" && "textResultForLlm" in r) {
      text = String(r.textResultForLlm ?? "");
      failed = ["failure", "denied", "rejected"].includes(r.resultType);
      if (r.error) text += (text ? "\n" : "") + r.error;
    } else text = JSON.stringify(r, null, 2);
    finish(text, failed ? 1 : 0);
  } else {
    throw new Error("unknown mode " + mode);
  }
} catch (e) {
  fail(e?.message || String(e));
}
