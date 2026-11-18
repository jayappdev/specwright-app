import { describe, expect, it } from "vitest";
import { checkArgs, inferParams } from "./toolParams.js";

describe("inferParams", () => {
  it("adds placeholders and finds types, formats, values and defaults", () => {
    const { params, changes } = inferParams({
      command: "node x.js {{file}} {{port}} {{mode}} {{extra}}",
      params: [
        { name: "file", type: "string" },
        { name: "port", type: "string" },
        { name: "mode", type: "string", desc: "Output format, one of: md, html or json (default md)" },
        { name: "since", type: "string" },
        { name: "dry_run", type: "string" },
        { name: "count", type: "string", format: "multiline" },
      ],
    });
    const by = Object.fromEntries(params.map((p) => [p.name, p]));
    expect(by.file.format).toBe("path");
    expect(by.port.type).toBe("integer");
    expect(by.mode.enum).toEqual(["md", "html", "json"]);
    expect(by.mode.default).toBe("md");
    expect(by.since.format).toBe("date");
    expect(by.dry_run.type).toBe("boolean");
    expect(by.count.format).toBe("multiline");
    expect(by.extra.required).toBe(false);
    expect(changes.length).toBeGreaterThanOrEqual(6);
  });
});

describe("checkArgs", () => {
  const e = {
    params: [
      { name: "port", type: "integer", required: true },
      { name: "ok", type: "boolean" },
      { name: "mode", type: "string", enum: ["a", "b"], default: "a" },
      { name: "day", type: "string", format: "date" },
    ],
  };
  it("converts", () => {
    const r = checkArgs(e, { port: "5432", ok: "yes", day: "2026-11-17" });
    expect(r.problems).toEqual([]);
    expect(r.args).toEqual({ port: 5432, ok: true, mode: "a", day: "2026-11-17" });
  });
  it("refuses", () => {
    expect(checkArgs(e, { port: "x", mode: "c", day: "17/11" }).problems).toHaveLength(3);
    expect(checkArgs(e, {}).problems).toEqual(["port is required"]);
  });
});
