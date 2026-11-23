// Run flows: setting up a run — inputs (incl. secrets), credentials and options.
import { DateTimeField } from "../../DateTimeField.jsx";
import { resolveDate } from "../../dates.js";
import { ModelPicker } from "../../ModelPicker.jsx";
import { backend } from "../../backend.js";
import { update, toast, errMsg, kcName, isWindows } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { openFlowInDesigner } from "../Flows.jsx";
import { AttachBar, DropZone, onPasteAttach, onPasteFiles } from "../../Attachments.jsx";
import { Commands, frs, loadFlow, start } from "../FlowRun.jsx";
import { TagEditor, allTags } from "../../flowLists.jsx";

// --- setup ------------------------------------------------------------------------

// A secret: where its value comes from (the run form, the Keychain, an
// environment variable, or a dialog once the run starts). Typed values are
// masked, with a button to look at them; the browser is asked not to
// remember or autofill them. The saved value itself never comes here.
function SecretField({ i, v, set }) {
  const r = frs(),
    shown = !!(r.reveal ||= {})[i.name],
    src = i.source || "ask",
    st = r.secretStatus?.[i.name];
  const kcRef = { service: i.keychain?.service || "SpecWright", account: i.keychain?.account || i.name };
  const canSave = r.keychain && (src === "keychain" || src === "auto") && i.keychain?.kind !== "internet";
  const sep = isWindows() ? "/" : " / ";
  const typing = src === "ask" || (src !== "prompt" && (!st?.found || (r.typeSecret ||= {})[i.name]));
  const masked = (
    <div className="fr-secret">
      <input
        type={shown ? "text" : "password"}
        value={v}
        autoComplete="new-password"
        autoCorrect="off"
        autoCapitalize="off"
        spellCheck={false}
        data-1p-ignore
        data-lpignore="true"
        aria-label={i.label || i.name}
        placeholder={st?.found ? "Type a value for this run only" : "Typed values are hidden"}
        onChange={(e) => set(e.target.value.replace(/[\r\n]/g, ""))}
      />
      <button
        type="button"
        className="btn secondary small"
        aria-pressed={shown}
        aria-label={shown ? "Hide the value" : "Show the value"}
        onClick={() => {
          r.reveal[i.name] = !shown;
          update();
        }}
      >
        {shown ? "Hide" : "Show"}
      </button>
    </div>
  );
  if (src === "prompt")
    return (
      <div className="fr-secret-src muted">
        🔒 Asked in a dialog when the run starts (also for scheduled runs).
      </div>
    );
  return (
    <>
      {src !== "ask" && (
        <div className={"fr-secret-src " + (st?.found ? "ok" : "warn")}>
          {st?.found ? "🔑 " : "⚠ "}
          {st?.found ? (
            <>
              Taken from <b>{st.where}</b>
            </>
          ) : (
            <>
              Not found in <b>{st?.where || `${kcName()} · ${kcRef.service} / ${kcRef.account}`}</b>
              {i.required ? ": type it below" : ""}
            </>
          )}
          {st?.found && (
            <>
              {" "}
              ·{" "}
              <button
                type="button"
                className="link"
                onClick={() => {
                  r.typeSecret[i.name] = !r.typeSecret[i.name];
                  if (!r.typeSecret[i.name]) set("");
                  update();
                }}
              >
                {r.typeSecret[i.name] ? "Use the saved value" : "Type a different value for this run"}
              </button>
            </>
          )}
        </div>
      )}
      {typing && masked}
      {typing && canSave && (
        <label className="check fr-secret-save">
          <input
            type="checkbox"
            checked={!!(r.saveSecret ||= {})[i.name]}
            onChange={(e) => {
              r.saveSecret[i.name] = e.target.checked;
              update();
            }}
          />{" "}
          Save to {kcName()} ({kcRef.service}
          {sep}
          {kcRef.account}) for later runs
        </label>
      )}
    </>
  );
}

