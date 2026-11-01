# Slice 5c — read-only bundle serving and verified attachments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port frozen-TS `serve --bundle`, independently verified attachments and privacy-minimized proof panels, with permanent oracle evidence and six-native CLI/HTTP/standalone acceptance.

**Architecture:** Extend `internal/proofview` without changing its two-argument loader or creating another server. Return prevention artifacts from the same shared verification pass; use invocation-local dependencies to test attachment rechecks deterministically. Keep CLI parsing and its SIGINT-only lifecycle separate from judge/browser/timer behavior.

**Tech Stack:** Go 1.27, standard library, existing ordered `jsjson`/canonical/schema/node compatibility packages and trusted Git CLI; Node 22.22.2 and existing pinned TypeScript dependencies for capture/live tests only.

**Spec:** [Approved written specification](../specs/2026-11-01-go-slice5c-serve-bundle-design.md). Read it with this plan; the spec controls any conflict.

## Execution status — 2026-11-01

**Local implementation complete; hosted acceptance pending.** Tasks 1–5 have independent specification-compliance and code-quality acceptance. The completed independent whole-branch architecture/security/correctness review accepts publication and exact-head CI qualification with no verified Critical, Important or minor findings; it is a static source assessment, not native/runtime certification. The requested Opus route failed three times with HTTP500; a reviewer on the controller's working frontier-model route completed the replacement review. No failed request is counted as a review pass.

Local retained evidence includes the 118-case/45-page frozen corpus, five supplemental verifier-valid binding refusals, exact attachment/panel replays, staged/hostile checks, actual startup-SIGINT and default-port regressions, 73-case pinned live Linux ARM64 VM comparisons, strict evidence/target controls, native Darwin ARM64 Node-free checks, release guards, vet and tidy-diff. VM/cross-build evidence is not native acceptance. The full local affected-package run retained one pre-existing opt-in Node-oracle skip; two earlier oversized attachment runs remain incomplete. Worker no-op shell violations and all earlier failures are preserved, not reclassified as compliance or passes.

All six native targets, actual native evidence identity/audit and Windows-console behavior, all seven stack sites × four shapes on every target, pinned regeneration/no drift, the unnarrowed Ubuntu amd64 positional gate and all required exact-head workflows remain mandatory before normal merge or 5c completion. Only Linux ARM64 serve-site offsets have been measured; other-platform strict-probe failures require real measurements, focused fixes and review, never guessed fits or waivers. Unchecked combined execution steps below retain these unrun hosted obligations; accepted local source work does not check a step that also requires native/hosted agreement. The user's standing instruction already authorizes routine publication/CI/normal merge when gates pass, but not a public release, protected-source unfreeze, oracle retirement or evidence cleanup. This is not completion of remaining 5b or all Slice 5.

## Global Constraints

### Compatibility and boundaries

- Go 1.27 and the standard library are required. No `encoding/json` on digest paths.
- Frozen source: `ce99a793af0b020bf1b9cf21114f8d7897b37207`; oracle Node: `22.22.2`; current-tree manifest: `0.1.4`; manifest baseline: `ff5fe7b1120410e60c321f3ae620cf524c5694ec`. The source-pin manifest `0.1.3` is not the current-tree manifest.
- `src/`, `package.json` and `pnpm-lock.yaml` are never modified under this slice. Preserve prior goldens and all evidence/workspaces.
- Preserve insertion order, canonical digests, JS number/string semantics, WTF-8/UTF-16, inherited Git behavior, error/output/exit bytes and existing absent-attachment pages. Production must not execute Node or read `src/`, `dist/`, testdata or a runtime package manifest.
- All six native targets are required: linux/darwin/windows × amd64/arm64. Cross-builds or emulated local containers are not native acceptance. The positional gate remains the unnarrowed Ubuntu amd64 `FAULTLINE_COVERAGE_GATE=1` run.

### Security and evidence

- Attachments remain outside the immutable base Git-proof root. `NOT_PROVIDED` means self-consistency without an externally retained root. Repair uses `frozenDigest`; prevention uses `witnessDigest`. Valid uncertified minimization and valid prevention summaries remain attachable.
- Use real files and real verifiers for acceptance cases. Invocation-local private seams prove ordering/fail-closed decisions, not actual filesystem race exclusion. No exported setter, mutable release-global override, environment switch or production seed. No sleep/load/thread-scheduling race acceptance.
- Tests compare complete expected output/page bytes and independently reject private sentinels. No new broad stack/output normalization, alternate accepted output, length-only comparison, Node-availability skip, coverage reduction or new broad mask.
- Tests use local scripted fakes only where necessary and `fl-test-key` for any fake key; no real provider operation or real-looking secret. No change to production entropy for deterministic testing.
- Preserve historical failures/incomplete attempts and exact skip identities/reasons. Downloaded contents are untrusted: separate fresh data directories, scripts elsewhere, Python `-I`, trusted cwd, never execute downloaded content.

### Execution and authority

- The user separately approved design (“ye”) and the written spec (“yes”) on 2026-11-01. After final planning checks they explicitly authorized proceeding when this plan was finished, continued Slice 5 delivery, publication/CI and normal merge without further routine permission prompts. Implementation is now authorized; exact-head CI/reviews/security gates remain mandatory. No protected-source changes, oracle retirement, evidence cleanup or public release occur under this 5c scope. This authority comes from the new instruction, not exhausted PR #216 authority.
- Current normal checkout: `/Users/anasqumhiyeh/Documents/faultline`, branch `go-slice5c-design-20261101`, baseline HEAD `ff5fe7b1120410e60c321f3ae620cf524c5694ec`. No new worktree was requested. Preserve all six documentation changes, including this plan; nothing is staged.
- Controller owns HOST/runtime serialization. Every local shell/build/interpreter invocation uses the owned runner, an aggregate deadline of at most 240 seconds plus bounded cleanup, retained stdout/stderr/receipt and no detached/watchdog-reset workaround, broad cleanup or silent retry. Over-budget full acceptance runs belong on hosted CI.
- Initialize only this approved plan's `.superpowers/sdd/2026-11-01-go-slice5c-serve-bundle/` workspace using the skill helper. Its ledger first line names this plan. The existing 5b.1 discovery/handoff workspace is not the new execution ledger. Never read active reports/transcripts. Implementers use dedicated Read/Edit/Write only on assigned task files and their own report; researchers/reviewers write only their own report. No worker shell/runtime/subagents/publication. Controller runs all red/green commands.
- Never bypass TruffleHog, change global Git configuration, use the work account/plain repository `gh`, or fall back after a failed push. Separately authorized commits use personal `Anas Tarek Qumhiyeh <anasqumhiyeh@gmail.com>` and end with `Co-Authored-By: Claude Code <noreply@anthropic.com>`; PR descriptions end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

---

## Execution map and ownership

Five independently reviewable deliverables; no parallel implementation is implied. Task 1 establishes evidence before production work. Task 2 precedes Task 3; Tasks 2–3 precede Task 4; Task 5 integrates all four. Independent source research can run alongside controller planning, but that is not implementation permission.

| Task | Exclusive production ownership | Test/evidence ownership | Consumes → produces |
| --- | --- | --- | --- |
| 1. Oracle corpus | None | New `difftest/gen/serve-bundle-{fixtures,cases}.ts`, their `.test.ts` files, `gen-serve-bundle.ts`, new corpus and `difftest/servefixture/corpus{,_test}.go` | Frozen TS APIs → coherent retained fixtures, exact pages/errors, stable corpus API |
| 2. Verified attachments | `internal/bundle/prevention/verify.go`; new `internal/proofview/attachments.go`, `attachment_minimization.go`, `attachment_repair.go`, `attachment_prevention.go`; loader/model changes in `judge.go` only | `verify_artifacts_test.go`, `attachments_test.go`, `attachments_recheck_test.go`, `attachments_hostile_test.go` | Task 1 inputs → attachment types, artifact-returning verifier, backward-compatible attachment loader |
| 3. Panels/replay | New `internal/proofview/attachment_panels.go`; panel constants/calls and hero summary in `judge.go` only | `attachment_panels_test.go`, new `difftest/serve_bundle_test.go` | Task 2 View → full exact HTML and permanent Go-only replay |
| 4. CLI/native lifecycle | New `internal/cli/serve_bundle.go`, `serve_bundle_wait.go`; exact dispatch case in `cli.go` | `serve_bundle_test.go` in CLI; add CLI replay/HTTP tests to `difftest/serve_bundle_test.go` after Task 3; `difftest/servefixture/{process,http,process_unix,process_windows}.go`, `console-helper/main_windows.go`, `difftest/serve_bundle_live_test.go`, `difftest/serve_bundle_signals_{unix,windows}_test.go`, `difftest/release/serve_bundle_test.go` | Task 3 page + existing server → actual CLI/HTTP/signal/Node-free evidence |
| 5. Integration/gates | No unrelated production refactor; only oracle-proven stack call-site correction if required | `difftest/serve_bundle_stack_live_test.go`, `coverage_test.go`, `covergate_test.go`, `.github/workflows/go.yml`, existing skip checker, `KNOWN_DIFFERENCES.md`, roadmap/spec/this plan status | Tasks 1–4 → registered no-drift, six-native and full exact-head acceptance, task + whole-branch reviews |

Shared-file barriers: Task 2 finishes loader/model edits to `judge.go` before Task 3 edits rendering there. Task 1 owns `servefixture/corpus.go`; Task 4 adds process/HTTP files without rewriting that API. Task 3 creates `difftest/serve_bundle_test.go` for View replay; only after its review does Task 4 append actual CLI replay/HTTP tests there. Task 5 alone edits CI, skip/coverage registration and completion documentation. Reviewers receive an immutable task-scoped diff plus completed report; do not ask them to run tools beyond dedicated Read/own-report Write/Edit.

### Command notation and setup after approval

All command payloads below are executed separately through this exact future runner prefix; `R`, `W` and `RUN` in prose are names, not shell state carried between calls:

```text
R = /Users/anasqumhiyeh/Documents/faultline
W = /Users/anasqumhiyeh/Documents/faultline/.superpowers/sdd/2026-11-01-go-slice5c-serve-bundle
RUN = python3 -I /Users/anasqumhiyeh/Documents/faultline/.superpowers/sdd/2026-11-01-go-slice5c-serve-bundle/run.py
```

For example, the complete local Task 2 command is:

```sh
python3 -I /Users/anasqumhiyeh/Documents/faultline/.superpowers/sdd/2026-11-01-go-slice5c-serve-bundle/run.py --name t2-attachments-red --seconds 240 --cwd /Users/anasqumhiyeh/Documents/faultline -- go test ./internal/proofview -run '^TestAttachment' -count=1 -timeout=180s -v
```

`RUN --name UNIQUE --seconds N --cwd R -- PAYLOAD` below means expand those fixed absolute names, choose a fresh receipt name, and execute that single payload. No background flag. No automatic retry. On timeout retain failure and rescope the next diagnostic; do not reset the same over-budget operation. For each production step record the specific missing behavior, meaningful red receipt, minimal green receipt and affected regression receipts. Missing fixtures, syntax/import/compiler errors and a failing TS infrastructure probe are not a meaningful production red.

Execution setup consists of verifying branch/baseline/index/protected hashes, reading applicable instructions, creating this plan's ledger using the existing skill helper, installing a byte-reviewed copy of the bounded runner in W, and recording the task/interface conflict table before dispatch. Do not reinstall dependencies or create a worktree as generic skill setup. Use available trusted tooling; the already authorized Podman path can run the pinned oracle in retained isolated storage, not upgrade host tools or erase old containers/volumes.

