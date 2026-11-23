// An AI step's own guidance (flow.Node Instructions, Skills,
// FlowSkillsOff): instructions written for this step and the skill files it
// follows, on top of the flow's context and skills (Flow settings). They go
// into the step's prompt (core aiSuffix) for AI prompts, improve steps, AI
// decisions and transforms, and tool steps whose arguments the AI fills.
import { useEffect, useRef, useState } from "react";
import { S } from "../../store.js";
import { AttachBar } from "../../Attachments.jsx";
import { FileLink } from "../../FileLinks.jsx";
import { fl } from "../Flows.jsx";

// aiStep: the steps whose prompt the AI answers.
export const aiStep = (n) =>
  n.type === "ai" ||
  n.type === "improve" ||
  (n.type === "decision" && n.decide === "ai") ||
  (n.type === "transform" && n.op === "ai") ||
  (n.type === "tool" && !!(n.instruction || "").trim());

export function StepGuidance({ n, set }) {
  const flow = fl().flow;
  const own = n.skills || [];
  const [open, setOpen] = useState(!!((n.instructions || "").trim() || own.length || n.flowSkillsOff));
  const attached = useRef(null);
  attached.current ||= [];
  const library = (S.ws?.libraries || []).filter((l) => l.kind === "skill");
  const has = (p) => own.some((s) => s.path === p);
  const toggle = (l) =>
    set(
      "skills",
      has(l.path) ? own.filter((s) => s.path !== l.path) : [...own, { path: l.path, name: l.name }],
    );
  // a skill file attached here becomes one of the step's skills
  useEffect(() => {
    const add = attached.current.filter((a) => a.path && !a.saving && !has(a.path));
    if (add.length)
      set("skills", [...own, ...add.map((a) => ({ path: a.path, name: a.name.replace(/\.md$/i, "") }))]);
  });
  const flowSkills = flow?.skills || [];
  const count = own.length + ((n.instructions || "").trim() ? 1 : 0);
  return (
    <div className="fl-guidance">
      <button
        type="button"
        className="link fl-guidance-head"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {open ? "▾" : "▸"} Guidance for this step
        {count > 0 && <span className="badge">{count}</span>}
      </button>
      {open && (
        <>
          <label className="field fl-field">
            <span className="muted">
              Instructions{" "}
              <small>· how to answer: tone, format, what to include or avoid; variables work</small>
            </span>
            <textarea
              rows={4}
              value={n.instructions || ""}
              placeholder={
                "e.g. Write for {{inputs.audience}}. Use short bullet points, no marketing language.\nIf something is unclear, say so instead of guessing."
              }
              onChange={(e) => set("instructions", e.target.value)}
            />
          </label>
          <div className="fl-field">
            <span className="muted">Skills for this step</span>
            <ul className="fl-checks">
              {library.map((l) => (
                <li key={l.id || l.path}>
                  <label className="check">
                    <input type="checkbox" checked={has(l.path)} onChange={() => toggle(l)} /> <b>{l.name}</b>{" "}
                    <span className="muted">{l.scope === "global" ? "corporate" : "project"}</span>
                  </label>
                </li>
              ))}
              {own
                .filter((s) => !library.some((l) => l.path === s.path))
                .map((s) => (
                  <li key={s.path}>
                    <label className="check">
                      <input
                        type="checkbox"
                        checked
                        onChange={() =>
                          set(
                            "skills",
                            own.filter((x) => x.path !== s.path),
                          )
                        }
                      />{" "}
                      <FileLink path={s.path}>{s.name || s.path}</FileLink>{" "}
                      <span className="muted">attached</span>
                    </label>
                  </li>
                ))}
              {!library.length && !own.length && (
                <li className="muted">No skills in this project yet: attach a .md file.</li>
              )}
            </ul>
            <AttachBar list={attached.current} />
          </div>
          <label className="check fl-field">
            <input
              type="checkbox"
              checked={!n.flowSkillsOff}
              onChange={(e) => set("flowSkillsOff", !e.target.checked || undefined)}
            />{" "}
            Also follow the flow's skills
            <span className="muted">
              {" "}
              (
              {flowSkills.length
                ? flowSkills.map((s) => s.name || s.path.split("/").pop()).join(", ")
                : "none set"}{" "}
              · Flow settings)
            </span>
          </label>
        </>
      )}
    </div>
  );
}
