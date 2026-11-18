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
  useEffect(() => {
    backend.KeychainAvailable?.().then(setKcOK, () => {});
  }, []);
  const [busy, setBusy] = useState(false);
  const [missing, setMissing] = useState([]);
  const [shown, setShown] = useState({});
  const first = useRef(null);
  useEffect(() => {
    setVals({});
    setRemember(false);
    setBusy(false);
    setMissing([]);
    setShown({});
    setTimeout(() => first.current?.focus(), 30);
  }, [req?.id]);
  if (!req) return null;
  const done = () => {
    S.secretAsks = S.secretAsks.filter((r) => r.id !== req.id);
    update();
  };
  const submit = async (e) => {
    e.preventDefault();
    const empty = req.secrets.filter((s) => !vals[s.name]);
    setMissing(empty.map((s) => s.name));
    if (empty.length) {
      document.getElementById("sp-in-" + empty[0].name)?.focus();
      return;
    }
    setBusy(true);
    try {
      if (keep)
        for (const s of req.secrets)
          await backend
            .SaveSecretToKeychain(s.name, vals[s.name])
            .catch((err) => toast(errMsg(err), "error"));
      await backend.AnswerSecrets(req.id, vals, remember);
      done();
    } catch (err) {
      toast(errMsg(err), "error");
      done();
    }
  };
  const decline = async () => {
    await backend.CancelSecrets(req.id).catch(() => {});
    done();
  };
  return (
    <div className="modal-scrim" role="presentation" onKeyDown={(e) => e.key === "Escape" && decline()}>
      <form
        className="card modal secret-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="sp-title"
        onSubmit={submit}
        autoComplete="off"
      >
        <div className="card-head">
          <Logo size={28} />
          <h3 id="sp-title">
            <code className="inline-code">{req.tool}</code> needs{" "}
            {req.secrets.length === 1 ? "a secret" : req.secrets.length + " secrets"}
          </h3>
          {S.secretAsks.length > 1 && <span className="badge">{S.secretAsks.length - 1} more waiting</span>}
        </div>
        <p className="wf-desc">
          {req.step ? (
            <>
              {S.flowRuns[req.run] ? (
                <>
                  The flow <b>{S.flowRuns[req.run].flowName}</b> is paused at <b>{req.step}</b> until you
                  answer.{" "}
                </>
              ) : (
                <>
                  The agent is calling this tool during <b>{req.step}</b>.{" "}
                </>
              )}
            </>
          ) : null}
          It waits until you answer. Values go only to the tool's process as environment variables; they are
          never saved, shown to the model or written to history.
        </p>
        {req.secrets.map((s, k) => {
          const bad = missing.includes(s.name) && !vals[s.name];
          return (
            <div key={s.name} className="field" style={{ marginTop: 8 }}>
              <label htmlFor={"sp-in-" + s.name}>
                <code className="inline-code">{s.name}</code> <span className="muted">{s.desc}</span>
              </label>
              <span className="secret-row">
                <input
                  id={"sp-in-" + s.name}
                  ref={k === 0 ? first : null}
                  type={shown[s.name] ? "text" : "password"}
                  autoComplete="off"
                  spellCheck="false"
                  value={vals[s.name] || ""}
                  aria-invalid={bad || undefined}
                  aria-describedby={bad ? "sp-err-" + s.name : undefined}
                  onChange={(e) => setVals({ ...vals, [s.name]: e.target.value })}
                />
                <button
                  type="button"
                  className="btn secondary secret-eye"
                  aria-pressed={!!shown[s.name]}
                  aria-label={(shown[s.name] ? "Hide " : "Show ") + s.name}
                  title={shown[s.name] ? "Hide the value" : "Show the value"}
                  onClick={() => setShown({ ...shown, [s.name]: !shown[s.name] })}
                >
                  {shown[s.name] ? "Hide" : "Show"}
                </button>
              </span>
              {bad && (
                <small id={"sp-err-" + s.name} className="field-error">
                  Enter a value, or choose Don't provide.
                </small>
              )}
            </div>
          );
        })}
        <label className="check" style={{ marginTop: 10 }}>
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />{" "}
          Remember for this session (until the app quits)
        </label>
        {kcOK && (
          <label className="check" style={{ marginTop: 4 }}>
            <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} /> Save in the
            system keychain, so later runs find it without asking
          </label>
        )}
        <div className="row-actions" style={{ marginTop: 12 }}>
          <button className="btn primary" disabled={busy}>
            <Icon.check /> Provide and run
          </button>
          <button type="button" className="btn secondary" onClick={decline}>
            Don't provide
          </button>
        </div>
      </form>
    </div>
  );
}
