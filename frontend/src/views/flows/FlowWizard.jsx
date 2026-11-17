// The flow wizard: an AI designer you can follow. Five phases —
//   1 Requirements  your request in your own words
//   2 Clarify       the requirements the AI understood, NOT-CLEAR ones
//                   flagged, its questions about them and about the flow's
//                   inputs, outputs, constraints and secrets
//   3 Plan          the plan document (tools in context, requirements,
//                   inputs/outputs, secrets), editable or revised by the AI
//   4 Drafts        temporary flows: tried with sample data, run for real,
//                   revised with feedback, compared
//   5 Apply         the approved draft becomes a flow in the Flow designer
// Sessions are saved in the project (.codegen/flow-wizard) and can be
// resumed. The Activity panel shows every AI call: what was asked, what came
// back, how long it took and what it used.
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg, fmtMs } from "../../store.js";
import { Icon, Pill } from "../../ui.jsx";
import { ModelPicker } from "../../ModelPicker.jsx";
import { AiProgress } from "../../AiProgress.jsx";
import { ContentView } from "../../ContentView.jsx";
import { toolRef } from "../../flowNames.js";
import { fl, loadList, openFlow, save, StepIcon, tools } from "../Flows.jsx";
import { PathChips, TrialReport, openDesigner } from "./AIDesigner.jsx";
import { pill } from "../FlowRun.jsx";

const PHASES = [
  ["requirements", "Requirements"],
  ["clarify", "Clarify"],
  ["plan", "Plan"],
  ["drafts", "Drafts"],
  ["applied", "Apply"],
];
const EXAMPLES = [
  "Every morning, tell me which blocking issues the quality gate found, grouped by file",
  "Read a Jira ticket and write acceptance criteria for it",
  "Check the API spec for problems and summarise what to fix first",
];
const BUSY_PHASES = {
  start: [
    "Reading your request",
    "Looking at the project's tools",
    "Listing the requirements",
    "Finding what is not clear",
  ],
  clarify: ["Reading your answers", "Updating the requirements", "Checking what is still unclear"],
  plan: [
    "Reading the tools and their contracts",
    "Choosing tools",
    "Deciding how secrets are handled",
    "Writing the plan",
  ],
  drafts: [
    "Building flows from the plan",
    "Wiring the steps",
    "Trying each draft with sample data",
    "Fixing what did not work",
  ],
  revise: ["Reading your feedback", "Changing the draft", "Trying it with sample data"],
};
const SECRET_SOURCES = [
  ["keychain", "Keychain"],
  ["env", "Environment variable"],
  ["auto", "Keychain, then environment, then ask"],
  ["ask", "Asked when the run starts"],
  ["prompt", "Asked when the step is reached"],
];
const LIVE = ["running", "review", "scheduled"];

export const wz = () => (S.wiz ||= { open: false, id: "", sess: null, list: null, busy: "", error: "" });

// openWizard opens the flow wizard: a session to resume, or a new one.
export async function openWizard(id = "") {
  const w = wz();
  Object.assign(w, { open: true, error: "", busy: "" });
  if (!id) Object.assign(w, { id: "", sess: null });
  update();
  w.list = (await backend.WizardList().catch(() => [])) || [];
  if (id) await loadSession(id);
  update();
}

async function loadSession(id) {
  const w = wz();
  try {
    w.sess = await backend.WizardLoad(id);
    w.id = id;
    w.view = w.sess.phase;
  } catch (e) {
    toast(errMsg(e), "error");
  }
  update();
}

// call runs one wizard call that may take a while (the AI), showing progress.
async function call(kind, fn) {
  const w = wz();
  Object.assign(w, { busy: kind, error: "" });
  S.wizProgress = null;
  update();
  try {
    const s = await fn();
    if (s?.id) {
      w.sess = s;
      w.id = s.id;
      w.view = s.phase;
    }
    return s;
  } catch (e) {
    w.error = errMsg(e);
    if (w.id) w.sess = await backend.WizardLoad(w.id).catch(() => w.sess); // the log has the failed call
  } finally {
    w.busy = "";
    update();
  }
}

const tokens = (u) => (u?.input_tokens || 0) + (u?.output_tokens || 0);
function usageText(u) {
  if (!u) return "";
  const parts = [];
  if (tokens(u)) parts.push(`${tokens(u).toLocaleString()} tokens`);
  if (u.premium_requests)
    parts.push(`${u.premium_requests} premium request${u.premium_requests === 1 ? "" : "s"}`);
  if (u.cost_usd) parts.push(`$${u.cost_usd.toFixed(3)}`);
  return parts.join(" · ");
}

// --- the dialog ----------------------------------------------------------------------

