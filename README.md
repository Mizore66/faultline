# FaultLine

> The Codex safety layer that turns a failing CI predicate into executable evidence and a verified prevention packet.

FaultLine does not ask a model to name the culprit. It freezes a human-approved, executable question before localization, runs that same witness over recorded states, finds a stable pass-to-fail boundary, and counterfactually tests the implicated edits.

The conclusion is deliberately narrow: FaultLine can establish the earliest recorded state that flips a frozen witness. It does not claim model intent, semantic root cause, or a unique cause.

## What works today

This repository ships a runnable, deterministic Developer Tools demo built around a real Node process:

- `fl judge-demo --rerun-all` materializes multiple immutable fixture states and executes the reviewed witness against each one.
- The engine attributes the failing contribution before it scans turns, checks transition stability through reruns, and displays non-monotonic history.
- A two-edit interaction is counterfactually validated in both directions; an invalid partial patch is visibly `UNRESOLVED`, never misclassified as evidence.
- The same frozen witness produces a three-state prevention proof: last-good `PASS`, first-bad `FAIL`, repaired `PASS`.
- `fl verify` performs offline proof-bundle verification without executing repository code; an externally recorded root digest upgrades that verification to tamper detection.
- A single incident page tells the story: **BREAK → FIND → PROVE → FIX → PREVENT**.

The sample is intentionally labeled as a deterministic, built-in judge fixture. It demonstrates the evidence protocol without pretending to be a general sandbox or a live Codex transport.

## Quick start

Requirements: Node.js 22+ and pnpm 10+. The current sample is designed to run through Node's local process runner on Windows, macOS, and Linux. The included GitHub Actions matrix is configured to verify Node 22 and 24 across those platforms when the repository is pushed.

```powershell
pnpm install
pnpm test
pnpm fl -- judge-demo --rerun-all
```

The last command creates `.faultline/judge-demo`, checks its complete declared file set, and starts the local incident page. Open the URL printed in the terminal. Press `Ctrl+C` to stop it.

For an export-only rerun suitable for a CI check:

```powershell
pnpm fl -- judge-demo --rerun-all --export-only
pnpm fl -- verify .faultline/judge-demo
```

For an instant cached replay, replace `--rerun-all` with `--replay`. Replay remains visibly labeled as cached; the UI button **Re-run all evidence** executes the built-in sample again.

## CLI

```text
fl judge-demo [--replay | --rerun-all] [--output <directory>] [--export-only]
fl verify <proof-bundle-directory>
fl serve [--port <number>]
fl codex --dry-run | --snapshot [--repo <directory>]
fl witness propose [--live] [--model <model>]
```

`fl codex --snapshot --repo <directory>` is an actual, limited sidecar capture: it refuses a dirty worktree, records the immutable Git `HEAD` tree and commit in a content-addressed local manifest, and does not modify visible Git history. It does **not** claim a live Codex transport, turn lifecycle capture, process quiescence, or agent provenance yet.

## GPT-5.6 witness proposal

FaultLine includes an optional live proposal boundary using the OpenAI Responses API and strict JSON Schema output. It accepts only a blinded incident packet—the schema rejects candidate turns, diffs, and localization fields—then requires a human to review and freeze the exact proposed witness before scanning.

```powershell
$env:OPENAI_API_KEY = "..."
pnpm fl -- witness propose --live --model gpt-5.6
```

