import { describe, expect, it } from "vitest";
import { findFileRefs, parseFileRef, wholeFileRef } from "./filerefs.js";

describe("parseFileRef", () => {
  it("reads a line number", () => expect(parseFileRef("src/a.java:42")).toEqual({ path: "src/a.java", line: 42 }));
  it("reads a GitHub-style line", () => expect(parseFileRef("src/a.go#L7")).toEqual({ path: "src/a.go", line: 7 }));
  it("has line 0 without one", () => expect(parseFileRef("README.md")).toEqual({ path: "README.md", line: 0 }));
});

describe("wholeFileRef", () => {
  it("accepts a path", () => expect(wholeFileRef("internal/core/hub.go")?.path).toBe("internal/core/hub.go"));
  it("rejects a URL", () => expect(wholeFileRef("https://example.com/a.go")).toBeNull());
  it("rejects a sentence", () => expect(wholeFileRef("run the tests now")).toBeNull());
});

describe("findFileRefs", () => {
  it("finds paths in text with their position", () => {
    const text = "See frontend/src/App.jsx:116 and internal/store/store.go.";
    const refs = findFileRefs(text);
    expect(refs.map((r) => [r.path, r.line])).toEqual([["frontend/src/App.jsx", 116], ["internal/store/store.go", 0]]);
    for (const r of refs) expect(text.slice(r.index, r.index + r.length)).toBe(r.raw);
  });
  it("finds a quoted path with spaces", () => expect(findFileRefs("open `My Project/a b.txt` please")[0]?.path).toBe("My Project/a b.txt"));
  it("skips URLs", () => expect(findFileRefs("go to https://example.com/x/y.html")).toEqual([]));
});
