import { registerPlace } from "../navigation.js";
import { ToolTester } from "./tools/ToolTester.jsx";
import { DecisionLog, openToolInPlayground } from "./tools/DecisionLog.jsx";
import { genTurns, testText } from "./tools/toolHistory.js";
import { useEffect } from "react";
import { ShareMenu, exportTools } from "./tools/Share.jsx";
import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { ModelPicker } from "../ModelPicker.jsx";
import { ContentView } from "../ContentView.jsx";
import { confirmDialog, askText } from "../Dialog.jsx";
import { HeadingPicker, loadLayout } from "../listLayout.jsx";
import { S, update, toast, errMsg, refreshExtensions, refreshSecrets, refreshAdmin } from "../store.js";
import { Icon, Chip, Pill } from "../ui.jsx";
import { NeedWorkspace, StatusBadge } from "./Wizard.jsx";
import { BuiltinList, BuiltinView, builtinsVisible } from "./Builtins.jsx";
import {
  CopilotExtList,
  CopilotExtView,
  CopilotToolReview,
  approveCopilotTool,
  loadCopilotExts,
} from "./CopilotExts.jsx";
import { AttachBar, AttachmentChips, DropZone, onPasteAttach } from "../Attachments.jsx";
import { Linkify } from "../FileLinks.jsx";
import { ToolTile, ToolGrid } from "../ToolTile.jsx";
import { AiProgress } from "../AiProgress.jsx";
import { DepsCard, ManagedBanner, SecretsCard, entryFile } from "./tools/Cards.jsx";
import { JSON_TYPES, OutputShapeCard, ShapeEditor } from "./tools/Shapes.jsx";
import { FoundTools, ToolFinder, ToolTagsBar, finding, loadToolTags, withTags } from "./tools/Finder.jsx";

const GEN_PHASES = [
  "Reading your request",
  "Choosing a language",
  "Writing the script",
  "Describing the parameters",
  "Checking for secrets",
  "Finishing up",
];
const TEST_PHASES = [
  "Starting the agent",
  "Reading the tool",
  "Choosing arguments",
  "Running the tool",
  "Checking the result",
];

// the kinds of parameter the Review table offers: type | format
const PARAM_KINDS = [
  ["string|", "text"],
  ["string|multiline", "long text"],
  ["string|path", "file path"],
  ["string|directory", "folder"],
  ["string|url", "URL"],
  ["string|date", "date"],
  ["string|date-time", "date and time"],
  ["string|email", "email"],
  ["string|password", "password"],
  ["integer|", "whole number"],
  ["number|", "number"],
  ["boolean|", "yes / no"],
  ["json|", "JSON"],
  ["object|", "JSON object"],
  ["array|", "JSON list"],
];

const STAGES = [
  ["describe", "Describe"],
  ["review", "Review"],
  ["test", "Test"],
  ["save", "Save"],
];
const CONNECTS = [
  ["none", "Nothing (local files)"],
  ["rest", "REST API"],
  ["db", "Database"],
  ["terminal", "Terminal / CLI"],
  ["other", "Other"],
];
// Auto picks the first installed language in this order.
const PRIORITY = ["Node.js", "Go", "Java", "Bash"];
const RUNS_IN = ["Auto", ...PRIORITY, "Python"];
const MAX_FIX = 3;
export const installed = () => S.info?.runtimes || PRIORITY;
const EXAMPLES = [
  "Find every TODO and FIXME in a package and list them with file and line",
  "Look up a Jira ticket by key and return its summary, status and acceptance criteria",
  "Run the unit tests for one package and report failures only",
  "Check the project's dependencies for known vulnerabilities",
];
const clone = (x) => JSON.parse(JSON.stringify(x));
const tbDefaults = () => ({
  stage: "describe",
  editing: null,
  prompt: "",
  runsIn: "Auto",
  connects: ["none"],
  gen: null,
  draft: null,
  refine: "",
  fixAsk: "",
  testPrompt: "",
  attachments: [],
  test: null,
  scope: "project",
});
// paths of the attachments that finished saving
const attPaths = (tb) => (tb.attachments || []).filter((a) => a.path && !a.saving).map((a) => a.path);
const fromPaths = (paths) =>
  (paths || []).map((p) => ({
    path: p,
    name: p
      .split("/")
      .pop()
      .replace(/^\d{8}-\d{6}-(\d+-)?/, ""),
    size: 0,
    kind: /\.(png|jpe?g|gif|webp|bmp|svg|heic)$/i.test(p)
      ? "image"
      : /\.pdf$/i.test(p)
        ? "pdf"
        : /\.(txt|md|json|csv|log|xml|ya?ml)$/i.test(p)
          ? "text"
          : "file",
  }));
const manualDraft = () => ({
  name: "my-tool",
  desc: "",
  kind: "script",
  lang: "Node.js",
  params: [],
  secrets: [],
  perms: ["read files"],
  command: "node .codegen/tools/my-tool.js",
  script: "// my-tool: describe what it does\n",
  sampleTest: "",
  call: {},
  revision: 1,
});

export function newTool(manual) {
  S.bt = null;
  if (S.cx) S.cx.sel = "";
  S.tb = tbDefaults();
  if (manual) {
    S.tb.draft = manualDraft();
    S.tb.stage = "review";
  }
  S.view = "tools";
  update();
}
// A managed (admin) tool opens read-only, unless admin mode is unlocked.
export function openTool(t) {
  S.bt = null;
  if (S.cx) S.cx.sel = "";
  const admin = !!t.managed && !!S.admin?.unlocked;
  S.tb = {
    history: S.toolHist?.[t.name] || [],
    ...tbDefaults(),
    editing: t.name,
    draft: clone(t),
    scope: t.scope,
    stage: "review",
    testPrompt: t.testPrompt || t.sampleTest || "",
    attachments: fromPaths(t.testFiles),
    prompt: t.prompt || "",
    test: t.lastTest || null,
    ro: !!t.managed && !admin,
    admin,
  };
  S.view = "tools";
  update();
  refreshSecrets();
}
// Start a new managed tool (admin mode): built with the same steps, published to all projects.
// Delete a project tool: its files and record move to .codegen/archive/<datetime>/.
// A Copilot extension tool takes its whole extension with it.
export async function archiveTool(d) {
  const cop = d.kind === "copilot",
    ext = cop ? (d.dir || "").split("/").pop() : "";
  const siblings = cop
    ? (S.ws?.extensions || []).filter((t) => t.kind === "copilot" && t.dir === d.dir).map((t) => t.name)
    : [d.name];
  const msg = cop
    ? `This deletes the Copilot extension ${ext} from the project${siblings.length > 1 ? `, with all its tools (${siblings.join(", ")})` : ""}. The folder ${d.dir} moves to .codegen/archive/<date-time>/ in the project, so you can restore it; your own copy in ~/.copilot is not touched.`
    : `Its files and settings move to .codegen/archive/<date-time>/ in the project, so you can restore them. Agents and flows can no longer use it; flow steps that call it will need another tool.`;
  if (
    !(await confirmDialog(msg, {
      title: cop ? `Delete ${ext} from this project?` : `Delete ${d.name}?`,
      ok: "Delete",
      danger: true,
    }))
  )
    return false;
  try {
    const r = await backend.ArchiveTool(d.name);
    toast(`Deleted ${(r.tools || [d.name]).join(", ")}. Archived in ${r.path}.`);
    S.tb = tbDefaults();
    if (S.cx) S.cx.sel = "";
    await refreshExtensions();
    loadCopilotExts(true);
    return true;
  } catch (e) {
    toast(errMsg(e), "error");
    return false;
  }
}

