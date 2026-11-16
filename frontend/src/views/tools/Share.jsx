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

export async function exportTools(names) {
  if (!names.length) return toast("Choose at least one tool to export.", "error");
  try {
    const file = await backend.ExportTools(names);
    const name = names.length === 1 ? `${names[0]}.tools.json` : `tools-${names.length}.tools.json`;
    await saveText(name, file, "application/json");
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
      const items = await backend.PreviewToolImport(data);
      const actions = {};
      for (const it of items) actions[it.tool.name] = it.exists || it.managed ? "skip" : "import";
      Object.assign(s, {
        mode: "import",
        items,
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
            () => {
              Object.assign(s, { mode: "export", names: [] });
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
        The file holds each tool's description, parameters, script, command, dependencies and tags, and the
        names of the secrets it needs — never their values. Whoever imports it has to approve each tool.
      </p>
      {list.length ? (
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
                </label>
              </li>
            ))}
          </ul>
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
          disabled={!s.names.length}
          onClick={async () => {
            await exportTools(s.names);
            close();
          }}
        >
          Export {s.names.length || ""} tool{s.names.length === 1 ? "" : "s"}
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
  const items = s.items || [];
  const n = items.filter((it) => s.actions[it.tool.name] !== "skip").length;
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
    s.busy = true;
    update();
    try {
      const res = await backend.ImportTools(s.data, actions);
      await refreshExtensions();
      toast(
        `Imported ${res.imported?.length || 0} tool(s); each needs your approval before agents can use it.` +
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
                      ? ` · needs secret(s) ${t.secrets.map((x) => x.name).join(", ")} (no values included)`
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
                onClick={() => setOpen({ ...open, [t.name]: !open[t.name] })}
              >
                {open[t.name] ? "Hide" : "Show"} the command and script
              </button>
              {open[t.name] && (
                <>
                  <pre className="code share-code">$ {t.command}</pre>
                  {t.script && <pre className="code share-code">{t.script}</pre>}
                  {t.deps?.length > 0 && <pre className="code share-code">{t.deps.join("\n")}</pre>}
                </>
              )}
            </li>
          );
        })}
      </ul>
      <div className="row-actions">
        <button className="btn primary" disabled={!n || s.busy} onClick={run}>
          {s.busy ? "Importing…" : `Import ${n} tool${n === 1 ? "" : "s"}`}
        </button>
        <button className="btn secondary" onClick={close}>
          Cancel
        </button>
      </div>
    </>
  );
}
