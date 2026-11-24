import { describe, expect, it } from "vitest";
import { runProgress, usualMs, leftMs, mood } from "./RunSimple.jsx";

const st = (status, steps) => ({
  status,
  steps: steps.map((s, i) => ({ id: "s" + i, title: "S" + i, ...s })),
});

describe("simple run view", () => {
  it("measures progress: finished steps whole, the running one half, a loop by its items", () => {
    expect(
      runProgress(
        st("running", [
          { status: "done" },
          { status: "running" },
          { status: "pending" },
          { status: "pending" },
        ]),
      ),
    ).toBeCloseTo(0.375);
    expect(
      runProgress(st("running", [{ status: "done" }, { status: "running", iterations: 10, iteration: 6 }])),
    ).toBeCloseTo(0.75);
    expect(runProgress(st("done", [{ status: "done" }]))).toBe(1);
    expect(runProgress(st("running", [{ status: "done" }]))).toBeLessThan(1);
  });
  it("knows how long the flow usually takes, from its finished runs only", () => {
    const r = (id, wf, state, ms) => ({
      id,
      workflow: wf,
      state,
      started: "2026-11-24T10:00:00Z",
      ended: new Date(Date.parse("2026-11-24T10:00:00Z") + ms).toISOString(),
    });
    const runs = [
      r("a", "f", "done", 60000),
      r("b", "f", "done", 120000),
      r("c", "f", "done", 90000),
      r("d", "f", "failed", 5000),
      r("e", "g", "done", 999999),
      r("now", "f", "done", 1),
    ];
    expect(usualMs(runs, "f", "now")).toBe(90000);
    expect(usualMs([], "f", "x")).toBe(0);
  });
  it("estimates what is left", () => {
    expect(leftMs(st("running", []), 30000, 90000)).toBe(60000);
    expect(leftMs(st("running", [{ status: "done" }, { status: "pending" }]), 40000, 0)).toBeCloseTo(40000);
    expect(leftMs(st("running", [{ status: "pending" }, { status: "pending" }]), 40000, 0)).toBeNull();
  });
  it("picks a mood for where the run is", () => {
    const run = st("running", [{ status: "running" }]);
    expect(mood(run, 10 * 60000, 1000, 0, 0).art).toBe("☕");
    expect(mood(run, 10000, 1000, 0, 0).art).toBe("🏁");
    expect(mood(run, null, 200000, 100000, 0).art).toBe("🐢");
    expect(mood(st("review", []), null, 1, 0, 0).hot).toBe(true);
    expect(mood(st("done", []), null, 30000, 60000, 0).line).toMatch(/quicker than usual/);
    expect(mood(st("failed", []), null, 1, 0, 0).art).toBe("🙈");
  });
});
