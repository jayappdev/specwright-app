import { describe, it, expect } from "vitest";
import { conversations } from "./askai.js";

describe("conversations", () => {
  it("groups follow-ups under the first question, oldest first", () => {
    const runs = [
      { id: "c", parents: ["b"], at: "2026-11-27T10:03" },
      { id: "a", parents: [], at: "2026-11-27T10:01" },
      { id: "b", parents: ["a"], at: "2026-11-27T10:02" },
      { id: "x", parents: [], at: "2026-11-27T11:00" },
    ];
    const m = conversations(runs);
    expect([...m.keys()].sort()).toEqual(["a", "x"]);
    expect(m.get("a").map((r) => r.id)).toEqual(["a", "b", "c"]);
  });
  it("starts a new conversation from a flow run or an unknown run", () => {
    const runs = [
      { id: "f", kind: "flow", at: "1" },
      { id: "p", parents: ["f"], at: "2" },
      { id: "q", parents: ["gone"], at: "3" },
    ];
    const m = conversations(runs);
    expect([...m.keys()].sort()).toEqual(["p", "q"]);
    expect(m.has("f")).toBe(false);
  });
});

import { contextWindow, contextBlock } from "./askai.js";

describe("contextWindow", () => {
  it("guesses from the model name", () => {
    expect(contextWindow("claude-sonnet-4-5")).toBe(200_000);
    expect(contextWindow("gpt-4o")).toBe(128_000);
    expect(contextWindow("gemini-2.5-pro")).toBe(1_000_000);
    expect(contextWindow("")).toBe(200_000);
  });
});

describe("contextBlock", () => {
  it("lists what was added, and nothing when nothing was", () => {
    expect(contextBlock([])).toBe("");
    const b = contextBlock([
      { kind: "file", value: "src/a.java" },
      { kind: "folder", value: "specs" },
      { kind: "url", value: "https://example.com/x" },
    ]);
    expect(b).toContain("- File: src/a.java");
    expect(b).toContain("- Folder: specs/");
    expect(b).toContain("- Web page: https://example.com/x (fetch it with fetch_webpage)");
  });
});

import { savedVersionOf } from "./askai.js";

describe("savedVersionOf", () => {
  it("reads the version save_flow reports, and nothing for a new flow", () => {
    expect(
      savedVersionOf(
        'Saved the flow "Release notes (v3)" (id release-notes-v3, file .codegen/flows/release-notes-v3.json). It is version 3 of release-notes; the original is unchanged.\nIt checks out.',
      ),
    ).toEqual({ name: "Release notes (v3)", id: "release-notes-v3", rev: "3", parent: "release-notes" });
    expect(
      savedVersionOf('Saved the flow "New" (id new, file .codegen/flows/new.json).\nIt checks out.'),
    ).toBe(null);
  });
});
