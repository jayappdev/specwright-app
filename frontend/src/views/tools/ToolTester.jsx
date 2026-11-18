// Tools › Test › Run with arguments: a form built from the tool's
// parameters (the right field for each type and format, its secrets masked),
// a direct run with the result checked, saved test cases, and the tool's
// parameters refined without AI (Infer) or with it (Refine with AI).
import { DateTimeField } from "../../DateTimeField.jsx";
import { useEffect, useState } from "react";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg, fmtMs, refreshExtensions } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { ContentView } from "../../ContentView.jsx";
import { ModelPicker } from "../../ModelPicker.jsx";
import { AiProgress } from "../../AiProgress.jsx";

export const FORMAT_LABEL = {
  path: "file path",
  file: "file",
  directory: "folder",
  url: "URL",
  date: "date",
  "date-time": "date and time",
  multiline: "long text",
  email: "email",
  password: "password",
};

const blank = (v) => v === undefined || v === null || v === "";

// the tester's state for the tool being tested
function tt(tb, d) {
  if (!tb.tt || tb.tt.tool !== d.name) {
    const vals = {};
    for (const p of d.params || []) vals[p.name] = p.type === "boolean" ? p.default || "" : p.default || "";
    tb.tt = { tool: d.name, vals, secrets: {}, run: null, running: false, results: null, meta: null };
  }
  return tb.tt;
}

// typeLabel: what a parameter is, in words
export function typeLabel(p) {
  if (p.enum?.length) return "one of " + p.enum.length;
  if (p.format) return FORMAT_LABEL[p.format] || p.format;
  return (
    {
      integer: "whole number",
      number: "number",
      boolean: "yes / no",
      json: "JSON",
      object: "JSON object",
      array: "JSON list",
    }[p.type] || "text"
  );
}

