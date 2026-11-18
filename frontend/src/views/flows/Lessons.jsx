// Flow Designer › an AI or Improve step › Lessons: what people asked for
// when they reviewed this step (Refine feedback, "another round" in an
// Improve step), kept on this computer and added to the step's prompt in
// later runs (core/lessons.go). They can be edited, turned off, deleted,
// or added by hand.
import { useEffect, useState } from "react";
import { backend } from "../../backend.js";
import { toast, errMsg } from "../../store.js";
import { fl } from "../Flows.jsx";
import { ago } from "../../time.js";

const FROM = {
  refine: "from a Refine review",
  improve: "from an Improve review",
  edit: "from your edit of its result",
  judge: "from the AI judge",
  manual: "added by hand",
};

export function LessonsEditor({ n }) {
  const flow = fl().flow;
  const [list, setList] = useState(null);
  const [text, setText] = useState("");
  const load = () =>
    backend
      .FlowLessons(flow.id)
      .then((l) => setList((l || []).filter((x) => x.step === n.id)))
      .catch(() => setList([]));
  useEffect(() => {
    load();
  }, [flow.id, n.id]);
  const act = async (p, msg) => {
    try {
      await p;
      if (msg) toast(msg);
    } catch (e) {
      toast(errMsg(e), "error");
    }
    load();
  };
  if (!list) return null;
  const on = list.filter((l) => !l.off).length;
  return (
    <details className="fl-lessons" open={list.length > 0 && list.length <= 3}>
      <summary>
        Lessons from reviews{" "}
        <span className="muted">
          · {list.length ? `${on} of ${list.length} given to the AI` : "none yet"}
        </span>
      </summary>
      <p className="muted fl-help">
        When you ask for changes while reviewing this step, or correct its result by hand, it is kept here and
        added to its prompt in later runs; so is the AI judge&apos;s first &quot;not good enough&quot; in an
        Improve step. Small on purpose: up to 8 that are on, about 1,500 characters in all, yours first; each
        step keeps the newest 3 of each learned kind. Kept on this computer, not in the flow file.
      </p>
      {list.map((l) => (
        <div key={l.id} className={"fl-lesson" + (l.off ? " off" : "")}>
          <textarea
            rows={2}
            defaultValue={l.text}
            aria-label="Lesson"
            onBlur={(e) =>
              e.target.value.trim() !== l.text && act(backend.UpdateLesson(l.id, e.target.value, l.off))
            }
          />
          <div className="fl-lesson-meta muted">
            <span>
              {FROM[l.source] || l.source} · {ago(l.created)}
              {l.used ? ` · used ${l.used}×` : ""}
            </span>
            <label className="check">
              <input
                type="checkbox"
                checked={!l.off}
                onChange={(e) => act(backend.UpdateLesson(l.id, l.text, !e.target.checked))}
              />{" "}
              On
            </label>
            <button
              className="link danger-text"
              aria-label={`Delete the lesson ${l.text}`}
              onClick={() => act(backend.DeleteLesson(l.id))}
            >
              Delete
            </button>
          </div>
        </div>
      ))}
      <div className="fl-lesson-add">
        <input
          value={text}
          placeholder="Add one, e.g. Keep the summary under 200 words"
          aria-label="New lesson"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) =>
            e.key === "Enter" && text.trim() && (act(backend.AddFlowLesson(flow.id, n.id, text)), setText(""))
          }
        />
        <button
          className="btn secondary"
          disabled={!text.trim()}
          onClick={() => {
            act(backend.AddFlowLesson(flow.id, n.id, text));
            setText("");
          }}
        >
          Add
        </button>
      </div>
    </details>
  );
}