The judge demo never needs an API key. The implementation follows the [Responses API quickstart](https://platform.openai.com/docs/quickstart/make-your-first-api-request) and its [structured-output format](https://platform.openai.com/docs/api-reference/responses-streaming/response/refusal/delta?lang=curl).

## Judge path

1. Run `pnpm fl -- judge-demo --rerun-all`.
2. Read the frozen witness and its digest before inspecting the suspect state.
3. Watch FaultLine compress four contribution boundaries to Session Cedar, then 31 recorded turns to the first stable `PASS → FAIL` boundary.
4. Inspect the two-hunk counterfactual and the unresolved partial-patch attempt.
5. Verify the three-state prevention proof and run `pnpm fl -- verify .faultline/judge-demo`.
6. Use **Re-run all evidence** to challenge the stored results locally.

This satisfies the core judge-testing path without a Codex login or an OpenAI API key.

## Evidence model

FaultLine labels claims rather than blending execution and interpretation:

| Label | Meaning |
| --- | --- |
| `EXECUTED` | A stored runner result for the frozen witness. |
| `DERIVED` | Deterministically computed from stored facts, such as the earliest stable boundary. |
| `INFERRED` | An explanation that must cite evidence but is not itself an execution result. |
| `UNKNOWN` | A material question that the available evidence does not answer. |

Verdicts are restricted to `PASS`, `FAIL`, `UNSTABLE`, `ERROR`, and `INAPPLICABLE`. Models never assign them.

## Proof bundle boundary

The demo writes this structure:

```text
.faultline/judge-demo/
  manifest.json
  report.md
  analysis.json
  witness/witness.json
  runs/<run-id>/{result.json,stdout.log,stderr.log}
  minimization/attempts.json
  prevention/three-state.json
  hashes.txt
  ROOT.sha256
  VERIFY.md
```

`fl verify` validates the complete declared file set, rejects undeclared leftovers, checks that every run cited by the analysis has a persisted record, and verifies the manifest's analysis digest. This is an offline self-consistency check:

```powershell
pnpm fl -- verify .faultline/judge-demo
```

For tamper detection, record the printed bundle root somewhere the bundle editor cannot rewrite (for example, a release manifest or CI attestation), then supply it explicitly:

```powershell
pnpm fl -- verify .faultline/judge-demo --expect-root sha256:<recorded-root>
```

Without an external root, no local hash file can prove authenticity against an editor who can modify every file in the bundle. Reproducibility is a separate, explicit rerun action.

## Current implementation boundary

The functional demo is purposefully narrow:

- one reviewed Node fixture and one built-in runner;
- a synthetic but executable integration ledger;
- an actual clean-Git sidecar snapshot, but not an interception of Codex internals;
- no claim of Docker isolation or generic arbitrary-code sandboxing.

The next credible extension is a real, supported Codex attach path that records Git tree snapshots from an actual worktree, then feeds the exact same engine and proof format. The demo never advertises unsupported security or provenance guarantees.

## Why this is a Developer Tool

The target user is the developer who merges several agent-assisted changes and gets a red CI run. FaultLine makes that debugging task auditable: freeze the falsifiable question first, then execute it across the relevant history and produce a bounded artifact another engineer can inspect.

| Related approach | What it helps with | FaultLine's distinct contribution |
| --- | --- | --- |
| Git bisect | Finds a revision boundary under a test predicate | Adds agent-session/turn provenance and a frozen witness-review protocol. |
| Delta debugging | Minimizes a failure-inducing input or change set | Keeps unresolved subsets distinct and validates both counterfactual directions when possible. |
| Agent traces / observability | Explains what an agent did | Treats source states and executable evidence—not transcript semantics—as the final arbiter. |
| AI root-cause narration | Produces a plausible explanation | Separates inference from verdicts and makes model hypotheses rejectable by execution. |

## Build Week submission checklist

The official [OpenAI Build Week submission requirements](https://openai.devpost.com/) call for a working project, a category, public under-three-minute demo video with voiceover explaining Codex and GPT-5.6 usage, code repository, README, and a qualifying `/feedback` session ID. Developer Tools entries should also provide installation, platform support, and a clear test path for judges.

- [x] Runnable judge fixture and one-command test path.
- [x] Installation, platform support, scope limits, and test instructions.
- [x] Optional GPT-5.6 structured witness boundary.
- [x] A demo page with a visible five-beat narrative.
- [ ] Record the qualifying Codex build session and capture its `/feedback` ID.
- [ ] Replace or complement the deterministic fixture with a publishable real worktree incident.
- [ ] Record a <3-minute narrated demo showing the live product path.
- [ ] Publish a licensed repository and complete the Devpost project submission.

No software project can honestly guarantee a 100% chance of winning a judged competition. FaultLine is designed instead to maximize verified rubric coverage: non-trivial executable implementation, coherent product design, a specific developer pain point, and a differentiated, evidence-first workflow.

## Development

```powershell
pnpm typecheck
pnpm test
pnpm build
```

The code is strict TypeScript with runtime schemas at the external/persisted boundaries. Tests cover canonical hashing, the stable boundary, bidirectional proof, integrity tampering, blinded-packet validation, and the rendered five-beat experience.
