// Admin: unlock admin mode, publish managed tools (global to the app and
// read-only for everyone else) with the Tools screen, and manage which
// admin keys this computer trusts.
import { useEffect, useState } from "react";
import { confirmDialog, askText } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { EmptyState, LearnMore } from "../ui.jsx";
import { S, update, toast, errMsg, refreshAdmin, refreshExtensions, saveText } from "../store.js";
import { Icon, Card } from "../ui.jsx";
import { openTool, newManagedTool } from "./Tools.jsx";

// publishFromFile: a tool from a tool file, opened in the tool editor in
// admin mode (review, test, then Publish to all projects).
async function publishFromFile(text) {
  try {
    const tools = (await backend.ReadToolFile(text)) || [];
    let t = tools[0];
    if (tools.length > 1) {
      const pick = await askText("Which tool to publish?", {
        message:
          "The file has " +
          tools.length +
          " tools: " +
          tools.map((x) => x.name).join(", ") +
          ". Publish one at a time.",
        value: tools[0].name,
        ok: "Open",
      });
      t = tools.find((x) => x.name === (pick || "").trim());
      if (!t) return;
    }
    newManagedTool(true);
    Object.assign(S.tb, {
      draft: { ...t, scope: "managed" },
      stage: "review",
      prompt: t.prompt || "",
      testPrompt: t.testPrompt || "",
    });
    S.view = "tools";
    update();
    toast(`${t.name} is open in the tool editor: review it, test it, then publish it.`);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}
import { loadBuiltins, selectBuiltin } from "./Builtins.jsx";

// a pasted public key: base64 that decodes to at least 16 bytes
const keyOk = (k) => {
  try {
    return /^[A-Za-z0-9+/_-]+={0,2}$/.test(k) && atob(k.replace(/-/g, "+").replace(/_/g, "/")).length >= 16;
  } catch {
    return false;
  }
};

function KeySetup({ a }) {
  const [name, setName] = useState(""),
    [p1, setP1] = useState(""),
    [p2, setP2] = useState(""),
    [busy, setBusy] = useState(false);
  if (a.hasKey) return null;
  const create = async (e) => {
    e.preventDefault();
    if (p1 !== p2) return toast("The passphrases do not match.");
    setBusy(true);
    try {
      S.admin = await backend.CreateAdminKey(name, p1);
      toast("Admin key created and unlocked.");
      await refreshAdmin();
    } catch (err) {
      toast(errMsg(err), "error");
    }
    setBusy(false);
  };
  return (
    <form className="card" onSubmit={create} autoComplete="off">
      <div className="card-head">
        <Icon.shield />
        <h3>Become the admin on this computer</h3>
      </div>
      <p className="wf-desc">
        Creates a signing key that is encrypted with your passphrase and stored only on this computer. Tools
        you publish are signed with it, so nobody without the key and passphrase can change them.
      </p>
      <div className="two-col">
        <label className="field">
          <span className="muted">Your name (shown on every tool you publish)</span>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Platform team" />
        </label>
        <span />
        <label className="field">
          <span className="muted">Passphrase (10+ characters)</span>
          <input
            type="password"
            value={p1}
            aria-invalid={(p1.length > 0 && p1.length < 10) || undefined}
            aria-describedby="ak-p1"
            onChange={(e) => setP1(e.target.value)}
          />
          <small id="ak-p1" className={p1 && p1.length < 10 ? "field-error" : "muted"}>
            {!p1
              ? "At least 10 characters."
              : p1.length < 10
                ? `${10 - p1.length} more character${10 - p1.length === 1 ? "" : "s"} needed.`
                : "Long enough."}
          </small>
        </label>
        <label className="field">
          <span className="muted">Repeat passphrase</span>
          <input
            type="password"
            value={p2}
            aria-invalid={(p2.length > 0 && p2 !== p1) || undefined}
            aria-describedby="ak-p2"
            onChange={(e) => setP2(e.target.value)}
          />
          {p2 && (
            <small id="ak-p2" className={p2 !== p1 ? "field-error" : "muted"}>
              {p2 !== p1 ? "Does not match the passphrase yet." : "Matches."}
            </small>
          )}
        </label>
      </div>
      <p className="muted" style={{ marginTop: 8 }}>
        There is no recovery: if the passphrase is lost, create a new key and publish the tools again.
      </p>
      <div className="row-actions">
        <button className="btn primary" disabled={busy || !name.trim() || p1.length < 10 || p1 !== p2}>
          {busy ? "Creating…" : "Create admin key"}
        </button>
      </div>
    </form>
  );
}

function LockCard({ a }) {
  const [p, setP] = useState(""),
    [busy, setBusy] = useState(false);
  if (!a.hasKey) return null;
  const unlock = async (e) => {
    e.preventDefault();
    setBusy(true);
    try {
      S.admin = await backend.UnlockAdmin(p);
      setP("");
      toast("Admin mode unlocked for this session.");
      await refreshAdmin();
    } catch (err) {
      toast(errMsg(err), "error");
    }
    setBusy(false);
  };
  return (
    <div className={"card" + (a.unlocked ? " admin-card" : "")}>
      <div className="card-head">
        <Icon.shield />
        <h3>Admin mode</h3>
        {a.unlocked ? (
          <span className="badge badge-warn">unlocked</span>
        ) : (
          <span className="badge">
            <Icon.lock /> locked
          </span>
        )}
        {!a.keyTrusted && <span className="badge badge-warn">key not trusted here</span>}
      </div>
      <dl className="kv">
        <dt>Admin</dt>
        <dd>{a.name}</dd>
        <dt>Key</dt>
        <dd>
          <code className="inline-code">{a.keyId}</code>
        </dd>
      </dl>
      {a.unlocked ? (
        <div className="row-actions">
          <button
            className="btn secondary"
            onClick={async () => {
              await backend.LockAdmin();
              toast("Admin mode locked.");
              refreshAdmin();
              if (S.tb?.admin) S.tb = null;
            }}
          >
            Lock
          </button>
          <span className="muted">Locks when the app quits.</span>
        </div>
      ) : (
        <form className="setup-input" style={{ marginTop: 10 }} onSubmit={unlock}>
          <input
            id="admin-pass"
            type="password"
            placeholder="Admin passphrase"
            value={p}
            onChange={(e) => setP(e.target.value)}
            autoComplete="current-password"
          />
          <button className="btn primary" disabled={busy || !p}>
            {busy ? "Checking…" : "Unlock"}
          </button>
        </form>
      )}
    </div>
  );
}

function ManagedCard({ a }) {
  const list = S.managed || [];
  const retire = async (t) => {
    if (
      !(await confirmDialog("It disappears from every project on this computer.", {
        title: `Retire ${t.name}?`,
        ok: "Retire",
        danger: true,
      }))
    )
      return;
    try {
      await backend.DeleteManagedTool(t.name);
      toast(`${t.name} retired.`);
      await refreshAdmin();
      refreshExtensions();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <Card title="Managed tools" badge={<span className="badge">{list.length}</span>}>
      <p className="wf-desc">Tools every project can run; only an admin can change them.</p>
      <LearnMore summary="Where they are kept">
        Global to the app: available in every project, runnable and testable by everyone, editable only in
        admin mode. Published to <code className="inline-code">{a.managedFile}</code>
        {a.machineCatalog ? (
          <>
            ; IT can also deploy a read-only catalog at{" "}
            <code className="inline-code">{a.machineCatalog}</code>
          </>
        ) : null}
        .
      </LearnMore>
      <ul className="lib-list">
        {list.map((t) => (
          <li key={t.name}>
            <span style={{ color: "var(--muted)" }}>
              <Icon.lock />
            </span>
            <div className="grow">
              <b>{t.name}</b>{" "}
              <span className={"badge " + (t.managed?.status === "verified" ? "badge-info" : "badge-warn")}>
                {t.managed?.status === "verified" ? "v" + t.managed.version : t.managed?.status}
              </span>
              <small>
                {t.desc} · signed by {t.managed?.signer}{" "}
                {t.managed?.signedAt ? "on " + new Date(t.managed.signedAt).toLocaleDateString() : ""}
              </small>
            </div>
            <button className="link" onClick={() => openTool(t)}>
              {a.unlocked ? "Edit" : "View"}
            </button>
            {a.unlocked && (
              <button className="link" onClick={() => retire(t)}>
                Retire
              </button>
            )}
          </li>
        ))}
        {!list.length && (
          <li>
            <EmptyState compact>
              No managed tools yet. Managed tools are signed by an admin; every project sees them and nobody
              else can change them.
            </EmptyState>
          </li>
        )}
      </ul>
      <div className="row-actions">
        <button className="btn primary" disabled={!a.unlocked} onClick={() => newManagedTool(false)}>
          + New managed tool with AI
        </button>
        <button className="btn secondary" disabled={!a.unlocked} onClick={() => newManagedTool(true)}>
          Write one myself
        </button>
        <label
          className={"btn secondary" + (a.unlocked ? "" : " disabled")}
          title="A tool file (*.tools.json, as Tools › Export writes, or one of library/tools): open it in the tool editor, test it and publish it"
        >
          Import a tool file to publish…
          <input
            type="file"
            accept=".json,application/json"
            hidden
            disabled={!a.unlocked}
            onChange={async (e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) await publishFromFile(await f.text());
            }}
          />
        </label>
        {!a.unlocked && a.hasKey && (
          <span className="muted">
            <button
              className="link"
              onClick={() => {
                const el = document.getElementById("admin-pass");
                el?.scrollIntoView({ block: "center" });
                el?.focus();
              }}
            >
              Unlock admin mode
            </button>{" "}
            to publish.
          </span>
        )}
        {!a.unlocked && !a.hasKey && <span className="muted">Become the admin above to publish.</span>}
        {a.unlocked && !S.ws && (
          <span className="muted">Open a project to test tools before publishing.</span>
        )}
      </div>
    </Card>
  );
}

function BuiltinsCard({ a }) {
  loadBuiltins();
  if (!a.unlocked) return null;
  const list = S.builtins || [],
    off = list.filter((t) => !t.enabled);
  return (
    <Card
      title="Built-in tools"
      badge={
        <span className="badge">
          {list.length - off.length} of {list.length} on
        </span>
      }
    >
      <p className="wf-desc">
        Compiled into SpecWright, so they cannot be modified by anyone, and attached to every workflow step
        and playground prompt. Only admins see them here and on the Tools screen; regular users never browse
        them, but their agents always use them. To switch one off on a computer, IT adds it to{" "}
        <code className="inline-code">"disabledBuiltinTools"</code> in{" "}
        <code className="inline-code">{a.policyPath}</code>, e.g.{" "}
        <code className="inline-code">["run_command", "fetch_webpage"]</code>.
      </p>
      <div className="chips">
        {list.map((t) => (
          <button
            key={t.name}
            className="chip"
            aria-pressed={false}
            onClick={() => selectBuiltin(t.name)}
            title={t.description}
            style={t.enabled ? {} : { textDecoration: "line-through", opacity: 0.7 }}
          >
            <code>{t.name}</code>
          </button>
        ))}
      </div>
    </Card>
  );
}

function TrustCard({ a }) {
  const [name, setName] = useState(""),
    [key, setKey] = useState("");
  const add = async () => {
    try {
      await backend.TrustAdminKey(name, key);
      setName("");
      setKey("");
      toast("Admin key trusted on this computer.");
      refreshAdmin();
      refreshExtensions();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <Card title="Trusted admins">
      <p className="wf-desc">Managed tools run only when they are signed by a key trusted here.</p>
      <LearnMore summary="How trust works">
        Managed tools only run when their signature matches a trusted admin key. Keys in the machine policy (
        <code className="inline-code">{a.policyPath}</code>) cannot be changed by regular users
        {a.policyFound ? "" : "; no policy file is installed on this computer"}.
        {a.allowLocal
          ? " Keys can also be trusted for this user only."
          : " The policy does not allow trusting keys locally."}
      </LearnMore>
      {a.policyError && <div className="banner error">{a.policyError}</div>}
      <ul className="lib-list">
        {(a.trusted || []).map((t) => (
          <li key={t.id + t.source}>
            <span className={"badge " + (t.source === "policy" ? "badge-ok" : "")}>
              {t.source === "policy" ? "machine policy" : "this user"}
            </span>
            <div className="grow">
              <b>{t.name || "(unnamed)"}</b>
              <small>
                <code>{t.id}</code>
              </small>
            </div>
            {t.source === "local" && (
              <button
                className="link"
                onClick={async () => {
                  if (
                    await confirmDialog("Tools signed with this key will be blocked.", {
                      title: `Stop trusting ${t.name || t.id}?`,
                      ok: "Stop trusting",
                      danger: true,
                    })
                  ) {
                    await backend.UntrustAdminKey(t.id);
                    refreshAdmin();
                    refreshExtensions();
                  }
                }}
              >
                Remove
              </button>
            )}
          </li>
        ))}
        {!(a.trusted || []).length && <li className="muted">No trusted admin keys.</li>}
      </ul>
      {a.allowLocal && (
        <div className="two-col" style={{ marginTop: 10 }}>
          <label className="field">
            <span className="muted">Admin name</span>
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="field">
            <span className="muted">Public key (base64)</span>
            <div className="setup-input">
              <input
                value={key}
                onChange={(e) => setKey(e.target.value.trim())}
                aria-invalid={(!!key && !keyOk(key)) || undefined}
                aria-describedby="ak-key"
                style={{ fontFamily: "var(--font-mono)" }}
              />
              <button className="btn secondary" disabled={!keyOk(key)} onClick={add}>
                Trust
              </button>
            </div>
            {key && (
              <small id="ak-key" className={keyOk(key) ? "muted" : "field-error"}>
                {keyOk(key)
                  ? `Key ${key.slice(0, 8)}…${key.slice(-8)}`
                  : "This does not look like a base64 public key. Paste the key from Copy public key on the other admin's computer."}
              </small>
            )}
          </label>
        </div>
      )}
      {a.hasKey && (
        <div className="sub-row">
          <span>
            <b>Your public key</b>{" "}
            <span className="muted">· share it so other computers trust your tools</span>
          </span>
          <span style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <button
              className="btn secondary"
              onClick={() =>
                navigator.clipboard?.writeText(a.publicKey).then(
                  () => toast("Public key copied."),
                  () => {},
                )
              }
            >
              Copy public key
            </button>
            <button
              className="btn secondary"
              onClick={async () => {
                try {
                  saveText("policy.json", await backend.AdminPolicySnippet(), "application/json");
                } catch (e) {
                  toast(errMsg(e), "error");
                }
              }}
            >
              Download policy.json for IT
            </button>
          </span>
        </div>
      )}
    </Card>
  );
}

export function Admin() {
  useEffect(() => {
    refreshAdmin();
  }, []);
  const a = S.admin;
  if (!a)
    return (
      <section className="view">
        <h1>Admin</h1>
        <p className="muted">Loading…</p>
      </section>
    );
  return (
    <section className="view">
      <header className="view-head">
        <h1>Admin</h1>
        {a.unlocked && (
          <span className="badge badge-warn">
            <Icon.shield /> admin mode
          </span>
        )}
      </header>
      <p className="wf-desc">Publish signed tools that every project can run and nobody else can change.</p>
      <LearnMore>
        Admins publish tools for the whole app. They are built and tested on the Tools screen like any other
        tool, then signed and published: every project sees them, and regular users cannot edit, replace or
        remove them.
      </LearnMore>
      <KeySetup a={a} />
      <LockCard a={a} />
      <ManagedCard a={a} />
      <BuiltinsCard a={a} />
      <TrustCard a={a} />
    </section>
  );
}
