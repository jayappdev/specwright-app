// Copilot CLI extensions in Tools: yours (~/.copilot/extensions/<name>) and
// the project's (.github/extensions/<name>). One of yours can be loaded into
// the project, so it travels with the repository and the app's Copilot runs
// use it; its npm packages can be installed there in the same step.
import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, refreshExtensions } from "../store.js";
import { Icon } from "../ui.jsx";
import { FileLink } from "../FileLinks.jsx";
import { confirmDialog } from "../Dialog.jsx";
import { fmtSize } from "../Attachments.jsx";

const cx = () =>
  (S.cx ||= { list: null, sel: "", source: "", busy: false, install: true, log: [], loadedFor: "" });

export async function loadCopilotExts(force) {
  const c = cx(),
    root = S.ws?.root || "";
  if (c.list && c.loadedFor === root && !force) return;
  c.loadedFor = root;
  c.list = (await backend.CopilotExtensions?.().catch(() => [])) || [];
  update();
}

export function selectCopilotExt(name, source) {
  const c = cx();
  c.sel = name;
  c.source = source;
  c.log = [];
  S.bt = null;
  S.view = "tools";
  update();
}

const keyOf = (x) => x.source + ":" + x.name;
const statusOf = (x) =>
  x.source === "project"
    ? x.fromUser
      ? "Loaded from yours"
      : "Project only"
    : !x.inProject
      ? "Not in this project"
      : x.projectSame
        ? "In this project"
        : "Project copy differs";
const toneOf = (x) => (x.source === "project" ? "ok" : !x.inProject ? "" : x.projectSame ? "ok" : "warn");

export function CopilotExtList() {
  const c = cx();
  useEffect(() => {
    loadCopilotExts();
  }, [S.ws?.root]);
  const list = c.list || [];
  if (!list.length) return null;
  const groups = [
    ["user", "Yours · ~/.copilot/extensions"],
    ["project", "This project · .github/extensions"],
  ];
  return (
    <div className="cx-list">
      <div className="tree-folder">Copilot extensions · {list.length}</div>
      {groups.map(([g, l]) => {
        const items = list.filter((x) => x.source === g);
        if (!items.length) return null;
        return (
          <div key={g}>
            <div className="bt-group">{l}</div>
            {items.map((x) => (
              <button
                key={keyOf(x)}
                className={
                  "tree-item bt-item cx-item" + (c.sel === x.name && c.source === x.source ? " active" : "")
                }
                onClick={() => selectCopilotExt(x.name, x.source)}
              >
                <span className="cx-item-name">
                  <CopilotGlyph /> <code>{x.name}</code>
                </span>
                <span
                  className={"cx-dot cx-" + (toneOf(x) || "none")}
                  title={statusOf(x)}
                  aria-label={statusOf(x)}
                />
              </button>
            ))}
          </div>
        );
      })}
    </div>
  );
}

