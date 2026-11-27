// Ask AI conversations (views/Playground.jsx). A follow-up is a prompt run
// whose first parent is the turn it answers, so a chain of prompt runs is
// one conversation. A run continuing something else (a flow run, a workflow
// step, a run not in the list) starts a conversation of its own.

// conversations groups the prompt runs: root id -> turns, oldest first.
export function conversations(runs) {
  const byId = new Map(runs.map((r) => [r.id, r]));
  const rootOf = (r) => {
    let x = r;
    for (let n = 0; n < 200; n++) {
      const up = byId.get(x.parents?.[0]);
      if (!up || up.kind === "flow") return x.id;
      x = up;
    }
    return x.id;
  };
  const out = new Map();
  for (const r of runs) {
    if (r.kind === "flow") continue;
    const root = rootOf(r);
    if (!out.has(root)) out.set(root, []);
    out.get(root).push(r);
  }
  for (const turns of out.values()) turns.sort((a, b) => String(a.at).localeCompare(String(b.at)));
  return out;
}

// contextWindow: about how many tokens a model can hold at once. A guess
// from its name (the CLIs do not report it); unknown models get 200k.
export function contextWindow(model = "") {
  const m = String(model).toLowerCase();
  if (/gemini/.test(m)) return 1_000_000;
  if (/gpt-4\.1|\[1m\]|-1m\b/.test(m)) return 1_000_000;
  if (/gpt-5|o3|o4/.test(m)) return 400_000;
  if (/gpt-4o|gpt-4-turbo/.test(m)) return 128_000;
  return 200_000;
}

// contextBlock: what the person added to a question, as text for the AI;
// "" when nothing was added. items: [{kind: file|folder|url, value}]
export function contextBlock(items = []) {
  if (!items.length) return "";
  const line = (x) =>
    x.kind === "url"
      ? `- Web page: ${x.value} (fetch it with fetch_webpage)`
      : x.kind === "folder"
        ? `- Folder: ${x.value}/ (look through what is in it)`
        : `- File: ${x.value}`;
  return (
    "\n\n---\nThe person added these to the conversation; read or fetch them before you answer:\n" +
    items.map(line).join("\n")
  );
}

// savedVersionOf: the flow version a save_flow result reports (core:
// flowsave_agent.go), or null for a new flow or an update.
export function savedVersionOf(text = "") {
  const m = /Saved the flow "(.*?)" \(id ([\w.-]+),.*?version (\d+) of ([\w.-]+)/s.exec(String(text));
  return m ? { name: m[1], id: m[2], rev: m[3], parent: m[4] } : null;
}
