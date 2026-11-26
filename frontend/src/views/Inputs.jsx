import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, saveProfile, saveWorkflow, downloadProjectZip } from "../store.js";
import { ProjectPicker } from "../ProjectPicker.jsx";
import { NeedWorkspace, WorkspaceBar } from "./Wizard.jsx";
import { ModelPicker } from "../ModelPicker.jsx";
import { ToolIcon } from "../ToolTile.jsx";
import { Icon, LearnMore } from "../ui.jsx";

const CTX_SVG = (
  <svg className="diagram-thumb" viewBox="0 0 320 120" aria-hidden="true">
    <g fill="none" stroke="currentColor" strokeWidth="1.2" style={{ color: "var(--muted)" }}>
      <rect x="120" y="40" width="80" height="40" rx="6" />
      <rect x="10" y="10" width="70" height="30" rx="4" />
      <rect x="10" y="80" width="70" height="30" rx="4" />
      <rect x="240" y="45" width="70" height="30" rx="4" />
      <path d="M80 25L120 50M80 95L120 70M200 60h40" />
    </g>
  </svg>
);
const SEQ_SVG = (
  <svg className="diagram-thumb" viewBox="0 0 320 120" aria-hidden="true">
    <g fill="none" stroke="currentColor" strokeWidth="1.2" style={{ color: "var(--muted)" }}>
      <path d="M50 22v92M160 22v92M270 22v92" strokeDasharray="3 3" />
      <path d="M50 45h110M160 65h110M270 85H160M160 100H50" />
    </g>
  </svg>
);
const DIAGRAM = /\.(drawio|svg|png|puml|plantuml|mmd|md)$/i;

