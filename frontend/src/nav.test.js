import { describe, it, expect } from "vitest";
import { createHistory } from "./nav.js";

describe("navigation history", () => {
  it("goes back and forward", () => {
    const h = createHistory();
    h.push({ view: "flows", key: "a" });
    h.push({ view: "playground", key: "" });
    h.push({ view: "tools", key: "lint" });
    expect(h.back()).toEqual({ view: "playground", key: "" });
    expect(h.back()).toEqual({ view: "flows", key: "a" });
    expect(h.canBack()).toBe(false);
    expect(h.back()).toBeNull();
    expect(h.forward()).toEqual({ view: "playground", key: "" });
    expect(h.peekForward()).toEqual({ view: "tools", key: "lint" });
  });
  it("drops the entries ahead when going somewhere new", () => {
    const h = createHistory();
    h.push({ view: "flows" });
    h.push({ view: "tools" });
    h.back();
    h.push({ view: "history", key: "r1" });
    expect(h.canForward()).toBe(false);
    expect(h.size()).toBe(2);
    expect(h.peekBack()).toEqual({ view: "flows" });
  });
  it("keeps one entry for the same place, and completes an empty selection", () => {
    const h = createHistory();
    h.push({ view: "flows", key: "" });
    expect(h.push({ view: "flows", key: "" })).toBe(false);
    h.push({ view: "flows", key: "todo" }); // the flow opened as the view appeared
    expect(h.size()).toBe(1);
    expect(h.current()).toEqual({ view: "flows", key: "todo" });
    h.push({ view: "flows", key: "other" }); // another flow: a new place
    expect(h.size()).toBe(2);
  });
  it("keeps an empty selection as its own place when asked (the list of flows)", () => {
    const h = createHistory();
    h.push({ view: "flows", key: "" }, { keepEmpty: true });
    h.push({ view: "flows", key: "todo" }, { keepEmpty: true });
    expect(h.size()).toBe(2);
    expect(h.back()).toEqual({ view: "flows", key: "" });
  });
  it("keeps at most cap entries", () => {
    const h = createHistory(3);
    for (const v of ["a", "b", "c", "d", "e"]) h.push({ view: v });
    expect(h.size()).toBe(3);
    expect(h.back()).toEqual({ view: "d" });
    expect(h.back()).toEqual({ view: "c" });
    expect(h.back()).toBeNull();
  });
});