## Task 1: Retain coherent frozen-TS fixtures and a replayable oracle corpus

**Files:** Create `difftest/gen/serve-bundle-fixtures.ts`, `serve-bundle-fixtures.test.ts`, `serve-bundle-cases.ts`, `serve-bundle-cases.test.ts`, `gen-serve-bundle.ts`; `difftest/servefixture/corpus.go`, `corpus_test.go`; `difftest/testdata/serve-bundle/{baseline.json,fixtures.json,cases.jsonl,golden.jsonl,pages/,inputs/}`. No prior corpus/generator output changes. CI registration belongs to Task 5.

**Interfaces:** Consumes actual frozen `loadVerifiedGitProofView`, `renderGitProofIncidentPage`, existing artifact constructors/verifiers and actual `dist/cli.js`. Produces the following additive test-only contracts. These are not new CLI commands or production overrides:

```ts
export type ServeBundleCase = {
  id: string;
  kind: "view" | "cli-error" | "cli-serve";
  fixture: string;
  expectedRoot: string | null;
  options: {
    minimizationFile?: string; expectedMinimizationDigest?: string;
    repairDirectory?: string; expectedRepairDigest?: string;
    preventionDirectory?: string; expectedPreventionDigest?: string;
  };
  argv: string[];
};
export type ServeBundleGolden = {
  id: string;
  valueJSON: string | null;
  error: string | null;
  page: string | null;
  process: { stdout: string; stderr: string; exitCode: number | null;
             signal: string | null } | null;
};
export function buildServeBundleFixtures(root: string): Promise<void>;
export function serveBundleCases(root: string): ServeBundleCase[];
export function captureServeBundleCase(root: string, c: ServeBundleCase): Promise<ServeBundleGolden>;
```

`valueJSON` is actual `JSON.stringify(view)` from TS, not canonical JSON invented by Go. `page` is a relative filename of full unmodified UTF-8 bytes, not a page digest. Failed view cases have `valueJSON/page/process = null` and exact caught message. `cli-error` has complete process output/exit; `cli-serve` has validated startup plus controlled completed SIGINT shutdown and the fetched full page. Case roots are resolved only in the adapter; embedded artifact data is never rewritten during replay.

```go
// Package servefixture is test support; cmd/fl does not import it.
type Options struct {
    MinimizationFile *string `json:"minimizationFile"`
    ExpectedMinimizationDigest *string `json:"expectedMinimizationDigest"`
    RepairDirectory *string `json:"repairDirectory"`
    ExpectedRepairDigest *string `json:"expectedRepairDigest"`
    PreventionDirectory *string `json:"preventionDirectory"`
    ExpectedPreventionDigest *string `json:"expectedPreventionDigest"`
}
type Case struct {
    ID string `json:"id"`; Kind string `json:"kind"`; Fixture string `json:"fixture"`
    ExpectedRoot *string `json:"expectedRoot"`; Options Options `json:"options"`
    Argv []string `json:"argv"`
}
type ProcessResult struct {
    Stdout string `json:"stdout"`; Stderr string `json:"stderr"`
    ExitCode *int `json:"exitCode"`; Signal *string `json:"signal"`
}
type Golden struct {
    ID string `json:"id"`; ValueJSON *string `json:"valueJSON"`
    Error *string `json:"error"`; Page *string `json:"page"`
    Process *ProcessResult `json:"process"`
}
type Corpus struct { Root string; Cases []Case; Goldens map[string]Golden }
func Load(root string) (*Corpus, error)
func (c *Corpus) CopyFixture(id, destination string) error
func (c *Corpus) Page(id string) ([]byte, error)
func (c *Corpus) FixturePath(id string) (string, error)
func (c *Corpus) ResolveOptions(id string, options Options) (Options, error)
func (c *Corpus) ResolveArgv(id, root string, port int, argv []string) ([]string, error)
```

`Load` validates exact metadata keys/values (including duplicate JSON members), the independent required case-ID list below, duplicate/missing case IDs, one matching golden per case, safe relative page/input paths, fixture file inventory SHA-256/byte lengths and digest strings. Metadata uses `encoding/json`; artifact parsing/digests never do. `CopyFixture` copies exact retained regular-file bytes to a new owned destination; it neither repairs digests nor calls Go artifact writers. Create private directories/files with 0700/0600 where POSIX modes apply, but do not inventory or compare those platform-specific permission bits: Git does not retain writer 0600 and Windows has different permission semantics. Reject links/special files in the retained portable inventory; hostile-link/special-file tests create their own inputs after copying. `ResolveOptions` resolves only path fields against the declared fixture root; expected digests remain literal. `ResolveArgv` expands only whole declared `{ROOT}` path prefixes and `{PORT}` chosen by the process harness, never arbitrary content. Fixture IDs cannot traverse outside `inputs/`.

### 1A. Schema and fixture integrity

- [ ] Write case-enumeration and corpus-integrity tests first. Each case has unique ID, known fixture, matching golden, full expected output and page where appropriate. Before creating fixture output, verify actual Node version, protected current-tree manifest and fresh CLI `FaultLine 0.1.4\n`; fail without writing on mismatch. Metadata is exactly:

```json
{"schema":"faultline.serve-bundle-oracle.v1","sourceCommit":"ce99a793af0b020bf1b9cf21114f8d7897b37207","manifestCommit":"ff5fe7b1120410e60c321f3ae620cf524c5694ec","manifestVersion":"0.1.4","nodeVersion":"22.22.2"}
```

`fixtures.json` is `{schema:"faultline.serve-bundle-fixtures.v1",fixtures:[{id,root,files:[{path,sha256,bytes}],digests:{proofRoot,minimization,repair,preventionSummary,preventionVerified},privateSentinels:string[]}]}`; roots are relative to the corpus. Inventories exclude `fixtures.json` itself and contain all fixture files. File hashes use ordinary SHA-256 for inventory, not as a substitute for actual artifact roots. Add `difftest/testdata/serve-bundle/.gitattributes` containing exactly `* -text\n` so checkout cannot convert retained capture bytes; this new corpus-local file is Task 1-owned, not a change to historical corpora. Missing inventory entries, extra files and changed bytes each have a negative integrity test.

```ts
import assert from "node:assert/strict";
import test from "node:test";
import { serveBundleCases } from "./serve-bundle-cases.js";
test("all attachment presence combinations are retained", () => {
  const ids = new Set(serveBundleCases("inputs/coherent").map(c => c.id));
  for (let bits = 0; bits < 8; bits++) {
    assert.ok(ids.has(`view-combination-${bits.toString(2).padStart(3, "0")}`));
  }
  assert.equal(ids.size, serveBundleCases("inputs/coherent").length);
});
```

- [ ] Run `RUN --name t1-case-contract-red --seconds 90 --cwd R -- pnpm exec tsx --test difftest/gen/serve-bundle-cases.test.ts`. Establish the first missing case/fixture behavior; module/import errors are infrastructure, not production TDD. No claim that frozen TS fails. Write the minimal fixture builder and explicit case table, rerun the same selector green, then run `go test ./difftest/servefixture -count=1 -timeout=60s` through a separate bounded receipt.
- [x] Construct coherent retained inputs using the direct TS pipeline below, not an unproved investigate/minimize scenario or Go-derived expected results. Bundle, minimization, repair packet and both prevention classifications refer to the same parsed investigation and witness, with distinct `frozenDigest` and `witnessDigest`. The existing `nativeDockerFixture` explicitly promotes synthetic recorded facts for a serialized fixture; fake-Docker minimization is also scripted fixture evidence, never claimed real Docker acceptance. First capture is allowed only in fresh owned scratch; preserve that scratch. Recapture NEVER rebuilds this pipeline: it reads exact committed fixture copies because bundles/minimization retain paths, versions and nonces.

In `serve-bundle-fixtures.ts`, import the following actual APIs and make `buildServeBundleFixtures(root)` create the coherent fixture at its passed absolute root (the caller passes `.../inputs/coherent`). Pin Git dates/config in this isolated first-capture process before calling it, as `gen-bases.ts:34-40` does; do not change global Git config. The core construction is:

