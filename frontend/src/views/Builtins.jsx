// SpecWright's built-in agent tools: compiled into the app (nobody can edit
// them), attached to every workflow step and playground prompt as their own
// MCP tools. This view documents each one and lets you try it.
import { backend } from "../backend.js";
import { ContentView } from "../ContentView.jsx";
import { S, update, toast, errMsg } from "../store.js";
import { Icon } from "../ui.jsx";

const GROUPS = [
  ["read", "Read"],
  ["search", "Search"],
  ["edit", "Edit"],
  ["execute", "Run"],
  ["web", "Web"],
  ["plan", "Plan"],
];
const EXAMPLES = {
  read_file: { path: "README.md", start_line: 1, end_line: 40 },
  list_directory: { path: ".", depth: 2 },
  file_search: { pattern: "**/*.java" },
  text_search: { query: "TODO", include: ["**/*.java", "**/*.md"], context_lines: 1 },
  find_usages: { symbol: "Order" },
  workspace_changes: { include_diff: true },
  create_file: { path: "notes/scratch.md", content: "# Scratch\n" },
  create_directory: { path: "notes" },
  edit_file: { path: "notes/scratch.md", old_text: "# Scratch", new_text: "# Scratch pad", dry_run: true },
  move_path: { from: "notes/scratch.md", to: "notes/pad.md" },
  delete_path: { path: "notes/pad.md" },
  run_command: { command: "git log --oneline -5", timeout_seconds: 30 },
  get_command_output: {},
  get_problems: {},
  fetch_webpage: { url: "https://example.com", max_chars: 4000 },
  git: { action: "log", max_count: 10 },
  file_compare: { a: "README.md", b: "README.md" },
  test_runner: { format: "text" },
  export_doc: { path: "README.md", to: "html", out: "exports/readme.html" },
  http_request: { url: "https://example.com", format: "json" },
  notify: { title: "Hello from SpecWright", message: "Built-in notify works" },
  todo_list: {
    items: [
      { title: "Read the spec", status: "done" },
      { title: "Write the tests", status: "in_progress" },
    ],
  },
};

// Built-in tools are listed and tried in admin mode only; regular users never
// see them (agents always get them). The list is reloaded when admin mode
// changes.
export const builtinsVisible = () => !!S.admin?.unlocked;

export async function loadBuiltins() {
  if (!builtinsVisible()) {
    if (S.builtins?.length) {
      S.builtins = null;
      if (S.bt) S.bt = null;
    }
    return;
  }
  if (S.builtins?.length) return;
  if (S.builtins) return; // loading
  S.builtins = [];
  S.builtins = (await backend.BuiltinTools().catch(() => [])) || [];
  update();
}

export function selectBuiltin(name) {
  if (S.cx) S.cx.sel = "";
  S.bt = { sel: name, args: JSON.stringify(EXAMPLES[name] || {}, null, 2), out: "", running: false };
  S.tb = null;
  S.view = "tools";
  update();
}

export function BuiltinList() {
  loadBuiltins();
  if (!builtinsVisible()) return null;
  const list = S.builtins || [];
  return (
    <div>
      <div className="tree-folder">Built-in · part of SpecWright · {list.length}</div>
      {GROUPS.map(([g, l]) => {
        const items = list.filter((t) => t.group === g);
        if (!items.length) return null;
        return (
          <div key={g}>
            <div className="bt-group">{l}</div>
            {items.map((t) => (
              <button
                key={t.name}
                className={"tree-item bt-item" + (S.bt?.sel === t.name ? " active" : "")}
                onClick={() => selectBuiltin(t.name)}
              >
                <code>{t.name}</code>
                {!t.enabled ? (
                  <span className="badge badge-warn">off</span>
                ) : !t.readOnly ? (
                  <span className="badge">writes</span>
                ) : null}
              </button>
            ))}
          </div>
        );
      })}
    </div>
  );
}