export function newManagedTool(manual) {
  newTool(manual);
  S.tb.admin = true;
  S.tb.scope = "managed";
  update();
}

// histOf: this tool's history (kept per tool name for the session, so
// reopening the tool keeps it).
function histOf(tb) {
  tb.history ||= [];
  const name = tb.draft?.name || tb.editing;
  if (name) (S.toolHist ||= {})[name] = tb.history;
  return tb.history;
}

// revise: ask the agent for a new draft (or revision) and record how it
// came about in the history; the request carries the history so far.
async function revise(req, kind, extra = {}) {
  const tb = S.tb;
  const before = tb.draft?.script || tb.draft?.command || "";
  const tr = await backend.GenerateExtensionTrace({
    ...req,
    model: tb.genModel || "",
    history: req.draft ? genTurns(histOf(tb)) : [],
  });
  const d = tr.tool;
  d.scope = tb.scope;
  tb.draft = d;
  histOf(tb).push({
    kind,
    at: new Date().toISOString(),
    revision: d.revision,
    ask: extra.ask || "",
    prompt: tr.prompt,
    reply: tr.reply,
    why: tr.why || "",
    model: tr.model || "",
    ms: tr.ms || 0,
    before,
    after: d.script || d.command || "",
    attempt: extra.attempt || 0,
  });
  return d;
}

// noteTest: the test after the latest revision goes into the history.
function noteTest(tb) {
  const t = tb.test;
  if (!t || t.running) return;
  const h = histOf(tb),
    last = h[h.length - 1];
  const entry = { test: t, testText: testText(t), question: tb.testPrompt };
  if (last && last.kind !== "test" && !last.test) Object.assign(last, entry);
  else h.push({ kind: "test", at: new Date().toISOString(), revision: tb.draft?.revision || 0, ...entry });
}

// applyToolRevision: a revision the AI gave in the Prompt Playground
// becomes the draft (of the tool that conversation is about), goes into
// its history and is tested again.
export async function applyToolRevision(tr, name, runId) {
  let tb = S.tb;
  if (!tb?.draft || (tb.draft.name !== name && tb.editing !== name)) {
    const t = (S.ws?.extensions || []).find((x) => x.name === name);
    if (!t) return toast(`Open the tool ${name} in Tools first.`, "error");
    openTool(t);
    tb = S.tb;
  }
  const before = tb.draft.script || tb.draft.command || "";
  const d = tr.tool;
  d.scope = tb.scope;
  tb.draft = d;
  histOf(tb).push({
    kind: "playground",
    at: new Date().toISOString(),
    revision: d.revision,
    ask: "",
    reply: tr.reply,
    why: tr.why || "",
    before,
    after: d.script || d.command || "",
    run: runId,
  });
  tb.fix = null;
  tb.direct = false;
  tb.stage = "test";
  tb.testTab = "ai";
  S.view = "tools";
  update();
  if (tb.testPrompt.trim()) await runTest();
}

// reloadTool: read a saved tool again from its files (after the Playground
// agent edited them), keeping this session's history.
export async function reloadTool(name) {
  await refreshExtensions();
  const t = (S.ws?.extensions || []).find((x) => x.name === name);
  if (!t) return toast(`${name} is not a saved tool of this project.`, "error");
  const before = S.tb?.draft?.name === name ? S.tb.draft.script || S.tb.draft.command || "" : null;
  openTool(t);
  const tb = S.tb;
  if (before !== null && before !== (t.script || t.command || ""))
    histOf(tb).push({
      kind: "playground",
      at: new Date().toISOString(),
      revision: t.revision,
      why: "Reloaded from the tool's files after the Prompt Playground changed them.",
      before,
      after: t.script || t.command || "",
    });
  tb.stage = "test";
  tb.testTab = "ai";
  update();
  toast(`Reloaded ${name} from its files.`);
}

