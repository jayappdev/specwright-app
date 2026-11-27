import { describe, it, expect } from "vitest";
import { refQuery, jsonRefs, rankRefs, applyRef } from "./refcomplete.js";

describe("refQuery", () => {
  it("opens after {{ and follows what is typed", () => {
    expect(refQuery("Hello {{", 8)).toEqual({ start: 6, query: "" });
    expect(refQuery("Hello {{inp", 11)).toEqual({ start: 6, query: "inp" });
    expect(refQuery("a {{steps.get.json[0].na", 25)).toEqual({ start: 2, query: "steps.get.json[0].na" });
  });
  it("stays closed outside a reference", () => {
    expect(refQuery("Hello", 5)).toBe(null);
    expect(refQuery("{{inputs.x}} and more", 21)).toBe(null);
    expect(refQuery("{{ what is this?", 16)).toBe(null);
  });
});

describe("jsonRefs", () => {
  it("lists a list's item fields with [0]", () => {
    const shape = {
      type: "array",
      items: { type: "object", properties: { sym: { type: "string" }, px: { type: "number" } } },
    };
    expect(jsonRefs("get.buffer_output", shape, "Get").map((x) => x[0])).toEqual([
      "get.buffer_output.json[0].sym",
      "get.buffer_output.json[0].px",
    ]);
    expect(jsonRefs("x", { type: "string" }, "X")).toEqual([]);
  });
});

describe("rankRefs", () => {
  const list = [
    ["inputs.version", "Version"],
    ["get.buffer_output", "Get tickets · text"],
    ["get.buffer_output.json[0].summary", "Get tickets · summary"],
    ["context", "context"],
  ];
  it("puts what starts with the typed text first", () => {
    expect(rankRefs(list, "inp")[0][0]).toBe("inputs.version");
    expect(rankRefs(list, "summ")[0][0]).toBe("get.buffer_output.json[0].summary");
    expect(rankRefs(list, "tickets").length).toBe(2);
    expect(rankRefs(list, "zzz")).toEqual([]);
    expect(rankRefs(list, "get")[0][0]).toBe("get.buffer_output");
  });
});

describe("applyRef", () => {
  it("completes the reference and closes it once", () => {
    expect(applyRef("Hi {{inp", 3, 8, "inputs.name")).toEqual({ value: "Hi {{inputs.name}}", caret: 18 });
    expect(applyRef("Hi {{inp}} there", 3, 8, "inputs.name")).toEqual({
      value: "Hi {{inputs.name}} there",
      caret: 18,
    });
  });
});
