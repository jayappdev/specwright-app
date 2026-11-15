// Asks whether the AI may use a tool it has no standing permission for
// (a "permission.request": Claude's WebFetch, a shell tool, an MCP tool that
// is not pre-allowed). The agent waits until you answer. Allow once lets this
// call through; Allow for this run stops asking about the same tool until the
// run ends; Deny tells the AI, with your note, so it can take another route.
import { useEffect, useRef, useState } from "react";
import { backend } from "./backend.js";
import { S, update, toast, errMsg } from "./store.js";
import { Icon } from "./ui.jsx";
import { CopyButton } from "./CopyButton.jsx";

const KIND = [
  [/^(Bash|run_command|.*shell.*)$/i, "run a command", "warn"],
  [
    /^(Write|Edit|MultiEdit|NotebookEdit|create_file|edit_file|delete_path|move_path)$/i,
    "change files",
    "warn",
  ],
  [/^(WebFetch|WebSearch|fetch_webpage)$/i, "use the web", "info"],
  [/^mcp__/i, "use a connected tool", "info"],
];
const kindOf = (tool) => KIND.find(([re]) => re.test(tool)) || [null, "use a tool", "info"];

export function PermissionPrompt() {
  const req = S.permAsks[0];
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const allowBtn = useRef(null);
  useEffect(() => {
    setNote("");
    setBusy(false);
    setShowAll(false);
    setTimeout(() => allowBtn.current?.focus(), 30);
  }, [req?.id]);
  if (!req) return null;
  const [, verb, tone] = kindOf(req.tool);
  const done = () => {
    S.permAsks = S.permAsks.filter((r) => r.id !== req.id);
    update();
  };
  const answer = async (decision) => {
    setBusy(true);
    try {
      await backend.AnswerPermission(req.id, decision, decision === "deny" ? note : "");
    } catch (err) {
      toast(errMsg(err), "error");
    }
    done();
  };
  const where =
    req.run && S.flowRuns[req.run] ? (
      <>
        The flow <b>{S.flowRuns[req.run].flowName}</b> is waiting.
      </>
    ) : req.step && req.step !== "playground" ? (
      <>
        The <b>{req.step}</b> step is waiting.
      </>
    ) : (
      <>The AI is waiting for your answer.</>
    );
  const input = JSON.stringify(req.input || {}, null, 2);
  return (
    <div
      className="modal-scrim"
      role="presentation"
      onKeyDown={(e) => e.key === "Escape" && !busy && answer("deny")}
    >
      <div
        className={"card modal perm-modal perm-" + tone}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="pp-title"
        aria-describedby="pp-desc"
      >
        <div className="perm-head">
          <span className="perm-icon" aria-hidden="true">
            {tone === "warn" ? <Icon.warn /> : <Icon.shield />}
          </span>
          <div>
            <h3 id="pp-title">Allow the AI to {verb}?</h3>
            <p id="pp-desc" className="muted">
              {where}{" "}
              {S.permAsks.length > 1 && <span className="badge">{S.permAsks.length - 1} more waiting</span>}
            </p>
          </div>
        </div>
        <div className="perm-what">
          <code className="perm-tool">{req.tool}</code>
          {req.target && (
            <div className="perm-target-row">
              <pre className="perm-target">{req.target}</pre>
              <CopyButton text={req.target} />
            </div>
          )}
          {input.length > 2 && input !== JSON.stringify({ command: req.target }, null, 2) && (
            <button type="button" className="link-btn" onClick={() => setShowAll(!showAll)}>
              {showAll ? "Hide details" : "Show all details"}
            </button>
          )}
          {showAll && <pre className="perm-input">{input}</pre>}
        </div>
        <label className="field perm-note">
          <span className="muted">If you deny, tell the AI why or what to do instead (optional)</span>
          <input
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="e.g. Use the local copy in docs/ instead"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                answer("deny");
              }
            }}
          />
        </label>
        <div className="perm-actions">
          <button ref={allowBtn} className="btn primary" disabled={busy} onClick={() => answer("once")}>
            <Icon.check /> Allow once
          </button>
          <button
            className="btn secondary"
            disabled={busy}
            onClick={() => answer("run")}
            title={"Don't ask again about " + req.tool + " until this run ends"}
          >
            Allow for this run
          </button>
          <span className="spacer" />
          <button className="btn danger" disabled={busy} onClick={() => answer("deny")}>
            <Icon.x /> Deny
          </button>
        </div>
      </div>
    </div>
  );
}
