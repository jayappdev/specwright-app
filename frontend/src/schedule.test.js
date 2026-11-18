import { describe, it, expect } from "vitest";
import { checkSchedule, nextRun, upcoming, describeSchedule } from "./schedule.js";

const at = (y, mo, d, h = 0, m = 0) => new Date(y, mo - 1, d, h, m);

describe("schedule", () => {
  it("checks", () => {
    expect(checkSchedule({ mode: "daily", time: "09:00" })).toBe("");
    expect(checkSchedule({ mode: "weekly", time: "09:00" })).toMatch(/days/);
    expect(checkSchedule({ mode: "every", every: 0, unit: "hours" })).toMatch(/how often/);
    expect(checkSchedule({ mode: "x" })).toMatch(/choose/);
  });
  it("daily and weekly", () => {
    expect(nextRun({ mode: "daily", time: "09:00" }, at(2026, 11, 18, 10), at(2026, 11, 1))).toEqual(
      at(2026, 11, 19, 9),
    );
    // 2026-11-18 is a Wednesday
    expect(
      nextRun({ mode: "weekly", time: "08:30", days: ["mon"] }, at(2026, 11, 18), at(2026, 11, 1)),
    ).toEqual(at(2026, 11, 23, 8, 30));
  });
  it("every, until and count", () => {
    const s = { mode: "every", every: 2, unit: "hours", at: "2026-11-18T09:00" };
    expect(upcoming(s, at(2026, 11, 18, 10), null, 3)).toEqual([
      at(2026, 11, 18, 11),
      at(2026, 11, 18, 13),
      at(2026, 11, 18, 15),
    ]);
    expect(upcoming({ ...s, until: "2026-11-18T13:00" }, at(2026, 11, 18, 10), null, 5)).toHaveLength(2);
    expect(upcoming({ ...s, count: 3 }, at(2026, 11, 18, 10), null, 5, 2)).toHaveLength(1);
  });
  it("once in the past has no next run", () => {
    expect(nextRun({ mode: "once", at: "2026-01-01T09:00" }, at(2026, 11, 18), null)).toBeNull();
  });
  it("describes", () => {
    expect(describeSchedule({ mode: "every", every: 1, unit: "hours" })).toBe("every hour");
    expect(describeSchedule({ mode: "daily", time: "09:00", count: 1 })).toBe(
      "daily at 09:00, 1 run at most",
    );
  });
});
