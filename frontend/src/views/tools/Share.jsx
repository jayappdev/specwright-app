// Tools: export tools to a file and import them from one.
//
// An export holds each tool's description, parameters, secrets (names only),
// script, command, dependencies, output shape and tags. An import first shows
// every tool with its script and command; imported tools land as "needs
// approval" and no agent can call them until someone opens and saves them.
import { useRef, useState } from "react";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg, refreshExtensions, saveText } from "../../store.js";
import { Icon, Menu } from "../../ui.jsx";

const share = () =>
  (S.toolShare ||= { mode: "", names: [], items: null, actions: {}, data: "", busy: false });

// exportable: this project's own script tools (not managed, built-in, npm or Copilot extension tools)
const exportable = () =>
  (S.ws?.extensions || []).filter(
    (t) => (t.kind || "script") === "script" && t.scope !== "managed" && !t.managed,
  );

export async function exportTools(names, exts = []) {
  if (!names.length && !exts.length) return toast("Choose at least one tool to export.", "error");
  try {
    const res = await backend.ExportTools(names, exts);
    const n = names.length + exts.length;
    const name = n === 1 ? `${names[0] || exts[0].split("/")[1]}.tools.json` : `tools-${n}.tools.json`;
    await saveText(name, res.file, "application/json");
    for (const w of res.warnings || []) toast(w, "error");
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

// ShareMenu: the "⋯" menu next to + New on the Tools screen.
export function ShareMenu() {
  const fileRef = useRef(null);
  const s = share();
  const pick = async (file) => {
    try {
      const data = await file.text();
      const pv = await backend.PreviewToolImport(data);
      const items = pv.tools || [],
        extItems = pv.extensions || [];
      const actions = {};
      for (const it of items) actions[it.tool.name] = it.exists || it.managed ? "skip" : "import";
      for (const x of extItems) actions["ext:" + x.name] = x.exists ? "skip" : "import";
      Object.assign(s, {
        mode: "import",
        items,
        extItems,
        actions,
        data,
        copyNames: Object.fromEntries(items.map((it) => [it.tool.name, it.suggest || ""])),
      });
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <>
      <Menu
        label="Export or import tools"
        items={[
          [
            "Export tools…",
            async () => {
              Object.assign(s, { mode: "export", names: [], exts: [], extList: null });
              update();
              s.extList = (await backend.CopilotExtensions?.().catch(() => [])) || [];
              update();
            },
          ],
          ["Import tools…", () => fileRef.current?.click()],
        ]}
      />
      <input
        ref={fileRef}
        type="file"
        accept=".json,application/json"
        hidden
        onChange={(e) => {
          const f = e.target.files[0];
          e.target.value = "";
          if (f) pick(f);
        }}
      />
      <ShareDialog />
    </>
  );
}

function ShareDialog() {
  const s = share();
  if (!s.mode) return null;
  const close = () => {
    Object.assign(s, { mode: "", items: null, data: "" });
    update();
  };
  return (
    <div className="modal-scrim" role="presentation" onKeyDown={(e) => e.key === "Escape" && close()}>
      <div className="card modal share-modal" role="dialog" aria-modal="true" aria-labelledby="share-title">
        {s.mode === "export" ? <ExportBody close={close} /> : <ImportBody close={close} />}
      </div>
    </div>
  );
}

function ExportBody({ close }) {
  const s = share(),
    list = exportable();
  const toggle = (n, on) => {
    s.names = on ? [...new Set([...s.names, n])] : s.names.filter((x) => x !== n);
    update();
  };
  return (
    <>
      <div className="card-head">
        <Icon.files />
        <h3 id="share-title">Export tools</h3>
      </div>
      <p className="wf-desc">
        The file holds each tool's description, parameters, script, command, dependencies and tags, the other
        files of a tool's folder, and the names of the secrets it needs — never their values. Installed
        packages are left out. Whoever imports it has to approve each tool.
      </p>
      {list.length || (s.extList || []).length ? (
        <>
          <div className="row-actions">
            <button
              className="link"
              onClick={() => {
                s.names = list.map((t) => t.name);
                update();
              }}
            >
              All
            </button>
            <button
              className="link"
              onClick={() => {
                s.names = [];
                update();
              }}
            >
              None
            </button>
          </div>
          <ul className="share-list">
            {list.map((t) => (
              <li key={t.name}>
                <label className="check">
                  <input
                    type="checkbox"
                    checked={s.names.includes(t.name)}
                    onChange={(e) => toggle(t.name, e.target.checked)}
                  />
                  <b>{t.name}</b> <span className="muted">{t.desc}</span>
                  {t.dir && <span className="badge">folder</span>}
                </label>
              </li>
            ))}
          </ul>
          {(s.extList || []).length > 0 && (
            <>
              <h4 style={{ margin: "8px 0 0" }}>Copilot extensions</h4>
              <ul className="share-list">
                {s.extList.map((x) => {
                  const id = x.source + "/" + x.name;
                  return (
                    <li key={id}>
                      <label className="check">
                        <input
                          type="checkbox"
                          checked={(s.exts || []).includes(id)}
                          onChange={(e) => {
                            s.exts = e.target.checked
                              ? [...new Set([...(s.exts || []), id])]
                              : s.exts.filter((v) => v !== id);
                            update();
                          }}
                        />
                        <b>{x.name}</b>{" "}
                        <span className="badge">{x.source === "user" ? "yours" : "this project"}</span>{" "}
                        <span className="muted">
                          {(x.tools || []).map((t) => t.name).join(", ") || x.desc} · {(x.files || []).length}{" "}
                          file(s)
                        </span>
                      </label>
                    </li>
                  );
                })}
              </ul>
              <p className="muted">
                Extensions go as their whole folder, without node_modules (installed again when approved).
              </p>
            </>
          )}
        </>
      ) : (
        <p className="muted">
          This project has no tools of its own to export. Managed, built-in, npm and Copilot extension tools
          are shared their own way.
        </p>
      )}
      <div className="row-actions">
        <button
          className="btn primary"
          disabled={!s.names.length && !(s.exts || []).length}
          onClick={async () => {
            await exportTools(s.names, s.exts || []);
            close();
          }}
        >
          Export {s.names.length + (s.exts || []).length || ""} item
          {s.names.length + (s.exts || []).length === 1 ? "" : "s"}
        </button>
        <button className="btn secondary" onClick={close}>
          Cancel
        </button>
      </div>
    </>
  );
}

function ImportBody({ close }) {
  const s = share(),
    [open, setOpen] = useState({});
  const items = s.items || [],
    exts = s.extItems || [];
  const n =
    items.filter((it) => s.actions[it.tool.name] !== "skip").length +
    exts.filter((x) => s.actions["ext:" + x.name] !== "skip").length;
  const act = (name, v) => {
    s.actions[name] = v;
    update();
  };
  const run = async () => {
    const actions = {};
    for (const it of items) {
      const a = s.actions[it.tool.name];
      actions[it.tool.name] = a === "copy" ? "copy:" + (s.copyNames[it.tool.name] || "").trim() : a;
    }
    for (const x of exts) actions["ext:" + x.name] = s.actions["ext:" + x.name];
    s.busy = true;
    update();
    try {
      const res = await backend.ImportTools(s.data, actions);
      await refreshExtensions();
      import("../CopilotExts.jsx").then((m) => m.loadCopilotExts?.(true)).catch(() => {});
      toast(
        `Imported ${res.imported?.length || 0} item(s); each needs your approval before agents can use it.` +
          (res.notes?.length ? " " + res.notes.join(". ") + "." : ""),
      );
      close();
    } catch (e) {
      toast(errMsg(e), "error");
    }
    s.busy = false;
    update();
  };
  return (
    <>
      <div className="card-head">
        <Icon.warn />
        <h3 id="share-title">
          Import {items.length} tool{items.length === 1 ? "" : "s"}
          {exts.length ? ` and ${exts.length} Copilot extension${exts.length === 1 ? "" : "s"}` : ""}
        </h3>
      </div>
      <p className="wf-desc">
        Tools run commands on this computer. Read each script and command below. Imported tools need your
        approval (open one, check it and save it) before any agent can call them; dependencies are installed
        then.
      </p>
      <ul className="share-list import">
        {items.map((it) => {
          const t = it.tool,
            a = s.actions[t.name];
          return (
            <li key={t.name} className={a === "skip" ? "skipped" : ""}>
              <div className="share-row">
                <div className="grow">
                  <b>{t.name}</b> {t.lang && <span className="badge">{t.lang}</span>}
                  {it.managed && <span className="badge badge-warn">name of a managed tool</span>}
                  {it.exists && !it.managed && (
                    <span className="badge badge-warn">already in this project</span>
                  )}
                  <div className="muted">{t.desc}</div>
                  <small className="muted">
                    {(t.params || []).length} parameter(s)
                    {t.secrets?.length
                      ? t.secrets.some((x) => !x.optional)
                        ? ` · needs secret(s) ${t.secrets
                            .filter((x) => !x.optional)
                            .map((x) => x.name)
                            .join(", ")} (no values included)`
                        : ` · ${t.secrets.length} optional secret(s), never asked for`
                      : ""}
                    {t.deps?.length
                      ? ` · ${t.deps.length} dependenc${t.deps.length === 1 ? "y" : "ies"}`
                      : ""}
                  </small>
                </div>
                <select
                  value={a}
                  aria-label={`What to do with ${t.name}`}
                  onChange={(e) => act(t.name, e.target.value)}
                >
                  {!it.exists && !it.managed && <option value="import">Import</option>}
                  {it.exists && !it.managed && <option value="replace">Replace mine</option>}
                  {(it.exists || it.managed) && <option value="copy">Import as a copy</option>}
                  <option value="skip">Skip</option>
                </select>
              </div>
              {a === "copy" && (
                <label className="field inline">
                  <span>New name</span>
                  <input
                    value={s.copyNames[t.name] || ""}
                    onChange={(e) => {
                      s.copyNames[t.name] = e.target.value;
                      update();
                    }}
                  />
                </label>
              )}
              <button
                className="link"
                aria-expanded={!!open[t.name]}
                onClick={() => setOpen((o) => ({ ...o, [t.name]: !o[t.name] }))}
              >
                {open[t.name] ? "Hide" : "Show"} the command and script
              </button>
              {open[t.name] && (
                <>
                  <pre className="code share-code">$ {t.command}</pre>
                  {t.script && <pre className="code share-code">{t.script}</pre>}
                  {t.deps?.length > 0 && <pre className="code share-code">{t.deps.join("\n")}</pre>}
                  {it.files?.length > 0 && (
                    <small className="muted">Other files of its folder: {it.files.join(", ")}</small>
                  )}
                </>
              )}
            </li>
          );
        })}
        {exts.map((x) => {
          const k = "ext:" + x.name,
            a = s.actions[k];
          return (
            <li key={k} className={a === "skip" ? "skipped" : ""}>
              <div className="share-row">
                <div className="grow">
                  <b>{x.name}</b> <span className="badge">Copilot extension</span>
                  {x.exists && <span className="badge badge-warn">already in this project</span>}
                  <div className="muted">{x.desc}</div>
                  <small className="muted">
                    {x.tools?.length ? `adds ${x.tools.join(", ")} · ` : ""}
                    {x.files.length} file(s) → .github/extensions/{x.name}/
                  </small>
                </div>
                <select
                  value={a}
                  aria-label={`What to do with ${x.name}`}
                  onChange={(e) => act(k, e.target.value)}
                >
                  {!x.exists && <option value="import">Import</option>}
                  {x.exists && <option value="replace">Replace mine</option>}
                  <option value="skip">Skip</option>
                </select>
              </div>
              <button
                className="link"
                aria-expanded={!!open[k]}
                onClick={() => setOpen((o) => ({ ...o, [k]: !o[k] }))}
              >
                {open[k] ? "Hide" : "Show"} its files and entry script
              </button>
              {open[k] && (
                <>
                  <small className="muted">{x.files.join(", ")}</small>
                  {x.entry && <pre className="code share-code">{x.entry}</pre>}
                </>
              )}
            </li>
          );
        })}
      </ul>
      <div className="row-actions">
        <button className="btn primary" disabled={!n || s.busy} onClick={run}>
          {s.busy ? "Importing…" : `Import ${n} item${n === 1 ? "" : "s"}`}
        </button>
        <button className="btn secondary" onClick={close}>
          Cancel
        </button>
      </div>
    </>
  );
}
