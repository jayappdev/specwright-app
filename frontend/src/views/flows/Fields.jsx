// Flow designer fields shared by the inspector: secret sources for inputs and
// tool parameters, and variable chips that insert {{…}} where the cursor is.
import { stepRef, loopInfo } from "../../flowNames.js";
import { useEffect, useState } from "react";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg, kcName, isWindows } from "../../store.js";
import { askText } from "../../Dialog.jsx";
import { openSettings } from "../Appearance.jsx";
import { ancestors, changed, fl, nodeOf, parentsOf } from "../Flows.jsx";
import { Field } from "./Inspector.jsx";

// --- secrets -------------------------------------------------------------------------

const SECRET_SOURCES = [
  ["ask", "Ask in the run form", "Typed when the flow runs, masked."],
  [
    "keychain",
    () => `System ${kcName()}`,
    () => `Read from the ${kcName()} when the run starts; nothing to type.`,
  ],
  ["env", "Environment variable", "For headless and CI runs."],
  [
    "auto",
    () => `${kcName()}, then environment, else ask`,
    "Uses whichever has a value; asks in the run form if none does.",
  ],
  [
    "prompt",
    "Ask in a dialog during the run",
    "Asked once the run has started: also scheduled runs, and on the terminal when headless.",
  ],
];
export const PASSWORD_PARAM =
  /(pass(word|wd|phrase)?|secret|token|api[_-]?key|private[_-]?key|credential|auth)/i;

