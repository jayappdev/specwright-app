import { describe, expect, it } from "vitest";
import { resolveDate } from "./dates.js";

const now = new Date(2026, 10, 17, 14, 35, 42); // "now" drops the seconds

describe("resolveDate", () => {
  it("reads dates, date-times and relative values", () => {
    const cases = [
      ["date", "", ""],
      ["date", "2026-03-04", "2026-03-04"],
      ["date", "2026-03-04T10:00", "2026-03-04"],
      ["date", "today", "2026-11-17"],
      ["date", "today-7d", "2026-11-10"],
      ["date", "Today + 2", "2026-11-19"],
      ["date", "today-1w", "2026-11-10"],
      ["datetime", "2026-03-04T09:05", "2026-03-04T09:05"],
      ["datetime", "2026-03-04 09:05:30", "2026-03-04T09:05:30"],
      ["datetime", "2026-03-04", "2026-03-04T00:00"],
      ["datetime", "now", "2026-11-17T14:35"],
      ["datetime", "now-2h", "2026-11-17T12:35"],
      ["datetime", "now+30m", "2026-11-17T15:05"],
      ["datetime", "today-1d", "2026-11-16T00:00"],
    ];
    for (const [type, s, want] of cases) expect(resolveDate(type, s, now), `${type} ${s}`).toBe(want);
  });
  it("refuses what is not a date", () => {
    for (const [type, s] of [
      ["date", "next tuesday"],
      ["date", "2026-13-01"],
      ["datetime", "2026-03-04T25:00"],
      ["date", "today-2h"],
    ])
      expect(() => resolveDate(type, s, now), `${type} ${s}`).toThrow();
  });
});
