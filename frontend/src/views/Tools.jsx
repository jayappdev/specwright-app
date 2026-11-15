import { useEffect, useState } from "react";
import { backend } from "../backend.js";
import { EmptyState } from "../ui.jsx";
import { Pane } from "../Pane.jsx";
import { ModelPicker } from "../ModelPicker.jsx";
import { ContentView } from "../ContentView.jsx";
import { confirmDialog } from "../Dialog.jsx";
import { S, update, toast, errMsg, refreshExtensions, refreshSecrets, refreshSecretsSoon, refreshAdmin } from "../store.js";
import { Icon, Chip, Pill } from "../ui.jsx";
import { NeedWorkspace, StatusBadge } from "./Wizard.jsx";
import { BuiltinList, BuiltinView, builtinsVisible, selectBuiltin } from "./Builtins.jsx";
import { CopilotExtList, CopilotExtView, CopilotToolReview, approveCopilotTool, loadCopilotExts } from "./CopilotExts.jsx";
import { loadPackages, registryFor } from "./Packages.jsx";
import { AttachBar, AttachmentChips, DropZone, onPasteAttach } from "../Attachments.jsx";
import { Linkify } from "../FileLinks.jsx";
import { ToolTile, ToolGrid } from "../ToolTile.jsx";
import { AiProgress } from "../AiProgress.jsx";
import { parseSchema, schemaOfText, compact, paths as schemaPaths } from "../schema.js";

const GEN_PHASES = ["Reading your request", "Choosing a language", "Writing the script", "Describing the parameters", "Checking for secrets", "Finishing up"];
const TEST_PHASES = ["Starting the agent", "Reading the tool", "Choosing arguments", "Running the tool", "Checking the result"];

const STAGES = [["describe", "Describe"], ["review", "Review"], ["test", "Test with AI"], ["save", "Save"]];
const CONNECTS = [["none", "Nothing (local files)"], ["rest", "REST API"], ["db", "Database"], ["terminal", "Terminal / CLI"], ["other", "Other"]];
// Auto picks the first installed language in this order.
const PRIORITY = ["Node.js", "Go", "Java", "Bash"];
const RUNS_IN = ["Auto", ...PRIORITY, "Python"];
const MAX_FIX = 3;
const installed = () => S.info?.runtimes || PRIORITY;
const EXAMPLES = [
  "Find every TODO and FIXME in a package and list them with file and line",
  "Look up a Jira ticket by key and return its summary, status and acceptance criteria",
  "Run the unit tests for one package and report failures only",
  "Check the project's dependencies for known vulnerabilities",
];
const clone = (x) => JSON.parse(JSON.stringify(x));
const tbDefaults = () => ({ stage: "describe", editing: null, prompt: "", runsIn: "Auto", connects: ["none"], gen: null, draft: null, refine: "", testPrompt: "", attachments: [], test: null, scope: "project" });
// paths of the attachments that finished saving
const attPaths = (tb) => (tb.attachments || []).filter((a) => a.path && !a.saving).map((a) => a.path);
const fromPaths = (paths) => (paths || []).map((p) => ({ path: p, name: p.split("/").pop().replace(/^\d{8}-\d{6}-(\d+-)?/, ""), size: 0, kind: /\.(png|jpe?g|gif|webp|bmp|svg|heic)$/i.test(p) ? "image" : /\.pdf$/i.test(p) ? "pdf" : /\.(txt|md|json|csv|log|xml|ya?ml)$/i.test(p) ? "text" : "file" }));
const manualDraft = () => ({ name: "my-tool", desc: "", kind: "script", lang: "Node.js", params: [], secrets: [], perms: ["read files"], command: "node .codegen/tools/my-tool.js", script: "// my-tool: describe what it does\n", sampleTest: "", call: {}, revision: 1 });

export function newTool(manual) {
  S.bt = null; if (S.cx) S.cx.sel = "";
  S.tb = tbDefaults();
  if (manual) { S.tb.draft = manualDraft(); S.tb.stage = "review"; }
  S.view = "tools"; update();
}
// A managed (admin) tool opens read-only, unless admin mode is unlocked.
export function openTool(t) {
  S.bt = null; if (S.cx) S.cx.sel = "";
  const admin = !!t.managed && !!S.admin?.unlocked;
  S.tb = { ...tbDefaults(), editing: t.name, draft: clone(t), scope: t.scope, stage: "review", testPrompt: t.testPrompt || t.sampleTest || "", attachments: fromPaths(t.testFiles), prompt: t.prompt || "", test: t.lastTest || null,
    ro: !!t.managed && !admin, admin };
  S.view = "tools"; update(); refreshSecrets();
}
// Start a new managed tool (admin mode): built with the same steps, published to all projects.
// Delete a project tool: its files and record move to .codegen/archive/<datetime>/.
// A Copilot extension tool takes its whole extension with it.
export async function archiveTool(d) {
  const cop = d.kind === "copilot", ext = cop ? (d.dir || "").split("/").pop() : "";
  const siblings = cop ? (S.ws?.extensions || []).filter((t) => t.kind === "copilot" && t.dir === d.dir).map((t) => t.name) : [d.name];
  const msg = cop
    ? `This deletes the Copilot extension ${ext} from the project${siblings.length > 1 ? `, with all its tools (${siblings.join(", ")})` : ""}. The folder ${d.dir} moves to .codegen/archive/<date-time>/ in the project, so you can restore it; your own copy in ~/.copilot is not touched.`
    : `Its files and settings move to .codegen/archive/<date-time>/ in the project, so you can restore them. Agents and flows can no longer use it; flow steps that call it will need another tool.`;
  if (!(await confirmDialog(msg, { title: cop ? `Delete ${ext} from this project?` : `Delete ${d.name}?`, ok: "Delete", danger: true }))) return false;
  try {
    const r = await backend.ArchiveTool(d.name);
    toast(`Deleted ${(r.tools || [d.name]).join(", ")}. Archived in ${r.path}.`);
    S.tb = tbDefaults(); if (S.cx) S.cx.sel = "";
    await refreshExtensions(); loadCopilotExts(true);
    return true;
  } catch (e) { toast(errMsg(e), "error"); return false; }
}

