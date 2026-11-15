// Flow step guide: how to use each kind of flow step, with examples whose
// results are previewed live and that open in the Flow designer.
import { useEffect, useState } from "react";
import { backend } from "../backend.js";
import { Pane } from "../Pane.jsx";
import { S, update, toast, errMsg } from "../store.js";
import { GUIDE, exampleFlow } from "./flowGuide.js";
import { StepIcon, openFlowInDesigner } from "./Flows.jsx";

// Open the guide at a step type (from the Flow designer).
export function openFlowGuide(type) {
  S.guide = type || "";
  S.view = "flowguide";
  update();
}

export const GUIDE_TYPES = new Set(GUIDE.map((g) => g.type));

// text with {{variables}} and `code` shown as code
function Rich({ text }) {
  const parts = String(text).split(/(\{\{[^}]*\}\}|`[^`]+`)/g);
  return (
    <>
      {parts.map((p, i) =>
        p.startsWith("{{") || p.startsWith("`") ? (
          <code key={i} className="inline-code" data-nofile>
            {p.replace(/^`|`$/g, "")}
          </code>
        ) : (
          p
        ),
      )}
    </>
  );
}

const previews = {}; // "<type>/<n>" → { output, error }

function stepSettings(n) {
  const c = { ...n };
  for (const k of ["id", "type", "x", "y", "title", "sample"]) delete c[k];
  return JSON.stringify(c, null, 2);
}

async function tryExample(g, ex) {
  if (!S.ws)
    return toast(
      "Open a workspace first (Development wizard or Choose input): the example is saved as a flow in it.",
    );
  try {
    // a second copy of the same example gets "(2)", and so on, so the flow list tells them apart
    const names = new Set(((await backend.Flows().catch(() => [])) || []).map((x) => x.name));
    let name = "Example: " + ex.title;
    for (let k = 2; names.has(name); k++) name = `Example: ${ex.title} (${k})`;
    const base = await backend.NewFlow(name);
    const { flow } = exampleFlow(ex);
    await backend.SaveFlow({
      ...base,
      name,
      description: `An example from the Flow step guide (${g.name}). Its first steps produce sample data; replace them with your own tools.`,
      inputs: flow.inputs,
      nodes: flow.nodes,
      edges: flow.edges,
    });
    toast(`Saved as the flow “${name}”. Delete it when you are done.`);
    openFlowInDesigner(base.id, ex.focus);
  } catch (e) {
    toast(errMsg(e), "error");
  }
}

function Example({ g, ex, k }) {
  const key = g.type + "/" + k,
    [, redraw] = useState(0);
  const { flow, steps } = exampleFlow(ex);
  const node = flow.nodes.find((n) => n.id === ex.focus);
  const title = (id) => flow.nodes.find((n) => n.id === id)?.title || id;
  const feeds = flow.edges
    .filter((e) => e.to === ex.focus)
    .map((e) => e.from)
    .filter((id) => id in steps);
  const after = flow.edges.filter((e) => e.from === ex.focus);
  useEffect(() => {
    if (previews[key] || g.type === "ai") return;
    previews[key] = { busy: true };
    backend
      .PreviewFlowStep({ id: "guide", name: "guide", ...flow, skills: [], context: { text: "" } }, ex.focus, {
        inputs: {},
        steps,
      })
      .then((r) => {
        previews[key] = r || {};
        redraw((x) => x + 1);
      })
      .catch((e) => {
        previews[key] = { error: errMsg(e) };
        redraw((x) => x + 1);
      });
  }, [key]);
  const res = g.type === "ai" ? { output: ex.result, ai: true } : previews[key] || { busy: true };
  // the path: every step in order, the example's step highlighted
  const order = [];
  const visit = (id) => {
    if (order.includes(id)) return;
    order.push(id);
    flow.edges.filter((e) => e.from === id).forEach((e) => visit(e.to));
  };
  visit("start");
  return (
    <div className="guide-ex">
      <h4>{ex.title}</h4>
      <p>
        <Rich text={ex.text} />
      </p>
      <div className="guide-path">
        {order
          .filter((id) => id !== "start")
          .map((id, i) => {
            const n = flow.nodes.find((x) => x.id === id),
              br = flow.edges.find((e) => e.to === id && e.branch);
            return (
              <span key={id} className={"guide-chip" + (id === ex.focus ? " focus" : "")}>
                {i > 0 && (
                  <span className="muted">
                    {br
                      ? `◆ ${br.branch === "else" ? "Otherwise" : (flow.nodes.find((x) => x.id === br.from)?.branches || []).find((b) => b.id === br.branch)?.label || br.branch} →`
                      : "→"}
                  </span>
                )}
                <StepIcon n={n} tools={[]} size={20} />
                {n.title || id}
              </span>
            );
          })}
      </div>
      <div className="guide-grid">
        {feeds.map((id) => (
          <div key={id}>
            <div className="muted">Input · {title(id)}</div>
            <pre className="code guide-pre">{steps[id]}</pre>
          </div>
        ))}
        <div>
          <div className="muted">{node.title || node.id} · settings</div>
          <pre className="code guide-pre">{stepSettings(node)}</pre>
        </div>
        <div>
          <div className="muted">
            {res.ai ? "Result · an example answer (the AI's will vary)" : "Result · previewed by the app"}
          </div>
          {res.busy ? (
            <p className="muted">Previewing…</p>
          ) : res.error ? (
            <div className="banner error">{res.error}</div>
          ) : node.type === "output" && node.format === "html" ? (
            <iframe className="fl-html guide-html" sandbox="" srcDoc={res.output} title="HTML preview" />
          ) : (
            <pre className="code guide-pre">{res.output}</pre>
          )}
          {after.length > 0 && node.type !== "decision" && (
            <small className="muted">Then: {after.map((e) => title(e.to)).join(", ")}</small>
          )}
        </div>
      </div>
      <div className="row-actions">
        <button
          className="btn secondary"
          onClick={() => tryExample(g, ex)}
          title="Saves this example as a flow in the workspace and opens it in the Flow designer"
        >
          Try it in the designer
        </button>
      </div>
    </div>
  );
}