// One input, by its type: text, long text (with attachments), number, yes/no,
// choice, one file, several files, or the clipboard (text, images, files).
function InputField({ i }) {
  const r = frs(),
    v = r.inputs[i.name] ?? "",
    set = (x) => {
      r.inputs[i.name] = x;
      update();
    };
  const list = (r.files[i.name] ||= []);
  const label = (
    <span>
      <b>{i.label || i.name}</b>
      {i.required && <span className="err-text"> *</span>}{" "}
      <code className="inline-code" data-nofile>{`{{inputs.${i.name}}}`}</code>
    </span>
  );
  const append = (t) => set((v ? v.replace(/\n*$/, "\n") : "") + t);
  let field,
    simple = true;
  switch (i.type) {
    case "multiline":
      simple = false;
      field = (
        <DropZone list={list}>
          <textarea rows={5} value={v} onChange={(e) => set(e.target.value)} onPaste={onPasteAttach(list)} />
          <AttachBar list={list} insertText={append} />
        </DropZone>
      );
      break;
    case "secret":
      simple = false;
      field = <SecretField i={i} v={v} set={set} />;
      break;
    case "number":
      field = <input type="number" step="any" value={v} onChange={(e) => set(e.target.value)} />;
      break;
    case "date":
    case "datetime": {
      const dt = i.type === "datetime";
      field = (
        <span className="date-field">
          {dt ? (
            <DateTimeField value={v} label={i.label || i.name} onChange={set} />
          ) : (
            <input
              type="date"
              value={v}
              aria-label={i.label || i.name}
              onChange={(e) => set(e.target.value)}
            />
          )}
          <button
            type="button"
            className="link"
            onClick={() => set(resolveDate(i.type, dt ? "now" : "today"))}
          >
            {dt ? "Now" : "Today"}
          </button>
          {v && (
            <button type="button" className="link" onClick={() => set("")}>
              Clear
            </button>
          )}
        </span>
      );
      break;
    }
    case "boolean":
      field = (
        <label className="check">
          <input
            type="checkbox"
            checked={v === "true"}
            onChange={(e) => set(e.target.checked ? "true" : "false")}
          />{" "}
          Yes
        </label>
      );
      break;
    case "multichoice": {
      simple = false;
      const opts = (i.options || []).filter(Boolean),
        picked = new Set(v.split("\n").filter(Boolean));
      const setPicked = (next) => set(opts.filter((o) => next.has(o)).join("\n"));
      field = (
        <div className="mc-list" role="group" aria-label={i.label || i.name}>
          {opts.map((o) => (
            <label key={o} className="check">
              <input
                type="checkbox"
                checked={picked.has(o)}
                onChange={(e) => {
                  const n = new Set(picked);
                  e.target.checked ? n.add(o) : n.delete(o);
                  setPicked(n);
                }}
              />{" "}
              {o}
            </label>
          ))}
          {opts.length > 3 && (
            <span className="mc-all">
              <button type="button" className="link" onClick={() => setPicked(new Set(opts))}>
                All
              </button>{" "}
              <button type="button" className="link" onClick={() => setPicked(new Set())}>
                None
              </button>{" "}
              <span className="muted">
                {picked.size} of {opts.length}
              </span>
            </span>
          )}
        </div>
      );
      break;
    }
    case "choice":
      field = (
        <select value={v} onChange={(e) => set(e.target.value)}>
          <option value="">Choose…</option>
          {(i.options || []).filter(Boolean).map((o) => (
            <option key={o}>{o}</option>
          ))}
        </select>
      );
      break;
    case "file":
    case "files": {
      simple = false;
      const single = i.type === "file";
      field = (
        <DropZone list={list} single={single} className="fr-drop">
          {!list.length && (
            <div className="muted fr-drop-hint">
              {single ? "Drop a file here, or choose one" : "Drop files here, or choose them"}
              {i.accept ? (
                <>
                  {" "}
                  ·{" "}
                  <code className="inline-code" data-nofile>
                    {i.accept}
                  </code>
                </>
              ) : null}
            </div>
          )}
          <AttachBar
            list={list}
            single={single}
            accept={i.accept}
            hint={single ? "one file · up to 25 MB" : "several files · up to 25 MB each"}
          />
        </DropZone>
      );
      break;
    }
    case "clipboard":
      simple = false;
      field = (
        <DropZone list={list}>
          <textarea
            rows={4}
            value={v}
            placeholder="Paste here (⌘V): text stays text; images and files are attached"
            onChange={(e) => set(e.target.value)}
            onPaste={onPasteFiles(list)}
          />
          <AttachBar
            list={list}
            insertText={append}
            textInline
            hint="text, images (screenshots, diagrams), PDFs or files"
          />
        </DropZone>
      );
      break;
    default:
      field = <input value={v} onChange={(e) => set(e.target.value)} />;
  }
  const Wrap = simple ? "label" : "div";
  return (
    <Wrap className="field fr-input">
      {label}
      {field}
      {i.description && <small className="muted">{i.description}</small>}
    </Wrap>
  );
}

