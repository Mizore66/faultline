// Preload for difftest TestLiveSelfIncidentMatch: replaces
// RECORDED_SELF_INCIDENT_ROOT in dist/proof-roots.js with the judge sample's
// root, so frozen TS takes its "Matched historical self-incident root."
// branch on the committed sample (no bundle anyone holds has the real root).
// The Go side reads FAULTLINE_TEST_SELF_INCIDENT_ROOT (internal/cli,
// faultline_test builds only). Loaded with `node --import`.
import { register } from "node:module";
register("./selfroot-hook.mjs", import.meta.url);
