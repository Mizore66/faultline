// Frozen-side clock hook of the guided-investigate oracle, loaded with
// `node --import difftest/gen/seed.mjs --import <this file>`.
//
// seed.mjs ticks its seeded clock on every `new Date()` and every
// `Date.now()`. The Go clock ticks only where a record timestamp or a
// bundle/ID time is read (clock.Now / clock.NowMillis); the review page's
// relative commit age (src/witness-review-presentation.ts, Date.now) uses the
// unseeded monotonic clock in Go (clock.Monotonic). This hook restores that
// single correspondence and nothing else: a Date.now() call made from
// witness-review-presentation.js returns the real wall clock without
// advancing the seeded sequence. Every other read, ordering and value is
// seed.mjs's. No output is rewritten.
const seededNow = Date.now.bind(Date);
const realNow = Object.getPrototypeOf(Date).now.bind(Object.getPrototypeOf(Date));
Date.now = function now() {
  const stack = new Error().stack ?? "";
  return stack.includes("witness-review-presentation") ? realNow() : seededNow();
};