export function newManagedTool(manual) {
  newTool(manual);
  S.tb.admin = true; S.tb.scope = "managed"; update();
}

async function generate(refining) {
  const tb = S.tb;
  if (!refining && !tb.prompt.trim()) return toast("Describe what the tool should do.");
  if (refining && !tb.refine.trim()) return toast("Say what to change.");
  tb.gen = { lines: [refining ? "Reading your change request" : "Understanding the request"] }; update();
  try {
    const d = await backend.GenerateExtension(refining ? { draft: tb.draft, change: tb.refine } : { prompt: tb.prompt, runsIn: tb.runsIn, connects: tb.connects });
    d.scope = tb.scope;
    tb.draft = d; tb.refine = ""; tb.test = null; tb.stage = "review";
    if (!refining) tb.testPrompt = d.sampleTest || d.testPrompt || "";
    refreshSecrets();
  } catch (e) { toast(errMsg(e), "error"); }
  tb.gen = null; update();
}

async function runTest() {
  const tb = S.tb;
  if (!tb.testPrompt.trim()) return toast("Write what you would ask the agent.");
  if ((tb.attachments || []).some((a) => a.saving)) return toast("Wait for the attachments to finish saving.");
  tb.test = { running: true, steps: [], checks: [] }; update();
  try { tb.test = await backend.TestExtensionWith(tb.draft, tb.testPrompt, attPaths(tb), tb.testModel || ""); }
  catch (e) { tb.test = { pass: false, steps: [{ k: "out", v: "error: " + errMsg(e) }], checks: [], summary: errMsg(e) }; }
  update();
}

// Ask the agent to fix the draft from the failed test, then test again;
// repeat until it passes or MAX_FIX attempts are used.
async function autoFix() {
  const tb = S.tb;
  tb.fix = { running: true, log: [] }; update();
  const say = (l) => { tb.fix.log.push(l); update(); };
  for (let a = 1; a <= MAX_FIX; a++) {
    say(`Attempt ${a} of ${MAX_FIX}: sending the failed test to the agent`);
    try {
      const d = await backend.GenerateExtension({ draft: tb.draft, failure: tb.test, question: tb.testPrompt, attachments: attPaths(tb), change: "Fix the tool so the test passes." });
      d.scope = tb.scope; tb.draft = d;
    } catch (e) { say("The agent could not fix it: " + errMsg(e)); break; }
    say(`Revision ${tb.draft.revision} written. Testing again…`);
    await runTest();
    if (tb.test.pass) { say(`Fixed: the test passed on attempt ${a}.`); break; }
    if (a === MAX_FIX) say(`Still failing after ${MAX_FIX} attempts. Review the script, or describe the fix under Review › Ask AI to change something.`);
  }
  tb.fix.running = false; update();
}

// Run the test; when it fails and "fix automatically" is on, start autoFix.
async function testAndMaybeFix() {
  const tb = S.tb;
  tb.fix = null;
  await runTest();
  const missing = (tb.draft?.secrets || []).some((s) => S.secretInfo[s.name]?.via === "missing");
  if (tb.test && !tb.test.pass && tb.autoFix !== false && !missing && !tb.ro) autoFix();
}

async function saveTool() {
  const tb = S.tb, d = tb.draft;
  if (!/^[a-z][a-z0-9_-]{0,39}$/.test(d.name)) { toast("Name must start with a letter and use lowercase letters, digits, - and _."); tb.stage = "review"; return update(); }
  if (tb.admin) {
    try {
      const rec = await backend.SaveManagedTool({ ...d, scope: "managed", testPrompt: tb.testPrompt, prompt: tb.prompt || d.prompt, lastTest: tb.test && !tb.test.running ? tb.test : null });
      toast(`${rec.name} v${rec.managed?.version || 1} published to all projects, signed by ${rec.managed?.signer || "you"}.`);
      S.tb = { ...tb, editing: rec.name, draft: clone(rec) };
      await refreshExtensions(); refreshAdmin();
    } catch (e) { toast(errMsg(e), "error"); }
    return;
  }
  try {
    const rec = d.kind === "npm" ? await backend.ApproveExtension(d.name)
      : await backend.SaveExtension({ ...d, scope: tb.scope, testPrompt: tb.testPrompt, testFiles: attPaths(tb), prompt: tb.prompt || d.prompt, lastTest: tb.test && !tb.test.running ? tb.test : null, status: tb.test?.pass ? "tested" : "untested" });
    toast(rec.status === "in review" ? `${rec.name} submitted to the corporate catalog for review.` : `${rec.name} saved${rec.scriptPath ? " to " + rec.scriptPath : ""} and approved for agents.`);
    S.tb = { ...tb, editing: rec.name, draft: clone(rec) };
    await refreshExtensions();
  } catch (e) { toast(errMsg(e), "error"); }
}

const DEPS_HINT = { "Node.js": "pg@8.13.1", Python: "psycopg[binary]==3.2.3", Go: "github.com/jackc/pgx/v5@v5.7.1", Java: "org.postgresql:postgresql:42.7.4" };
const entryFile = (lang) => ({ "Node.js": "index.js", Python: "main.py", Go: "main.go", Java: "Tool.java" })[lang] || "tool.sh";

