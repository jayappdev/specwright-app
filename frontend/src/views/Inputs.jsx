import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg } from "../store.js";
import { NeedWorkspace, WorkspaceBar } from "./Wizard.jsx";

const CTX_SVG = <svg className="diagram-thumb" viewBox="0 0 320 120" aria-hidden="true"><g fill="none" stroke="currentColor" strokeWidth="1.2" style={{ color: "var(--muted)" }}><rect x="120" y="40" width="80" height="40" rx="6" /><rect x="10" y="10" width="70" height="30" rx="4" /><rect x="10" y="80" width="70" height="30" rx="4" /><rect x="240" y="45" width="70" height="30" rx="4" /><path d="M80 25L120 50M80 95L120 70M200 60h40" /></g></svg>;
const SEQ_SVG = <svg className="diagram-thumb" viewBox="0 0 320 120" aria-hidden="true"><g fill="none" stroke="currentColor" strokeWidth="1.2" style={{ color: "var(--muted)" }}><path d="M50 22v92M160 22v92M270 22v92" strokeDasharray="3 3" /><path d="M50 45h110M160 65h110M270 85H160M160 100H50" /></g></svg>;
const DIAGRAM = /\.(drawio|svg|png|puml|plantuml|mmd|md)$/i;

async function setDiagram(kind, rel) {
  try { await backend.SetDiagram(kind, rel); S.ws.profile.diagrams = { ...(S.ws.profile.diagrams || {}) }; if (rel) S.ws.profile.diagrams[kind] = rel; else delete S.ws.profile.diagrams[kind]; update(); }
  catch (e) { toast(errMsg(e)); }
}

function DiagramCard({ kind, title, desc, thumb }) {
  const v = S.ws.profile.diagrams?.[kind];
  const candidates = S.files.list.filter((f) => DIAGRAM.test(f) && !f.startsWith(".codegen/") && !f.startsWith("specs/"));
  return (
    <div className="card"><div className="card-head"><h3>{title}</h3>{v ? <span className="badge badge-ok">attached</span> : <span className="badge">optional</span>}</div>
      <p className="wf-desc">{desc}</p>
      <div className={"drop" + (v ? " has" : "")}>
        {v ? <>{thumb}<span><code className="inline-code">{v}</code></span><div className="row-actions" style={{ margin: 0 }}><button className="btn secondary" onClick={() => setDiagram(kind, "")}>Remove</button></div></>
          : <>
            <span className="muted">Pick a .drawio, .svg, .png, Mermaid or PlantUML file. Its path is sent with every step that has “Attach input diagrams” on.</span>
            <div className="row-actions" style={{ margin: 0 }}>
              {backend.kind === "desktop" && <button className="btn secondary" onClick={async () => { try { const rel = await backend.PickDiagram(kind); if (rel) { S.ws.profile.diagrams = { ...(S.ws.profile.diagrams || {}), [kind]: rel }; update(); } } catch (e) { toast(errMsg(e)); } }}>Choose file…</button>}
              <select aria-label={"Workspace file for " + title} value="" onChange={(e) => e.target.value && setDiagram(kind, e.target.value)}>
                <option value="">{candidates.length ? "…or use a file in the workspace" : "No diagram files in the workspace"}</option>
                {candidates.map((f) => <option key={f} value={f}>{f}</option>)}
              </select>
            </div>
          </>}
      </div>
    </div>
  );
}

export function Inputs() {
  useEffect(() => { if (S.ws) backend.ListFiles().then((l) => { S.files.list = l; update(); }).catch(() => {}); }, [S.ws?.root]);
  if (!S.ws) return <NeedWorkspace what="inputs" />;
  const p = S.ws.profile;
  return (
    <section className="view">
      <header className="view-head"><h1>Choose input</h1><div className="head-controls"><button className="btn primary" onClick={() => { S.view = "workflow"; update(); }}>Next: run spec workflow</button></div></header>
      <p className="wf-desc">Point the agent at the source to read, then add diagrams so the spec starts from what the system actually does.</p>
      <div className="card"><div className="card-head"><h3>Project folder</h3>{S.ws.specKit ? <span className="badge badge-ok">spec project ready</span> : <span className="badge badge-warn">no .specify/ yet</span>}</div>
        <WorkspaceBar />
        <p className="muted" style={{ marginTop: 8 }}>{S.ws.fileCount} files indexed · {p.lang} · {p.build}. Only this folder is visible to the agent.
          {!S.ws.specKit && <> Run <code className="inline-code">specify init --here --ai {S.setup.provider === "copilot" ? "copilot" : "claude"}</code> in it once to set up the spec commands the workflow uses.</>}</p>
      </div>
      <div className="two-col">
        <DiagramCard kind="context" title="Context / activity diagram" desc="Who and what the system talks to, or the activity flow of the feature." thumb={CTX_SVG} />
        <DiagramCard kind="sequence" title="Sequence diagram" desc="Calls between components for the main scenario." thumb={SEQ_SVG} />
      </div>
    </section>
  );
}
