import { describe, expect, it } from "vitest";
import { createDemoAnalysis } from "../src/engine.js";
import { renderIncidentPage } from "../src/ui.js";

describe("deterministic sample page", () => {
  it("visibly separates fixture theater from the real incident path", () => {
    const page = renderIncidentPage(createDemoAnalysis("RERUN"));
    expect(page).toContain("DETERMINISTIC SAMPLE");
    expect(page).toContain("fl judge-demo");
    expect(page).toContain("fl incident start");
    expect(page).toContain("Sample executable states");
    expect(page).toContain("SAMPLE RESULT - NOT A CUSTOMER PREVENTION CLAIM");
    expect(page).not.toContain("Session Cedar");
    expect(page).not.toContain("recorded sessions");
    expect(page).not.toContain("VERIFIED PREVENTION");
  });
});