```ts
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join, resolve, delimiter } from "node:path";
import { digestJson, sha256 } from "../../src/canonical.js";
import { investigateGitRange, GitInvestigationResultSchema } from "../../src/git-investigation.js";
import { writeGitInvestigationProofBundle, verifyGitInvestigationProofBundle } from "../../src/git-proof-bundle.js";
import { minimizeGitDiff, verifyGitMinimizationResultFile, writeGitMinimizationResult } from "../../src/git-minimization.js";
import { createRepairEvidencePacket } from "../../src/repair-brief.js";
import { writeRepairBriefArtifact, verifyRepairBriefArtifact } from "../../src/repair-brief-store.js";
import { buildPreventionProofFromVerifiedArtifacts } from "../../src/prevention-from-artifacts.js";
import { writePreventionProof, verifyPreventionProof } from "../../src/prevention-proof.js";
import { loadVerifiedGitProofView } from "../../src/git-proof-view.js";
import { createRepository, createFrozenWitness, deterministicDockerRunner, nativeDockerFixture, pinnedImage } from "./fixtures.js";

export async function buildServeBundleFixtures(root: string): Promise<void> {
  root = resolve(root);
  assert.equal(existsSync(root), false, "first-capture output already exists");
  const scratch = mkdtempSync(join(tmpdir(), "faultline-serve-fixture-"));
  const fakeDocker = resolve("difftest/testdata/fakedocker/pass-fail");
  const repo = createRepository();
  const frozen = createFrozenWitness(join(scratch,"witness-lock"), [
    {path:"private-sentinel.txt",text:"FL_PRIVATE_OVERLAY_5C_NOT_FOR_HTML\n"}
  ]);
  assert.notEqual(frozen.frozenDigest,frozen.witnessDigest);
  const observed = await investigateGitRange({
    repository:repo.root,range:{ancestor:repo.ancestor,descendant:repo.descendant},
    frozenWitness:frozen,expectedFrozenDigest:frozen.frozenDigest,
    sandbox:{mode:"DOCKER_ISOLATED",image:pinnedImage},runner:deterministicDockerRunner()
  });
  assert.equal(observed.proof.isProof,false);
  const investigation = nativeDockerFixture(observed);
  const introduction = investigation.transitions.find(t => t.kind === "PASS_TO_FAIL");
  const recovery = investigation.transitions.find(t => t.kind === "FAIL_TO_PASS");
  assert.ok(introduction); assert.ok(recovery);
  mkdirSync(root,{recursive:true});
  const bundle = writeGitInvestigationProofBundle(join(root,"bundle"),investigation,frozen,{
    proofRoot:root,generatedAt:"2026-07-16T11:03:00.000Z"
  });
  const base = verifyGitInvestigationProofBundle(bundle.directory);
  assert.equal(base.valid,true); assert.ok(base.rootDigest);
  const request = {
    repository:repo.root,before:introduction.before.commit,after:introduction.after.commit,
    frozenWitness:frozen,expectedFrozenDigest:frozen.frozenDigest,
    sandbox:{mode:"DOCKER_ISOLATED" as const,image:pinnedImage},budget:{maxExecutions:40}
  };
  const oldPath = process.env.PATH;
  process.env.PATH = fakeDocker+delimiter+(oldPath ?? "");
  try {
    const certified = await minimizeGitDiff(request);
    assert.equal(certified.proof.isProof,true);
    await writeGitMinimizationResult(join(root,"minimization.json"),certified);
    const uncertified = await minimizeGitDiff({...request,runner:deterministicDockerRunner()});
    assert.equal(uncertified.proof.isProof,false);
    await writeGitMinimizationResult(join(root,"minimization-uncertified.json"),uncertified);
  } finally {
    if (oldPath === undefined) delete process.env.PATH; else process.env.PATH = oldPath;
  }
  const parsed = GitInvestigationResultSchema.parse(JSON.parse(readFileSync(join(bundle.directory,"investigation.json"),"utf8")));
  const packet = createRepairEvidencePacket(parsed);
  const cited = (statement:string,evidenceIds:string[]) => ({statement,evidenceIds});
  const brief = {
    schemaVersion:"faultline.repair-brief.v1",evidencePacketDigest:packet.packetDigest,
    proposedInvariant:cited("FL_PRIVATE_INVARIANT_5C_NOT_FOR_HTML",["E1","E1","E2"]),
    repairDirections:[cited("FL_PRIVATE_REPAIR_5C_NOT_FOR_HTML",["E2","E3"])],
    prevention:{hardEnforcement:[cited("FL_PRIVATE_HARD_5C_NOT_FOR_HTML",["E1"])],
      softGuidance:[cited("FL_PRIVATE_SOFT_5C_NOT_FOR_HTML",["E2"])]},
    uncertainties:["FL_PRIVATE_DIAGNOSTIC_5C_NOT_FOR_HTML"]
  };
  const oldCwd = process.cwd();
  process.chdir(root);
  try {
    const repair = writeRepairBriefArtifact(join(root,".faultline/repair-briefs/attached"),packet,brief,{kind:"OFFLINE_INPUT"});
    assert.equal(verifyRepairBriefArtifact(repair.directory).valid,true);
    const environmentDigest = parsed.environment.distinctDigests[0]; assert.ok(environmentDigest);
    const repaired = recovery.after;
    const runs = repaired.runIds.map((runId,i) => ({
      runId,executionId:repaired.executionIds[i]!,commit:repaired.commit,tree:repaired.tree,
      verdict:"PASS" as const,witnessDigest:frozen.witnessDigest,environmentDigest,
      executionTrust:"NATIVE_DOCKER" as const,executionKind:"EXECUTED" as const
    }));
    const repairPatch = execFileSync("git",["-C",repo.root,"diff","--binary","--no-ext-diff","--no-textconv",
      introduction.after.commit,repaired.commit,"--"],{maxBuffer:4*1024*1024});
    const built = buildPreventionProofFromVerifiedArtifacts({bundleDirectory:bundle.directory,
      expectRoot:base.rootDigest,repaired:{commit:repaired.commit,tree:repaired.tree,runs},
      repairPatchDigest:`sha256:${sha256(repairPatch)}`});
    assert.equal(built.ok,true); if (!built.ok) throw new Error(built.reasons.join("; "));
    const verified = writePreventionProof(join(root,".faultline/prevention-proofs/verified"),built.input);
    const summary = writePreventionProof(join(root,".faultline/prevention-proofs/summary"),{
      ...built.input,grounding:"SUMMARY",repairedRunBindings:undefined
    });
    const {investigationDigest:_omitted,...withoutInvestigation} = built.input;
    writePreventionProof(join(root,".faultline/prevention-proofs/no-investigation"),{
      ...withoutInvestigation,grounding:"SUMMARY",repairedRunBindings:undefined
    });
    assert.equal(verifyPreventionProof(verified.directory).valid,true);
    assert.equal(verifyPreventionProof(summary.directory).valid,true);
    const minimization = verifyGitMinimizationResultFile(join(root,"minimization.json"));
    assert.equal(minimization.valid,true); assert.ok(minimization.resultDigest);
    const view = loadVerifiedGitProofView(bundle.directory,base.rootDigest,{
      minimizationFile:join(root,"minimization.json"),expectedMinimizationDigest:minimization.resultDigest,
      repairDirectory:repair.directory,expectedRepairDigest:repair.manifest.manifestDigest,
      preventionDirectory:verified.directory,expectedPreventionDigest:verified.manifest.rootDigest
    });
    assert.equal(view.attachments.minimization?.result.proof.isProof,true);
    assert.equal(view.attachments.prevention?.manifest.classification,"PREVENTION_VERIFIED");
    assert.equal(view.attachments.repair?.packet.investigationDigest,digestJson(parsed));
  } finally { process.chdir(oldCwd); }
}
```

First-capture runs on the pinned Linux oracle with the existing POSIX fake Docker and real trusted Git. Permanent fixture replay does not run the fake Docker on Windows. The first pipeline test asserts proof, links and actual loader success before any case table depends on it; if TS rejects the proposed fixture, fix only this new builder and retain the failed attempt. Do not mutate the frozen source or weaken a verifier. Private-sentinel inventory also records the actual omitted witness behavior, command, approval reviewer and source paths from this new fixture. Citation `E10` ordering belongs to a direct renderer test unless the actual generated packet contains E10; do not invent a verifier-valid E10 citation. Add malformed/linkage variants as explicit fresh-copy mutations with TS recomputed valid digests where the source verifier permits them, and capture its actual first refusal rather than forcing a later adapter error.

### 1B. Case ownership and capture

- [x] Enumerate the stable case families below in `serve-bundle-cases.ts`; every variant gets an explicit stable ID and exact expected TS capture. Base-combination bits mean minimization/repair/prevention in that order; `000` is base only (eight total, not nine).

| Family | Concrete variants |
| --- | --- |
| `view-combination-000` through `111` | Retained expected base root; certified minimization, valid repair, valid summary prevention; full view JSON and page |
| `view-root-*` | No expected root, matching root, wrong root; original external status retained |
| `view-min-*` | Valid uncertified result; expected digest absent/match/wrong; null witness, either witness digest wrong; null before/after; wrong before commit/tree, after commit/tree, non-PASS_TO_FAIL base transition |
| `view-repair-*` | Expected absent/match/wrong; parsed investigation/frozen binding wrong; current valid packet, legacy packet under existing verifier acceptance/rejection; invalid citations; malformed manifest/packet/brief; mismatched manifest/content |
| `view-prevention-*` | Both classifications; expected absent/match/wrong; wrong original root, witness or present investigation digest; absent optional investigation digest; grounded repaired-runs wrong/missing |
| `view-private-*` | Private behavior/command/overlay/reviewer/path/output/patch/packet/guidance/diagnostic sentinels; legitimate packet citations with duplicate invariant reference count, prevention-over-repair and prevention-only hero |
| `cli-options-*` | Required bundle, duplicate/equals/missing/empty/prefixed values; six one-sided attachment pairs; all-value parse-before-pair checks; min→repair→prevention pair precedence; root option consumed raw |
| `cli-order-*` | Pair error before missing bundle I/O; invalid base before invalid/occupied port; invalid attachment before port; missing later value before earlier pair error |
| `cli-port-*` | Default 4173, zero, empty, missing value, next flag as value, whitespace, hex, exponent, fractional, negative, NaN, infinity, 65536, occupied; valid load before coercion |
| `cli-serve-*` | All eight valid combinations; no root; ignored output/browser/serve-ms flags; exact two-line startup, full page, clean completed SIGINT output/exit |

Pin the required ID set independently in `serve-bundle-cases.test.ts` and `servefixture/corpus_test.go`; neither expected set may be derived from `cases.jsonl`, `golden.jsonl` or the implementation's exported case rows. `corpus.go` also carries this fixed required-ID contract so every consumer of `Load`, not just the integrity test, rejects missing/unknown IDs. Expand the following test-owned table as `prefix + suffix` (plus all eight three-bit combination IDs). Every listed ID is required; additions require an explicit expectation-table update, not automatic discovery:

```text
view-root-: absent match wrong
view-min-: uncertified expected-absent expected-match expected-wrong witness-null frozen-wrong witness-wrong before-null after-null before-commit before-tree after-commit after-tree wrong-transition
view-repair-: expected-absent expected-match expected-wrong investigation-wrong frozen-wrong legacy citations-invalid manifest-malformed packet-malformed brief-malformed manifest-content
view-prevention-: summary verified expected-absent expected-match expected-wrong root-wrong witness-wrong investigation-wrong investigation-absent runs-wrong runs-missing
view-private-: sentinels prevention-priority prevention-only-hero
cli-options-: bundle-missing bundle-empty bundle-prefixed bundle-duplicate bundle-equals duplicate-first equals-ignored min-path-only min-digest-only repair-path-only repair-digest-only prevention-path-only prevention-digest-only all-values-first pair-min-first pair-repair-first root-raw relative-paths
cli-order-: pair-before-io base-before-port attachment-before-port later-value-before-pair
cli-port-: default zero empty missing next-flag whitespace hex exponent fractional negative nan infinity out-of-range occupied
cli-serve-: 000 001 010 011 100 101 110 111 no-root ignored-flags
```

All `view-combination-*` cases have kind `view`; all `cli-serve-*` have kind `cli-serve`. Valid default/zero/empty/missing/whitespace/hex/exponent port cases also use `cli-serve`; malformed/occupied cases use `cli-error`. A duplicate bundle case supplies a valid first path and invalid second path to prove first occurrence wins. `cli-options-bundle-equals` includes a later exact `--bundle` so actual TS/Go bundle handling remains in scope; the separate only-equals Go refusal regression is not a TS parity capture. For the portable duplicate-flag and `next-flag` cases specify exact argv in the new row and retain its actual outcome, not an assumed missing-value diagnostic.

For CLI-reachable bad attachments, also create `cli-reject-<view-case-id>` rows with the same copied input and complete path/digest pairs; pin this explicit source list independently in both integrity tests:

```text
view-min-expected-wrong view-min-frozen-wrong view-min-witness-wrong view-min-before-commit view-min-before-tree view-min-after-commit view-min-after-tree view-min-wrong-transition
view-repair-expected-wrong view-repair-investigation-wrong view-repair-frozen-wrong view-repair-citations-invalid view-repair-manifest-malformed view-repair-packet-malformed view-repair-brief-malformed view-repair-manifest-content
view-prevention-expected-wrong view-prevention-root-wrong view-prevention-witness-wrong view-prevention-investigation-wrong view-prevention-runs-wrong view-prevention-runs-missing
```

These actual-binary replays exercise ordinary adapter error positions for coverage, not just the in-process View tests. If an underlying verifier rejects a proposed mutation before an adapter binding check, retain that real refusal and exercise the private predicate separately; do not assert a later check ran or weaken verifier semantics. Integrity negative tests delete one required ID, duplicate one, add an unknown ID and remove one full page, each requiring rejection even if cases and goldens are edited together.

Pure renderer fuzz/escaping fields that schemas forbid belong to Task 3's synthetic View tests, not falsely valid fixture packages. E10 and arbitrary Unicode citation ordering are direct-renderer coverage, not a claim that the coherent packet includes E10. Native filesystem error strings, inherited/default signals and Windows console termination are live platform cases owned by Task 4, not Linux strings declared universal. Plain serve and only `--bundle=value` use explicit Go not-ported regressions, not a claim of TS plain-serve parity.

