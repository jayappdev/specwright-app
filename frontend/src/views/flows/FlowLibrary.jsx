// The Flow designer's library: every flow of the project, as cards, when no
// flow is open. Opening one gives the editor the whole width; its "← Flows"
// (or the app's Back) returns here.
import { FlowFilterBar, FlowGroup, FlowTagChips, flowFilter, groupFlows } from "../../flowLists.jsx";
import { dragProps } from "../../listLayout.jsx";
import { S } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { ago } from "../../time.js";

function FlowCard({ x, list, onOpen }) {
  const parent = x.revision > 1 ? (list || []).find((y) => y.id === x.parent)?.name || x.parent : "";
  return (
    <button {...dragProps("flows", x.id)} className="fl-card" onClick={() => onOpen(x.id)}>
      <span className={"fl-card-icon" + (x.chat ? " is-chat" : "")} aria-hidden="true">
        {x.chat ? <Icon.chat /> : <Icon.workflow />}
      </span>
      <span className="fl-card-body">
        <span className="fl-card-top">
          <b>{x.name}</b>
          {x.chat && (
            <span className="badge badge-info" title="A Chatflow: chatted with on the Chats screen">
              chat
            </span>
          )}
          {x.revision > 1 && (
            <span className="badge" title={"A version of " + parent}>
              v{x.revision}
            </span>
          )}
        </span>
        {x.description && <span className="fl-card-desc">{x.description}</span>}
        <span className="fl-card-meta">
          {x.steps} step{x.steps === 1 ? "" : "s"}
          {!x.chat && (
            <>
              {" · "}
              {x.outputs} output{x.outputs === 1 ? "" : "s"}
            </>
          )}
          {x.updated ? " · edited " + ago(x.updated) : ""}
        </span>
        <FlowTagChips tags={x.tags} area="designer" />
      </span>
    </button>
  );
}

// actions: { open, create, withAI, importFile, paste, guide }
export function FlowLibrary({ list, actions }) {
  const groups = groupFlows(list, flowFilter("designer"));
  const n = (list || []).length;
  return (
    <section className="fl-lib" aria-label="Flows">
      <header className="fl-lib-head">
        <div>
          <h1>Flows</h1>
          <p className="muted">
            {n ? `${n} flow${n === 1 ? "" : "s"} in this project. ` : ""}
            Steps on a canvas: inputs → tools, AI and transforms → outputs. A Chat step makes a Chatflow.
          </p>
        </div>
        <div className="fl-lib-acts">
          <button className="btn secondary" onClick={actions.importFile}>
            Import…
          </button>
          {S.flowClip && (
            <button
              className="btn secondary"
              title={`${S.flowClip.nodes.length} copied step(s) from ${S.flowClip.from || "a flow"}`}
              onClick={actions.paste}
            >
              Paste {S.flowClip.nodes.length} step{S.flowClip.nodes.length === 1 ? "" : "s"} as a flow
            </button>
          )}
          <button
            className="btn secondary"
            title="Describe what you want: the AI works out the requirements with you, plans, and builds draft flows to try"
            onClick={actions.withAI}
          >
            <Icon.spark /> New with AI
          </button>
          <button className="btn primary" onClick={actions.create}>
            <Icon.plus /> New flow
          </button>
        </div>
      </header>
      {list === null || list === undefined ? (
        <p className="muted">Loading…</p>
      ) : n ? (
        <>
          <FlowFilterBar area="designer" list={list} />
          {!groups.length && <p className="muted">No flow matches.</p>}
          {groups.map((g) => (
            <FlowGroup
              key={g.key}
              area="designer"
              name={g.name}
              heading={g.heading}
              count={g.items.length}
              groups={groups.length}
            >
              <div className="fl-cards">
                {g.items.map((x) => (
                  <FlowCard key={x.id} x={x} list={list} onOpen={actions.open} />
                ))}
              </div>
            </FlowGroup>
          ))}
        </>
      ) : (
        <div className="fl-lib-empty">
          <Icon.workflow />
          <h3>Design your first flow</h3>
          <p className="muted">
            Drag tools onto a canvas and connect them. Give the flow inputs (text, files, clipboard), merge
            and transform data between steps, and produce outputs that are saved, shown or passed to a tool.
          </p>
          <div className="row-actions">
            <button className="btn primary" onClick={actions.withAI}>
              <Icon.spark /> Describe it to the AI
            </button>
            <button className="btn secondary" onClick={actions.create}>
              Create a flow
            </button>
            <button className="link" onClick={actions.guide}>
              Flow step guide
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