function Section({ g }) {
  return (
    <section className="card guide-step" id={"guide-" + g.type}>
      <div className="guide-head">
        <StepIcon n={{ type: g.type }} tools={[]} size={40} />
        <div>
          <h2>{g.name}</h2>
          <p className="wf-desc">
            <Rich text={g.summary} />
          </p>
        </div>
      </div>
      <div className="guide-cols">
        <div>
          <h3>When to use it</h3>
          <ul>
            {g.when.map((w, i) => (
              <li key={i}>
                <Rich text={w} />
              </li>
            ))}
          </ul>
        </div>
        <div>
          <h3>Settings</h3>
          <dl className="guide-dl">
            {g.settings.map(([k, v]) => (
              <div key={k}>
                <dt>{k}</dt>
                <dd>
                  <Rich text={v} />
                </dd>
              </div>
            ))}
          </dl>
        </div>
      </div>
      <h3>How to use it</h3>
      <ol className="guide-how">
        {g.how.map((h, i) => (
          <li key={i}>
            <Rich text={h} />
          </li>
        ))}
      </ol>
      <h3>Examples</h3>
      {g.examples.map((ex, k) => (
        <Example key={k} g={g} ex={ex} k={k} />
      ))}
    </section>
  );
}

export function FlowGuide() {
  const [cur, setCur] = useState(S.guide || GUIDE[0].type);
  useEffect(() => {
    if (!S.guide) return;
    const el = document.getElementById("guide-" + S.guide);
    if (el) el.scrollIntoView({ block: "start" });
    S.guide = "";
  }, [S.guide]);
  const go = (t) => {
    setCur(t);
    document.getElementById("guide-" + t)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };
  return (
    <div className="files guide">
      <Pane k="flowguide" v="--pane-l" pane=".ftree" min={200} max={480} label="Resize the step list" />
      <nav className="ftree" aria-label="Flow steps">
        <div style={{ padding: "0 8px 8px" }}>
          <h1>Flow step guide</h1>
          <button
            className="link"
            onClick={() => {
              S.view = "flows";
              update();
            }}
          >
            ← Back to the Flow designer
          </button>
        </div>
        {GUIDE.map((g) => (
          <button
            key={g.type}
            className={"run-item guide-nav" + (cur === g.type ? " active" : "")}
            onClick={() => go(g.type)}
          >
            <StepIcon n={{ type: g.type }} tools={[]} size={22} />
            <span>
              <b>{g.name}</b>
              <small>{g.examples.length} examples</small>
            </span>
          </button>
        ))}
      </nav>
      <section className="view guide-main">
        <div className="card">
          <h2>How the flow steps work</h2>
          <p className="wf-desc">
            A flow passes data from step to step along its connections: each step gets the output of the steps
            connected into it, and any step can read an earlier one by its step id,{" "}
            <code className="inline-code" data-nofile>
              {"{{<step id>.buffer_output}}"}
            </code>{" "}
            (its text) or{" "}
            <code className="inline-code" data-nofile>
              {"{{<step id>.output_file}}"}
            </code>{" "}
            (the file it was saved in). Below, each step type is explained with examples. The results shown
            are produced by the app from the sample data, and <b>Try it in the designer</b> saves an example
            as a flow you can open, change and run.
          </p>
          <p className="wf-desc">
            Every step's inspector has a <b>Data</b> panel: the fields it gets and gives (nested objects and
            lists, with types), whether the shape is declared (by a tool or an AI step's expected reply),
            worked out from the steps before, or learned from sample data or the last run. Click a field to
            use it where you are typing; path boxes suggest the fields, and the flow is checked for fields
            that are not there.
          </p>
          <p className="wf-desc">
            Not sure where to start? <b>✦ New flow with AI</b> (in the Flows list) turns a plain description
            into a few ready-made flows built from your tools, with AI steps only where needed. After a run,{" "}
            <b>Run flows</b> offers <b>✦ Fix with AI</b> on a failed step and <b>✦ Improve</b> on a finished
            one: the AI changes only that step, you try the change (the steps before it reuse the run's
            results) and apply it to the flow when the result is right.
          </p>
        </div>
        {GUIDE.map((g) => (
          <Section key={g.type} g={g} />
        ))}
      </section>
    </div>
  );
}