- [x] Implement real capture orchestration: API view calls use the frozen loader/renderer; CLI cases launch actual `node dist/cli.js`. For a server, read the exact two startup lines, parse `http://127.0.0.1:<port>`, require bound port in 1–65535, fetch that concrete URL, verify the response, then normalize only this proven startup port to `{PORT}`. Compare/fetch before substitution. Only declared case-root occurrences in process diagnostics become `{ROOT}`; do not normalize roots inside artifacts or HTML. Date handling belongs to HTTP comparison, not a blanket stream replacement.
- [x] Implement three mutually exclusive modes: `--capture-fixtures` builds in a fresh owned staging tree and writes the new corpus only if its destination is absent; `--recapture` copies committed inputs into fresh owned scratch and compares all recaptured bytes/inventory without writing the corpus; `--check` first strictly validates inventory/metadata/case alignment, then performs that same no-write recapture. Default is `--check`, never rebuilding inputs. `--only <exact-id>` is diagnostic selection and cannot mark whole-corpus acceptance. Preserve first output and all failures. No dependency on Go output in any TS expected-value capture.

```ts
// The golden always comes from the frozen implementation.
import { loadVerifiedGitProofView, renderGitProofIncidentPage } from "../../src/git-proof-view.js";
export function captureView(directory: string, expectedRoot: string | undefined,
  options: Parameters<typeof loadVerifiedGitProofView>[2]) {
  const view = loadVerifiedGitProofView(directory, expectedRoot, options);
  return { valueJSON: JSON.stringify(view), html: renderGitProofIncidentPage(view) };
}
```

- [ ] Run separate bounded first-capture and no-write verification payloads: `pnpm exec tsx difftest/gen/gen-serve-bundle.ts --capture-fixtures`, then `pnpm exec tsx difftest/gen/gen-serve-bundle.ts --recapture`, then `pnpm exec tsx difftest/gen/gen-serve-bundle.ts --check`. Expected: identical new fixture/page/golden inventory, no prior testdata changes. If the whole capture cannot fit 240 seconds, use hosted CI; `--only` diagnosis is not an aggregate pass. Do not restart a watchdog around a still-running capture.
- [x] Controller reads completed report and full outputs, snapshots the task diff and fixture inventory, then dispatches independent spec-compliance + task-quality review. No commit. Gate: real TS artifacts verify, corpus integrity and no-write recapture pass; approved task review. This is evidence infrastructure, not Go feature acceptance.

## Task 2: Add verified attachment loading without weakening existing verifiers

**Files:** Modify `internal/bundle/prevention/verify.go`, loader/model portions of `internal/proofview/judge.go`. Create `internal/bundle/prevention/verify_artifacts_test.go`, `internal/proofview/{attachments,attachment_minimization,attachment_repair,attachment_prevention}.go` and `{attachments,attachments_recheck,attachments_hostile}_test.go`.

**Interfaces:** Consumes Task 1 retained inputs and existing `minimize.VerifyFile`, `repair.VerifyBriefArtifact`, `repair.ValidateBrief`, `bundle.ParseValue/SameCanonical`, `canonical.DigestJSON`, `gitproof.VerifyWithManifest`. Produces exactly:

```go
// internal/bundle/prevention
func VerifyWithArtifacts(directory, expectedRoot string, rootProvided bool) (bundle.Result, jsjson.Value, jsjson.Value)
// Existing Verify(directory, expectedRoot string, rootProvided bool) bundle.Result stays unchanged.

// internal/proofview
type AttachmentOptions struct {
    MinimizationFile, ExpectedMinimizationDigest *string
    RepairDirectory, ExpectedRepairDigest *string
    PreventionDirectory, ExpectedPreventionDigest *string
}
type VerifiedMinimizationAttachment struct {
    Result jsjson.Value
    ResultDigest, ExternalDigestStatus string
}
type VerifiedRepairAttachment struct {
    Manifest, Packet, Brief jsjson.Value
    ExternalDigestStatus string
}
type VerifiedPreventionAttachment struct {
    Manifest, Prevention jsjson.Value
    RootDigest, ExternalDigestStatus string
}
type VerifiedAttachments struct {
    Minimization *VerifiedMinimizationAttachment
    Repair *VerifiedRepairAttachment
    Prevention *VerifiedPreventionAttachment
}
// Add Attachments VerifiedAttachments to the existing View; preserve all other fields.
func LoadVerifiedGitProofViewWithAttachments(directory string, expectedRoot *string, options AttachmentOptions) (View, error)
// Existing LoadVerifiedGitProofView(directory string, expectedRoot *string) (View,error) is preserved.

type repairContents struct { packet, brief jsjson.Value }
type attachmentDependencies struct {
    minimize func(string, string, bool) minimize.Verification
    repair func(string, *string) repair.BriefVerification
    readRepair func(string) repairContents
    prevention func(string, string, bool) (bundle.Result, jsjson.Value, jsjson.Value)
}
func realAttachmentDependencies() attachmentDependencies
func loadAttachments(options AttachmentOptions, investigation, frozenWitness jsjson.Value, proofRoot *string, deps attachmentDependencies) VerifiedAttachments
func loadMinimization(file string, expected *string, investigation, frozenWitness jsjson.Value, deps attachmentDependencies) *VerifiedMinimizationAttachment
func loadRepair(directory string, expected *string, investigation, frozenWitness jsjson.Value, deps attachmentDependencies) *VerifiedRepairAttachment
func loadPrevention(directory string, expected *string, investigation, frozenWitness jsjson.Value, proofRoot string, deps attachmentDependencies) *VerifiedPreventionAttachment
```

Private functions throw with existing `jsexc.Throw`; public loader wraps `jsexc.Try` and returns a zero View on failure. Nil path means undefined, not empty string; supplied empty strings still resolve like TS at library level. Dependency values are created per call; no mutable package-level replacement. `loadAttachments` runs min, repair, prevention sequentially. Missing prevention base root is checked only when the prevention path exists, after any earlier attachment work.

### 2A. Same-invocation prevention artifacts

- [ ] Before refactoring write a table against existing valid summary/verified and hostile/malformed fixtures: existing `Verify` verdict/errors/root/status/classification equals the sibling's verdict, and successful returned parsed manifest/body equal the retained TS-parsed objects. Invalid/partial parse cases must expose only values actually parsed; caller rejects absent/partial values.

```go
func TestVerifyWithArtifactsPreservesVerdict(t *testing.T) {
    for _, name := range []string{"prevention-summary", "prevention-verified"} {
        directory := filepath.Join("..", "..", "..", "difftest", "testdata", "bases", name)
        want := Verify(directory, "", false)
        got, manifest, body := VerifyWithArtifacts(directory, "", false)
        if !reflect.DeepEqual(got, want) { t.Fatalf("%s: got %+v want %+v", name, got, want) }
        if !got.Valid || manifest.Kind() != jsjson.Object || body.Kind() != jsjson.Object {
            t.Fatalf("%s: missing verified artifacts", name)
        }
    }
}
```

- [ ] Run `go test ./internal/bundle/prevention -run '^TestVerifyWithArtifacts' -count=1 -timeout=60s -v` through RUN. Missing sibling compilation establishes API absence only; add/run the semantic assertions before treating the refactor accepted. Move the existing verifier body into one private `verifyWithArtifacts` function with named result/manifest/body returns; both public wrappers call it once. Retain every existing early result and deferred catch/error/status/classification branch. Remove shadow declarations of parsed manifest/body; keep `haveManifest/havePrevention` flags. The catch must update the returned verdict while retaining the values parsed before failure. Do not add another read or duplicate validation body.

```go
func Verify(directory, expectedRoot string, rootProvided bool) bundle.Result {
    result, _, _ := verifyWithArtifacts(directory, expectedRoot, rootProvided)
    return result
}
func VerifyWithArtifacts(directory, expectedRoot string, rootProvided bool) (bundle.Result, jsjson.Value, jsjson.Value) {
    return verifyWithArtifacts(directory, expectedRoot, rootProvided)
}
```

- [ ] Rerun that selector and all `./internal/bundle/prevention` tests separately green. Compare exact invalid outputs, not only `Valid`. Later actual serve CLI stack tests remain required; unchanged `repair verify`/`prevention verify` results do not establish new call-site parity.

### 2B. Model, library contract and minimization

- [ ] Write failing real-fixture tests for all eight attachment combinations and digest asymmetry before loader production edits. Helpers read the new retained corpus, not source/manifests/Git history. A concrete base-status regression uses only already existing APIs:

```go
func TestAttachmentBaseExternalStatusIsNotPromoted(t *testing.T) {
    dir := filepath.Join("..", "..", "difftest", "testdata", "serve-bundle", "inputs", "coherent", "bundle")
    v, err := LoadVerifiedGitProofView(dir, nil)
    if err != nil { t.Fatal(err) }
    if v.ExternalRootStatus != "NOT_PROVIDED" { t.Fatalf("status %q", v.ExternalRootStatus) }
}
```

For the new API tests compare success/failure, complete View projection and final returned snapshots against TS captures. Zero options and expected-digest-only options must not touch nonexistent paths. Capture meaningful existing-loader absence of attachments after the new API forwards to the old loader, before implementing attachment behavior; a missing symbol is not the sole TDD evidence.

- [ ] Run the `^TestAttachment` selector using the complete command above; expected red: requested verified attachment is absent or binding is not enforced. Add the types and explicit new entry point. Keep base verify/read/verify/read/verify code intact and preserve original external-root status. Have the old public loader delegate to new entry point with zero options; private existing sample/preview loader also supplies zero options. Append attachment loading only after all final base conditions pass:

```go
attachments := loadAttachments(options, recheckedArtifacts.investigation,
    recheckedArtifacts.frozenWitness, &rootDigest, realAttachmentDependencies())
// Existing return fields remain unchanged; add Attachments: attachments.
```

The private base loader accepts `options AttachmentOptions` as its third argument; update its two private callers together. The public two-argument signature does not change. Do not turn internal root pinning into external `MATCH`.

- [x] Implement minimization using the exact existing verifier twice. The binding validator accumulates errors in TS order (null witness OR frozen mismatch then witness mismatch; null endpoints OR exact PASS_TO_FAIL endpoint mismatch). Production core:

```go
func optionalDigest(expected *string) (string, bool) {
    if expected == nil { return "", false }; return *expected, true
}
func attachmentFailure(message string) { jsexc.Throw(errors.New(message)) }
func validateMinimizationBinding(result, investigation, frozen jsjson.Value) []string {
    var errs []string
    w := result.Get("witness")
    if w.Kind() == jsjson.Null {
        errs = append(errs, "minimization has no frozen-witness summary")
    } else {
        if w.Get("frozenDigest").Str() != frozen.Get("frozenDigest").Str() { errs = append(errs, "minimization frozen digest does not match the Git proof witness") }
        if w.Get("witnessDigest").Str() != frozen.Get("witnessDigest").Str() { errs = append(errs, "minimization witness digest does not match the Git proof witness") }
    }
    before, after := result.Get("before"), result.Get("after")
    if before.Kind() == jsjson.Null || after.Kind() == jsjson.Null {
        errs = append(errs, "minimization has no resolved before/after states")
    } else {
        matches := false
        for _, tr := range investigation.Get("transitions").Items() {
            if tr.Get("kind").Str() == "PASS_TO_FAIL" &&
                tr.Get("before", "commit").Str() == before.Get("commit").Str() &&
                tr.Get("before", "tree").Str() == before.Get("tree").Str() &&
                tr.Get("after", "commit").Str() == after.Get("commit").Str() &&
                tr.Get("after", "tree").Str() == after.Get("tree").Str() { matches = true; break }
        }
        if !matches { errs = append(errs, "minimization range is not a stable PASS_TO_FAIL transition in the Git proof package") }
    }
    return errs
}
func loadMinimization(file string, expected *string, investigation, frozenWitness jsjson.Value, deps attachmentDependencies) *VerifiedMinimizationAttachment {
    want, provided := optionalDigest(expected)
    first := deps.minimize(nodefs.Resolve(file), want, provided)
    if !first.Valid || first.Result == nil || first.ResultDigest == nil {
        attachmentFailure(jsexc.Concat("Refusing to attach an invalid minimization record: ", jsexc.Join(first.Errors, "; ")))
    }
    if linkage := validateMinimizationBinding(*first.Result, investigation, frozenWitness); len(linkage) != 0 {
        attachmentFailure(jsexc.Concat("Refusing to attach a minimization record for a different incident: ", jsexc.Join(linkage, "; ")))
    }
    second := deps.minimize(nodefs.Resolve(file), want, provided)
    if !second.Valid || second.Result == nil || second.ResultDigest == nil ||
        *second.ResultDigest != *first.ResultDigest || !bundle.SameCanonical(*second.Result, *first.Result) {
        detail := jsexc.Join(second.Errors, "; "); if detail == "" { detail = "digest changed" }
        attachmentFailure(jsexc.Concat("Refusing to attach a minimization record that changed during verification: ", detail))
    }
    return &VerifiedMinimizationAttachment{Result:*second.Result, ResultDigest:*second.ResultDigest, ExternalDigestStatus:second.ExternalDigestStatus}
}
```

