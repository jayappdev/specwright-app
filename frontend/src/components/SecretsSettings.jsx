// Settings › Secrets: the values flow templates read from outside the flow.
//   {{project_secret.NAME}}  encrypted in the project (this computer's key)
//   {{keychain.NAME}}        the system keychain (SpecWright's entries)
//   {{env.NAME}}             the flow's variables, then the environment
// Values are typed in and saved; they never come back to the screen.
import { useEffect, useState } from "react";
import { backend } from "../backend.js";
import { S, toast, errMsg } from "../store.js";
import { Icon } from "../ui.jsx";
import { confirmDialog } from "../Dialog.jsx";
import { ago } from "../time.js";

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_.-]{0,127}$/;

function useLoad(fn, deps) {
  const [v, setV] = useState(null);
  const [n, setN] = useState(0);
  useEffect(() => {
    let live = true;
    fn()
      .then((x) => live && setV(x))
      .catch(() => live && setV([]));
    return () => {
      live = false;
    };
  }, [n, ...deps]); // deps: what the loader depends on
  return [v, () => setN((x) => x + 1)];
}

// Ref: a template reference with a copy button
function Ref({ text }) {
  return (
    <button
      className="secret-ref"
      title="Copy, to paste into a step's field"
      onClick={() =>
        navigator.clipboard
          ?.writeText(text)
          .then(() => toast("Copied " + text))
          .catch(() => {})
      }
    >
      <code>{text}</code> <Icon.copy />
    </button>
  );
}

