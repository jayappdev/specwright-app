import { describe, expect, it } from "vitest";
import { ago } from "./time.js";

describe("ago", () => {
  const now = new Date(2026, 10, 15, 15, 0, 0).getTime();
  const at = (ms) => new Date(now - ms).toISOString();
  it("says just now under 45 s", () => expect(ago(at(10_000), now)).toBe("just now"));
  it("counts minutes", () => expect(ago(at(5 * 60_000), now)).toBe("5 min ago"));
  it("counts hours up to 6", () => expect(ago(at(2 * 3600_000), now)).toBe("2 h ago"));
  it("says today after 6 h", () => expect(ago(at(7 * 3600_000), now)).toMatch(/^today /));
  it("says yesterday", () => expect(ago(at(20 * 3600_000), now)).toMatch(/^yesterday /));
  it("does not say 'ago' for a time in the future", () =>
    expect(ago(at(-60_000), now)).not.toMatch(/ago|now/));
});