// The same extensions as tiles, for the Development wizard's "Tools and
// extensions" card: load one of yours into the project from there, or open
// it in Tools.
export function CopilotExtTiles() {
  const c = cx();
  useEffect(() => {
    loadCopilotExts();
  }, [S.ws?.root]);
  const list = c.list || [];
  const groups = [
    ["user", "GitHub Copilot extensions · yours (~/.copilot/extensions)"],
    ["project", "GitHub Copilot extensions · this project (.github/extensions)"],
  ];
  if (c.list && !list.length)
    return (
      <section className="tool-group">
        <div className="tree-folder">GitHub Copilot extensions · 0</div>
        <p className="muted tool-empty">
          None found in <code className="inline-code">~/.copilot/extensions</code> or the project's{" "}
          <code className="inline-code">.github/extensions</code>.
        </p>
      </section>
    );
  return groups.map(([g, l]) => {
    const items = list.filter((x) => x.source === g);
    if (!items.length) return null;
    return (
      <section key={g} className="tool-group">
        <div className="tree-folder">
          {l} · {items.length}
        </div>
        <div className="tool-grid">
          {items.map((x) => {
            const canLoad = g === "user" && !x.inProject,
              canUpdate = g === "user" && x.inProject && !x.projectSame;
            const open = () => selectCopilotExt(x.name, x.source);
            return (
              <div
                key={keyOf(x)}
                className="tool-tile cx-tile"
                role="button"
                tabIndex={0}
                title={`${x.name}: ${x.desc || ""}`}
                onClick={open}
                onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), open())}
              >
                <div className="tt-top">
                  <span className="tool-icon cx-tile-icon">
                    <CopilotGlyph />
                    <span className="tool-lang">CP</span>
                  </span>
                  <span className={"badge cx-badge cx-" + (toneOf(x) || "none")}>{statusOf(x)}</span>
                </div>
                <b className="tt-name">{x.name}</b>
                {x.desc && <small className="tt-desc">{x.desc}</small>}
                <div className="tt-foot">
                  <span className="muted">
                    {x.tools.length} tool{x.tools.length === 1 ? "" : "s"}
                    {x.tools.length ? ": " + x.tools.map((t) => t.name).join(", ") : ""}
                  </span>
                  {canLoad || canUpdate ? (
                    <button
                      className="btn primary btn-sm"
                      disabled={c.busy}
                      onClick={(e) => {
                        e.stopPropagation();
                        load(x, canUpdate);
                      }}
                    >
                      {c.busy ? "Loading…" : canUpdate ? "Update" : "Load into project"}
                    </button>
                  ) : (
                    <span className="link">Open</span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </section>
    );
  });
}

const CopilotGlyph = () => (
  <svg className="cx-glyph" viewBox="0 0 16 16" aria-hidden="true">
    <path
      d="M3 9.5c0-3 2.2-5.5 5-5.5s5 2.5 5 5.5v1.2c0 .9-.7 1.8-1.7 2.1L8 14l-3.3-1.2C3.7 12.5 3 11.6 3 10.7z"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
    />
    <circle cx="6.2" cy="9.3" r=".9" fill="currentColor" />
    <circle cx="9.8" cy="9.3" r=".9" fill="currentColor" />
  </svg>
);

async function load(x, replace) {
  const c = cx();
  if (
    replace &&
    !(await confirmDialog(
      `The project's copy of ${x.name} goes to SpecWright's trash and is replaced with yours.`,
      { title: "Update the project copy?", ok: "Replace" },
    ))
  )
    return;
  c.busy = true;
  c.log = [];
  update();
  try {
    const r = await backend.LoadCopilotExtension(
      x.name,
      !!replace,
      c.install && Object.keys(x.deps || {}).length > 0,
    );
    c.log = r.log || [];
    if (r.note) c.log = [...c.log, "Not made into tools yet: " + r.note];
    toast(
      r.tools?.length
        ? `Loaded ${x.name}: ${r.tools.join(", ")} ${r.tools.length === 1 ? "is now a project tool" : "are now project tools"}.`
        : `Loaded ${x.name} into ${r.path}.`,
    );
    refreshExtensions();
  } catch (e) {
    toast(errMsg(e), "error");
    c.log = [errMsg(e)];
  }
  c.busy = false;
  await loadCopilotExts(true);
}

async function remove(x) {
  const tools = (S.ws?.extensions || [])
    .filter((t) => t.kind === "copilot" && t.dir === ".github/extensions/" + x.name)
    .map((t) => t.name);
  if (
    !(await confirmDialog(
      `.github/extensions/${x.name}${tools.length ? ` and its tools (${tools.join(", ")})` : ""} move to .codegen/archive/<date-time>/ in the project, so you can restore them. Your own copy in ~/.copilot is not touched.`,
      { title: `Delete ${x.name} from this project?`, ok: "Delete", danger: true },
    ))
  )
    return;
  try {
    const r = await backend.RemoveCopilotExtension(x.name);
    toast(`Deleted ${x.name} from the project. Archived in ${r.path}.`);
    const c = cx();
    c.sel = "";
    refreshExtensions();
  } catch (e) {
    toast(errMsg(e), "error");
  }
  await loadCopilotExts(true);
}

export function CopilotExtView() {
  const c = cx();
  const x = (c.list || []).find((e) => e.name === c.sel && e.source === c.source);
  if (!x)
    return (
      <div className="card">
        <p className="muted">That extension is no longer there.</p>
      </div>
    );
  const deps = Object.entries(x.deps || {});
  const user = x.source === "user";
  const twin = (c.list || []).find((e) => e.name === x.name && e.source !== x.source);
  const rel = ".github/extensions/" + x.name;
  return (
    <>
      <header className="view-head">
        <h1>
          <span className="cx-title-glyph">
            <CopilotGlyph />
          </span>{" "}
          {x.name}
        </h1>
        <span className="badge">{user ? "your extension" : "project extension"}</span>
        <span className={"badge cx-badge cx-" + (toneOf(x) || "none")}>{statusOf(x)}</span>
      </header>

      <div className="card cx-hero">
        <p className="cx-desc">{x.desc || "No description."}</p>
        <dl className="kv">
          <dt>Folder</dt>
          <dd>
            <code className="inline-code">{x.dir}</code>
          </dd>
          <dt>Entry</dt>
          <dd>
            <FileLink path={x.dir + "/" + x.entry}>{x.entry}</FileLink>
          </dd>
          <dt>Files</dt>
          <dd>
            {x.files.length} · {fmtSize(x.size)} <span className="muted">(node_modules not counted)</span>
          </dd>
          <dt>Packages</dt>
          <dd>
            {deps.length ? (
              deps.map(([k, v]) => (
                <span key={k} className="badge cx-dep">
                  {k}@{v}
                </span>
              ))
            ) : (
              <span className="muted">none</span>
            )}
            {deps.length > 0 && <span className="muted"> · {x.modules ? "installed" : "not installed"}</span>}
          </dd>
        </dl>
        {user ? (
          <div className="cx-actions">
            {!x.inProject && (
              <button className="btn primary" disabled={c.busy} onClick={() => load(x, false)}>
                <Icon.plus /> {c.busy ? "Loading…" : "Load into this project"}
              </button>
            )}
            {x.inProject && !x.projectSame && (
              <button className="btn primary" disabled={c.busy} onClick={() => load(x, true)}>
                {c.busy ? "Updating…" : "Update the project copy"}
              </button>
            )}
            {x.inProject && x.projectSame && (
              <span className="cx-same">
                <Icon.check /> The project has the same copy at{" "}
                <FileLink path={S.ws.root + "/" + rel}>{rel}</FileLink>
              </span>
            )}
            {deps.length > 0 && !(x.inProject && x.projectSame) && (
              <label className="check">
                <input
                  type="checkbox"
                  checked={c.install}
                  disabled={c.busy}
                  onChange={(e) => {
                    c.install = e.target.checked;
                    update();
                  }}
                />{" "}
                Install its npm packages there ({deps.map(([k]) => k).join(", ")})
              </label>
            )}
            {twin && (
              <button className="link" onClick={() => selectCopilotExt(twin.name, twin.source)}>
                View the project copy
              </button>
            )}
          </div>
        ) : (
          <div className="cx-actions">
            {twin && (
              <button className="btn secondary" onClick={() => selectCopilotExt(twin.name, twin.source)}>
                View your copy
              </button>
            )}
            <button className="btn danger" onClick={() => remove(x)}>
              <Icon.x /> Delete from project
            </button>
          </div>
        )}
        {user && !x.inProject && (
          <p className="muted cx-where">
            Copies the folder to <code className="inline-code">{rel}</code> in <b>{S.ws.name}</b>, without{" "}
            <code className="inline-code">node_modules</code>. Your copy stays as it is.
          </p>
        )}
        {c.log.length > 0 && <pre className="cx-log">{c.log.join("\n")}</pre>}
      </div>

      {!user && <ProjectToolsCard x={x} />}
      <div className="card">
        <div className="card-head">
          <h3>Tools it adds</h3>
          <span className="badge">{x.tools.length}</span>
        </div>
        {x.tools.length ? (
          <ul className="cx-tools">
            {x.tools.map((t) => (
              <li key={t.name}>
                <code className="perm-tool">{t.name}</code>
                <span>{t.desc}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">Could not read the tool list from {x.entry}; it is still loaded by Copilot.</p>
        )}
      </div>

      <div className="card">
        <div className="card-head">
          <h3>Files</h3>
        </div>
        <ul className="cx-files">
          {x.files.map((f) => (
            <li key={f}>
              <Icon.file /> <FileLink path={x.dir + "/" + f}>{f}</FileLink>
            </li>
          ))}
        </ul>
      </div>

      <div className="card cx-help">
        <h3>How it is used</h3>
        <ul>
          <li>
            <b>Copilot CLI</b> loads project extensions from{" "}
            <code className="inline-code">.github/extensions/</code> once you trust the folder (extensions are
            experimental: <code className="inline-code">copilot --experimental</code>).
          </li>
          <li>
            <b>SpecWright's Copilot runs</b> (workflow steps, Ask AI, flows) load them automatically when the
            project has any.
          </li>
          <li>
            <b>Share it</b> by committing <code className="inline-code">{rel}</code>; installed packages are
            kept out of git.
          </li>
          <li>
            Claude Code does not use Copilot extensions; for tools both agents can call, create a project tool
            with <b>+ New</b>.
          </li>
        </ul>
      </div>
    </>
  );
}

// --- a Copilot extension's tool, opened from the tool grid -----------------

const extOfTool = (d) => (d.dir || "").split("/").pop();

/** Review stage for a tool that comes from a Copilot extension (read-only:
 * it changes by editing the extension's files). */
export function CopilotToolReview({ d, onNext }) {
  const ext = extOfTool(d),
    needs = d.status === "needs approval" || !d.approved;
  return (
    <>
      <div className="card cx-hero">
        <div className="card-head">
          <CopilotGlyph />
          <h3>Copilot extension tool</h3>
          <span className="badge badge-info">Node.js</span>
          <span className={"badge cx-badge cx-" + (needs ? "warn" : "ok")}>
            {needs ? "needs approval" : d.status === "tested" ? "tested" : "approved"}
          </span>
        </div>
        <dl className="kv">
          <dt>Name</dt>
          <dd>
            <code className="inline-code">{d.name}</code>
            {d.tool && d.tool !== d.name && (
              <span className="muted">
                {" "}
                · <code className="inline-code">{d.tool}</code> in the extension
              </span>
            )}
          </dd>
          <dt>Description</dt>
          <dd>{d.desc || <span className="muted">none</span>}</dd>
          <dt>From</dt>
          <dd>
            <FileLink path={S.ws.root + "/" + d.dir + "/" + d.entry}>
              {d.dir}/{d.entry}
            </FileLink>{" "}
            ·{" "}
            <button
              className="link"
              onClick={() => {
                loadCopilotExts(true).then(() => selectCopilotExt(ext, "project"));
              }}
            >
              Open extension
            </button>
          </dd>
          <dt>Packages</dt>
          <dd>
            {d.deps?.length ? d.deps.join(", ") : <span className="muted">none</span>}
            {d.deps?.length ? <span className="muted"> · installed in its folder on first use</span> : null}
          </dd>
        </dl>
        <p className="muted" style={{ marginTop: 10 }}>
          Works like any project tool: agents (Claude and Copilot) can call it, flows can use it as a step,
          and you can test it with AI. It runs the extension's own code with Node.js in the project folder;
          Copilot hooks and session events are not available outside Copilot. To change it, edit the
          extension's files; approval covers every file, so a change needs approving again.
        </p>
        {needs && (
          <div className="banner" style={{ marginTop: 10, borderColor: "var(--warn)" }}>
            {d.approved
              ? "The extension's files changed since it was approved."
              : "This extension came with the project and has not been approved on this computer."}{" "}
            Approving loads it once to read its tools.
          </div>
        )}
      </div>
      <div className="card">
        <h3>Parameters</h3>
        {d.params?.length ? (
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Required</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {d.params.map((p) => (
                <tr key={p.name}>
                  <td>
                    <code>{p.name}</code>
                  </td>
                  <td>{p.type}</td>
                  <td>{p.required ? "yes" : ""}</td>
                  <td>{p.desc}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="muted">{needs ? "Read when you approve the extension." : "No parameters."}</p>
        )}
      </div>
      <div className="row-actions">
        {needs ? (
          <button className="btn primary" onClick={() => approveCopilotTool(d)}>
            Approve for agents
          </button>
        ) : (
          <button className="btn primary" onClick={onNext}>
            Next: test with AI
          </button>
        )}
        <span className="status-spacer" />
        <button
          className="btn danger"
          onClick={async () => {
            const { archiveTool } = await import("./Tools.jsx");
            archiveTool(d);
          }}
        >
          <Icon.x /> Delete tool
        </button>
      </div>
    </>
  );
}

export async function approveCopilotTool(d) {
  try {
    const rec = await backend.ApproveExtension(d.name);
    await refreshExtensions();
    S.tb = { ...S.tb, editing: rec.name, draft: JSON.parse(JSON.stringify(rec)) };
    toast(`${extOfTool(rec)} approved: its tools are available to agents and flows.`);
    loadCopilotExts(true);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// For a project extension: the project tools it became (or approval).
function ProjectToolsCard({ x }) {
  const rel = ".github/extensions/" + x.name;
  const mine = (S.ws?.extensions || []).filter((t) => t.kind === "copilot" && t.dir === rel);
  const needs = mine.some((t) => t.status === "needs approval" || !t.approved);
  return (
    <div className={"card" + (needs ? " cx-needs" : "")}>
      <div className="card-head">
        <h3>As project tools</h3>
        <span className={"badge cx-badge cx-" + (needs ? "warn" : "ok")}>
          {needs ? "needs approval" : "available to agents and flows"}
        </span>
      </div>
      <p className="wf-desc">
        {needs
          ? "Approve it to use its tools like any other project tool: Claude and Copilot can call them, flows can use them as steps, and you can test them with AI. Approving loads the extension once to read its tools."
          : "Its tools are project tools: Claude and Copilot can call them, the flow designer lists them, and you can test them with AI."}
      </p>
      <div className="cx-tool-links">
        {mine.map((t) => (
          <button
            key={t.name}
            className="chip"
            onClick={async () => {
              const { openTool } = await import("./Tools.jsx");
              openTool(t);
            }}
          >
            <code>{t.name}</code> · {t.status}
          </button>
        ))}
      </div>
      {needs && mine[0] && (
        <div className="row-actions">
          <button className="btn primary" onClick={() => approveCopilotTool(mine[0])}>
            Approve for agents
          </button>
        </div>
      )}
    </div>
  );
}
