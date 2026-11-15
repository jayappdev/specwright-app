import { useState } from "react";
import { backend } from "../backend.js";
import { Linkify } from "../FileLinks.jsx";
import { S, update, saveProfile, toast, call, saveText, openWorkspace } from "../store.js";
import { Icon, Seg, Chip, Card } from "../ui.jsx";
import { newTool, openTool } from "./Tools.jsx";
import { RichText } from "../RichEditor.jsx";
import { mdToRules, rulesToMd, rulesToHtmlDoc } from "../richtext.js";
import { openRuleDrawer, openBulkEditor } from "./RulesEditors.jsx";
import { PackagesCard } from "./Packages.jsx";
import { openLibraryEditor, deleteLibrary, deleteSkillFile } from "./LibraryEditor.jsx";
import { ToolTile, ToolGrid } from "../ToolTile.jsx";
import { CopilotExtTiles } from "./CopilotExts.jsx";

const WIZ = [
  ["w-lang", "Language"],
  ["w-global", "Corporate libraries"],
  ["w-project", "Project skills"],
  ["w-folders", "Folder structure"],
  ["w-rules", "Rules"],
  ["w-tools", "Tools & extensions"],
  ["w-packages", "Packages & proxy"],
];

export function NeedWorkspace({ what = "this view" }) {
  return (
    <section className="view">
      <div className="card">
        <h3>Open a workspace folder first</h3>
        <p className="wf-desc">
          Choose the folder the agent works in to use {what}. Only that folder is visible to the agent.
        </p>
        <WorkspaceBar />
      </div>
    </section>
  );
}

export function WorkspaceBar() {
  const v = S._wsInput ?? S.ws?.root ?? S.lastWorkspace ?? "";
  return (
    <>
      <div className="setup-input">
        <input
          aria-label="Workspace folder"
          style={{ fontFamily: "var(--font-mono)" }}
          value={v}
          placeholder="~/src/my-workspace"
          onChange={(e) => {
            S._wsInput = e.target.value;
            update();
          }}
          onKeyDown={(e) => e.key === "Enter" && openWorkspace(v)}
        />
        <button className="btn secondary" onClick={() => openWorkspace(v)}>
          Open
        </button>
        {backend.kind === "desktop" && (
          <button className="btn secondary" onClick={() => openWorkspace("", true)}>
            Browse…
          </button>
        )}
      </div>
      {S.wsError && (
        <p className="err-text" style={{ marginTop: 8 }}>
          {S.wsError}
        </p>
      )}
    </>
  );
}

// each section builds rules from its own content: name it, so the three rows do not look like one action repeated
const BUILD_FROM = {
  globalMode: "corporate libraries",
  projectMode: "project skills and scripts",
  foldersMode: "folder structure",
};
function BuildRow({ modeKey }) {
  const p = S.ws.profile,
    from = BUILD_FROM[modeKey];
  return (
    <div className="sub-row">
      <span>
        <b>Build rules{from ? " from the " + from : ""}</b>{" "}
        <span className="muted">· incremental adds what changed; bulk reloads everything</span>
      </span>
      <span style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <Seg
          value={p[modeKey]}
          options={[
            ["incremental", "Incremental"],
            ["bulk", "Bulk load"],
          ]}
          onChange={(v) => {
            p[modeKey] = v;
            update();
            saveProfile();
          }}
        />
        <button
          type="button"
          className="btn secondary"
          onClick={async () => {
            await backend.SaveProfile(p);
            toast(await call(backend.BuildRules, "wizard." + modeKey, p[modeKey]));
          }}
        >
          Build
        </button>
      </span>
    </div>
  );
}