No `proof.isProof` or status gate is added. The real verifier's regular/non-symlink/128 MiB checks remain authoritative. Run null/mismatch/transition variants individually red→green; then rerun the entire Task 2 selector.

### 2C. Repair and prevention rechecks

- [ ] Write real-fixture and staged-boundary tests before each adapter. A repair read uses existing `readJSON` with labels `repair evidence packet` and `repair brief`, schema-parses with `repair.EvidencePacketSchema` and `repair.BriefSchema`, calls `repair.ValidateBrief`, and returns the validated Brief. Preserve read wrapper and schema exception behavior:

```go
func readRepairContents(root string) repairContents {
    packet := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(root,"evidence-packet.json"),"repair evidence packet"),repair.EvidencePacketSchema))
    brief := bundle.Must(bundle.ParseValue(readJSON(nodefs.Join(root,"repair-brief.json"),"repair brief"),repair.BriefSchema))
    validation := repair.ValidateBrief(packet,brief)
    if !validation.Valid || validation.Brief.Kind() == jsjson.Undefined || validation.Brief.Kind() == jsjson.Null {
        attachmentFailure(jsexc.Concat("Refusing to attach an invalid repair brief payload: ",jsexc.Join(validation.Errors,"; ")))
    }
    return repairContents{packet:packet,brief:validation.Brief}
}
func manifestMatchesRepairContents(manifest jsjson.Value, contents repairContents) bool {
    return manifest.Get("evidencePacket","digest").Str() == contents.packet.Get("packetDigest").Str() &&
        manifest.Get("repairBrief","digest").Str() == bundle.Must(canonical.DigestJSON(contents.brief))
}
func sameRepairContents(a,b repairContents) bool {
    return bundle.SameCanonical(a.packet,b.packet) && bundle.SameCanonical(a.brief,b.brief)
}
```

- [x] Port the repair body in `src/incident-attachments.ts:173-218` with these exact dependencies and predicates. Its ordered actions are explicit, not optional:

```text
first = deps.repair(resolve(directory), expected)
invalid or absent manifest → "Refusing to attach an invalid repair brief: " + joined errors
one = deps.readRepair(root)
!manifestMatchesRepairContents(first.Manifest,one) → "Refusing to attach repair guidance whose in-memory payload does not match its verified manifest."
one.packet.investigationDigest != canonical.DigestJSON(investigation) → "Refusing to attach repair guidance from a different Git investigation."
one.packet.frozenWitnessDigest != frozenWitness.frozenDigest → "Refusing to attach repair guidance for a different frozen witness."
second = deps.repair(root,expected)
invalid/absent/different canonical manifest → changed-prefix + joined errors, fallback "manifest changed"
two = deps.readRepair(root)
third = deps.repair(root,expected)
invalid/absent/different canonical manifest OR !sameRepairContents(one,two) OR !manifestMatchesRepairContents(third.Manifest,two)
    → changed-prefix + joined errors, fallback "artifact contents changed"
return third.Manifest, two.packet, two.brief, third.ExternalDigestStatus
changed-prefix = "Refusing to attach repair guidance that changed during verification: "
```

Use `jsjson.Undefined` as the existing Go verifier's absent-manifest sentinel; also reject Null from staged tests. Empty error arrays intentionally produce the source's empty suffix on initial invalid verification, not a newly invented fallback. `canonical.DigestJSON(investigation)` takes the schema-parsed base View value. Do not lstat in the adapter's extra reads or claim universal race confinement.

- [x] Port prevention with the shared artifact-returning verifier, no read helper:

```text
first,manifest,body = deps.prevention(resolve(directory), optionalDigest(expected))
invalid/absent manifest/body/root → "Refusing to attach an invalid prevention proof: " + joined errors
body.originalProofRoot != verified proofRoot → "Refusing to attach a prevention proof bound to a different Git proof root."
body.frozenWitnessDigest != frozenWitness.witnessDigest → "Refusing to attach a prevention proof for a different frozen witness."
present body.investigationDigest != canonical.DigestJSON(investigation) → "Refusing to attach a prevention proof from a different Git investigation."
second,manifest2,body2 = deps.prevention(root, optionalDigest(expected))
invalid/absent fields OR root differs OR !bundle.SameCanonical(body,body2)
    → "Refusing to attach a prevention proof that changed during verification: " + joined errors, fallback "digest changed"
return manifest2, body2, second root, second.ExternalRootStatus
```

`loadAttachments` missing-root failure is exactly `Prevention attachment requires the verified Git proof root digest.`; an expected prevention digest without a path does not trigger it. Both valid classifications attach. Do not add separate manifest/repaired-runs equality or change classification semantics.

### 2D. Deterministic boundary and hostile-file gate

- [x] Make staged dependencies local values and assert exact event traces: min `verify,verify`; repair `verify,read,verify,read,verify`; prevention `verify,verify`; attachment order min→repair→prevention. Prove base-before-attachment failure precedence separately with a real invalid base plus invalid attachment and require the base error; do not invent an unplanned base-verifier seam. Each staged callback fails immediately on an unexpected invocation. Stage changes independently: first failure; second invalid/result/digest; repair first contents/second manifest/second packet/second brief/final manifest/final-content check; prevention final invalid/root/body. Distinct final statuses/snapshots demonstrate which pass is returned. Capture panics only through `jsexc.Try`, not broad `recover` that hides coding defects.
- [x] Add real-file hostile cases in copied fixtures: wrong path type, root/child symlink, missing/extra artifact, malformed/oversized minimization and retained-digest mismatch; special file on platforms that can create it. Symlink privilege or special-file nonapplicability must have exact explicit existing eligibility, while portable regular-file rejection tests run everywhere. No new global skip. These test actual verifier rejection separately from staged orchestration.
- [ ] Run separately through RUN: `go test ./internal/proofview -run '^TestAttachment' -count=1 -timeout=180s -v`; `go test ./internal/bundle/prevention -count=1 -timeout=90s`; `go test ./internal/proofview ./internal/repair ./internal/minimize -run '^(TestAttachment|TestVerif|TestPreviewOfCommittedSample)' -count=1 -timeout=180s`. Expected: green with original absent-view behavior and no suppressed failures. Full suites remain hosted if too large locally.
- [x] Snapshot production/tests/corpus hashes and dispatch independent task spec/quality review. Review specifically checks nil-pointer short-circuiting, old verifier early/catch returns, no unchecked prevention reread, original root status, parsed-value bindings, digest asymmetry and exact recheck counts. No commit.

## Task 3: Render supplied panels and add permanent Go-only replay

**Files:** Create `internal/proofview/attachment_panels.go`, `attachment_panels_test.go`, `difftest/serve_bundle_test.go`; modify only existing attachment constants/call sites and hero-summary selection in `internal/proofview/judge.go`.

**Interfaces:** Consumes `View.Attachments` and types from Task 2 and Task 1 Corpus. Produces private `minimizationPanel(view View) string`, `repairPanel(view View) string`, `preventionGuidancePanel(view View) string`, `citationSummary(items []jsjson.Value) string`; existing `RenderGitProofIncidentPage(View) string` signature/output contract is unchanged. Tagged permanent entry point is `TestServeBundleReplay` plus `TestServeBundleMetadata`; no Node/Git-history/source/manifest preflight.

### 3A. Byte-level red and pure rendering

- [ ] Write full-page comparisons for all eight combinations before production rendering edits. The existing loader now attaches data but renderer still emits absent branches; this is meaningful red. A concrete test consumes retained TS bytes directly:

```go
func TestAttachmentPanelsFullPage(t *testing.T) {
    root := filepath.Join("..","..","difftest","testdata","serve-bundle")
    corpus,err := servefixture.Load(root); if err != nil { t.Fatal(err) }
    dir,err := corpus.FixturePath("coherent"); if err != nil { t.Fatal(err) }
    for _,tc := range corpus.Cases {
        if !strings.HasPrefix(tc.ID,"view-combination-") { continue }
        t.Run(tc.ID,func(t *testing.T) {
            options,err := corpus.ResolveOptions(tc.Fixture,tc.Options); if err != nil { t.Fatal(err) }
            v,err := LoadVerifiedGitProofViewWithAttachments(filepath.Join(dir,"bundle"),tc.ExpectedRoot,AttachmentOptions{
                MinimizationFile:options.MinimizationFile,ExpectedMinimizationDigest:options.ExpectedMinimizationDigest,
                RepairDirectory:options.RepairDirectory,ExpectedRepairDigest:options.ExpectedRepairDigest,
                PreventionDirectory:options.PreventionDirectory,ExpectedPreventionDigest:options.ExpectedPreventionDigest,
            })
            if err != nil { t.Fatal(err) }
            want,err := corpus.Page(tc.ID); if err != nil { t.Fatal(err) }
            if got := []byte(jsstr.ToUTF8(RenderGitProofIncidentPage(v))); !bytes.Equal(got,want) {
                t.Fatalf("full page differs: got %d bytes, want %d",len(got),len(want))
            }
        })
    }
}
```

All eight combination cases use fixture `coherent`. The strict Corpus loader pins these IDs independently, so deleting a case cannot make this loop vacuously pass. Expected base root and attachment paths/digests always come from that same case; root-absent behavior has a separate golden.

- [ ] Run `go test ./internal/proofview -run '^TestAttachmentPanels' -count=1 -timeout=90s -v` through RUN, require the supplied-panel byte mismatch, then rename the three old constants to `absentMinimizationPanel`, `absentRepairPanel`, `absentPreventionGuidancePanel` without changing their bytes. New functions return those constants on nil attachments. Keep the old absent-summary string; choose the source's supplied summary only when minimization OR repair exists, not prevention alone.

```go
func citationSummary(items []jsjson.Value) string {
    seen := map[string]bool{}
    var ids []string
    for _,item := range items {
        for _,v := range item.Get("evidenceIds").Items() {
            id := v.Str()
            if !seen[id] { jsexc.SetAdd(seen,id); ids = append(ids,id) }
        }
    }
    slices.SortFunc(ids,jsstr.CompareUTF16)
    return `<div class="proof"><span>Validated citations</span><small>[` + EscapeHTML(jsexc.Join(ids,", ")) + `]</small></div>`
}
```

