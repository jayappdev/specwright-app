// A small "the AI is working" indicator: an animated ring, a short phrase
// for what is happening now ("Thinking", "Reading OrderStatus.java",
// "Running mvn"), the elapsed time, and a dot per tool call. It stays
// quiet on purpose: one line, a few words.
//
//   <AiProgress evs={events} />           live runs: the phrase follows the events
//   <AiProgress phases={["…", "…"]} />    calls without events: phrases rotate
import { useEffect, useRef, useState } from "react";

const base = (p) =>
  String(p || "")
    .split(/[\\/]/)
    .filter(Boolean)
    .pop() || "";
const short = (s, n = 26) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

// The phrase for a tool call, from the tool's name and arguments.
function toolPhrase(e) {
  const t = String(e.tool || "").replace(/^mcp__agentbridge__|^agentbridge-/, "");
  const a = e.args || {};
  const file = base(
    a.path || a.file_path || a.filePath || a.from || (Array.isArray(a.paths) ? a.paths[0] : ""),
  );
  if (/extension_managed$/.test(t)) return "Using " + short(a.name || "a project tool");
  if (/^(read_file|Read|view|NotebookRead)$/i.test(t))
    return file ? "Reading " + short(file) : "Reading files";
  if (/^(edit_file|Edit|MultiEdit|Write|create_file|str_replace|write|NotebookEdit)$/i.test(t))
    return file ? "Editing " + short(file) : "Editing files";
  if (/^(text_search|Grep|grep|find_usages)$/i.test(t))
    return a.query || a.pattern || a.symbol
      ? "Searching “" + short(String(a.query || a.pattern || a.symbol), 18) + "”"
      : "Searching";
  if (/^(file_search|Glob|glob)$/i.test(t)) return "Finding files";
  if (/^(list_directory|LS|ls)$/i.test(t)) return "Looking around";
  if (/^(run_command|Bash|bash|shell)$/i.test(t)) {
    const w = String(a.command || "")
      .trim()
      .split(/\s+/)[0];
    return w ? "Running " + short(base(w), 18) : "Running a command";
  }
  if (/^(get_command_output)$/i.test(t)) return "Watching a command";
  if (/^(get_problems)$/i.test(t)) return "Compiling";
  if (/^(fetch_webpage|WebFetch|web_fetch|WebSearch)$/i.test(t)) return "Browsing the web";
  if (/^(todo_list|TodoWrite)$/i.test(t)) return "Planning";
  if (/^(workspace_changes)$/i.test(t)) return "Reviewing changes";
  if (/^ToolSearch$/i.test(t)) return "Finding tools";
  if (/^(Task|Agent)$/i.test(t)) return "Delegating";
  return "Using " + short(t || "a tool", 20);
}

// What is happening now, from the most recent meaningful event.
export function phaseOf(evs = []) {
  for (let i = evs.length - 1; i >= 0; i--) {
    const e = evs[i];
    switch (e.type) {
      case "tool.call":
        return evs.slice(i + 1).some((x) => x.type === "tool.result") ? "Thinking" : toolPhrase(e);
      case "model.request":
      case "tool.result":
      case "builtin.result":
      case "ext.result":
        return "Thinking";
      case "model.text":
        return "Writing";
      case "fs.write":
      case "fs.edit":
        return "Editing " + short(base(e.path || e.summary));
      case "run.retry":
        return "Retrying";
      case "run.start":
        return "Starting";
      case "ext.install":
      case "ext.install.line":
        return "Installing packages";
      case "secret.request":
      case "permission.request":
        return "Waiting for you";
    }
  }
  return "Starting";
}

const fmt = (s) => (s < 60 ? s + "s" : Math.floor(s / 60) + "m " + String(s % 60).padStart(2, "0") + "s");

export function AiProgress({ evs, phases, compact, className = "" }) {
  const start = useRef(Date.now()),
    [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  // count from the run's first event when there is one (survives switching screens)
  const t0 = evs?.length && evs[0].ts ? Date.parse(evs[0].ts) || start.current : start.current;
  const secs = Math.max(0, Math.round((now - t0) / 1000));
  const phase = evs
    ? phaseOf(evs)
    : phases?.length
      ? phases[Math.min(phases.length - 1, Math.floor(secs / 4))]
      : "Thinking";
  const tools = evs ? evs.filter((e) => e.type === "tool.call").length : 0;
  return (
    <div
      className={"ai-progress" + (compact ? " compact" : "") + " " + className}
      role="status"
      aria-live="polite"
      aria-label={phase}
    >
      <span className="ai-orb" aria-hidden="true">
        <i />
      </span>
      <span key={phase} className="ai-phase">
        {phase}
      </span>
      <span className="ai-dots" aria-hidden="true">
        <b />
        <b />
        <b />
      </span>
      {!compact && tools > 0 && (
        <span className="ai-tools" title={`${tools} tool call${tools === 1 ? "" : "s"}`} aria-hidden="true">
          {Array.from({ length: Math.min(tools, 12) }, (_, k) => (
            <i key={k} />
          ))}
          {tools > 12 && <em>+{tools - 12}</em>}
        </span>
      )}
      <span className="ai-time">{fmt(secs)}</span>
    </div>
  );
}