// The files in a project skill's folder besides SKILL.md, grouped by folder.
function SkillFiles({ l, canEdit, scope = "project" }) {
  const files = l.files || [],
    [all, setAll] = useState(false);
  const shown = all ? files : files.slice(0, 8);
  const openFile = (f) =>
    openLibraryEditor(scope, /^scripts\/[^/]+$/.test(f) ? "script" : "doc", null, { skill: l.name, file: f });
  return (
    <div className="skill-files">
      {files.length > 0 && (
        <ul>
          {shown.map((f) => {
            const i = f.lastIndexOf("/");
            return (
              <li key={f}>
                {/^scripts\//.test(f) ? <Icon.terminal /> : <Icon.file />}
                {canEdit ? (
                  <button className="link sf-name" title={"Edit " + f} onClick={() => openFile(f)}>
                    <span className="muted">{f.slice(0, i + 1)}</span>
                    {f.slice(i + 1)}
                  </button>
                ) : (
                  <span className="link sf-name" role="link" tabIndex={0} data-file={(l.dir || "") + "/" + f}>
                    <span className="muted">{f.slice(0, i + 1)}</span>
                    {f.slice(i + 1)}
                  </span>
                )}
                {canEdit && (
                  <button
                    className="sf-del"
                    aria-label={"Delete " + f}
                    title={"Delete " + f}
                    onClick={() => deleteSkillFile(l.name, f, scope)}
                  >
                    <Icon.x />
                  </button>
                )}
              </li>
            );
          })}
          {files.length > 8 && (
            <li>
              <button className="link" onClick={() => setAll(!all)}>
                {all ? "Show fewer" : `…and ${files.length - 8} more`}
              </button>
            </li>
          )}
        </ul>
      )}
      {canEdit && (
        <span className="sf-add">
          <button
            className="link"
            onClick={() => openLibraryEditor(scope, "script", null, { skill: l.name })}
          >
            <Icon.plus /> Script
          </button>
          <button className="link" onClick={() => openLibraryEditor(scope, "doc", null, { skill: l.name })}>
            <Icon.plus /> File or doc
          </button>
        </span>
      )}
    </div>
  );
}

function LibCard({ id, n, title, desc, scope, key2, modeKey }) {
  const p = S.ws.profile,
    sel = p[key2] || (p[key2] = []);
  const libs = (S.ws.libraries || []).filter((l) => l.scope === scope);
  const g = scope === "global",
    admin = !!S.admin?.unlocked,
    canEdit = !g || admin;
  return (
    <Card
      id={id}
      index={n}
      title={title}
      badge={
        <>
          {g && (
            <span className={"badge " + (admin ? "badge-warn" : "")}>
              <Icon.lock /> {admin ? "admin mode" : "managed by admin"}
            </span>
          )}
          <span className="badge">{sel.length} selected</span>
        </>
      }
    >
      <p className="wf-desc">{desc}</p>
      {g && !admin && (
        <p className="muted lib-note">
          <Icon.lock /> Corporate libraries are published by your admin for every project. You can read and
          select them; to add or change one, unlock admin mode on the Admin screen.
        </p>
      )}
      <ul className="lib-list">
        {libs.map((l) => (
          <li key={l.id}>
            <input
              type="checkbox"
              id={l.id}
              checked={sel.includes(l.id)}
              onChange={(e) => {
                e.target.checked ? sel.push(l.id) : sel.splice(sel.indexOf(l.id), 1);
                update();
                saveProfile();
              }}
            />
            <label className="grow" htmlFor={l.id}>
              <b>{l.name}</b>{" "}
              <span className={"badge " + (l.kind === "skill" ? "badge-info" : "badge-warn")}>{l.kind}</span>
              {g &&
                (l.managed ? (
                  <span
                    className="badge badge-ok"
                    title={`Signed by ${l.managed.signer} on ${new Date(l.managed.signedAt).toLocaleString()}${l.managed.source === "machine" ? " · machine catalog" : ""}`}
                  >
                    v{l.managed.version} · {l.managed.signer}
                  </span>
                ) : (
                  <span className="badge" title="A file in this project only, not published by an admin">
                    local copy
                  </span>
                ))}
              <small>
                <Linkify text={l.note || l.path} />
              </small>
              {l.kind === "skill" &&
                (() => {
                  // corporate skills: an unlocked admin edits the files of a published (app catalog) skill
                  const editFiles = g ? admin && !!l.managed && l.managed.source !== "machine" : canEdit;
                  return (
                    (editFiles || l.files?.length > 0) && (
                      <SkillFiles l={l} scope={scope} canEdit={editFiles} />
                    )
                  );
                })()}
            </label>
            <span className="lib-actions">
              {canEdit &&
                !(g && l.managed?.source === "machine") &&
                (g && !l.managed ? (
                  <button
                    className="link"
                    title="Sign this local copy and publish it to every project"
                    onClick={() => openLibraryEditor("global", "skill", l, { publish: true })}
                  >
                    Publish
                  </button>
                ) : (
                  <button className="link" onClick={() => openLibraryEditor(scope, l.kind, l)}>
                    Edit
                  </button>
                ))}
              {canEdit && !(g && !l.managed) && !(g && l.managed?.source === "machine") && (
                <button className="link" onClick={() => deleteLibrary(scope, l)}>
                  {g ? "Retire" : "Delete"}
                </button>
              )}
              {!canEdit && (
                <span className="link" role="link" tabIndex={0} data-file={l.path} title={"View " + l.path}>
                  View
                </span>
              )}
            </span>
          </li>
        ))}
        {!libs.length && (
          <li className="muted">
            {g ? (
              admin ? (
                "No corporate skills yet. Publish one for every project."
              ) : (
                "Your admin has not published any corporate skills yet."
              )
            ) : (
              <>
                No project skills yet. Press <b>New skill</b> to create one: it becomes a folder (
                <code className="inline-code">.codegen/skills/project/&lt;name&gt;/</code>) where you can then
                add docs in sub-folders and its own <code className="inline-code">scripts/</code>.
              </>
            )}
          </li>
        )}
      </ul>
      {canEdit && (
        <div className="row-actions">
          {g ? (
            <button className="btn secondary" onClick={() => openLibraryEditor("global", "skill", null)}>
              <Icon.plus /> New corporate skill
            </button>
          ) : (
            <>
              <button className="btn secondary" onClick={() => openLibraryEditor("project", "skill", null)}>
                <Icon.plus /> New skill
              </button>
              <button className="btn secondary" onClick={() => openLibraryEditor("project", "script", null)}>
                <Icon.plus /> New script
              </button>
            </>
          )}
        </div>
      )}
      <BuildRow modeKey={modeKey} />
    </Card>
  );
}

export function Wizard() {
  if (!S.ws) return <NeedWorkspace what="the wizard" />;
  const p = S.ws.profile,
    langs = S.info?.langOrder || ["Java", "Kotlin", "TypeScript", "Python", "C#", "Go"],
    builds = S.info?.languages || {};
  const exts = S.ws.extensions || [];
  const setRules = (r) => {
    p.rules = r;
    update();
    saveProfile();
  };
  const importRules = (f) =>
    f.text().then((t) => {
      let r;
      try {
        r = JSON.parse(t);
        if (!Array.isArray(r)) throw 0;
        r = r.map((x, k) => ({ id: x.id || "R-" + (k + 1), text: String(x.text || "") }));
      } catch {
        r = mdToRules(t);
      }
      setRules(r);
      toast(`Imported ${r.length} rules.`);
    });
  // one section at a time (default), or all of them in one scroll ("Show all sections", remembered)
  const all = !!S.layout["wiz:all"],
    cur = WIZ.some(([id]) => id === S.wizSection) ? S.wizSection : WIZ[0][0],
    ci = WIZ.findIndex(([id]) => id === cur);
  const go = (id) => {
    S.wizSection = id;
    update();
    if (all) document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    else document.querySelector("main")?.scrollTo({ top: 0 });
  };
  const count = {
    "w-global": (p.global || []).length,
    "w-project": (p.project || []).length,
    "w-rules": p.rules.length,
    "w-tools": exts.length,
  };
  return (
    <section className="view">
      <header className="view-head">
        <h1>Development wizard</h1>
        <div className="head-controls">
          <span className="muted">
            Profile saved to <code className="inline-code">.codegen/profile.json</code>
          </span>
          <button
            className="btn primary"
            onClick={() => {
              S.view = "inputs";
              update();
            }}
          >
            Next: choose input
          </button>
        </div>
      </header>
      <div className="wiz">
        <nav className="wiz-nav" aria-label="Wizard sections">
          {WIZ.map(([id, l], i) => (
            <a
              key={id}
              href={"#" + id}
              className={cur === id ? "active" : ""}
              aria-current={cur === id ? "step" : undefined}
              onClick={(e) => {
                e.preventDefault();
                go(id);
              }}
            >
              <span className="card-index">{i + 1}</span>
              <span className="grow">{l}</span>
              {count[id] ? <span className="badge">{count[id]}</span> : null}
            </a>
          ))}
          <label className="check wiz-all">
            <input
              type="checkbox"
              checked={all}
              onChange={(e) => {
                if (e.target.checked) S.layout["wiz:all"] = 1;
                else delete S.layout["wiz:all"];
                update();
              }}
            />{" "}
            Show all sections
          </label>
        </nav>
        <div className={"wiz-body" + (all ? "" : " one")}>
          {!all && <style>{`.wiz-body.one > [id]:not(#${cur}) { display: none; }`}</style>}
          <Card id="w-lang" index={1} title="Choose language">
            <div className="chips" role="group" aria-label="Language">
              {langs.map((l) => (
                <Chip
                  key={l}
                  on={p.lang === l}
                  onClick={() => {
                    p.lang = l;
                    p.build = (builds[l] || [""])[0];
                    update();
                    saveProfile();
                  }}
                >
                  {l}
                </Chip>
              ))}
            </div>
            <div className="row-actions">
              <label className="field inline">
                <span>Build tool</span>
                <select
                  value={p.build}
                  onChange={(e) => {
                    p.build = e.target.value;
                    update();
                    saveProfile();
                  }}
                >
                  {(builds[p.lang] || [p.build]).map((b) => (
                    <option key={b}>{b}</option>
                  ))}
                </select>
              </label>
            </div>
          </Card>
          <LibCard
            id="w-global"
            n={2}
            title="Global corporate libraries"
            desc="Corporate skills an admin publishes for every project in the app. They are copied into .codegen/skills/global/ here, read-only."
            scope="global"
            key2="global"
            modeKey="globalMode"
          />
          <LibCard
            id="w-project"
            n={3}
            title="Project-specific skills and scripts"
            desc="Skills and scripts that only apply to this project."
            scope="project"
            key2="project"
            modeKey="projectMode"
          />
          <Card id="w-folders" index={4} title="Compartmentalized folder structure">
            <p className="wf-desc">
              Where rules, skills, scripts and tools are stored. Each compartment is loaded separately so one
              team's rules never leak into another's.
            </p>
            <ul className="lib-list">
              {p.folders.map((f, i) => (
                <li key={i}>
                  <span style={{ color: "var(--muted)" }}>
                    <Icon.folder />
                  </span>
                  <input
                    aria-label="Folder path"
                    className="grow"
                    value={f.path}
                    style={{ fontFamily: "var(--font-mono)" }}
                    onChange={(e) => {
                      f.path = e.target.value;
                      update();
                      saveProfile();
                    }}
                  />
                  <span className="muted" style={{ flex: 1 }}>
                    {f.note}
                  </span>
                </li>
              ))}
            </ul>
            <BuildRow modeKey="foldersMode" />
          </Card>
          <Card
            id="w-rules"
            index={5}
            title="Functionality rules"
            badge={<span className="badge">{p.rules.length}</span>}
          >
            <p className="wf-desc">
              Readable rules the agent must follow, with formatting (bold, lists, code, links) kept as
              Markdown. Add them one at a time, or edit them all as one document.
            </p>
            <div className="sub-row" style={{ marginTop: 0, paddingTop: 0, borderTop: 0 }}>
              <span>
                <b>Editing</b>{" "}
                <span className="muted">
                  ·{" "}
                  {(p.rulesMode || "incremental") === "bulk"
                    ? "all rules in one editor; save as Markdown or rich text"
                    : "add or change one rule at a time in a side panel"}
                </span>
              </span>
              <span style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                <Seg
                  value={p.rulesMode || "incremental"}
                  options={[
                    ["incremental", "Incremental"],
                    ["bulk", "Bulk edit"],
                  ]}
                  onChange={(v) => {
                    p.rulesMode = v;
                    update();
                    saveProfile();
                  }}
                />
                {(p.rulesMode || "incremental") === "bulk" ? (
                  <button className="btn primary" onClick={openBulkEditor}>
                    Open bulk editor
                  </button>
                ) : (
                  <button className="btn primary" onClick={() => openRuleDrawer()}>
                    + Add rules
                  </button>
                )}
              </span>
            </div>
            <ul className="lib-list rule-list">
              {p.rules.map((r, i) => (
                <li key={r.id + i}>
                  <code className="inline-code">{r.id}</code>
                  {r.text.trim() ? (
                    <RichText md={r.text} className="grow rule-text" />
                  ) : (
                    <span className="grow muted">(empty)</span>
                  )}
                  <button className="link" onClick={() => openRuleDrawer(i)}>
                    Edit
                  </button>
                  <button className="link" onClick={() => setRules(p.rules.filter((_, k) => k !== i))}>
                    Remove
                  </button>
                </li>
              ))}
              {!p.rules.length && <li className="muted">No rules yet.</li>}
            </ul>
            <div className="row-actions">
              <span className="status-spacer" />
              <button
                className="btn secondary"
                onClick={() =>
                  saveText("codegen-rules.json", JSON.stringify(p.rules, null, 2), "application/json")
                }
              >
                Export
              </button>
              <label className="btn secondary" style={{ display: "inline-block" }}>
                Import
                <input
                  type="file"
                  accept=".json,.md"
                  className="hidden-input"
                  onChange={(e) => e.target.files[0] && importRules(e.target.files[0])}
                />
              </label>
              <button
                className="btn secondary"
                onClick={() => saveText("codegen-rules.md", rulesToMd(p.rules), "text/markdown")}
              >
                Download .md
              </button>
              <button
                className="btn secondary"
                onClick={() => saveText("codegen-rules.html", rulesToHtmlDoc(p.rules), "text/html")}
              >
                Download rich text
              </button>
            </div>
          </Card>
          <Card
            id="w-tools"
            index={6}
            title="Tools and extensions"
            badge={<span className="badge">{exts.length}</span>}
          >
            <p className="wf-desc">
              Tools the agent may call while it runs a workflow step. Describe one in plain language and the
              AI writes it, tests it, and you save it. Approved <code className="inline-code">ext:*</code> npm
              scripts from package.json appear here too. GitHub Copilot extensions are listed below: load one
              into the project and its tools become project tools (marked CP) that work with any agent and in
              flows.
            </p>
            {[
              ["managed", "Managed by admin · all projects"],
              ["project", "Project-specific"],
              ["global", "Global"],
            ].map(([g, l]) => (
              <ToolGrid
                key={g}
                title={l}
                tools={exts.filter((t) => t.scope === g)}
                render={(t) => (
                  <ToolTile
                    key={t.name}
                    t={t}
                    onOpen={() => openTool(t)}
                    action={
                      <span className="link">
                        {t.managed
                          ? S.admin?.unlocked
                            ? "Edit"
                            : "View"
                          : t.status === "needs approval"
                            ? "Review"
                            : "Edit"}
                      </span>
                    }
                  />
                )}
              />
            ))}
            <CopilotExtTiles />
            <div className="row-actions">
              <button className="btn primary" onClick={() => newTool(false)}>
                Create a tool with AI
              </button>
              <button className="btn secondary" onClick={() => newTool(true)}>
                Write a script myself
              </button>
            </div>
          </Card>
          <PackagesCard index={7} />
          {!all && (
            <div className="row-actions wiz-steps">
              {ci > 0 && (
                <button className="btn secondary" onClick={() => go(WIZ[ci - 1][0])}>
                  ← {WIZ[ci - 1][1]}
                </button>
              )}
              <span className="status-spacer" />
              {ci < WIZ.length - 1 ? (
                <button className="btn primary" onClick={() => go(WIZ[ci + 1][0])}>
                  Next: {WIZ[ci + 1][1]} →
                </button>
              ) : (
                <button
                  className="btn primary"
                  onClick={() => {
                    S.view = "inputs";
                    update();
                  }}
                >
                  Next: choose input →
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

export function StatusBadge({ s }) {
  return (
    <span
      className={
        "badge " +
        (s === "tested" ? "badge-ok" : s === "in review" || s === "needs approval" ? "badge-warn" : "")
      }
    >
      {s}
    </span>
  );
}
