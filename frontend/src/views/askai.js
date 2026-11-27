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
