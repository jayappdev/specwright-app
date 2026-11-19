// The tool builder's history: every time the AI wrote, changed or fixed a
// draft (what it was sent, its reply, what it said it changed, the script
// before and after) and the test after it. The history goes with every
// later revision, so the AI does not repeat a fix that failed, and to the
// Prompt Playground, to keep improving the tool in a conversation.

const MAX_DIFF_CELLS = 4_000_000;

// lineDiff: the lines of b against a, each " " (same), "-" (only in a) or
// "+" (only in b). Longest common subsequence; very large inputs fall back
// to all of a removed, all of b added.
export function lineDiff(a, b) {
  const x = String(a || "").split("\n"),
    y = String(b || "").split("\n");
  if (a === b) return x.map((s) => ({ t: " ", s }));
  if (x.length * y.length > MAX_DIFF_CELLS)
    return [...x.map((s) => ({ t: "-", s })), ...y.map((s) => ({ t: "+", s }))];
  const n = x.length,
    m = y.length;
  const L = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      L[i][j] = x[i] === y[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  const out = [];
  let i = 0,
    j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) {
      out.push({ t: " ", s: x[i] });
      i++;
      j++;
    } else if (L[i + 1][j] >= L[i][j + 1]) out.push({ t: "-", s: x[i++] });
    else out.push({ t: "+", s: y[j++] });
  }
  while (i < n) out.push({ t: "-", s: x[i++] });
  while (j < m) out.push({ t: "+", s: y[j++] });
  return out;
}

// diffCount: lines added and removed.
export function diffCount(d) {
  return { add: d.filter((l) => l.t === "+").length, del: d.filter((l) => l.t === "-").length };
}

// testText: a test result in one line, for the AI.
export function testText(t) {
  if (!t || t.running) return "";
  if (t.pass) return "passed";
  const failed = (t.checks || []).filter((c) => !c.ok).map((c) => c.label);
  return ["failed", failed.length ? failed.join("; ") : "", t.summary || ""].filter(Boolean).join(": ");
}

// genTurns: the history as the backend's GenTurn list (attempts only).
export function genTurns(history) {
  return (history || [])
    .filter((h) => h.kind !== "test")
    .map((h) => ({ revision: h.revision || 0, ask: h.ask || "", why: h.why || "", test: h.testText || "" }));
}

// The draft without what the AI has no use for.
export function draftForAI(d) {
  if (!d) return {};
  const { lastTest: _l, hash: _h, approved: _a, approvedAt: _at, tree: _t, metaAt: _m, ...rest } = d;
  return rest;
}

const KIND_WORD = {
  generate: "Wrote the tool",
  change: "Changed on request",
  fix: "Fix attempt",
  playground: "Applied from the Prompt Playground",
  test: "Test",
};
export const kindWord = (h) => KIND_WORD[h.kind] + (h.kind === "fix" && h.attempt ? " " + h.attempt : "");

// playgroundPrompt: the tool, what happened to it, its last test and what
// the developer wants, as a Prompt Playground conversation's first prompt.
export function playgroundPrompt(draft, history, test, question, ask) {
  const d = draftForAI(draft);
  const lines = [
    `Help me improve the SpecWright tool "${d.name || "new tool"}". Talk it through with me: you may read the project's files and try commands to find the cause.`,
    `When you change the tool, end your answer with the WHOLE revised tool as one JSON object in a \`\`\`json fence, in the same shape as below (name, desc, lang, command, script, params, secrets, deps), plus "why": what you changed and why. I will apply it to the tool and test it again.`,
    "",
    `## The tool now (revision ${d.revision || 1})`,
    "```json",
    JSON.stringify(d, null, 2),
    "```",
  ];
  const turns = (history || []).filter((h) => h.kind !== "test");
  if (turns.length) {
    lines.push("", "## What happened so far (oldest first)");
    for (const h of turns.slice(-10))
      lines.push(
        `- ${kindWord(h)} → revision ${h.revision}` +
          (h.ask ? `; asked: ${h.ask}` : "") +
          (h.why ? `; the AI changed: ${h.why}` : "") +
          (h.testText ? `; test after it: ${h.testText}` : ""),
      );
  }
  if (test && !test.running) {
    lines.push(
      "",
      "## The last test",
      `Question the agent was asked: ${question || "(none)"}`,
      `Result: ${testText(test)}`,
    );
    for (const s of test.steps || [])
      if (["call", "run", "out", "install"].includes(s.k))
        lines.push(
          `${{ call: "Tool call", run: "Command", out: "Output", install: "Install log" }[s.k]}:`,
          String(s.v).slice(0, 3000),
        );
  }
  lines.push(
    "",
    "## What I want",
    (ask || "").trim() ||
      (test && !test.pass
        ? "Find out why the test fails and fix it."
        : "Suggest how to make this tool better."),
  );
  return lines.join("\n");
}
