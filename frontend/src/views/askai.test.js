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