async function tryIt() {
  const bt = S.bt;
  let args;
  try {
    args = bt.args.trim() ? JSON.parse(bt.args) : {};
  } catch (e) {
    return toast("Arguments must be JSON: " + e.message);
  }
  bt.running = true;
  bt.out = "";
  update();
  const t0 = performance.now();
  try {
    bt.out = await backend.CallBuiltin(bt.sel, args);
    bt.err = false;
  } catch (e) {
    bt.out = errMsg(e);
    bt.err = true;
  }
  bt.ms = Math.round(performance.now() - t0);
  bt.running = false;
  update();
}

export function BuiltinView() {
  if (!builtinsVisible()) return null;
  const t = (S.builtins || []).find((x) => x.name === S.bt?.sel);
  if (!t) return null;
  const props = Object.entries(t.inputSchema?.properties || {}),
    req = new Set(t.inputSchema?.required || []);
  const bt = S.bt;
  return (
    <>
      <header className="view-head">
        <h1>
          <code>{t.name}</code>{" "}
          <span className="muted" style={{ fontWeight: 400, fontSize: ".75em" }}>
            {t.title}
          </span>
        </h1>
        <span className="badge badge-info">
          <Icon.lock /> built-in
        </span>
      </header>
      <div className="banner admin-banner">
        <Icon.lock /> Built into SpecWright: no one can change it, and every workflow step and playground
        prompt gets it as the MCP tool <code className="inline-code">mcp__agentbridge__{t.name}</code>. Paths
        are confined to the open workspace.
      </div>
      {!t.enabled && (
        <div className="banner error">
          Turned off on this computer by the machine policy (
          <code className="inline-code">disabledBuiltinTools</code>).
        </div>
      )}
      <div className="card">
        <div className="card-head">
          <h3>What the agent is told</h3>
          <span className={"badge " + (t.readOnly ? "badge-ok" : "badge-warn")}>
            {t.readOnly ? "read-only" : "changes the workspace"}
          </span>
          <span className="badge">{t.group}</span>
        </div>
        <p style={{ margin: 0 }}>{t.description}</p>
      </div>
      <div className="card">
        <h3>Parameters</h3>
        {props.length ? (
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Required</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {props.map(([k, v]) => (
                <tr key={k}>
                  <td>
                    <code>{k}</code>
                  </td>
                  <td>
                    {v.type}
                    {v.items?.type ? ` of ${v.items.type}` : ""}
                    {v.enum ? ` (${v.enum.join(" | ")})` : ""}
                  </td>
                  <td>{req.has(k) ? "yes" : ""}</td>
                  <td>{v.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="muted">No parameters.</p>
        )}
      </div>
      <div className="card">
        <div className="card-head">
          <h3>Try it</h3>
          <span className="muted">
            runs in {S.ws ? <code className="inline-code">{S.ws.root}</code> : "the open workspace"}
          </span>
        </div>
        <textarea
          className="editor"
          rows={Math.min(12, Math.max(4, bt.args.split("\n").length + 1))}
          spellCheck="false"
          style={{ minHeight: 90 }}
          value={bt.args}
          onChange={(e) => {
            bt.args = e.target.value;
            update();
          }}
          aria-label="Arguments (JSON)"
        />
        <div className="row-actions">
          <button className="btn primary" disabled={bt.running || !t.enabled || !S.ws} onClick={tryIt}>
            {bt.running ? "Running…" : "Run " + t.name}
          </button>
          <button
            className="link"
            onClick={() => {
              bt.args = JSON.stringify(EXAMPLES[t.name] || {}, null, 2);
              update();
            }}
          >
            Reset example
          </button>
          {!t.readOnly && <span className="muted">This one changes files or runs commands for real.</span>}
          {bt.ms != null && !bt.running && (
            <span className="muted" style={{ marginLeft: "auto" }}>
              {bt.ms} ms
            </span>
          )}
        </div>
        {bt.running ? (
          <pre className="code console-out">…</pre>
        ) : (
          bt.out &&
          (bt.err ? (
            <pre className="code console-out err-text">{bt.out}</pre>
          ) : (
            <ContentView title={bt.sel + " output"} text={bt.out} />
          ))
        )}
      </div>
    </>
  );
}
