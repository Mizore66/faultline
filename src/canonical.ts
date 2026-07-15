import { createHash } from "node:crypto";

type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

function normalize(value: unknown): JsonValue {
  if (Array.isArray(value)) {
    return value.map(normalize);
  }
  if (value !== null && typeof value === "object") {
    return Object.entries(value)
      .filter(([, child]) => child !== undefined)
      .sort(([left], [right]) => left.localeCompare(right))
      .reduce<Record<string, JsonValue>>((result, [key, child]) => {
        result[key] = normalize(child);
        return result;
      }, {});
  }
  if (value === null || typeof value === "boolean" || typeof value === "number" || typeof value === "string") {
    return value;
  }
  throw new TypeError(`Value is not JSON-serializable: ${typeof value}`);
}

export function canonicalJson(value: unknown): string {
  return JSON.stringify(normalize(value));
}

export function sha256(value: string | Buffer): string {
  return createHash("sha256").update(value).digest("hex");
}

export function digestJson(value: unknown): string {
  return `sha256:${sha256(canonicalJson(value))}`;
}
