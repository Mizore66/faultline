// Live-oracle clock shim for the frozen TypeScript witness review (loaded with
// `node --import`). It is deliberately NOT difftest/gen/seed.mjs: seed.mjs also
// ticks on Date.now(), which the frozen presentation calls on every page
// render (relative commit age), while the Go clock (faultline_test) ticks only
// where Go reads clock.Now() for a record timestamp. This shim ticks on
// `new Date()` with no argument only (the sole source of approvedAt/frozenAt
// in src/witness-lock.ts); Date.now() stays the real wall clock on both sides.
// On both sides the first record is stamped 2026-07-16T12:00:00.000Z and the
// next 2026-07-16T12:00:00.001Z. Nothing else is altered; src/ and dist/ are
// untouched.
const EPOCH = 1784203200000;
let tick = 0;
const RealDate = Date;
class LiveDate extends RealDate {
  constructor(...args) {
    if (args.length === 0) super(EPOCH + tick++);
    else super(...args);
  }
}
globalThis.Date = LiveDate;
