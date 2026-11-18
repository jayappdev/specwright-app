import { describe, expect, it } from "vitest";
import { applyStepIDs, slug, stepNames } from "./flowNames.js";

describe("flow step names", () => {
  it("slugs titles", () => expect(slug("Find TODOs (v2)!")).toBe("find-todos-v2"));
  it("maps tool names and titles to ids, dropping ambiguous ones", () => {
    const flow = {
      nodes: [
        { id: "a", type: "tool", tool: "builtin:read_file", title: "Read it" },
        { id: "b", type: "ai", title: "Read it" },
      ],
    };
    const { names, ambiguous } = stepNames(flow);
    expect(names.read_file).toBe("a");
    expect(names["read-it"]).toBeUndefined();
    expect(ambiguous.has("read-it")).toBe(true);
  });
  it("rewrites references by tool name to step ids", () => {
    const flow = {
      nodes: [
        { id: "s1", type: "tool", tool: "lint" },
        { id: "s2", type: "ai", prompt: "Summarise {{lint.output_file}}" },
      ],
    };
    expect(applyStepIDs(flow).nodes[1].prompt).toBe("Summarise {{s1.output_file}}");
  });
});