// renameTool: a new name for a saved project tool; its script, the flows
// that call it, its tags and its heading follow (core/rename.go).
async function renameTool(old) {
  const name = (
    await askText(`Rename ${old}`, {
      value: old,
      ok: "Rename",
      message:
        "Lowercase letters, digits, - and _. Its script file is renamed and the flows that call it are updated; past runs keep the old name.",
    })
  )?.trim();
  if (!name || name === old) return;
  try {
    const t = await backend.RenameTool(old, name);
    await refreshExtensions();
    await loadLayout(true);
    if (S.toolTags) S.toolTags = null;
    openTool(t);
    toast(`Renamed ${old} to ${t.name}.`);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function generate(refining) {
  const tb = S.tb;
  if (!refining && !tb.prompt.trim()) return toast("Describe what the tool should do.");
  if (refining && !tb.refine.trim()) return toast("Say what to change.");
  tb.gen = { lines: [refining ? "Reading your change request" : "Understanding the request"] };
  update();
  try {
    if (!refining) {
      tb.history = [];
      tb.draft = null;
    }
    const d = await revise(
      refining
        ? {
            draft: tb.draft,
            change: tb.refine,
            // a failed test goes along, so the change can also fix it
            ...(tb.test && !tb.test.pass && !tb.test.running
              ? { failure: tb.test, question: tb.testPrompt, attachments: attPaths(tb) }
              : {}),
          }
        : { prompt: tb.prompt, runsIn: tb.runsIn, connects: tb.connects },
      refining ? "change" : "generate",
      { ask: refining ? tb.refine : tb.prompt },
    );
    tb.refine = "";
    tb.test = null;
    tb.direct = false;
    tb.stage = "review";
    if (!refining) tb.testPrompt = d.sampleTest || d.testPrompt || "";
    refreshSecrets();
  } catch (e) {
    toast(errMsg(e), "error");
  }
  tb.gen = null;
  update();
}

async function runTest() {
  const tb = S.tb;
  if (!tb.testPrompt.trim()) return toast("Write what you would ask the agent.");
  if ((tb.attachments || []).some((a) => a.saving))
    return toast("Wait for the attachments to finish saving.");
  tb.test = { running: true, steps: [], checks: [] };
  update();
  try {
    tb.test = await backend.TestExtensionWith(tb.draft, tb.testPrompt, attPaths(tb), tb.testModel || "");
  } catch (e) {
    tb.test = {
      pass: false,
      steps: [{ k: "out", v: "error: " + errMsg(e) }],
      checks: [],
      summary: errMsg(e),
    };
  }
  noteTest(tb);
  update();
}

// Ask the agent to fix the draft from the failed test, then test again;
// repeat until it passes or MAX_FIX attempts are used. What the developer
// typed under the test result (tb.fixAsk) goes with every attempt.
async function autoFix() {
  const tb = S.tb;
  const ask = (tb.fixAsk || "").trim();
  tb.fix = { running: true, log: [], ask };
  tb.fixAsk = "";
  update();
  const say = (l) => {
    tb.fix.log.push(l);
    update();
  };
  for (let a = 1; a <= MAX_FIX; a++) {
    say(
      `Attempt ${a} of ${MAX_FIX}: sending the failed test${ask ? " and your instructions" : ""} to the agent`,
    );
    try {
      await revise(
        {
          draft: tb.draft,
          failure: tb.test,
          question: tb.testPrompt,
          attachments: attPaths(tb),
          change: ask ? ask + "\nAnd fix the tool so the test passes." : "",
        },
        "fix",
        { ask, attempt: a },
      );
    } catch (e) {
      say("The agent could not fix it: " + errMsg(e));
      break;
    }
    say(`Revision ${tb.draft.revision} written. Testing again…`);
    await runTest();
    if (tb.test.pass) {
      say(`Fixed: the test passed on attempt ${a}.`);
      break;
    }
    if (a === MAX_FIX)
      say(
        `Still failing after ${MAX_FIX} attempts. Tell the AI what to change in the box above and try again, or review the script.`,
      );
  }
  tb.fix.running = false;
  update();
}

// The test passed but the developer wants a change: revise the draft with
// their words, then test it again.
async function refineAndTest() {
  const tb = S.tb;
  const ask = (tb.fixAsk || "").trim();
  if (!ask) return toast("Say what to change.");
  tb.fix = { running: true, log: [], ask };
  tb.fixAsk = "";
  update();
  const say = (l) => {
    tb.fix.log.push(l);
    update();
  };
  say("Sending your change to the agent");
  try {
    await revise({ draft: tb.draft, change: ask }, "change", { ask });
    say(`Revision ${tb.draft.revision} written. Testing again…`);
    await runTest();
    say(tb.test.pass ? "Changed, and the test still passes." : "Changed, but the test fails now.");
  } catch (e) {
    say("The agent could not change it: " + errMsg(e));
  }
  tb.fix.running = false;
  update();
  if (tb.test && !tb.test.pass && tb.autoFix !== false && !tb.ro) autoFix();
}

// Run the test; when it fails and "fix automatically" is on, start autoFix.
async function testAndMaybeFix() {
  const tb = S.tb;
  tb.fix = null;
  await runTest();
  const missing = (tb.draft?.secrets || []).some(
    (s) => !s.optional && S.secretInfo[s.name]?.via === "missing",
  );
  if (tb.test && !tb.test.pass && tb.autoFix !== false && !missing && !tb.ro) autoFix();
}

// lastTestOf: the tool's latest test, the AI test or its test cases,
// whichever ran last (or what it had); a passing one makes it "tested".
function lastTestOf(tb, d) {
  const xs = [tb.test, tb.caseTest, d.lastTest].filter((x) => x && !x.running);
  if (!xs.length) return null;
  return xs.reduce((a, b) => (String(b.at || "") > String(a.at || "") ? b : a));
}

async function saveTool() {
  const tb = S.tb,
    d = tb.draft;
  if (!/^[a-z][a-z0-9_-]{0,39}$/.test(d.name)) {
    toast("Name must start with a letter and use lowercase letters, digits, - and _.");
    tb.stage = "review";
    return update();
  }
  if (tb.admin) {
    try {
      const rec = await backend.SaveManagedTool({
        ...d,
        scope: "managed",
        testPrompt: tb.testPrompt,
        prompt: tb.prompt || d.prompt,
        lastTest: tb.test && !tb.test.running ? tb.test : null,
      });
      toast(
        `${rec.name} v${rec.managed?.version || 1} published to all projects, signed by ${rec.managed?.signer || "you"}.`,
      );
      S.tb = { ...tb, editing: rec.name, draft: clone(rec) };
      await refreshExtensions();
      refreshAdmin();
    } catch (e) {
      toast(errMsg(e), "error");
    }
    return;
  }
  try {
    const rec =
      d.kind === "npm"
        ? await backend.ApproveExtension(d.name)
        : await backend.SaveExtension({
            ...d,
            scope: tb.scope,
            testPrompt: tb.testPrompt,
            testFiles: attPaths(tb),
            prompt: tb.prompt || d.prompt,
            lastTest: lastTestOf(tb, d),
            status: lastTestOf(tb, d)?.pass ? "tested" : "untested",
          });
    toast(
      rec.status === "in review"
        ? `${rec.name} submitted to the corporate catalog for review.`
        : `${rec.name} saved${rec.scriptPath ? " to " + rec.scriptPath : ""} and approved for agents.`,
    );
    S.tb = { ...tb, editing: rec.name, draft: clone(rec) };
    await refreshExtensions();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// TestTabs: test a tool directly with arguments, or have the AI use it.
function TestTabs({ tb }) {
  const tab = tb.testTab || "args";
  return (
    <div className="tabs" role="tablist" aria-label="How to test">
      {[
        ["args", "Run with arguments"],
        ["ai", "Test with AI"],
      ].map(([k, l]) => (
        <button
          key={k}
          role="tab"
          className="tab"
          aria-selected={tab === k}
          onClick={() => {
            tb.testTab = k;
            update();
          }}
        >
          {l}
        </button>
      ))}
    </div>
  );
}

export function Tools() {
  useEffect(() => {
    loadToolTags();
  }, [S.toolTags === null]);
  useEffect(() => {
    loadLayout();
  }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="tools" />;
  if (!S.tb) S.tb = tbDefaults();
  const tb = S.tb,
    d = tb.draft,
    si = STAGES.findIndex(([id]) => id === tb.stage),
    exts = S.ws.extensions || [];
  const reach = (k) =>
    tb.ro
      ? (k === 1 || k === 2) && d
      : k === 0 ||
        (k <= 2 && d) ||
        // an imported tool ("needs approval") can be approved after reading it, without a test
        (k === 3 &&
          d &&
          (d.kind === "npm" || d.status === "needs approval" || tb.direct || (tb.test && !tb.test.running)));
  const set = (k, v) => {
    tb[k] = v;
    update();
  };
  const setD = (k, v) => {
    d[k] = v;
    update();
  };
  // only secrets with no way to get a value block a test; "ask" ones prompt
  const missing = (d?.secrets || []).filter((s) => !s.optional && S.secretInfo[s.name]?.via === "missing");
  let body = null;

  if (tb.stage === "describe") {
    body = (
      <div className="card">
        <h3>What should the tool do?</h3>
        <p className="wf-desc">
          Describe it in plain language. The AI writes the script, the parameters and the list of secrets it
          needs. You review everything before it is saved.
        </p>
        <textarea
          rows={4}
          placeholder={"e.g. " + EXAMPLES[0]}
          disabled={!!tb.gen}
          value={tb.prompt}
          onChange={(e) => set("prompt", e.target.value)}
        />
        <div className="chips" style={{ marginTop: 8 }}>
          {EXAMPLES.map((e) => (
            <Chip key={e} onClick={() => set("prompt", e)}>
              {e.split(" ").slice(0, 5).join(" ")}…
            </Chip>
          ))}
        </div>
        <div className="two-col" style={{ marginTop: 12 }}>
          <div>
            <h4 style={{ marginTop: 0 }}>Connects to</h4>
            <div className="chips">
              {CONNECTS.map(([v, l]) => (
                <Chip
                  key={v}
                  on={tb.connects.includes(v)}
                  onClick={() => {
                    if (v === "none") tb.connects = ["none"];
                    else {
                      tb.connects = tb.connects.filter((c) => c !== "none");
                      tb.connects = tb.connects.includes(v)
                        ? tb.connects.filter((c) => c !== v)
                        : [...tb.connects, v];
                      if (!tb.connects.length) tb.connects = ["none"];
                    }
                    update();
                  }}
                >
                  {l}
                </Chip>
              ))}
            </div>
          </div>
          <label className="field">
            <h4 style={{ marginTop: 0 }}>Runs in</h4>
            <select value={tb.runsIn} onChange={(e) => set("runsIn", e.target.value)}>
              {RUNS_IN.map((r) => (
                <option key={r} value={r}>
                  {r === "Auto"
                    ? `Auto (${installed()[0] || "Bash"})`
                    : installed().includes(r) || r === "Python"
                      ? r
                      : r + " (not installed)"}
                </option>
              ))}
            </select>
            <small className="muted">
              Auto prefers {PRIORITY.join(" → ")} and uses the first one installed on this computer.
            </small>
          </label>
        </div>
        <div className="row-actions">
          <button className="btn primary" disabled={!!tb.gen} onClick={() => generate(false)}>
            {tb.gen ? "Generating…" : "Generate tool with AI"}
          </button>
          <ModelPicker
            value={tb.genModel || ""}
            onChange={(v) => set("genModel", v)}
            label="Model for writing the tool"
            disabled={!!tb.gen}
          />
          <button
            className="btn secondary"
            onClick={() => {
              tb.draft = manualDraft();
              tb.stage = "review";
              tb.test = null;
              tb.direct = false;
              tb.direct = false;
              update();
            }}
          >
            Write the script myself
          </button>
        </div>
        {tb.gen && (
          <div className="transcript" style={{ minHeight: 0, marginTop: 10 }}>
            <div className="transcript-inner">
              {tb.gen.lines.map((l, k) => (
                <div className="row note" key={k}>
                  <Icon.check />
                  <span>{l}</span>
                </div>
              ))}
              <AiProgress phases={GEN_PHASES} />
            </div>
          </div>
        )}
      </div>
    );
  } else if (tb.stage === "review" && d && d.kind === "copilot") {
    body = <CopilotToolReview d={d} onNext={() => set("stage", "test")} />;
  } else if (tb.stage === "save" && d && d.kind === "copilot") {
    body = (
      <div className="card">
        <h3>{d.status === "needs approval" ? "Approve this Copilot extension tool?" : "Nothing to save"}</h3>
        <p className="wf-desc">
          {d.status === "needs approval" ? (
            "Approving reads the extension's tools again from its current files and lets agents and flows use them."
          ) : (
            <>
              Copilot extension tools are saved by their files in <code className="inline-code">{d.dir}</code>
              ; it is approved and available to agents and flows.
            </>
          )}
        </p>
        <div className="row-actions">
          <button className="btn secondary" onClick={() => set("stage", "test")}>
            Back
          </button>
          {d.status === "needs approval" && (
            <button className="btn primary" onClick={() => approveCopilotTool(d)}>
              Approve for agents
            </button>
          )}
        </div>
      </div>
    );
  } else if (tb.stage === "review" && d) {
    const npm = d.kind === "npm";
    // the Review step in tabs, so the script is not 1,700 px down the page
    const RT = [
      ["overview", "Overview", npm ? null : d.params.length],
      ["script", npm ? "Command" : "Script"],
      ["data", "Output shape"],
      ...(npm
        ? []
        : [
            ["deps", "Dependencies", (d.deps || []).length],
            ["secrets", "Secrets", (d.secrets || []).length],
          ]),
    ];
    const rt = RT.some(([k]) => k === tb.rtab) ? tb.rtab : "overview";
    body = (
      <>
        <fieldset className="ro-fieldset" disabled={!!tb.ro}>
          <div className="tabs tl-rtabs" role="tablist" aria-label="Review the tool">
            {RT.map(([k, l, n]) => (
              <button
                key={k}
                type="button"
                role="tab"
                className="tab"
                aria-selected={rt === k}
                onClick={() => set("rtab", k)}
              >
                {l}
                {n ? <span className="badge">{n}</span> : null}
              </button>
            ))}
          </div>
          {rt === "overview" && (
            <>
              <div className="card">
                <div className="card-head">
                  <h3>{npm ? "npm script" : "Generated tool"}</h3>
                  <span className="badge">revision {d.revision || 1}</span>
                  <span className="badge badge-info">{d.lang || "Bash"}</span>
                  {npm && <StatusBadge s={d.status} />}
                </div>
                <div className="two-col">
                  <label className="field">
                    <span className="muted">Name</span>
                    <input
                      value={d.name}
                      disabled={npm}
                      style={{ fontFamily: "var(--font-mono)" }}
                      onChange={(e) => setD("name", e.target.value)}
                    />
                  </label>
                  <label className="field">
                    <span className="muted">Description (what the agent sees)</span>
                    <input value={d.desc} onChange={(e) => setD("desc", e.target.value)} />
                  </label>
                </div>
                {npm && (
                  <p className="muted" style={{ marginTop: 8 }}>
                    From <code className="inline-code">package.json</code> →{" "}
                    <code className="inline-code">scripts["ext:{d.name}"]</code>. Agents can run it only after
                    you approve it; editing the script in package.json withdraws the approval. Needs Node.js
                    {S.info?.node ? "" : " (not found on PATH)"}.
                  </p>
                )}
              </div>
              {!npm && (
                <div className="card">
                  <h3>Parameters</h3>
                  <p className="wf-desc">
                    The agent fills these when it calls the tool. Use{" "}
                    <code className="inline-code">{"{{name}}"}</code> in the command.
                  </p>
                  <table className="table">
                    <thead>
                      <tr>
                        <th>Name</th>
                        <th>Type</th>
                        <th>Required</th>
                        <th>Description</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {d.params
                        .map((p, k) => (
                          <tr key={k}>
                            <td>
                              <input
                                aria-label="Parameter name"
                                value={p.name}
                                style={{ width: "100%", fontFamily: "var(--font-mono)" }}
                                onChange={(e) => {
                                  p.name = e.target.value;
                                  update();
                                }}
                              />
                            </td>
                            <td>
                              <select
                                aria-label="Type"
                                value={(p.type || "string") + "|" + (p.format || "")}
                                style={{ width: "100%" }}
                                onChange={(e) => {
                                  const [t, f] = e.target.value.split("|");
                                  p.type = t;
                                  if (f) p.format = f;
                                  else delete p.format;
                                  update();
                                }}
                              >
                                {!PARAM_KINDS.some(
                                  ([v]) => v === (p.type || "string") + "|" + (p.format || ""),
                                ) && (
                                  <option value={(p.type || "string") + "|" + (p.format || "")}>
                                    {p.type}
                                    {p.format ? " · " + p.format : ""}
                                  </option>
                                )}
                                {PARAM_KINDS.map(([v, l]) => (
                                  <option key={v} value={v}>
                                    {l}
                                  </option>
                                ))}
                              </select>
                            </td>
                            <td>
                              <input
                                type="checkbox"
                                aria-label="Required"
                                checked={!!p.required}
                                onChange={(e) => {
                                  p.required = e.target.checked;
                                  update();
                                }}
                              />
                            </td>
                            <td>
                              <input
                                aria-label="Description"
                                value={p.desc}
                                style={{ width: "100%" }}
                                onChange={(e) => {
                                  p.desc = e.target.value;
                                  update();
                                }}
                              />
                            </td>
                            <td>
                              <button
                                className="link"
                                onClick={() => {
                                  d.params.splice(k, 1);
                                  update();
                                }}
                              >
                                Remove
                              </button>
                            </td>
                          </tr>
                        ))
                        .flatMap((row, k) => [
                          row,
                          ...(JSON_TYPES.test(d.params[k].type || "")
                            ? [
                                <tr key={"s" + k} className="param-shape">
                                  <td colSpan={5}>
                                    <ShapeEditor
                                      label={`Shape of ${d.params[k].name || "this parameter"}`}
                                      hint="Example JSON or a JSON Schema. The agent is given its fields, compactly, when it fills this parameter in."
                                      value={d.params[k].schema}
                                      onChange={(v) => {
                                        d.params[k].schema = v;
                                        update();
                                      }}
                                    />
                                  </td>
                                </tr>,
                              ]
                            : []),
                        ])}
                    </tbody>
                  </table>
                  <div className="row-actions">
                    <button
                      className="btn secondary"
                      onClick={() => {
                        d.params.push({ name: "", type: "string", required: false, desc: "" });
                        update();
                      }}
                    >
                      + Add parameter
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
          {rt === "data" && <OutputShapeCard d={d} test={tb.test} />}
          {rt === "deps" && !npm && <DepsCard d={d} scope={tb.scope} />}
          {rt === "secrets" && !npm && <SecretsCard d={d} />}
          {rt === "script" && (
            <div className="card">
              <div className="card-head">
                <h3>{npm ? "Command" : "Script"}</h3>
                <code className="inline-code">{d.command}</code>
              </div>
              {!npm &&
                (d.deps?.length ? (
                  <p className="muted" style={{ marginBottom: 8 }}>
                    The app writes the command for tools with dependencies: it runs{" "}
                    <code className="inline-code">{entryFile(d.lang)}</code> from the tool's folder, with the
                    parameters in order.
                  </p>
                ) : (
                  <label className="field" style={{ marginBottom: 8 }}>
                    <span className="muted">Command template</span>
                    <input
                      value={d.command}
                      style={{ fontFamily: "var(--font-mono)" }}
                      onChange={(e) => setD("command", e.target.value)}
                    />
                  </label>
                ))}
              <textarea
                className="editor"
                rows={12}
                spellCheck="false"
                style={{ minHeight: npm ? 60 : 200 }}
                value={d.script || ""}
                disabled={npm}
                onChange={(e) => setD("script", e.target.value)}
              />
              {!npm && (
                <label className="field" style={{ marginTop: 10 }}>
                  <span className="muted">Ask AI to change something</span>
                  <div className="setup-input">
                    <input
                      value={tb.refine}
                      placeholder="e.g. add a 30 second timeout, also scan .kt files"
                      onChange={(e) => set("refine", e.target.value)}
                    />
                    <button className="btn secondary" disabled={!!tb.gen} onClick={() => generate(true)}>
                      {tb.gen ? "Updating…" : "Update"}
                    </button>
                    <ModelPicker
                      value={tb.genModel || ""}
                      onChange={(v) => set("genModel", v)}
                      label="Model for changing the tool"
                      disabled={!!tb.gen}
                    />
                  </div>
                </label>
              )}
              {tb.gen && (
                <AiProgress
                  phases={[
                    "Reading the change",
                    "Updating the script",
                    "Checking the parameters",
                    "Finishing up",
                  ]}
                />
              )}
            </div>
          )}
          <div className="row-actions tl-review-actions">
            <button className="btn secondary" onClick={() => set("stage", "describe")}>
              Back
            </button>
            <button className="btn primary" onClick={() => set("stage", npm ? "save" : "test")}>
              {npm ? "Next: approve" : "Next: test"}
            </button>
            {!npm && d.status === "needs approval" && (
              <button
                className="btn secondary"
                title="An imported tool: once you have read its script and command, you can approve it without an AI test"
                onClick={() => set("stage", "save")}
              >
                Skip the test
              </button>
            )}
            {tb.editing && !tb.ro && !tb.admin && (
              <>
                <span className="status-spacer" />
                <HeadingPicker list="tools" id={tb.editing} />
                {d?.kind === "script" && d?.scope === "project" && d?.approved && (
                  <button className="btn secondary" onClick={() => renameTool(tb.editing)}>
                    Rename…
                  </button>
                )}
                {d?.kind !== "npm" && d?.scope !== "global" && (
                  <button className="btn danger" onClick={() => archiveTool(d)}>
                    <Icon.x /> Delete tool
                  </button>
                )}
              </>
            )}
          </div>
        </fieldset>
        <DecisionLog tb={tb} />
      </>
    );
  } else if (tb.stage === "test" && d && (tb.testTab || "args") === "args") {
    body = (
      <>
        <TestTabs tb={tb} />
        <ToolTester tb={tb} d={d} />
        <div className="row-actions">
          <button className="btn secondary" onClick={() => set("stage", "review")}>
            Back to review
          </button>
          {!tb.ro && (
            <button
              className="btn primary"
              disabled={!(tb.direct || (tb.test && !tb.test.running))}
              title={tb.direct ? "" : "Run it once (with arguments or with AI) first"}
              onClick={() => set("stage", "save")}
            >
              {d.kind === "copilot" ? "Next" : tb.admin ? "Next: publish" : "Next: save"}
            </button>
          )}
        </div>
      </>
    );
  } else if (tb.stage === "test" && d) {
    const t = tb.test;
    body = (
      <>
        <TestTabs tb={tb} />
        <DropZone list={tb.attachments} className="card att-zone">
          <h3>Test with AI</h3>
          <p className="wf-desc">
            Ask something a developer would ask, and attach screenshots, PDFs or text if they help. The agent
            is offered only <code className="inline-code">{d.name}</code>, decides whether to use it, fills
            the arguments, runs it, and the result is checked.
          </p>
          <div className="setup-input">
            <input
              value={tb.testPrompt}
              placeholder={d.sampleTest}
              onPaste={onPasteAttach(tb.attachments)}
              onChange={(e) => set("testPrompt", e.target.value)}
            />
            <button
              className="btn primary"
              disabled={t?.running || tb.fix?.running}
              onClick={testAndMaybeFix}
            >
              {t?.running ? "Testing…" : t ? "Test again" : "Run test"}
            </button>
          </div>
          <AttachBar
            list={tb.attachments}
            disabled={t?.running}
            insertText={(x) => set("testPrompt", (tb.testPrompt ? tb.testPrompt + " " : "") + x)}
          />
          <div className="tb-model">
            <ModelPicker
              value={tb.testModel || ""}
              onChange={(v) => set("testModel", v)}
              label="Test with"
              disabled={t?.running}
            />
          </div>
          <label className="check" style={{ marginTop: 8 }}>
            <input
              type="checkbox"
              checked={tb.autoFix !== false}
              onChange={(e) => set("autoFix", e.target.checked)}
            />{" "}
            Fix automatically with AI when the test fails (up to {MAX_FIX} attempts)
          </label>
          {missing.length > 0 && (
            <p className="muted" style={{ marginTop: 8, color: "var(--warn)" }}>
              <Icon.warn /> {missing.map((s) => s.name).join(", ")} has no value. Set the environment
              variable, enter it under Review › Secrets, or switch it to “Ask me”.
            </p>
          )}
          {(d.secrets || []).some((s) => S.secretInfo[s.name]?.via === "ask") && (
            <p className="muted" style={{ marginTop: 8 }}>
              You will be asked for{" "}
              {(d.secrets || [])
                .filter((s) => S.secretInfo[s.name]?.via === "ask")
                .map((s) => s.name)
                .join(", ")}{" "}
              when the tool runs.
            </p>
          )}
        </DropZone>
        {t && (
          <div className="transcript" style={{ minHeight: 0 }}>
            <div className="transcript-inner">
              <div className="row note">
                <Icon.check />
                <span>You: {tb.testPrompt}</span>
              </div>
              {(t.steps || []).map((s, k) =>
                s.k === "attach" ? (
                  <div key={k} className="row">
                    <div className="muted">Attached</div>
                    <AttachmentChips
                      list={fromPaths(s.v.split("\n")).map(
                        (a) => (tb.attachments || []).find((x) => x.path === a.path) || a,
                      )}
                    />
                  </div>
                ) : s.k === "answer" ? (
                  <div key={k} className="row assistant">
                    <div className="muted">Agent's answer</div>
                    <Linkify text={s.v} />
                  </div>
                ) : s.k === "say" ? (
                  <div key={k} className="row assistant">
                    <Linkify text={s.v} />
                  </div>
                ) : s.k === "call" ? (
                  <div key={k} className="row">
                    <div className="muted">Tool call</div>
                    <pre className="code">
                      <Linkify text={s.v} />
                    </pre>
                  </div>
                ) : s.k === "install" ? (
                  <div key={k} className="row">
                    <div className="muted">Installing dependencies</div>
                    <pre className="tool-result">
                      <Linkify text={s.v} />
                    </pre>
                  </div>
                ) : s.k === "run" ? (
                  <div key={k} className="row system">
                    <Linkify text={s.v} />
                  </div>
                ) : (
                  <div key={k} className="row">
                    <div className="muted">Output</div>
                    <ContentView title={(d?.name || "tool") + " output"} text={s.v} />
                  </div>
                ),
              )}
              {t.running && <AiProgress phases={TEST_PHASES} />}
            </div>
          </div>
        )}
        {t && !t.running && (
          <div className="card" style={{ borderColor: `var(${t.pass ? "--ok" : "--error"})` }}>
            <div className="card-head">
              <h3>{t.pass ? "Test passed" : "Test failed"}</h3>
              <Pill s={t.pass ? "done" : "failed"} label={t.pass ? "Passed" : "Failed"} />
            </div>
            <ul className="checklist">
              {(t.checks || []).map((c) => (
                <li key={c.label} className={c.ok ? "ok" : ""}>
                  {c.ok ? <Icon.check /> : <Icon.x />}
                  <span>{c.label}</span>
                </li>
              ))}
            </ul>
            <p className="muted">
              <Linkify text={t.summary} />
            </p>
            {!tb.fix?.running && !tb.ro && (
              <div className="fix-ask">
                {!t.pass && missing.length > 0 ? (
                  <p className="muted">
                    Give {missing.map((s) => s.name).join(", ")} a value first (environment variable, session
                    value or “Ask me”): the AI cannot supply secrets.
                  </p>
                ) : (
                  <>
                    <label className="field">
                      <span>
                        <b>{t.pass ? "Change something" : "Let the AI fix it"}</b>{" "}
                        <span className="muted">
                          {t.pass
                            ? "· say what to change; the AI revises the tool and tests it again"
                            : `· say what is wrong or what to do (optional); it also gets the failed checks and output, applies its fix and tests again (up to ${MAX_FIX} attempts)`}
                        </span>
                      </span>
                      <textarea
                        rows={3}
                        value={tb.fixAsk || ""}
                        placeholder={
                          t.pass
                            ? "e.g. also return the file count; sort the results by size"
                            : "e.g. the API wants the token in an Authorization header; dates are YYYY-MM-DD"
                        }
                        onChange={(e) => set("fixAsk", e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
                            t.pass ? refineAndTest() : autoFix();
                        }}
                      />
                    </label>
                    <div className="row-actions">
                      {t.pass ? (
                        <button
                          className="btn secondary"
                          disabled={!(tb.fixAsk || "").trim()}
                          onClick={refineAndTest}
                        >
                          <Icon.spark /> Change and test again
                        </button>
                      ) : (
                        <button className="btn primary" onClick={autoFix}>
                          <Icon.spark /> Fix with AI
                        </button>
                      )}
                      <ModelPicker
                        value={tb.genModel || ""}
                        onChange={(v) => set("genModel", v)}
                        label="Model for changing the tool"
                      />
                      <button
                        className="btn secondary"
                        onClick={() => openToolInPlayground()}
                        title="Talk it through with the AI in the Prompt Playground: it gets this tool, what was tried and the last test, and can read the project and try commands"
                      >
                        <Icon.playground /> Discuss in Prompt Playground
                      </button>
                    </div>
                  </>
                )}
              </div>
            )}
          </div>
        )}
        {tb.fix && (
          <div className="card">
            <div className="card-head">
              <h3>{tb.fix.ask ? "AI change" : "Automatic fix"}</h3>
              {tb.fix.running ? (
                <span className="spinner" />
              ) : (
                <span className={"badge " + (t?.pass ? "badge-ok" : "badge-warn")}>
                  {t?.pass
                    ? tb.fix.ask && !tb.fix.log.some((l) => l.startsWith("Attempt"))
                      ? "done"
                      : "fixed"
                    : "not fixed"}
                </span>
              )}
            </div>
            {tb.fix.ask && (
              <p className="muted">
                You asked: <i>{tb.fix.ask}</i>
              </p>
            )}
            <ul className="lib-list">
              {tb.fix.log.map((l, k) => (
                <li key={k}>{l}</li>
              ))}
            </ul>
            {!tb.fix.running && (
              <div className="row-actions">
                <button className="link" onClick={() => set("stage", "review")}>
                  Review the changed script
                </button>
              </div>
            )}
          </div>
        )}
        <DecisionLog tb={tb} />
        <div className="row-actions">
          <button className="btn secondary" onClick={() => set("stage", "review")}>
            Back to review
          </button>
          {!tb.ro && (
            <button
              className="btn primary"
              disabled={!(tb.direct || (t && !t.running))}
              onClick={() => set("stage", "save")}
            >
              {tb.admin ? "Next: publish" : "Next: save"}
            </button>
          )}
        </div>
      </>
    );
  } else if (tb.stage === "save" && d && tb.admin) {
    const passed = tb.test?.pass,
      v = d.managed?.version;
    body = (
      <div className="card admin-card">
        <div className="card-head">
          <Icon.lock />
          <h3>Publish as a managed tool</h3>
          <span className="badge badge-warn">admin</span>
        </div>
        <p className="wf-desc">
          Managed tools are global: every project in this app can use them, and regular users can run and test
          them but never change, replace or remove them. The tool is signed with your admin key; any edit made
          outside admin mode breaks the signature and the app stops running it.
        </p>
        <dl className="kv" style={{ marginTop: 12 }}>
          <dt>Name</dt>
          <dd>
            <code>{d.name}</code>
          </dd>
          <dt>Version</dt>
          <dd>{v ? `v${v} → v${v + 1}` : "v1 (new)"}</dd>
          <dt>Runtime</dt>
          <dd>{d.lang || "Bash"}</dd>
          <dt>Secrets</dt>
          <dd>
            {(d.secrets || []).map((x) => x.name).join(", ") || "none"}{" "}
            <span className="muted">(each user supplies values from their environment or when asked)</span>
          </dd>
          <dt>Test</dt>
          <dd>
            {passed ? (
              <>
                <span className="badge badge-ok">passed</span> {tb.testPrompt}
              </>
            ) : (
              <span className="badge badge-warn">not passed</span>
            )}
          </dd>
          <dt>Signed by</dt>
          <dd>
            {S.admin?.name} <code className="inline-code">{S.admin?.keyId}</code>
          </dd>
        </dl>
        {!passed && (
          <div className="banner" style={{ marginTop: 10, borderColor: "var(--warn)" }}>
            Managed tools must pass a test before they are published.
          </div>
        )}
        {!S.admin?.unlocked && (
          <div className="banner error" style={{ marginTop: 10 }}>
            Admin mode is locked. Unlock it on the Admin screen to publish.
          </div>
        )}
        <div className="row-actions">
          <button className="btn secondary" onClick={() => set("stage", "test")}>
            Back
          </button>
          <button className="btn primary" disabled={!passed || !S.admin?.unlocked} onClick={saveTool}>
            {v ? "Publish new version" : "Publish to all projects"}
          </button>
        </div>
      </div>
    );
  } else if (tb.stage === "save" && d) {
    const passed = tb.test?.pass,
      npm = d.kind === "npm";
    body = (
      <div className="card">
        <h3>{npm ? "Approve this npm script?" : "Where should this tool live?"}</h3>
        {!npm && (
          <div className="setup-choices" role="radiogroup" aria-label="Scope" style={{ marginTop: 8 }}>
            <button
              type="button"
              role="radio"
              aria-checked={tb.scope === "project"}
              className={"setup-choice" + (tb.scope === "project" ? " active" : "")}
              onClick={() => set("scope", "project")}
            >
              <strong>Project-specific</strong>
              <span className="muted">
                Only this project. Saved to <code>.codegen/tools/project/</code> and listed in{" "}
                <code>.codegen/extensions.json</code>.
              </span>
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={tb.scope === "global"}
              className={"setup-choice" + (tb.scope === "global" ? " active" : "")}
              onClick={() => set("scope", "global")}
            >
              <strong>Global (corporate)</strong>
              <span className="muted">
                Every project can use it once a library owner reviews it in the corporate catalog.
              </span>
            </button>
          </div>
        )}
        <dl className="kv" style={{ marginTop: 12 }}>
          <dt>Name</dt>
          <dd>
            <code>{d.name}</code>
          </dd>
          <dt>Runtime</dt>
          <dd>{d.lang || "Bash"}</dd>
          <dt>Parameters</dt>
          <dd>{(d.params || []).map((p) => p.name + (p.required ? "" : "?")).join(", ") || "none"}</dd>
          <dt>Secrets</dt>
          <dd>
            {(d.secrets || []).map((s) => s.name).join(", ") || "none"}{" "}
            <span className="muted">(names only; values stay on this computer)</span>
          </dd>
          <dt>Test</dt>
          <dd>
            {passed ? (
              <>
                <span className="badge badge-ok">passed</span> {tb.testPrompt}
              </>
            ) : (
              <span className="badge badge-warn">not passed</span>
            )}
          </dd>
          <dt>Approval</dt>
          <dd>
            Saving approves this exact {npm ? "command" : "script"} for agents. Any later change needs a new
            approval.
          </dd>
        </dl>
        {!passed && !npm && (
          <div className="banner" style={{ marginTop: 10, borderColor: "var(--warn)" }}>
            The tool has not passed a test.{" "}
            {tb.scope === "global"
              ? "Global tools must pass a test before they can be submitted."
              : "You can still save it to this project, marked untested."}
          </div>
        )}
        <div className="row-actions">
          <button className="btn secondary" onClick={() => set("stage", npm ? "review" : "test")}>
            Back
          </button>
          <button
            className="btn primary"
            disabled={!npm && !passed && tb.scope === "global"}
            onClick={saveTool}
          >
            {npm
              ? "Approve for agents"
              : tb.scope === "global"
                ? "Submit to global catalog"
                : d.status === "needs approval"
                  ? "Approve and save"
                  : tb.editing
                    ? "Update project tool"
                    : "Save to project"}
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="files">
      <Pane k="tools" v="--pane-l" pane=".ftree" min={200} max={560} label="Resize the tool list" />
      <nav className="ftree" aria-label="Tools">
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            padding: "0 8px 8px",
          }}
        >
          <h1>Tools</h1>
          <span style={{ display: "flex", gap: 6 }}>
            <button className="btn primary" onClick={() => newTool(false)}>
              + New
            </button>
            <ShareMenu />
          </span>
        </div>
        <ToolFinder />
        {finding() ? (
          <FoundTools exts={exts} />
        ) : (
          <>
            <BuiltinList />
            {[
              ["managed", "Managed by admin · all projects"],
              ["project", "Project tools"],
              ["global", "Global tools"],
            ].map(([g, l]) => (
              <ToolGrid
                key={g}
                title={l}
                compact
                tools={withTags(exts.filter((t) => t.scope === g))}
                render={(t) => (
                  <ToolTile
                    key={t.name}
                    t={t}
                    compact
                    active={S.tb?.editing === t.name}
                    onOpen={() => openTool(t)}
                  />
                )}
              />
            ))}
            <CopilotExtList />
          </>
        )}
      </nav>
      <div className="fview">
        {S.bt?.sel && builtinsVisible() && !S.cx?.sel ? <ToolTagsBar name={"builtin:" + S.bt.sel} /> : null}
        {S.cx?.sel ? (
          <CopilotExtView />
        ) : S.bt?.sel && builtinsVisible() ? (
          <BuiltinView />
        ) : (
          <>
            <header className="view-head">
              <h1>
                {tb.admin
                  ? tb.editing
                    ? "Managed tool · " + tb.editing
                    : "New managed tool"
                  : tb.ro
                    ? "Managed tool · " + tb.editing
                    : tb.editing
                      ? (d?.kind === "copilot" ? "Copilot tool · " : "Edit tool · ") + tb.editing
                      : "New tool"}
              </h1>
              {(tb.admin || tb.ro) && (
                <span className={"badge " + (tb.admin ? "badge-warn" : "")}>
                  <Icon.lock /> {tb.admin ? "admin mode" : "read-only"}
                </span>
              )}
              {tb.editing && !tb.admin && !tb.ro && (d?.kind || "script") === "script" && (
                <button
                  className="btn secondary"
                  style={{ marginLeft: "auto" }}
                  title="Save this tool to a file another project can import"
                  onClick={() => exportTools([tb.editing])}
                >
                  Export
                </button>
              )}
            </header>
            {tb.editing && <ToolTagsBar name={tb.editing} />}
            {tb.ro && <ManagedBanner d={d} />}
            {tb.admin && !tb.editing && (
              <div className="banner admin-banner">
                <Icon.lock /> Admin mode: this tool will be signed and published to every project in the app.
              </div>
            )}
            <div className="stepstrip">
              {STAGES.map(([id, l], k) => (
                <span key={id} style={{ display: "contents" }}>
                  <button
                    className={"stepchip" + (k === si ? " st-running" : "")}
                    disabled={!reach(k)}
                    aria-current={k === si ? "step" : undefined}
                    style={{ cursor: "pointer", ...(k < si ? { borderColor: "var(--ok)" } : {}) }}
                    onClick={() => set("stage", id)}
                  >
                    <span className={"card-index" + (k < si ? " done" : "")}>
                      {k < si ? <Icon.check /> : k + 1}
                    </span>
                    <span className="stepchip-title">{l}</span>
                  </button>
                  {k < STAGES.length - 1 && <span className="muted">→</span>}
                </span>
              ))}
            </div>
            {body}
          </>
        )}
      </div>
    </div>
  );
}

// Back / Forward: the tool open on the Tools screen (navigation.js)
registerPlace("tools", {
  key: () => S.tb?.editing || "",
  restore: (name) => {
    const t = (S.ws?.extensions || []).find((x) => x.name === name);
    if (t) openTool(t);
  },
});
