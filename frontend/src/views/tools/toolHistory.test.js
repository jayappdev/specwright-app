import { describe, expect, it } from "vitest";
import {
  lineDiff,
  diffCount,
  testText,
  genTurns,
  draftForAI,
  playgroundPrompt,
  proposedTool,
} from "./toolHistory.js";

describe("tool history", () => {
  it("diffs lines", () => {
    const d = lineDiff("a\nb\nc", "a\nB\nc\nd");
    expect(d).toEqual([
      { t: " ", s: "a" },
      { t: "-", s: "b" },
      { t: "+", s: "B" },
      { t: " ", s: "c" },
      { t: "+", s: "d" },
    ]);
    expect(diffCount(d)).toEqual({ add: 2, del: 1 });
    expect(diffCount(lineDiff("x", "x"))).toEqual({ add: 0, del: 0 });
  });

  it("says how a test went", () => {
    expect(testText({ pass: true })).toBe("passed");
    expect(
      testText({
        pass: false,
        summary: "exit 1",
        checks: [
          { label: "Exit code 0", ok: false },
          { label: "Picked", ok: true },
        ],
      }),
    ).toBe("failed: Exit code 0: exit 1");
    expect(testText({ running: true })).toBe("");
  });

  it("sends attempts, not tests, as history", () => {
    const h = [
      { kind: "generate", revision: 1, ask: "find todos", testText: "failed: x" },
      { kind: "test", revision: 1 },
      { kind: "fix", revision: 2, why: "quoted", testText: "passed" },
    ];
    expect(genTurns(h)).toEqual([
      { revision: 1, ask: "find todos", why: "", test: "failed: x" },
      { revision: 2, ask: "", why: "quoted", test: "passed" },
    ]);
  });

  it("builds the Playground prompt", () => {
    const d = { name: "find-todos", script: "s", revision: 3, lastTest: { pass: false }, hash: "h" };
    expect(draftForAI(d)).toEqual({ name: "find-todos", script: "s", revision: 3 });
    const p = playgroundPrompt(
      d,
      [{ kind: "fix", attempt: 1, revision: 3, why: "quoted", testText: "failed: Exit code 0" }],
      {
        pass: false,
        checks: [{ label: "Exit code 0", ok: false }],
        steps: [{ k: "out", v: "No such file" }],
      },
      "list todos",
      "the folder has spaces",
    );
    expect(p).toContain('SpecWright tool "find-todos"');
    expect(p).toContain("Fix attempt 1 → revision 3; the AI changed: quoted");
    expect(p).toContain("No such file");
    expect(p).toMatch(/## What I want\nthe folder has spaces$/);
    expect(p).not.toContain('"hash"');
  });
});

describe("proposedTool", () => {
  it("takes the last save_tool call that worked", () => {
    const evs = [
      {
        type: "builtin.result",
        name: "save_tool",
        ok: true,
        args: { tool: { name: "a", script: "1" }, why: "first" },
      },
      { type: "builtin.result", name: "save_tool", ok: false, args: { tool: { name: "a", script: "bad" } } },
      { type: "builtin.result", name: "read_file", ok: true, args: {} },
      {
        type: "builtin.result",
        name: "save_tool",
        ok: true,
        args: { tool: '{"name":"a","script":"2"}', why: "second" },
      },
    ];
    expect(proposedTool(evs)).toEqual({ tool: { name: "a", script: "2" }, why: "second" });
    expect(proposedTool(evs.slice(0, 3))).toEqual({ tool: { name: "a", script: "1" }, why: "first" });
    expect(proposedTool([])).toBeNull();
  });
});
