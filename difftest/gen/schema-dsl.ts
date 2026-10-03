import { z, type ZodTypeAny } from "zod";

const regexes: Record<string, RegExp> = {
  sha256: /^sha256:[a-f0-9]{64}$/,
  gitObjectId: /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/,
  identifier: /^[A-Za-z0-9][A-Za-z0-9._:-]*$/,
};

export function buildZod(dsl: any): ZodTypeAny {
  switch (dsl.t) {
    case "string": {
      let s = z.string();
      for (const c of dsl.checks) {
        if (c.k === "trim") s = s.trim();
        else if (c.k === "min") s = s.min(c.n);
        else if (c.k === "max") s = s.max(c.n);
        else if (c.k === "regex") s = c.msg === null ? s.regex(regexes[c.name]!) : s.regex(regexes[c.name]!, c.msg);
        else if (c.k === "datetime") s = s.datetime({ offset: true });
        else if (c.k === "uuid") s = s.uuid();
      }
      return s;
    }
    case "number": {
      let n = z.number();
      for (const c of dsl.checks) {
        if (c.k === "int") n = n.int();
        else if (c.k === "positive") n = n.positive();
        else if (c.k === "nonnegative") n = n.nonnegative();
        else if (c.k === "min") n = n.min(c.n);
        else if (c.k === "max") n = n.max(c.n);
      }
      return n;
    }
    case "boolean": return z.boolean();
    case "literal": return z.literal(dsl.v);
    case "enum": return z.enum(dsl.values);
    case "array": {
      let a = z.array(buildZod(dsl.item));
      for (const c of dsl.checks) {
        if (c.k === "length") a = a.length(c.n);
        else if (c.k === "min") a = a.min(c.n);
        else if (c.k === "max") a = a.max(c.n);
      }
      return a;
    }
    case "record": return z.record(buildZod(dsl.value));
    case "optional": return buildZod(dsl.inner).optional();
    case "nullable": return buildZod(dsl.inner).nullable();
    case "object": {
      const shape = Object.fromEntries(dsl.shape.map(([k, s]: [string, any]) => [k, buildZod(s)]));
      const o = z.object(shape);
      return dsl.strict ? o.strict() : o;
    }
    case "disc": return z.discriminatedUnion(dsl.key, dsl.options.map(buildZod));
    case "refine": {
      const pred = dsl.pred === "nonEmpty" ? (v: unknown) => typeof v === "string" && v.length > 0 : () => false;
      return buildZod(dsl.inner).refine(pred, dsl.msg);
    }
    case "default": return buildZod(dsl.inner).default(dsl.v);
  }
  throw new Error(`unknown DSL node: ${dsl.t}`);
}