// ArgField is the right field for one parameter.
function ArgField({ p, v, set }) {
  const ph = p.example || (p.default ? `default: ${p.default}` : "");
  if (p.enum?.length)
    return (
      <select value={v} aria-label={p.name} onChange={(e) => set(e.target.value)}>
        <option value="">{p.required ? "Choose…" : p.default ? `(default: ${p.default})` : "(none)"}</option>
        {p.enum.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    );
  if (p.type === "boolean")
    return (
      <select value={v} aria-label={p.name} onChange={(e) => set(e.target.value)}>
        <option value="">{p.default ? `(default: ${p.default})` : "(not set)"}</option>
        <option value="true">Yes (true)</option>
        <option value="false">No (false)</option>
      </select>
    );
  if (p.type === "integer" || p.type === "number")
    return (
      <input
        type="number"
        step={p.type === "integer" ? 1 : "any"}
        value={v}
        placeholder={ph}
        aria-label={p.name}
        onChange={(e) => set(e.target.value)}
      />
    );
  if (["json", "object", "array"].includes(p.type))
    return (
      <textarea
        rows={4}
        className="tt-mono"
        value={v}
        aria-label={p.name}
        placeholder={p.example || (p.type === "array" ? "[ … ]" : "{ … }")}
        onChange={(e) => set(e.target.value)}
      />
    );
  switch (p.format) {
    case "date":
      return <input type="date" value={v} aria-label={p.name} onChange={(e) => set(e.target.value)} />;
    case "date-time":
      return <DateTimeField value={v} label={p.name} onChange={set} />;
    case "multiline":
      return (
        <textarea
          rows={4}
          value={v}
          placeholder={ph}
          aria-label={p.name}
          onChange={(e) => set(e.target.value)}
        />
      );
    case "password":
      return (
        <input
          type="password"
          value={v}
          aria-label={p.name}
          autoComplete="off"
          onChange={(e) => set(e.target.value)}
        />
      );
    case "url":
      return (
        <input
          type="url"
          value={v}
          placeholder={ph || "https://…"}
          aria-label={p.name}
          onChange={(e) => set(e.target.value)}
        />
      );
    case "email":
      return (
        <input
          type="email"
          value={v}
          placeholder={ph}
          aria-label={p.name}
          onChange={(e) => set(e.target.value)}
        />
      );
    case "path":
    case "file":
    case "directory": {
      const files = (S.files?.list || []).map((f) => f.path || f).filter((x) => typeof x === "string");
      const id = "tt-files-" + p.name;
      return (
        <>
          <input
            className="tt-mono"
            value={v}
            list={id}
            placeholder={ph || (p.format === "directory" ? "e.g. src" : "e.g. src/main.go")}
            aria-label={p.name}
            onChange={(e) => set(e.target.value)}
          />
          <datalist id={id}>
            {files
              .filter((f) => (p.format === "directory" ? f.endsWith("/") : true))
              .slice(0, 300)
              .map((f) => (
                <option key={f} value={f} />
              ))}
          </datalist>
        </>
      );
    }
  }
  return <input value={v} placeholder={ph} aria-label={p.name} onChange={(e) => set(e.target.value)} />;
}

// the arguments to send: blanks left out (the tool's defaults apply)
const argsOf = (params, vals) => {
  const a = {};
  for (const p of params) if (!blank(vals[p.name])) a[p.name] = vals[p.name];
  return a;
};

// persistMeta applies new parameters, output shape or tests to the tool:
// saved at once for a saved tool, kept in the draft otherwise.
export async function persistMeta(tb, d, meta) {
  const m = {
    params: meta.params ?? d.params ?? [],
    output: meta.output !== undefined ? meta.output : d.output,
    tests: meta.tests ?? d.tests ?? [],
  };
  Object.assign(d, m);
  if (tb.editing && !tb.ro && !tb.admin) {
    try {
      const saved = await backend.SaveToolMeta(d.name, m);
      Object.assign(d, {
        params: saved.params,
        output: saved.output,
        tests: saved.tests || [],
        metaAt: saved.metaAt,
      });
      await refreshExtensions();
      toast("Saved. What runs did not change, so the tool keeps its approval.");
    } catch (e) {
      toast(errMsg(e), "error");
    }
  }
  update();
}

export function ToolTester({ tb, d }) {
  const t = tt(tb, d);
  const params = d.params || [];
  const set = (k, v) => {
    t.vals[k] = v;
    update();
  };
  // a Copilot extension tool with no parameters known: read them from its source
  useEffect(() => {
    if (d.kind === "copilot" && !params.length && !t.meta) infer(tb, d);
  }, [d.name]);
  const run = async () => {
    Object.assign(t, { running: true, run: null });
    update();
    try {
      t.run = await backend.RunToolWith(d, argsOf(params, t.vals), t.secrets, null);
      tb.direct = true; // a real run counts as tested: Save is reachable
    } catch (e) {
      t.run = { ok: false, error: errMsg(e), output: "", checks: [] };
    }
    t.running = false;
    update();
  };
  const missingReq = params.filter((p) => p.required && blank(t.vals[p.name]) && blank(p.default));
  return (
    <>
      <Meta tb={tb} d={d} />
      <div className="card">
        <div className="card-head">
          <h3>Run with arguments</h3>
          <span className="muted">
            {params.length} parameter{params.length === 1 ? "" : "s"} · runs the tool itself, no AI
          </span>
        </div>
        {!params.length && (
          <p className="muted">
            This tool declares no parameters. If it takes some, use <b>Infer</b> or <b>Refine with AI</b>{" "}
            above.
          </p>
        )}
        <div className="tt-form">
          {params.map((p) => (
            <label key={p.name} className="field tt-field">
              <span>
                <code className="inline-code" data-nofile>
                  {p.name}
                </code>
                {p.required && <span className="err-text"> *</span>}{" "}
                <span className="badge">{typeLabel(p)}</span>
                {p.desc && <span className="muted"> {p.desc}</span>}
              </span>
              <ArgField p={p} v={t.vals[p.name] ?? ""} set={(v) => set(p.name, v)} />
            </label>
          ))}
        </div>
        {(d.secrets || []).length > 0 && (
          <div className="tt-secrets">
            <h4>Secrets</h4>
            {(d.secrets || []).map((s) => {
              const via = S.secretInfo?.[s.name]?.via;
              return (
                <label key={s.name} className="field tt-field">
                  <span>
                    <code className="inline-code" data-nofile>
                      {s.name}
                    </code>{" "}
                    <span className="muted">
                      {via === "missing"
                        ? "no value found: enter one for this run"
                        : via === "ask"
                          ? "you are asked when it runs, or enter it here"
                          : "found (" +
                            (via || "session or environment") +
                            "); enter one to use another for this run"}
                    </span>
                  </span>
                  <input
                    type="password"
                    autoComplete="off"
                    value={t.secrets[s.name] || ""}
                    aria-label={s.name}
                    onChange={(e) => {
                      t.secrets[s.name] = e.target.value;
                      update();
                    }}
                  />
                </label>
              );
            })}
            <small className="muted">Values typed here are used for this run only and never saved.</small>
          </div>
        )}
        <div className="row-actions">
          <button
            className="btn primary"
            disabled={t.running}
            onClick={run}
            title={missingReq.length ? "Required: " + missingReq.map((p) => p.name).join(", ") : ""}
          >
            <Icon.run /> {t.running ? "Running…" : "Run"}
          </button>
          {missingReq.length > 0 && (
            <span className="muted">Required: {missingReq.map((p) => p.name).join(", ")}</span>
          )}
          <button
            className="link"
            onClick={() => {
              for (const p of params) t.vals[p.name] = p.example || p.default || "";
              update();
            }}
          >
            Fill with examples
          </button>
        </div>
        {t.running && (
          <AiProgress phases={["Checking the arguments", "Running the tool", "Checking the output"]} />
        )}
        {t.run && <RunResult r={t.run} d={d} />}
        {t.run && !t.running && <SaveCase tb={tb} d={d} args={argsOf(params, t.vals)} r={t.run} />}
      </div>
      <Cases tb={tb} d={d} />
    </>
  );
}

function Checks({ checks }) {
  return (
    <ul className="checklist">
      {(checks || []).map((c, i) => (
        <li key={i} className={c.ok ? "ok" : ""}>
          {c.ok ? <Icon.check /> : <Icon.x />}
          <span>
            {c.label}
            {c.note ? <span className="muted"> · {c.note}</span> : null}
          </span>
        </li>
      ))}
    </ul>
  );
}

function RunResult({ r, d }) {
  const pass = (r.checks || []).length > 0 && r.checks.every((c) => c.ok);
  return (
    <div className="tt-result" style={{ borderColor: `var(${pass ? "--ok" : "--error"})` }}>
      <div>
        <b>
          {r.problems?.length ? "Not run: the arguments do not fit" : pass ? "It works" : "It did not pass"}
        </b>
        {r.durationMs ? <span className="muted"> · {fmtMs(r.durationMs)}</span> : null}
      </div>
      <Checks checks={r.checks} />
      {r.command && <pre className="code">$ {r.command}</pre>}
      {r.install && (
        <details>
          <summary className="muted">Installing its packages</summary>
          <pre className="tool-result">{r.install}</pre>
        </details>
      )}
      {r.error && !r.problems?.length && <div className="banner error">{r.error}</div>}
      {(r.output || !r.problems?.length) && (
        <ContentView title={(d.name || "tool") + " output"} text={r.output || ""} />
      )}
    </div>
  );
}

// SaveCase saves the last run as a test case.
function SaveCase({ tb, d, args, r }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [contains, setContains] = useState("");
  const [fields, setFields] = useState("");
  const [fail, setFail] = useState(false);
  useEffect(() => {
    setFail(!r.ok);
    const first = (r.output || "").split("\n").find((l) => l.trim()) || "";
    setContains(r.ok ? first.trim().slice(0, 60) : "");
    setName(`Case ${(d.tests || []).length + 1}`);
  }, [r]);
  if (tb.ro) return null;
  if (!open)
    return (
      <div className="row-actions">
        <button className="btn secondary" onClick={() => setOpen(true)}>
          Save as a test case
        </button>
      </div>
    );
  return (
    <div className="tt-case-form">
      <label className="field">
        <span className="muted">Name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="field">
        <span className="muted">The output contains (one text per line)</span>
        <textarea rows={2} value={contains} onChange={(e) => setContains(e.target.value)} />
      </label>
      <label className="field">
        <span className="muted">JSON fields that must exist (one path per line, e.g. rows[0].id)</span>
        <textarea rows={2} className="tt-mono" value={fields} onChange={(e) => setFields(e.target.value)} />
      </label>
      <label className="check">
        <input type="checkbox" checked={fail} onChange={(e) => setFail(e.target.checked)} /> It must fail (a
        case the tool should refuse)
      </label>
      <div className="row-actions">
        <button
          className="btn primary"
          disabled={!name.trim()}
          onClick={async () => {
            const lines = (s) =>
              s
                .split("\n")
                .map((x) => x.trim())
                .filter(Boolean);
            const test = {
              name: name.trim(),
              args,
              expect: { contains: lines(contains), fields: lines(fields), fail },
            };
            await persistMeta(tb, d, {
              tests: [...(d.tests || []).filter((x) => x.name !== test.name), test],
            });
            setOpen(false);
          }}
        >
          Save the test case
        </button>
        <button className="link" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </div>
  );
}

// Cases lists the saved test cases and runs them.
function Cases({ tb, d }) {
  const t = tt(tb, d);
  const tests = d.tests || [];
  if (!tests.length) return null;
  const res = t.results || [];
  const passed = res.filter((x) => x.pass).length;
  return (
    <div className="card">
      <div className="card-head">
        <h3>Test cases</h3>
        {res.length > 0 && (
          <span className={"badge " + (passed === res.length ? "badge-ok" : "badge-warn")}>
            {passed} of {res.length} passed
          </span>
        )}
      </div>
      <ul className="tt-cases">
        {tests.map((c) => {
          const r = res.find((x) => x.name === c.name);
          return (
            <li key={c.name}>
              <div>
                {r ? r.pass ? <Icon.check /> : <Icon.x /> : <span className="muted">·</span>} <b>{c.name}</b>{" "}
                <code className="inline-code" data-nofile>
                  {JSON.stringify(c.args)}
                </code>
                <span className="muted">
                  {c.expect?.fail ? " · must fail" : ""}
                  {c.expect?.contains?.length
                    ? " · contains " + c.expect.contains.map((x) => `“${x}”`).join(", ")
                    : ""}
                  {c.expect?.fields?.length ? " · fields " + c.expect.fields.join(", ") : ""}
                </span>
                {!tb.ro && (
                  <>
                    {" "}
                    <button
                      className="link"
                      onClick={() => {
                        for (const p of d.params || []) t.vals[p.name] = c.args?.[p.name] ?? "";
                        update();
                      }}
                    >
                      Load
                    </button>{" "}
                    <button
                      className="link danger-text"
                      aria-label={`Delete the test case ${c.name}`}
                      onClick={() => persistMeta(tb, d, { tests: tests.filter((x) => x.name !== c.name) })}
                    >
                      Delete
                    </button>
                  </>
                )}
              </div>
              {r && !r.pass && <Checks checks={r.run.checks.filter((x) => !x.ok)} />}
            </li>
          );
        })}
      </ul>
      <div className="row-actions">
        <button
          className="btn primary"
          disabled={t.casesRunning}
          onClick={async () => {
            t.casesRunning = true;
            update();
            try {
              t.results = await backend.RunToolTests(d, tests, t.secrets);
              tb.direct = true;
            } catch (e) {
              toast(errMsg(e), "error");
            }
            t.casesRunning = false;
            update();
          }}
        >
          {t.casesRunning ? "Running…" : `Run all ${tests.length} test case${tests.length === 1 ? "" : "s"}`}
        </button>
      </div>
    </div>
  );
}

// --- the tool's parameters, refined -------------------------------------------------

async function infer(tb, d) {
  const t = tt(tb, d);
  t.meta = { busy: "infer" };
  update();
  try {
    const r = await backend.InspectTool(d);
    t.meta = { kind: "infer", params: r.params, changes: r.changes, source: r.source, notes: r.notes };
  } catch (e) {
    t.meta = { error: errMsg(e) };
  }
  update();
}

async function refine(tb, d, model) {
  const t = tt(tb, d);
  t.meta = { busy: "ai" };
  update();
  try {
    const r = await backend.RefineToolWithAI(d, model || "");
    t.meta = { kind: "ai", ...r };
  } catch (e) {
    t.meta = { error: errMsg(e) };
  }
  update();
}

function Meta({ tb, d }) {
  const t = tt(tb, d);
  const [model, setModel] = useState("");
  const m = t.meta;
  const params = d.params || [];
  const managed = tb.admin || d.managed || d.scope === "managed";
  return (
    <div className="card">
      <h3 style={{ marginTop: 0 }}>Inputs and output</h3>
      <p className="muted tt-summary">
        {params.length
          ? params.map((p) => `${p.name}${p.required ? "*" : ""}: ${typeLabel(p)}`).join(" · ")
          : "no parameters declared"}
        {d.output?.type ? ` → output: ${d.output.type}` : " → output: text"}
      </p>
      {!tb.ro && !managed && (
        <div className="row-actions">
          <button
            className="btn secondary"
            disabled={!!m?.busy}
            onClick={() => infer(tb, d)}
            title="From the command, the extension's schema, and the names and descriptions; no AI"
          >
            Infer
          </button>
          <button
            className="btn secondary"
            disabled={!!m?.busy}
            onClick={() => refine(tb, d, model)}
            title="The AI reads the tool's source and proposes parameters, its output shape and test cases"
          >
            <Icon.spark /> Refine with AI
          </button>
          <ModelPicker value={model} onChange={setModel} label="Model for refining" />
        </div>
      )}
      {m?.busy && (
        <AiProgress
          phases={
            m.busy === "ai"
              ? ["Reading the tool's source", "Working out its parameters", "Writing test cases"]
              : ["Reading the tool"]
          }
        />
      )}
      {m?.error && <div className="banner error">{m.error}</div>}
      {m && !m.busy && !m.error && (
        <div className="tt-proposal">
          <div className="muted">
            {m.kind === "ai" ? "The AI proposes" : "Found"}
            {m.source ? ` (from ${m.source})` : ""}:
          </div>
          {(m.changes || []).length ? (
            <ul className="ai-changes">
              {m.changes.map((c, i) => (
                <li key={i}>
                  <code className="inline-code" data-nofile>
                    {c.param}
                  </code>{" "}
                  {c.what}
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">Nothing to change: the parameters are as precise as this can tell.</p>
          )}
          {m.why && <p className="muted fl-help">{m.why}</p>}
          {(m.notes || []).length > 0 && <p className="muted fl-help">{m.notes.join(" ")}</p>}
          <table className="tt-params">
            <thead>
              <tr>
                <th>Parameter</th>
                <th>Kind</th>
                <th>Required</th>
                <th>Default / example</th>
              </tr>
            </thead>
            <tbody>
              {(m.params || []).map((p) => (
                <tr key={p.name}>
                  <td>
                    <code className="inline-code" data-nofile>
                      {p.name}
                    </code>
                    {p.desc ? <div className="muted">{p.desc}</div> : null}
                  </td>
                  <td>
                    {typeLabel(p)}
                    {p.enum?.length ? <div className="muted">{p.enum.join(", ")}</div> : null}
                  </td>
                  <td>{p.required ? "yes" : ""}</td>
                  <td className="muted">
                    {[p.default && "default " + p.default, p.example && "e.g. " + p.example]
                      .filter(Boolean)
                      .join(" · ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {m.output && <div className="muted fl-help">Output: {JSON.stringify(m.output)}</div>}
          {(m.tests || []).length > 0 && (
            <div className="muted fl-help">Test cases: {m.tests.map((x) => x.name).join(", ")}</div>
          )}
          <div className="row-actions">
            <button
              className="btn primary"
              disabled={!(m.changes || []).length}
              onClick={async () => {
                const meta = { params: m.params };
                if (m.output) meta.output = m.output;
                if ((m.tests || []).length) {
                  const names = new Set(m.tests.map((x) => x.name));
                  meta.tests = [...(d.tests || []).filter((x) => !names.has(x.name)), ...m.tests];
                }
                await persistMeta(tb, d, meta);
                t.meta = null;
                // new parameters: the form starts from their defaults
                for (const p of d.params || []) if (blank(t.vals[p.name])) t.vals[p.name] = p.default || "";
                update();
              }}
            >
              Use these
            </button>
            <button
              className="link"
              onClick={() => {
                t.meta = null;
                update();
              }}
            >
              Discard
            </button>
            {!tb.editing && <span className="muted">Kept in this draft; saved with the tool.</span>}
          </div>
        </div>
      )}
    </div>
  );
}
