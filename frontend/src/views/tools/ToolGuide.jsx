// Tool editor › Test › Guide & examples: a tool's usage guide (what it is
// for, when and how to use it) with worked examples that run here, open as a
// flow in the Flow designer, and show in the Flow step guide.
import { useState } from "react";
import { backend } from "../../backend.js";
import { update, toast, errMsg, refreshExtensions } from "../../store.js";
import { ModelPicker } from "../../ModelPicker.jsx";
import { AiProgress } from "../../AiProgress.jsx";
import { toolGuideSection } from "../flowGuide.js";
import { tryExample, openFlowGuide } from "../FlowGuide.jsx";

const clone = (x) => JSON.parse(JSON.stringify(x ?? null));
const lines = (s) =>
  String(s || "")
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
const emptyGuide = () => ({ summary: "", when: [], how: [], examples: [] });

// a new example: every required parameter (and those with an example value)
function newExample(d, n) {
  const args = {};
  for (const p of d.params || []) if (p.required || p.example) args[p.name] = p.example || p.default || "";
  return { title: `Example ${n}`, text: "", args, sample: "", result: "" };
}

function cleanGuide(g) {
  return {
    summary: (g.summary || "").trim(),
    when: g.when || [],
    how: g.how || [],
    examples: (g.examples || []).map((x) => ({
      ...x,
      args: Object.fromEntries(Object.entries(x.args || {}).filter(([k, v]) => k.trim() && v !== "")),
    })),
  };
}

