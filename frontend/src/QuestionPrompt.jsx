// ask_user (core/questions.go): multiple-choice questions an agent or a
// flow step asks you. Shown on every page while they wait; the
// recommended answers are chosen already, you change what you want (or
// write your own where allowed) and send. The call waits until you answer,
// go with the recommendations, or its time runs out.
import { useEffect, useState } from "react";
import { backend } from "./backend.js";
import { S, update, toast, errMsg } from "./store.js";
import { Logo } from "./Logo.jsx";

// the recommendations as the starting answers
const initial = (qs) =>
  Object.fromEntries(
    (qs || []).map((q) => [
      q.id,
      { selected: [...(q.recommended?.length ? q.recommended : [])], other: "", useOther: false },
    ]),
  );

export async function loadPendingQuestions() {
  const p = (await backend.PendingQuestions?.().catch(() => [])) || [];
  for (const q of p)
    if (!S.questionAsks.some((x) => x.id === q.id))
      S.questionAsks.push({ ...q, run: q.run_id || "", deadline: q.deadline });
  update();
}

function left(deadline) {
  const ms = new Date(deadline) - Date.now();
  if (!(ms > 0)) return "";
  const m = Math.floor(ms / 60000),
    s = Math.floor((ms % 60000) / 1000);
  return m >= 1 ? `${m} min` : `${s} s`;
}

export function QuestionPrompt() {
  const req = S.questionAsks[0];
  const [ans, setAns] = useState({});
  const [busy, setBusy] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    setAns(initial(req?.questions));
    setBusy(false);
  }, [req?.id]);
  useEffect(() => {
    if (!req) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [req?.id]);
  if (!req) return null;
  const done = () => {
    S.questionAsks = S.questionAsks.filter((r) => r.id !== req.id);
    update();
  };
  const send = async (recommended) => {
    setBusy(true);
    const answers = (req.questions || []).map((q) => {
      const a = recommended ? initial([q])[q.id] : ans[q.id] || { selected: [] };
      return { id: q.id, selected: a.selected, other: a.useOther ? a.other : "" };
    });
    try {
      await backend.AnswerQuestions(req.id, answers, !!recommended);
    } catch (e) {
      toast(errMsg(e), "error");
    }
    done();
  };
  const set = (q, a) => setAns({ ...ans, [q.id]: { ...(ans[q.id] || { selected: [] }), ...a } });
  const unanswered = (req.questions || []).filter((q) => {
    const a = ans[q.id];
    return !a || (!a.selected.length && !(a.useOther && a.other.trim()));
  });
  const flow = S.flowRuns[req.run];
  return (
    <div className="modal-scrim" role="presentation">
      <form
        className="card modal question-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="qp-title"
        onSubmit={(e) => {
          e.preventDefault();
          if (!unanswered.length) send(false);
        }}
      >
        <div className="card-head">
          <Logo size={28} />
          <h3 id="qp-title">{req.title || "Questions for you"}</h3>
          {S.questionAsks.length > 1 && (
            <span className="badge">{S.questionAsks.length - 1} more waiting</span>
          )}
        </div>
        <p className="wf-desc">
          {flow ? (
            <>
              The flow <b>{flow.flowName}</b> waits at <b>{req.step}</b> for your answers.{" "}
            </>
          ) : req.step ? (
            <>
              The agent asks during <b>{req.step}</b>.{" "}
            </>
          ) : null}
          The recommended answers are chosen already.
          {req.deadline && left(req.deadline) && (
            <> Without an answer in {left(req.deadline)}, the recommendations are taken.</>
          )}
        </p>
        {req.intro && <p>{req.intro}</p>}
        <div className="qp-list">
          {(req.questions || []).map((q, i) => {
            const a = ans[q.id] || { selected: [], other: "", useOther: false };
            const pick = (o) => {
              if (q.multiple)
                set(q, {
                  selected: a.selected.includes(o) ? a.selected.filter((x) => x !== o) : [...a.selected, o],
                });
              else set(q, { selected: [o], useOther: false });
            };
            return (
              <fieldset key={q.id} className="qp-q">
                <legend>
                  <span className="qp-n">{i + 1}</span> {q.question}
                  {q.multiple && <span className="muted"> · choose any</span>}
                </legend>
                {(q.options || []).map((o) => {
                  const rec = (q.recommended || []).includes(o);
                  return (
                    <label key={o} className={"qp-opt" + (a.selected.includes(o) ? " on" : "")}>
                      <input
                        type={q.multiple ? "checkbox" : "radio"}
                        name={"qp-" + req.id + "-" + q.id}
                        checked={a.selected.includes(o)}
                        onChange={() => pick(o)}
                      />
                      <span>{o}</span>
                      {rec && <span className="badge badge-ok">Recommended</span>}
                    </label>
                  );
                })}
                {q.why && (
                  <p className="muted qp-why">
                    <b>Why the recommendation:</b> {q.why}
                  </p>
                )}
                {q.allowOther && (
                  <label className="qp-opt qp-other">
                    <input
                      type={q.multiple ? "checkbox" : "radio"}
                      name={"qp-" + req.id + "-" + q.id}
                      checked={!!a.useOther}
                      onChange={() =>
                        set(q, q.multiple ? { useOther: !a.useOther } : { useOther: true, selected: [] })
                      }
                    />
                    <span>Other:</span>
                    <input
                      type="text"
                      value={a.other || ""}
                      placeholder="Your own answer"
                      aria-label={"Your own answer to: " + q.question}
                      onChange={(e) =>
                        set(q, {
                          other: e.target.value,
                          useOther: true,
                          ...(q.multiple ? {} : { selected: [] }),
                        })
                      }
                    />
                  </label>
                )}
              </fieldset>
            );
          })}
        </div>
        {unanswered.length > 0 && (
          <p className="muted">
            Not answered yet: {unanswered.map((q) => (req.questions || []).indexOf(q) + 1).join(", ")}
          </p>
        )}
        <div className="row-actions">
          <button className="btn primary" type="submit" disabled={busy || unanswered.length > 0}>
            Send answers
          </button>
          <button className="btn secondary" type="button" disabled={busy} onClick={() => send(true)}>
            Use the recommendations
          </button>
        </div>
      </form>
    </div>
  );
}
