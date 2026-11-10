// Asks for secret values a tool needs right now. Shown on every page when a
// "secret.request" arrives (a tool test, or an agent calling a tool during
// a workflow step); the tool call waits until you answer or decline.
import { useEffect, useRef, useState } from "react";
import { backend } from "./backend.js";
import { S, update, toast, errMsg } from "./store.js";
import { Icon } from "./ui.jsx";
import { Logo } from "./Logo.jsx";

export function SecretPrompt() {
  const req = S.secretAsks[0];
  const [vals, setVals] = useState({});
  const [remember, setRemember] = useState(false);
  const [keep, setKeep] = useState(false);
  const [kcOK, setKcOK] = useState(false);
  useEffect(() => { backend.KeychainAvailable?.().then(setKcOK, () => {}); }, []);
  const [busy, setBusy] = useState(false);
  const first = useRef(null);
  useEffect(() => { setVals({}); setRemember(false); setBusy(false); setTimeout(() => first.current?.focus(), 30); }, [req?.id]);
  if (!req) return null;
  const done = () => { S.secretAsks = S.secretAsks.filter((r) => r.id !== req.id); update(); };
  const submit = async (e) => {
    e.preventDefault();
    const empty = req.secrets.filter((s) => !vals[s.name]);
    if (empty.length) return toast("Enter " + empty.map((s) => s.name).join(", ") + ", or choose Don't provide.");
    setBusy(true);
    try {
      if (keep) for (const s of req.secrets) await backend.SaveSecretToKeychain(s.name, vals[s.name]).catch((err) => toast(errMsg(err)));
      await backend.AnswerSecrets(req.id, vals, remember); done();
    }
    catch (err) { toast(errMsg(err)); done(); }
  };
  const decline = async () => { await backend.CancelSecrets(req.id).catch(() => {}); done(); };
  return (
    <div className="modal-scrim" role="presentation" onKeyDown={(e) => e.key === "Escape" && decline()}>
      <form className="card modal secret-modal" role="dialog" aria-modal="true" aria-labelledby="sp-title" onSubmit={submit} autoComplete="off">
        <div className="card-head"><Logo size={28} /><h3 id="sp-title"><code className="inline-code">{req.tool}</code> needs {req.secrets.length === 1 ? "a secret" : req.secrets.length + " secrets"}</h3>
          {S.secretAsks.length > 1 && <span className="badge">{S.secretAsks.length - 1} more waiting</span>}</div>
        <p className="wf-desc">{req.step ? <>{S.flowRuns[req.run] ? <>The flow <b>{S.flowRuns[req.run].flowName}</b> is paused at <b>{req.step}</b> until you answer. </> : <>The agent is calling this tool during <b>{req.step}</b>. </>}</> : null}It waits until you answer. Values go only to the tool's process as environment variables; they are never saved, shown to the model or written to history.</p>
        {req.secrets.map((s, k) => (
          <label key={s.name} className="field" style={{ marginTop: 8 }}><span><code className="inline-code">{s.name}</code> <span className="muted">{s.desc}</span></span>
            <input ref={k === 0 ? first : null} type="password" autoComplete="off" spellCheck="false" value={vals[s.name] || ""} onChange={(e) => setVals({ ...vals, [s.name]: e.target.value })} /></label>
        ))}
        <label className="check" style={{ marginTop: 10 }}><input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} /> Remember for this session (until the app quits)</label>
        {kcOK && <label className="check" style={{ marginTop: 4 }}><input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} /> Save in the system keychain, so later runs find it without asking</label>}
        <div className="row-actions" style={{ marginTop: 12 }}>
          <button className="btn primary" disabled={busy}><Icon.check /> Provide and run</button>
          <button type="button" className="btn secondary" onClick={decline}>Don't provide</button>
        </div>
      </form>
    </div>
  );
}