async function setDiagram(kind, rel) {
  try {
    await backend.SetDiagram(kind, rel);
    S.ws.profile.diagrams = { ...(S.ws.profile.diagrams || {}) };
    if (rel) S.ws.profile.diagrams[kind] = rel;
    else delete S.ws.profile.diagrams[kind];
    update();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

function DiagramCard({ kind, title, desc, thumb }) {
  const v = S.ws.profile.diagrams?.[kind];
  const candidates = (S.files.list || []).filter(
    (f) => DIAGRAM.test(f) && !f.startsWith(".codegen/") && !f.startsWith("specs/"),
  );
  return (
    <div className="card" id={"in-" + kind}>
      <div className="card-head">
        <h3>{title}</h3>
        {v ? <span className="badge badge-ok">attached</span> : <span className="badge">optional</span>}
      </div>
      <p className="wf-desc">{desc}</p>
      <div className={"drop" + (v ? " has" : "")}>
        {v ? (
          <>
            {thumb}
            <span>
              <code className="inline-code">{v}</code>
            </span>
            <div className="row-actions" style={{ margin: 0 }}>
              <button className="btn secondary" onClick={() => setDiagram(kind, "")}>
                Remove
              </button>
            </div>
          </>
        ) : (
          <>
            <span className="muted">
              Pick a .drawio, .svg, .png, Mermaid or PlantUML file. Its path is sent with every step that has
              “Attach input diagrams” on.
            </span>
            <div className="row-actions" style={{ margin: 0 }}>
              {backend.kind === "desktop" && (
                <button
                  className="btn secondary"
                  onClick={async () => {
                    try {
                      const rel = await backend.PickDiagram(kind);
                      if (rel) {
                        S.ws.profile.diagrams = { ...(S.ws.profile.diagrams || {}), [kind]: rel };
                        update();
                      }
                    } catch (e) {
                      toast(errMsg(e), "error");
                    }
                  }}
                >
                  Choose file…
                </button>
              )}
              <select
                aria-label={"Workspace file for " + title}
                value=""
                onChange={(e) => e.target.value && setDiagram(kind, e.target.value)}
              >
                <option value="">
                  {candidates.length
                    ? "…or use a file in the workspace"
                    : "No diagram files in the workspace"}
                </option>
                {candidates.map((f) => (
                  <option key={f} value={f}>
                    {f}
                  </option>
                ))}
              </select>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// --- what every spec workflow step brings in ------------------------------------------

const BUILTIN_GROUPS = [
  ["read", "Read"],
  ["search", "Search"],
  ["edit", "Edit files"],
  ["execute", "Run"],
  ["web", "Web"],
];
const runTools = () =>
  (S.ws?.extensions || []).filter(
    (t) => t.approved && t.status !== "in review" && (!t.managed || t.managed.status === "verified"),
  );

// One checkbox chip.
function Pick({ on, onChange, children, title }) {
  return (
    <label className={"ctx-pick" + (on ? " on" : "")} title={title}>
      <input type="checkbox" checked={on} onChange={(e) => onChange(e.target.checked)} />
      {children}
    </label>
  );
}

function Group({ title, count, total, all, none, children, empty }) {
  return (
    <div className="ctx-group">
      <div className="ctx-group-head">
        <b>{title}</b>
        <span className="muted">
          {count} of {total}
        </span>
        {total > 0 && (
          <span className="ctx-links">
            {all && (
              <button className="link" onClick={all}>
                All
              </button>
            )}
            {none && (
              <button className="link" onClick={none}>
                None
              </button>
            )}
          </span>
        )}
      </div>
      {total ? <div className="ctx-grid">{children}</div> : <p className="muted ctx-empty">{empty}</p>}
    </div>
  );
}

function RunContextCard() {
  const p = S.ws.profile,
    wf = S.wf;
  if (!wf) return null;
  const c = (wf.context ||= {});
  const save = () => {
    update();
    saveWorkflow();
  };
  // libraries: the profile's selection, shared with the Development wizard
  const libGroup = (scope, key, title, empty) => {
    const libs = (S.ws.libraries || []).filter((l) => l.scope === scope),
      sel = (p[key] ||= []);
    const set = (ids) => {
      p[key] = ids;
      update();
      saveProfile();
    };
    return (
      <Group
        title={title}
        count={libs.filter((l) => sel.includes(l.id)).length}
        total={libs.length}
        empty={empty}
        all={() => set(libs.map((l) => l.id))}
        none={() => set([])}
      >
        {libs.map((l) => (
          <Pick
            key={l.id}
            on={sel.includes(l.id)}
            title={l.note || l.path}
            onChange={(v) => set(v ? [...sel, l.id] : sel.filter((x) => x !== l.id))}
          >
            <span className="ctx-name">{l.name}</span>
            <span className={"badge " + (l.kind === "skill" ? "badge-info" : "badge-warn")}>{l.kind}</span>
            {l.files?.length ? <span className="muted ctx-sub">+{l.files.length} files</span> : null}
          </Pick>
        ))}
      </Group>
    );
  };
  // rules and tools: the workflow's own choice (nothing chosen yet = all rules / all approved tools)
  const rules = p.rules || [],
    ruleOn = (id) => !c.rules || c.rules.includes(id);
  const setRules = (ids) => {
    c.rules = ids;
    save();
  };
  const tools = runTools(),
    bts = S.flowBuiltins || [];
  const toolOn = (n) => (n.startsWith("builtin:") ? !!c.tools?.includes(n) : !c.tools || c.tools.includes(n));
  const current = () => c.tools || tools.map((t) => t.name);
  const setTool = (n, v) => {
    const cur = current().filter((x) => x !== n);
    c.tools = v ? [...cur, n] : cur;
    save();
  };
  const toolGroup = (title, list, empty) => (
    <Group
      title={title}
      count={list.filter((t) => toolOn(t.name)).length}
      total={list.length}
      empty={empty}
      all={() => {
        c.tools = [...new Set([...current(), ...list.map((t) => t.name)])];
        save();
      }}
      none={() => {
        const drop = new Set(list.map((t) => t.name));
        c.tools = current().filter((x) => !drop.has(x));
        save();
      }}
    >
      {list.map((t) => (
        <Pick key={t.name} on={toolOn(t.name)} title={t.desc} onChange={(v) => setTool(t.name, v)}>
          <ToolIcon t={t} size={22} />
          <span className="ctx-name">{t.name.replace(/^builtin:/, "")}</span>
        </Pick>
      ))}
    </Group>
  );
  const shared = tools.filter((t) => t.scope !== "project"),
    mine = tools.filter((t) => t.scope === "project");
  return (
    <>
      <div className="card ctx-card" id="in-model">
        <div className="card-head">
          <h3>AI model</h3>
          <span className="badge">{wf.model || "app default"}</span>
        </div>
        <p className="wf-desc">
          Every step of the spec workflow runs with this model. It is the same setting as on Run spec
          workflow.
        </p>
        <ModelPicker
          value={wf.model || ""}
          label="Model for workflow steps"
          onChange={(v) => {
            wf.model = v;
            save();
          }}
        />
      </div>

      <div className="card ctx-card" id="in-skills">
        <div className="card-head">
          <h3>Skills, libraries and rules</h3>
        </div>
        <p className="wf-desc">
          What the agent reads before each step. Skills and libraries are the same selection as in the
          Development wizard.
        </p>
        {libGroup(
          "global",
          "global",
          "Corporate (global) libraries and skills",
          "No corporate libraries published yet.",
        )}
        {libGroup(
          "project",
          "project",
          "Project skills and scripts",
          "No project skills yet. Add them in the Development wizard.",
        )}
        <Group
          title="Rules"
          count={rules.filter((r) => ruleOn(r.id)).length}
          total={rules.length}
          empty="No rules yet. Write them in the Development wizard."
          all={() => setRules(rules.map((r) => r.id))}
          none={() => setRules([])}
        >
          {rules.map((r) => (
            <Pick
              key={r.id}
              on={ruleOn(r.id)}
              title={r.text}
              onChange={(v) =>
                setRules(
                  v
                    ? [...new Set([...(c.rules || rules.map((x) => x.id)), r.id])]
                    : (c.rules || rules.map((x) => x.id)).filter((x) => x !== r.id),
                )
              }
            >
              <span className="ctx-name">{r.id}</span>
              <span className="muted ctx-sub">{(r.text || "").split("\n")[0].slice(0, 60)}</span>
            </Pick>
          ))}
        </Group>
      </div>

      <div className="card ctx-card" id="in-tools">
        <div className="card-head">
          <h3>Tools</h3>
          {c.tools && (
            <button
              className="link"
              style={{ marginLeft: "auto" }}
              onClick={() => {
                delete c.tools;
                save();
              }}
            >
              Reset to all approved tools
            </button>
          )}
        </div>
        <p className="wf-desc">
          The tools listed in each step's prompt for the agent to use. Unchosen project and global tools can't
          be called during the workflow. Built-in tools stay available to the agent, and the chosen ones are
          named in the prompt.
        </p>
        {(() => {
          const steps = wf.steps || [],
            setAll = (v) => {
              steps.forEach((st) => {
                st.options.tools = v;
              });
              save();
            };
          const chosen = [...tools, ...bts].filter((t) => toolOn(t.name)).length,
            withTools = steps.filter((st) => st.options?.tools).length;
          return (
            <>
              {chosen > 0 && steps.length > 0 && !withTools && (
                <div className={"banner ctx-warn" + (c.tools ? " warn" : "")} role="status">
                  {c.tools ? <Icon.warn /> : null}
                  <span>
                    {c.tools ? (
                      <>
                        <b>No step gets these tools.</b> {chosen} tool{chosen === 1 ? " is" : "s are"} chosen
                        below, but every step has its Tools option off, so none is offered to the agent.
                      </>
                    ) : (
                      <>
                        Tools are off for every step, so the {chosen} approved tool{chosen === 1 ? "" : "s"}{" "}
                        below {chosen === 1 ? "is" : "are"} not offered to the agent yet.
                      </>
                    )}
                  </span>
                  <button className="btn secondary" onClick={() => setAll(true)}>
                    Give tools to every step
                  </button>
                </div>
              )}
              <Group
                title="Steps that get these tools"
                count={withTools}
                total={steps.length}
                all={() => setAll(true)}
                none={() => setAll(false)}
                empty=""
              >
                {steps.map((st) => (
                  <Pick
                    key={st.id}
                    on={!!st.options?.tools}
                    title={st.cmd}
                    onChange={(v) => {
                      st.options.tools = v;
                      save();
                    }}
                  >
                    <span className="ctx-name">{st.title}</span>
                  </Pick>
                ))}
              </Group>
            </>
          );
        })()}
        {toolGroup("Global tools", shared, "No global or corporate tools.")}
        {toolGroup(
          "Project tools",
          mine,
          "No approved project tools. Create or approve them on the Tools screen.",
        )}
        {BUILTIN_GROUPS.map(([g, l]) => {
          const list = bts.filter((t) => t.lang === g);
          return list.length ? <div key={g}>{toolGroup("Built-in · " + l, list, "")}</div> : null;
        })}
      </div>
      <p className="muted ctx-foot">
        Rules and skills go to every step whose Rules / Skills option is on (Run spec workflow › step
        options); they are on by default.
      </p>
    </>
  );
}

// --- Spec Kit: set the project up with the bundled template ------------------------------

const INTEGRATIONS = [
  ["claude", "Claude Code", "CLAUDE.md", ".claude/skills/"],
  ["copilot", "GitHub Copilot", ".github/copilot-instructions.md", ".github/skills/"],
];
const sk = () => (S.sk ||= { busy: false, result: null, integration: "", name: null });
const slugName = (s) =>
  String(s || "")
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^[^a-z0-9]+|-+$/g, "")
    .slice(0, 64);

// the Spec Kit download (SpecWright does not ship it): poll while it runs
async function refreshSpecKit() {
  try {
    S.ws = { ...S.ws, speckit: { ...(S.ws.speckit || {}), ...(await backend.SpecKitInfo()) } };
    update();
  } catch {
    /* ignore */
  }
  if (S.ws?.speckit?.downloading) setTimeout(refreshSpecKit, 1500);
}
async function downloadSpecKit(force) {
  try {
    S.ws = { ...S.ws, speckit: { ...(S.ws.speckit || {}), ...(await backend.DownloadSpecKit(force)) } };
    update();
    refreshSpecKit();
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

async function initSpecKit(project, integration) {
  const k = sk();
  k.busy = true;
  update();
  if (!S.ws.speckit?.available) setTimeout(refreshSpecKit, 800); // shows the download's progress
  try {
    const r = await backend.InitSpecKit(project, integration);
    k.result = r;
    const ws = await backend.Workspace();
    S.ws = { ...S.ws, ...ws };
    S.wf = ws.workflow;
    toast(
      `Spec Kit ${r.version} is set up for ${INTEGRATIONS.find((x) => x[0] === r.integration)?.[1] || r.integration}.`,
    );
  } catch (e) {
    toast(errMsg(e), "error");
  }
  k.busy = false;
  update();
}

function SpecKitCard() {
  const k = sk(),
    info = S.ws.speckit || { initialized: S.ws.specKit };
  useEffect(() => {
    refreshSpecKit();
  }, [S.ws?.root]);
  const def = k.integration || info.integration || (S.setup.provider === "copilot" ? "copilot" : "claude");
  const integ = INTEGRATIONS.find((x) => x[0] === def) || INTEGRATIONS[0];
  const steps = (S.wf?.steps || []).filter((st) => (st.cmd || "").startsWith("/speckit"));
  const r = k.result,
    cur = info.project || "",
    projects = S.ws.projects || [];
  const creating = k.newMode || !projects.length; // a new project folder
  const name = creating ? (k.name ?? (projects.length ? "" : slugName(S.ws.name) + "-app")) : cur;
  const nameOk = !name || /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(name);
  const taken = creating && projects.some((x) => x.name === name);
  const newFolder = creating && !!name && name !== cur;
  return (
    <div className="card sk-card" id="in-speckit">
      <div className="card-head">
        <h3>Spec Kit</h3>
        {info.initialized ? (
          <span className="badge badge-ok">
            {cur ? cur + "/ · " : ""}v{info.version || "?"} ·{" "}
            {(info.integrations || [info.integration]).filter(Boolean).join(" + ") || "set up"}
          </span>
        ) : (
          <span className="badge badge-warn">not set up</span>
        )}
        {info.downloading ? (
          <span className="badge badge-info">
            <span className="spinner" /> downloading
          </span>
        ) : info.available ? (
          <span className="badge" title="Downloaded on this computer from github.com/github/spec-kit">
            downloaded v{info.available}
          </span>
        ) : (
          <span className="badge badge-warn">not downloaded</span>
        )}
      </div>
      <p className="wf-desc">
        {info.initialized ? (
          <>
            The project is ready for spec-driven development. Each step runs its own Spec Kit command and
            stays within its scope, in order, with your approval in between.
          </>
        ) : (
          <>
            Create the project in its own folder inside this workspace and set it up with GitHub Spec Kit,
            like{" "}
            <code className="inline-code">
              specify init {name || "my-project"} --integration {def}
            </code>
            .
          </>
        )}
        {projects.length > 1 && (
          <>
            {" "}
            This workspace has {projects.length} projects. Each has its own workflow progress; the skills,
            rules and tools are shared.
          </>
        )}
      </p>
      {projects.length > 0 && (
        <div className="sk-row">
          <label className="field inline">
            <span>Project</span>
            <ProjectPicker
              value={creating ? "+new" : cur}
              disabled={k.busy}
              onNew={() => {
                k.newMode = true;
                k.name = "";
                k.result = null;
                update();
              }}
              onPick={() => {
                k.newMode = false;
                k.name = null;
                k.result = null;
              }}
            />
          </label>
          {!creating && (
            <span className="muted sk-where">
              {cur ? (
                <>
                  in <code className="inline-code">{cur}/</code>
                </>
              ) : (
                "in the top folder"
              )}
            </span>
          )}
          {creating && (
            <button
              className="link"
              onClick={() => {
                k.newMode = false;
                k.name = null;
                update();
              }}
            >
              Cancel
            </button>
          )}
        </div>
      )}
      <div className="sk-row">
        {creating && (
          <label className="field inline">
            <span>{projects.length ? "New project folder" : "Project folder"}</span>
            <input
              className="sk-name"
              value={name}
              placeholder="e.g. billing-service"
              disabled={k.busy}
              spellCheck={false}
              autoFocus={projects.length > 0}
              onChange={(e) => {
                k.name = e.target.value.trim();
                update();
              }}
            />
            <span className="muted">/</span>
          </label>
        )}
        <label className="field inline">
          <span>Agent</span>
          <select
            value={def}
            disabled={k.busy}
            onChange={(e) => {
              k.integration = e.target.value;
              update();
            }}
          >
            {INTEGRATIONS.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <button
          className={"btn " + (info.initialized && !newFolder ? "secondary" : "primary")}
          disabled={k.busy || !nameOk || taken || (creating && !name)}
          onClick={async () => {
            await initSpecKit(name, def);
            if (k.result) {
              k.newMode = false;
              k.name = null;
              update();
            }
          }}
        >
          {k.busy
            ? info.downloading || !info.available
              ? "Downloading Spec Kit…"
              : "Setting up…"
            : newFolder
              ? `Create ${name}/ and set up Spec Kit`
              : !info.initialized
                ? "Set up Spec Kit"
                : (info.integrations || []).includes(def)
                  ? "Refresh Spec Kit files and context"
                  : `Add ${integ[1]}`}
        </button>
        {info.initialized && !creating && (
          <button
            className="btn secondary"
            disabled={S.zipBusy}
            title="The project folder, with the skills, rules and diagrams it uses"
            onClick={() => downloadProjectZip(true)}
          >
            {S.zipBusy ? "Zipping…" : "Download project (.zip)"}
          </button>
        )}
      </div>
      <div className={"sk-dl" + (info.error ? " bad" : "")}>
        {info.downloading ? (
          <>
            <span className="spinner" /> {info.progress || "Downloading Spec Kit…"}
          </>
        ) : info.error ? (
          <>
            <b>Spec Kit could not be downloaded.</b>{" "}
            <span className="sk-dl-err">{info.error.split("\n")[0]}</span> Check the proxy, CA and package
            index under{" "}
            <button
              className="link"
              onClick={() => {
                S.view = "wizard";
                S.wizSection = "w-packages";
                update();
                setTimeout(
                  () => document.getElementById("w-packages")?.scrollIntoView({ block: "start" }),
                  50,
                );
              }}
            >
              Development wizard › Packages & proxy
            </button>
            , then{" "}
            <button className="link" onClick={() => downloadSpecKit(true)}>
              try again
            </button>
            .
          </>
        ) : info.available ? (
          <>
            Spec Kit {info.available} was downloaded on this computer from{" "}
            <code className="inline-code">github.com/github/spec-kit</code> through your proxy.{" "}
            <button className="link" onClick={() => downloadSpecKit(true)}>
              Check for a newer release
            </button>
          </>
        ) : (
          <>
            Spec Kit is not on this computer yet.{" "}
            <button className="link" onClick={() => downloadSpecKit(false)}>
              Download it now
            </button>
            <LearnMore>
              SpecWright doesn't ship Spec Kit. It downloads the latest release from{" "}
              <code className="inline-code">github.com/github/spec-kit</code> through the proxy under
              Development wizard › Packages & proxy, either when you first save the proxy settings or at the
              first setup. It runs Spec Kit's own CLI with uv, pipx or Python.
            </LearnMore>
          </>
        )}
      </div>
      {!nameOk && (
        <p className="err-text sk-err">
          Use letters, digits, - . and _ (no spaces or slashes), e.g. orders-service.
        </p>
      )}
      {taken && (
        <p className="err-text sk-err">{name}/ is already a project here: pick it in Project instead.</p>
      )}
      <details className="sk-what" open={!info.initialized}>
        <summary>What it sets up</summary>
        <ol>
          <li>
            <b>The project folder</b> <code className="inline-code">{name || cur || "my-project"}/</code> in
            this workspace: Spec Kit, the specs and the code all go there, and every step runs inside it.
          </li>
          <li>
            <b>The Spec Kit template:</b> <code className="inline-code">.specify/</code> (constitution,
            templates for spec, plan, tasks and checklists, the helper scripts, the SDD workflow) and the{" "}
            <code className="inline-code">speckit-*</code> skills in{" "}
            <code className="inline-code">{integ[3]}</code>. Files you have changed, and a written
            constitution, are kept.
          </li>
          <li>
            <b>The project context:</b> the corporate and project skills, rules, tools and diagrams chosen
            below, in a SpecWright section of <code className="inline-code">{integ[2]}</code>. The rest of
            that file is kept, and the section is refreshed before every step.
          </li>
          <li>
            <b>The workflow, ready to run:</b>{" "}
            {steps.map((st, i) => (
              <span key={st.id}>
                {i > 0 && " → "}
                <span className={"sk-step sk-" + (st.status || "pending")}>{st.title}</span>
              </span>
            ))}
            . Each step does only its own job: Constitution writes only the constitution, Specify only the
            spec, and so on, and Analyze checks everything before Implement.
          </li>
        </ol>
      </details>
      {info.initialized && (
        <p className="muted sk-status">
          Constitution:{" "}
          {info.constitution ? "written" : "still the blank template. Run the Constitution step first"} ·
          feature specs go to <code className="inline-code">{cur ? cur + "/" : ""}specs/</code>
        </p>
      )}
      {r && (
        <div className="sk-result">
          <b>
            Spec Kit {r.version} for {r.integration}:
          </b>{" "}
          {r.created?.length || 0} files created, {r.updated?.length || 0} updated, {r.kept?.length || 0} kept
          {r.context?.length ? (
            <>
              {" "}
              · context in{" "}
              {r.context.map((f) => (
                <code key={f} className="inline-code" data-file={f}>
                  {f}
                </code>
              ))}
            </>
          ) : null}
          {r.kept?.length > 0 && (
            <details>
              <summary>Kept as they are</summary>
              <ul>
                {r.kept.map((f) => (
                  <li key={f}>
                    <code className="inline-code" data-file={f}>
                      {f}
                    </code>
                  </li>
                ))}
              </ul>
            </details>
          )}
          {r.created?.length > 0 && (
            <details>
              <summary>Created</summary>
              <ul>
                {r.created.map((f) => (
                  <li key={f}>
                    <code className="inline-code" data-file={f}>
                      {f}
                    </code>
                  </li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

// InputSummary: where each choice stands, as links to its card
function InputSummary() {
  const p = S.ws.profile,
    wf = S.wf,
    sk = S.ws.speckit || {},
    d = p.diagrams || {};
  const nd = ["context", "sequence"].filter((k) => d[k]).length,
    steps = wf?.steps || [],
    withTools = steps.filter((s) => s.options?.tools).length;
  const skills = (p.global?.length || 0) + (p.project?.length || 0);
  const jump = (id) => document.getElementById(id)?.scrollIntoView({ block: "start", behavior: "smooth" });
  const item = (id, ok, label, value) => (
    <button key={id} type="button" className={"in-sum-item" + (ok ? " ok" : "")} onClick={() => jump(id)}>
      <span>{label}</span>
      <b>{value}</b>
    </button>
  );
  return (
    <nav className="in-sum" aria-label="Summary of the choices on this page">
      {item("in-speckit", sk.initialized, "Spec Kit", sk.initialized ? sk.project || "set up" : "not set up")}
      {item("in-context", nd > 0, "Diagrams", `${nd} of 2`)}
      {item("in-model", true, "Model", wf?.model || "app default")}
      {item("in-skills", skills > 0, "Skills and libraries", `${skills} chosen`)}
      {item("in-tools", withTools > 0, "Steps with tools", `${withTools} of ${steps.length}`)}
    </nav>
  );
}

export function Inputs() {
  useEffect(() => {
    if (S.ws)
      backend
        .ListFiles()
        .then((l) => {
          S.files.list = l || [];
          update();
        })
        .catch(() => {});
  }, [S.ws?.root]);
  useEffect(() => {
    if (S.ws && !S.flowBuiltins)
      backend
        .FlowBuiltinTools()
        .then((x) => {
          S.flowBuiltins = x || [];
          update();
        })
        .catch(() => {});
  }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="inputs" />;
  const p = S.ws.profile;
  return (
    <section className="view">
      <header className="view-head">
        <h1>Choose input</h1>
        <div className="head-controls">
          <button
            className="btn primary"
            onClick={() => {
              S.view = "workflow";
              update();
            }}
          >
            Next: run spec workflow
          </button>
        </div>
      </header>
      <p className="wf-desc">
        Point the agent at the source to read and add diagrams so the spec starts from what the system
        actually does. Then choose the model, skills, rules and tools every step uses.
      </p>
      <InputSummary />
      <div className="card">
        <div className="card-head">
          <h3>Workspace folder</h3>
        </div>
        <WorkspaceBar />
        <p className="muted" style={{ marginTop: 8 }}>
          {S.ws.fileCount} files indexed · {p.lang} · {p.build}. It holds your Spec Kit projects (each in its
          own folder) and the skills, rules and tools they share in{" "}
          <code className="inline-code">.codegen/</code>. Only this folder is visible to the agent.
        </p>
      </div>
      <SpecKitCard />
      <div className="two-col">
        <DiagramCard
          kind="context"
          title="Context / activity diagram"
          desc="Who and what the system talks to, or the activity flow of the feature."
          thumb={CTX_SVG}
        />
        <DiagramCard
          kind="sequence"
          title="Sequence diagram"
          desc="Calls between components for the main scenario."
          thumb={SEQ_SVG}
        />
      </div>
      <RunContextCard />
    </section>
  );
}