export function FlowWizard() {
  const w = wz(),
    s = w.sess;
  const close = () => {
    w.open = false;
    update();
  };
  const view = s ? w.view || s.phase : "requirements";
  const reached = (p) => {
    if (!s) return p === "requirements";
    const order = PHASES.map(([k]) => k);
    return order.indexOf(p) <= order.indexOf(s.phase) || (p === "drafts" && s.drafts.length > 0);
  };
  // on the page itself, above the designer's panes (they stack over the canvas)
  return createPortal(
    <div className="modal-scrim fwz-scrim" role="presentation">
      <div
        className="fwz"
        role="dialog"
        aria-modal="true"
        aria-label="Flow wizard"
        onKeyDown={(e) => {
          if (e.key === "Escape" && !w.busy) {
            e.stopPropagation();
            close();
          }
        }}
      >
        <div className="fwz-head">
          <Icon.spark />
          <h3>{s ? s.title : "New flow with AI"}</h3>
          {s && (
            <button
              className="link"
              onClick={() => {
                Object.assign(w, { id: "", sess: null, error: "" });
                update();
              }}
            >
              Sessions
            </button>
          )}
          <button className="link" style={{ marginLeft: "auto" }} aria-label="Close" onClick={close}>
            ✕
          </button>
        </div>
        <ol className="fwz-steps" aria-label="Phases">
          {PHASES.map(([k, l], i) => (
            <li key={k} className={(view === k ? "on " : "") + (reached(k) ? "reached" : "")}>
              <button
                type="button"
                disabled={!reached(k) || !!w.busy}
                aria-current={view === k ? "step" : undefined}
                onClick={() => {
                  w.view = k;
                  update();
                }}
              >
                <span className="fwz-num">{i + 1}</span> {l}
              </button>
            </li>
          ))}
        </ol>
        <div className="fwz-body">
          <div className="fwz-main">
            {w.error && <div className="banner error">{w.error}</div>}
            {w.busy && (
              <div className="fwz-busy">
                <AiProgress phases={BUSY_PHASES[w.busy] || BUSY_PHASES.start} />
                {S.wizProgress?.msg && <span className="muted">{S.wizProgress.msg}</span>}
              </div>
            )}
            {!s && <Start />}
            {s && view === "requirements" && <Requirements s={s} />}
            {s && view === "clarify" && <Clarify s={s} />}
            {s && view === "plan" && <Plan s={s} />}
            {s && view === "drafts" && <Drafts s={s} />}
            {s && view === "applied" && <Applied s={s} />}
          </div>
          {s && <Activity s={s} />}
        </div>
      </div>
    </div>,
    document.body,
  );
}

// --- 1 requirements: a new session, or one to resume ---------------------------------