const VIA = {
  env: (c) => <span className="badge badge-ok">${c.env} is set</span>,
  keychain: () => (
    <span className="badge badge-ok">
      <Icon.lock /> in the keychain
    </span>
  ),
  session: () => <span className="badge badge-ok">entered this session</span>,
  ask: () => <span className="badge">will ask when needed</span>,
  missing: (c) => (
    <span className="badge badge-warn">{c.source === "env" ? "$" + c.env + " not set" : "no value"}</span>
  ),
};

function Credentials() {
  const r = frs();
  if (!r.creds.length) return null;
  const save = async (c, where) => {
    const v = r.secretVals[c.name];
    if (!v) return toast("Type the value first.");
    try {
      if (where === "keychain") await backend.SaveSecretToKeychain(c.name, v);
      else await backend.SetSecret(c.name, v);
      r.secretVals[c.name] = "";
      r.creds = await backend.FlowCredentials(r.flowId);
      toast(where === "keychain" ? `${c.name} saved in the keychain.` : `${c.name} set for this session.`);
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  return (
    <div className="card">
      <div className="card-head">
        <Icon.lock />
        <h3>Credentials</h3>
        <span className="badge">{r.creds.length}</span>
      </div>
      <p className="wf-desc">
        The flow's tools need these. Each is read from the session, the environment variable or the system
        keychain; if none has it, the run pauses and asks you.
      </p>
      {r.creds.map((c) => (
        <div key={c.name} className="fr-cred">
          <div>
            <code className="inline-code" data-nofile>
              {c.name}
            </code>{" "}
            {(VIA[c.via] || VIA.ask)(c)}{" "}
            <small className="muted">
              {c.desc}
              {c.tools?.length ? " · " + c.tools.join(", ") : ""}
            </small>
          </div>
          <div className="fr-cred-row">
            <input
              type="password"
              autoComplete="off"
              placeholder={c.via === "ask" || c.via === "missing" ? "Enter a value" : "Replace the value"}
              value={r.secretVals[c.name] || ""}
              onChange={(e) => {
                r.secretVals[c.name] = e.target.value;
                update();
              }}
            />
            <button className="btn secondary" onClick={() => save(c, "session")}>
              Use this session
            </button>
            {r.keychain && (
              <button
                className="btn secondary"
                title="Kept by the operating system; found next time without asking"
                onClick={() => save(c, "keychain")}
              >
                <Icon.lock /> Save in keychain
              </button>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

export const REVIEW_HINT = {
  refine: "Say what should be different; the AI redoes this step's result and the flow continues from it.",
  command: "Reshape the result with a shell command, such as jq, sort or grep.",
  edit: "Change the result by hand; the flow continues with your version.",
  retry: "Change this step's settings and run it again with the same inputs.",
};
const REVIEW_LABEL = {
  ai: "reviews after AI steps",
  each: "review after every step",
  marked: "reviews only marked steps",
  none: "no reviews",
};
// the summary line: the review mode, then only what differs from the defaults
function optionsSummary(o, f) {
  const name = (id) => f.nodes.find((n) => n.id === id)?.title || id,
    out = [REVIEW_LABEL[o.review] || o.review];
  if (+o.repeat > 1) out.push(`${o.repeat} times`);
  if (o.forEach.input)
    out.push(
      `once per item of ${f.inputs.find((i) => i.name === o.forEach.input)?.label || o.forEach.input}`,
    );
  if (o.startAt)
    out.push("starts " + new Date(o.startAt).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }));
  if (+o.timeoutMinutes) out.push(`stops after ${o.timeoutMinutes} min`);
  if (+o.stepTimeoutSeconds) out.push(`${o.stepTimeoutSeconds} s per step`);
  if (o.stopAfter) out.push(`stops after ${name(o.stopAfter)}`);
  if (o.skip.length) out.push(`skips ${o.skip.length} step${o.skip.length === 1 ? "" : "s"}`);
  if (o.model?.trim()) out.push("model " + o.model.trim());
  return out.length === 1 ? out[0] + " · no limits" : out.join(" · ");
}

function Options() {
  const r = frs(),
    o = r.opts,
    f = r.flow,
    set = (k, v) => {
      o[k] = v;
      update();
    };
  const steps = f.nodes.filter((n) => n.type !== "start");
  return (
    <details
      className="card fr-opts"
      open={r.showOpts}
      onToggle={(e) => {
        r.showOpts = e.target.open;
      }}
    >
      <summary>
        <b>Options</b> <span className="muted">· {optionsSummary(o, f)}</span>
      </summary>
      <div className="fr-grid">
        <label className="field">
          <span className="muted">Pause for review</span>
          <select value={o.review} onChange={(e) => set("review", e.target.value)}>
            <option value="ai">After AI steps and marked steps</option>
            <option value="each">After every step</option>
            <option value="marked">Only steps marked for review</option>
            <option value="none">Never</option>
          </select>
        </label>
        <label className="field">
          <span className="muted">Run the whole flow</span>
          <div className="fr-inline">
            <input
              type="number"
              min="1"
              max="50"
              value={o.repeat}
              onChange={(e) => set("repeat", e.target.value)}
              style={{ width: 70 }}
            />{" "}
            time(s)
          </div>
        </label>
        <label className="field">
          <span className="muted">Run once per item of</span>
          <select
            value={o.forEach.input}
            onChange={(e) => {
              o.forEach.input = e.target.value;
              update();
            }}
          >
            <option value="">— no, run once —</option>
            {f.inputs
              .filter((i) => i.type !== "secret")
              .map((i) => (
                <option key={i.name} value={i.name}>
                  {i.label || i.name}
                </option>
              ))}
          </select>
        </label>
        {o.forEach.input && (
          <>
            <label className="field">
              <span className="muted">Items are</span>
              <select
                value={o.forEach.split}
                onChange={(e) => {
                  o.forEach.split = e.target.value;
                  update();
                }}
              >
                <option value="lines">One per line</option>
                <option value="json">A JSON array</option>
                <option value="csv">CSV rows</option>
              </select>
            </label>
            <label className="field">
              <span className="muted">Only items matching (pattern)</span>
              <input
                value={o.forEach.filter}
                placeholder="e.g. ^ORD- or error|fail"
                style={{ fontFamily: "var(--font-mono)" }}
                onChange={(e) => {
                  o.forEach.filter = e.target.value;
                  update();
                }}
              />
            </label>
            <label className="field">
              <span className="muted">At most</span>
              <input
                type="number"
                min="1"
                max="1000"
                value={o.forEach.max}
                onChange={(e) => {
                  o.forEach.max = e.target.value;
                  update();
                }}
              />
            </label>
          </>
        )}
        <label className="field">
          <span className="muted">Start</span>
          <DateTimeField value={o.startAt} label="Start" onChange={(v) => set("startAt", v)} />
          <small className="muted">{o.startAt ? "Waits until then (keep the app open)" : "Empty: now"}</small>
        </label>
        <label className="field">
          <span className="muted">Stop the run after (minutes)</span>
          <input
            type="number"
            min="0"
            value={o.timeoutMinutes}
            onChange={(e) => set("timeoutMinutes", e.target.value)}
          />
          <small className="muted">0: no limit</small>
        </label>
        <label className="field">
          <span className="muted">Each step may take (seconds)</span>
          <input
            type="number"
            min="0"
            value={o.stepTimeoutSeconds}
            onChange={(e) => set("stepTimeoutSeconds", e.target.value)}
          />
          <small className="muted">0: 2 min for tools, 10 min for AI</small>
        </label>
        <label className="field">
          <span className="muted">Stop after step</span>
          <select value={o.stopAfter} onChange={(e) => set("stopAfter", e.target.value)}>
            <option value="">— run to the end —</option>
            {steps.map((n) => (
              <option key={n.id} value={n.id}>
                {n.title || n.id}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div className="fr-skip">
        <span className="muted">Skip steps:</span>
        {steps.map((n) => (
          <label key={n.id} className="check">
            <input
              type="checkbox"
              checked={o.skip.includes(n.id)}
              onChange={(e) => {
                o.skip = e.target.checked ? [...o.skip, n.id] : o.skip.filter((x) => x !== n.id);
                update();
              }}
            />{" "}
            {n.title || n.id}
          </label>
        ))}
      </div>
    </details>
  );
}

export function Setup() {
  const r = frs(),
    f = r.flow;
  return (
    <>
      <header className="view-head">
        <select
          className="fr-switch"
          aria-label="Flow"
          value={f.id}
          onChange={(e) => loadFlow(e.target.value)}
        >
          {(r.flows || []).map((x) => (
            <option key={x.id} value={x.id}>
              {x.name}
            </option>
          ))}
        </select>
        <h1>{f.name}</h1>
        <div className="head-controls head-model">
          <button className="link" onClick={() => openFlowInDesigner(f.id)}>
            Edit in the designer
          </button>
          <ModelPicker
            value={r.opts.model || ""}
            onChange={(v) => {
              r.opts.model = v;
              update();
            }}
            label="Model for AI steps"
          />
        </div>
      </header>
      {f.description && <p className="wf-desc">{f.description}</p>}
      <div className="fl-tagrow">
        <span className="muted">Tags</span>
        <TagEditor
          tags={f.tags || []}
          suggestions={allTags(r.flows)}
          onChange={async (t) => {
            const before = f.tags;
            f.tags = t;
            update();
            try {
              f.tags = await backend.SetFlowTags(f.id, t);
              const x = (r.flows || []).find((y) => y.id === f.id);
              if (x) x.tags = f.tags;
            } catch (e) {
              f.tags = before;
              toast(errMsg(e), "error");
            }
            update();
          }}
        />
      </div>
      <div className="card">
        <div className="card-head">
          <Icon.inputs />
          <h3>Inputs</h3>
          <span className="badge">{f.inputs.length}</span>
        </div>
        {f.inputs.length ? (
          <div className="fr-inputs">
            {f.inputs.map((i) => (
              <InputField key={i.name} i={i} />
            ))}
          </div>
        ) : (
          <p className="muted">This flow asks for nothing.</p>
        )}
      </div>
      <Credentials />
      <Commands />
      <Options />
      <div className="row-actions fr-go">
        <button
          className="btn primary fr-run"
          disabled={(r.cmds || []).some((c) => !c.approved)}
          aria-describedby={(r.cmds || []).some((c) => !c.approved) ? "fr-why" : undefined}
          onClick={start}
        >
          <Icon.workflow /> {r.opts.startAt ? "Schedule run" : "Run flow"}
        </button>
        {(r.cmds || []).some((c) => !c.approved) && (
          <span id="fr-why" className="muted run-why">
            Needed first: approve the commands above.
          </span>
        )}
        <span className="muted">
          {(() => {
            const n = f.nodes.filter((x) => !["start", "output"].includes(x.type)).length,
              o = f.nodes.filter((x) => x.type === "output").length;
            return `${n} step${n === 1 ? "" : "s"} · ${o} output${o === 1 ? "" : "s"}`;
          })()}{" "}
          · saves about {estimate(f)} min each run
        </span>
      </div>
    </>
  );
}

const estimate = (f) =>
  f.minutesSaved ||
  f.nodes.reduce(
    (m, n) => m + ({ tool: 10, ai: 20, merge: 5, transform: 5, loop: 5, gather: 5, output: 5 }[n.type] || 0),
    0,
  );
