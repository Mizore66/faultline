import { describe, expect, it } from "vitest";
import { createDemoAnalysis } from "../src/engine.js";
import { renderIncidentPage } from "../src/ui.js";

describe("deterministic sample page", () => {
  it("renders the calm white-paper demo walkthrough without claiming a live proof", () => {
    const page = renderIncidentPage(createDemoAnalysis("RERUN"));
    expect(page).toContain("DEMO MODE");
    expect(page).toContain("A STEP-BY-STEP BUG INVESTIGATION");
    expect(page).toContain("Find where a bug started.");
    expect(page).toContain("Prove it with the same test.");
    expect(page).toContain("work sessions checked");
    expect(page).toContain("saved steps checked");
    expect(page).toContain("Pick one simple test");
    expect(page).toContain("Which versions pass or fail?");
    expect(page).toContain("Atlas version");
    expect(page).toContain("Passed the approved test");
    expect(page).toContain("claims-scroll");
    expect(page).toContain("Find when it first broke");
    expect(page).toContain("Check which changes matter");
    expect(page).toContain("Write a clear handoff");
    expect(page).toContain("Confirm the fix and save the proof");
    expect(page).toContain("Step 5 — first version that broke");
    expect(page).toContain("uses the safe built-in example");
    // Title / hero screen stays as-is
    expect(page).toContain("A STEP-BY-STEP BUG INVESTIGATION");
    expect(page).toContain("Find where a bug started.");
    expect(page).not.toContain("<<<<<<");
    expect(page).not.toContain("VERIFIED PREVENTION");
    expect(page).toContain("color-scheme:light");
    expect(page).toContain("--paper:#fff");
    expect(page).toContain("scroll-snap-align:center");
    expect(page).toContain('class="section pin-start"');
    expect(page).toContain("isPinStart");
    expect(page).toContain("sectionAllows");
    expect(page).toContain("softEase");
    expect(page).toContain("softDur=520");
    expect(page).toContain("ease=t=>t<0.5?4*t*t*t:1-Math.pow(-2*t+2,3)/2");
    expect(page).toContain("duration=1100");
  });
});