- [x] Add these complete panel functions to `attachment_panels.go`. Imports are `strconv`, `slices` and existing `jsexc/jsjson/jsstr` packages; `slices/jsexc/jsstr` are used by `citationSummary` above. Existing `count`, `underscoresToSpaces`, `EscapeHTML`, `shortDigest`, `shortCommit` and `verdictBadge` remain shared in this package.

```go
func minimizationPanel(view View) string {
    attachment := view.Attachments.Minimization
    if attachment == nil { return absentMinimizationPanel }
    result := attachment.Result
    proof,summary := "NOT CERTIFIED AS COUNTERFACTUAL PROOF","The attached record did not establish both counterfactual directions."
    if result.Get("proof","isProof").Bool() {
        proof,summary = "BIDIRECTIONALLY CERTIFIED","The attached record certifies both counterfactual directions for its selected units."
    }
    oneMinimal := "not established"
    if result.Get("minimality","oneMinimal").Bool() { oneMinimal = "recorded" }
    return `<article class="card panel"><h3>Counterfactual minimization</h3>
    <p><strong>` + EscapeHTML(proof) + `</strong><br><small>` + EscapeHTML(summary) + `</small></p>
    <div class="proof"><span>Candidate units</span><strong>` + count(result.Get("candidateUnitIds")) + `</strong></div>
    <div class="proof"><span>1-minimal</span><small>` + oneMinimal + `</small></div>
    <div class="proof"><span>Sufficiency</span><small>` + EscapeHTML(underscoresToSpaces(result.Get("certification","sufficiency","status").Str())) + `</small></div>
    <div class="proof"><span>Necessity</span><small>` + EscapeHTML(underscoresToSpaces(result.Get("certification","necessity","status").Str())) + `</small></div>
    <div class="proof"><span>Attachment digest</span><code title="` + EscapeHTML(attachment.ResultDigest) + `">` + EscapeHTML(shortDigest(attachment.ResultDigest)) + `</code></div>
    <small>Separately verified record; external digest: ` + EscapeHTML(attachment.ExternalDigestStatus) + `. File paths, patch bytes, raw output, and free-form diagnostic text are intentionally omitted.</small>
  </article>`
}

func repairPanel(view View) string {
    attachment := view.Attachments.Repair
    if attachment == nil { return absentRepairPanel }
    brief := attachment.Brief
    refs := len(brief.Get("proposedInvariant","evidenceIds").Items())
    suffix := "s"; if refs == 1 { suffix = "" }
    items := []jsjson.Value{brief.Get("proposedInvariant")}
    items = append(items,brief.Get("repairDirections").Items()...)
    return `<article class="card panel"><h3>Evidence-bounded repair guidance</h3>
    <p><span class="pill">INFERRED</span> Citation-validated guidance from a separately verified repair artifact; it is not an executed repair or proof of model intent.</p>
    <div class="proof"><span>Cited invariant</span><small>` + strconv.Itoa(refs) + ` evidence reference` + suffix + `</small></div>
    <div class="proof"><span>Cited repair directions</span><strong>` + count(brief.Get("repairDirections")) + `</strong></div>
    ` + citationSummary(items) + `
    <small>Artifact ` + EscapeHTML(shortDigest(attachment.Manifest.Get("manifestDigest").Str())) + ` is bound to this proof's investigation and frozen-witness digests; external digest: ` + EscapeHTML(attachment.ExternalDigestStatus) + `. Free-form guidance remains in the private repair artifact and is deliberately not rendered in this shareable proof view.</small>
  </article>`
}

func preventionGuidancePanel(view View) string {
    prevention := view.Attachments.Prevention
    if prevention != nil {
        body := prevention.Prevention
        heading,pill := "Prevention evidence summary","PREVENTION_EVIDENCE_SUMMARY"
        summary := "Offline-checked three-state package bound to this proof root and frozen witness. Prefer a grounded package from <code>fl prevention write --from-bundle</code>."
        if prevention.Manifest.Get("classification").Str() == "PREVENTION_VERIFIED" {
            heading,pill = "Prevention verified","Prevention verified"
            summary = "Grounded three-state package: last-good / first-bad run IDs from this proof root plus repaired-state NATIVE_DOCKER bindings under the same frozen witness."
        }
        return `<article class="card panel"><h3>` + heading + `</h3>
      <p><span class="pill">` + pill + `</span> ` + summary + `</p>
      <div class="proof"><span>Last good</span><code title="` + EscapeHTML(body.Get("lastGood","commit").Str()) + `">` + EscapeHTML(shortCommit(body.Get("lastGood","commit").Str())) + `</code> ` + verdictBadge(body.Get("lastGood","verdict").Str()) + `</div>
      <div class="proof"><span>First bad</span><code title="` + EscapeHTML(body.Get("firstBad","commit").Str()) + `">` + EscapeHTML(shortCommit(body.Get("firstBad","commit").Str())) + `</code> ` + verdictBadge(body.Get("firstBad","verdict").Str()) + `</div>
      <div class="proof"><span>Repaired</span><code title="` + EscapeHTML(body.Get("repaired","commit").Str()) + `">` + EscapeHTML(shortCommit(body.Get("repaired","commit").Str())) + `</code> ` + verdictBadge(body.Get("repaired","verdict").Str()) + `</div>
      <div class="proof"><span>Prevention root</span><code title="` + EscapeHTML(prevention.RootDigest) + `">` + EscapeHTML(shortDigest(prevention.RootDigest)) + `</code></div>
      <small>Claims only PASS→FAIL→PASS under the same frozen witness in NATIVE_DOCKER. Does not claim model intent or a unique semantic cause.</small>
    </article>`
    }
    repair := view.Attachments.Repair
    if repair == nil { return absentPreventionGuidancePanel }
    guidance := repair.Brief.Get("prevention")
    items := append([]jsjson.Value{},guidance.Get("hardEnforcement").Items()...)
    items = append(items,guidance.Get("softGuidance").Items()...)
    return `<article class="card panel"><h3>Prevention guidance</h3>
    <p><span class="pill">INFERRED</span> These are cited recommendations, not a three-state prevention proof.</p>
    <div class="proof"><span>Hard enforcement directions</span><strong>` + count(guidance.Get("hardEnforcement")) + `</strong></div>
    <div class="proof"><span>Soft guidance directions</span><strong>` + count(guidance.Get("softGuidance")) + `</strong></div>
    ` + citationSummary(items) + `
    <small>Free-form prevention text remains in the private repair artifact. A repaired state must still be executed under the same frozen witness and packaged via <code>fl prevention write --from-bundle</code> for Prevention verified.</small>
  </article>`
}
```

The prevention `heading/pill/summary` strings are fixed source constants, not attacker-provided HTML. No JSON dump, filesystem access or free-form statement projection. Rename old `attachmentSummary` to `absentAttachmentSummary` unchanged, then define the local summary at the start of `RenderGitProofIncidentPage`:

```go
attachmentSummary := absentAttachmentSummary
if view.Attachments.Minimization != nil || view.Attachments.Repair != nil {
    attachmentSummary = "It separately verifies each supplied downstream artifact and its applicable evidence binding before rendering it."
}
```

Replace the three old panel-constant concatenations with `minimizationPanel(view)`, `repairPanel(view)`, `preventionGuidancePanel(view)` respectively; leave chrome/CSS/layout/footer/newline bytes untouched.

### 3B. Privacy, escaping and replay

- [x] Add real-fixture private sentinel assertions independent of full byte comparisons. Then pure-render synthetic View tests insert `<>&\"'`, astral characters and lone-surrogate WTF-8 into each projected string field (including title attributes and digest/commit slices); compare exact escaped bytes. These are renderer-defense tests, not schema-valid fixture claims. Citation direct test includes E1/E10/E2 duplicates and a Unicode pair distinguishing UTF-16 from UTF-8/ICU order. Assert invariant original reference count versus deduplicated summary, both minimality/proof states, both prevention classifications, precedence and prevention-only hero quirk.
- [x] Add `//go:build faultline_test` permanent tests. `TestServeBundleMetadata` validates the entire corpus; `TestServeBundleReplay` executes every `view` case through the direct loader and compares full JSON/error/page. This test owns library/panel replay only: CLI cases are validated as retained evidence here but are not executed or reported as feature passes before Task 4. Task 4 adds the separate `TestServeBundleCLIReplay` in this file, executing every `cli-error`/`cli-serve` row through actual `flBinary`; no skip or premature CLI acceptance. Construct actual View JSON in TS field order using `jsjson.MustObject`, including `attachments` with all three null-or-record fields; manifest/investigation/frozen objects are schema-parsed ordered values. Artifact records contain exactly the fields in the public type definitions and TS capture; `ValueJSON` comparison uses JS serialization, not `encoding/json` or an unordered map.
- [ ] Run separately: `go test ./internal/proofview -run '^(TestAttachmentPanels|TestPreviewOfCommittedSample|TestEscapeHTML|TestShortDigest)' -count=1 -timeout=120s`; `go test -tags faultline_test ./difftest -run '^TestServeBundle(Metadata|Replay)$' -count=1 -timeout=180s -v`. When TestMain supplies an instrumented binary preserve its required GOCOVERDIR. Expected: all new pages exact, old judge/preview replay unchanged; no Node preflight or runtime TS dependency.
- [ ] Run the existing permanent judge-preview corpus selector separately through RUN: `go test -tags faultline_test ./difftest -run '^TestDemoArtifactsReplay$' -count=1 -timeout=180s -v`; `TestPreviewOfCommittedSample` is already in the direct regression command above. This is regression evidence for the new rendering delta, not reopening completed 5b.1 task reviews. If the complete replay exceeds the local budget retain the incomplete receipt and require its full hosted result. Snapshot/diff and obtain independent task spec/quality review. No commit.

## Task 4: Implement exact bundle CLI and native CLI/HTTP/signal tests

**Files:** Create `internal/cli/{serve_bundle,serve_bundle_wait,serve_bundle_test}.go` (tests in `_test.go`); modify dispatch in `internal/cli/cli.go`. After Task 3 is accepted, add `TestServeBundleCLIReplay` and `TestServeBundleHTTP` to `difftest/serve_bundle_test.go`; retain its existing View replay/metadata tests unchanged. Create `difftest/servefixture/{process,http,process_unix,process_windows}.go`, `console-helper/main_windows.go`, `difftest/serve_bundle_live_test.go`, `serve_bundle_signals_{unix,windows}_test.go`, `difftest/release/serve_bundle_test.go`.

**Interfaces:** Consumes `proofview.LoadVerifiedGitProofViewWithAttachments`, `AttachmentOptions`, existing `StartGitProofServer(View,float64)(*Server,error)`, `Server.URL()/Close()`, `hasFlag/option/requiredOption/optionPointer`, `env.out`. Produces `serveBundleCommand(e *env,args []string) error`, `awaitBundleInterrupt(close func() error)`; no browser/timer/public test command. Test-support process/HTTP API:

```go
type Started struct { URL string; Cmd *exec.Cmd }
func Start(ctx context.Context, executable string, prefix, argv, env []string, cwd string) (*Started,error)
func (p *Started) Interrupt(ctx context.Context, consoleHelper string) error
func (p *Started) Terminate(ctx context.Context) error
func (p *Started) Wait(ctx context.Context) (ProcessResult,error)
func (p *Started) Dispose() error
func CheckHTTP(ctx context.Context, url string, page []byte) error
```

