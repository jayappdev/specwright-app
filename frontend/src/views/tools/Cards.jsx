// Tools: cards of the Review step — dependencies, secrets, the managed-tool banner.
import { backend } from "../../backend.js";
import { S, update, refreshSecretsSoon } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { loadPackages, registryFor } from "../Packages.jsx";

const DEPS_HINT = {
  "Node.js": "pg@8.13.1",
  Python: "psycopg[binary]==3.2.3",
  Go: "github.com/jackc/pgx/v5@v5.7.1",
  Java: "org.postgresql:postgresql:42.7.4",
};
export const entryFile = (lang) =>
  ({ "Node.js": "index.js", Python: "main.py", Go: "main.go", Java: "Tool.java" })[lang] || "tool.sh";

export function DepsCard({ d, scope }) {
  const hint = DEPS_HINT[d.lang],
    text = (d.deps || []).join("\n");
  if (!S.pkg) loadPackages();
  const reg = registryFor(d.lang);
  return (
    <div className="card">
      <div className="card-head">
        <h3>Dependencies</h3>
        <span className="badge">{(d.deps || []).length}</span>
      </div>
      {hint ? (
        <>
          <p className="wf-desc">
            One package per line, with an exact version, e.g. <code className="inline-code">{hint}</code>.
            They are installed{" "}
            {scope === "managed" ? (
              <>into the app's private copy of this managed tool (the same in every project)</>
            ) : (
              <>
                into{" "}
                <code className="inline-code">
                  .codegen/tools/{scope === "global" ? "global" : "project"}/{d.name}/
                </code>
              </>
            )}{" "}
            when you test or save, downloaded from{" "}
            <code className="inline-code">{reg || "the default registry"}</code> through the proxy set under
            Development wizard › Packages & proxy.
          </p>
          <textarea
            rows={3}
            spellCheck="false"
            style={{ fontFamily: "var(--font-mono)" }}
            placeholder={hint}
            value={text}
            onChange={(e) => {
              d.deps = e.target.value
                .split("\n")
                .map((l) => l.trim())
                .filter(Boolean);
              update();
            }}
          />
          <div className="row-actions">
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
              Edit proxy and registries
            </button>
          </div>
        </>
      ) : (
        <p className="muted">
          Bash tools cannot have dependencies. Choose Node.js, Go, Java or Python to use packages.
        </p>
      )}
    </div>
  );
}

export function ManagedBanner({ d }) {
  const m = d?.managed;
  if (!m) return null;
  const ok = m.status === "verified";
  return (
    <div className={"banner " + (ok ? "admin-banner" : "error")}>
      <Icon.lock />{" "}
      {ok ? (
        <>
          Managed by <b>{m.signer}</b> · v{m.version} · signature verified. You can run and test it in any
          project, but only an admin can change it.
        </>
      ) : (
        <>
          This managed tool is blocked: {m.status}. It was changed outside admin mode or signed by a key this
          computer does not trust, so agents cannot run it. Ask your admin.
        </>
      )}
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
    case "session":
      return <span className="badge badge-ok">set for this session</span>;
    case "env":
      return (
        <span className="badge badge-ok" title={"Read from $" + st.env}>
          from ${st.env}
        </span>
      );
    case "keychain":
      return (
        <span className="badge badge-ok" title="Stored in the system keychain (service SpecWright)">
          in the keychain
        </span>
      );
    case "ask":
      return <span className="badge badge-info">will ask when run</span>;
    case "optional":
      return <span className="badge">optional · not set</span>;
    default:
      return (
        <span className="badge badge-warn">
          {st.source === "env" ? "$" + st.env + " not set" : "no value"}
        </span>
      );
  }
}

// Per-secret source: environment variable, ask at run time, or a session value.
export function SecretsCard({ d }) {
  const setS = (s, k, v) => {
    s[k] = v;
    update();
    refreshSecretsSoon();
  };
  return (
    <div className="card">
      <h3>Secrets</h3>
      <p className="wf-desc">
        Passed to the tool as environment variables. Each one can be read from an environment variable on this
        computer, asked for when the tool runs, or entered here for this session. Values are never saved, sent
        to the model, or written to run history.
      </p>
      {d.secrets.length ? (
        <ul className="lib-list secret-list">
          {d.secrets.map((s) => {
            const st = S.secretInfo[s.name],
              src = s.source || "auto";
            return (
              <li key={s.name}>
                <div className="secret-name">
                  <code className="inline-code">{s.name}</code>
                  <small>{s.desc}</small>
                  <label
                    className="check secret-optional"
                    title="Optional: passed when it has a value, never asked for (e.g. a password only some queries need)"
                  >
                    <input
                      type="checkbox"
                      checked={!!s.optional}
                      onChange={(e) => setS(s, "optional", e.target.checked || undefined)}
                    />{" "}
                    optional
                  </label>
                </div>
                <div className="secret-controls">
                  <select
                    aria-label={"Where " + s.name + " comes from"}
                    value={src}
                    onChange={(e) => setS(s, "source", e.target.value === "auto" ? "" : e.target.value)}
                  >
                    {SOURCES.map(([v, l]) => (
                      <option key={v} value={v}>
                        {l}
                      </option>
                    ))}
                  </select>
                  {(src === "auto" || src === "env") && (
                    <label className="secret-env">
                      <span className="muted">$</span>
                      <input
                        aria-label={"Environment variable for " + s.name}
                        value={s.env || ""}
                        placeholder={s.name}
                        spellCheck="false"
                        onChange={(e) => setS(s, "env", e.target.value.trim())}
                      />
                    </label>
                  )}
                  {(src === "auto" || src === "session") && (
                    <input
                      type="password"
                      autoComplete="off"
                      aria-label={"Value for " + s.name}
                      placeholder={src === "auto" ? "value (optional)" : "value"}
                      style={{ width: 170 }}
                      onChange={(e) => {
                        backend.SetSecret(s.name, e.target.value);
                        refreshSecretsSoon();
                      }}
                    />
                  )}
                  {src === "ask" && S.secretInfo[s.name]?.via === "session" && (
                    <button
                      className="link"
                      onClick={() => {
                        backend.SetSecret(s.name, "");
                        refreshSecretsSoon();
                      }}
                    >
                      Forget remembered value
                    </button>
                  )}
                  <SecretBadge st={st} />
                </div>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="muted">This tool needs no secrets.</p>
      )}
      <p className="muted" style={{ marginTop: 8 }}>
        Permissions:{" "}
        {(d.perms || []).map((p) => (
          <span key={p} className="badge" style={{ marginRight: 4 }}>
            {p}
          </span>
        ))}
      </p>
      <p className="muted" style={{ marginTop: 4 }}>
        Environment variables are read from the app and from your login shell (for example{" "}
        <code className="inline-code">~/.zshrc</code>), so values exported there work even when the app was
        opened from the Dock.
      </p>
    </div>
  );
}
