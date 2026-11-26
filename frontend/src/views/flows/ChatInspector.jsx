// The Chat step's inspector (docs/design/2026-11-24-chatflows.md): the
// assistant's instructions, starter questions, model and cost limit; the
// Chatflow's variables and secrets; and what the chat can do (its setup
// steps and its tools, as the backend derives them: flow/chatflow.go).
import { ModelPicker } from "../../ModelPicker.jsx";
import { Icon } from "../../ui.jsx";
import { changed } from "../Flows.jsx";
import { Field } from "./Inspector.jsx";
import { track } from "./Fields.jsx";

// the steps with a path to id, and those id reaches
const reach = (flow, id, dir) => {
  const seen = new Set();
  const walk = (x) =>
    flow.edges
      .filter((e) => (dir === "up" ? e.to === x : e.from === x))
      .forEach((e) => {
        const y = dir === "up" ? e.from : e.to;
        if (!seen.has(y)) {
          seen.add(y);
          walk(y);
        }
      });
  walk(id);
  return seen;
};

// chatSetup and chatTools mirror flow.ChatSetup and flow.ChatTools
export const chatSetup = (flow, chat) =>
  flow.nodes.filter((n) => reach(flow, chat.id, "up").has(n.id) && n.type !== "start");
export function chatTools(flow, chat) {
  return flow.edges
    .filter((e) => e.from === chat.id)
    .map((e) => {
      const first = flow.nodes.find((n) => n.id === e.to);
      if (!first) return null;
      const set = reach(flow, first.id, "down");
      set.add(first.id);
      return { id: first.id, title: first.title || first.id, steps: flow.nodes.filter((n) => set.has(n.id)) };
    })
    .filter(Boolean);
}

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;

// Rows edits a list of {name, value?, desc} (variables or secrets).
function Rows({ list, onChange, secret }) {
  const setRow = (i, k, v) => {
    const next = list.map((x, j) => (j === i ? { ...x, [k]: v } : x));
    onChange(next);
  };
  return (
    <div className="chat-env">
      {list.map((v, i) => (
        <div key={i} className="chat-env-row">
          <input
            aria-label={secret ? "Secret name" : "Variable name"}
            className={
              "mono" + (secret ? " chat-env-wide" : "") + (v.name && !NAME_RE.test(v.name) ? " bad" : "")
            }
            placeholder={secret ? "DB_PASSWORD" : "REGION"}
            value={v.name}
            onChange={(e) => setRow(i, "name", e.target.value.trim())}
          />
          {!secret && (
            <input
              aria-label="Value"
              placeholder="eu"
              value={v.value || ""}
              onChange={(e) => setRow(i, "value", e.target.value)}
            />
          )}
          <input
            className="chat-env-desc"
            aria-label="What it is"
            placeholder={secret ? "What it is for (shown when asked)" : "What it is for"}
            value={v.desc || ""}
            onChange={(e) => setRow(i, "desc", e.target.value)}
          />
          <button
            className="icon-btn"
            aria-label="Remove"
            title="Remove"
            onClick={() => onChange(list.filter((_, j) => j !== i))}
          >
            <Icon.x />
          </button>
        </div>
      ))}
      <button
        className="btn secondary"
        onClick={() =>
          onChange([...list, secret ? { name: "", desc: "" } : { name: "", value: "", desc: "" }])
        }
      >
        <Icon.plus /> {secret ? "Add a secret" : "Add a variable"}
      </button>
    </div>
  );
}

export function ChatInspector({ n, set, flow }) {
  const setup = chatSetup(flow, n),
    tools = chatTools(flow, n);
  const setFlow = (k, v) => {
    flow[k] = v;
    changed();
  };
  return (
    <>
      <p className="wf-desc">
        This makes the flow a <b>Chatflow</b>: people chat with it on the Chats screen. The steps before this
        one set up each session (they run once); each path out of it is something the chat can run when a
        question needs it.
      </p>
      <Field
        label="Instructions"
        hint="What the assistant is for and how it answers. It also gets the setup steps' results and the list of what it can run."
      >
        <textarea
          rows={6}
          value={n.instructions || ""}
          placeholder="e.g. You answer questions about our sales data. Use the data you were given; run Query sales for anything it does not cover. Show numbers in tables."
          {...track}
          onChange={(e) => set("instructions", e.target.value)}
        />
      </Field>
      <Field label="Starter questions" hint="One per line: shown when a session has no messages yet.">
        <textarea
          rows={3}
          value={(n.starters || []).join("\n")}
          placeholder={"Which region grew the most?\nWhat were last month's top products?"}
          onChange={(e) =>
            set(
              "starters",
              e.target.value.split("\n").filter((x, i, all) => x.trim() || i === all.length - 1),
            )
          }
        />
      </Field>
      <div className="field fl-field">
        <span className="muted">Model</span>
        <ModelPicker value={n.model || ""} onChange={(v) => set("model", v)} label="Model" />
        <small className="muted">Default: the app's model.</small>
      </div>
      <Field label="Cost limit per session (USD)" hint="0 or empty: the project's budget.">
        <input
          type="number"
          min="0"
          step="0.5"
          value={n.costLimit || ""}
          placeholder="e.g. 2"
          onChange={(e) => set("costLimit", Number(e.target.value) || 0)}
        />
      </Field>

      <h4 className="chat-h">Environment</h4>
      <p className="muted chat-note">
        Given to every step a session runs. Variables have values here (a variable of the same name in your
        environment wins); secrets are named only: their values come from Tools › Secrets, the keychain or are
        asked for once, and never appear in the chat or the flow file.
      </p>
      <div className="muted chat-sub">Variables</div>
      <Rows list={flow.vars || []} onChange={(v) => setFlow("vars", v)} />
      <div className="muted chat-sub">Secrets</div>
      <Rows list={flow.secrets || []} onChange={(v) => setFlow("secrets", v)} secret />

      <h4 className="chat-h">What the chat can do</h4>
      <div className="muted chat-sub">Setup · runs once per session</div>
      {setup.length ? (
        <ol className="chat-list">
          {setup.map((s) => (
            <li key={s.id}>{s.title || s.id}</li>
          ))}
        </ol>
      ) : (
        <p className="muted chat-note">Nothing yet: connect steps between the Inputs step and this one.</p>
      )}
      <div className="muted chat-sub">Can run · one per path out of this step</div>
      {tools.length ? (
        <ul className="chat-list">
          {tools.map((t) => (
            <li key={t.id}>
              <b>{t.title}</b>
              {t.steps.length > 1 && (
                <span className="muted"> · {t.steps.map((s) => s.title || s.id).join(" → ")}</span>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted chat-note">Nothing yet: connect steps after this one (a query, a fetch…).</p>
      )}
    </>
  );
}
