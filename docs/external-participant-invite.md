# External participant invite (N=1 impact)

Copy, personalize, send. You need **one** consented person with a real failing test/CI log in **their** repo.

---

## Message (edit names)

Subject: 60–90 min to try FaultLine on a real CI failure?

Hi &lt;Name&gt;,

I’m submitting FaultLine to OpenAI Build Week and need one honest external trial (not my own repo).

FaultLine freezes a human-reviewed failing predicate, replays it across Git states in Docker when available, and packages evidence another engineer can verify offline — without claiming model intent.

**Ask:** 60–90 minutes on a small real failure in *your* project (CI log + failing command). You Approve/Freeze in a local browser; nothing auto-merges.

**Consent:** May I publish a *redacted* summary (your role generalized, no secrets) on Devpost? You can refuse any quote.

If yes, reply with a good time and whether Docker is available on your machine (or we use my machine with a redacted clone/log you provide).

Thanks,
&lt;You&gt;

---

## Session agenda (for you as facilitator)

1. Consent + redaction rules (2 min)
2. Their triggering question in their words (5 min)
3. `pnpm fl doctor --repo <their-repo>`
4. `pnpm fl investigate --ci-log <log> --repo <their-repo> --command "<failing predicate>" --runtime node` (or matching runtime)
5. Browser: Approve → Freeze (separate clicks)
6. If Docker READY: continue / serve / verify; else stop at freeze and record “proof not produced”
7. Fill [impact-validation-external-01.md](impact-validation-external-01.md) immediately after
8. Ask: would you try again? (quote only with permission)

## What not to do

- Do not use the FaultLine self-incident as “external”
- Do not invent time-saved numbers
- Do not publish private keys, full CI logs, or employer names without consent
