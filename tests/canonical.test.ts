import { describe, expect, it } from "vitest";
import { canonicalJson, digestJson } from "../src/canonical.js";

describe("canonical JSON", () => {
  it("has a stable key order and excludes undefined optional properties", () => {
    const left = { b: 2, a: { z: true, x: "value" }, optional: undefined };
    const right = { a: { x: "value", z: true }, b: 2 };
    expect(canonicalJson(left)).toBe('{"a":{"x":"value","z":true},"b":2}');
    expect(digestJson(left)).toBe(digestJson(right));
  });
});