Store synchronized stdout/stderr collectors, wait channel, raw clients and platform ownership as private `Started` fields. `Start` reads exactly two complete LF lines, validates actual bound URL, retains both output streams and fails on startup exit/extra line/wrong host/invalid port. `Dispose` closes only owned clients, terminates a still-running owned child, joins it and retains evidence; normal assertions happen before teardown. No background service survives a test. Context timeouts are test deadlines, never new CLI semantics.

### 4A. Parser precedence red→green

The handler and wait are one compile-complete production change, not an undefined wait symbol or a throwaway production stub. Before editing either production file, write the parser tests here and the actual-process ordinary/repeated interrupt tests specified in 4B using Task 3's retained pages. Run both selections against the existing not-ported binary and record meaningful refusal/startup failure. Then add the handler plus 4B's wait together and rerun both selections. Later specialized signal refinements require their own observed failing behavior before correction.

- [ ] Add exact direct CLI failure tests before dispatcher changes. They must exercise existing `Run` and full formatting; one concrete test:

```go
func TestServeBundleParsesAllValuesBeforePairs(t *testing.T) {
    var out,errOut bytes.Buffer
    code := Run([]string{"serve","--bundle","missing","--minimization","result.json","--expect-repair"},&out,&errOut)
    want := "FaultLine error: Missing required option: --expect-repair\nNext: pnpm fl help\n"
    if code != 1 || out.String() != "" || errOut.String() != want {
        t.Fatalf("exit=%d stdout=%q stderr=%q",code,out.String(),errOut.String())
    }
}
```

- [ ] Run `go test ./internal/cli -run '^TestServeBundle' -count=1 -timeout=90s -v` through RUN; expected red is `not yet ported: serve`, not a fixture/import error. Add only this dispatcher branch before recognized-but-unported fallback:

```go
case command == "serve" && hasFlag(rest,"--bundle"):
    return serveBundleCommand(e,rest)
```

Plain serve and only `--bundle=value` still return exactly the prior explicit not-ported failure. No unknown-option validation or general parser.

- [x] Implement the handler in source order:

```go
func serveBundleCommand(e *env,args []string) error {
    raw,err := requiredOption(args,"--bundle"); if err != nil { return err }
    directory := nodefs.Resolve(raw)
    flags := []string{"--minimization","--repair","--prevention","--expect-minimization","--expect-repair","--expect-prevention"}
    values := make([]*string,len(flags))
    for i,flag := range flags {
        if hasFlag(args,flag) {
            value,err := requiredOption(args,flag); if err != nil { return err }
            values[i] = &value
        }
    }
    pairErrors := []string{
        "A shareable minimization attachment requires both --minimization <result.json> and --expect-minimization <retained-digest>.",
        "A shareable repair attachment requires both --repair <repair-brief-directory> and --expect-repair <retained-artifact-digest>.",
        "A shareable prevention attachment requires both --prevention <prevention-proof-directory> and --expect-prevention <retained-root-digest>.",
    }
    for i := 0; i < 3; i++ {
        if (values[i] == nil) != (values[i+3] == nil) { return fail("%s",pairErrors[i]) }
    }
    for i := 0; i < 3; i++ {
        if values[i] != nil { absolute := nodefs.Resolve(*values[i]); values[i] = &absolute }
    }
    v,err := proofview.LoadVerifiedGitProofViewWithAttachments(directory,optionPointer(args,"--expect-root"),proofview.AttachmentOptions{
        MinimizationFile:values[0],ExpectedMinimizationDigest:values[3],
        RepairDirectory:values[1],ExpectedRepairDigest:values[4],
        PreventionDirectory:values[2],ExpectedPreventionDigest:values[5],
    })
    if err != nil { return err }
    port,ok := option(args,"--port"); if !ok { port = "4173" }
    server,err := proofview.StartGitProofServer(v,jsjson.ToNumber(port)); if err != nil { return err }
    e.out("FaultLine read-only Git proof page: "+server.URL()+"\nPress Ctrl+C to stop.\n")
    awaitBundleInterrupt(server.Close)
    return nil
}
```

- [ ] Re-run parser selector green. Extend table with every Task 1 CLI option/port/precedence case, including pair failures before I/O and bundle/attachment errors before occupied port; run failure cases through actual binary too. An inherited `--serve-ms`/browser/output option must be ignored, not validated or acted on. Missing port value defaults; `--expect-root` remains raw `optionPointer` behavior.

### 4B. SIGINT-only wait and actual process boundaries

- [ ] Add actual-process signal tests before wait implementation. Use explicit accepted/startup/listener-closed/socket-closed barriers and owned clients, not artificial sleeps or load. Implement one signal listener only, following established boot/sigexit coordination:

```go
func awaitBundleInterrupt(close func() error) {
    signals := make(chan os.Signal,8)
    signal.Notify(signals,os.Interrupt)
    sigexit.Intercept(syscall.SIGINT)
    <-signals
    go func() {
        <-signals
        sigexit.Die(syscall.SIGINT)
    }()
    _ = close()
}
```

Do not subscribe to SIGTERM, use `awaitServeInterrupt`, install a timer, open a browser or print a stop line. First SIGINT is consumed once; second during held Close uses the existing default-action helper. Native Windows comparison below must verify the actual default-action exit rather than assuming Unix status. If that platform's existing helper differs from Node, keep the correction confined to this command's platform wait/death helper and its failing actual-process test; do not waive it as an old judge gap or change generic signal behavior without review.

- [ ] Unix process helper owns its child group, sends signals to the intended process/group per case and records raw wait signal plus exit code separately. Cover normal SIGINT → successful clean return; held Close + second SIGINT → source default death; SIGTERM without prior SIGINT → default death; held Close then SIGTERM → default death; inherited SIG_IGN/blocked signal launch conditions under existing eligible cgo/kernel mechanics. Do not equate shell `128+sig` with a raw signal death. Existing boot behavior is reused, not reimplemented.
- [ ] Windows uses a real isolated console: launch the target with `CREATE_NEW_CONSOLE` (`0x10`), not `CREATE_NEW_PROCESS_GROUP` alone because that changes Ctrl+C eligibility. Build the test-only `console-helper/main_windows.go` natively. It frees its own console, attaches only to the target's new console using PID, ignores Ctrl+C in the emitter itself via `SetConsoleCtrlHandler(NULL,TRUE)`, then calls `GenerateConsoleCtrlEvent(CTRL_C_EVENT,0)`. It checks every API result and exits; the test host never detaches or broadcasts to a shared runner console. Send a second event only after observing listener closure while the owned active client remains held. Native TS and Go raw Windows exit statuses/complete streams must agree; `os.Process.Signal(os.Interrupt)` is not supported Windows evidence. Termination uses an owned process handle/TerminateProcess and compares the actual Node child-kill semantics; do not pretend Windows received a POSIX SIGTERM. No Windows skip substitutes for this evidence.

The Windows helper is a test executable, not a production environment override. Its actual core is:

```go
kernel := syscall.NewLazyDLL("kernel32.dll")
free := kernel.NewProc("FreeConsole")
attach := kernel.NewProc("AttachConsole")
ignore := kernel.NewProc("SetConsoleCtrlHandler")
emit := kernel.NewProc("GenerateConsoleCtrlEvent")
if ok,_,err := free.Call(); ok == 0 && err != syscall.Errno(6) { panic(err) } // ERROR_INVALID_HANDLE: emitter had no console.
if ok,_,err := attach.Call(uintptr(pid)); ok == 0 { panic(err) }
if ok,_,err := ignore.Call(0,1); ok == 0 { panic(err) }
if ok,_,err := emit.Call(0,0); ok == 0 { panic(err) }
```

Parse `pid` from a required positive decimal argv, not an inherited arbitrary environment target. Caller builds helper with native `go build -o <owned-path> ./difftest/servefixture/console-helper` on Windows only; test timeout owns target/emitter cleanup. Record console API error/status rather than silently falling back to Ctrl+Break or forced termination for SIGINT.

### 4C. Portable HTTP, snapshot and standalone contracts

- [x] Add `TestServeBundleCLIReplay`, executing all `cli-error` and `cli-serve` rows with the actual tagged `flBinary` and Task 1 corpus. Assert full streams/exit for errors, full startup/page/completed SIGINT streams/exit for servers, and nonzero executed counts for both kinds; preserve TestMain's GOCOVERDIR. Add portable direct `TestServeBundleHTTP` using the same retained pages and existing real server.
- [ ] Implement one portable HTTP verifier and use it from direct native server tests, actual CLI tests and release tests. GET `/`, query root and normalized path variants get exact full page; HEAD/POST/OPTIONS/unknown paths and `/api/analysis`, `/api/rerun`, `/manifest.json`, `/download` get exact source status/body (HEAD body empty). Check every application/security header and HTTP framing/connection headers; validate Date parse/format/presence before ignoring only its value. Raw TCP cases cover HTTP/1.0, close/keepalive, OPTIONS `*`, Expect continue/failure, chunked bodies, malformed framing/header limits and existing narrow request-target exceptions.
- [ ] Test render-once snapshot: copy a valid coherent fixture, launch actual CLI, fetch expected bytes, change/remove copied inputs, fetch again and require identical full page. Prove no mutation/analysis/rerun route and no repository code execution with distinctive fixture scripts/markers that must not run. Close idle/silent/partial-header/incomplete-body clients separately; do not force server Close or add a production deadline to avoid a hang. Release owned held clients before normal teardown, wait for process exit and retain stdout/stderr/raw HTTP evidence.
- [ ] Add `TestLiveServeBundle` (portable `faultline_test`, no Unix-only file guard) and `TestLiveServeBundleSignals` platform files. Require explicit oracle enablement under existing eligibility, then pin actual Node/manifest/CLI version. Run concrete TS and Go command paths on identical retained fixture copies, compare full output/page/HTTP and platform-specific failure strings. No Node-availability skip. Platform diagnostics stay source/oracle-derived on their native runner; do not compare Windows against Linux errno prose.
- [ ] Add untagged `TestServeBundleStandalone` in release tests using the actual untagged binary outside the repository. Controlled PATH excludes Node/npm/pnpm/tsx while retaining real trusted Git and necessary trusted OS/Git tools. Resolve trusted Git first, verify every allowed PATH entry lacks Node launchers, retain trusted Git support directories and verify a real bundle; a fake Git or empty PATH is not this contract. Runtime cwd has no source/dist/testdata/package manifest; fixture bytes are copied as explicit inputs. Request full pages for all combinations, exercise invalid attachment-before-listen, ordinary interrupt and no timer/browser/output side effects. Compare tagged/unmarked release behavior under existing release-override tests.
- [ ] Run separately through RUN: CLI selector (`90s` test timeout), `go test -tags faultline_test ./difftest -run '^TestServeBundle(Metadata|Replay|CLIReplay|HTTP)$' -count=1 -timeout=180s -v`, live selectors with oracle enabled (`180s` test timeout, only bounded case subsets locally), and `go test ./difftest/release -run '^TestServeBundleStandalone$' -count=1 -timeout=180s -v`. Windows console tests run natively on both Windows architectures in hosted CI; local cross-build is only compile evidence. Capture unavailable local platform/runtime as pending, not passed. Obtain independent task spec/quality review, including process ownership and shutdown correctness. No commit.

## Task 5: Register exact gates, close call-site parity and review the whole branch

**Files:** Create `difftest/serve_bundle_stack_live_test.go`; modify `.github/workflows/go.yml`, existing skip-check registrations and coverage tables only where needed, `difftest/KNOWN_DIFFERENCES.md` to distinguish shared transport from command lifecycle, roadmap/spec/plan completion status. Stack-profile correction, if demanded by the actual oracle test, may touch `internal/jsjson/stack.go` and the narrow existing verifier call-site plumbing; no unrelated semantics or broad normalization.

