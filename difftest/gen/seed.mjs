// Seeded clock, UUIDs and mkdtemp suffixes for writer goldens (slice 2 spec 2.1).
// Loaded with `node --import`; src/ is not modified.
import crypto, { createHash } from "node:crypto";
import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";

const seed = process.env.FAULTLINE_TEST_SEED;
if (seed) {
  const EPOCH = 1784203200000;
  const ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let tick = 0, uuid = 0, tmp = 0;
  // Read only by the oracle's repairWithCodex op (Go: clock.ConsumedForTest).
  Object.defineProperty(globalThis, "__faultlineSeedCounters", { get: () => ({ ticks: tick, uuids: uuid }), configurable: true });
  const hash = (kind, n) => createHash("sha256").update(`${seed}\0${kind}\0${n}`, "utf8").digest();
  const RealDate = Date;
  class SeededDate extends RealDate {
    constructor(...args) { if (args.length === 0) super(EPOCH + tick++); else super(...args); }
    static now() { return EPOCH + tick++; }
  }
  globalThis.Date = SeededDate;
  crypto.randomUUID = () => {
    const b = hash("uuid", uuid++).subarray(0, 16);
    b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
    const h = b.toString("hex");
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
  };
  const suffix = () => { const h = hash("tmp", tmp++); let s = ""; for (let i = 0; i < 6; i++) s += ALPHABET[h[i] % 62]; return s; };
  const realSync = fs.mkdtempSync;
  fs.mkdtempSync = (prefix, options) => {
    for (;;) {
      const path = `${prefix}${suffix()}`;
      try { fs.mkdirSync(path, 0o700); return path; }
      catch (error) { if (error?.code !== "EEXIST") return realSync(prefix, options); }
    }
  };
  // fs/promises.mkdtemp reports a failure with the name it tried (libuv's
  // req->path, which mkdtemp(3) filled in), except on Windows, where libuv
  // leaves the template there: rethrow mkdir's error as that mkdtemp error.
  const mkdtempError = (error, path, prefix) => {
    const tail = `, mkdir '${path}'`;
    if (typeof error?.errno !== "number" || error.syscall !== "mkdir" || !error.message.endsWith(tail)) return error;
    const shown = process.platform === "win32" ? `${prefix}XXXXXX` : path;
    return Object.assign(new Error(`${error.message.slice(0, -tail.length)}, mkdtemp '${shown}'`), { errno: error.errno, code: error.code, syscall: "mkdtemp", path: shown });
  };
  fs.promises.mkdtemp = async (prefix, options) => {
    for (;;) {
      const path = `${prefix}${suffix()}`;
      try { await fs.promises.mkdir(path, 0o700); return path; }
      catch (error) { if (error?.code !== "EEXIST") throw mkdtempError(error, path, prefix); }
    }
  };
  fs.mkdtemp = (prefix, options, callback) => {
    const cb = typeof options === "function" ? options : callback;
    fs.promises.mkdtemp(prefix).then((p) => cb(null, p), (e) => cb(e));
  };
  syncBuiltinESMExports();
}
