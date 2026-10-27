// Fetch shim for the writer replay and the Node oracle (slice 4 spec 2.3):
// loaded with `node --import` after seed.mjs; src/ is not modified. When
// FAULTLINE_TEST_OPENAI_URL is set, a fetch of exactly
// https://api.openai.com/v1/responses (src/repair-brief.ts
// proposeRepairBriefWithGpt) goes to that URL instead, with the same init:
// the harness's fake OpenAI server (difftest/gen/fake-openai.ts). Every
// other call passes through unchanged. It reads no clock and no UUID, so
// the seeded sequence is unchanged. The Go twin is the faultline_test
// override in internal/repair.
const OPENAI_RESPONSES = "https://api.openai.com/v1/responses";
const realFetch = globalThis.fetch;
globalThis.fetch = function fetch(input, init) {
  const target = process.env.FAULTLINE_TEST_OPENAI_URL;
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : undefined;
  if (target && url === OPENAI_RESPONSES) return realFetch(target, init);
  return realFetch(input, init);
};