function ProjectSecrets({ uses, reloadUses }) {
  const [list, reload] = useLoad(() => backend.ProjectSecrets(), [S.ws?.root]);
  const [form, setForm] = useState(null); // { name, value, desc, editing }
  const save = async () => {
    try {
      await backend.SetProjectSecret(form.name.trim(), form.value, form.desc || "");
      toast(form.editing ? "Saved " + form.name : "Added " + form.name);
      setForm(null);
      reload();
      reloadUses();
      S.projectSecretsFor = null; // the designer's menus load them again
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const remove = async (p) => {
    if (
      !(await confirmDialog(
        `Delete the project secret ${p.name}? Flows that read it stop with an error until it is added again.`,
        { ok: "Delete", danger: true },
      ))
    )
      return;
    try {
      await backend.DeleteProjectSecret(p.name);
      reload();
      reloadUses();
      S.projectSecretsFor = null; // the designer's menus load them again
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const missing = (uses || []).filter((u) => u.store === "project_secret" && !u.found);
  const badName = form && form.name && !NAME_RE.test(form.name.trim());
  return (
    <div className="card">
      <h3>Project secrets</h3>
      <p className="wf-desc">
        Kept encrypted in this project (<code className="inline-code">.codegen/secrets</code>, left out of
        git) with a key that stays on this computer. A step reads one as{" "}
        <code className="inline-code">{"{{project_secret.NAME}}"}</code>. The value is hidden in results,
        history and the chat, and never shown again here.
      </p>
      {list === null ? (
        <p className="muted">Loading…</p>
      ) : list.length ? (
        <table className="secret-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>What it is</th>
              <th>Set</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.map((p) => (
              <tr key={p.name}>
                <td>
                  <Ref text={`{{project_secret.${p.name}}}`} />
                  {!p.readable && (
                    <span
                      className="badge badge-warn"
                      title="Saved with another computer's key: set it again here"
                    >
                      set again
                    </span>
                  )}
                </td>
                <td className="muted">{p.desc || "—"}</td>
                <td className="muted">{p.updated ? ago(p.updated) : ""}</td>
                <td className="secret-acts">
                  <button
                    className="icon-btn"
                    title="Replace the value or the description"
                    aria-label={"Edit " + p.name}
                    onClick={() => setForm({ name: p.name, value: "", desc: p.desc || "", editing: true })}
                  >
                    <Icon.pencil />
                  </button>
                  <button
                    className="icon-btn"
                    title="Delete"
                    aria-label={"Delete " + p.name}
                    onClick={() => remove(p)}
                  >
                    <Icon.trash />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="muted">No project secrets yet.</p>
      )}
      {missing.length > 0 && (
        <div className="banner warn">
          Flows read {missing.length === 1 ? "a project secret that is" : "project secrets that are"} not set:{" "}
          {missing.map((u, i) => (
            <span key={u.name}>
              {i > 0 && ", "}
              <button
                className="link"
                onClick={() => setForm({ name: u.name, value: "", desc: "", editing: false })}
              >
                {u.name}
              </button>{" "}
              <small className="muted">({u.flows.join(", ")})</small>
            </span>
          ))}
        </div>
      )}
      {form ? (
        <form
          className="secret-form"
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <label className="field">
            <span className="muted">Name</span>
            <input
              className={"mono" + (badName ? " bad" : "")}
              aria-label="Secret name"
              value={form.name}
              disabled={form.editing}
              placeholder="API_TOKEN"
              autoFocus={!form.editing}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </label>
          <label className="field">
            <span className="muted">{form.editing ? "New value (empty: keep it)" : "Value"}</span>
            <input
              type="password"
              aria-label="Secret value"
              autoComplete="new-password"
              value={form.value}
              autoFocus={form.editing}
              onChange={(e) => setForm({ ...form, value: e.target.value })}
            />
          </label>
          <label className="field secret-form-desc">
            <span className="muted">What it is (optional)</span>
            <input
              aria-label="What it is"
              value={form.desc}
              onChange={(e) => setForm({ ...form, desc: e.target.value })}
            />
          </label>
          <div className="row-actions">
            <button type="button" className="btn secondary" onClick={() => setForm(null)}>
              Cancel
            </button>
            <button
              type="submit"
              className="btn primary"
              disabled={!form.name.trim() || badName || (!form.editing && !form.value)}
            >
              {form.editing ? "Save" : "Add secret"}
            </button>
          </div>
        </form>
      ) : (
        <button
          className="btn secondary"
          onClick={() => setForm({ name: "", value: "", desc: "", editing: false })}
        >
          <Icon.plus /> Add a project secret
        </button>
      )}
    </div>
  );
}

function KeychainSecrets({ uses, reloadUses }) {
  const [avail, setAvail] = useState(null);
  const [form, setForm] = useState({ name: "", value: "" });
  const [check, setCheck] = useState(null); // { name, found, where }
  useEffect(() => {
    backend
      .KeychainAvailable()
      .then(setAvail)
      .catch(() => setAvail(false));
  }, []);
  const look = async (name) => {
    try {
      const st = await backend.CheckKeychain({ account: name });
      setCheck({ name, found: st.found, where: st.where });
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const save = async () => {
    const name = form.name.trim();
    try {
      await backend.SaveSecretToKeychain(name, form.value);
      toast("Saved " + name + " in the keychain");
      setForm({ name: "", value: "" });
      look(name);
      reloadUses();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const remove = async (name) => {
    if (!(await confirmDialog(`Remove ${name} from the keychain?`, { ok: "Remove", danger: true }))) return;
    try {
      await backend.RemoveSecretFromKeychain(name);
      look(name);
      reloadUses();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const kcUses = (uses || []).filter((u) => u.store === "keychain");
  const badName = form.name && !NAME_RE.test(form.name.trim());
  return (
    <div className="card">
      <h3>Keychain</h3>
      <p className="wf-desc">
        Your computer's keychain (macOS Keychain, Windows Credential Manager, Linux Secret Service). A step
        reads SpecWright's entry <i>NAME</i> as <code className="inline-code">{"{{keychain.NAME}}"}</code>.
        Good for your own passwords: they stay with you, not with the project.
      </p>
      {avail === false && <div className="banner warn">This computer has no keychain the app can use.</div>}
      {kcUses.length > 0 && (
        <table className="secret-table">
          <thead>
            <tr>
              <th>Read by flows</th>
              <th>Flows</th>
              <th>In the keychain</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {kcUses.map((u) => (
              <tr key={u.name}>
                <td>
                  <Ref text={`{{keychain.${u.name}}}`} />
                </td>
                <td className="muted">{u.flows.join(", ")}</td>
                <td>
                  {u.found ? (
                    <span className="badge badge-ok">found</span>
                  ) : (
                    <span className="badge badge-danger">missing</span>
                  )}
                </td>
                <td className="secret-acts">
                  <button className="link" onClick={() => setForm({ name: u.name, value: "" })}>
                    {u.found ? "Replace" : "Add"}
                  </button>
                  {u.found && (
                    <button className="link danger" onClick={() => remove(u.name)}>
                      Remove
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <form
        className="secret-form"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        <label className="field">
          <span className="muted">Name</span>
          <input
            className={"mono" + (badName ? " bad" : "")}
            value={form.name}
            aria-label="Keychain entry name"
            placeholder="DB_PASSWORD"
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </label>
        <label className="field">
          <span className="muted">Value</span>
          <input
            type="password"
            aria-label="Keychain value"
            autoComplete="new-password"
            value={form.value}
            onChange={(e) => setForm({ ...form, value: e.target.value })}
          />
        </label>
        <div className="row-actions">
          <button
            type="button"
            className="btn secondary"
            disabled={!form.name.trim() || badName}
            onClick={() => look(form.name.trim())}
          >
            Check
          </button>
          <button
            type="submit"
            className="btn primary"
            disabled={!avail || !form.name.trim() || badName || !form.value}
          >
            Save to keychain
          </button>
        </div>
      </form>
      {check && (
        <p className="muted">
          {check.found ? <Icon.check /> : <Icon.x />} <b>{check.name}</b>{" "}
          {check.found ? "is in" : "is not in"} {check.where}
          {check.found && (
            <>
              {" · "}
              <button className="link danger" onClick={() => remove(check.name)}>
                Remove it
              </button>
            </>
          )}
        </p>
      )}
    </div>
  );
}

function EnvHelp() {
  return (
    <div className="card">
      <h3>Variables</h3>
      <p className="wf-desc">
        <code className="inline-code">{"{{env.NAME}}"}</code> reads a variable: the flow's own (a Chatflow's
        Environment › Variables), and when the environment the app runs in has a variable of the same name,
        that value wins. A name the flow does not declare is read from the environment (empty when it is not
        set). Use variables for settings, not secrets: their values are not hidden.
      </p>
    </div>
  );
}

export function SecretsSettings() {
  const [uses, reloadUses] = useLoad(() => backend.SecretRefUses(), [S.ws?.root]);
  if (!S.ws)
    return (
      <div className="card">
        <h3>Secrets</h3>
        <p className="muted">Open a project first: project secrets are kept in it.</p>
      </div>
    );
  return (
    <>
      <ProjectSecrets uses={uses} reloadUses={reloadUses} />
      <KeychainSecrets uses={uses} reloadUses={reloadUses} />
      <EnvHelp />
    </>
  );
}
