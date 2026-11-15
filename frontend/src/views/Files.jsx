import { useEffect, useRef } from "react";
import { ModelPicker } from "../ModelPicker.jsx";
import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg, usageOf, fmtK, fmtMs, fmtCredits } from "../store.js";
import { Icon, Pill, Chip } from "../ui.jsx";
import { NeedWorkspace } from "./Wizard.jsx";
import { toolCalls, stateOf } from "./Playground.jsx";
import { loadPackages } from "./Packages.jsx";
import { Linkify as FileLinkify } from "../FileLinks.jsx";
import { FileView, loadFileInto } from "../FileView.jsx";
import { AiProgress } from "../AiProgress.jsx";

// the open file's content (PreviewFile result), shown with the shared FileView
const view = () => (S.files.view ||= {});
const reloadView = () => {
  if (S.files.sel) loadFileInto(view(), S.files.sel);
};

function buildTree(paths) {
  const root = {};
  for (const p of paths) {
    const parts = p.split("/");
    let n = root;
    parts.forEach((part, k) => {
      if (k === parts.length - 1) n[part] = null;
      else {
        n[part] = n[part] || {};
        n = n[part];
      }
    });
  }
  return root;
}

function Tree({ node, prefix, depth }) {
  return Object.keys(node)
    .sort((a, b) => (node[a] === null) - (node[b] === null) || a.localeCompare(b))
    .map((name) => {
      const path = prefix ? prefix + "/" + name : name,
        kid = node[name];
      if (kid === null)
        return (
          <button
            key={path}
            className={"tree-item" + (S.files.sel === path ? " active" : "")}
            style={{ paddingLeft: 8 + depth * 14 }}
            onClick={() => {
              S.files.sel = path;
              S.files.line = 0;
              update();
            }}
          >
            <Icon.file />
            <span>{name}</span>
          </button>
        );
      // collapse single-child package chains under src/*/java|kotlin: com/acme/orders → com.acme.orders
      let label = name,
        p = path,
        k = kid;
      if (/src\/(main|test)\/(java|kotlin)\/[^/]+$/.test(path))
        while (k && Object.keys(k).length === 1 && k[Object.keys(k)[0]] !== null) {
          const n = Object.keys(k)[0];
          label += "." + n;
          p += "/" + n;
          k = k[n];
        }
      const isPkg = /src\/(main|test)\/(java|kotlin)\//.test(p + "/");
      const open = S.files.q ? true : (S.files.open[p] ?? !/^\.codegen$/.test(p));
      return (
        <div key={p}>
          <button
            className="tree-item"
            style={{ paddingLeft: 8 + depth * 14 }}
            aria-expanded={open}
            onClick={() => {
              S.files.open[p] = !open;
              update();
            }}
          >
            {isPkg ? <Icon.pkg /> : <Icon.folder />}
            <span>{label}</span>
          </button>
          {open && <Tree node={k} prefix={p} depth={depth + 1} />}
        </div>
      );
    });
}

// --- AI assistant -------------------------------------------------------------
// The file browser's prompt box: ask the agent to find code, explain it, fix
// the build or sort out packages. It runs like a playground prompt, with the
// open file and the last compile output as context; file paths in the answer
// open here, and the project can be compiled again when the agent finishes.

const fa = () => (S.fa ||= { prompt: "", runId: "", auto: true, kind: "", seen: "", model: "" });

const tail = (s, n) => (s.length > n ? "…" + s.slice(-n) : s);

function compose(text, kind) {
  const f = S.files,
    c = f.compile,
    p = S.ws.profile || {};
  const parts = [
    `You are helping in SpecWright's file browser for the project "${S.ws.name}" (${p.lang || "unknown language"}, built with ${p.build || "its usual build tool"}). Work only inside the workspace.`,
  ];
  if (f.sel) parts.push(`The user has ${f.sel} open${f.line ? ` at line ${f.line}` : ""}.`);
  if (c && !c.running && c.out)
    parts.push(
      `The last compile (${c.cmd || "build"}) ${c.ok ? "succeeded" : "FAILED"}. Its output:\n\`\`\`\n${tail(c.out, 6000)}\n\`\`\``,
    );
  if (kind === "fix")
    parts.push(
      "Fix the compile errors with the smallest correct changes to the source and build files, then compile again with the project's build tool and repeat until it builds. Do not delete or skip tests to make it pass.",
    );
  if (kind === "packages")
    parts.push(
      "Find missing, wrong or conflicting dependencies: compare the imports in the code with the build file (pom.xml, build.gradle, package.json, go.mod, requirements.txt, …). Add or correct them with pinned versions, download them with the project's build tool (for example `./mvnw -q -B dependency:resolve`, `npm install`, `go mod tidy`, `pip install -r requirements.txt`), then compile to confirm. The corporate proxy and registry settings are already in the environment when they are turned on; do not change global configuration.",
    );
  if (kind === "compile")
    parts.push(
      "Compile the project with its build tool. If it fails, fix the errors and compile again until it builds; then summarise what you changed.",
    );
  parts.push(`Request: ${text}`);
  parts.push(
    "When you mention files, write workspace-relative paths (with :line where useful) so the user can click them. Keep the answer short.",
  );
  return parts.join("\n\n");
}

