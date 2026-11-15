import { describe, expect, it } from "vitest";
import { mdToRules, nextRuleId, rulesToMd } from "./richtext.js";

describe("rules as Markdown", () => {
  const rules = [
    { id: "R-001", text: "Use **UTC** for times." },
    { id: "R-002", text: "No secrets in logs.\n\n- not even tokens" },
  ];
  it("round-trips", () => expect(mdToRules(rulesToMd(rules))).toEqual(rules));
  it("numbers the next rule", () => expect(nextRuleId(rules)).toBe("R-003"));
});