export function ToolGuideEditor({ tb, d }) {
  // the working copy: kept on tb so it survives switching tabs
  if (!tb.guide || tb.guide.of !== d.name) {
    const g = clone(d.guide) || emptyGuide();
    tb.guide = {
      of: d.name,
      g,
      whenText: (g.when || []).join("\n"),
      howText: (g.how || []).join("\n"),
      runs: {},
    };
  }
  const st = tb.guide,
    g = st.g;
  const [model, setModel] = useState("");
  const [ask, setAsk] = useState("");
  const ro = !!tb.ro;
  const saved = tb.editing && !ro && !tb.admin; // saved at once (keeps approval)
  const touch = () => {
    st.dirty = true;
    update();
  };
  const current = () => ({ ...cleanGuide(g), when: lines(st.whenText), how: lines(st.howText) });
  const section = () => toolGuideSection({ ...d, guide: current() });

  async function save() {
    const next = current();
    if (!saved) {
      d.guide = next;
      st.dirty = false;
      toast(tb.admin ? "Kept: it is published with the tool." : "Kept: it is saved with the tool.");
      return update();
    }
    try {
      const r = await backend.SaveToolGuide(d.name, next);
      d.guide = r.guide || null;
      st.dirty = false;
      await refreshExtensions();
      toast("Guide saved. What runs did not change, so the tool keeps its approval.");
    } catch (e) {
      toast(errMsg(e), "error");
    }
    update();
  }

  async function generate() {
    st.gen = { running: true };
    update();
    try {
      const r = await backend.GenerateToolGuide({ ...d, guide: current() }, model, ask);
      const ng = r.guide || emptyGuide();
      Object.assign(st, {
        g: { summary: ng.summary || "", when: ng.when || [], how: ng.how || [], examples: ng.examples || [] },
        whenText: (ng.when || []).join("\n"),
        howText: (ng.how || []).join("\n"),
        runs: {},
        dirty: true,
        gen: { why: r.why },
      });
    } catch (e) {
      st.gen = { error: errMsg(e) };
    }
    update();
  }

  async function run(k) {
    st.runs[k] = { busy: true };
    update();
    try {
      st.runs[k] = await backend.RunToolExample(d, cleanGuide(g).examples[k], {});
    } catch (e) {
      st.runs[k] = { error: errMsg(e) };
    }
    update();
  }

  const params = d.params || [];
  return (
    <div className="card">
      <div className="card-head">
        <h3>Guide &amp; examples</h3>
        {d.guide?.updated && <span className="badge">saved</span>}
        {st.dirty && <span className="badge badge-warn">not saved</span>}
      </div>
      <p className="wf-desc">
        How to use <code className="inline-code">{d.name}</code> in a flow, with worked examples. The guide
        shows in the Flow step guide (under <b>Your tools</b>) and in the designer's <b>How to use</b> for
        this tool. It does not change what runs, so the tool keeps its approval
        {d.scope === "global" ? "; it travels with the global library tool" : ""}
        {tb.admin ? "; it is published with the tool" : ""}.
      </p>
      {!ro && (
        <div className="row-actions" style={{ justifyContent: "flex-start", flexWrap: "wrap" }}>
          <ModelPicker value={model} onChange={setModel} label="Model for the guide" compact />
          <input
            style={{ flex: 1, minWidth: 220 }}
            placeholder="What the guide should cover (optional), e.g. joining a CSV with Postgres"
            aria-label="What the guide should cover"
            value={ask}
            onChange={(e) => setAsk(e.target.value)}
          />
          <button className="btn secondary" disabled={st.gen?.running} onClick={generate}>
            ✦ {g.summary || g.examples?.length ? "Rewrite with AI" : "Write with AI"}
          </button>
        </div>
      )}
      {st.gen?.running && <AiProgress phases={["Reading the tool", "Writing the guide and examples"]} />}
      {st.gen?.error && <div className="banner error">{st.gen.error}</div>}
      {st.gen?.why && <p className="muted">✦ {st.gen.why} Review it, run the examples, then save.</p>}
      <fieldset className="ro-fieldset" disabled={ro}>
        <label className="field">
          <span className="muted">Summary</span>
          <textarea
            rows={2}
            value={g.summary || ""}
            onChange={(e) => {
              g.summary = e.target.value;
              touch();
            }}
          />
        </label>
        <div className="two-col">
          <label className="field">
            <span className="muted">When to use it (one per line)</span>
            <textarea
              rows={4}
              value={st.whenText}
              onChange={(e) => {
                st.whenText = e.target.value;
                touch();
              }}
            />
          </label>
          <label className="field">
            <span className="muted">How to use it (one tip per line)</span>
            <textarea
              rows={4}
              value={st.howText}
              onChange={(e) => {
                st.howText = e.target.value;
                touch();
              }}
            />
          </label>
        </div>
      </fieldset>
      <h4 style={{ marginTop: 14 }}>Examples</h4>
      {(g.examples || []).length === 0 && (
        <p className="muted">No examples yet. Add one, or let the AI write the guide.</p>
      )}
      {(g.examples || []).map((x, k) => {
        const r = st.runs[k];
        const extra = Object.keys(x.args || {}).filter((n) => !params.some((p) => p.name === n));
        return (
          <div className="tg-ex" key={k}>
            <fieldset className="ro-fieldset" disabled={ro}>
              <div className="two-col">
                <label className="field">
                  <span className="muted">Title</span>
                  <input
                    value={x.title}
                    onChange={(e) => {
                      x.title = e.target.value;
                      touch();
                    }}
                  />
                </label>
                <label className="field">
                  <span className="muted">What it shows</span>
                  <input
                    value={x.text || ""}
                    onChange={(e) => {
                      x.text = e.target.value;
                      touch();
                    }}
                  />
                </label>
              </div>
              <div className="muted" style={{ marginTop: 8 }}>
                Step fields — <code className="inline-code">{"{{sample.buffer_output}}"}</code>,{" "}
                <code className="inline-code">{"{{input}}"}</code> and{" "}
                <code className="inline-code">{"{{input_file}}"}</code> are the sample input below
              </div>
              <div className="tg-args">
                {[...params.map((p) => p.name), ...extra].map((n) => {
                  const p = params.find((q) => q.name === n);
                  const v = x.args?.[n] ?? "";
                  return [
                    <code
                      key={n + "l"}
                      className="inline-code"
                      title={p?.desc || "not a parameter of this tool"}
                    >
                      {n}
                      {p?.required ? " *" : ""}
                      {!p ? " ?" : ""}
                    </code>,
                    <textarea
                      key={n}
                      rows={Math.min(8, Math.max(1, String(v).split("\n").length))}
                      aria-label={n}
                      placeholder={
                        p?.default ? `default: ${p.default}` : p?.example ? `e.g. ${p.example}` : ""
                      }
                      value={v}
                      onChange={(e) => {
                        x.args = { ...(x.args || {}), [n]: e.target.value };
                        touch();
                      }}
                    />,
                  ];
                })}
              </div>
              <div className="two-col">
                <label className="field">
                  <span className="muted">Sample input (the previous step's output; optional)</span>
                  <textarea
                    rows={4}
                    value={x.sample || ""}
                    onChange={(e) => {
                      x.sample = e.target.value;
                      touch();
                    }}
                  />
                </label>
                <label className="field">
                  <span className="muted">Result (shown in the guide)</span>
                  <textarea
                    rows={4}
                    value={x.result || ""}
                    onChange={(e) => {
                      x.result = e.target.value;
                      touch();
                    }}
                  />
                </label>
              </div>
            </fieldset>
            {r && (
              <div className="guide-live">
                {r.busy ? (
                  <p className="muted">Running {d.name}…</p>
                ) : (
                  <>
                    {(r.problems?.length || r.error) && (
                      <div className="banner error">{(r.problems || []).join("; ") || r.error}</div>
                    )}
                    {r.output && <pre className="code guide-pre">{r.output}</pre>}
                    {r.durationMs != null && (
                      <small className="muted">
                        {r.ok ? "OK" : "Failed"} · {r.durationMs} ms
                      </small>
                    )}
                  </>
                )}
              </div>
            )}
            <div className="row-actions">
              {!ro && (
                <button
                  className="btn secondary"
                  onClick={() => {
                    g.examples.splice(k, 1);
                    st.runs = {};
                    touch();
                  }}
                >
                  Remove
                </button>
              )}
              {!ro && r?.ok && r.output && r.output !== x.result && (
                <button
                  className="btn secondary"
                  title="Saves this output as the example's result"
                  onClick={() => {
                    x.result = r.output.length > 4000 ? r.output.slice(0, 4000) + "\n…" : r.output;
                    touch();
                  }}
                >
                  Use this result
                </button>
              )}
              <button className="btn secondary" disabled={r?.busy} onClick={() => run(k)}>
                Run example
              </button>
              <button
                className="btn secondary"
                title="Saves this example as a flow in the workspace and opens it in the Flow designer"
                onClick={() => {
                  if (!tb.editing) return toast("Save the tool first: the flow's step calls the saved tool.");
                  const s = section();
                  tryExample(s, s.examples[k]);
                }}
              >
                Try it in the designer
              </button>
            </div>
          </div>
        );
      })}
      <div className="row-actions">
        {!ro && (
          <button
            className="btn secondary"
            disabled={(g.examples || []).length >= 20}
            onClick={() => {
              g.examples = [...(g.examples || []), newExample(d, (g.examples || []).length + 1)];
              touch();
            }}
          >
            + Add example
          </button>
        )}
        {d.guide && tb.editing && (
          <button className="btn secondary" onClick={() => openFlowGuide("tool:" + d.name)}>
            Open in the Flow step guide
          </button>
        )}
        {!ro && (
          <button className="btn primary" disabled={!st.dirty} onClick={save}>
            {saved ? "Save guide" : "Keep guide"}
          </button>
        )}
      </div>
    </div>
  );
}