const ACTIONS = [
  {
    kind: "fix",
    label: "Fix the build errors",
    text: "Fix the build errors",
    when: () => S.files.compile && !S.files.compile.running && !S.files.compile.ok,
  },
  {
    kind: "packages",
    label: "Install or fix packages",
    text: "Install or fix the project's packages and dependencies",
  },
  {
    kind: "compile",
    label: "Compile and fix until it builds",
    text: "Compile the project and fix anything that fails",
  },
  {
    kind: "",
    label: "Explain this file",
    text: "Explain what the open file does and how it fits in the project",
    when: () => !!S.files.sel,
  },
  { kind: "", label: "Where is … used?", text: "Find where  is defined and used", caret: 15 },
  { kind: "", label: "Find TODOs", text: "List the open TODOs and FIXMEs with file and line" },
];

async function ask(text, kind) {
  const a = fa();
  text = (text ?? a.prompt).trim();
  if (!text) return toast("Type what you want the AI to do.");
  try {
    const id = await backend.RunPrompt(compose(text, kind ?? a.kind), { tools: true, model: a.model || "" });
    S.loaded[id] = true;
    S.events[id] ||= [];
    Object.assign(a, { runId: id, asked: text, kind: "", prompt: "", seen: "" });
    if (kind === "fix" || kind === "packages" || kind === "compile") a.auto = true;
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// Open a workspace path (optionally :line) from the AI's answer.
function openPath(path, line) {
  const f = S.files;
  f.sel = path;
  f.line = line || 0;
  const parts = path.split("/");
  for (let k = 1; k < parts.length; k++) f.open[parts.slice(0, k).join("/")] = true;
  update();
}

function Assistant({ onHide }) {
  const a = fa(),
    box = useRef(null),
    f = S.files;
  const evs = a.runId ? S.events[a.runId] || [] : [];
  const state = a.runId ? stateOf(evs) : "",
    running = state === "running";
  const answer = evs
    .filter((e) => e.type === "model.text")
    .map((e) => e.text)
    .join("\n\n");
  const calls = toolCalls(evs),
    u = usageOf(evs);
  const problems = evs.filter((e) => ["error", "loop.detected", "budget.exceeded"].includes(e.type));
  useEffect(() => {
    if (!S.pkg) loadPackages();
  }, []);
  // when the agent finishes: refresh the tree and the open file, compile again if asked
  useEffect(() => {
    if (!a.runId || running || !evs.length || a.seen === a.runId) return;
    a.seen = a.runId;
    backend
      .ListFiles()
      .then((l) => {
        f.list = l || [];
        update();
      })
      .catch(() => {});
    reloadView();
    if (a.auto && state === "done" && u.files.length) compile();
  }, [a.runId, running, evs.length]);
  const busy = running || (S.wf?.steps || []).some((s) => s.status === "running" || s.status === "paused");
  const noProxy = S.pkg?.saved && !S.pkg.saved.applyToAgents;
  return (
    <div className="card fa">
      <div className="card-head">
        <Icon.spark />
        <h3>Ask AI</h3>
        <span className="muted" style={{ fontWeight: 400 }}>
          find, explain, fix the build, install packages
        </span>
        {onHide && (
          <button
            className="icon-btn fa-hide"
            aria-label="Hide the Ask AI panel"
            title="Hide the Ask AI panel"
            onClick={onHide}
          >
            <Icon.x />
          </button>
        )}
        {a.runId && (
          <button
            className="link"
            style={{ marginLeft: "auto" }}
            onClick={() => {
              a.runId = "";
              update();
            }}
          >
            Clear
          </button>
        )}
      </div>
      <div className="fa-input">
        <textarea
          ref={box}
          rows={2}
          value={a.prompt}
          placeholder={
            f.sel
              ? `e.g. "Why does ${f.sel.split("/").pop()} fail to compile?" or "Where is OrderPlaced published?"  (⌘/Ctrl + Enter)`
              : "Ask the AI about this project…"
          }
          onChange={(e) => {
            a.prompt = e.target.value;
            update();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              ask();
            }
          }}
        />
        {running ? (
          <button
            className="btn secondary"
            onClick={() => backend.CancelRun(a.runId).catch((e) => toast(errMsg(e), "error"))}
          >
            Stop
          </button>
        ) : (
          <button className="btn primary" disabled={busy || !a.prompt.trim()} onClick={() => ask()}>
            Ask
          </button>
        )}
      </div>
      <div className="chips" style={{ marginTop: 8 }}>
        {ACTIONS.filter((x) => !x.when || x.when()).map((x) => (
          <Chip
            key={x.label}
            onClick={() => {
              if (x.caret) {
                a.prompt = x.text;
                a.kind = x.kind;
                update();
                setTimeout(() => {
                  box.current?.focus();
                  box.current?.setSelectionRange(x.caret, x.caret);
                }, 0);
                return;
              }
              if (!busy) ask(x.text, x.kind);
            }}
          >
            {x.kind === "fix" ? <b>{x.label}</b> : x.label}
          </Chip>
        ))}
      </div>
      <div className="row-actions" style={{ marginTop: 6 }}>
        <label className="check">
          <input
            type="checkbox"
            checked={a.auto}
            onChange={(e) => {
              a.auto = e.target.checked;
              update();
            }}
          />{" "}
          Compile again when the AI changes files
        </label>
        {noProxy && (
          <span className="muted">
            AI runs don't use your proxy and registry settings yet: turn on "Also use the proxy…" under{" "}
            <button
              className="link"
              onClick={() => {
                S.view = "wizard";
                update();
              }}
            >
              Development wizard › Packages & proxy
            </button>
            .
          </span>
        )}
      </div>
      {a.runId && (
        <div className="fa-result">
          <div className="fa-asked">
            <b>You:</b> {a.asked || "(prompt)"}{" "}
            {running ? null : (
              <Pill
                s={state}
                label={{ done: "Done", failed: "Failed", cancelled: "Stopped" }[state] || state}
              />
            )}
          </div>
          {problems.map((e) => (
            <div key={e.seq} className="banner error">
              <FileLinkify text={e.summary} />
            </div>
          ))}
          {answer ? (
            <div className="row assistant pg-text fa-answer">
              <FileLinkify text={answer} />
              {running && <span className="caret" />}
            </div>
          ) : null}
          {running && <AiProgress evs={evs} />}
          {calls.length > 0 && (
            <div className="fa-steps">
              {calls.map((c, k) => {
                const name = /extension_managed$/.test(c.call.tool || "")
                  ? "project tool " + (c.call.args?.name || "")
                  : (c.call.tool || "").replace(/^mcp__agentbridge__/, "");
                const arg =
                  c.call.args?.path ||
                  c.call.args?.file_path ||
                  c.call.args?.command ||
                  c.call.args?.query ||
                  c.call.args?.pattern ||
                  "";
                return (
                  <span
                    key={k}
                    className={"fa-step" + (c.res && !c.res.ok ? " is-error" : "")}
                    title={c.call.summary}
                  >
                    {!c.res ? <span className="spinner" /> : null}
                    {name}
                    {arg ? (
                      <>
                        {" "}
                        <FileLinkify text={String(arg).slice(0, 120)} />
                      </>
                    ) : (
                      ""
                    )}
                  </span>
                );
              })}
            </div>
          )}
          {u.files.length > 0 && (
            <div className="fa-changed">
              <span className="muted">Changed:</span>{" "}
              {u.files.map((x) => (
                <button key={x.path} className="link fa-path" onClick={() => openPath(x.path)}>
                  {x.path}
                </button>
              ))}
            </div>
          )}
          {!running && evs.length > 0 && (
            <div className="muted fa-usage">
              {fmtMs(u.ms)} · {fmtK(u.in + u.out)} tokens · {fmtCredits(u)} · {u.tools} tool calls ·{" "}
              <button
                className="link"
                onClick={() => {
                  S.pg ||= { prompt: "", tools: true, model: "", runs: [], sel: "", loaded: false };
                  S.pg.loaded = false;
                  S.pg.sel = a.runId;
                  S.view = "playground";
                  update();
                }}
              >
                Full details in the playground
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

async function compile() {
  S.files.compile = { running: true, ok: false, out: "", cmd: "" };
  update();
  try {
    const r = await backend.Compile();
    S.files.compile = { running: false, ok: r.ok, out: r.output, cmd: r.command };
  } catch (e) {
    S.files.compile = { running: false, ok: false, out: errMsg(e), cmd: "" };
  }
  update();
}

export function Files() {
  const f = S.files;
  useEffect(() => {
    if (S.ws)
      backend
        .ListFiles()
        .then((l) => {
          l = l || [];
          f.list = l;
          if (!f.sel && l.length)
            f.sel = l.find((x) => x.startsWith("specs/") || x.startsWith("src/")) || l[0];
          update();
        })
        .catch((e) => toast(errMsg(e), "error"));
  }, [S.ws?.root]);
  useEffect(() => {
    if (f.sel) return loadFileInto(view(), f.sel);
  }, [f.sel, f.list.length]);
  if (!S.ws) return <NeedWorkspace what="files" />;
  const c = f.compile,
    aiHidden = !!S.panes["files:ai:c"];
  const toggleAI = () => {
    if (aiHidden) delete S.panes["files:ai:c"];
    else S.panes["files:ai:c"] = 1;
    update();
  };
  return (
    <div className={"files" + (aiHidden ? "" : " files-ai")}>
      <Pane k="files" v="--pane-l" pane=".ftree" min={200} max={560} label="Resize the file tree" />
      {!aiHidden && (
        <Pane
          k="files-ai"
          v="--pane-r"
          pane=".fa-side"
          edge="left"
          min={300}
          max={720}
          label="Resize the Ask AI panel"
        />
      )}
      <nav className="ftree" aria-label="Files, packages and folders">
        <h1 style={{ padding: "0 8px 8px" }}>Files</h1>
        <input
          className="tree-filter"
          type="search"
          placeholder="Filter files"
          aria-label="Filter files by name or path"
          value={f.q || ""}
          onChange={(e) => {
            f.q = e.target.value;
            update();
          }}
        />
        <div className="tree-folder" title={S.ws.root}>
          {S.ws.name}
        </div>
        {(() => {
          const q = (f.q || "").toLowerCase().trim(),
            list = q ? f.list.filter((p) => p.toLowerCase().includes(q)) : f.list;
          return list.length ? (
            <Tree node={buildTree(list)} prefix="" depth={0} />
          ) : (
            <p className="muted" style={{ padding: "4px 8px" }}>
              No file matches “{f.q}”.
            </p>
          );
        })()}
      </nav>
      <div className="fview">
        <header className="view-head">
          <h1 style={{ fontFamily: "var(--font-mono)", fontSize: "1em", wordBreak: "break-all" }}>
            {f.sel || "No file selected"}
          </h1>
          <div className="head-controls head-model">
            {aiHidden && (
              <button className="btn secondary" onClick={toggleAI} title="Show the Ask AI panel">
                <Icon.spark /> Ask AI
              </button>
            )}
            <ModelPicker
              value={fa().model || ""}
              onChange={(v) => {
                fa().model = v;
                update();
              }}
              label="Model"
            />
            <button className="btn primary" disabled={c?.running} onClick={compile}>
              {c?.running ? "Compiling…" : "Compile project"}
            </button>
          </div>
        </header>
        {c && (
          <div className="card">
            <div className="card-head">
              <h3>
                Compilation{" "}
                {c.cmd && (
                  <span className="muted" style={{ fontWeight: 400 }}>
                    <code>{c.cmd}</code>
                  </span>
                )}
              </h3>
              {c.running ? (
                <span className="spinner" />
              ) : (
                <Pill s={c.ok ? "done" : "failed"} label={c.ok ? "Passed" : "Failed"} />
              )}
            </div>
            <pre className="code console-out">{c.out ? <FileLinkify text={c.out} /> : "…"}</pre>
          </div>
        )}
        {f.sel && (
          <div className="fv-card">
            <FileView slot={view()} line={f.line} />
          </div>
        )}
      </div>
      {!aiHidden && (
        <aside className="fa-side" aria-label="Ask AI about the project">
          <Assistant onHide={toggleAI} />
        </aside>
      )}
    </div>
  );
}