// Where a secret input's value comes from. The flow file keeps only where
// to look, never the value.
export function SecretSourceEditor({ i }) {
  const src = i.source || "ask",
    k = i.keychain || {},
    [st, setSt] = useState(null),
    [val, setVal] = useState("");
  const ref = () => ({
    service: k.service || "SpecWright",
    account: k.account || i.name,
    kind: k.kind || "",
  });
  const set = (patch) => {
    Object.assign(i, patch);
    setSt(null);
    changed();
  };
  const setK = (patch) => {
    i.keychain = k;
    Object.assign(k, patch);
    for (const x of ["service", "account", "kind"]) if (!k[x]) delete k[x];
    if (!Object.keys(k).length) delete i.keychain;
    setSt(null);
    changed();
  };
  const check = async () => {
    try {
      setSt(await backend.CheckKeychain(ref()));
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const save = async () => {
    try {
      await backend.SaveToKeychain(ref(), val);
      setVal("");
      toast(`Saved in the ${kcName()} (${ref().service} / ${ref().account}).`);
      check();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const usesKc = src === "keychain" || src === "auto",
    usesEnv = src === "env" || src === "auto";
  return (
    <div className="fl-secret">
      <Field label="Where the value comes from">
        <select
          value={src}
          onChange={(e) => set({ source: e.target.value === "ask" ? undefined : e.target.value })}
        >
          {SECRET_SOURCES.map(([v, l]) => (
            <option key={v} value={v}>
              {typeof l === "function" ? l() : l}
            </option>
          ))}
        </select>
      </Field>
      <p className="muted fl-help">
        {(() => {
          const h = SECRET_SOURCES.find(([v]) => v === src)?.[2];
          return typeof h === "function" ? h() : h;
        })()}{" "}
        Never saved in the flow: run history, step outputs and results show{" "}
        <code className="inline-code" data-nofile>
          ••••••••
        </code>
        . Tools get it through <code className="inline-code" data-nofile>{`{{inputs.${i.name}}}`}</code> in
        their arguments; AI steps should not use it.
      </p>
      {usesKc && (
        <>
          <div className="fl-row">
            {isWindows() ? (
              <p className="muted fl-help">
                Credential Manager › Windows Credentials. SpecWright's entries are named{" "}
                <code className="inline-code" data-nofile>
                  service/account
                </code>
                ; to read one another program saved, put its <b>Internet or network address</b> under Service.
              </p>
            ) : (
              <Field
                label="Keychain item"
                hint={
                  k.kind === "internet"
                    ? "An internet password saved for a server (e.g. by Safari)"
                    : "A password saved under a service and account"
                }
              >
                <select value={k.kind || ""} onChange={(e) => setK({ kind: e.target.value })}>
                  <option value="">Password (service + account)</option>
                  <option value="internet">Internet password (server + account)</option>
                </select>
              </Field>
            )}
          </div>
          <div className="fl-row">
            <Field label={k.kind === "internet" ? "Server" : "Service"}>
              <input
                value={k.service || ""}
                placeholder={k.kind === "internet" ? "github.com" : "SpecWright"}
                onChange={(e) => setK({ service: e.target.value.trim() })}
              />
            </Field>
            <Field label="Account">
              <input
                value={k.account || ""}
                placeholder={i.name}
                onChange={(e) => setK({ account: e.target.value.trim() })}
              />
            </Field>
          </div>
          <div className="fl-row fl-secret-kc">
            <button type="button" className="btn secondary small" onClick={check}>
              Check the {kcName()}
            </button>
            {st && (
              <span className={st.found ? "ok-text" : "muted"}>
                {!st.available
                  ? "No keychain on this computer"
                  : st.found
                    ? `✓ Found: ${st.where}`
                    : `Nothing in ${st.where} yet`}
              </span>
            )}
          </div>
          {k.kind !== "internet" && (
            <div className="fl-row fl-secret-kc">
              <input
                type="password"
                value={val}
                autoComplete="new-password"
                data-1p-ignore
                data-lpignore="true"
                placeholder="Save a value now (optional)"
                aria-label={`Value to save in the ${kcName()}`}
                onChange={(e) => setVal(e.target.value.replace(/[\r\n]/g, ""))}
              />
              <button type="button" className="btn secondary small" disabled={!val} onClick={save}>
                Save to {kcName()}
              </button>
            </div>
          )}
          {!isWindows() && k.kind !== "internet" && (k.service || "SpecWright") !== "SpecWright" && (
            <p className="muted fl-help">
              macOS asks once before reading an item another app saved (the prompt names{" "}
              <code className="inline-code" data-nofile>
                security
              </code>
              ): choose <b>Always Allow</b> so later runs don't ask.
            </p>
          )}
          {!isWindows() && k.kind === "internet" && (
            <p className="muted fl-help">
              macOS asks once before reading it (the prompt names{" "}
              <code className="inline-code" data-nofile>
                security
              </code>
              ): choose <b>Always Allow</b> so later runs don't ask.
            </p>
          )}
        </>
      )}
      {usesEnv && (
        <Field
          label="Environment variable"
          hint={src === "auto" ? `Optional: tried when the ${kcName()} has nothing` : undefined}
        >
          <input
            value={i.env || ""}
            placeholder={i.name.toUpperCase()}
            style={{ fontFamily: "var(--font-mono)" }}
            onChange={(e) => set({ env: e.target.value.replace(/[^A-Za-z0-9_]/g, "") || undefined })}
          />
        </Field>
      )}
    </div>
  );
}

// "🔒 Use a secret" next to a tool argument: an existing secret input, or a
// new one (asked in the run form) named after the argument.
export function SecretPick({ args, p }) {
  const flow = fl().flow,
    secrets = flow.inputs.filter((i) => i.type === "secret"),
    projectSecrets = useProjectSecrets();
  const pick = (v) => {
    if (!v) return;
    if (v.startsWith("{{")) {
      args[p.name] = v;
      changed();
      return;
    }
    if (v === "+keychain") {
      askText("Keychain entry", {
        message: `The name of SpecWright's entry in the ${kcName()} (add or check it in Settings › Secrets).`,
        placeholder: p.name.toUpperCase(),
      }).then((n) => {
        if (n && n.trim()) {
          args[p.name] = `{{keychain.${n.trim()}}}`;
          changed();
        }
      });
      return;
    }
    let name = v;
    if (v === "+new") {
      const base =
        p.name
          .toLowerCase()
          .replace(/[^a-z0-9_]/g, "_")
          .replace(/^_+|_+$/g, "") || "secret";
      name = base;
      let k = 2;
      while (flow.inputs.some((i) => i.name === name)) name = base + "_" + k++;
      flow.inputs.push({
        name,
        label: p.name.replace(/[_-]+/g, " ").replace(/^./, (c) => c.toUpperCase()),
        type: "secret",
        required: true,
      });
      toast(
        `Added the secret input “${name}”. Choose where its value comes from under Inputs (run form, ${kcName()}, environment, or a dialog).`,
      );
    }
    args[p.name] = `{{inputs.${name}}}`;
    changed();
  };
  return (
    <select
      className="fl-use fl-secret-pick"
      value=""
      aria-label={`Use a secret for ${p.name}`}
      title={`Fill this argument from a secret (asked at run time or read from the ${kcName()})`}
      onChange={(e) => pick(e.target.value)}
    >
      <option value="">🔒 Secret…</option>
      {secrets.map((i) => (
        <option key={i.name} value={i.name}>
          {i.label || i.name}
        </option>
      ))}
      <option value="+new">+ New secret input</option>
      {projectSecrets.length > 0 && (
        <optgroup label="Project secrets">
          {projectSecrets.map((s) => (
            <option key={s.name} value={`{{project_secret.${s.name}}}`}>
              {s.name}
            </option>
          ))}
        </optgroup>
      )}
      <option value="+keychain">{kcName()} entry…</option>
    </select>
  );
}

// --- secrets from outside the flow: {{project_secret.X}}, {{keychain.X}} ---------------

// useProjectSecrets: the project's secret names (loaded once per project)
function useProjectSecrets() {
  const root = S.ws?.root;
  useEffect(() => {
    if (!root || S.projectSecretsFor === root) return;
    S.projectSecretsFor = root;
    backend
      .ProjectSecrets()
      .then((l) => {
        S.projectSecrets = l || [];
        update();
      })
      .catch(() => {});
  }, [root]);
  return S.projectSecrets || [];
}

// SecretRefPick: a menu that puts {{project_secret.X}} or {{keychain.X}}
// in a field (fill: replace a tool argument; else insert at the cursor)
export function SecretRefPick({ fill, label = "🔒 Stored secret…" }) {
  const ps = useProjectSecrets();
  const put = (ref) => (fill ? fill(ref) : insertText(ref));
  const pick = async (v) => {
    if (!v) return;
    if (v === "+keychain") {
      const name = await askText("Keychain entry", {
        message: `The name of SpecWright's entry in the ${kcName()} (add or check it in Settings › Secrets).`,
        placeholder: "DB_PASSWORD",
      });
      if (name && name.trim()) put(`{{keychain.${name.trim()}}}`);
    } else if (v === "+settings") openSettings("secrets");
    else put(`{{project_secret.${v}}}`);
  };
  return (
    <select
      className="fl-use fl-secret-pick"
      value=""
      aria-label="Use a stored secret"
      title="A project secret (encrypted in this project) or a keychain entry: looked up when the flow runs, hidden everywhere"
      onChange={(e) => pick(e.target.value)}
    >
      <option value="">{label}</option>
      {ps.length > 0 && (
        <optgroup label="Project secrets">
          {ps.map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </optgroup>
      )}
      <option value="+keychain">{kcName()} entry…</option>
      <option value="+settings">Manage secrets in Settings…</option>
    </select>
  );
}

// --- variable chips: insert {{…}} where the cursor is ---------------------------------

export let lastField = null;
export const track = {
  onFocus: (e) => {
    lastField = e.target;
  },
};
export function insertVar(v) {
  insertText(`{{${v}}}`);
}
export function insertText(text, replace) {
  const el = lastField;
  if (!el || !document.body.contains(el)) return toast("Click in a field first, then pick a variable.");
  if (replace) {
    el.setSelectionRange(0, el.value.length);
  }
  const s = el.selectionStart ?? el.value.length,
    e = el.selectionEnd ?? s;
  const val = el.value.slice(0, s) + text + el.value.slice(e);
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, val);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.focus();
  el.setSelectionRange(s + text.length, s + text.length);
}

export function VarChips({ node, extra = [] }) {
  const f = fl(),
    flow = f.flow;
  const anc = node ? [...ancestors(flow, node.id)].filter((id) => id !== "start") : [];
  const fed = node && parentsOf(flow, node.id).some((p) => p !== "start");
  const vars = [
    ...flow.inputs
      .filter((i) => i.type !== "secret" || node?.type !== "ai")
      .map((i) => ["inputs." + i.name, (i.type === "secret" ? "🔒 " : "") + (i.label || i.name)]),
    ...anc.flatMap((id) => {
      const r = stepRef(flow, id),
        l = nodeOf(id)?.title || nodeOf(id)?.tool || id;
      return [
        [r + ".buffer_output", l + " · text"],
        [r + ".output_file", l + " · file"],
      ];
    }),
    ...(fed && node.type === "tool" ? [["input_file", "connected steps, as a file"]] : []),
    ...(node && (loopInfo(flow).stack[node.id] || []).length
      ? [
          ["item", "↻ item"],
          ["item_index", "↻ item no."],
          ["item_count", "↻ item count"],
        ]
      : []),
    // nested loops: each loop's item by the loop's name
    ...(node && (loopInfo(flow).stack[node.id] || []).length > 1
      ? loopInfo(flow).stack[node.id].flatMap((l) => {
          const r = stepRef(flow, l),
            t = nodeOf(l)?.title || l;
          return [
            [r + ".item", "↻ " + t + " · item"],
            [r + ".item_index", "↻ " + t + " · no."],
          ];
        })
      : []),
    ["context", "context"],
    ["skills", "skills"],
    // the flow's variables (and a Chatflow's secrets: not for an AI step)
    ...(flow.vars || []).filter((v) => v.name).map((v) => ["env." + v.name, "$" + v.name]),
    ...(node?.type === "ai"
      ? []
      : (flow.secrets || []).filter((v) => v.name).map((v) => ["env." + v.name, "🔒 $" + v.name])),
    ...extra.map((v) => [v, v]),
  ];
  return (
    <div className="fl-vars">
      <span className="muted">Insert:</span>
      {vars.map(([v, l]) => (
        <button
          key={v}
          type="button"
          className="fl-var"
          title={`{{${v}}}`}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => insertVar(v)}
        >
          {l}
        </button>
      ))}
      {node?.type !== "ai" && <SecretRefPick />}
    </div>
  );
}

// What an edge carries, shown on the connection: the arguments of the
// target that read the source, "AI" for a described step, "stdin" for a tool
// that takes it on standard input only.