**Interfaces:** Consumes all prior task selectors, corpus generator and process helpers. Produces `TestLiveServeBundleStackCallSites`, hosted regeneration/native registrations, exact error-literal reachability and final acceptance ledger. It does not manufacture a new release or inherit PR #216's completed CI.

### 5A. Actual error-call-site and positional coverage

- [ ] Write an actual CLI deep-schema comparison before any stack-model adjustment. Use copied coherent inputs and the new command argv, independently corrupt minimization, repair manifest/packet/brief and prevention body/manifest at a schema-literal position. Use the existing four shape constructors (array/object/index-key/alternating) and deterministic depth bisection from `stack_live_test.go`, but call actual `serve --bundle` with paired retained attachment digests. A verifier schema failure must win over digest/port errors. Measure exact full stdout/stderr/exit where stable and explicit threshold transitions, not an added `unstableTolerance` or broad RangeError mask. Repair extra-read schema/canonical failures also get private seam tests; do not manufacture a filesystem race to reach them.
- [ ] Run bounded individual site/shape probes locally and the full selector on existing hosted stack-native runners. First record the real mismatch, including Node/version/platform/argv/depth; only then adjust call-site handling if required. Reuse the existing `jsjson.UseCallSite/AtCallSite` mechanism, keep ordinary verifier entry points on their existing sites, and share the verifier body. Derive numeric offsets from actual same-command four-shape measurements; do not copy the `repair verify` row or invent a constant. Add exact boundary regressions and rerun both old and new call-site suites. Any new internal call-site wrapper is private/typed, production fixed and independent of test environment; no mutable release-global override.
- [x] Add the new `serve-bundle/golden.jsonl` to `goldenText`'s fixed file list in `coverage_test.go`; ordinary attachment/pair failures must be retained TS CLI captures and executed through the instrumented `flBinary`, not merely direct loader calls. Task 4's `TestServeBundleCLIReplay` owns actual `cli-serve` replay: fetch full page and complete ordinary SIGINT exit so coverage counters flush. Preserve `GOCOVERDIR` and do not count signal-killed children as normal-exit coverage.
- [x] Handle race/library-only sites using the existing exact-text `unreachableLiterals` mechanism, never a whole-file `liveGatedFiles` entry, scanner exclusion or package gate removal. The source proof and mandatory direct test for each new candidate are fixed below. Put these tests in Task 2's `attachments_recheck_test.go`; test names are part of the Task 2 handoff. Add a table entry only if the scanner actually discovers that literal and the retained TS captures do not produce it:

| Exact literal | Why no stable shipped-CLI fixture reaches it / required direct test |
| --- | --- |
| `Refusing to attach a minimization record that changed during verification: ` | Same real verifier/path/options runs twice; a differing valid parsed result/digest or second invalid result requires change between passes. `TestAttachmentMinimizationRecheck` stages invalid, digest and canonical-result changes, plus final-status return |
| `Refusing to attach repair guidance that changed during verification: ` | First accepted manifest and parsed contents are compared after two later real verifications; mutation/read failure between boundaries is required. `TestAttachmentRepairRecheck` stages each manifest, packet, brief and final-content condition independently |
| `Refusing to attach a prevention proof that changed during verification: ` | Same artifact-returning verifier/path/options runs twice; differing root/body or later invalid result requires change between passes. `TestAttachmentPreventionRecheck` proves root/body/invalid rejection and second-object return |
| `Refusing to attach an invalid repair brief payload: ` | Unchanged packet/brief already passed the same schemas and citation validator inside `VerifyBriefArtifact`; the adapter's later validation can fail only after changed content or an explicitly staged dependency. `TestAttachmentRepairReadValidation` directly calls the real reader on a forged invalid brief and pins the wrapper |
| `Refusing to attach repair guidance whose in-memory payload does not match its verified manifest.` | For an unchanged package the real verifier has already matched those same canonical contents; requires change between verify/read or a staged mismatch. `TestAttachmentRepairFirstSnapshotMismatch` pins the initial check |
| `Prevention attachment requires the verified Git proof root digest.` | Library guard only: both shipped public View loaders require successful base root before calling `loadAttachments` with its address; no CLI path passes nil. `TestAttachmentPreventionRequiresBaseRoot` calls the private orchestrator with nil and distinguishes expected-digest-only/no-path |

The existing `Unable to read verified` row stays only while no actual capture produces it. The fallback literals `digest changed`, `manifest changed`, `artifact contents changed` receive separate exact reasons naming the corresponding rows/tests if the scanner discovers them; their spelling alone is not a reachability proof. For null/endpoints or other binding diagnostics precluded by an earlier verifier, retain the actual TS first refusal and document that exact schema/semantic predicate with its direct validator test before adding a specific row. No guessed collective exemption. A genuinely reachable text must have an actual CLI replay instead.

- [x] Run both `TestGoldensReachEveryErrorLiteral` (also detects stale `unreachableLiterals`) and `TestUnreachableSitesTable` separately before the full coverage gate. Remove a stale row only after a real new captured case proves its claim false. Leave `unreachableSites` and its cardinality checks unchanged unless an actual duplicate-position proof, not a race-only text, requires a separately justified exact entry. Do not place `coverage gate: race only` on ordinary error sites. These are applications of existing per-literal reachability rules with mandatory tests, not waivers of any reachable position or a reduction of the unnarrowed Ubuntu gate.

### 5B. CI and native bindings

- [ ] Register the dedicated generator in the existing goldens job before its final no-drift check. Run no-write recapture against retained inputs; compare all new files and retain previous corpus byte hashes. Register TypeScript fixture/case tests explicitly without altering protected package scripts. `TestLiveServeBundle*` must be discovered by the actual live selector, and portable native HTTP/standalone selectors must run on linux/darwin/windows × amd64/arm64. Add an explicit six-native live substep/job where the existing live job's platform exclusions would otherwise omit Windows; do not weaken unrelated existing exclusions or claim their skips as 5c evidence.
- [ ] Native workflow records six bindings `(runner OS/arch, actual go env GOOS/GOARCH, Node version, exact checked-out head, actual binary hash, new test counts/no unexpected skips)`. Windows console helper is built and run natively on both architectures. Actual TS CLI capture on each platform is retained as run artifacts with case/fixture inventory and full outputs. Only portable diagnostics are in the universal permanent corpus; platform-specific evidence has a native platform identity. Standalone mode proves Node absent while trusted Git remains, not just an unset variable.
- [ ] Preserve release no-test-override checks, original strict skip checker expectations and the unnarrowed Ubuntu amd64 coverage invocation. Keep existing fault/stack/native/Docker jobs and security/freeze guard unchanged in strength. Existing unrelated Windows recorder/PEM/browser and Docker applicability gaps remain qualified 5i/program obligations; none can skip this slice's supported paths. Do not add a fake successful HTTP or native marker.
- [ ] Local integration payloads are separate bounded receipts: affected untagged packages; permanent replay; release/no-override checks; generator check; pinned live subset; both coverage tables; `go vet` on affected packages; module no-drift; protected/prior corpus/source invariant. Full `go test ./...`, full tagged/live/stack/fault/native and positional suites run on hosted CI. Preserve a timed-out local attempt even if hosted execution later passes.

### 5C. Review, publication boundary and completion

- [x] Controller packages the whole working-tree delta from baseline plus newly created files and immutable fixture inventories (not `HEAD~1`, not tracked-only diff), after task reviews pass. Dispatch one independent whole-branch review at the required architecture/security-capable tier with spec, plan, full diff and completed task evidence. Reviewers remain Read/own-report Write/Edit only; no Bash or runtime. Resume original implementer/reviewer for scoped fixes; one coherent final fix wave and scoped recheck, not repeated broad reviews or speculative agents. Preserve reviewed hashes.
- [ ] Run final affected verification after fixes and recheck exact reviewed code/fixtures/protected paths/index/identity. Update factual documentation/ledger status; implementation checkboxes become checked only with actual execution evidence. Record what is locally verified versus hosted pending. The subsequent human instruction already supplies publication/CI/normal-merge authority; do not request it again.
- [ ] Use only verified personal wrapper/origin and normal TruffleHog/hook for authorized publication. Stop and report any push failure without fallback. Retain exact-head automatic push/PR workflows and every required job log/skip/artifact. Investigate actionable CI failures within authorized scope, add meaningful regression red→green, have scoped fix review and re-run the changed exact head; do not merely announce the failure or inherit old-head greens. Merge normally only after actual exact-head readiness; no admin bypass, public release or evidence cleanup.
- [ ] Completion requires all Task 1–4 contracts, permanent/live agreement, real-verifier hostile plus deterministic recheck tests, six-native standalone/CLI/HTTP/Windows-console evidence, full exact-head hosted matrices and unnarrowed Ubuntu positional gate, and accepted independent task/whole-branch review. While hosted gates are pending report **local implementation complete; hosted acceptance pending**, not 5c complete. Continue remaining Slice 5 work under the user's standing instruction, preserving each later scope/security boundary; no premature all-Slice-5 or release claim.

## Controller self-review before handoff

| Approved spec requirement | Plan owner / verification |
| --- | --- |
| Scope/architecture, old APIs, no second server | Global constraints; Tasks 2–4 interfaces and file barriers |
| Base 3-verify/2-read, parsed binding, original external status | Task 2B plus base integration regression; private old callers updated together |
| Min/repair/prevention links, statuses and rechecks | Task 2B–2D; source-exact sequence/errors; real fixtures plus private staged tests |
| Rendering/privacy/UTF-16/counts/summary quirks | Task 3A–3B; full TS pages, independent sentinel/escaping tests |
| CLI precedence/port/startup/plain refusal | Task 1 case matrix and Task 4A; complete handler code and actual binary tests |
| HTTP snapshot/security/Close/signal distinctions | Task 4B–4C; actual process/raw TCP; Windows isolated console native ownership |
| Exact metadata/additive capture/no-write replay | Task 1; strict corpus API and actual TS captures; Task 5 CI registration |
| Stack-error call sites/coverage/no overrides | Task 5A; actual new CLI probes before measured correction; both existing coverage tables |
| Six-native/Node-free/trusted Git/hosted coverage | Tasks 4C/5B–5C; explicit platform/runtime provenance and no gate inheritance |
| TDD/reviews/authorization/evidence retention | Global constraints and every task's review gate; Task 5 final boundary |

- [x] Check each approved spec section against the ownership table, including source asymmetries and Windows mechanics.
- [x] Scan for incomplete instructions and undefined cross-task interfaces; fix directly rather than dispatch another plan review.
- [x] Check every new type/function consumed later against its producing task and the actual existing API names.
- [x] Verify documentation-only diff, local links, source-matched literals, exact metadata, protected/prior corpus hashes and empty index with the bounded checker; preserve the result in the discovery handoff ledger.
- [x] Prepare the separate implementation-approval handoff, recommending sequential Sonnet task implementation with independent review; offer inline execution as the alternative. No implementation/publication has occurred during planning.

Only this controller planning self-review is checked at the planning checkpoint. The user subsequently explicitly authorized implementation and continued Slice 5 delivery through passing CI and normal merge, without further routine permission prompts. All implementation tasks still require actual execution evidence. Planning evidence, checker failures/corrections and the source-research process deviation remain in the preserved discovery handoff; approved execution is now tracked in this plan's own 5c workspace ledger.
