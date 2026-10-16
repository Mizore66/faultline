// Module hook of difftest/gen/selfroot.mjs.
export const SAMPLE_ROOT = "sha256:f85c446dfd5ab92222b10a314e79209a8a7dc10ee69af9d2deaa04aceafeb7d9";
export async function load(url, context, next) {
  const result = await next(url, context);
  if (!url.endsWith("/dist/proof-roots.js")) return result;
  const source = String(result.source);
  const replaced = source.replace(/export const RECORDED_SELF_INCIDENT_ROOT = "[^"]*"/, `export const RECORDED_SELF_INCIDENT_ROOT = "${SAMPLE_ROOT}"`);
  if (replaced === source) throw new Error("selfroot-hook: RECORDED_SELF_INCIDENT_ROOT not found in dist/proof-roots.js");
  return { ...result, source: replaced };
}