function Start() {
  const w = wz();
  const [req, setReq] = useState(w.draftReq || "");
  const [model, setModel] = useState("");
  const start = () =>
    call("start", () => {
      w.draftReq = "";
      return backend.WizardStart(req, model);
    });
  return (
    <>
      <p className="wf-desc">
        Describe the flow in your own words. The AI lists the requirements it understood, marks what is not
        clear and asks about it; then it writes a plan, and builds draft flows you can try before anything is
        added to your flows.
      </p>
      <textarea
        rows={4}
        value={req}
        aria-label="What should the flow do?"
        placeholder={"e.g. " + EXAMPLES[0]}
        onChange={(e) => {
          setReq(e.target.value);
          w.draftReq = e.target.value;
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && req.trim()) start();
        }}
      />
      {!req && (
        <div className="chips">
          {EXAMPLES.map((x) => (
            <button key={x} type="button" className="chip" onClick={() => setReq(x)}>
              {x.split(" ").slice(0, 7).join(" ")}…
            </button>
          ))}
        </div>
      )}
      <div className="ai-model">
        <span className="muted">Model</span>
        <ModelPicker value={model} onChange={setModel} label="Model for the flow wizard" />
        <small className="muted">Default: the app's model.</small>
      </div>
      <div className="row-actions">
        <button className="btn primary" disabled={!req.trim() || !!w.busy} onClick={start}>
          <Icon.spark /> Work out the requirements
        </button>
        <button
          className="link"
          title="The one-step AI designer: flow options straight from your words, no questions"
          onClick={() => {
            w.open = false;
            openDesigner("new");
            if (req.trim()) fl().ai.prompt = req;
            update();
          }}
        >
          Quick design, no questions
        </button>
      </div>
      {w.list?.length > 0 && (
        <>
          <h4>Continue a session</h4>
          <ul className="fwz-list">
            {w.list.map((x) => (
              <li key={x.id}>
                <button className="link" onClick={() => loadSession(x.id)}>
                  {x.title}
                </button>{" "}
                <span className="badge">{PHASES.find(([k]) => k === x.phase)?.[1] || x.phase}</span>
                {x.unclear > 0 && <span className="badge badge-warn">{x.unclear} not clear</span>}
                {x.drafts > 0 && (
                  <span className="muted">
                    {" "}
                    · {x.drafts} draft{x.drafts === 1 ? "" : "s"}
                  </span>
                )}
                <span className="muted"> · {new Date(x.updated).toLocaleString()}</span>
                <button
                  className="link danger-text fwz-del"
                  aria-label={`Delete the session ${x.title}`}
                  onClick={async () => {
                    if (!confirm(`Delete the session “${x.title}”? Flows already applied stay.`)) return;
                    await backend.WizardDelete(x.id).catch((e) => toast(errMsg(e), "error"));
                    w.list = (await backend.WizardList().catch(() => [])) || [];
                    update();
                  }}
                >
                  Delete
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

function Requirements({ s }) {
  const w = wz();
  const [req, setReq] = useState(s.request);
  return (
    <>
      <p className="wf-desc">
        What you asked for. Change it and work the requirements out again if you like.
      </p>
      <textarea rows={4} value={req} aria-label="Your request" onChange={(e) => setReq(e.target.value)} />
      <div className="row-actions">
        <button
          className="btn primary"
          disabled={!req.trim() || !!w.busy}
          onClick={() =>
            call("start", () =>
              backend.WizardClarify(s.id, { request: req, requirements: [] }).then((x) => x),
            )
          }
        >
          Work the requirements out again
        </button>
        <button
          className="btn secondary"
          onClick={() => {
            w.view = "clarify";
            update();
          }}
        >
          Back to the requirements
        </button>
      </div>
    </>
  );
}

// --- 2 clarify: requirements, NOT-CLEAR ones, questions, the flow's spec ---------------

function Clarify({ s }) {
  const w = wz();
  const [answers, setAnswers] = useState({});
  const [reqs, setReqs] = useState(null); // edited requirements
  useEffect(() => {
    setAnswers(Object.fromEntries(s.questions.map((q) => [q.id, q.answer || ""])));
    setReqs(null);
  }, [s.updated]);
  const list = reqs || s.requirements;
  const open = s.questions.filter((q) => !q.answer);
  const answered = s.questions.filter((q) => q.answer);
  const changed = reqs !== null || s.questions.some((q) => (answers[q.id] || "") !== (q.answer || ""));
  const edit = () => ({ answers, ...(reqs ? { requirements: reqs } : {}) });
  const unclear = list.filter((r) => r.status === "not-clear").length;
  const setReq = (i, patch) => {
    const next = list.map((r, k) => (k === i ? { ...r, ...patch } : r));
    setReqs(next);
  };
  return (
    <>
      <h4>
        Requirements{" "}
        <span className="muted" style={{ fontWeight: 400 }}>
          · {list.length - unclear} clear{unclear ? `, ${unclear} not clear` : ""}
        </span>
      </h4>
      <ul className="fwz-reqs">
        {list.map((r, i) => (
          <li key={r.id} className={r.status === "not-clear" ? "unclear" : ""}>
            <span className={"badge " + (r.status === "not-clear" ? "badge-warn" : "badge-ok")}>
              {r.status === "not-clear" ? "NOT-CLEAR" : "CLEAR"}
            </span>{" "}
            <code className="inline-code" data-nofile>
              {r.id}
            </code>{" "}
            <input
              className="fwz-req-text"
              value={r.text}
              aria-label={`Requirement ${r.id}`}
              onChange={(e) => setReq(i, { text: e.target.value })}
            />
            <button
              className="link"
              title={r.status === "not-clear" ? "Mark it clear" : "Mark it not clear"}
              onClick={() =>
                setReq(
                  i,
                  r.status === "not-clear" ? { status: "clear", unclear: "" } : { status: "not-clear" },
                )
              }
            >
              {r.status === "not-clear" ? "Mark clear" : "Not clear"}
            </button>
            <button
              className="link"
              aria-label={`Remove ${r.id}`}
              onClick={() => setReqs(list.filter((_, k) => k !== i))}
            >
              ✕
            </button>
            {r.status === "not-clear" && r.unclear && (
              <div className="muted fwz-unclear">Not clear: {r.unclear}</div>
            )}
          </li>
        ))}
      </ul>
      <button
        className="link"
        onClick={() => setReqs([...list, { id: "R" + (list.length + 1), text: "", status: "clear" }])}
      >
        + Add a requirement
      </button>

      {open.length > 0 && (
        <>
          <h4>Questions</h4>
          <div className="fwz-questions">
            {open.map((q) => (
              <Question
                key={q.id}
                q={q}
                v={answers[q.id] || ""}
                set={(v) => setAnswers({ ...answers, [q.id]: v })}
              />
            ))}
          </div>
        </>
      )}
      {answered.length > 0 && (
        <details>
          <summary className="muted">Answered ({answered.length})</summary>
          <div className="fwz-questions">
            {answered.map((q) => (
              <Question
                key={q.id}
                q={q}
                v={answers[q.id] || ""}
                set={(v) => setAnswers({ ...answers, [q.id]: v })}
              />
            ))}
          </div>
        </details>
      )}
      <Spec s={s} />
      <div className="row-actions fwz-actions">
        <button
          className="btn primary"
          disabled={!!w.busy}
          onClick={() => call("clarify", () => backend.WizardClarify(s.id, edit()))}
          title="The AI updates the requirements with your answers, and asks only about what is still unclear"
        >
          <Icon.spark /> {open.length ? "Send the answers" : "Check the requirements again"}
        </button>
        <button
          className={"btn " + (unclear || open.length ? "secondary" : "primary")}
          disabled={!!w.busy}
          title={
            unclear || open.length
              ? "The AI makes reasonable assumptions for what is unclear and says so in the plan"
              : "Write the plan"
          }
          onClick={() =>
            call("plan", async () => {
              if (changed) await backend.WizardUpdate(s.id, edit());
              return backend.WizardPlan(s.id, "");
            })
          }
        >
          Continue to the plan{unclear || open.length ? " anyway" : ""} →
        </button>
        <button
          className="link"
          disabled={!!w.busy}
          title="Build draft flows from the requirements now, without a plan"
          onClick={() =>
            call("drafts", async () => {
              if (changed) await backend.WizardUpdate(s.id, edit());
              return backend.WizardDraft(s.id, 3, "");
            })
          }
        >
          Skip to drafts
        </button>
        {changed && (
          <button className="link" onClick={() => call("", () => backend.WizardUpdate(s.id, edit()))}>
            Save without asking the AI
          </button>
        )}
      </div>
    </>
  );
}

function Question({ q, v, set }) {
  const opts = q.options || [];
  const picked = new Set(v.split("\n").filter(Boolean));
  let field;
  if ((q.kind === "choice" || q.kind === "yesno") && opts.length) {
    const other = v && !opts.includes(v);
    field = (
      <div className="chips" role="radiogroup" aria-label={q.text}>
        {opts.map((o) => (
          <button
            key={o}
            type="button"
            role="radio"
            aria-checked={v === o}
            className={"chip" + (v === o ? " on" : "")}
            onClick={() => set(v === o ? "" : o)}
          >
            {o}
          </button>
        ))}
        {q.kind === "choice" && (
          <input
            className="fwz-other"
            value={other ? v : ""}
            placeholder="Something else…"
            aria-label={`Another answer to: ${q.text}`}
            onChange={(e) => set(e.target.value)}
          />
        )}
      </div>
    );
  } else if (q.kind === "multichoice" && opts.length) {
    field = (
      <div className="mc-list" role="group" aria-label={q.text}>
        {opts.map((o) => (
          <label key={o} className="check">
            <input
              type="checkbox"
              checked={picked.has(o)}
              onChange={(e) => {
                const next = new Set(picked);
                e.target.checked ? next.add(o) : next.delete(o);
                set(opts.filter((x) => next.has(x)).join("\n"));
              }}
            />{" "}
            {o}
          </label>
        ))}
      </div>
    );
  } else {
    field = <textarea rows={2} value={v} aria-label={q.text} onChange={(e) => set(e.target.value)} />;
  }
  return (
    <div className="fwz-q">
      <div>
        <b>{q.text}</b>{" "}
        {q.req && (
          <code className="inline-code" data-nofile>
            {q.req}
          </code>
        )}{" "}
        {q.topic && q.topic !== "requirement" && <span className="badge">{q.topic}</span>}
      </div>
      {field}
    </div>
  );
}

// the flow's inputs, outputs, constraints and secrets as understood so far
function Spec({ s, editSecrets }) {
  const sp = s.spec;
  const [secrets, setSecrets] = useState(null);
  useEffect(() => setSecrets(null), [s.updated]);
  const list = secrets || sp.secrets;
  return (
    <div className="fwz-spec">
      <div>
        <h4>Inputs</h4>
        {sp.inputs.length ? (
          <ul>
            {sp.inputs.map((i) => (
              <li key={i.name}>
                <code className="inline-code" data-nofile>
                  {i.name}
                </code>{" "}
                <span className="muted">
                  {i.type}
                  {i.required ? ", required" : ""}
                  {i.description ? " · " + i.description : ""}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">None yet</p>
        )}
      </div>
      <div>
        <h4>Outputs</h4>
        {sp.outputs.length ? (
          <ul>
            {sp.outputs.map((o) => (
              <li key={o.name}>
                <b>{o.name}</b>{" "}
                <span className="muted">
                  {o.type}
                  {o.destination ? " → " + o.destination : ""}
                  {o.description ? " · " + o.description : ""}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">None yet</p>
        )}
      </div>
      <div>
        <h4>Constraints</h4>
        {sp.constraints.length ? (
          <ul>
            {sp.constraints.map((c, i) => (
              <li key={i}>{c}</li>
            ))}
          </ul>
        ) : (
          <p className="muted">None yet</p>
        )}
      </div>
      <div>
        <h4>Secrets</h4>
        {list.length ? (
          <ul>
            {list.map((x, i) => (
              <li key={x.name}>
                <code className="inline-code" data-nofile>
                  {x.name}
                </code>{" "}
                <span className="muted">{x.usedBy ? "for " + x.usedBy : ""}</span>
                {editSecrets ? (
                  <select
                    value={x.source || "auto"}
                    aria-label={`Where ${x.name} comes from`}
                    onChange={(e) =>
                      setSecrets(list.map((y, k) => (k === i ? { ...y, source: e.target.value } : y)))
                    }
                  >
                    {SECRET_SOURCES.map(([v, l]) => (
                      <option key={v} value={v}>
                        {l}
                      </option>
                    ))}
                  </select>
                ) : (
                  x.source && (
                    <span className="badge">
                      {SECRET_SOURCES.find(([v]) => v === x.source)?.[1] || x.source}
                    </span>
                  )
                )}
                {x.why && <div className="muted">{x.why}</div>}
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">None</p>
        )}
        {secrets && (
          <button
            className="btn secondary"
            onClick={() => call("", () => backend.WizardUpdate(s.id, { spec: { ...sp, secrets } }))}
          >
            Save where the secrets come from
          </button>
        )}
        <p className="muted fl-help">
          Secrets are flow inputs of type secret: given to tools only, never to AI steps, and never saved in
          the flow file.
        </p>
      </div>
    </div>
  );
}

// --- 3 plan --------------------------------------------------------------------------

function Plan({ s }) {
  const w = wz();
  const [edit, setEdit] = useState(false);
  const [text, setText] = useState(s.plan || "");
  const [fb, setFb] = useState("");
  const [n, setN] = useState(3);
  useEffect(() => setText(s.plan || ""), [s.updated]);
  if (!s.plan)
    return (
      <>
        <p className="wf-desc">No plan yet.</p>
        <button
          className="btn primary"
          disabled={!!w.busy}
          onClick={() => call("plan", () => backend.WizardPlan(s.id, ""))}
        >
          <Icon.spark /> Write the plan
        </button>
      </>
    );
  return (
    <>
      {s.notes?.length > 0 && <div className="banner">{s.notes.join(" ")}</div>}
      {s.planInfo?.tools?.length > 0 && (
        <div className="fwz-tools">
          <span className="muted">Tools in context:</span>{" "}
          {s.planInfo.tools.map((t) => (
            <span key={t.name} className="chip" title={t.why}>
              <StepIcon n={{ type: "tool", tool: t.name }} tools={tools()} size={16} /> {toolRef(t.name)}
            </span>
          ))}
        </div>
      )}
      <div className="row-actions">
        <button className="link" onClick={() => setEdit(!edit)}>
          {edit ? "Show the plan" : "Edit the plan"}
        </button>
      </div>
      {edit ? (
        <>
          <textarea
            rows={18}
            className="fwz-plan-edit"
            value={text}
            aria-label="The plan"
            onChange={(e) => setText(e.target.value)}
          />
          <div className="row-actions">
            <button
              className="btn primary"
              disabled={text === s.plan}
              onClick={async () => {
                await call("", () => backend.WizardUpdate(s.id, { plan: text }));
                setEdit(false);
              }}
            >
              Save the plan
            </button>
          </div>
        </>
      ) : (
        <ContentView title="Plan" text={s.plan} format="md" />
      )}
      <Spec s={s} editSecrets />
      <div className="fwz-iter">
        <textarea
          rows={2}
          value={fb}
          placeholder="Ask for changes to the plan, e.g. read the token from an environment variable in CI; send the report to the team channel"
          aria-label="Changes to the plan"
          onChange={(e) => setFb(e.target.value)}
        />
        <div className="row-actions fwz-actions">
          <button
            className="btn secondary"
            disabled={!fb.trim() || !!w.busy}
            onClick={() => call("plan", () => backend.WizardPlan(s.id, fb)).then(() => setFb(""))}
          >
            <Icon.spark /> Revise the plan
          </button>
          <label className="muted">
            Drafts{" "}
            <select value={n} onChange={(e) => setN(+e.target.value)} aria-label="How many drafts">
              {[1, 2, 3, 4].map((k) => (
                <option key={k}>{k}</option>
              ))}
            </select>
          </label>
          <button
            className="btn primary"
            disabled={!!w.busy}
            onClick={() => call("drafts", () => backend.WizardDraft(s.id, n, ""))}
          >
            Build draft flows from the plan →
          </button>
        </div>
      </div>
    </>
  );
}

// --- 4 drafts: temporary flows --------------------------------------------------------

function Drafts({ s }) {
  const w = wz();
  const [fb, setFb] = useState("");
  const [showGone, setShowGone] = useState(false);
  const live = s.drafts.filter((d) => !d.discarded);
  const gone = s.drafts.length - live.length;
  return (
    <>
      <p className="wf-desc">
        Temporary flows: not in your flows until you apply one. Each was checked and tried with sample data
        (tools and AI mocked, every other step run for real). Run one for real with your inputs, ask for a
        revision, then apply the one you want.
      </p>
      {!live.length && <p className="muted">No drafts{gone ? " (all discarded)" : ""}.</p>}
      <div className="fwz-drafts">
        {lineage(showGone ? s.drafts : live).map((d) => (
          <DraftCard key={d.id} s={s} d={d} />
        ))}
      </div>
      {gone > 0 && (
        <button className="link" onClick={() => setShowGone(!showGone)}>
          {showGone ? "Hide" : "Show"} discarded drafts ({gone})
        </button>
      )}
      <div className="fwz-iter">
        <textarea
          rows={2}
          value={fb}
          placeholder="More drafts — anything to do differently? e.g. without AI steps; save the report as HTML"
          aria-label="What the new drafts should do differently"
          onChange={(e) => setFb(e.target.value)}
        />
        <div className="row-actions">
          <button
            className="btn secondary"
            disabled={!!w.busy}
            onClick={() => call("drafts", () => backend.WizardDraft(s.id, 2, fb)).then(() => setFb(""))}
          >
            <Icon.spark /> More drafts
          </button>
        </div>
      </div>
    </>
  );
}

// lineage orders drafts as they were made, each with its revisions just
// before it (newest version first), so a draft and its versions stay together
function lineage(drafts) {
  const by = Object.fromEntries(drafts.map((d) => [d.id, d]));
  const root = (d) => {
    let r = d;
    for (let k = 0; r.parent && by[r.parent] && k < 50; k++) r = by[r.parent];
    return r.id;
  };
  const groups = {};
  const order = [];
  for (const d of drafts) {
    const r = root(d);
    if (!groups[r]) {
      groups[r] = [];
      order.push(r);
    }
    groups[r].push(d);
  }
  return order.flatMap((r) => groups[r].slice().sort((a, b) => b.version - a.version));
}

function DraftCard({ s, d }) {
  const w = wz();
  const [mode, setMode] = useState(""); // run | revise | apply
  const [fb, setFb] = useState("");
  const [name, setName] = useState(d.flow.name || d.title);
  const errs = (d.issues || []).filter((i) => i.level === "error");
  const parent = d.parent && s.drafts.find((x) => x.id === d.parent);
  return (
    <div
      className={"ai-option fwz-draft" + (d.discarded ? " discarded" : "") + (d.applied ? " applied" : "")}
    >
      <div className="ai-option-head">
        <b>
          {d.title} <span className="muted">v{d.version}</span>
        </b>
        {d.recommended && d.version === 1 && <span className="badge badge-ok">Recommended</span>}
        <span className={"badge " + (d.aiSteps ? "badge-warn" : "badge-info")}>
          {d.aiSteps ? `AI in ${d.aiSteps} step${d.aiSteps === 1 ? "" : "s"}` : "No AI"}
        </span>
        {d.trial && (
          <span className={"badge " + (d.trial.ok ? "badge-ok" : "badge-warn")}>
            {d.trial.ok ? "✓ Tried" : "✗ Trial failed"}
          </span>
        )}
        {errs.length > 0 && (
          <span className="badge badge-warn" title={errs.map((i) => i.message).join("\n")}>
            {errs.length} problem{errs.length === 1 ? "" : "s"}
          </span>
        )}
        {d.applied && <span className="badge badge-ok">Applied</span>}
      </div>
      {parent && (
        <p className="muted fl-help">
          Revision of {parent.title} v{parent.version}
          {d.feedback ? ` — you asked: “${d.feedback}”` : ""}
        </p>
      )}
      <p>{d.summary}</p>
      {d.why && <p className="muted fl-help">{d.why}</p>}
      <PathChips flow={d.flow} />
      {d.tools?.length > 0 && (
        <div className="muted fl-help">
          Uses:{" "}
          {d.tools.map((t) => (
            <code key={t} className="inline-code" data-nofile>
              {toolRef(t)}{" "}
            </code>
          ))}
        </div>
      )}
      {d.changes?.length > 0 && d.parent && (
        <ul className="ai-changes">
          {d.changes.map((c, i) => (
            <li key={i}>{c}</li>
          ))}
        </ul>
      )}
      {d.flow.inputs?.some((i) => i.type === "secret") && (
        <div className="muted fl-help">
          Secrets:{" "}
          {d.flow.inputs
            .filter((i) => i.type === "secret")
            .map(
              (i) => `${i.label || i.name} (${SECRET_SOURCES.find(([v]) => v === (i.source || "ask"))?.[1]})`,
            )
            .join(", ")}
        </div>
      )}
      {errs.length > 0 && <div className="banner error">{errs.map((i) => i.message).join(" ")}</div>}
      {d.trial && <TrialReport t={d.trial} />}
      {(d.runs || []).length > 0 && <RunStatus id={d.runs[d.runs.length - 1]} count={d.runs.length} />}
      {!d.discarded && !d.applied && (
        <div className="row-actions">
          <button
            className="btn secondary"
            disabled={!!w.busy || errs.length > 0}
            onClick={() => setMode(mode === "run" ? "" : "run")}
          >
            ▶ Run for real
          </button>
          <button
            className="btn secondary"
            disabled={!!w.busy}
            onClick={() => setMode(mode === "revise" ? "" : "revise")}
          >
            <Icon.spark /> Revise
          </button>
          <button
            className="btn primary"
            disabled={!!w.busy}
            onClick={() => setMode(mode === "apply" ? "" : "apply")}
          >
            Apply…
          </button>
          <button className="link" onClick={() => call("", () => backend.WizardDiscard(s.id, d.id, true))}>
            Discard
          </button>
        </div>
      )}
      {d.discarded && (
        <button className="link" onClick={() => call("", () => backend.WizardDiscard(s.id, d.id, false))}>
          Restore
        </button>
      )}
      {mode === "run" && <RunForm s={s} d={d} done={() => setMode("")} />}
      {mode === "revise" && (
        <div className="fwz-iter">
          <textarea
            rows={2}
            value={fb}
            placeholder="What should change? e.g. it found nothing: the filter is too strict; also save the report"
            aria-label={`What to change in ${d.title}`}
            onChange={(e) => setFb(e.target.value)}
          />
          <small className="muted">
            The AI also gets how its last real run went. The revision is a new draft; this one stays.
          </small>
          <div className="row-actions">
            <button
              className="btn primary"
              disabled={!!w.busy}
              onClick={() =>
                call("revise", () => backend.WizardRevise(s.id, d.id, fb)).then(() => setMode(""))
              }
            >
              <Icon.spark /> Revise this draft
            </button>
          </div>
        </div>
      )}
      {mode === "apply" && (
        <div className="fwz-iter">
          <label className="field">
            <span className="muted">Name of the flow</span>
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <small className="muted">
            It is added to your flows and opens in the Flow designer; the clarified requirements become its
            custom context. The wizard session stays, so you can come back and apply another draft.
          </small>
          <div className="row-actions">
            <button
              className="btn primary"
              disabled={!name.trim()}
              onClick={async () => {
                try {
                  const st = await backend.WizardApply(s.id, d.id, name);
                  toast(`Added the flow ${st.flow.name}. Check the steps, then Run it.`);
                  wz().open = false;
                  if (fl().dirty) await save();
                  await loadList();
                  await openFlow(st.flow.id);
                } catch (e) {
                  toast(errMsg(e), "error");
                }
              }}
            >
              Apply as a new flow
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// RunForm asks for a draft's inputs and runs it for real.
function RunForm({ s, d, done }) {
  const ins = d.flow.inputs || [];
  const [vals, setVals] = useState(() =>
    Object.fromEntries(ins.map((i) => [i.name, d.test?.inputs?.[i.name] ?? i.default ?? ""])),
  );
  const [busy, setBusy] = useState(false);
  return (
    <div className="fwz-iter">
      <small className="muted">
        Runs the draft with your project's tools and the AI (no reviews; you see the steps below). Nothing is
        saved as a flow.
      </small>
      {ins.map((i) => (
        <label key={i.name} className="field">
          <span className="muted">
            {i.label || i.name}
            {i.required ? " *" : ""} <span className="badge">{i.type}</span>
          </span>
          {i.type === "secret" && (i.source || "ask") !== "ask" ? (
            <small className="muted">
              From {SECRET_SOURCES.find(([v]) => v === i.source)?.[1] || i.source}
              {i.env ? ` ($${i.env})` : ""}
            </small>
          ) : i.type === "multiline" ? (
            <textarea
              rows={3}
              value={vals[i.name]}
              onChange={(e) => setVals({ ...vals, [i.name]: e.target.value })}
            />
          ) : (
            <input
              type={
                i.type === "secret"
                  ? "password"
                  : i.type === "number"
                    ? "number"
                    : i.type === "date"
                      ? "date"
                      : i.type === "datetime"
                        ? "datetime-local"
                        : "text"
              }
              value={vals[i.name]}
              onChange={(e) => setVals({ ...vals, [i.name]: e.target.value })}
            />
          )}
        </label>
      ))}
      <div className="row-actions">
        <button
          className="btn primary"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await backend.WizardRunDraft(s.id, d.id, { inputs: vals });
              wz().sess = await backend.WizardLoad(s.id);
              done();
            } catch (e) {
              toast(errMsg(e), "error");
            }
            setBusy(false);
            update();
          }}
        >
          ▶ Run
        </button>
      </div>
    </div>
  );
}

// RunStatus follows a draft's real run: its steps and outputs.
function RunStatus({ id, count }) {
  const [st, setSt] = useState(S.flowRuns?.[id] || null);
  useEffect(() => {
    let stop = false,
      t;
    const tick = async () => {
      const x = await backend.FlowRun(id).catch(() => null);
      if (stop) return;
      if (x) setSt(x);
      if (!x || LIVE.includes(x.status)) t = setTimeout(tick, 900);
    };
    tick();
    return () => {
      stop = true;
      clearTimeout(t);
    };
  }, [id]);
  if (!st) return <p className="muted">Loading the run…</p>;
  return (
    <details className="fwz-run" open={LIVE.includes(st.status) || st.status === "failed"}>
      <summary>
        Real run{count > 1 ? ` (latest of ${count})` : ""}: <Pill s={pill(st.status)} label={st.status} />{" "}
        {st.usage?.durationMs ? <span className="muted">{fmtMs(st.usage.durationMs)}</span> : null}
      </summary>
      {st.error && <div className="banner error">{st.error}</div>}
      <ol className="ai-trial-steps">
        {st.steps
          .filter((x) => x.type !== "start")
          .map((x) => (
            <li key={x.id} className={x.status}>
              <b>{x.title}</b> <span className="muted">{x.status}</span>
              {x.error && <div className="ai-trial-err">{x.error}</div>}
            </li>
          ))}
      </ol>
      {(st.outputs || []).map((o, i) => (
        <ContentView key={i} title={o.title} text={o.content} format={o.format} compact />
      ))}
    </details>
  );
}

// --- 5 apply --------------------------------------------------------------------------

function Applied({ s }) {
  return (
    <>
      {(s.applied || []).length ? (
        <ul className="fwz-list">
          {s.applied.map((a, i) => {
            const d = s.drafts.find((x) => x.id === a.draft);
            return (
              <li key={i}>
                <b>{d ? `${d.title} v${d.version}` : a.draft}</b> → flow{" "}
                <button
                  className="link"
                  onClick={async () => {
                    wz().open = false;
                    await loadList();
                    await openFlow(a.flow);
                  }}
                >
                  {a.flow}
                </button>{" "}
                <span className="muted">{new Date(a.at).toLocaleString()}</span>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="muted">Nothing applied yet: choose a draft and Apply it.</p>
      )}
      <button
        className="btn secondary"
        onClick={() => {
          wz().view = "drafts";
          update();
        }}
      >
        Back to the drafts
      </button>
    </>
  );
}

// --- what happened behind ----------------------------------------------------------------

function Activity({ s }) {
  const [open, setOpen] = useState({});
  return (
    <aside className="fwz-activity" aria-label="Activity">
      <h4>
        Activity{" "}
        <span className="muted" style={{ fontWeight: 400 }}>
          {usageText(s.usage)}
        </span>
      </h4>
      <ol>
        {s.log
          .slice()
          .reverse()
          .map((e, i) => {
            const k = s.log.length - 1 - i;
            return (
              <li key={k} className={e.error ? "bad" : ""}>
                <div>
                  <span className="badge">{e.phase}</span> {e.summary}
                </div>
                <small className="muted">
                  {new Date(e.at).toLocaleTimeString()}
                  {e.durationMs ? " · " + fmtMs(e.durationMs) : ""}
                  {usageText(e.usage) ? " · " + usageText(e.usage) : ""}
                </small>
                {e.error && <div className="ai-trial-err">{e.error}</div>}
                {(e.prompt || e.answer) && (
                  <button className="link" onClick={() => setOpen({ ...open, [k]: !open[k] })}>
                    {open[k] ? "Hide" : "Show"} what was asked and answered
                  </button>
                )}
                {open[k] && (
                  <>
                    {e.prompt && (
                      <details>
                        <summary className="muted">Sent to the AI</summary>
                        <pre className="code guide-pre">{e.prompt}</pre>
                      </details>
                    )}
                    {e.answer && (
                      <details open>
                        <summary className="muted">Its answer</summary>
                        <pre className="code guide-pre">{e.answer}</pre>
                      </details>
                    )}
                  </>
                )}
              </li>
            );
          })}
      </ol>
    </aside>
  );
}
