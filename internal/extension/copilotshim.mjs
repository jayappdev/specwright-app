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
    finish(JSON.stringify({ tools: tools.map((t) => ({ name: t.name, description: t.description || "", parameters: t.parameters || {} })), hooks }), 0);
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