function DepsCard({ d, scope }) {
  const hint = DEPS_HINT[d.lang], text = (d.deps || []).join("\n");
  if (!S.pkg) loadPackages();
  const reg = registryFor(d.lang);
  return (
    <div className="card"><div className="card-head"><h3>Dependencies</h3><span className="badge">{(d.deps || []).length}</span></div>
      {hint ? <>
        <p className="wf-desc">One package per line, with an exact version, e.g. <code className="inline-code">{hint}</code>. They are installed {scope === "managed" ? <>into the app's private copy of this managed tool (the same in every project)</> : <>into <code className="inline-code">.codegen/tools/{scope === "global" ? "global" : "project"}/{d.name}/</code></>} when you test or save, downloaded from <code className="inline-code">{reg || "the default registry"}</code> through the proxy set under Development wizard › Packages & proxy.</p>
        <textarea rows={3} spellCheck="false" style={{ fontFamily: "var(--font-mono)" }} placeholder={hint} value={text}
          onChange={(e) => { d.deps = e.target.value.split("\n").map((l) => l.trim()).filter(Boolean); update(); }} />
        <div className="row-actions"><button className="link" onClick={() => { S.view = "wizard"; S.wizSection = "w-packages"; update(); setTimeout(() => document.getElementById("w-packages")?.scrollIntoView({ block: "start" }), 50); }}>Edit proxy and registries</button></div>
      </> : <p className="muted">Bash tools cannot have dependencies. Choose Node.js, Go, Java or Python to use packages.</p>}
    </div>
  );
}

function ManagedBanner({ d }) {
  const m = d?.managed;
  if (!m) return null;
  const ok = m.status === "verified";
  return (
    <div className={"banner " + (ok ? "admin-banner" : "error")}>
      <Icon.lock /> {ok ? <>Managed by <b>{m.signer}</b> · v{m.version} · signature verified. You can run and test it in any project, but only an admin can change it.</>
        : <>This managed tool is blocked: {m.status}. It was changed outside admin mode or signed by a key this computer does not trust, so agents cannot run it. Ask your admin.</>}
    </div>
  );
}

const SOURCES = [
  ["auto", "Auto: session, environment, keychain, then ask me"],
  ["keychain", "System keychain only"],
  ["env", "Environment variable only"],
  ["ask", "Ask me each time the tool runs"],
  ["session", "Value entered here (this session)"],
];

function SecretBadge({ st }) {
  if (!st) return <span className="badge">…</span>;
  switch (st.via) {
    case "session": return <span className="badge badge-ok">set for this session</span>;
    case "env": return <span className="badge badge-ok" title={"Read from $" + st.env}>from ${st.env}</span>;
    case "keychain": return <span className="badge badge-ok" title="Stored in the system keychain (service SpecWright)">in the keychain</span>;
    case "ask": return <span className="badge badge-info">will ask when run</span>;
    default: return <span className="badge badge-warn">{st.source === "env" ? "$" + st.env + " not set" : "no value"}</span>;
  }
}

// Per-secret source: environment variable, ask at run time, or a session value.
function SecretsCard({ d }) {
  const setS = (s, k, v) => { s[k] = v; update(); refreshSecretsSoon(); };
  return (
    <div className="card"><h3>Secrets</h3>
      <p className="wf-desc">Passed to the tool as environment variables. Each one can be read from an environment variable on this computer, asked for when the tool runs, or entered here for this session. Values are never saved, sent to the model, or written to run history.</p>
      {d.secrets.length ? <ul className="lib-list secret-list">{d.secrets.map((s) => {
        const st = S.secretInfo[s.name], src = s.source || "auto";
        return (
          <li key={s.name}>
            <div className="secret-name"><code className="inline-code">{s.name}</code><small>{s.desc}</small></div>
            <div className="secret-controls">
              <select aria-label={"Where " + s.name + " comes from"} value={src} onChange={(e) => setS(s, "source", e.target.value === "auto" ? "" : e.target.value)}>{SOURCES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
              {(src === "auto" || src === "env") && <label className="secret-env"><span className="muted">$</span><input aria-label={"Environment variable for " + s.name} value={s.env || ""} placeholder={s.name} spellCheck="false" onChange={(e) => setS(s, "env", e.target.value.trim())} /></label>}
              {(src === "auto" || src === "session") && <input type="password" autoComplete="off" aria-label={"Value for " + s.name} placeholder={src === "auto" ? "value (optional)" : "value"} style={{ width: 170 }}
                onChange={(e) => { backend.SetSecret(s.name, e.target.value); refreshSecretsSoon(); }} />}
              {src === "ask" && S.secretInfo[s.name]?.via === "session" && <button className="link" onClick={() => { backend.SetSecret(s.name, ""); refreshSecretsSoon(); }}>Forget remembered value</button>}
              <SecretBadge st={st} />
            </div>
          </li>);
      })}</ul> : <p className="muted">This tool needs no secrets.</p>}
      <p className="muted" style={{ marginTop: 8 }}>Permissions: {(d.perms || []).map((p) => <span key={p} className="badge" style={{ marginRight: 4 }}>{p}</span>)}</p>
      <p className="muted" style={{ marginTop: 4 }}>Environment variables are read from the app and from your login shell (for example <code className="inline-code">~/.zshrc</code>), so values exported there work even when the app was opened from the Dock.</p>
    </div>
  );
}

export function Tools() {
  useEffect(() => { loadToolTags(); }, []);
  if (!S.ws) return <NeedWorkspace what="tools" />;
  if (!S.tb) S.tb = tbDefaults();
  const tb = S.tb, d = tb.draft, si = STAGES.findIndex(([id]) => id === tb.stage), exts = S.ws.extensions || [];
  const reach = (k) => tb.ro ? (k === 1 || k === 2) && d : k === 0 || (k <= 2 && d) || (k === 3 && d && (d.kind === "npm" || (tb.test && !tb.test.running)));
  const set = (k, v) => { tb[k] = v; update(); };
  const setD = (k, v) => { d[k] = v; update(); };
  // only secrets with no way to get a value block a test; "ask" ones prompt
  const missing = (d?.secrets || []).filter((s) => S.secretInfo[s.name]?.via === "missing");
  let body = null;

  if (tb.stage === "describe") {
    body = (
      <div className="card"><h3>What should the tool do?</h3><p className="wf-desc">Describe it in plain language. The AI writes the script, the parameters and the list of secrets it needs. You review everything before it is saved.</p>
        <textarea rows={4} placeholder={"e.g. " + EXAMPLES[0]} disabled={!!tb.gen} value={tb.prompt} onChange={(e) => set("prompt", e.target.value)} />
        <div className="chips" style={{ marginTop: 8 }}>{EXAMPLES.map((e) => <Chip key={e} onClick={() => set("prompt", e)}>{e.split(" ").slice(0, 5).join(" ")}…</Chip>)}</div>
        <div className="two-col" style={{ marginTop: 12 }}>
          <div><h4 style={{ marginTop: 0 }}>Connects to</h4><div className="chips">{CONNECTS.map(([v, l]) => <Chip key={v} on={tb.connects.includes(v)} onClick={() => {
            if (v === "none") tb.connects = ["none"];
            else { tb.connects = tb.connects.filter((c) => c !== "none"); tb.connects = tb.connects.includes(v) ? tb.connects.filter((c) => c !== v) : [...tb.connects, v]; if (!tb.connects.length) tb.connects = ["none"]; }
            update();
          }}>{l}</Chip>)}</div></div>
          <label className="field"><h4 style={{ marginTop: 0 }}>Runs in</h4>
            <select value={tb.runsIn} onChange={(e) => set("runsIn", e.target.value)}>{RUNS_IN.map((r) => <option key={r} value={r}>{r === "Auto" ? `Auto (${installed()[0] || "Bash"})` : installed().includes(r) || r === "Python" ? r : r + " (not installed)"}</option>)}</select>
            <small className="muted">Auto prefers {PRIORITY.join(" → ")} and uses the first one installed on this computer.</small></label>
        </div>
        <div className="row-actions"><button className="btn primary" disabled={!!tb.gen} onClick={() => generate(false)}>{tb.gen ? "Generating…" : "Generate tool with AI"}</button><button className="btn secondary" onClick={() => { tb.draft = manualDraft(); tb.stage = "review"; tb.test = null; update(); }}>Write the script myself</button></div>
        {tb.gen && <div className="transcript" style={{ minHeight: 0, marginTop: 10 }}><div className="transcript-inner">{tb.gen.lines.map((l, k) => <div className="row note" key={k}><Icon.check /><span>{l}</span></div>)}<AiProgress phases={GEN_PHASES} /></div></div>}
      </div>
    );
  } else if (tb.stage === "review" && d && d.kind === "copilot") {
    body = <CopilotToolReview d={d} onNext={() => set("stage", "test")} />;
  } else if (tb.stage === "save" && d && d.kind === "copilot") {
    body = <div className="card"><h3>{d.status === "needs approval" ? "Approve this Copilot extension tool?" : "Nothing to save"}</h3>
      <p className="wf-desc">{d.status === "needs approval" ? "Approving reads the extension's tools again from its current files and lets agents and flows use them." : <>Copilot extension tools are saved by their files in <code className="inline-code">{d.dir}</code>; it is approved and available to agents and flows.</>}</p>
      <div className="row-actions"><button className="btn secondary" onClick={() => set("stage", "test")}>Back</button>
        {d.status === "needs approval" && <button className="btn primary" onClick={() => approveCopilotTool(d)}>Approve for agents</button>}</div></div>;
  } else if (tb.stage === "review" && d) {
    const npm = d.kind === "npm";
    // the Review step in tabs, so the script is not 1,700 px down the page
    const RT = [["overview", "Overview", npm ? null : d.params.length], ["script", npm ? "Command" : "Script"], ["data", "Output shape"], ...(npm ? [] : [["deps", "Dependencies", (d.deps || []).length], ["secrets", "Secrets", (d.secrets || []).length]])];
    const rt = RT.some(([k]) => k === tb.rtab) ? tb.rtab : "overview";
    body = <><fieldset className="ro-fieldset" disabled={!!tb.ro}>
      <div className="tabs tl-rtabs" role="tablist" aria-label="Review the tool">{RT.map(([k, l, n]) => <button key={k} type="button" role="tab" className="tab" aria-selected={rt === k} onClick={() => set("rtab", k)}>{l}{n ? <span className="badge">{n}</span> : null}</button>)}</div>
      {rt === "overview" && <>
      <div className="card"><div className="card-head"><h3>{npm ? "npm script" : "Generated tool"}</h3><span className="badge">revision {d.revision || 1}</span><span className="badge badge-info">{d.lang || "Bash"}</span>{npm && <StatusBadge s={d.status} />}</div>
        <div className="two-col"><label className="field"><span className="muted">Name</span><input value={d.name} disabled={npm} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => setD("name", e.target.value)} /></label>
          <label className="field"><span className="muted">Description (what the agent sees)</span><input value={d.desc} onChange={(e) => setD("desc", e.target.value)} /></label></div>
        {npm && <p className="muted" style={{ marginTop: 8 }}>From <code className="inline-code">package.json</code> → <code className="inline-code">scripts["ext:{d.name}"]</code>. Agents can run it only after you approve it; editing the script in package.json withdraws the approval. Needs Node.js{S.info?.node ? "" : " (not found on PATH)"}.</p>}
      </div>
      {!npm && <div className="card"><h3>Parameters</h3><p className="wf-desc">The agent fills these when it calls the tool. Use <code className="inline-code">{"{{name}}"}</code> in the command.</p>
        <table className="table"><thead><tr><th>Name</th><th>Type</th><th>Required</th><th>Description</th><th /></tr></thead><tbody>
          {d.params.map((p, k) => (
            <tr key={k}><td><input aria-label="Parameter name" value={p.name} style={{ width: "100%", fontFamily: "var(--font-mono)" }} onChange={(e) => { p.name = e.target.value; update(); }} /></td>
              <td><input aria-label="Type" value={p.type} style={{ width: "100%" }} onChange={(e) => { p.type = e.target.value; update(); }} /></td>
              <td><input type="checkbox" aria-label="Required" checked={!!p.required} onChange={(e) => { p.required = e.target.checked; update(); }} /></td>
              <td><input aria-label="Description" value={p.desc} style={{ width: "100%" }} onChange={(e) => { p.desc = e.target.value; update(); }} /></td>
              <td><button className="link" onClick={() => { d.params.splice(k, 1); update(); }}>Remove</button></td></tr>
          )).flatMap((row, k) => [row, ...(JSON_TYPES.test(d.params[k].type || "") ? [<tr key={"s" + k} className="param-shape"><td colSpan={5}><ShapeEditor label={`Shape of ${d.params[k].name || "this parameter"}`} hint="Example JSON or a JSON Schema. The agent is given its fields, compactly, when it fills this parameter in." value={d.params[k].schema} onChange={(v) => { d.params[k].schema = v; update(); }} /></td></tr>] : [])])}
        </tbody></table><div className="row-actions"><button className="btn secondary" onClick={() => { d.params.push({ name: "", type: "string", required: false, desc: "" }); update(); }}>+ Add parameter</button></div></div>}
      </>}
      {rt === "data" && <OutputShapeCard d={d} test={tb.test} />}
      {rt === "deps" && !npm && <DepsCard d={d} scope={tb.scope} />}
      {rt === "secrets" && !npm && <SecretsCard d={d} />}
      {rt === "script" && <div className="card"><div className="card-head"><h3>{npm ? "Command" : "Script"}</h3><code className="inline-code">{d.command}</code></div>
        {!npm && (d.deps?.length ? <p className="muted" style={{ marginBottom: 8 }}>The app writes the command for tools with dependencies: it runs <code className="inline-code">{entryFile(d.lang)}</code> from the tool's folder, with the parameters in order.</p>
          : <label className="field" style={{ marginBottom: 8 }}><span className="muted">Command template</span><input value={d.command} style={{ fontFamily: "var(--font-mono)" }} onChange={(e) => setD("command", e.target.value)} /></label>)}
        <textarea className="editor" rows={12} spellCheck="false" style={{ minHeight: npm ? 60 : 200 }} value={d.script || ""} disabled={npm} onChange={(e) => setD("script", e.target.value)} />
        {!npm && <label className="field" style={{ marginTop: 10 }}><span className="muted">Ask AI to change something</span>
          <div className="setup-input"><input value={tb.refine} placeholder="e.g. add a 30 second timeout, also scan .kt files" onChange={(e) => set("refine", e.target.value)} /><button className="btn secondary" disabled={!!tb.gen} onClick={() => generate(true)}>{tb.gen ? "Updating…" : "Update"}</button></div></label>}
        {tb.gen && <AiProgress phases={["Reading the change", "Updating the script", "Checking the parameters", "Finishing up"]} />}</div>}
      <div className="row-actions tl-review-actions"><button className="btn secondary" onClick={() => set("stage", "describe")}>Back</button><button className="btn primary" onClick={() => set("stage", npm ? "save" : "test")}>{npm ? "Next: approve" : "Next: test with AI"}</button>
        {tb.editing && !tb.ro && !tb.admin && <><span className="status-spacer" />{d?.kind !== "npm" && d?.scope !== "global" && <button className="btn danger" onClick={() => archiveTool(d)}><Icon.x /> Delete tool</button>}</>}</div>
    </fieldset></>;
  } else if (tb.stage === "test" && d) {
    const t = tb.test;
    body = <>
      <DropZone list={tb.attachments} className="card att-zone"><h3>Test with AI</h3><p className="wf-desc">Ask something a developer would ask, and attach screenshots, PDFs or text if they help. The agent is offered only <code className="inline-code">{d.name}</code>, decides whether to use it, fills the arguments, runs it, and the result is checked.</p>
        <div className="setup-input"><input value={tb.testPrompt} placeholder={d.sampleTest} onPaste={onPasteAttach(tb.attachments)} onChange={(e) => set("testPrompt", e.target.value)} /><button className="btn primary" disabled={t?.running || tb.fix?.running} onClick={testAndMaybeFix}>{t?.running ? "Testing…" : t ? "Test again" : "Run test"}</button></div>
        <AttachBar list={tb.attachments} disabled={t?.running} insertText={(x) => set("testPrompt", (tb.testPrompt ? tb.testPrompt + " " : "") + x)} />
        <div className="tb-model"><ModelPicker value={tb.testModel || ""} onChange={(v) => set("testModel", v)} label="Test with" disabled={t?.running} /></div>
        <label className="check" style={{ marginTop: 8 }}><input type="checkbox" checked={tb.autoFix !== false} onChange={(e) => set("autoFix", e.target.checked)} /> Fix automatically with AI when the test fails (up to {MAX_FIX} attempts)</label>
        {missing.length > 0 && <p className="muted" style={{ marginTop: 8, color: "var(--warn)" }}><Icon.warn /> {missing.map((s) => s.name).join(", ")} has no value. Set the environment variable, enter it under Review › Secrets, or switch it to “Ask me”.</p>}
        {(d.secrets || []).some((s) => S.secretInfo[s.name]?.via === "ask") && <p className="muted" style={{ marginTop: 8 }}>You will be asked for {(d.secrets || []).filter((s) => S.secretInfo[s.name]?.via === "ask").map((s) => s.name).join(", ")} when the tool runs.</p>}
      </DropZone>
      {t && <div className="transcript" style={{ minHeight: 0 }}><div className="transcript-inner">
        <div className="row note"><Icon.check /><span>You: {tb.testPrompt}</span></div>
        {(t.steps || []).map((s, k) => s.k === "attach" ? <div key={k} className="row"><div className="muted">Attached</div><AttachmentChips list={fromPaths(s.v.split("\n")).map((a) => (tb.attachments || []).find((x) => x.path === a.path) || a)} /></div>
          : s.k === "answer" ? <div key={k} className="row assistant"><div className="muted">Agent's answer</div><Linkify text={s.v} /></div>
          : s.k === "say" ? <div key={k} className="row assistant"><Linkify text={s.v} /></div>
          : s.k === "call" ? <div key={k} className="row"><div className="muted">Tool call</div><pre className="code"><Linkify text={s.v} /></pre></div>
          : s.k === "install" ? <div key={k} className="row"><div className="muted">Installing dependencies</div><pre className="tool-result"><Linkify text={s.v} /></pre></div>
          : s.k === "run" ? <div key={k} className="row system"><Linkify text={s.v} /></div>
          : <div key={k} className="row"><div className="muted">Output</div><ContentView title={(d?.name || "tool") + " output"} text={s.v} /></div>)}
        {t.running && <AiProgress phases={TEST_PHASES} />}
      </div></div>}
      {t && !t.running && <div className="card" style={{ borderColor: `var(${t.pass ? "--ok" : "--error"})` }}><div className="card-head"><h3>{t.pass ? "Test passed" : "Test failed"}</h3><Pill s={t.pass ? "done" : "failed"} label={t.pass ? "Passed" : "Failed"} /></div>
        <ul className="checklist">{(t.checks || []).map((c) => <li key={c.label} className={c.ok ? "ok" : ""}>{c.ok ? <Icon.check /> : <Icon.x />}<span>{c.label}</span></li>)}</ul><p className="muted"><Linkify text={t.summary} /></p>
        {!t.pass && !tb.fix?.running && <div className="sub-row">
          <span>{missing.length ? <>Give {missing.map((s) => s.name).join(", ")} a value first (environment variable, session value or “Ask me”): the AI cannot supply secrets.</> : <><b>Let the AI fix it</b> <span className="muted">· sends the failed checks and output to the agent, applies its fix and tests again (up to {MAX_FIX} attempts)</span></>}</span>
          {!tb.ro && <button className="btn primary" disabled={missing.length > 0} onClick={autoFix}>Fix with AI</button>}</div>}</div>}
      {tb.fix && <div className="card"><div className="card-head"><h3>Automatic fix</h3>{tb.fix.running ? <span className="spinner" /> : <span className={"badge " + (t?.pass ? "badge-ok" : "badge-warn")}>{t?.pass ? "fixed" : "not fixed"}</span>}</div>
        <ul className="lib-list">{tb.fix.log.map((l, k) => <li key={k}>{l}</li>)}</ul>
        {!tb.fix.running && <div className="row-actions"><button className="link" onClick={() => set("stage", "review")}>Review the changed script</button></div>}</div>}
      <div className="row-actions"><button className="btn secondary" onClick={() => set("stage", "review")}>Back to review</button>{!tb.ro && <button className="btn primary" disabled={!(t && !t.running)} onClick={() => set("stage", "save")}>{tb.admin ? "Next: publish" : "Next: save"}</button>}</div>
    </>;
  } else if (tb.stage === "save" && d && tb.admin) {
    const passed = tb.test?.pass, v = d.managed?.version;
    body = (
      <div className="card admin-card"><div className="card-head"><Icon.lock /><h3>Publish as a managed tool</h3><span className="badge badge-warn">admin</span></div>
        <p className="wf-desc">Managed tools are global: every project in this app can use them, and regular users can run and test them but never change, replace or remove them. The tool is signed with your admin key; any edit made outside admin mode breaks the signature and the app stops running it.</p>
        <dl className="kv" style={{ marginTop: 12 }}><dt>Name</dt><dd><code>{d.name}</code></dd><dt>Version</dt><dd>{v ? `v${v} → v${v + 1}` : "v1 (new)"}</dd><dt>Runtime</dt><dd>{d.lang || "Bash"}</dd>
          <dt>Secrets</dt><dd>{(d.secrets || []).map((x) => x.name).join(", ") || "none"} <span className="muted">(each user supplies values from their environment or when asked)</span></dd>
          <dt>Test</dt><dd>{passed ? <><span className="badge badge-ok">passed</span> {tb.testPrompt}</> : <span className="badge badge-warn">not passed</span>}</dd>
          <dt>Signed by</dt><dd>{S.admin?.name} <code className="inline-code">{S.admin?.keyId}</code></dd></dl>
        {!passed && <div className="banner" style={{ marginTop: 10, borderColor: "var(--warn)" }}>Managed tools must pass a test before they are published.</div>}
        {!S.admin?.unlocked && <div className="banner error" style={{ marginTop: 10 }}>Admin mode is locked. Unlock it on the Admin screen to publish.</div>}
        <div className="row-actions"><button className="btn secondary" onClick={() => set("stage", "test")}>Back</button>
          <button className="btn primary" disabled={!passed || !S.admin?.unlocked} onClick={saveTool}>{v ? "Publish new version" : "Publish to all projects"}</button></div>
      </div>
    );
  } else if (tb.stage === "save" && d) {
    const passed = tb.test?.pass, npm = d.kind === "npm";
    body = (
      <div className="card"><h3>{npm ? "Approve this npm script?" : "Where should this tool live?"}</h3>
        {!npm && <div className="setup-choices" role="radiogroup" aria-label="Scope" style={{ marginTop: 8 }}>
          <button type="button" role="radio" aria-checked={tb.scope === "project"} className={"setup-choice" + (tb.scope === "project" ? " active" : "")} onClick={() => set("scope", "project")}><strong>Project-specific</strong><span className="muted">Only this project. Saved to <code>.codegen/tools/project/</code> and listed in <code>.codegen/extensions.json</code>.</span></button>
          <button type="button" role="radio" aria-checked={tb.scope === "global"} className={"setup-choice" + (tb.scope === "global" ? " active" : "")} onClick={() => set("scope", "global")}><strong>Global (corporate)</strong><span className="muted">Every project can use it once a library owner reviews it in the corporate catalog.</span></button>
        </div>}
        <dl className="kv" style={{ marginTop: 12 }}><dt>Name</dt><dd><code>{d.name}</code></dd><dt>Runtime</dt><dd>{d.lang || "Bash"}</dd>
          <dt>Parameters</dt><dd>{(d.params || []).map((p) => p.name + (p.required ? "" : "?")).join(", ") || "none"}</dd>
          <dt>Secrets</dt><dd>{(d.secrets || []).map((s) => s.name).join(", ") || "none"} <span className="muted">(names only; values stay on this computer)</span></dd>
          <dt>Test</dt><dd>{passed ? <><span className="badge badge-ok">passed</span> {tb.testPrompt}</> : <span className="badge badge-warn">not passed</span>}</dd>
          <dt>Approval</dt><dd>Saving approves this exact {npm ? "command" : "script"} for agents. Any later change needs a new approval.</dd></dl>
        {!passed && !npm && <div className="banner" style={{ marginTop: 10, borderColor: "var(--warn)" }}>The tool has not passed a test. {tb.scope === "global" ? "Global tools must pass a test before they can be submitted." : "You can still save it to this project, marked untested."}</div>}
        <div className="row-actions"><button className="btn secondary" onClick={() => set("stage", npm ? "review" : "test")}>Back</button>
          <button className="btn primary" disabled={!npm && !passed && tb.scope === "global"} onClick={saveTool}>{npm ? "Approve for agents" : tb.scope === "global" ? "Submit to global catalog" : tb.editing ? "Update project tool" : "Save to project"}</button></div>
      </div>
    );
  }

  return (
    <div className="files">
      <Pane k="tools" v="--pane-l" pane=".ftree" min={200} max={560} label="Resize the tool list" />
      <nav className="ftree" aria-label="Tools">
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "0 8px 8px" }}><h1>Tools</h1><button className="btn primary" onClick={() => newTool(false)}>+ New</button></div>
        <ToolFinder />
        {finding() ? <FoundTools exts={exts} /> : <>
        <BuiltinList />
        {[["managed", "Managed by admin · all projects"], ["project", "Project tools"], ["global", "Global tools"]].map(([g, l]) => (
          <ToolGrid key={g} title={l} compact tools={withTags(exts.filter((t) => t.scope === g))} render={(t) => <ToolTile key={t.name} t={t} compact active={S.tb?.editing === t.name} onOpen={() => openTool(t)} />} />))}
        <CopilotExtList /></>}
      </nav>
      <div className="fview">
        {(S.bt?.sel && builtinsVisible() && !S.cx?.sel) ? <ToolTagsBar name={"builtin:" + S.bt.sel} /> : null}
        {S.cx?.sel ? <CopilotExtView /> : S.bt?.sel && builtinsVisible() ? <BuiltinView /> : <>
        <header className="view-head"><h1>{tb.admin ? (tb.editing ? "Managed tool · " + tb.editing : "New managed tool") : tb.ro ? "Managed tool · " + tb.editing : tb.editing ? (d?.kind === "copilot" ? "Copilot tool · " : "Edit tool · ") + tb.editing : "New tool"}</h1>
          {(tb.admin || tb.ro) && <span className={"badge " + (tb.admin ? "badge-warn" : "")}><Icon.lock /> {tb.admin ? "admin mode" : "read-only"}</span>}</header>
        {tb.editing && <ToolTagsBar name={tb.editing} />}
        {tb.ro && <ManagedBanner d={d} />}
        {tb.admin && !tb.editing && <div className="banner admin-banner"><Icon.lock /> Admin mode: this tool will be signed and published to every project in the app.</div>}
        <div className="stepstrip">{STAGES.map(([id, l], k) => <span key={id} style={{ display: "contents" }}>
          <button className={"stepchip" + (k === si ? " st-running" : "")} disabled={!reach(k)} aria-current={k === si ? "step" : undefined} style={{ cursor: "pointer", ...(k < si ? { borderColor: "var(--ok)" } : {}) }} onClick={() => set("stage", id)}>
            <span className={"card-index" + (k < si ? " done" : "")}>{k < si ? <Icon.check /> : k + 1}</span><span className="stepchip-title">{l}</span></button>
          {k < STAGES.length - 1 && <span className="muted">→</span>}</span>)}</div>
        {body}
        </>}
      </div>
    </div>
  );
}


