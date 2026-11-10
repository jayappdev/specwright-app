// Icons for run events (Activity and Debug tabs): one small, colored glyph
// per kind of event, so a log reads at a glance — reading, editing,
// searching, running commands, AI requests and answers, files, reviews,
// flow steps, outputs, errors.

const g = (d) => <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{d}</svg>;

const I = {
  read: g(<><path d="M4 2h5l3 3v9H4z" /><path d="M6 8h4M6 10.5h4" /></>),
  edit: g(<><path d="M10.5 2.5l3 3-7.5 7.5H3v-3z" /><path d="M9 4l3 3" /></>),
  search: g(<><circle cx="7" cy="7" r="4" /><path d="M10 10l3.5 3.5" /></>),
  terminal: g(<><rect x="1.5" y="2.5" width="13" height="11" rx="2" /><path d="M4.5 6.5l2 1.5-2 1.5M8 10h3.5" /></>),
  web: g(<><circle cx="8" cy="8" r="6" /><path d="M2 8h12M8 2c1.8 2 2.6 4 2.6 6S9.8 12 8 14M8 2C6.2 4 5.4 6 5.4 8s.8 4 2.6 6" /></>),
  tool: g(<path d="M9.8 4.2a2.7 2.7 0 00-3.5 3.5L2.6 11.4a1 1 0 001.4 1.4l3.7-3.7a2.7 2.7 0 003.5-3.5l-1.6 1.6-1.4-.4-.4-1.4z" />),
  pkg: g(<><path d="M8 1.8l5.5 3v6.4L8 14.2l-5.5-3V4.8z" /><path d="M2.5 4.8L8 7.8l5.5-3M8 7.8v6.4" /></>),
  folder: g(<path d="M1.8 4.2h4l1.2 1.5h7.2v7.1H1.8z" />),
  plan: g(<><path d="M5.5 4h8M5.5 8h8M5.5 12h8" /><path d="M2.5 4h.01M2.5 8h.01M2.5 12h.01" /></>),
  ai: g(<path d="M8 1.8l1.5 4.2 4.2 1.5-4.2 1.5L8 13.2 6.5 9 2.3 7.5 6.5 6z" />),
  say: g(<path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" />),
  up: g(<path d="M8 13V3M4 7l4-4 4 4" />),
  down: g(<path d="M8 3v10M4 9l4 4 4-4" />),
  ok: g(<path d="M3.5 8.5l3 3 6-7" />),
  fail: g(<path d="M4.5 4.5l7 7M11.5 4.5l-7 7" />),
  fileAdd: g(<><path d="M4 2h5l3 3v9H4z" /><path d="M8 7v4M6 9h4" /></>),
  fileDel: g(<><path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.7 9h5.6l.7-9" /></>),
  gate: g(<><path d="M8 1.8l5 1.8v4.2c0 3.1-2.1 5.6-5 6.4-2.9-.8-5-3.3-5-6.4V3.6z" /><path d="M6 8l1.5 1.5L10.5 6.5" /></>),
  pause: g(<><rect x="4" y="3" width="2.5" height="10" rx=".8" /><rect x="9.5" y="3" width="2.5" height="10" rx=".8" /></>),
  play: g(<path d="M4.5 3l9 5-9 5z" />),
  out: g(<><path d="M8 2.5v8M4.5 7l3.5 3.5L11.5 7" /><path d="M2.5 11.5v2h11v-2" /></>),
  save: g(<><path d="M3 2.5h8l2 2v9H3z" /><path d="M5.5 2.5v3h5v-3M5.5 13.5v-4h5v4" /></>),
  repeat: g(<><path d="M3 7V5.5A2.5 2.5 0 015.5 3H12l-2-2M13 9v1.5A2.5 2.5 0 0110.5 13H4l2 2" /></>),
  flag: g(<><path d="M3.5 14V2.5M3.5 3h8l-2 2.5 2 2.5h-8" /></>),
  gauge: g(<><path d="M2.5 11a5.5 5.5 0 1111 0" /><path d="M8 11l3-3" /></>),
  warn: g(<><path d="M8 2.5l6 10.5H2z" /><path d="M8 6.8v2.8M8 11.6v.1" /></>),
  key: g(<><circle cx="5.5" cy="10.5" r="3" /><path d="M7.8 8.2L13.5 2.5M11 5l1.5 1.5M12.5 3.5L14 5" /></>),
  doc: g(<><path d="M4 2h5l3 3v9H4z" /><path d="M9 2v3h3" /></>),
  build: g(<><path d="M10.5 2.5l3 3-8.5 8.5H2v-3z" /></>),
  clock: g(<><circle cx="8" cy="8" r="6" /><path d="M8 4.5V8l2.5 1.5" /></>),
  dot: g(<circle cx="8" cy="8" r="2.5" fill="currentColor" />),
};

const COLOR = { read: "--info", edit: "--warn", search: "--syn-property", terminal: "--muted", web: "--ansi-cyan", tool: "--syn-function", pkg: "--syn-function", folder: "--info",
  plan: "--muted", ai: "--btn-bg", say: "--fg", up: "--info", down: "--info", ok: "--ok", fail: "--error", fileAdd: "--ok", fileDel: "--error", gate: "--warn", pause: "--warn",
  play: "--activity-accent", out: "--warn", save: "--ok", repeat: "--muted", flag: "--muted", gauge: "--muted", warn: "--error", key: "--warn", doc: "--syn-type", build: "--warn", clock: "--muted", dot: "--muted" };

// The glyph for a tool name.
export function toolGlyph(tool = "") {
  const t = String(tool).replace(/^mcp__agentbridge__|^agentbridge-/, "");
  if (/extension_managed$/.test(t)) return "pkg";
  if (/^(read_file|Read|view|NotebookRead)$/i.test(t)) return "read";
  if (/^(edit_file|Edit|MultiEdit|Write|create_file|create_directory|move_path|str_replace|NotebookEdit)$/i.test(t)) return "edit";
  if (/^(delete_path)$/i.test(t)) return "fileDel";
  if (/^(text_search|Grep|grep|find_usages|file_search|Glob|glob|ToolSearch)$/i.test(t)) return "search";
  if (/^(list_directory|LS|ls)$/i.test(t)) return "folder";
  if (/^(run_command|Bash|bash|shell|get_command_output|command)$/i.test(t)) return "terminal";
  if (/^(get_problems)$/i.test(t)) return "build";
  if (/^(fetch_webpage|WebFetch|WebSearch|web_fetch)$/i.test(t)) return "web";
  if (/^(todo_list|TodoWrite)$/i.test(t)) return "plan";
  return "tool";
}

// The glyph for an event.
export function eventGlyph(e) {
  switch (e.type) {
    case "tool.call": return toolGlyph(e.tool);
    case "tool.result": case "builtin.result": return e.ok === false ? "fail" : "ok";
    case "ext.result": return e.ok === false || e.exit_code ? "fail" : "pkg";
    case "model.request": return "up";
    case "model.response": return "down";
    case "model.text": return "say";
    case "model.usage": case "run.usage": return "gauge";
    case "fs.write": return "fileAdd";
    case "fs.edit": return "edit";
    case "fs.delete": return "fileDel";
    case "gate.wait": case "flow.review": return "pause";
    case "gate.approved": return "ok";
    case "gate.rejected": return "fail";
    case "gate.changes": return "ai";
    case "flow.step": return e.status === "failed" ? "fail" : e.status === "done" ? "ok" : "play";
    case "flow.output": return "out";
    case "flow.saved": return "save";
    case "flow.pass": return "repeat";
    case "flow.scheduled": return "clock";
    case "flow.state": return "dot";
    case "run.start": case "run.end": return "flag";
    case "run.retry": return "repeat";
    case "prompt.compose": return "doc";
    case "secret.request": case "secret.resolved": return "key";
    case "loop.detected": case "budget.exceeded": case "error": return "warn";
    case "build.start": case "build.line": case "build.end": return "build";
  }
  return e.type?.startsWith("ext.") ? "pkg" : "dot";
}

export function EventIcon({ e, glyph }) {
  const k = glyph || eventGlyph(e);
  return <span className={"ev-ic ev-" + k} style={{ "--ec": `var(${COLOR[k] || "--muted"})` }}>{I[k] || I.dot}</span>;
}
