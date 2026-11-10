// Where a run's results were saved: .codegen/runs/<date>/<run id>/ with
// summary.md, prompt.md, answer.md, tools.md, outputs/… Shown under every
// run (Spec workflow, Playground, Run flows, History); each file opens in
// the quick preview.
import { useEffect } from "react";
import { backend } from "./backend.js";
import { S, update, toast, errMsg } from "./store.js";
import { Icon } from "./ui.jsx";
import { FileLink } from "./FileLinks.jsx";

const KEY_FILES = ["summary.md", "answer.md", "prompt.md", "tools.md", "files.md"];

async function load(id) {
  S.results[id] = { loading: true };
  try { S.results[id] = await backend.RunResults(id); }
  catch (e) { S.results[id] = { error: errMsg(e) }; }
  update();
}

export function ResultsLink({ id, ended = true }) {
  const r = id ? S.results[id] : null;
  useEffect(() => { if (id && ended && !S.results[id]) load(id); }, [id, ended, r]);
  if (!id || !ended || !r || r.loading || r.error || !r.ready) return null;
  const key = KEY_FILES.filter((f) => r.files.includes(f));
  const outs = r.files.filter((f) => f.startsWith("outputs/"));
  return (
    <div className="results-link">
      <span className="results-head"><Icon.folder /> Results saved in <FileLink path={r.dir}>{r.dir}/</FileLink></span>
      <span className="results-files">{[...key, ...outs.slice(0, 6)].map((f) => <FileLink key={f} path={r.dir + "/" + f}>{f}</FileLink>)}{outs.length > 6 && <span className="muted">+{outs.length - 6} outputs</span>}</span>
      <span className="results-actions">
        <button className="link" title={"Run id " + id} onClick={() => navigator.clipboard?.writeText(id).then(() => toast("Run id copied."), () => {})}>Copy run id</button>
        {backend.kind === "desktop" && <button className="link" onClick={() => backend.RevealPath(r.dir + "/summary.md").catch((e) => toast(errMsg(e)))}>Show in Finder</button>}
      </span>
    </div>
  );
}