// --- data shapes: what a tool prints, and the shape of JSON parameters ---------------

const JSON_TYPES = /^(json|object|array)$/i;

// Example JSON or a JSON Schema in; the shape (and its fields) shown.
function ShapeEditor({ label, hint, value, onChange }) {
  const [text, setText] = useState(value ? JSON.stringify(value, null, 2) : "");
  const [err, setErr] = useState("");
  useEffect(() => { setText(value ? JSON.stringify(value, null, 2) : ""); }, [JSON.stringify(value || null)]);
  const apply = () => { try { const s = parseSchema(text); setErr(""); onChange(s || undefined); } catch (e) { setErr(e.message); } };
  return <div className="shape-editor">
    <label className="field"><span className="muted">{label}</span>
      <textarea rows={value ? 6 : 3} spellCheck="false" style={{ fontFamily: "var(--font-mono)" }} value={text} placeholder={'{"id": "A-1001", "total": 120.5, "items": [{"sku": "KB-01", "qty": 2}]}'} onChange={(e) => setText(e.target.value)} onBlur={apply} />
      {hint && <small className="muted">{hint}</small>}</label>
    {err && <div className="banner error">{err}</div>}
    {value && <code className="fl-compact">{compact(value)}</code>}
  </div>;
}

function OutputShapeCard({ d, test }) {
  const out = !test?.running ? test?.output || d.lastTest?.output || "" : "";
  const learned = out.trim() && /^[[{]/.test(out.trim()) ? schemaOfText(out) : null;
  const fieldsOf = d.output ? schemaPaths(d.output, 200) : [];
  return <div className="card"><h3>Output shape</h3>
    <p className="wf-desc">When the tool prints JSON, describe its shape: the Flow designer then shows its fields to the steps after it, suggests them in filters, decisions and aggregates, and checks the paths flows use. It is metadata for the designer only and is never sent to the AI.</p>
    <ShapeEditor label="What the tool prints (example JSON or a JSON Schema)" value={d.output} onChange={(v) => { d.output = v; update(); }} />
    {learned && <div className="row-actions"><button className="btn secondary" onClick={() => { d.output = learned; update(); toast("Learned the output shape from the last test. Add descriptions to the fields if you like."); }}>{d.output ? "Learn it again from the last test" : "Learn it from the last test"}</button>
      <span className="muted">{compact(learned)}</span></div>}
    {fieldsOf.length > 0 && <details><summary className="muted">{fieldsOf.length} field{fieldsOf.length === 1 ? "" : "s"}</summary>
      <ul className="shape-fields">{fieldsOf.map((p) => <li key={p.path} style={{ paddingLeft: p.depth * 14 }}><code>{p.path}</code> <span className="fl-shape-type">{p.type}</span>{p.description && <span className="muted"> · {p.description}</span>}</li>)}</ul></details>}
  </div>;
}


// --- finding tools: search, tags, grouping --------------------------------------------

const tf = () => (S.tf ||= { q: "", tag: "", groupBy: "source" });
const finding = () => { const f = tf(); return !!(f.q.trim() || f.tag || f.groupBy === "tag"); };

async function loadToolTags() {
  if (S.toolTags) return;
  S.toolTags = (await backend.ToolTags().catch(() => null)) || {};
  update();
}
const tagsOf = (key) => (S.toolTags || {})[key] || [];
const withTags = (list) => list.map((t) => ({ ...t, tags: tagsOf(t.name) }));
const allTags = () => [...new Set(Object.values(S.toolTags || {}).flat())].sort();

function ToolFinder() {
  const f = tf(), set = (k, v) => { f[k] = v; update(); }, tags = allTags();
  return <div className="tf-bar">
    <input type="search" value={f.q} placeholder="Search tools: name, what it does, #tag" aria-label="Search tools" onChange={(e) => set("q", e.target.value)} />
    <div className="tf-row"><span className="muted">Group by</span>
      <div className="seg" role="group" aria-label="Group by">{[["source", "Source"], ["tag", "Tag"]].map(([v, l]) => <button key={v} type="button" aria-pressed={f.groupBy === v} onClick={() => set("groupBy", v)}>{l}</button>)}</div></div>
    {tags.length > 0 && <div className="tf-tags">{tags.map((t) => <button key={t} type="button" className={"tt-tag tf-tag" + (f.tag === t ? " on" : "")} aria-pressed={f.tag === t} onClick={() => set("tag", f.tag === t ? "" : t)}>#{t}</button>)}</div>}
  </div>;
}

// every tool (built-in, managed, project, global, Copilot) matching the
// search and tag, grouped by source or by tag
function FoundTools({ exts }) {
  const f = tf(), q = f.q.trim().toLowerCase().replace(/^#/, "");
  const items = [
    ...(builtinsVisible() ? S.builtins || [] : []).map((t) => ({ name: t.name, key: "builtin:" + t.name, desc: t.description, kind: "builtin", scope: "builtin", status: t.enabled === false ? "off" : "built-in", open: () => { if (S.cx) S.cx.sel = ""; selectBuiltin(t.name); } })),
    ...exts.map((t) => ({ ...t, key: t.name, open: () => { if (S.bt) S.bt.sel = ""; if (S.cx) S.cx.sel = ""; openTool(t); } })),
  ].map((t) => ({ ...t, tags: tagsOf(t.key) }))
    .filter((t) => (!f.tag || t.tags.includes(f.tag)) && (!q || (t.name + " " + (t.desc || "") + " " + t.tags.join(" ")).toLowerCase().includes(q)));
  const SRC = [["builtin", "Built-in"], ["managed", "Managed by admin"], ["project", "Project tools"], ["global", "Global tools"]];
  const groups = f.groupBy === "tag"
    ? [...allTags().filter((t) => !f.tag || t === f.tag).map((t) => ["#" + t, items.filter((x) => x.tags.includes(t))]), ["Not tagged", items.filter((x) => !x.tags.length)]]
    : [...SRC.map(([s, l]) => [l, items.filter((x) => x.scope === s)]), ["Other", items.filter((x) => !SRC.some(([s]) => s === x.scope))]];
  const shown = groups.filter(([, l]) => l.length);
  return <>{shown.map(([l, list]) => <ToolGrid key={l} title={l} compact tools={list} render={(t) => <ToolTile key={t.key} t={t} compact active={S.tb?.editing === t.name || (t.kind === "builtin" && S.bt?.sel === t.name)} onOpen={t.open} />} />)}
    {!shown.length && <EmptyState compact title={`No tools match${q ? ` “${f.q}”` : ""}${f.tag ? ` with #${f.tag}` : ""}`} actions={[(q || f.tag) && ["Clear the search", () => { f.q = ""; f.tag = ""; update(); }], ["Create a tool", () => newTool(false)]]} />}</>;
}

// The open tool's tags.
function ToolTagsBar({ name }) {
  const tags = tagsOf(name), [text, setText] = useState("");
  const save = async (next) => { try { S.toolTags = await backend.SetToolTags(name, next); update(); } catch (e) { toast(errMsg(e), "error"); } };
  const add = () => { const t = text.split(",").map((x) => x.trim().replace(/^#/, "")).filter(Boolean); if (t.length) save([...tags, ...t]); setText(""); };
  return <div className="tt-tagbar"><span className="muted">Tags</span>
    {tags.map((t) => <span key={t} className="tt-tag">#{t}<button className="link" aria-label={"Remove tag " + t} onClick={() => save(tags.filter((x) => x !== t))}>✕</button></span>)}
    <input value={text} list="tool-tag-list" placeholder={tags.length ? "add a tag" : "add tags, e.g. database, reporting"} aria-label="Add a tag" onChange={(e) => setText(e.target.value)}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === ",") { e.preventDefault(); add(); } }} onBlur={add} />
    <datalist id="tool-tag-list">{allTags().filter((t) => !tags.includes(t)).map((t) => <option key={t} value={t} />)}</datalist>
  </div>;
}
